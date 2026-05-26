"""AWS Lambda entry point for hosting one ``@wagon`` impl as a Lambda peer (M7).

Used as the container image's CMD when the compiler emits a Lambda function::

    CMD ["caravan_rpc.lambda_handler.lambda_handler"]

Cold start reads four env vars set by the compiler-emitted Lambda function spec:

* ``CARAVAN_RPC_LAMBDA_INTERFACE`` — name of the ``@wagon`` class (e.g.
  ``ValidateExtraction``).
* ``CARAVAN_RPC_LAMBDA_INTERFACE_MODULE`` — Python module containing the
  ``@wagon`` interface class.
* ``CARAVAN_RPC_LAMBDA_IMPL`` — ``module.path:ClassName`` of the concrete impl
  to register via :func:`caravan_rpc.provide`.

The Function URL event shape we accept follows AWS Lambda Function URLs with
payload format v2.0. We parse ``requestContext.http.path`` to extract the
``/_caravan/rpc/<interface>/<method>`` route, mirror ``serve.py``'s wire-version
+ interface-match + method-lookup checks, then dispatch to the registered impl.

Auth is enforced by Function URL's ``AuthType: AWS_IAM`` at the AWS edge (SigV4
required by the caller); the handler does not check a bearer secret. That's
the M7 auth split: bearer for HTTP mode, SigV4-only for Lambda mode.
"""

from __future__ import annotations

import base64
import importlib
import json
import os
import sys
import traceback
from typing import Any

from . import _codec, _registry, provide

_PATH_PREFIX = "/_caravan/rpc/"
_WIRE_VERSION = "1"


def _resolve_and_register(
    interface_name: str, impl_ref: str, interface_module_path: str | None
) -> type:
    """Import + register the impl, return the ``@wagon`` interface class."""
    if ":" not in impl_ref:
        raise SystemExit(
            f"CARAVAN_RPC_LAMBDA_IMPL must be 'module.path:ClassName'; got {impl_ref!r}"
        )
    impl_module_path, impl_class_name = impl_ref.split(":", 1)

    impl_module = importlib.import_module(impl_module_path)
    impl_class = getattr(impl_module, impl_class_name)

    iface_module_path = interface_module_path or impl_module_path
    iface_module = importlib.import_module(iface_module_path)
    interface_cls = getattr(iface_module, interface_name)
    if not getattr(interface_cls, "__caravan_wagon__", False):
        raise SystemExit(
            f"{iface_module_path}.{interface_name} is not @wagon-decorated"
        )

    provide(interface_cls, impl_class())
    return interface_cls


def _err(status: int, code: str, message: str) -> dict[str, Any]:
    """Build a Lambda Function URL response carrying a wire-v1 error envelope."""
    return {
        "statusCode": status,
        "headers": {
            "Content-Type": "application/json",
            "X-Caravan-Rpc-Version": _WIRE_VERSION,
        },
        "body": json.dumps({"ok": False, "error": {"code": code, "message": message}}),
    }


def _ok(payload: dict[str, Any]) -> dict[str, Any]:
    return {
        "statusCode": 200,
        "headers": {
            "Content-Type": "application/json",
            "X-Caravan-Rpc-Version": _WIRE_VERSION,
        },
        "body": json.dumps(payload),
    }


# Cold-start registration. AWS Lambda re-uses the container across invocations,
# so importing + providing once at module load amortizes the cost. Cold start
# wraps a fresh container; warm invocations reuse this state.
_INTERFACE_NAME = os.environ.get("CARAVAN_RPC_LAMBDA_INTERFACE")
_INTERFACE_MODULE = os.environ.get("CARAVAN_RPC_LAMBDA_INTERFACE_MODULE")
_IMPL_REF = os.environ.get("CARAVAN_RPC_LAMBDA_IMPL")

_INTERFACE_CLS: type | None
if _INTERFACE_NAME and _IMPL_REF:
    _INTERFACE_CLS = _resolve_and_register(_INTERFACE_NAME, _IMPL_REF, _INTERFACE_MODULE)
else:
    # Defer the import error until invocation so module-level import never
    # crashes (e.g. when this module is imported by tests without the env vars).
    _INTERFACE_CLS = None


def lambda_handler(event: dict[str, Any], context: Any) -> dict[str, Any]:  # noqa: ARG001
    """Lambda Function URL entry point.

    Returns a v2.0 Function URL response dict. Wire-v1 envelope sits inside
    ``body``; statusCode reflects transport-level outcomes (200 for ok or
    logical error, 400/404 for malformed requests).
    """
    if _INTERFACE_CLS is None:
        return _err(
            500,
            "MissingEnv",
            "CARAVAN_RPC_LAMBDA_INTERFACE / CARAVAN_RPC_LAMBDA_IMPL must be set",
        )

    interface_name = _INTERFACE_CLS.__name__
    methods = _INTERFACE_CLS.__caravan_methods__

    # Path extraction. Function URL v2.0 payload puts the path under
    # requestContext.http.path; fall back to rawPath for safety.
    request_ctx = event.get("requestContext") or {}
    http_ctx = request_ctx.get("http") or {}
    path = http_ctx.get("path") or event.get("rawPath") or ""
    if not path.startswith(_PATH_PREFIX):
        return _err(404, "BadPath", f"expected path starting with {_PATH_PREFIX!r}")
    tail = path[len(_PATH_PREFIX):]
    parts = tail.split("/")
    if len(parts) != 2 or not all(parts):
        return _err(404, "BadPath", f"expected {_PATH_PREFIX}<interface>/<method>; got {path!r}")
    recv_iface, recv_method = parts
    if recv_iface != interface_name:
        return _err(
            404,
            "InterfaceMismatch",
            f"server hosts {interface_name!r}, got {recv_iface!r}",
        )
    if recv_method not in methods:
        return _err(
            404,
            "UnknownMethod",
            f"{interface_name} has no @wagon method {recv_method!r}",
        )

    # Headers come in case-insensitive but boto/AWS normalize to lowercase.
    headers = {k.lower(): v for k, v in (event.get("headers") or {}).items()}
    version = headers.get("x-caravan-rpc-version")
    if version != _WIRE_VERSION:
        return _err(
            400,
            "BadVersion",
            f"expected X-Caravan-Rpc-Version: {_WIRE_VERSION}; got {version!r}",
        )

    # Body decode. Function URL marks binary payloads with isBase64Encoded.
    raw_body = event.get("body") or ""
    if event.get("isBase64Encoded"):
        raw_body = base64.b64decode(raw_body).decode("utf-8")
    try:
        envelope = json.loads(raw_body) if raw_body else {}
    except json.JSONDecodeError as exc:
        return _err(400, "BadJSON", str(exc))

    try:
        args, kwargs = _codec.decode_call(_INTERFACE_CLS, recv_method, envelope)
    except Exception as exc:
        return _err(400, type(exc).__name__, str(exc))

    try:
        impl = _registry.lookup(_INTERFACE_CLS)
    except LookupError as exc:
        return _err(500, "NoProvider", str(exc))

    method = getattr(impl, recv_method)
    try:
        result = method(*args, **kwargs)
    except Exception as exc:
        traceback.print_exc(file=sys.stderr)
        return _err(500, type(exc).__name__, str(exc))

    try:
        encoded = _codec.encode_return(_INTERFACE_CLS, recv_method, result)
    except Exception as exc:
        return _err(500, "EncodeError", str(exc))

    return _ok({"ok": True, "result": encoded})
