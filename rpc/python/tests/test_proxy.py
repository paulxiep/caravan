"""Tests for ``client(I)`` dispatch proxy (B0 step 1d).

The load-bearing property is **no-config inertness**: with
``CARAVAN_RPC_PEERS`` unset, ``client(I).method`` must be the registered
impl's bound method itself — no wrapper, no overhead. Tests assert value
equality (``MethodType.__eq__`` compares by ``__func__`` + ``__self__``)
because each ``getattr(obj, name)`` produces a fresh ``MethodType`` instance.

HTTP dispatch is tested by patching ``urllib.request.urlopen`` so we exercise
the encode → wire → decode path without binding a real port. Step 1e's
``serve.py`` will add a subprocess-spawned end-to-end test.
"""

from __future__ import annotations

import io
import json
import os
import urllib.error
from dataclasses import dataclass
from unittest.mock import patch

import pytest
from pydantic import BaseModel

from caravan_rpc import (
    RpcRemoteError,
    RpcTransportError,
    client,
    provide,
    wagon,
)
from caravan_rpc import _codec, _registry


# ---------------------------------------------------------------------------
# Fixtures shaped like invoice-parse's real types.

@dataclass
class FakeRawOcr:
    text: str = ""


@dataclass
class FakeTableExtraction:
    rows: list[list[str]] | None = None


class FakeInvoiceExtraction(BaseModel):
    vendor: str
    total: float


@wagon
class LLMExtraction:
    def extract(
        self,
        raw_ocr: FakeRawOcr | None = None,
        table_extraction: FakeTableExtraction | None = None,
    ) -> FakeInvoiceExtraction:
        ...


class GeminiExtractor:
    def __init__(self):
        self.calls = 0

    def extract(
        self,
        raw_ocr: FakeRawOcr | None = None,
        table_extraction: FakeTableExtraction | None = None,
    ) -> FakeInvoiceExtraction:
        self.calls += 1
        return FakeInvoiceExtraction(
            vendor=f"gemini:{raw_ocr.text if raw_ocr else ''}",
            total=42.0,
        )


@pytest.fixture(autouse=True)
def _isolate():
    _registry.clear()
    _codec._clear_cache()
    # Strip any caravan env vars left by other tests / the host shell.
    for var in ("CARAVAN_RPC_PEERS", "CARAVAN_RPC_SHARED_SECRET"):
        os.environ.pop(var, None)
    yield
    _registry.clear()
    _codec._clear_cache()
    for var in ("CARAVAN_RPC_PEERS", "CARAVAN_RPC_SHARED_SECRET"):
        os.environ.pop(var, None)


# ---------------------------------------------------------------------------
# No-config inertness — the load-bearing property of B0.

def test_inertness_unset_env_returns_bound_method_of_registered_impl():
    """With CARAVAN_RPC_PEERS unset, client(I).method must equal impl.method."""
    impl = GeminiExtractor()
    provide(LLMExtraction, impl)

    proxy_bound = client(LLMExtraction).extract
    impl_bound = impl.extract
    # MethodType equality: same __func__ + __self__.
    assert proxy_bound == impl_bound
    assert proxy_bound.__self__ is impl


def test_inertness_unset_env_call_goes_to_impl_directly():
    impl = GeminiExtractor()
    provide(LLMExtraction, impl)

    raw = FakeRawOcr(text="invoice text")
    result = client(LLMExtraction).extract(raw_ocr=raw)
    assert isinstance(result, FakeInvoiceExtraction)
    assert result.vendor == "gemini:invoice text"
    assert impl.calls == 1


def test_inertness_mode_inproc_explicit_returns_bound_method():
    """Even with CARAVAN_RPC_PEERS set to inproc, no wrapper is inserted."""
    impl = GeminiExtractor()
    provide(LLMExtraction, impl)
    os.environ["CARAVAN_RPC_PEERS"] = json.dumps({"LLMExtraction": {"mode": "inproc"}})

    proxy_bound = client(LLMExtraction).extract
    impl_bound = impl.extract
    assert proxy_bound == impl_bound
    assert proxy_bound.__self__ is impl


def test_inertness_other_interfaces_in_table_dont_affect_us():
    """If the env var lists another interface but not ours, ours stays inproc."""
    impl = GeminiExtractor()
    provide(LLMExtraction, impl)
    os.environ["CARAVAN_RPC_PEERS"] = json.dumps(
        {"OtherSeam": {"mode": "http", "url": "http://other:8080"}}
    )

    proxy_bound = client(LLMExtraction).extract
    assert proxy_bound == impl.extract


def test_proxy_rejects_non_seam_method():
    """Strict: only @wagon-declared methods are dispatchable."""
    provide(LLMExtraction, GeminiExtractor())
    with pytest.raises(AttributeError, match="not_a_method"):
        _ = client(LLMExtraction).not_a_method


def test_proxy_rejects_non_wagon_interface():
    class NotAnInterface:
        def extract(self) -> int: ...

    with pytest.raises(TypeError, match="not @wagon-decorated"):
        client(NotAnInterface)


def test_proxy_inproc_without_provide_raises_clear_error():
    """Calling client(I).method with no impl registered should LookupError, not crash."""
    with pytest.raises(LookupError, match="LLMExtraction"):
        _ = client(LLMExtraction).extract


# ---------------------------------------------------------------------------
# HTTP dispatch — encode → urlopen → decode.

def _fake_urlopen(response_body: dict, status: int = 200):
    """Build a fake urlopen() context manager returning ``response_body`` as JSON."""

    class _FakeResp:
        def __init__(self, body):
            self._body = body

        def __enter__(self):
            return self

        def __exit__(self, *exc):
            return False

        def read(self):
            return self._body

    def _opener(req, timeout=None):
        # Stash the captured request on the opener for assertions.
        _opener.captured.append(req)
        return _FakeResp(json.dumps(response_body).encode("utf-8"))

    _opener.captured = []
    return _opener


def test_http_mode_encodes_request_and_decodes_response():
    impl = GeminiExtractor()
    provide(LLMExtraction, impl)  # not used in http mode, but must be set for inertness fallback safety
    os.environ["CARAVAN_RPC_PEERS"] = json.dumps(
        {"LLMExtraction": {"mode": "http", "url": "http://llm-extractor:8080"}}
    )

    fake_response = {
        "ok": True,
        "result": {"vendor": "remote-vendor", "total": 99.5},
    }
    opener = _fake_urlopen(fake_response)

    with patch("caravan_rpc._proxy.urllib.request.urlopen", side_effect=opener):
        result = client(LLMExtraction).extract(
            raw_ocr=FakeRawOcr(text="http test"),
            table_extraction=None,
        )

    assert isinstance(result, FakeInvoiceExtraction)
    assert result.vendor == "remote-vendor"
    assert result.total == 99.5

    # The impl was NOT called on this side — dispatch went over wire.
    assert impl.calls == 0

    # Inspect the request that was sent.
    captured = opener.captured
    assert len(captured) == 1
    req = captured[0]
    assert req.full_url == "http://llm-extractor:8080/_caravan/rpc/LLMExtraction/extract"
    assert req.get_method() == "POST"
    assert req.headers["Content-type"] == "application/json"
    assert req.headers["X-caravan-rpc-version"] == "1"

    body = json.loads(req.data)
    assert body["args"] == []
    assert body["kwargs"]["raw_ocr"] == {"text": "http test"}
    assert body["kwargs"]["table_extraction"] is None


def test_http_mode_injects_bearer_when_secret_set():
    provide(LLMExtraction, GeminiExtractor())
    os.environ["CARAVAN_RPC_PEERS"] = json.dumps(
        {"LLMExtraction": {"mode": "http", "url": "http://x:1"}}
    )
    os.environ["CARAVAN_RPC_SHARED_SECRET"] = "test-secret-xyz"

    opener = _fake_urlopen({"ok": True, "result": {"vendor": "v", "total": 0.0}})
    with patch("caravan_rpc._proxy.urllib.request.urlopen", side_effect=opener):
        client(LLMExtraction).extract()

    req = opener.captured[0]
    assert req.headers["Authorization"] == "Bearer test-secret-xyz"


def test_http_mode_omits_bearer_when_secret_unset():
    provide(LLMExtraction, GeminiExtractor())
    os.environ["CARAVAN_RPC_PEERS"] = json.dumps(
        {"LLMExtraction": {"mode": "http", "url": "http://x:1"}}
    )

    opener = _fake_urlopen({"ok": True, "result": {"vendor": "v", "total": 0.0}})
    with patch("caravan_rpc._proxy.urllib.request.urlopen", side_effect=opener):
        client(LLMExtraction).extract()

    req = opener.captured[0]
    assert "Authorization" not in dict(req.headers)


def test_http_mode_remote_error_envelope_raises_rpc_remote_error():
    provide(LLMExtraction, GeminiExtractor())
    os.environ["CARAVAN_RPC_PEERS"] = json.dumps(
        {"LLMExtraction": {"mode": "http", "url": "http://x:1"}}
    )

    fake_response = {
        "ok": False,
        "error": {"code": "ValueError", "message": "bad invoice"},
    }
    opener = _fake_urlopen(fake_response)
    with patch("caravan_rpc._proxy.urllib.request.urlopen", side_effect=opener):
        with pytest.raises(RpcRemoteError) as exc_info:
            client(LLMExtraction).extract()
    assert exc_info.value.code == "ValueError"
    assert exc_info.value.message == "bad invoice"


def test_http_mode_http_5xx_raises_transport_error():
    provide(LLMExtraction, GeminiExtractor())
    os.environ["CARAVAN_RPC_PEERS"] = json.dumps(
        {"LLMExtraction": {"mode": "http", "url": "http://x:1"}}
    )

    def _raise_500(req, timeout=None):
        raise urllib.error.HTTPError(
            req.full_url, 500, "Internal Server Error", {}, io.BytesIO(b"not json")
        )

    with patch("caravan_rpc._proxy.urllib.request.urlopen", side_effect=_raise_500):
        with pytest.raises(RpcTransportError, match="HTTP 500"):
            client(LLMExtraction).extract()


def test_http_mode_http_5xx_with_error_envelope_promoted_to_remote_error():
    """If 5xx body still carries the {"ok": false, "error": {...}} envelope,
    surface it as RpcRemoteError (the server intentionally signaled a remote
    failure), not RpcTransportError (which means transport/infra broke).
    """
    provide(LLMExtraction, GeminiExtractor())
    os.environ["CARAVAN_RPC_PEERS"] = json.dumps(
        {"LLMExtraction": {"mode": "http", "url": "http://x:1"}}
    )

    err_body = json.dumps(
        {"ok": False, "error": {"code": "RuntimeError", "message": "remote boom"}}
    ).encode("utf-8")

    def _raise_500_with_body(req, timeout=None):
        raise urllib.error.HTTPError(
            req.full_url, 500, "Internal Server Error", {}, io.BytesIO(err_body)
        )

    with patch("caravan_rpc._proxy.urllib.request.urlopen", side_effect=_raise_500_with_body):
        with pytest.raises(RpcRemoteError) as exc_info:
            client(LLMExtraction).extract()
    assert exc_info.value.code == "RuntimeError"


def test_http_mode_url_error_raises_transport_error():
    provide(LLMExtraction, GeminiExtractor())
    os.environ["CARAVAN_RPC_PEERS"] = json.dumps(
        {"LLMExtraction": {"mode": "http", "url": "http://x:1"}}
    )

    def _refuse(req, timeout=None):
        raise urllib.error.URLError("Connection refused")

    with patch("caravan_rpc._proxy.urllib.request.urlopen", side_effect=_refuse):
        with pytest.raises(RpcTransportError, match="Connection refused"):
            client(LLMExtraction).extract()


# ---------------------------------------------------------------------------
# Lambda mode (M7). client(I).method returns a SigV4-signed dispatcher.
# We don't make a real round-trip here; just verify the dispatcher is built
# (vs. the M2/B0-era NotImplementedError stub it replaced).

def test_lambda_mode_returns_sigv4_dispatcher():
    pytest.importorskip("botocore")
    provide(LLMExtraction, GeminiExtractor())
    os.environ["CARAVAN_RPC_PEERS"] = json.dumps(
        {
            "LLMExtraction": {
                "mode": "lambda",
                "function_url": "https://abc.lambda-url.us-east-1.on.aws/",
            }
        }
    )
    dispatcher = client(LLMExtraction).extract
    assert callable(dispatcher)
    # Naming preserved for tracebacks.
    assert dispatcher.__name__ == "LLMExtraction.extract"


# ---------------------------------------------------------------------------
# Malformed env var.

def test_malformed_peers_env_var_raises_at_proxy_construction():
    provide(LLMExtraction, GeminiExtractor())
    os.environ["CARAVAN_RPC_PEERS"] = "this is not json"
    with pytest.raises(RuntimeError, match="CARAVAN_RPC_PEERS must be valid JSON"):
        client(LLMExtraction)
