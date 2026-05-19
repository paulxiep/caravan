"""Tests for the ``@wagon`` decorator (B0 step 1a).

The shape under test mirrors invoice-parse's real ``LLMExtraction`` interface:
optional dataclass arguments with ``None`` defaults, a non-trivial return type,
``from __future__ import annotations`` causing annotations to be string forms.
"""

from __future__ import annotations

import inspect
from dataclasses import dataclass

from caravan_rpc import wagon


# ---------------------------------------------------------------------------
# Fixtures shaped like real invoice-parse types.

@dataclass
class FakeRawOcr:
    text: str = ""


@dataclass
class FakeTableExtraction:
    rows: list[list[str]] | None = None


@dataclass
class FakeInvoiceExtraction:
    vendor: str
    total: float


# ---------------------------------------------------------------------------
# Marker + identity.

def test_wagon_returns_same_class():
    @wagon
    class Greet:
        def hi(self, who: str) -> str: ...

    assert isinstance(Greet(), Greet)
    assert Greet.__name__ == "Greet"


def test_wagon_sets_marker():
    @wagon
    class Greet:
        def hi(self, who: str) -> str: ...

    assert Greet.__caravan_wagon__ is True


def test_wagon_empty_interface():
    @wagon
    class Empty:
        pass

    assert Empty.__caravan_wagon__ is True
    assert Empty.__caravan_methods__ == {}


# ---------------------------------------------------------------------------
# Real-shape: LLMExtraction.

@wagon
class LLMExtraction:
    """Mirror of invoice-parse's real interface, defined at module scope to match real use."""

    def extract(
        self,
        raw_ocr: FakeRawOcr | None = None,
        table_extraction: FakeTableExtraction | None = None,
    ) -> FakeInvoiceExtraction:
        ...


def test_real_seam_methods_captured():
    methods = LLMExtraction.__caravan_methods__
    assert set(methods) == {"extract"}


def test_real_seam_parameter_shape():
    sig = LLMExtraction.__caravan_methods__["extract"]
    assert list(sig.parameters) == ["self", "raw_ocr", "table_extraction"]
    # Real-world defaults: both args optional, default None.
    assert sig.parameters["raw_ocr"].default is None
    assert sig.parameters["table_extraction"].default is None
    # Both args are positional-or-keyword (no keyword-only marker in the real source).
    assert sig.parameters["raw_ocr"].kind is inspect.Parameter.POSITIONAL_OR_KEYWORD
    assert sig.parameters["table_extraction"].kind is inspect.Parameter.POSITIONAL_OR_KEYWORD


def test_real_seam_signature_supports_kwarg_bind():
    """The proxy at step 1d will call `sig.bind(**kwargs)` to normalize args.

    invoice-parse worker.py calls `extract(raw_ocr=..., table_extraction=...)` —
    keyword form. The captured signature must accept that.
    """
    sig = LLMExtraction.__caravan_methods__["extract"]
    raw = FakeRawOcr(text="hello")
    tab = FakeTableExtraction(rows=[["a", "b"]])

    bound = sig.bind(None, raw_ocr=raw, table_extraction=tab)  # `None` stands in for self
    bound.apply_defaults()
    assert bound.arguments["raw_ocr"] is raw
    assert bound.arguments["table_extraction"] is tab


def test_real_seam_signature_fills_defaults_on_partial_call():
    """Real call sites sometimes pass only one of the two args. Defaults must fill in."""
    sig = LLMExtraction.__caravan_methods__["extract"]
    raw = FakeRawOcr(text="only-raw")

    bound = sig.bind(None, raw_ocr=raw)
    bound.apply_defaults()
    assert bound.arguments["raw_ocr"] is raw
    assert bound.arguments["table_extraction"] is None


def test_real_seam_annotations_are_strings_under_future_annotations():
    """Sanity check: this test file uses `from __future__ import annotations`,
    so the captured signature carries string annotations, not resolved types.
    The codec at step 1c is responsible for resolution; wagon just preserves
    whatever ``inspect.signature`` returns.
    """
    sig = LLMExtraction.__caravan_methods__["extract"]
    raw_ann = sig.parameters["raw_ocr"].annotation
    # Under PEP 563 / `from __future__ import annotations`, this is a string form
    # like "FakeRawOcr | None". The exact form is Python-version-dependent but
    # always a string.
    assert isinstance(raw_ann, str)
    assert "FakeRawOcr" in raw_ann


# ---------------------------------------------------------------------------
# Underscore-prefix exclusion.

def test_wagon_excludes_underscore_methods():
    @wagon
    class WithUnderscored:
        def public(self) -> int: ...
        def _private(self) -> int: ...
        def __dunder__(self) -> int: ...

    assert set(WithUnderscored.__caravan_methods__) == {"public"}
