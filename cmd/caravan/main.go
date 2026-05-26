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

	"github.com/paulxiep/caravan/internal/compiler"
	"github.com/paulxiep/caravan/internal/compiler/emit"
	"github.com/paulxiep/caravan/internal/compiler/emit/hcl"
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
	case "up":
		os.Exit(runUp(os.Args[2:]))
	case "down":
		os.Exit(runDown(os.Args[2:]))
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
  compile  [--target=name] [--config=path] phases 1–4 + write artifacts to <output_dir>/<target>/generated/ (default target from caravan.yaml's default_target)
  up       [--target=name] [--config=path] ECR build/push + tofu init+apply on the on-disk HCL (Fargate targets only)
  down     [--target=name] [--config=path] tofu destroy on the on-disk HCL (Fargate targets only)

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

// runCompile implements `caravan compile [--target=NAME]`. When --target
// is omitted, falls back to `default_target` from caravan.yaml (same
// behavior as `docker compose` reading service from compose.yaml).
func runCompile(args []string) int {
	fs := flag.NewFlagSet("compile", flag.ContinueOnError)
	config := fs.String("config", "caravan.yaml", "path to caravan.yaml")
	target := fs.String("target", "", "target name (default: caravan.yaml's default_target)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	resolved, err := resolveTargetName(*config, *target)
	if err != nil {
		fmt.Fprintln(os.Stderr, "caravan compile:", err)
		return 2
	}
	_, _, wrote, code := compileAndEmit(*config, resolved)
	for _, p := range wrote {
		fmt.Fprintln(os.Stderr, "caravan compile: wrote", p)
	}
	return code
}

// resolveTargetName picks the target to operate on. Precedence:
//
//  1. `--target=NAME` flag from the user.
//  2. `default_target:` declared in caravan.yaml.
//
// Errors when neither is set, with a message naming the config path so
// the user knows where to add `default_target:`.
func resolveTargetName(configPath, requested string) (string, error) {
	if requested != "" {
		return requested, nil
	}
	plan, _, err := compiler.CompileFile(configPath)
	if err != nil {
		return "", err
	}
	if plan == nil || plan.DefaultTarget == "" {
		return "", fmt.Errorf("--target is required (no `default_target:` declared in %s)", configPath)
	}
	return plan.DefaultTarget, nil
}

// compileAndEmit runs the full compile + emit pipeline for one target.
// Returns the resolved plan + per-target outDir + written file paths +
// exit code (0 on success). Shared by `caravan compile` and `caravan
// up` — `up` adds the ECR push + print-apply steps on top.
//
// Side effects: prints diagnostics to stderr; writes files into
// <outputDir>/<target>/generated/.
func compileAndEmit(config, target string) (*compiler.ResolvedPlan, string, []string, int) {
	rp, diag, err := compiler.CompileFileForTarget(config, target)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, "", nil, 1
	}
	_, _ = diag.WriteTo(os.Stderr)
	if diag.HasErrors() {
		return nil, "", nil, 1
	}
	if rp == nil {
		return nil, "", nil, 1
	}

	outDir := filepath.Join(rp.Plan.OutputDir, target, "generated")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return rp, outDir, nil, 1
	}

	wrote := []string{}
	tgt := rp.Plan.Targets[target]

	// Compose emit fires for any docker-compose target (Phase 1 + M4
	// hybrid-dev). Pure-Fargate targets skip compose entirely.
	if tgt.Runtime == compiler.RuntimeDockerCompose {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return rp, outDir, wrote, 1
		}
		userRepoName := filepath.Base(cwd)

		baseServices, scanErr := emit.BaseComposeServiceNames(cwd)
		if scanErr != nil {
			fmt.Fprintf(os.Stderr, "caravan compile: warning: %v (continuing with full resource emission)\n", scanErr)
		}

		body, err := emit.EmitComposeOverride(rp, userRepoName, baseServices)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return rp, outDir, wrote, 1
		}
		path := filepath.Join(outDir, "docker-compose.override.generated.yaml")
		if err := os.WriteFile(path, body, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return rp, outDir, wrote, 1
		}
		wrote = append(wrote, path)

		peerPaths, err := writeRustPeerCrates(rp, target, outDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return rp, outDir, wrote, 1
		}
		wrote = append(wrote, peerPaths...)

		manifestPaths, err := emit.EmitManifestPatches(rp, outDir, cwd)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return rp, outDir, wrote, 1
		}
		wrote = append(wrote, manifestPaths...)
		rp.Plan.PatchedManifests = manifestPaths
	}

	// HCL emit fires for any AWS-producing target (Target.EmitsHCL —
	// hybrid-dev, Fargate, Lambda, …). Pure docker-compose targets fall
	// through with no HCL artifacts.
	if tgt.EmitsHCL() {
		hclPaths, err := hcl.EmitHCL(rp, outDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return rp, outDir, wrote, 1
		}
		wrote = append(wrote, hclPaths...)

		// Hybrid-dev gets the compose-against-real-AWS README.
		// Fargate gets its own README (see emit.EmitFargateReadme) with
		// the `caravan up` workflow.
		if tgt.CredsPassthrough {
			envTemplate, err := emit.EmitEnvTemplate(rp, outDir)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return rp, outDir, wrote, 1
			}
			if envTemplate != "" {
				wrote = append(wrote, envTemplate)
			}

			repoRelative := filepath.Join(rp.Plan.OutputDir, target, "generated")
			readme, err := emit.EmitHybridReadme(rp, outDir, repoRelative)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return rp, outDir, wrote, 1
			}
			if readme != "" {
				wrote = append(wrote, readme)
			}
		} else if tgt.Runtime == compiler.RuntimeFargate {
			repoRelative := filepath.Join(rp.Plan.OutputDir, target, "generated")
			readme, err := emit.EmitFargateReadme(rp, outDir, repoRelative)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return rp, outDir, wrote, 1
			}
			if readme != "" {
				wrote = append(wrote, readme)
			}
		}
	}

	return rp, outDir, wrote, 0
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
