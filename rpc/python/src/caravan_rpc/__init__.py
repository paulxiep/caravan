"""caravan-rpc — runtime SDK for the Caravan application-definition compiler.

A user decorates a seam-interface class with ``@wagon``, registers a concrete
implementation via ``provide(I, impl)``, and dispatches through ``client(I)``.
Dispatch mode (inproc / http / lambda) is read from the ``CARAVAN_RPC_PEERS``
env var at process start; when the env var is unset, ``client(I).method`` is a
direct call on the registered impl with no overhead.

See https://github.com/paulxiep/caravan/blob/main/docs/poc_rpc_sdk.md for the
wire contract and per-language surface.
"""

from __future__ import annotations

import inspect

from . import _registry
from ._proxy import RpcRemoteError, RpcTransportError, client

__version__ = "0.1.0"


def wagon(cls):
    """Mark a class as a seam interface.

    Captures public method signatures on the class for later use by the wire
    codec (each method's args/return type hints become ``pydantic.TypeAdapter``
    instances at step 1c). The class itself is returned unchanged for
    ``isinstance`` purposes; only ``__caravan_wagon__`` and ``__caravan_methods__``
    metadata is attached.

    Methods beginning with ``_`` are excluded. Only regular functions defined on
    the class are captured — classmethods, staticmethods, and inherited methods
    are not part of the seam surface.
    """
    cls.__caravan_wagon__ = True
    cls.__caravan_methods__ = {
        name: inspect.signature(member)
        for name, member in inspect.getmembers(cls, predicate=inspect.isfunction)
        if not name.startswith("_")
    }
    return cls


def provide(interface_cls, impl):
    """Register ``impl`` as the provider for ``interface_cls`` in this process.

    Call once at process startup (worker entry, CLI ``main()``) before any
    ``client(interface_cls).method(...)`` call. The proxy looks up the
    registered impl on every inproc dispatch.

    Raises ``TypeError`` if ``interface_cls`` is not ``@wagon``-decorated.
    """
    _registry.register(interface_cls, impl)


# Resources re-export lives after `provide` (which the resource adapters
# call internally during auto_register). E402 is suppressed because the
# load-order dependency is structural, not stylistic.
from .resources import (  # noqa: E402
    BlobStore,
    LocalFsBlobStore,
    MessageQueue,
    RabbitMQQueue,
    RedisStreamQueue,
    S3BlobStore,
    SqsQueue,
    auto_register_resources,
)

__all__ = [
    "wagon",
    "provide",
    "client",
    "RpcRemoteError",
    "RpcTransportError",
    # Resource seams (Caravan-shipped; users import + call client(BlobStore) etc.).
    "BlobStore",
    "MessageQueue",
    # Concrete impls (mainly for testing or explicit construction; auto_register
    # picks the right one based on env / yaml fallback for typical usage).
    "LocalFsBlobStore",
    "S3BlobStore",
    "RedisStreamQueue",
    "RabbitMQQueue",
    "SqsQueue",
    # Bootstrap helper — call once at process startup.
    "auto_register_resources",
    "__version__",
]
