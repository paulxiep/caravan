"""``client(I)`` dispatch proxy (B0 step 1d).

When ``CARAVAN_RPC_PEERS`` is unset OR the interface name isn't in the table,
``client(I).method`` returns the registered impl's bound method directly with
no wrapping (no-config inertness). When the env var carries a peer entry,
``client(I).method`` returns a callable that encodes args, POSTs the wire
envelope, and decodes the response.

Peer table shape (per docs/poc_rpc_sdk.md and docs/ir.md):

    {
      "LLMExtraction": {"mode": "inproc"},
      "Embedder":      {"mode": "http",   "url": "http://embedder:8080"},
      "Fraud":         {"mode": "lambda", "function_url": "https://..."}
    }
"""

from __future__ import annotations

import json
import os
import urllib.error
import urllib.request
from typing import Any, Callable

from . import _codec, _registry

_WIRE_VERSION = "1"
_PATH_TEMPLATE = "/_caravan/rpc/{interface}/{method}"
_DEFAULT_TIMEOUT_SECONDS = 30


class RpcTransportError(RuntimeError):
    """HTTP/network-level failure: timeout, 5xx without a parseable error body,
    connection refused, DNS failure.
    """


class RpcRemoteError(RuntimeError):
    """Peer returned ``{"ok": false, "error": {"code": ..., "message": ...}}``.

    The remote impl raised an exception that the server handler converted to an
    error envelope. ``code`` is the exception class name; ``message`` is its
    stringified body.
    """

    def __init__(self, code: str, message: str):
        super().__init__(f"{code}: {message}")
        self.code = code
        self.message = message


def _load_peers() -> dict[str, dict[str, Any]]:
    """Parse ``CARAVAN_RPC_PEERS`` from the environment. Empty/unset → ``{}``."""
    raw = os.environ.get("CARAVAN_RPC_PEERS")
    if not raw:
        return {}
    try:
        return json.loads(raw)
    except json.JSONDecodeError as exc:
        raise RuntimeError(
            f"CARAVAN_RPC_PEERS must be valid JSON; got: {raw!r}"
        ) from exc


class _ClientProxy:
    """Dispatch proxy returned by ``client(I)``. Reads the peer table once at
    construction (locks dispatch mode for the proxy's lifetime); each method
    access dispatches per that mode.
    """

    __slots__ = ("_interface", "_peer")

    def __init__(self, interface_cls: type):
        if not getattr(interface_cls, "__caravan_wagon__", False):
            raise TypeError(
                f"{interface_cls.__name__} is not @wagon-decorated; "
                "client() requires a @wagon interface."
            )
        self._interface = interface_cls
        peers = _load_peers()
        self._peer = peers.get(interface_cls.__name__)

    def __repr__(self) -> str:
        mode = self._peer.get("mode") if self._peer else "inproc(no-env)"
        return f"<caravan_rpc.client {self._interface.__name__} mode={mode}>"

    def __getattr__(self, method_name: str) -> Callable[..., Any]:
        if method_name.startswith("_"):
            raise AttributeError(method_name)
        methods = self._interface.__caravan_methods__
        if method_name not in methods:
            raise AttributeError(
                f"{self._interface.__name__} has no @wagon method {method_name!r}; "
                f"declared methods: {sorted(methods)}"
            )

        peer = self._peer
        if peer is None or peer.get("mode") == "inproc":
            impl = _registry.lookup(self._interface)
            return getattr(impl, method_name)

        mode = peer.get("mode")
        if mode == "http":
            return _make_http_dispatcher(self._interface, method_name, peer["url"])
        if mode == "lambda":
            raise NotImplementedError(
                "lambda dispatch mode lands at M7 (SigV4 signing not yet implemented)"
            )
        raise ValueError(
            f"unknown dispatch mode {mode!r} for {self._interface.__name__} "
            f"in CARAVAN_RPC_PEERS"
        )


def _make_http_dispatcher(
    interface_cls: type, method_name: str, base_url: str
) -> Callable[..., Any]:
    """Build the per-call HTTP dispatch closure for one ``(interface, method)``."""
    path = _PATH_TEMPLATE.format(interface=interface_cls.__name__, method=method_name)
    full_url = base_url.rstrip("/") + path

    def dispatch(*args: Any, **kwargs: Any) -> Any:
        envelope = _codec.encode_call(interface_cls, method_name, *args, **kwargs)
        body = json.dumps(envelope).encode("utf-8")
        headers = {
            "Content-Type": "application/json",
            "X-Caravan-Rpc-Version": _WIRE_VERSION,
        }
        # Read the secret at call time, not closure-bind time — lets tests rotate it.
        secret = os.environ.get("CARAVAN_RPC_SHARED_SECRET")
        if secret:
            headers["Authorization"] = f"Bearer {secret}"

        req = urllib.request.Request(full_url, data=body, headers=headers, method="POST")
        try:
            with urllib.request.urlopen(req, timeout=_DEFAULT_TIMEOUT_SECONDS) as resp:
                raw = resp.read()
        except urllib.error.HTTPError as exc:
            err_payload: dict[str, Any] | None = None
            try:
                err_payload = json.loads(exc.read())
            except (json.JSONDecodeError, ValueError):
                err_payload = None
            if (
                isinstance(err_payload, dict)
                and err_payload.get("ok") is False
                and isinstance(err_payload.get("error"), dict)
            ):
                e = err_payload["error"]
                raise RpcRemoteError(
                    str(e.get("code", "Unknown")), str(e.get("message", ""))
                ) from None
            raise RpcTransportError(
                f"HTTP {exc.code} from {full_url}: {exc.reason}"
            ) from exc
        except urllib.error.URLError as exc:
            raise RpcTransportError(
                f"transport failure to {full_url}: {exc.reason}"
            ) from exc

        response = json.loads(raw)
        if response.get("ok") is True:
            return _codec.decode_return(interface_cls, method_name, response.get("result"))
        err = response.get("error", {}) or {}
        raise RpcRemoteError(str(err.get("code", "Unknown")), str(err.get("message", "")))

    dispatch.__name__ = f"{interface_cls.__name__}.{method_name}"
    dispatch.__qualname__ = dispatch.__name__
    return dispatch


def client(interface_cls: type) -> _ClientProxy:
    """Return a dispatch proxy for ``interface_cls``.

    With ``CARAVAN_RPC_PEERS`` unset (or no entry for this interface), every
    attribute access returns the registered impl's bound method directly — no
    wrapping, no overhead. Call ``provide(interface_cls, impl)`` first.
    """
    return _ClientProxy(interface_cls)
