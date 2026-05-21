// Package main is the caravan CLI entry point.
//
// caravan is an application-definition compiler that emits Terraform/HCL
// (cloud targets) or docker-compose (local targets) from a single
// caravan.yaml. See docs/poc_yaml_spec.md for the yaml shape and
// docs/development_plan.md for milestone scope.
//
// Subcommands at M0:
//
//	caravan check    [--config=path]                 phases 1–2; exit 1 on schema errors
//	caravan spec     [--config=path] [--target=name] phases 1–3 (or 1–4 with --target); JSON to stdout
//	caravan compile  --target=name [--config=path]   phases 1–4 + placeholder files in <output_dir>/<target>/generated/ (yaml `output_dir:`, default `caravan-out/`)
//	caravan --version | -v                           print version
//
// M1 fills the Emit phase for the docker-compose override target.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/paulxiep/caravan/internal/compiler"
	"github.com/paulxiep/caravan/internal/compiler/emit"
)

const version = "0.0.0-pre-scoping"

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "check":
		os.Exit(runCheck(os.Args[2:]))
	case "spec":
		os.Exit(runSpec(os.Args[2:]))
	case "compile":
		os.Exit(runCompile(os.Args[2:]))
	case "--version", "-v", "version":
		fmt.Println("caravan", version)
	case "--help", "-h", "help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "caravan: unknown subcommand %q\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w *os.File) {
	fmt.Fprintln(w, `caravan — application-definition compiler

Subcommands:
  check    [--config=path]                 phases 1–2 (lex + parse); exit 1 on schema errors
  spec     [--config=path] [--target=name] phases 1–3 (or 1–4 with --target); JSON Plan/ResolvedPlan to stdout
  compile  --target=name [--config=path]   phases 1–4 + write placeholder files to <output_dir>/<target>/generated/ (yaml output_dir, default caravan-out/)

Flags:
  --config=PATH    path to caravan.yaml (default: ./caravan.yaml)
  --target=NAME    target name from caravan.yaml
  --json           emit JSON (default for `+"`spec`"+`)
  -v, --version    print version
  -h, --help       this help`)
}

// runCheck implements `caravan check`.
func runCheck(args []string) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	config := fs.String("config", "caravan.yaml", "path to caravan.yaml")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	plan, diag, err := compiler.CompileFile(*config)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	_, _ = diag.WriteTo(os.Stderr)
	if diag.HasErrors() {
		return 1
	}
	if plan == nil {
		// Shouldn't happen if HasErrors is false, but defensive.
		return 1
	}
	return 0
}

// runSpec implements `caravan spec`.
func runSpec(args []string) int {
	fs := flag.NewFlagSet("spec", flag.ContinueOnError)
	config := fs.String("config", "caravan.yaml", "path to caravan.yaml")
	target := fs.String("target", "", "target name (for phase 4 / peer-table resolution)")
	asJSON := fs.Bool("json", true, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *target != "" {
		rp, diag, err := compiler.CompileFileForTarget(*config, *target)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		_, _ = diag.WriteTo(os.Stderr)
		if diag.HasErrors() {
			return 1
		}
		return emitJSON(*asJSON, rp)
	}
	plan, diag, err := compiler.CompileFile(*config)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	_, _ = diag.WriteTo(os.Stderr)
	if diag.HasErrors() {
		return 1
	}
	return emitJSON(*asJSON, plan)
}

// runCompile implements `caravan compile --target=NAME`.
func runCompile(args []string) int {
	fs := flag.NewFlagSet("compile", flag.ContinueOnError)
	config := fs.String("config", "caravan.yaml", "path to caravan.yaml")
	target := fs.String("target", "", "target name (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *target == "" {
		fmt.Fprintln(os.Stderr, "caravan compile: --target is required")
		return 2
	}
	rp, diag, err := compiler.CompileFileForTarget(*config, *target)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	_, _ = diag.WriteTo(os.Stderr)
	if diag.HasErrors() {
		return 1
	}
	if rp == nil {
		return 1
	}

	outDir := filepath.Join(rp.Plan.OutputDir, *target, "generated")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	// M1: emit the docker-compose override for docker-compose targets.
	// M4-cloud will add HCL emission for aws targets.
	wrote := []string{}
	if rp.Plan.Targets[*target].Runtime == compiler.RuntimeDockerCompose {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		userRepoName := filepath.Base(cwd)

		// M4: scan the user's hand-authored compose for collision
		// detection. When a resource's emitted service name (e.g.
		// `postgres`, `redis`) already appears in the base compose,
		// caravan skips the duplicate container but still injects
		// the resource endpoint env vars into consumers. Read failure
		// is non-fatal: log + fall back to emit-everything.
		baseServices, scanErr := emit.BaseComposeServiceNames(cwd)
		if scanErr != nil {
			fmt.Fprintf(os.Stderr, "caravan compile: warning: %v (continuing with full resource emission)\n", scanErr)
		}

		body, err := emit.EmitComposeOverride(rp, userRepoName, baseServices)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		path := filepath.Join(outDir, "docker-compose.override.generated.yaml")
		if err := os.WriteFile(path, body, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		wrote = append(wrote, path)

		// M2: for each container-mode Rust seam, emit the synthetic
		// peer crate (Cargo.toml + src/main.rs + Dockerfile). Python
		// container-mode seams need none — they reuse the user's
		// image with a command override (handled in compose.go).
		peerPaths, err := writeRustPeerCrates(rp, *target, outDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		wrote = append(wrote, peerPaths...)

		// M3: emit per-target patched manifests (requirements.txt for
		// Python entries; Rust is a no-op until its own milestone).
		// User's on-disk manifest is read but never modified; the
		// patched copy lives in the per-target build-context.
		manifestPaths, err := emit.EmitManifestPatches(rp, outDir, cwd)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		wrote = append(wrote, manifestPaths...)
		rp.Plan.PatchedManifests = manifestPaths
	}

	// HCL emission lands at M4-cloud. Until then we still drop a
	// placeholder so the directory has the expected shape.
	timestamp := time.Now().UTC().Format(time.RFC3339)
	hclPath := filepath.Join(outDir, "main.tf")
	hclBody := fmt.Sprintf("# generated by caravan %s at %s\n# HCL emission lands at M4-cloud (see docs/development_plan.md)\n", version, timestamp)
	if err := os.WriteFile(hclPath, []byte(hclBody), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	wrote = append(wrote, hclPath)

	for _, p := range wrote {
		fmt.Fprintln(os.Stderr, "caravan compile: wrote", p)
	}
	return 0
}

func emitJSON(asJSON bool, v any) int {
	if !asJSON {
		fmt.Fprintln(os.Stderr, "caravan: non-JSON output not implemented at M0; use --json")
		return 2
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// writeRustPeerCrates was the M2-original entry point that emitted a
// synthetic Rust peer crate per container-mode seam. After the Path B
// refactor it's a no-op: Rust peers are now hosted by the same chat
// image as the consumer entry, with the SDK's `caravan_rpc::run_or_serve`
// detouring into peer mode based on `CARAVAN_RPC_ROLE`. No synthetic
// crate, no workspace.members edit, no Dockerfile marker. The compose
// emitter (`buildRustPeerService`) handles the peer-service yaml shape
// directly.
//
// Kept as a stub so callers don't need to be restructured; will be
// inlined / removed in a follow-up.
func writeRustPeerCrates(_ *compiler.ResolvedPlan, _, _ string) ([]string, error) {
	// Path B (2026-05-21): synthetic peer crates removed. The Rust peer
	// service in the emitted compose override uses the chat image with
	// `CARAVAN_RPC_ROLE=peer-<Iface>`; the SDK's `run_or_serve` detours
	// inside the chat binary. No on-disk emission needed.
	//
	// The earlier stale-`infra/peers/` sweep was dropped when output_dir
	// became yaml-configurable: it was a one-shot M2 migration aid and
	// only made sense for the formerly-hardcoded `infra/` layout.
	return nil, nil
}

// unused stub to silence linters until full subcommand wiring lands.
var _ = strings.TrimSpace
