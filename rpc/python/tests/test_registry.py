"""Tests for the inproc registry + ``provide()`` (B0 step 1b).

Real-shape: a ``@wagon LLMExtraction`` interface registered with a
``GeminiExtractor``-style impl, looked up by interface class.
"""

from __future__ import annotations

from dataclasses import dataclass

import pytest

from caravan_rpc import provide, wagon
from caravan_rpc import _registry


@dataclass
class FakeInvoiceExtraction:
    vendor: str


@wagon
class LLMExtraction:
    def extract(self, raw_ocr: str | None = None) -> FakeInvoiceExtraction:
        ...


class GeminiExtractor:
    def extract(self, raw_ocr: str | None = None) -> FakeInvoiceExtraction:
        return FakeInvoiceExtraction(vendor=f"gemini:{raw_ocr or ''}")


@pytest.fixture(autouse=True)
def _isolate_registry():
    _registry.clear()
    yield
    _registry.clear()


def test_provide_then_lookup_returns_same_instance():
    impl = GeminiExtractor()
    provide(LLMExtraction, impl)
    assert _registry.lookup(LLMExtraction) is impl


def test_provide_rejects_non_wagon_interface():
    class NotAnInterface:
        def extract(self, raw_ocr: str | None = None) -> FakeInvoiceExtraction: ...

    with pytest.raises(TypeError, match="not @wagon-decorated"):
        provide(NotAnInterface, GeminiExtractor())


def test_lookup_unregistered_raises_clear_error():
    with pytest.raises(LookupError, match="LLMExtraction"):
        _registry.lookup(LLMExtraction)


def test_provide_last_write_wins():
    """Re-registration replaces the prior impl. Useful for tests that
    swap in a mock; production code should only call provide() once per process.
    """
    first = GeminiExtractor()
    second = GeminiExtractor()
    provide(LLMExtraction, first)
    provide(LLMExtraction, second)
    assert _registry.lookup(LLMExtraction) is second


def test_registry_isolates_different_interfaces():
    @wagon
    class OtherSeam:
        def thing(self) -> int: ...

    class OtherImpl:
        def thing(self) -> int:
            return 42

    g = GeminiExtractor()
    o = OtherImpl()
    provide(LLMExtraction, g)
    provide(OtherSeam, o)
    assert _registry.lookup(LLMExtraction) is g
    assert _registry.lookup(OtherSeam) is o


def test_registry_is_thread_safe_for_concurrent_provide():
    """Smoke test: many threads registering different interfaces concurrently
    should not corrupt the registry. (We're not asserting performance; just
    that the lock keeps the dict consistent.)
    """
    import threading

    interfaces = []
    for i in range(20):
        cls = wagon(type(f"Iface{i}", (), {"m": lambda self: i}))
        interfaces.append(cls)

    impls = [object() for _ in interfaces]

    def register_one(idx: int) -> None:
        provide(interfaces[idx], impls[idx])

    threads = [threading.Thread(target=register_one, args=(i,)) for i in range(len(interfaces))]
    for t in threads:
        t.start()
    for t in threads:
        t.join()

    for iface, impl in zip(interfaces, impls):
        assert _registry.lookup(iface) is impl
