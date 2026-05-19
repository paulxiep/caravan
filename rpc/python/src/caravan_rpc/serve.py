"""``python -m caravan_rpc.serve`` — long-running HTTP server hosting one ``@wagon`` impl.

Usage::

    python -m caravan_rpc.serve \\
        --interface LLMExtraction \\
        --impl invoice_processing.extraction:GeminiExtractor \\
        [--interface-module invoice_processing.extraction] \\
        [--host 0.0.0.0] \\
        [--port 8080]

Defaults:
    --interface-module: the same module as ``--impl`` (works when interface +
                        impl live in the same file, as in invoice-parse's
                        ``extraction.py``).
    --host: 0.0.0.0
    --port: 8080

Wire contract (matches ``_proxy.py`` and ``docs/poc_rpc_sdk.md``):

    POST /_caravan/rpc/<interface>/<method>
    Content-Type: application/json
    X-Caravan-Rpc-Version: 1
    Authorization: Bearer <shared-secret>     # only enforced when
                                              # CARAVAN_RPC_SHARED_SECRET is set

    {"args": [], "kwargs": {...}}

    →  200 {"ok": true,  "result": ...}
       500 {"ok": false, "error": {"code": "<ExcClass>", "message": "..."}}
"""

from __future__ import annotations

import argparse
import importlib
import json
import os
import sys
import traceback
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any

from . import _codec, _registry, provide

_PATH_PREFIX = "/_caravan/rpc/"
_WIRE_VERSION = "1"


def _parse_impl_ref(ref: str) -> tuple[str, str]:
    """Parse ``module.path:ClassName`` → ``(module_path, class_name)``."""
    if ":" not in ref:
        raise SystemExit(
            f"--impl must be of the form 'module.path:ClassName'; got {ref!r}"
        )
    module_path, class_name = ref.split(":", 1)
    if not module_path or not class_name:
        raise SystemExit(
            f"--impl must be of the form 'module.path:ClassName'; got {ref!r}"
        )
    return module_path, class_name


def _resolve_interface_and_impl(
    interface_name: str, impl_ref: str, interface_module: str | None
) -> tuple[type, type]:
    """Import the impl module + class, locate the matching ``@wagon`` interface."""
    impl_module_path, impl_class_name = _parse_impl_ref(impl_ref)
    try:
        impl_module = importlib.import_module(impl_module_path)
    except ImportError as exc:
        raise SystemExit(f"cannot import impl module {impl_module_path!r}: {exc}") from exc
    try:
        impl_class = getattr(impl_module, impl_class_name)
    except AttributeError:
        raise SystemExit(
            f"module {impl_module_path!r} has no attribute {impl_class_name!r}"
        ) from None

    iface_module_path = interface_module or impl_module_path
    try:
        iface_module = importlib.import_module(iface_module_path)
    except ImportError as exc:
        raise SystemExit(
            f"cannot import interface module {iface_module_path!r}: {exc}"
        ) from exc
    try:
        interface_cls = getattr(iface_module, interface_name)
    except AttributeError:
        raise SystemExit(
            f"module {iface_module_path!r} has no attribute {interface_name!r}; "
            f"pass --interface-module to point at a different module"
        ) from None

    if not getattr(interface_cls, "__caravan_wagon__", False):
        raise SystemExit(
            f"{iface_module_path}.{interface_name} is not @wagon-decorated"
        )
    return interface_cls, impl_class


def _make_handler(interface_cls: type, secret: str | None) -> type:
    """Build a ``BaseHTTPRequestHandler`` subclass bound to this interface."""
    interface_name = interface_cls.__name__
    methods = interface_cls.__caravan_methods__

    class Handler(BaseHTTPRequestHandler):
        # Quiet the default access-log spam; subclasses can override.
        def log_message(self, fmt: str, *args: Any) -> None:
            sys.stderr.write(
                "%s - %s\n" % (self.address_string(), fmt % args)
            )

        def _write_envelope(self, status: int, payload: dict[str, Any]) -> None:
            body = json.dumps(payload).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def _err(self, status: int, code: str, message: str) -> None:
            self._write_envelope(
                status, {"ok": False, "error": {"code": code, "message": message}}
            )

        def do_POST(self) -> None:  # noqa: N802 — http.server API
            # Path: /_caravan/rpc/<interface>/<method>
            if not self.path.startswith(_PATH_PREFIX):
                self._err(
                    HTTPStatus.NOT_FOUND,
                    "BadPath",
                    f"expected path starting with {_PATH_PREFIX!r}",
                )
                return
            tail = self.path[len(_PATH_PREFIX):]
            parts = tail.split("/")
            if len(parts) != 2 or not all(parts):
                self._err(
                    HTTPStatus.NOT_FOUND,
                    "BadPath",
                    f"expected {_PATH_PREFIX}<interface>/<method>; got {self.path!r}",
                )
                return
            recv_iface, recv_method = parts
            if recv_iface != interface_name:
                self._err(
                    HTTPStatus.NOT_FOUND,
                    "InterfaceMismatch",
                    f"server hosts {interface_name!r}, got {recv_iface!r}",
                )
                return
            if recv_method not in methods:
                self._err(
                    HTTPStatus.NOT_FOUND,
                    "UnknownMethod",
                    f"{interface_name} has no @wagon method {recv_method!r}",
                )
                return

            # Version header.
            version = self.headers.get("X-Caravan-Rpc-Version")
            if version != _WIRE_VERSION:
                self._err(
                    HTTPStatus.BAD_REQUEST,
                    "BadVersion",
                    f"expected X-Caravan-Rpc-Version: {_WIRE_VERSION}; got {version!r}",
                )
                return

            # Auth (skipped when secret is unset — dev mode).
            if secret is not None:
                auth = self.headers.get("Authorization", "")
                if not auth.startswith("Bearer ") or auth[len("Bearer "):] != secret:
                    self._err(
                        HTTPStatus.UNAUTHORIZED,
                        "Unauthorized",
                        "missing or invalid Bearer token",
                    )
                    return

            # Read body.
            try:
                length = int(self.headers.get("Content-Length", "0"))
            except ValueError:
                self._err(HTTPStatus.BAD_REQUEST, "BadLength", "invalid Content-Length")
                return
            raw_body = self.rfile.read(length) if length > 0 else b""
            try:
                envelope = json.loads(raw_body) if raw_body else {}
            except json.JSONDecodeError as exc:
                self._err(HTTPStatus.BAD_REQUEST, "BadJSON", str(exc))
                return

            # Decode → call impl → encode.
            try:
                args, kwargs = _codec.decode_call(interface_cls, recv_method, envelope)
            except Exception as exc:
                self._err(HTTPStatus.BAD_REQUEST, type(exc).__name__, str(exc))
                return

            try:
                impl = _registry.lookup(interface_cls)
            except LookupError as exc:
                self._err(
                    HTTPStatus.INTERNAL_SERVER_ERROR, "NoProvider", str(exc)
                )
                return

            method = getattr(impl, recv_method)
            try:
                result = method(*args, **kwargs)
            except Exception as exc:
                traceback.print_exc(file=sys.stderr)
                self._err(
                    HTTPStatus.INTERNAL_SERVER_ERROR, type(exc).__name__, str(exc)
                )
                return

            try:
                encoded = _codec.encode_return(interface_cls, recv_method, result)
            except Exception as exc:
                self._err(
                    HTTPStatus.INTERNAL_SERVER_ERROR, "EncodeError", str(exc)
                )
                return

            self._write_envelope(HTTPStatus.OK, {"ok": True, "result": encoded})

    return Handler


def serve_blocking(
    interface_cls: type,
    *,
    host: str = "0.0.0.0",
    port: int = 8080,
    secret: str | None = None,
) -> None:
    """Start a ``ThreadingHTTPServer`` bound to ``host:port`` and serve forever.

    Useful from tests (spawn in a daemon thread) and from user-owned launcher
    modules. The CLI in ``main()`` is the standard entry point.
    """
    handler_cls = _make_handler(interface_cls, secret)
    server = ThreadingHTTPServer((host, port), handler_cls)
    try:
        server.serve_forever()
    finally:
        server.server_close()


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="python -m caravan_rpc.serve",
        description="Host a @wagon impl over HTTP for caravan-rpc clients.",
    )
    parser.add_argument(
        "--interface",
        required=True,
        help="Name of the @wagon-decorated interface class.",
    )
    parser.add_argument(
        "--impl",
        required=True,
        help="'module.path:ClassName' of the concrete impl to register.",
    )
    parser.add_argument(
        "--interface-module",
        default=None,
        help="Module containing the @wagon interface (default: same module as --impl).",
    )
    parser.add_argument("--host", default="0.0.0.0")
    parser.add_argument("--port", type=int, default=8080)
    args = parser.parse_args(argv)

    interface_cls, impl_class = _resolve_interface_and_impl(
        args.interface, args.impl, args.interface_module
    )
    provide(interface_cls, impl_class())

    secret = os.environ.get("CARAVAN_RPC_SHARED_SECRET")
    if secret is None:
        print(
            "[caravan_rpc.serve] WARNING: CARAVAN_RPC_SHARED_SECRET unset; "
            "server will accept unauthenticated requests (dev mode).",
            file=sys.stderr,
        )

    print(
        f"[caravan_rpc.serve] serving {interface_cls.__name__} on "
        f"http://{args.host}:{args.port}",
        file=sys.stderr,
    )
    try:
        serve_blocking(
            interface_cls, host=args.host, port=args.port, secret=secret
        )
    except KeyboardInterrupt:
        print("[caravan_rpc.serve] shutting down", file=sys.stderr)
    return 0


if __name__ == "__main__":  # pragma: no cover
    raise SystemExit(main())
