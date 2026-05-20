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
//	caravan compile  --target=name [--config=path]   phases 1–4 + placeholder files in infra/<target>/generated/
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
  compile  --target=name [--config=path]   phases 1–4 + write placeholder files to infra/<target>/generated/

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

	outDir := filepath.Join("infra", *target, "generated")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	// M1: emit the docker-compose override for docker-compose targets.
	// M4-cloud will add HCL emission for aws targets.
	wrote := []string{}
	if rp.Plan.Targets[*target].Runtime == compiler.RuntimeDockerCompose {
		body, err := emit.EmitComposeOverride(rp)
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

// unused stub to silence linters until full subcommand wiring lands.
var _ = strings.TrimSpace
