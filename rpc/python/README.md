# caravan-rpc (Python) — Pre-release placeholder

Runtime SDK for the [Caravan](https://github.com/paulxiep/caravan) application-definition compiler.

**Status: 0.0.1 pre-release placeholder.** This release reserves the PyPI name. The functional SDK lands at 0.1.0.

## What is Caravan?

Caravan is an application-definition compiler. The same source code deploys across **packaging** (inproc / container / lambda) × **placement** (compose / Fargate / Lambda / Batch) × **composition** (oss-local / cloud-managed) axes by yaml-line changes alone — no source-code edits.

Read the [thesis](https://github.com/paulxiep/caravan/blob/main/docs/thesis.md) for the full pitch and the [PoC RPC SDK spec](https://github.com/paulxiep/caravan/blob/main/docs/poc_rpc_sdk.md) for what this package will become.

## When 0.1.0 ships

```python
from caravan_rpc import wagon, provide, client

@wagon
class LLMExtraction:
    def extract(self, ocr_text: str) -> dict: ...

# provider side
class GeminiExtractor(LLMExtraction):
    def extract(self, ocr_text):
        ...

provide(LLMExtraction, GeminiExtractor())

# caller side — dispatches inproc / http / lambda per CARAVAN_RPC_PEERS
extractor = client(LLMExtraction)
result = extractor.extract(text)
```

The 0.0.1 placeholder provides import-clean stubs but `client()` raises `NotImplementedError` — it's not functional yet.

## Roadmap

- **0.0.1** (this release): reserve PyPI name, import-clean stubs.
- **0.1.0**: functional runtime. `@wagon`, `provide`, `client` work with `CARAVAN_RPC_PEERS` env-var dispatch. See [development_plan.md](https://github.com/paulxiep/caravan/blob/main/docs/development_plan.md) milestones B0–M3.

## License

Apache-2.0. See [LICENSE](LICENSE).
