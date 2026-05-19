# caravan-rpc (TypeScript / npm) — Pre-release placeholder

Runtime SDK for the [Caravan](https://github.com/paulxiep/caravan) application-definition compiler.

**Status: 0.0.1 pre-release placeholder.** This release reserves the npm name. The functional SDK lands at 0.1.0.

## What is Caravan?

Caravan is an application-definition compiler. The same source code deploys across **packaging** (inproc / container / lambda) × **placement** (compose / Fargate / Lambda / Batch) × **composition** (oss-local / cloud-managed) axes by yaml-line changes alone — no source-code edits.

Read the [thesis](https://github.com/paulxiep/caravan/blob/main/docs/thesis.md) and the [PoC RPC SDK spec](https://github.com/paulxiep/caravan/blob/main/docs/poc_rpc_sdk.md).

## When 0.1.0 ships

```typescript
import { defineWagon, provide, client } from "caravan-rpc";

interface Embedder {
  embed(text: string): Promise<number[]>;
}

export const EmbedderToken = defineWagon<Embedder>("Embedder");

// provider side
class FastEmbedImpl implements Embedder {
  async embed(text: string) { return [/* ... */]; }
}

provide(EmbedderToken, new FastEmbedImpl());

// caller side — dispatches inproc / http / lambda per CARAVAN_RPC_PEERS
const embedder = client(EmbedderToken);
const v = await embedder.embed("hello");
```

The 0.0.1 placeholder exposes `defineWagon`, `provide`, `client` symbols but `client()` throws.

## Roadmap

- **0.0.1** (this release): reserve npm name, import-clean stubs.
- **0.1.0**: functional runtime. Proxy-based client dispatch. See [development_plan.md](https://github.com/paulxiep/caravan/blob/main/docs/development_plan.md).

## License

Apache-2.0. See [LICENSE](LICENSE).
