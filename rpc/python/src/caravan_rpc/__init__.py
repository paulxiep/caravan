"""Pre-release placeholder for caravan-rpc.

Runtime SDK for the Caravan application-definition compiler.

The functional SDK lands at 0.1.0. This 0.0.1 release reserves the PyPI name
and provides import-time-compatible no-op stubs so SDK-wrapped code does not
crash at import.

See https://github.com/paulxiep/caravan for thesis, PoC specs, and roadmap.
"""

from __future__ import annotations

__version__ = "0.0.1"

_PLACEHOLDER_MSG = (
    "caravan-rpc 0.0.1 is a pre-release placeholder. "
    "The functional SDK lands at 0.1.0; see https://github.com/paulxiep/caravan."
)


def wagon(cls):
    """Pre-release decorator placeholder.

    The real ``@wagon`` decorator captures the class as a seam declaration
    for the Caravan compiler. In 0.0.1 it is an identity function so
    SDK-wrapped code imports cleanly.
    """
    return cls


def provide(_interface, _impl):
    """Pre-release no-op.

    The real ``provide()`` registers ``_impl`` as the provider for
    ``_interface`` in the SDK's inproc registry. In 0.0.1 it does nothing.
    """


def client(_interface):
    """Pre-release stub.

    The real ``client()`` returns a dispatcher proxy for ``_interface``
    that consults ``CARAVAN_RPC_PEERS`` and routes calls inproc / http / lambda.
    In 0.0.1 it raises NotImplementedError to signal that the SDK isn't usable yet.
    """
    raise NotImplementedError(_PLACEHOLDER_MSG)


__all__ = ["wagon", "provide", "client", "__version__"]
