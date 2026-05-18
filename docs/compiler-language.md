# Compiler Language — Go vs Rust vs TypeScript

> **2026-05-16 update.** Earlier framings of this document leaned on `thesis.md:63` ("Pulumi-Go-as-CLI-internal is the next move") as a same-language argument for **Go** — since in-process Pulumi-Go embedding requires the host to be Go too. That framing is now downgraded: the Pulumi fallback is contingent at v1 and subprocess works from any language, so it's no longer a §4.1 criterion (see §4.2 — Pulumi appears as a Go-side *option*, not a requirement). The decision still lands on Go, but the rationale rests on `hclwrite` + `tfexec` + distribution + dogfooding, not on the Pulumi-embed argument. v4 §7d.7's "derived rather than open" reading was stronger than the body now supports. §5 recommendation and §5.1 flip-triggers are the authoritative view.

> Three layers of analysis, each narrower than the last. Each layer filters the candidates further.
>
> **§2 Generic-generic** — what each language *is*, use-case agnostic. The widest view: language profiles, sweet spots, philosophies.
>
> **§3 CLI compiler generic** — narrowed to "static-binary CLI that compiles config files." Strengths and weaknesses for this category of tool, independent of caravan.
>
> **§4 Caravan-specific** — narrowed to caravan's actual requirements. This is where Go's case gets the detailed treatment because caravan's needs amplify Go's CLI-compiler strengths.
>
> Scope: language for the caravan compiler binary that users invoke. Not user code. Not the `caravan-rpc-*` runtime adapters (those are Python + Rust regardless).

---

## 1. Framing

Three finalists after upstream eliminations (Python, uncompiled TS, Zig, OCaml/Haskell — see [architecture-brainstorm.md §9.2](architecture-brainstorm.md)): **Go**, **Rust**, **TypeScript** (compiled to static binary via bun / deno).

Each is defensible at the widest view. The narrowing is what picks one.

---

## 2. Generic-generic — what each language is for

Zoomed all the way out. No assumption about what kind of tool is being built.

### 2.1 Go

Designed at Google (2009) for large-scale software at industrial volume. Opinionated, simple, garbage-collected, statically typed. Goroutines + channels for concurrency (CSP-style). Sub-second compile. Strong standard library. Designed for "10 engineers, 1M lines, 5 years."

- **Sweet spots:** distributed services, infrastructure software, CLI tools, build pipelines.
- **Famous tools written in Go:** Docker, Kubernetes, Terraform, OpenTofu, Pulumi (engine), Prometheus, Hugo, GitHub CLI, SST (engine), etcd, Caddy.
- **Philosophy:** maintainability over expressiveness. Every Go programmer reads the same idioms. The language refuses features that would let teams diverge in style (no operator overloading, no inheritance, generics added late and kept minimal). This is why Go won the cloud-infrastructure market.

### 2.2 Rust

Designed at Mozilla (2010, stable 2015) for memory-safe systems programming without GC. Ownership model, algebraic data types, exhaustive pattern matching, zero-cost abstractions. Slow compiles, steep learning curve, top-tier LSP (`rust-analyzer`).

- **Sweet spots:** systems software, browsers, OS / embedded, performance-critical paths, parsers and compilers (it's an excellent compiler language).
- **Famous tools written in Rust:** Firefox components, parts of Linux kernel, ripgrep, fd, bat, Cargo, the Deno runtime, Rspack, Turbopack, Cloudflare workerd, Linkerd2-proxy, uv.
- **Philosophy:** correctness and performance, no compromises. The type system enforces invariants that other languages enforce by convention or testing. Once code compiles, large bug classes are gone — at the cost of fighting the borrow checker.

### 2.3 TypeScript

JavaScript with structural types. Designed at Microsoft (2012). Type-checks at compile time, erases to plain JS at runtime. Runs anywhere JS does: Node, Deno, Bun, browsers. With `bun compile` / `deno compile`: increasingly viable for static-binary CLI tools.

- **Sweet spots:** web frontends (dominant), backend services on Node/Bun/Deno, tooling that extends the JS ecosystem (linters, bundlers, editor extensions).
- **Famous tools written in TypeScript:** VS Code, almost every modern SaaS frontend, Next.js, Vite, Vitest, ESLint, Prettier, SST (config / components layer).
- **Philosophy:** the language meets the existing world. Types are bolt-on, gradual, structural — they describe what's already happening in JS rather than imposing a new model. Trades strict-by-default safety for ecosystem reach.

### 2.4 At-a-glance — generic-generic

| Axis | Go | Rust | TypeScript |
|---|---|---|---|
| Memory model | GC | Ownership, no GC | GC (V8/Bun) |
| Type system | Nominal, basic | Algebraic, expressive | Structural, gradual |
| Compiles to | Native binary | Native binary | JS (or native via bun/deno) |
| Concurrency | Goroutines (CSP) | async/await + threads | async/await + workers |
| Compile time | Sub-second | Slow (10–30× Go) | Sub-second |
| Stdlib | Large, batteries-included | Minimal, crates.io heavy | Tiny, npm heavy |
| Industry niche | Cloud infra / DevOps | Systems / perf / safety | Web / SaaS / tooling |
| Hiring (overall) | Medium pool | Smaller, growing | Largest pool |
| Hiring (IaC-adjacent) | Largest pool | Smaller | Medium |

**At this layer, none is wrong.** Go is the cloud-infra default; Rust is the compiler-and-parser default; TS is the web-and-tooling default. Caravan sits at an intersection: it's a CLI compiler (Rust's territory) that operates on cloud infrastructure (Go's territory) for an audience of Python and Rust developers (Rust-leaning culturally). The narrower layers below break the tie.

---

## 3. CLI compiler generic — three languages for this category of tool

Narrower: assume a hypothetical static-binary compiler that parses yaml, transforms it through phases, emits structured config, and orchestrates subprocesses. Still no commitment to a particular emission format or IaC ecosystem.

### 3.1 Go — CLI-compiler-generic case

**Strengths:**
- Static binaries with `GOOS=… GOARCH=…` cross-compile from any host. Industry-best.
- Sub-second compile + run; iteration cost near zero.
- Subprocess (`os/exec`) is idiomatic — calling external CLIs is unceremonious.
- Stdlib covers YAML (via well-known third-party), HTTP, structured logging, embed (`//go:embed`).
- Goroutines + channels for trivially parallel work.
- Mature release tooling (one config file → 12 platform binaries + package-manager artifacts).
- Largest contributor pool in the cloud-tooling / DevOps / infra space.

**Weaknesses:**
- **No native ADTs / sum types.** Sealed-interface + type-switch is the emulation; `default: panic` is the safety net. Missing a case is a runtime bug, not a compile error. This is the single biggest weakness for compiler-shaped work.
- Generics (1.18+) help with collection helpers, not with sum types.
- YAML tagged-union unmarshal requires custom two-pass parsing.

### 3.2 Rust — CLI-compiler-generic case

**Strengths:**
- **Native enums / pattern matching.** ADTs are first-class; matches are exhaustive-checked. The IR-as-sum-type lands without emulation.
- `serde` derive on enums handles tagged-union YAML/JSON with one annotation. Best-in-class data-parsing ergonomics.
- Single static binary with `cargo` + `cross`; `musl` for portable Linux.
- Strong compile-time guarantees: borrow checker, exhaustive match, `Option`/`Result`. Many compiler-shaped bugs become impossible.
- `rust-analyzer` is best-in-class LSP across all languages.

**Weaknesses:**
- Compile times 10–30× Go's.
- Subprocess orchestration is awkward; async (tokio) vs sync (std) is a recurring decision tax.
- Smaller cloud-tooling / IaC contributor pool than Go.

### 3.3 TypeScript — CLI-compiler-generic case

**Strengths:**
- **Discriminated unions** with type-narrowing in switches. Between Go and Rust in expressiveness — closer to Rust at type-check time.
- Largest contributor pool overall.
- Excellent YAML and JSON libraries.
- Fast iteration (bun is sub-second).
- `zod` / `valibot` give runtime validation with derived types — closes part of the Rust-serde gap.

**Weaknesses:**
- Type safety is **compile-time-only**; type erasure at runtime means malformed input bypasses types unless you wire runtime validators.
- `bun compile` / `deno compile` are newer; cross-compile matrix less battle-tested than Go's.
- Subprocess via Node APIs is promise-heavy.
- IaC/cloud-tooling ecosystem is thinner than Go's.

### 3.4 CLI-compiler-generic matrix

| | Go | Rust | TypeScript |
|---|---|---|---|
| Static binary distribution | ✅ best | ✅ strong | ⚠️ newer |
| Cross-compile maturity | ✅ best | ✅ via `cross` | ⚠️ less proven |
| YAML tagged-union ergonomics | ⚠️ two-pass | ✅ serde derive | ✅ zod + libs |
| Type system for IR (ADTs) | ❌ emulated | ✅ native | ✅ discriminated unions |
| Iteration speed | ✅ sub-second | ⚠️ 10–30× slower | ✅ sub-second |
| Subprocess orchestration | ✅ idiomatic | ⚠️ async tax | ⚠️ promise tax |
| Cloud/IaC contributor pool | ✅ largest | ⚠️ growing | ⚠️ medium |

**No clear CLI-compiler-generic winner.** Rust wins type-system. Go wins plumbing + ecosystem-breadth. TS is a competent middle. The next layer breaks the tie.

---

## 4. Caravan-specific case

The CLI-compiler-generic analysis doesn't pick a winner. Caravan's actual requirements break the tie — strongly toward Go.

### 4.1 Caravan's specific requirements

Four things caravan needs that are not generic CLI-compiler needs:

1. **Emit canonical, `terraform fmt`-clean HCL.** Phase 5 is HCL emission with HashiCorp's quirks (heredocs, interpolation, dynamic blocks, provider-specific syntax). Not generic config; not JSON.
2. **Wrap `tofu`/`terraform` CLI with structured output.** `caravan plan` parses plan output; `caravan diff` compares structured plans; `caravan up` streams `apply` with proper cancellation.
3. **IR has 4 sum types totaling ~20 variants** (8 resources + `cloud_only` = 9 `ResourceKind`; 5 `ModuleKind` http/worker/cron/batch/adapter; 3 `BundleShape` long_running/function/batch; 5 `TriggerKind` — terminology per the 2026-05-17 A-disposition in `considerations.md`). The PoC scoping pass (item Q in `considerations.md`) collapses `ModuleKind` + `BundleShape` into a single per-target `Container` struct with one `shape` field; it also adds 4 ResourceKinds (cache/stream/search/llm), so total variant count stays roughly 20. Tagged-union ergonomics matter regardless.
4. **CLI diagnostic rendering quality.** Caravan's user-visible value is partly *better error messages than raw Terraform* — when yaml is malformed, when env-var wiring is broken, when a Tier-1 community library is missing. Source spans with multi-line carets, suggestions, colored output. The compiler's user surface is its diagnostics as much as its emitted HCL.

Requirements 1–2 are clear Go advantages; requirement 3 is a Rust advantage; requirement 4 is a Rust advantage. (Two things deliberately *not* listed: single-binary distribution and cross-compile maturity — the §1 elimination already filtered out non-binary candidates, so all three finalists clear that bar by construction. And same-language-as-v1-user-runtime — same logic: Python is out at §1, and Go / Rust / TS all pass, so it doesn't differentiate among the finalists. Both belong in the §3.4 matrix where they live, not in §4.1.) A separate non-criterion — the Pulumi fallback — is discussed in §4.2 as a Go-side option, not a thing caravan must satisfy.

### 4.2 Go — caravan fit (deep dive)

Go's CLI-compiler strengths align with requirements 1–2. Two first-party libraries do most of the work on 1–2, plus one external validation point, two notes on the Go-side weaknesses (requirements 3 and 4), and one Go-only option (in-process Pulumi) discussed below:

**`github.com/hashicorp/hcl/v2/hclwrite` — solves requirement 1.**

HashiCorp's own structured HCL builder. Output is bit-identical to `terraform fmt`. Example shape:

```go
f := hclwrite.NewEmptyFile()
body := f.Body()
block := body.AppendNewBlock("resource", []string{"aws_s3_bucket", "uploads"})
block.Body().SetAttributeValue("bucket", cty.StringVal("uploads-prod"))
```

No string templating. No quoting bugs. Adding a resource is 5–10 lines of structured calls. Golden-file tests are trivial because output is canonical.

**This library has no equivalent in Rust or TypeScript.** `hcl-rs` is community-maintained with thinner write support; nothing comparable exists in JS/TS. The HCL-emit story alone is multiple weeks of work in either alternative.

**`github.com/hashicorp/terraform-exec/tfexec` — solves requirement 2.**

HashiCorp's typed wrapper around the `terraform`/`tofu` CLI. `Init`, `Plan`, `Apply`, `Destroy`, `Output`, `Show` as typed methods. `tf.Plan(ctx)` returns a `*tfjson.Plan` — structured, walkable, comparable. Cancellation propagates SIGINT so `tofu` saves state on Ctrl-C.

**No equivalent in Rust or TypeScript.** Another week or two to build.

**`github.com/pulumi/pulumi/sdk/v3/go/auto` — a Go-only option, not a criterion.**

Pulumi's Automation API in Go. The thesis's escape hatch ([thesis.md:63](thesis.md#L63), "Pulumi-Go-as-CLI-internal is the next move") is *literally* this library, in-process. This is not listed as a §4.1 criterion because the fallback is contingent at v1 and subprocess Pulumi works from any language. Worth keeping in mind only: *if* the fallback ever fires often enough to matter, Go is the only candidate where in-process embed is on the table — Rust has no Pulumi SDK at all; TS shifts the escape hatch to Pulumi-TS, which is a different fallback than the thesis named. Pure upside if it ever matters; zero cost if it never does.

**SST's `cmd/sst` (Go) — supporting evidence, not a requirement.**

SST is 56% TypeScript + 24% Go. CLI binary in Go, Pulumi engine embedded, Terraform providers bridged through Pulumi. Components in TypeScript (the user-visible layer caravan replaces with yaml). GA since 2024-08.

This isn't a criterion caravan *needs to satisfy* — it's external evidence that the engine pattern (Go CLI embedding Pulumi + Terraform providers) works in production. Picking Go means inheriting a path already validated at scale; picking Rust or TS means novel territory. Useful background, but not a requirement and not a tiebreaker on its own.

**The cost — requirement 3.**

Caravan's IR is a sum type, Go doesn't have those. Sealed-interface + type-switch + `default: panic` is the emulation.

- 16 sum-type variants total, dispatched in ~3 places each
- Missing a case is a runtime panic, not a compile error
- Mitigation: golden-file tests exercise every variant; CI catches misses

Cost: ~5–10% more code than Rust would need, plus the bug class "added a primitive, forgot to handle it." Real but small relative to the wins on requirements 1–2.

**YAML deserialization cost.** Two-pass parse in Go is ~30 lines of boilerplate per variant. Rust with `serde(tag = "type")` is one annotation. ~250 lines of Go boilerplate that Rust eliminates.

**The diagnostic gap — requirement 4.** Go has no first-class equivalent to Rust's `miette` / `ariadne` / `codespan-reporting`. The state of the art in Go is bespoke: a struct with line/column, a snippet renderer, ANSI color via `fatih/color`. Roll-your-own. This is the one place where the Go pick is materially worse than the Rust alternative for a *compiler-shaped* product. Mitigation: study `miette`'s shape and build a thin equivalent (~1 week of work) before v1 ships its first error message; treat diagnostics as a deliberate design surface, not a side concern.

### 4.3 Rust — caravan fit

Wins requirements 3 and 4 cleanly. Loses requirements 1–2:

1. `hcl-rs` is thinner than HashiCorp's library; edge cases will bite. Add multi-week buffer.
2. No `tfexec`-equivalent. Write it.
3. ADTs / pattern matching / `serde` tagged-union derive — clean win on the sum-type IR.
4. **`miette` / `ariadne` / `codespan-reporting`** — best-in-class diagnostic rendering across all languages. Source spans with multi-line carets, suggestions, terminal-aware colors. The Rust-compiler / Elm-compiler diagnostic standard. This is a clean, structural win that Go cannot match without significant custom engineering.

Separately: no SST-style precedent in Rust either, and the Pulumi fallback discussed in §4.2 has no Rust equivalent (no Pulumi-Rust SDK) — neither is a criterion caravan must satisfy, but both are worth noting as Go-side options that aren't on the table here.

**Triggers that flip the call to Rust:**

- You're willing to absorb requirement-1 and -2 costs in exchange for compile-time exhaustiveness and best-in-class diagnostics.
- DNA signal / community-building is v1's headline goal.
- First hire is a Rust-IaC veteran who pushes back.

### 4.4 TypeScript — caravan fit

Wins discriminated unions (req 3). Loses 1, 2, 4:

1. No HCL library. Build it.
2. No Terraform CLI wrap. Build it.
3. Discriminated unions are a clean win; TS does this better than Go.
4. Diagnostic story is middle — better than Go (chalk, source-map traces), nowhere near Rust's `miette`-tier ergonomics.

(`bun compile` / `deno compile` are also newer and the cross-compile matrix is less battle-tested than Go or Rust — captured in the §3.4 matrix, not as a separate §4.1 criterion.)

The Pulumi-TS embed path (a Go-side option discussed in §4.2) does exist for TS too, but only matters if emission target shifts from raw HCL to Pulumi — which would itself shift the project's shape.

Plus: cultural-fit FAQ ("why is the compiler for my Rust app written in JavaScript?"). And separately: TS is SST's *components* language, not its engine language — borrowing TS-the-engine means doing what SST explicitly chose not to do. Not a criterion caravan must satisfy, but a useful signal about which language pairs are pre-validated for engine work.

**Triggers that flip the call to TS:**

- Emission target shifts from raw HCL to Pulumi (via `cdktf` or Pulumi-TS direct).
- A VSCode extension or web UI becomes a v1 deliverable.

Most realistic trigger: shift away from raw HCL. Without that, TS is third place.

---

## 5. Decision

**Go.**

The three-layer narrowing tells the whole story:

- **Generic-generic (§2)** — Go is the cloud-infrastructure default. Doesn't decide.
- **CLI-compiler generic (§3)** — three-way tie. Doesn't decide.
- **Caravan-specific (§4)** — Go wins outright on requirements 1 and 2 (structural library wins with no comparable alternative). Loses cleanly on 3 (sum-type IR, real but cheap to mitigate) and 4 (diagnostic rendering, where Rust's `miette`-tier story is genuinely better and Go must build a thin equivalent deliberately). Separately, SST in Go validates that the broader engine pattern works in production, and in-process Pulumi via `auto`-API is a Go-only option *if* the fallback ever fires — both are supporting evidence, not requirements.

The diagnostic gap (req 4) is the one item the original framing missed. It's real, but the right response is *invest in error rendering as a deliberate v1 design surface*, not flip to Rust — because the cost of giving up `hclwrite` + `tfexec` (requirements 1 and 2) is multiple weeks of structural rebuild, while the cost of building a Go diagnostic library good enough to be caravan-class is roughly one week of focused work.

Commit. Validate with the experiments in §6 in parallel with starting v1, not as a gate.

### 5.1 What would change the call

| Trigger | Flips to |
|---|---|
| Emission target shifts from raw HCL to Pulumi | TS (Pulumi-TS) or Rust |
| `hclwrite` hits a wall on a real feature | Rust or TS via Pulumi |
| Diagnostic rendering becomes a v1 differentiator and the Go effort to match `miette` exceeds the budgeted ~1 week | Rust |
| Community-building / DNA signal becomes v1's headline goal | Rust |
| First hire is a Rust-IaC veteran who pushes back | Rust |

**Calibration for the current solo developer.** The author already works in Rust day-to-day (Rust is not a "new language to learn" for them, and a Rust portfolio exists independently of caravan). Two of the triggers above are inert in this situation: there's no DNA signal to chase, and no Rust-IaC-veteran-first-hire path because the developer *is* the first hire. The "learning value" of Go-from-Rust is real but bounded (~1–2 weeks to productive, ~3 months to idiomatic) — meaningfully gentler than the cost of rebuilding `hclwrite` / `tfexec` equivalents in Rust. Net: the lean toward Go is slightly stronger for this developer than for a generic reader.

---

## 6. Validating experiments

Three half-day spikes — each isolates one caravan-specific requirement:

1. **HCL emission spike.** Hand-emit the resolved plan for `module.api` + `bucket.uploads` + `queue.jobs` + IAM role with policies in each candidate language. Measure: lines of code, readability, test ergonomics. Expected: Go via `hclwrite` wins cleanly.
2. **YAML round-trip with `Resource` tagged union.** Parse two of each primitive in each language. Measure: boilerplate, error-message quality. Expected: Rust via `serde` wins; Go workable with custom unmarshalers; TS competitive via zod.
3. **`tofu plan` wrapper with Ctrl-C handling.** Stream `tofu plan` output, propagate SIGINT, surface structured errors. Measure: lines of code, robustness. Expected: Go via `tfexec` wins cleanly.

If Go wins #1 and #3 and is close on #2, commit. If #1 or #3 is close, reopen — that would mean `hclwrite` or `tfexec` is less load-bearing than this doc claims, which is the surprise worth chasing.

---

*See also: [architecture-brainstorm.md](architecture-brainstorm.md) §9 for the original framing, §11 for the orthogonal packaging-SDK question, §10 for v1 milestone scope.*
