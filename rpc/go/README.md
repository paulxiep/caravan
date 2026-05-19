# Caravan Go SDK — Pre-release placeholder

Runtime SDK for the [Caravan](https://github.com/paulxiep/caravan) application-definition compiler.

**Status: v0.0.1 pre-release placeholder.** This release reserves the Go module import path. The functional SDK lands at v0.1.0.

## Import path

```go
import caravanrpc "github.com/paulxiep/caravan/rpc/go"
```

The SDK lives at `rpc/go/` inside the Caravan monorepo and is versioned via Go's monorepo tag convention: `rpc/go/v<version>`. No separate `caravan-rpc-go` repo — `go get github.com/paulxiep/caravan/rpc/go@v0.0.1` resolves directly from this repo's tag.

## What is Caravan?

Caravan is an application-definition compiler. The same source code deploys across **packaging** (inproc / container / lambda) × **placement** (compose / Fargate / Lambda / Batch) × **composition** (oss-local / cloud-managed) axes by yaml-line changes alone — no source-code edits.

Read the [thesis](https://github.com/paulxiep/caravan/blob/main/docs/thesis.md) and the [PoC RPC SDK spec](https://github.com/paulxiep/caravan/blob/main/docs/poc_rpc_sdk.md).

## When v0.1.0 ships

```go
package main

import (
    "context"
    caravanrpc "github.com/paulxiep/caravan/rpc/go"
)

//go:generate caravan gen-wagon Embedder

type Embedder interface {
    Embed(ctx context.Context, text string) ([]float32, error)
}

// caller side — dispatches inproc / http / lambda per CARAVAN_RPC_PEERS
func main() {
    embedder := caravanrpc.Client /* [Embedder] */ ()
    _ = embedder
}
```

The v0.0.1 placeholder exposes `Wagon`, `Provide`, `Client` as no-op functions; `Client` panics.

## Publishing (maintainers only)

The workflow at [`.github/workflows/publish-go-sdk.yml`](https://github.com/paulxiep/caravan/blob/main/.github/workflows/publish-go-sdk.yml) tags `rpc/go/v<version>` and pings `proxy.golang.org` to index it. No PAT or external repo needed — uses the auto-provided `GITHUB_TOKEN` scoped to this repo. Manual trigger only.

## Roadmap

- **v0.0.1** (this release): reserve import path, build-clean stubs.
- **v0.1.0**: functional runtime with `caravan gen-wagon` codegen, generics-typed `Client[T]()` dispatcher, `CARAVAN_RPC_PEERS` env-var dispatch. See [development_plan.md](https://github.com/paulxiep/caravan/blob/main/docs/development_plan.md).

## License

Apache-2.0. See [LICENSE](LICENSE).
