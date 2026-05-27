# caravan-rpc (Python)

Runtime SDK for the [Caravan](https://github.com/paulxiep/caravan) application-definition compiler.

**Status: 0.1.1.** Functional runtime with `@wagon`, `provide`, `client`, `CARAVAN_RPC_PEERS` env-var dispatch (inproc / HTTP / Lambda Function URL via SigV4), peer-mode self-call guard, base64-encoded `bytes` over the wire, and Caravan-shipped resource adapters (BlobStore, MessageQueue) for S3 / Redis / RabbitMQ / SQS.

## What is Caravan?

Caravan is an application-definition compiler. The same source code deploys across **packaging** (inproc / container / lambda) × **placement** (compose / Fargate / Lambda) × **composition** (oss-local / cloud-managed) axes by yaml-line changes alone — no source-code edits.

Read the [thesis](https://github.com/paulxiep/caravan/blob/main/docs/thesis.md) and the [PoC RPC SDK spec](https://github.com/paulxiep/caravan/blob/main/docs/poc_rpc_sdk.md).

## Install

```bash
pip install caravan-rpc>=0.1.1
```

Optional extras (each pulls in its backend client only when needed):

```bash
pip install "caravan-rpc[aws,redis,rabbit,lambda]>=0.1.1"
# aws    — boto3 for S3BlobStore + SqsQueue.
# redis  — redis-py for RedisStreamQueue.
# rabbit — pika for RabbitMQQueue.
# lambda — botocore SigV4 signer for Lambda Function URL client dispatch.
# all    — convenience meta-extra.
```

## Three-point structural contract

User code interacts with Caravan through three SDK entry points; everything else is compiler-managed:

```python
from caravan_rpc import wagon, provide, client

# 1. @wagon declares an interface as a Caravan seam — a synchronous
#    abstraction boundary that yaml can flip between inproc / HTTP /
#    Lambda dispatch per target.
@wagon
class LLMExtraction:
    def extract(self, ocr_text: str, file_bytes: bytes) -> InvoiceExtraction: ...

# 2. provide() registers a concrete impl at process startup.
class GeminiExtractor:
    def extract(self, ocr_text, file_bytes):
        ...

provide(LLMExtraction, GeminiExtractor())

# 3. client() dispatches a call — inproc, HTTP, or Lambda per the
#    `CARAVAN_RPC_PEERS` env var the compiler emits per target.
def run(pdf_bytes: bytes, ocr_text: str):
    extractor = client(LLMExtraction)
    return extractor.extract(ocr_text, pdf_bytes)
```

`bytes` arguments are JSON-encoded as base64 over the wire (set via `pydantic.ConfigDict(ser_json_bytes="base64")` on the codec's `TypeAdapter`). Binary payloads — PDFs, images — cross HTTP / Lambda cleanly without UTF-8 decode failures.

## Dispatch modes

`CARAVAN_RPC_PEERS` is a per-deploy-unit JSON map the compiler emits:

```json
{
  "LLMExtraction":     {"mode": "http",   "url": "http://llm-extractor:8080"},
  "OCRText":           {"mode": "inproc"},
  "ValidateExtraction": {"mode": "lambda", "function_url": "https://...lambda-url.ap-southeast-1.on.aws/"}
}
```

- `inproc` → `client(I).method` returns the registered impl's bound method directly (zero overhead, no-config-inert).
- `http`  → returns a callable that POSTs to `/_caravan/rpc/<iface>/<method>` with a Bearer token.
- `lambda` → SigV4-signed POST to the Lambda Function URL (requires `[lambda]` extra).

Peer-mode self-call guard: when `CARAVAN_RPC_ROLE=peer-<Interface>` matches the served interface, `client(I)` falls through to the local impl instead of an HTTP dispatcher pointing back at this same container. Peer containers share the consumer's `CARAVAN_RPC_PEERS`, so without the guard requests would loop.

## Peer-mode entry: `python -m caravan_rpc.serve`

Caravan emits compose peer services that invoke this module:

```
python -m caravan_rpc.serve --interface LLMExtraction \
    --impl invoice_processing.extraction:GeminiExtractor \
    --port 8080
```

The CLI imports the impl, calls `provide()`, then serves `@wagon` methods on `/_caravan/rpc/<iface>/<method>`. Same wire contract the `client(I)` HTTP dispatcher targets.

## Resource adapters

Caravan-shipped impls of common resource seams (`BlobStore`, `MessageQueue`):

```python
from caravan_rpc.resources import auto_register_resources, BlobStore
from caravan_rpc import client

def main():
    with open("config/app.yaml") as f:
        cfg = yaml.safe_load(f)
    auto_register_resources(yaml_fallback=cfg)

    blob = client(BlobStore)
    blob.put("input.pdf", pdf_bytes)
```

Backend selection is driven by explicit Caravan-emitted markers:

- `CARAVAN_BLOB_BACKEND=s3` + `S3_BUCKET` set → `S3BlobStore` (real AWS or MinIO via `S3_ENDPOINT_URL`).
- `CARAVAN_BLOB_BACKEND=local-fs` → `LocalFsBlobStore` rooted at the path from `yaml_fallback.blob_storage.base_path` (default `/data/blobs`).
- Marker unset → consult `yaml_fallback`. Non-caravan local-dev path.

`CARAVAN_BLOB_BACKEND=s3` with no `S3_BUCKET` loud-fails at startup (catches the "user forgot to populate `.env.hybrid` from `tofu output`" footgun).

`MessageQueue` selects on `QUEUE_URL` scheme: `redis://` → `RedisStreamQueue`, `amqp://` → `RabbitMQQueue`, `https://` → `SqsQueue`.

## Versions

- **0.1.1**: peer-mode self-call guard; `CARAVAN_BLOB_BACKEND` explicit marker; bytes serialized as base64 (was utf-8 default in pydantic, which crashed on binary payloads). SDK version bumped alongside Rust 0.1.1 for matching wire-protocol semantics.
- **0.1.0**: first functional release. `@wagon` / `provide` / `client` with `CARAVAN_RPC_PEERS` env-var dispatch; HTTP + Lambda SigV4 client dispatchers; `caravan_rpc.serve` peer CLI; `caravan_rpc.lambda_handler` for Function URL event v2.0; resource adapters.
- **0.0.1**: PyPI name reservation placeholder.

See [development_plan.md](https://github.com/paulxiep/caravan/blob/main/docs/development_plan.md) for the full milestone history.

## License

Apache-2.0. See [LICENSE](LICENSE).
