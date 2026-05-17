# PoC inter-process RPC SDK (`supeux-rpc-<lang>`)

> The load-bearing primitive of the supeux thesis. User code writes `client::<Interface>().method()` once. The same call site dispatches as in-process function call (when provider lives in the same process), HTTP POST (when in a different container in the same compose runtime), or Lambda Function URL invoke (when in a different Lambda function) — without source-code edits. The yaml decides which.
>
> The SDK is the one structural contract supeux asks of the user. Within it, code is whatever the user wrote.
>
> Companion to [poc_groups_to_code.md](poc_groups_to_code.md) (data-plane / resource catalog). Both feed [poc_yaml_spec.md](poc_yaml_spec.md), where the yaml's seam-dispatch decisions drive what this SDK does at runtime.
>
> Upstream source of truth: [ir.md §4](ir.md#L225). This doc is the user-facing version with 4-language code surfaces.

## 1. Why it exists — and the structural buy-in it asks of the user

The thesis ([thesis.md:7-8](thesis.md#L7-L8)):
> An application is a graph of **modules** connected by interfaces. supeux lets one yaml project that graph onto any point in three orthogonal dimensions, with the source code unchanged.

The **packaging** dimension is the load-bearing one. Without a primitive that abstracts inter-process calls, users would write Flask / axum / Hono / chi HTTP plumbing inside every potential split point — hard-coding the assumption that peers are reachable over the network. Flipping to a single-process monolith would require deleting that plumbing; flipping to Lambda would require swapping it for `lambda.Invoke`. The "source code unchanged" claim collapses.

The supeux-rpc SDK is what makes the claim hold. **It is the one structural contract supeux asks of the user**: write inter-component calls that *might* split through the SDK, not through hand-rolled HTTP. Once that contract is observed, the seam can be deployed inproc / container / lambda by yaml choice alone, with no code edits.

**The unit of supeux's vocabulary is the seam.** A seam = an `@interface` declaration + its `provide(...)` impl + its `client(...)` call sites. Per seam, per target, [poc_yaml_spec.md](poc_yaml_spec.md) decides the dispatch mode.

### Without the SDK (anti-pattern, do not write this)

```python
# In the api code:
import httpx
resp = httpx.post(f"{os.environ['EMBEDDER_URL']}/embed", json={"text": "hi"})
vec = resp.json()["vector"]
```

```python
# In a separate embedder process, a hand-written Flask/FastAPI server:
from fastapi import FastAPI
app = FastAPI()
@app.post("/embed")
def embed(body: dict): return {"vector": [...]}
```

The api code assumes the embedder is reachable over HTTP. If both end up in the same process (monolith yaml), the HTTP call is a wasteful localhost round-trip; the user has to manually rewrite to a direct function call. `EMBEDDER_URL` env var, HTTP path, JSON shape — all user-defined; none adapt to packaging.

### With the SDK (correct pattern)

```python
# shared/interfaces.py — declared once, used by both sides:
from supeux_rpc import interface

@interface
class Embedder:
    def embed(self, text: str) -> list[float]: ...
```

```python
# embedder source — provider:
from supeux_rpc import provide
from shared.interfaces import Embedder

class EmbedderImpl(Embedder):
    def embed(self, text):
        return [...]  # actual embedding

provide(Embedder, EmbedderImpl())
```

```python
# api source — caller:
from supeux_rpc import client
from shared.interfaces import Embedder

embedder = client(Embedder)            # supeux looks up the provider by interface name
vec = embedder.embed("hi")             # dispatches inproc / http / lambda based on yaml decision
```

`api`'s code is identical whether `Embedder` runs in the same process, a sibling Fargate container, or a separate Lambda. The yaml decides; the SDK reads `SUPEUX_RPC_PEERS` at startup and routes each call accordingly. Same source, three lives.

### Why `client(Interface)` takes no peer-name argument

In an earlier draft the API was `client(Interface, target_module="embedder")` — the user had to name which peer hosts the interface. PoC drops that argument because **interface names are unique within a yaml** (phase-2 enforces this — multiple providers of the same interface in one yaml = phase-2 error). The interface name alone disambiguates. Less ceremony at every call site.

Multi-provider routing (load-balanced fan-out, geographic) is a v1 expansion.

## 2. Wire contract (language-agnostic)

All four SDKs speak the same wire format. Anyone can implement a 5th-language SDK from this section alone.

**HTTP/JSON v1.** No sidecar; no gRPC; all four runtimes have mature HTTP+JSON stacks. gRPC reconsidered at v2 if profiling demands ([ir.md §4](ir.md#L262)).

### Request

```
POST /_supeux/rpc/<interface>/<method>
Host: <peer-host>
Content-Type: application/json
X-Supeux-Rpc-Version: 1
Authorization: Bearer <shared-secret>          # compose / Fargate-internal modes
# OR
Authorization: AWS4-HMAC-SHA256 ...            # Lambda Function URL mode (SigV4)

{"args": [...], "kwargs": {...}}
```

- `<interface>` and `<method>` map directly to the `@interface`-decorated class/trait and its methods.
- `args` carries positional arguments in declaration order; `kwargs` carries named arguments. Languages without kwargs (Rust, Go) populate only `args`.
- JSON encoding: standard JSON for primitives + arrays + objects; binary fields use base64 in a `{"_bytes": "..."}` envelope; datetimes use ISO 8601 strings.

### Response (success)

```
HTTP/1.1 200 OK
Content-Type: application/json
X-Supeux-Rpc-Version: 1

{"ok": true, "result": <method-return-value>}
```

### Response (failure)

```
HTTP/1.1 200 OK                                # transport succeeded; logical failure carried in body
Content-Type: application/json

{"ok": false, "error": {"code": "<string>", "message": "<string>", "details"?: {...}}}
```

Transport-level failures (timeout, 5xx) propagate as language-native exceptions with `RpcTransportError` class. Logical failures (peer returned `{"ok": false}`) propagate as `RpcRemoteError(code, message)`.

### Auth

- **inproc mode**: no auth (direct function call; no HTTP at all).
- **http mode** (peer in a different container in compose or Fargate): shared bearer secret, injected by compiler phase 4 as `SUPEUX_RPC_SHARED_SECRET` env var on all deploy units in the same yaml.
- **lambda mode** (peer is a Lambda Function URL with `AuthType: AWS_IAM`): SigV4 signing using the caller's IAM role credentials (auto-derived per [ir.md §6a](ir.md#L313) — caller gets `lambda:InvokeFunctionUrl` on peer's Function URL ARN at compile time).

### Hidden ABI risk

Per [ir.md §7 risk #2](ir.md#L364): the wire-version (`X-Supeux-Rpc-Version`) commits supeux to a stable ABI. Breaking it forces lockstep upgrades across all 4 SDK packages and every deployed function. Treat the wire as frozen at v1; behavior additions go through optional headers; breaking changes wait for a coordinated v2.

## 3. Env-var contract

Phase 4 of the compiler injects three env vars per deploy unit.

| Env var | Value | Meaning |
|---|---|---|
| `SUPEUX_RPC_SELF` | `api` or `embedder` (yaml-derived name) | The deploy unit this process executes as |
| `SUPEUX_RPC_PEERS` | JSON dispatch table (see below), keyed by **interface name** | Per-seam dispatch mode + endpoint |
| `SUPEUX_RPC_SHARED_SECRET` | random per-deploy hex string | Bearer auth for `http` mode |

### `SUPEUX_RPC_PEERS` shape

```json
{
  "Embedder":   { "mode": "inproc" },
  "Billing":    { "mode": "http",   "url": "http://billing:8080" },
  "FraudCheck": { "mode": "lambda", "function_url": "https://abc.lambda-url.us-east-1.on.aws/" }
}
```

Keys are **interface names** (from the user's code, declared via `@interface`).
Values name the dispatch mode + endpoint for the seam.

Phase-4 computation rules:

- Target's `seams: { <Interface>: inproc }` (or omitted) → `mode: inproc`. The deploy unit's binary contains the `provide(...)` impl; SDK dispatches directly. Same for every deploy unit's table — the inproc seam is present-everywhere because the binary carries the code.
- Target's `seams: { <Interface>: container }` → `mode: http`, `url: http://<seam-name>:<port>`. The seam is its own compose service / Fargate task.
- Target's `seams: { <Interface>: lambda }` → `mode: lambda`, `function_url: <peer Function URL with AuthType=AWS_IAM>`.

The local `provide(...)` call still runs at startup in every deploy unit's binary (registers into the inproc registry), but the SDK consults the peer table per-call: if the interface's mode is `http` or `lambda` on this unit, the dispatch goes external regardless of whether a local impl exists. The local impl becomes inert (size cost but no correctness issue).

## 4. Per-language SDK surface

All four SDKs expose the same conceptual API (`interface` declaration, `provide`, `client`) with language-idiomatic syntax.

### 4.1 Python

```python
# shared/interfaces.py
from supeux_rpc import interface

@interface
class Embedder:
    def embed(self, text: str) -> list[float]: ...
    def embed_batch(self, texts: list[str]) -> list[list[float]]: ...
```

```python
# embedder source
from supeux_rpc import provide
from shared.interfaces import Embedder

class EmbedderImpl(Embedder):
    def embed(self, text):
        return self._model.encode(text).tolist()
    def embed_batch(self, texts):
        return [self.embed(t) for t in texts]

provide(Embedder, EmbedderImpl())
```

```python
# api source
from supeux_rpc import client
from shared.interfaces import Embedder

embedder = client(Embedder)
vec = embedder.embed("hello world")
```

**Runtime reflection v1**: `@interface` captures method signatures via `inspect.signature`; argument types from annotations drive JSON (de)serialization. `provide(Cls, instance)` registers into the inproc registry plus, if any peer in `SUPEUX_RPC_PEERS` (anywhere in the deploy) marks this deploy unit as the http/lambda target for this interface, the SDK starts a lightweight HTTP server (uvicorn / aiohttp) on the listen port serving `/_supeux/rpc/...`. `client(Cls)` returns a proxy whose attribute access creates per-method dispatchers reading the peer entry.

### 4.2 Rust

```rust
// shared/src/interfaces.rs
use supeux_rpc::interface;

#[interface]
pub trait Embedder: Send + Sync {
    async fn embed(&self, text: String) -> Vec<f32>;
    async fn embed_batch(&self, texts: Vec<String>) -> Vec<Vec<f32>>;
}
```

```rust
// embedder/src/lib.rs
use shared::interfaces::Embedder;

pub struct EmbedderImpl { model: FastEmbed }

#[async_trait::async_trait]
impl Embedder for EmbedderImpl {
    async fn embed(&self, text: String) -> Vec<f32> {
        self.model.embed(&text).await
    }
    async fn embed_batch(&self, texts: Vec<String>) -> Vec<Vec<f32>> {
        self.model.embed_batch(&texts).await
    }
}

pub fn register() {
    supeux_rpc::provide::<dyn Embedder>(Box::new(EmbedderImpl::new()));
}
```

```rust
// api binary entry — main.rs
use shared::interfaces::Embedder;

#[tokio::main]
async fn main() {
    embedder::register();              // when monolith, registers the inproc impl
    let embedder = supeux_rpc::client::<dyn Embedder>().await;
    // ... handle HTTP requests, call embedder.embed(text).await ...
    supeux_rpc::serve_forever().await;
}
```

**Codegen at compile time**: `#[interface]` proc-macro emits two impls alongside the trait — an HTTP-server adapter that decodes JSON args / dispatches to the provided impl / encodes the result, and an HTTP-client adapter that implements the trait by POSTing to the peer. `provide::<dyn T>` registers; `client::<dyn T>` returns the right adapter (inproc-direct or HTTP-client) based on the runtime peer table.

### 4.3 TypeScript

```typescript
// shared/interfaces.ts
import { defineInterface } from "supeux-rpc";

export interface Embedder {
  embed(text: string): Promise<number[]>;
  embedBatch(texts: string[]): Promise<number[][]>;
}

export const EmbedderToken = defineInterface<Embedder>("Embedder");
```

```typescript
// embedder source
import { provide, serveForever } from "supeux-rpc";
import { EmbedderToken, type Embedder } from "../shared/interfaces.js";

class EmbedderImpl implements Embedder {
  async embed(text: string) { return await this.model.encode(text); }
  async embedBatch(texts: string[]) { return Promise.all(texts.map(t => this.embed(t))); }
}

provide(EmbedderToken, new EmbedderImpl());
await serveForever();
```

```typescript
// api source
import { client } from "supeux-rpc";
import { EmbedderToken } from "../shared/interfaces.js";

const embedder = client(EmbedderToken);
const vec = await embedder.embed("hello world");
```

**Runtime reflection v1.** TypeScript types are erased at runtime, so the token (`defineInterface<I>(name)`) carries the runtime identity. `client(token)` returns a `Proxy` that intercepts method calls and dispatches per the peer table.

### 4.4 Go

```go
// shared/interfaces/embedder.go
package interfaces

//go:generate supeux gen-rpc Embedder

type Embedder interface {
    Embed(ctx context.Context, text string) ([]float32, error)
    EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}
```

```go
// embedder source
package main

import (
    "github.com/example/myapp/shared/interfaces"
    "github.com/anthropics/supeux-rpc-go"
)

type embedderImpl struct{ model FastEmbed }

func (e *embedderImpl) Embed(ctx context.Context, text string) ([]float32, error) { return e.model.Embed(ctx, text) }
func (e *embedderImpl) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
    out := make([][]float32, len(texts))
    for i, t := range texts { v, _ := e.Embed(ctx, t); out[i] = v }
    return out, nil
}

func main() {
    supeuxrpc.Provide[interfaces.Embedder](&embedderImpl{model: NewFastEmbed()})
    supeuxrpc.ServeForever()
}
```

```go
// api source
package main

import (
    "github.com/example/myapp/shared/interfaces"
    "github.com/anthropics/supeux-rpc-go"
)

func main() {
    embedder := supeuxrpc.Client[interfaces.Embedder]()
    vec, _ := embedder.Embed(context.Background(), "hello world")
}
```

**Codegen at v1.** `go generate` runs `supeux gen-rpc` which reads the interface and emits server + client adapter files.

## 5. Dispatch modes — pseudocode

```
dispatcher(interface_name, method_name, args, kwargs):
  peer = SUPEUX_RPC_PEERS[interface_name]

  match peer.mode:
    case "inproc":
      impl = INPROC_REGISTRY[interface_name]
      return impl.<method_name>(*args, **kwargs)

    case "http":
      response = http_post(
        url     = f"{peer.url}/_supeux/rpc/{interface_name}/{method_name}",
        json    = {"args": args, "kwargs": kwargs},
        headers = {
          "Authorization":         f"Bearer {SUPEUX_RPC_SHARED_SECRET}",
          "X-Supeux-Rpc-Version":  "1",
        },
      )
      body = response.json()
      if body["ok"]: return body["result"]
      else: raise RpcRemoteError(body["error"]["code"], body["error"]["message"])

    case "lambda":
      response = http_post(
        url    = f"{peer.function_url}_supeux/rpc/{interface_name}/{method_name}",
        json   = {"args": args, "kwargs": kwargs},
        auth   = SigV4(service="lambda", region=AWS_REGION, credentials=DefaultCredentialProvider()),
        headers= {"X-Supeux-Rpc-Version": "1"},
      )
      # same body handling as http mode
```

Each language SDK implements this dispatcher idiomatically.

## 6. Library home + dep injection

- **Monorepo layout**: `/sdk/python/`, `/sdk/rust/`, `/sdk/typescript/`, `/sdk/go/` — confirmed in [considerations.md item B](considerations.md).
- **Per-language native packaging**:
  - Python → PyPI: `supeux-rpc`
  - Rust → crates.io: `supeux-rpc`
  - TypeScript → npm: `@supeux/rpc`
  - Go → `github.com/<org>/supeux-rpc-go`

**Supeux auto-patches the user's package manifest** to add the SDK dep. The user does *not* need to remember to `pip install supeux-rpc` or `cargo add supeux-rpc`. See [poc_yaml_spec.md "Manifest patching"](poc_yaml_spec.md#manifest-patching).

## 7. Out of PoC scope

- **Multiple providers per interface** (load-balanced fan-out, geographic routing). PoC enforces one provider per interface; phase-2 errors on duplicates.
- **Chained seams** (a seam that calls another seam). PoC supports seam-as-leaf; chains are v1.
- **Streaming RPC** (server-streamed iterators, bidirectional). v1 is request/response only.
- **Codegen for Python & TypeScript** (runtime reflection v1; codegen v2). Rust + Go are codegen-from-day-one because their type systems demand it.
- **Cross-language IDL** (a `.supeux-rpc` schema file generating stubs in all 4 languages). v1 uses each language's native interface declaration.
- **Retry / circuit breaker / trace propagation.** v1 surfaces transport errors as raw exceptions and does not auto-forward trace context.

For everything above: the user can drop down to direct HTTP (using their web framework) for the specific call site that needs it, without abandoning the SDK for other seams.

---

See [poc_groups_to_code.md](poc_groups_to_code.md) for the data-plane (resource catalog), and [poc_yaml_spec.md](poc_yaml_spec.md) for the entries + seams + targets yaml shape that drives this SDK's dispatch decisions.
