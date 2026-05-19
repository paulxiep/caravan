"""Inproc registry mapping seam interfaces to concrete impl instances.

A user calls ``provide(I, impl)`` once per process at startup (worker/CLI
entry) to register the impl for the seam interface ``I``. The proxy returned
by ``client(I)`` looks up the impl here on every call when dispatch mode is
``inproc`` (or when ``CARAVAN_RPC_PEERS`` is unset, which is the no-config
inertness path).

Thread-safe via a module-level lock. The registry is process-global by
design — every process that runs ``provide()`` builds its own.
"""

from __future__ import annotations

import threading
from typing import Any

_lock = threading.Lock()
_impls: dict[type, Any] = {}


def register(interface_cls: type, impl: Any) -> None:
    """Register ``impl`` as the provider for ``interface_cls``.

    Raises ``TypeError`` if ``interface_cls`` is not a ``@wagon``-decorated class.
    Re-registering an interface replaces the prior impl (last-write-wins) — this
    is intentional for test isolation; production code should ``provide()`` once.
    """
    if not getattr(interface_cls, "__caravan_wagon__", False):
        raise TypeError(
            f"{interface_cls.__name__} is not @wagon-decorated; "
            "decorate the interface class with @wagon before calling provide()."
        )
    with _lock:
        _impls[interface_cls] = impl


def lookup(interface_cls: type) -> Any:
    """Return the registered impl for ``interface_cls``.

    Raises ``LookupError`` if no impl is registered. The proxy converts this
    into a clearer error message naming the missing interface.
    """
    with _lock:
        try:
            return _impls[interface_cls]
        except KeyError:
            raise LookupError(
                f"no impl registered for @wagon interface {interface_cls.__name__}; "
                f"call provide({interface_cls.__name__}, <impl>) at process startup."
            ) from None


def clear() -> None:
    """Reset the registry. For tests only."""
    with _lock:
        _impls.clear()
