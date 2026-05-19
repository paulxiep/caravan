# caravan_rpc (Elixir / Erlang) — Pre-release placeholder

Runtime SDK for the [Caravan](https://github.com/paulxiep/caravan) application-definition compiler.

**Status: 0.0.1 pre-release placeholder.** This release reserves the Hex.pm name. The functional SDK lands at 0.1.0.

## Install

```elixir
def deps do
  [
    {:caravan_rpc, "~> 0.0.1"}
  ]
end
```

## What is Caravan?

Caravan is an application-definition compiler. The same source code deploys across **packaging** (inproc / container / lambda) × **placement** (compose / Fargate / Lambda / Batch) × **composition** (oss-local / cloud-managed) axes by yaml-line changes alone — no source-code edits.

Read the [thesis](https://github.com/paulxiep/caravan/blob/main/docs/thesis.md) for the full pitch and the [PoC RPC SDK spec](https://github.com/paulxiep/caravan/blob/main/docs/poc_rpc_sdk.md) for what this package will become.

## When 0.1.0 ships

```elixir
defmodule Embedder do
  @callback embed(String.t()) :: [float()]
end

defmodule LocalEmbedder do
  @behaviour Embedder
  def embed(_text), do: [0.1, 0.2, 0.3]
end

CaravanRpc.wagon(Embedder)
CaravanRpc.provide(Embedder, LocalEmbedder)

# caller side — dispatches inproc / http / lambda per CARAVAN_RPC_PEERS
embedder = CaravanRpc.client(Embedder)
vec = embedder.embed("hello")
```

The 0.0.1 placeholder provides compile-clean stubs but `CaravanRpc.client/1` raises — it's not functional yet.

## Roadmap

- **0.0.1** (this release): reserve Hex.pm name, compile-clean stubs.
- **0.1.0**: functional runtime. `wagon/1`, `provide/2`, `client/1` work with `CARAVAN_RPC_PEERS` env-var dispatch. See [development_plan.md](https://github.com/paulxiep/caravan/blob/main/docs/development_plan.md).

## License

Apache-2.0. See [LICENSE](LICENSE).
