# Caravan.Rpc (.NET) — Pre-release placeholder

Runtime SDK for the [Caravan](https://github.com/paulxiep/caravan) application-definition compiler.

**Status: 0.0.1 pre-release placeholder.** This release reserves the NuGet package name. The functional SDK lands at 0.1.0.

## Install

```sh
dotnet add package Caravan.Rpc --version 0.0.1
```

## What is Caravan?

Caravan is an application-definition compiler. The same source code deploys across **packaging** (inproc / container / lambda) × **placement** (compose / Fargate / Lambda / Batch) × **composition** (oss-local / cloud-managed) axes by yaml-line changes alone — no source-code edits.

Read the [thesis](https://github.com/paulxiep/caravan/blob/main/docs/thesis.md) for the full pitch and the [PoC RPC SDK spec](https://github.com/paulxiep/caravan/blob/main/docs/poc_rpc_sdk.md) for what this package will become.

## When 0.1.0 ships

```csharp
using Caravan.Rpc;

public interface IEmbedder
{
    float[] Embed(string text);
}

// provider side
public class LocalEmbedder : IEmbedder
{
    public float[] Embed(string text) => /* ... */;
}

Wagon.Provide(typeof(IEmbedder), new LocalEmbedder());

// caller side — dispatches inproc / http / lambda per CARAVAN_RPC_PEERS
var embedder = Wagon.Client<IEmbedder>();
var vec = embedder.Embed("hello");
```

The 0.0.1 placeholder provides import-clean stubs but `Wagon.Client<T>()` throws `NotImplementedException` — it's not functional yet.

## Roadmap

- **0.0.1** (this release): reserve NuGet name, build-clean stubs.
- **0.1.0**: functional runtime. `Wagon.WagonOf`, `Wagon.Provide`, `Wagon.Client<T>` work with `CARAVAN_RPC_PEERS` env-var dispatch. See [development_plan.md](https://github.com/paulxiep/caravan/blob/main/docs/development_plan.md).

## License

Apache-2.0. See [LICENSE](LICENSE).
