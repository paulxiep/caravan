"""End-to-end tests for ``caravan_rpc.serve`` (B0 step 1e).

Spins up ``ThreadingHTTPServer`` in a background thread on an ephemeral port,
points ``CARAVAN_RPC_PEERS`` at it, and exercises the full client → wire →
server → impl → wire → client roundtrip with real HTTP. Validates:

- Wire path, version header, bearer auth.
- Pydantic / dataclass roundtrip via the codec.
- Remote exception → ``{"ok": false, "error": ...}`` → ``RpcRemoteError``.
- CLI argument parsing + impl module resolution.
"""

from __future__ import annotations

import contextlib
import json
import os
import socket
import sys
import threading
import time
from dataclasses import dataclass
from http.server import ThreadingHTTPServer

import pytest
from pydantic import BaseModel

from caravan_rpc import RpcRemoteError, client, provide, wagon
from caravan_rpc import _codec, _registry
from caravan_rpc.serve import (
    _make_handler,
    _parse_impl_ref,
    _resolve_interface_and_impl,
)


# ---------------------------------------------------------------------------
# Fixtures shaped like invoice-parse's real types.

@dataclass
class FakeRawOcr:
    text: str = ""


class FakeInvoiceExtraction(BaseModel):
    vendor: str
    total: float


@wagon
class LLMExtraction:
    def extract(
        self, raw_ocr: FakeRawOcr | None = None
    ) -> FakeInvoiceExtraction: ...


class GeminiExtractor:
    def __init__(self) -> None:
        self.calls = 0

    def extract(
        self, raw_ocr: FakeRawOcr | None = None
    ) -> FakeInvoiceExtraction:
        self.calls += 1
        return FakeInvoiceExtraction(
            vendor=f"gemini:{raw_ocr.text if raw_ocr else ''}",
            total=42.0,
        )


class BrokenExtractor:
    def extract(self, raw_ocr: FakeRawOcr | None = None):
        raise ValueError("invoice unparseable")


@pytest.fixture(autouse=True)
def _isolate():
    _registry.clear()
    _codec._clear_cache()
    for v in ("CARAVAN_RPC_PEERS", "CARAVAN_RPC_SHARED_SECRET"):
        os.environ.pop(v, None)
    yield
    _registry.clear()
    _codec._clear_cache()
    for v in ("CARAVAN_RPC_PEERS", "CARAVAN_RPC_SHARED_SECRET"):
        os.environ.pop(v, None)


def _pick_free_port() -> int:
    with contextlib.closing(socket.socket(socket.AF_INET, socket.SOCK_STREAM)) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


@contextlib.contextmanager
def _running_server(interface_cls, *, secret: str | None = None):
    port = _pick_free_port()
    handler_cls = _make_handler(interface_cls, secret)
    server = ThreadingHTTPServer(("127.0.0.1", port), handler_cls)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        # Tiny wait for socket to actually accept.
        for _ in range(50):
            with contextlib.closing(socket.socket()) as probe:
                probe.settimeout(0.05)
                try:
                    probe.connect(("127.0.0.1", port))
                    break
                except OSError:
                    time.sleep(0.01)
        yield port
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)


# ---------------------------------------------------------------------------
# End-to-end HTTP roundtrip.

def test_e2e_real_http_roundtrip():
    impl = GeminiExtractor()
    provide(LLMExtraction, impl)

    with _running_server(LLMExtraction) as port:
        os.environ["CARAVAN_RPC_PEERS"] = json.dumps(
            {"LLMExtraction": {"mode": "http", "url": f"http://127.0.0.1:{port}"}}
        )
        result = client(LLMExtraction).extract(raw_ocr=FakeRawOcr(text="e2e"))

    assert isinstance(result, FakeInvoiceExtraction)
    assert result.vendor == "gemini:e2e"
    assert result.total == 42.0
    # Each request goes over the wire and hits the impl exactly once.
    assert impl.calls == 1


def test_e2e_remote_exception_surfaced_as_rpc_remote_error():
    impl = BrokenExtractor()
    provide(LLMExtraction, impl)

    with _running_server(LLMExtraction) as port:
        os.environ["CARAVAN_RPC_PEERS"] = json.dumps(
            {"LLMExtraction": {"mode": "http", "url": f"http://127.0.0.1:{port}"}}
        )
        with pytest.raises(RpcRemoteError) as exc_info:
            client(LLMExtraction).extract(raw_ocr=FakeRawOcr(text="x"))

    assert exc_info.value.code == "ValueError"
    assert "invoice unparseable" in exc_info.value.message


def test_e2e_bearer_auth_enforced_when_secret_set():
    impl = GeminiExtractor()
    provide(LLMExtraction, impl)
    secret = "test-secret-9b3a"

    with _running_server(LLMExtraction, secret=secret) as port:
        url = f"http://127.0.0.1:{port}"

        # Without bearer — server rejects.
        os.environ["CARAVAN_RPC_PEERS"] = json.dumps(
            {"LLMExtraction": {"mode": "http", "url": url}}
        )
        with pytest.raises(RpcRemoteError) as exc_info:
            client(LLMExtraction).extract()
        assert exc_info.value.code == "Unauthorized"

        # With bearer — server accepts.
        os.environ["CARAVAN_RPC_SHARED_SECRET"] = secret
        result = client(LLMExtraction).extract()
        assert result.vendor == "gemini:"


def test_e2e_wrong_version_header_rejected():
    """Server-side enforcement of the wire-version contract."""
    import urllib.request

    impl = GeminiExtractor()
    provide(LLMExtraction, impl)

    with _running_server(LLMExtraction) as port:
        req = urllib.request.Request(
            f"http://127.0.0.1:{port}/_caravan/rpc/LLMExtraction/extract",
            data=json.dumps({"args": [], "kwargs": {}}).encode("utf-8"),
            headers={
                "Content-Type": "application/json",
                "X-Caravan-Rpc-Version": "99",  # wrong version
            },
            method="POST",
        )
        import urllib.error
        with pytest.raises(urllib.error.HTTPError) as exc_info:
            urllib.request.urlopen(req, timeout=5)
        body = json.loads(exc_info.value.read())
        assert body["error"]["code"] == "BadVersion"


def test_e2e_wrong_interface_path_returns_404_envelope():
    import urllib.error
    import urllib.request

    impl = GeminiExtractor()
    provide(LLMExtraction, impl)

    with _running_server(LLMExtraction) as port:
        req = urllib.request.Request(
            f"http://127.0.0.1:{port}/_caravan/rpc/SomeOtherInterface/extract",
            data=b"{}",
            headers={
                "Content-Type": "application/json",
                "X-Caravan-Rpc-Version": "1",
            },
            method="POST",
        )
        with pytest.raises(urllib.error.HTTPError) as exc_info:
            urllib.request.urlopen(req, timeout=5)
        body = json.loads(exc_info.value.read())
        assert body["error"]["code"] == "InterfaceMismatch"


# ---------------------------------------------------------------------------
# CLI helpers (no subprocess; we call the parsers + resolvers directly).

def test_parse_impl_ref_valid():
    assert _parse_impl_ref("pkg.mod:Class") == ("pkg.mod", "Class")
    assert _parse_impl_ref("a.b.c:D") == ("a.b.c", "D")


def test_parse_impl_ref_rejects_missing_colon():
    with pytest.raises(SystemExit, match="module.path:ClassName"):
        _parse_impl_ref("no_colon_here")


def test_parse_impl_ref_rejects_empty_halves():
    with pytest.raises(SystemExit):
        _parse_impl_ref(":NoModule")
    with pytest.raises(SystemExit):
        _parse_impl_ref("no.class:")


def test_resolve_interface_and_impl_same_module():
    """Default: --interface-module defaults to the impl's module. Mirrors the
    real invoice-parse layout where @wagon LLMExtraction and GeminiExtractor
    live in the same extraction.py file.
    """
    iface, impl_cls = _resolve_interface_and_impl(
        interface_name="LLMExtraction",
        impl_ref=f"{__name__}:GeminiExtractor",
        interface_module=None,
    )
    assert iface is LLMExtraction
    assert impl_cls is GeminiExtractor


def test_resolve_interface_and_impl_rejects_non_wagon():
    with pytest.raises(SystemExit, match="not @wagon-decorated"):
        _resolve_interface_and_impl(
            interface_name="FakeRawOcr",  # a dataclass, not a wagon interface
            impl_ref=f"{__name__}:GeminiExtractor",
            interface_module=None,
        )


def test_resolve_interface_and_impl_unknown_class():
    with pytest.raises(SystemExit, match="NoSuchClass"):
        _resolve_interface_and_impl(
            interface_name="LLMExtraction",
            impl_ref=f"{__name__}:NoSuchClass",
            interface_module=None,
        )


def test_resolve_interface_and_impl_unknown_module():
    with pytest.raises(SystemExit, match="cannot import impl module"):
        _resolve_interface_and_impl(
            interface_name="LLMExtraction",
            impl_ref="nonexistent.pkg.that.does.not.exist:X",
            interface_module=None,
        )
