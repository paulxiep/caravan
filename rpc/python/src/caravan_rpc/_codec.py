"""Unified wire codec for ``@wagon`` interfaces, built on ``pydantic.TypeAdapter``.

For each ``(interface_cls, method_name)`` pair, build one ``TypeAdapter`` per
annotated parameter and one for the return type. Adapters are cached lazily on
first use — by then the user's whole module is imported, so string annotations
under ``from __future__ import annotations`` resolve correctly via
``typing.get_type_hints``.

Pydantic v2 ``TypeAdapter`` handles a single code path for: primitives,
``Optional[X]`` / ``X | None``, ``list[X]``, ``dict[K, V]``, unions, Pydantic
``BaseModel`` subclasses, standard library dataclasses, ``TypedDict``,
generics, ``Annotated`` types. No per-type forks in this module.

Wire envelope on the wire:

    request:  {"args": [], "kwargs": {<param_name>: <jsonable>}}
    response: {"ok": true, "result": <jsonable>}
              | {"ok": false, "error": {"code": str, "message": str}}

Args are always normalized into ``kwargs`` (positional args are bound to their
parameter names via ``inspect.Signature.bind``). The ``args`` list stays empty
in v1 — keeps the format language-agnostic.
"""

from __future__ import annotations

import inspect
import threading
from typing import Any, get_type_hints

from pydantic import ConfigDict, TypeAdapter

# Cache: (interface_cls, method_name) -> (param_adapters, return_adapter)
_cache_lock = threading.Lock()
_cache: dict[tuple[type, str], tuple[dict[str, TypeAdapter], TypeAdapter | None]] = {}

# Shared adapter config: serialize bytes as base64 (default is utf8 which
# crashes on binary payloads — PDFs, images, anything outside ASCII).
# Mirrors the Rust SDK's serde_bytes-style encoding. Pydantic rejects
# applying `config=` on types that own a config (BaseModel, dataclass,
# TypedDict) — `_build_adapter` falls back to the no-config form for
# those; bytes-bearing primitives + Optional/list/dict wrappers get the
# base64 encoding.
_wire_config = ConfigDict(ser_json_bytes="base64", val_json_bytes="base64")


def _build_adapter(hint: Any) -> TypeAdapter:
    """Build a TypeAdapter for `hint`, applying `_wire_config` when possible.

    Pydantic v2 disallows `config=` on BaseModel / dataclass / TypedDict
    (those own their config). For everything else — bytes, primitives,
    Optional, list, dict, unions of the above — the base64 serialization
    config matters; for owned-config types pydantic already handles their
    own bytes fields correctly.
    """
    try:
        return TypeAdapter(hint, config=_wire_config)
    except Exception:  # noqa: BLE001 — pydantic.PydanticUserError per docs
        return TypeAdapter(hint)


def _adapters_for(
    interface_cls: type, method_name: str
) -> tuple[dict[str, TypeAdapter], TypeAdapter | None]:
    """Return cached ``(param_adapters, return_adapter)`` for a wagon method.

    ``param_adapters``: dict of parameter name → ``TypeAdapter``, excluding
        ``self`` and unannotated parameters (which pass through as raw values).
    ``return_adapter``: ``TypeAdapter`` for the return type, or ``None`` when
        the method has no return annotation.

    String annotations (PEP 563) are resolved at first call using the method's
    module globals; results are cached.
    """
    key = (interface_cls, method_name)
    with _cache_lock:
        cached = _cache.get(key)
    if cached is not None:
        return cached

    if not getattr(interface_cls, "__caravan_wagon__", False):
        raise TypeError(
            f"{interface_cls.__name__} is not @wagon-decorated; "
            "codec lookups require @wagon metadata."
        )
    try:
        method = getattr(interface_cls, method_name)
    except AttributeError as exc:
        raise LookupError(
            f"{interface_cls.__name__} has no method {method_name!r}"
        ) from exc

    # Resolve string annotations using the method's module globals + the
    # interface class's local namespace (handles classes defined alongside the
    # interface in the same module).
    module = inspect.getmodule(interface_cls)
    globalns = getattr(module, "__dict__", {}) if module is not None else {}
    hints = get_type_hints(method, globalns=globalns, include_extras=True)

    sig: inspect.Signature = interface_cls.__caravan_methods__[method_name]
    param_adapters: dict[str, TypeAdapter] = {}
    for name in sig.parameters:
        if name == "self":
            continue
        if name in hints:
            param_adapters[name] = _build_adapter(hints[name])

    return_adapter: TypeAdapter | None = None
    if "return" in hints and hints["return"] is not type(None):
        return_adapter = _build_adapter(hints["return"])

    result = (param_adapters, return_adapter)
    with _cache_lock:
        _cache[key] = result
    return result


def encode_call(
    interface_cls: type, method_name: str, *args: Any, **kwargs: Any
) -> dict[str, Any]:
    """Bind ``args``/``kwargs`` to the method signature, encode each value via its
    ``TypeAdapter``, and return the wire envelope ``{"args": [], "kwargs": {...}}``.

    Defaults are applied so optional args omitted by the caller appear in the
    envelope with their default values (mirrors the impl's view).
    """
    param_adapters, _ = _adapters_for(interface_cls, method_name)
    sig: inspect.Signature = interface_cls.__caravan_methods__[method_name]

    # `None` placeholder for `self` — the caller never passes self through the proxy.
    bound = sig.bind(None, *args, **kwargs)
    bound.apply_defaults()

    encoded_kwargs: dict[str, Any] = {}
    for name, value in bound.arguments.items():
        if name == "self":
            continue
        adapter = param_adapters.get(name)
        if adapter is not None:
            encoded_kwargs[name] = adapter.dump_python(value, mode="json")
        else:
            encoded_kwargs[name] = value
    return {"args": [], "kwargs": encoded_kwargs}


def decode_call(
    interface_cls: type, method_name: str, envelope: dict[str, Any]
) -> tuple[list[Any], dict[str, Any]]:
    """Decode a wire envelope into ``(args, kwargs)`` ready to call the impl.

    ``args`` is empty in v1 (all params normalized into ``kwargs`` by the encoder);
    if a remote sends positional args, they're passed through as-is.
    """
    param_adapters, _ = _adapters_for(interface_cls, method_name)
    raw_args = envelope.get("args", [])
    raw_kwargs = envelope.get("kwargs", {})

    decoded_kwargs: dict[str, Any] = {}
    for name, raw in raw_kwargs.items():
        adapter = param_adapters.get(name)
        if adapter is not None:
            decoded_kwargs[name] = adapter.validate_python(raw)
        else:
            decoded_kwargs[name] = raw
    return list(raw_args), decoded_kwargs


def encode_return(interface_cls: type, method_name: str, value: Any) -> Any:
    """Encode a return value via the method's return ``TypeAdapter`` (JSON mode)."""
    _, return_adapter = _adapters_for(interface_cls, method_name)
    if return_adapter is None:
        return value
    return return_adapter.dump_python(value, mode="json")


def decode_return(interface_cls: type, method_name: str, raw: Any) -> Any:
    """Decode a return value via the method's return ``TypeAdapter``."""
    _, return_adapter = _adapters_for(interface_cls, method_name)
    if return_adapter is None:
        return raw
    return return_adapter.validate_python(raw)


def _clear_cache() -> None:
    """Reset the adapter cache. For tests only."""
    with _cache_lock:
        _cache.clear()
