# caravan-rpc (Ruby) — Pre-release placeholder

Runtime SDK for the [Caravan](https://github.com/paulxiep/caravan) application-definition compiler.

**Status: 0.0.1 pre-release placeholder.** This release reserves the RubyGems name. The functional SDK lands at 0.1.0.

## Install

```sh
gem install caravan-rpc -v 0.0.1
```

or in a Gemfile:

```ruby
gem "caravan-rpc", "~> 0.0.1"
```

## What is Caravan?

Caravan is an application-definition compiler. The same source code deploys across **packaging** (inproc / container / lambda) × **placement** (compose / Fargate / Lambda / Batch) × **composition** (oss-local / cloud-managed) axes by yaml-line changes alone — no source-code edits.

Read the [thesis](https://github.com/paulxiep/caravan/blob/main/docs/thesis.md) for the full pitch and the [PoC RPC SDK spec](https://github.com/paulxiep/caravan/blob/main/docs/poc_rpc_sdk.md) for what this package will become.

## When 0.1.0 ships

```ruby
require "caravan/rpc"

class Embedder
  def embed(text); end
end

# provider side
class LocalEmbedder < Embedder
  def embed(text)
    # ...
  end
end

Caravan::Rpc.provide(Embedder, LocalEmbedder.new)

# caller side — dispatches inproc / http / lambda per CARAVAN_RPC_PEERS
embedder = Caravan::Rpc.client(Embedder)
vec = embedder.embed("hello")
```

The 0.0.1 placeholder provides require-clean stubs but `Caravan::Rpc.client` raises `NotImplementedError` — it's not functional yet.

## Roadmap

- **0.0.1** (this release): reserve RubyGems name, require-clean stubs.
- **0.1.0**: functional runtime. `wagon`, `provide`, `client` work with `CARAVAN_RPC_PEERS` env-var dispatch. See [development_plan.md](https://github.com/paulxiep/caravan/blob/main/docs/development_plan.md).

## License

Apache-2.0. See [LICENSE](LICENSE).
