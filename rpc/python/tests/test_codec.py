"""Tests for the wire codec (B0 step 1c).

Real-shape: types mirror invoice-parse's ``RawOcrOutput`` (dataclass with nested
lists), ``TableExtractionOutput`` (dataclass), ``InvoiceExtraction`` (Pydantic
model). Tests cover encode/decode roundtrip + JSON wire-form roundtrip (encode
→ json.dumps → json.loads → decode), which is what the proxy + serve handler
will do at runtime.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field

import pytest
from pydantic import BaseModel

from caravan_rpc import wagon
from caravan_rpc._codec import (
    _clear_cache,
    decode_call,
    decode_return,
    encode_call,
    encode_return,
)


# ---------------------------------------------------------------------------
# Fixtures shaped like invoice-parse's real types.

@dataclass
class FakeRawOcr:
    text: str = ""
    confidence: float = 0.0
    boxes: list[list[float]] = field(default_factory=list)


@dataclass
class FakeTableExtraction:
    rows: list[list[str]] | None = None
    page_index: int = 0


class FakeLineItem(BaseModel):
    description: str
    amount: float


class FakeInvoiceExtraction(BaseModel):
    vendor: str
    total: float
    line_items: list[FakeLineItem] = []


@wagon
class LLMExtraction:
    def extract(
        self,
        raw_ocr: FakeRawOcr | None = None,
        table_extraction: FakeTableExtraction | None = None,
    ) -> FakeInvoiceExtraction:
        ...


@pytest.fixture(autouse=True)
def _isolate_cache():
    _clear_cache()
    yield
    _clear_cache()


# ---------------------------------------------------------------------------
# Encode produces JSON-safe envelopes.

def test_encode_call_dataclass_args_to_jsonable_kwargs():
    raw = FakeRawOcr(text="invoice text", confidence=0.92, boxes=[[1.0, 2.0]])
    tab = FakeTableExtraction(rows=[["a", "b"]], page_index=1)
    envelope = encode_call(LLMExtraction, "extract", raw_ocr=raw, table_extraction=tab)

    # Envelope is wire-shaped:
    assert envelope["args"] == []
    assert set(envelope["kwargs"]) == {"raw_ocr", "table_extraction"}

    # Dataclasses encoded as JSON-safe dicts.
    assert envelope["kwargs"]["raw_ocr"] == {
        "text": "invoice text",
        "confidence": 0.92,
        "boxes": [[1.0, 2.0]],
    }
    assert envelope["kwargs"]["table_extraction"] == {
        "rows": [["a", "b"]],
        "page_index": 1,
    }


def test_encode_call_optional_none_defaults_round_trip():
    """Caller passes nothing; defaults of None for both args should serialize as null."""
    envelope = encode_call(LLMExtraction, "extract")
    assert envelope["kwargs"] == {"raw_ocr": None, "table_extraction": None}


def test_encode_call_partial_kwargs_fills_defaults():
    raw = FakeRawOcr(text="only-raw")
    envelope = encode_call(LLMExtraction, "extract", raw_ocr=raw)
    # `table_extraction` filled with its default (None).
    assert envelope["kwargs"]["table_extraction"] is None
    assert envelope["kwargs"]["raw_ocr"]["text"] == "only-raw"


def test_encode_call_positional_args_bound_to_kwargs():
    """Wire normalizes positional → kwargs via Signature.bind."""
    raw = FakeRawOcr(text="positional")
    envelope = encode_call(LLMExtraction, "extract", raw)
    assert envelope["kwargs"]["raw_ocr"]["text"] == "positional"
    assert envelope["args"] == []


# ---------------------------------------------------------------------------
# Decode reconstructs typed objects.

def test_decode_call_reconstructs_dataclass_and_optional_none():
    envelope = {
        "args": [],
        "kwargs": {
            "raw_ocr": {"text": "decoded", "confidence": 0.5, "boxes": []},
            "table_extraction": None,
        },
    }
    args, kwargs = decode_call(LLMExtraction, "extract", envelope)
    assert args == []
    assert isinstance(kwargs["raw_ocr"], FakeRawOcr)
    assert kwargs["raw_ocr"].text == "decoded"
    assert kwargs["raw_ocr"].confidence == 0.5
    assert kwargs["table_extraction"] is None


# ---------------------------------------------------------------------------
# Full JSON wire roundtrip (encode → json.dumps → json.loads → decode).

def test_full_wire_roundtrip_dataclass_args():
    raw = FakeRawOcr(text="wire", confidence=0.7, boxes=[[0.0, 0.0], [1.0, 1.0]])
    tab = FakeTableExtraction(rows=[["x"]], page_index=3)

    encoded = encode_call(LLMExtraction, "extract", raw_ocr=raw, table_extraction=tab)
    wire = json.dumps(encoded)
    received = json.loads(wire)
    args, kwargs = decode_call(LLMExtraction, "extract", received)

    assert args == []
    assert kwargs["raw_ocr"] == raw
    assert kwargs["table_extraction"] == tab


def test_full_wire_roundtrip_pydantic_return():
    result = FakeInvoiceExtraction(
        vendor="ACME",
        total=123.45,
        line_items=[FakeLineItem(description="widget", amount=12.34)],
    )

    encoded = encode_return(LLMExtraction, "extract", result)
    wire = json.dumps(encoded)
    received = json.loads(wire)
    decoded = decode_return(LLMExtraction, "extract", received)

    assert isinstance(decoded, FakeInvoiceExtraction)
    assert decoded.vendor == "ACME"
    assert decoded.total == 123.45
    assert decoded.line_items[0].description == "widget"
    assert decoded.line_items[0].amount == 12.34


def test_pydantic_return_validates_dict_form_from_wire():
    """The server side may dump_python the impl's return; the client receives a dict
    and must reconstruct the Pydantic model.
    """
    raw = {"vendor": "X", "total": 0.0, "line_items": []}
    decoded = decode_return(LLMExtraction, "extract", raw)
    assert isinstance(decoded, FakeInvoiceExtraction)
    assert decoded.vendor == "X"


# ---------------------------------------------------------------------------
# Edge cases.

def test_codec_rejects_non_wagon_interface():
    class NotAnInterface:
        def method(self) -> int: ...

    with pytest.raises(TypeError, match="not @wagon-decorated"):
        encode_call(NotAnInterface, "method")


def test_codec_unknown_method_name_raises():
    with pytest.raises(LookupError, match="extract_typo"):
        encode_call(LLMExtraction, "extract_typo")


@wagon
class PrimitiveSeam:
    def add(self, a: int, b: int) -> int: ...


def test_codec_primitive_args_and_return():
    envelope = encode_call(PrimitiveSeam, "add", a=2, b=3)
    assert envelope["kwargs"] == {"a": 2, "b": 3}

    args, kwargs = decode_call(PrimitiveSeam, "add", envelope)
    assert kwargs == {"a": 2, "b": 3}

    encoded_ret = encode_return(PrimitiveSeam, "add", 5)
    assert encoded_ret == 5
    assert decode_return(PrimitiveSeam, "add", encoded_ret) == 5


@wagon
class UnannotatedSeam:
    def passthrough(self, x): ...  # no type annotation


def test_codec_unannotated_param_passes_through():
    envelope = encode_call(UnannotatedSeam, "passthrough", x={"any": "shape"})
    assert envelope["kwargs"] == {"x": {"any": "shape"}}

    args, kwargs = decode_call(UnannotatedSeam, "passthrough", envelope)
    assert kwargs == {"x": {"any": "shape"}}
