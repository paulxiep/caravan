package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/paulxiep/caravan/internal/compiler"
	"github.com/paulxiep/caravan/internal/compiler/emit"
)

// runUp implements `caravan up --target=NAME`.
//
// Dispatches on target.Runtime + CredsPassthrough:
//
//   - Fargate: build + push images to ECR, then tofu init + apply against
//     the on-disk HCL. User never types a tofu command.
//   - Compose (pure): docker compose up against the hand-authored base
//     + the caravan-emitted override. Optional `--profile X` via the yaml
//     target's `compose_profile:`.
//   - Compose + creds_passthrough (hybrid): tofu init + apply provisions
//     cloud resources, caravan writes .env.hybrid from tofu outputs, then
//     docker compose up with --env-file points local containers at real AWS.
//     A single command brings both layers up so they communicate, while
//     the laptop-IP SG on the cloud resources blocks other callers.
//
// All paths operate on on-disk artifacts (HCL, override) — `caravan up`
// never re-emits. The user runs `caravan compile --target=X` first.
func runUp(args []string) int {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	config := fs.String("config", "caravan.yaml", "path to caravan.yaml")
	target := fs.String("target", "", "target name (default: caravan.yaml's default_target)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	resolved, err := resolveTargetName(*config, *target)
	if err != nil {
		fmt.Fprintln(os.Stderr, "caravan up:", err)
		return 2
	}
	*target = resolved

	rp, diag, err := compiler.CompileFileForTarget(*config, *target)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	_, _ = diag.WriteTo(os.Stderr)
	if diag.HasErrors() || rp == nil {
		return 1
	}

	tgt := rp.Plan.Targets[*target]
	switch tgt.Runtime {
	case compiler.RuntimeFargate:
		return runUpFargate(rp, tgt)
	case compiler.RuntimeDockerCompose:
		if tgt.CredsPassthrough {
			return runUpHybrid(rp, tgt)
		}
		return runUpCompose(rp, tgt, "")
	default:
		fmt.Fprintf(os.Stderr, "caravan up: target %q has runtime=%q; supported runtimes: docker-compose, fargate.\n",
			tgt.Name, tgt.Runtime)
		return 2
	}
}

// runDown implements `caravan down --target=NAME`.
//
// Dispatches on target.Runtime + CredsPassthrough:
//
//   - Fargate: tofu destroy against the on-disk HCL.
//   - Compose (pure): docker compose down (-v) against base + override.
//   - Compose + creds_passthrough (hybrid): docker compose down first
//     (so containers release any open connections), then tofu destroy.
//
// ECR images persist across `caravan down` (different lifecycle) — they're
// re-used by the next `caravan up`. Compose volumes are removed (`-v`) on
// teardown to keep dev iterations clean.
func runDown(args []string) int {
	fs := flag.NewFlagSet("down", flag.ContinueOnError)
	config := fs.String("config", "caravan.yaml", "path to caravan.yaml")
	target := fs.String("target", "", "target name (default: caravan.yaml's default_target)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	resolved, err := resolveTargetName(*config, *target)
	if err != nil {
		fmt.Fprintln(os.Stderr, "caravan down:", err)
		return 2
	}
	*target = resolved

	rp, diag, err := compiler.CompileFileForTarget(*config, *target)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	_, _ = diag.WriteTo(os.Stderr)
	if diag.HasErrors() || rp == nil {
		return 1
	}

	tgt := rp.Plan.Targets[*target]
	switch tgt.Runtime {
	case compiler.RuntimeFargate:
		return runDownFargate(rp, tgt)
	case compiler.RuntimeDockerCompose:
		if tgt.CredsPassthrough {
			return runDownHybrid(rp, tgt)
		}
		return runDownCompose(rp, tgt)
	default:
		fmt.Fprintf(os.Stderr, "caravan down: target %q has runtime=%q; supported runtimes: docker-compose, fargate.\n",
			tgt.Name, tgt.Runtime)
		return 2
	}
}

// runUpFargate is the existing M4b/M7 path: build+push images, tofu init+apply.
func runUpFargate(rp *compiler.ResolvedPlan, tgt *compiler.Target) int {
	outDir := filepath.Join(rp.Plan.OutputDir, tgt.Name, "generated")
	if err := ensureHCLPresent(outDir); err != nil {
		fmt.Fprintf(os.Stderr, "caravan up: %v\nRun `caravan compile --target=%s` first.\n", err, tgt.Name)
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "caravan up:", err)
		return 1
	}
	tfvars, err := tofuSecretVars(cwd, outDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := buildAndPushImages(rp, tgt); err != nil {
		fmt.Fprintln(os.Stderr, "caravan up:", err)
		return 1
	}
	if err := runTofu(outDir, "init", nil, nil); err != nil {
		return 1
	}
	// `caravan up` represents user consent — pass -auto-approve so tofu
	// doesn't deadlock asking for "yes" from a non-TTY subprocess. The
	// HCL was on-disk-reviewable since `caravan compile`.
	if err := runTofu(outDir, "apply", []string{"-auto-approve"}, tfvars); err != nil {
		return 1
	}
	fmt.Println()
	fmt.Println("caravan up: done. Verify tasks with:")
	fmt.Printf("  aws ecs list-tasks --cluster %s --region %s --profile %s\n",
		tgt.ECSClusterName, tgt.Region, tgt.AwsProfile)
	fmt.Printf("Tear down with: caravan down --target=%s\n", tgt.Name)
	return 0
}

// runDownFargate runs tofu destroy. ECR images persist; user removes
// repos manually for a fresh start.
func runDownFargate(rp *compiler.ResolvedPlan, tgt *compiler.Target) int {
	outDir := filepath.Join(rp.Plan.OutputDir, tgt.Name, "generated")
	if err := ensureHCLPresent(outDir); err != nil {
		fmt.Fprintf(os.Stderr, "caravan down: %v\n", err)
		return 1
	}
	cwd, _ := os.Getwd()
	tfvars, _ := tofuSecretVars(cwd, outDir) // best-effort; destroy doesn't need real secrets but tofu still asks for them
	if err := runTofu(outDir, "init", nil, nil); err != nil {
		return 1
	}
	if err := runTofu(outDir, "destroy", []string{"-auto-approve"}, tfvars); err != nil {
		return 1
	}
	fmt.Println()
	fmt.Println("caravan down: resources destroyed.")
	fmt.Println("Note: ECR images persist for next `caravan up` re-use; delete repos manually for a fresh start.")
	return 0
}

// runUpCompose runs `docker compose -f <base> -f <override> [--profile X]
// [--env-file <env>] up -d --build`. Used for pure compose targets and
// (with extraEnvFile) reused by hybrid mode after tofu apply.
func runUpCompose(rp *compiler.ResolvedPlan, tgt *compiler.Target, extraEnvFile string) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "caravan up:", err)
		return 1
	}
	overridePath := filepath.Join(rp.Plan.OutputDir, tgt.Name, "generated", "docker-compose.override.generated.yaml")
	if _, err := os.Stat(overridePath); err != nil {
		fmt.Fprintf(os.Stderr, "caravan up: no override at %s\nRun `caravan compile --target=%s` first.\n", overridePath, tgt.Name)
		return 1
	}
	composeArgs := buildComposeArgs(cwd, overridePath, extraEnvFile)
	composeArgs = append(composeArgs, "up", "-d", "--build")
	fmt.Fprintf(os.Stderr, "caravan up: bringing compose up for target %q\n", tgt.Name)
	if err := runStreaming("docker", composeArgs, cwd, nil); err != nil {
		fmt.Fprintln(os.Stderr, "caravan up: docker compose failed:", err)
		return 1
	}
	fmt.Println()
	fmt.Println("caravan up: done. Verify services with:")
	fmt.Println("  docker compose ps")
	fmt.Printf("Tear down with: caravan down --target=%s\n", tgt.Name)
	return 0
}

// runDownCompose runs `docker compose ... down -v` for pure compose targets.
func runDownCompose(rp *compiler.ResolvedPlan, tgt *compiler.Target) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "caravan down:", err)
		return 1
	}
	overridePath := filepath.Join(rp.Plan.OutputDir, tgt.Name, "generated", "docker-compose.override.generated.yaml")
	if _, err := os.Stat(overridePath); err != nil {
		fmt.Fprintf(os.Stderr, "caravan down: no override at %s\n", overridePath)
		return 1
	}
	composeArgs := buildComposeArgs(cwd, overridePath, "")
	composeArgs = append(composeArgs, "down", "-v")
	fmt.Fprintf(os.Stderr, "caravan down: tearing down compose for target %q\n", tgt.Name)
	if err := runStreaming("docker", composeArgs, cwd, nil); err != nil {
		fmt.Fprintln(os.Stderr, "caravan down: docker compose failed:", err)
		return 1
	}
	fmt.Println()
	fmt.Println("caravan down: containers + volumes removed.")
	return 0
}

// runUpHybrid is the mixed compose + tofu path (hybrid-dev). Single
// command brings BOTH layers up so they can communicate:
//
//  1. tofu init + apply — provisions real AWS resources (S3, RDS, SQS, ...).
//     The laptop-IP SG on RDS/Cache (emitted by hcl.go) restricts ingress
//     to the developer's current IP — local compose containers (egress as
//     that same IP) are reachable; other callers are blocked.
//  2. Capture tofu outputs → .env.hybrid file in the target's generated/.
//  3. docker compose up with --env-file pointing at .env.hybrid; the
//     containers read DATABASE_URL / S3_BUCKET / QUEUE_URL from the env
//     file at runtime, hitting real AWS.
func runUpHybrid(rp *compiler.ResolvedPlan, tgt *compiler.Target) int {
	outDir := filepath.Join(rp.Plan.OutputDir, tgt.Name, "generated")
	if err := ensureHCLPresent(outDir); err != nil {
		fmt.Fprintf(os.Stderr, "caravan up: %v\nRun `caravan compile --target=%s` first.\n", err, tgt.Name)
		return 1
	}
	cwd, _ := os.Getwd()
	tfvars, err := tofuSecretVars(cwd, outDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := runTofu(outDir, "init", nil, nil); err != nil {
		return 1
	}
	if err := runTofu(outDir, "apply", []string{"-auto-approve"}, tfvars); err != nil {
		return 1
	}
	envPath := filepath.Join(outDir, ".env.hybrid")
	if err := writeEnvHybrid(outDir, envPath); err != nil {
		fmt.Fprintln(os.Stderr, "caravan up:", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "caravan up: wrote %s from tofu outputs\n", envPath)
	return runUpCompose(rp, tgt, envPath)
}

// runDownHybrid mirrors runUpHybrid in reverse: compose down first (frees
// open connections to AWS resources), then tofu destroy (which removes
// the resources themselves). Each step is best-effort — a failed compose
// down doesn't block tofu destroy, because the user is on the cost hook
// for any still-running AWS resources.
func runDownHybrid(rp *compiler.ResolvedPlan, tgt *compiler.Target) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "caravan down:", err)
		return 1
	}
	outDir := filepath.Join(rp.Plan.OutputDir, tgt.Name, "generated")
	overridePath := filepath.Join(outDir, "docker-compose.override.generated.yaml")
	envPath := filepath.Join(outDir, ".env.hybrid")
	composeOK := true
	if _, statErr := os.Stat(overridePath); statErr == nil {
		composeArgs := buildComposeArgs(cwd, overridePath, envPath)
		composeArgs = append(composeArgs, "down", "-v")
		fmt.Fprintf(os.Stderr, "caravan down: tearing down compose for target %q\n", tgt.Name)
		if err := runStreaming("docker", composeArgs, cwd, nil); err != nil {
			fmt.Fprintln(os.Stderr, "caravan down: docker compose failed (continuing to tofu destroy):", err)
			composeOK = false
		}
	} else {
		fmt.Fprintf(os.Stderr, "caravan down: no override at %s; skipping compose teardown\n", overridePath)
	}
	if err := ensureHCLPresent(outDir); err != nil {
		fmt.Fprintf(os.Stderr, "caravan down: %v\n", err)
		return 1
	}
	tfvars, _ := tofuSecretVars(cwd, outDir)
	if err := runTofu(outDir, "init", nil, nil); err != nil {
		return 1
	}
	if err := runTofu(outDir, "destroy", []string{"-auto-approve"}, tfvars); err != nil {
		return 1
	}
	if !composeOK {
		fmt.Fprintln(os.Stderr, "caravan down: AWS resources destroyed but compose teardown reported errors above — inspect Docker state manually.")
		return 1
	}
	fmt.Println()
	fmt.Println("caravan down: containers + AWS resources torn down.")
	return 0
}

// buildComposeArgs assembles the leading `docker compose -f BASE -f OVERRIDE
// --profile <emit.AppProfile> [--env-file ENV]` arguments shared by every
// compose-up and compose-down invocation. Caller appends the subcommand
// (`up -d --build`, `down -v`, etc.).
//
// Base compose is discovered via emit.DiscoverBaseCompose (conventional
// locations relative to cwd). When absent, only the override is passed.
//
// Profile value comes from the exported emit.AppProfile constant — the
// same value emit-side attaches to every peer service + resource
// container (compose.go, resources.go). Single source of truth, so
// `--profile X` only matches what caravan actually emitted. For base
// composes with no profiles at all (code-rag), the flag is a no-op
// (services without profiles run regardless). For base composes that
// declare other profiles (e.g. invoice-parse's one-shot `ingest`),
// the flag correctly excludes them from `caravan up`.
func buildComposeArgs(cwd, overridePath, envFile string) []string {
	args := []string{"compose"}
	if base := emit.DiscoverBaseCompose(cwd); base != "" {
		args = append(args, "-f", base)
	}
	args = append(args, "-f", overridePath)
	if envFile != "" {
		args = append(args, "--env-file", envFile)
	}
	args = append(args, "--profile", emit.AppProfile)
	return args
}

// writeEnvHybrid captures `tofu output -json` against outDir and emits a
// dotenv-style file at envPath. Each top-level output becomes one
// `KEY=value` line. Used by runUpHybrid to bridge tofu outputs into
// docker compose's --env-file at deploy time.
//
// Mirrors the existing manual flow documented in the hybrid README
// (`tofu output -json | jq -r '...' > .env.hybrid`), but caravan owns
// the invocation — the user never types tofu.
func writeEnvHybrid(outDir, envPath string) error {
	out, err := captureOutput("tofu", []string{"-chdir=" + outDir, "output", "-json"}, "")
	if err != nil {
		return fmt.Errorf("tofu output -json: %w", err)
	}
	var raw map[string]struct {
		Value any `json:"value"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return fmt.Errorf("parse tofu output JSON: %w", err)
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var body strings.Builder
	body.WriteString("# Generated by `caravan up`. Captured from `tofu output -json`. Do not commit.\n")
	for _, k := range keys {
		body.WriteString(k)
		body.WriteByte('=')
		body.WriteString(fmt.Sprint(raw[k].Value))
		body.WriteByte('\n')
	}
	return os.WriteFile(envPath, []byte(body.String()), 0o644)
}

// ensureHCLPresent reports an error if main.tf isn't present in outDir.
// Used by `caravan up` and `caravan down` to fail fast when the user
// hasn't run `caravan compile` yet (or pointed at the wrong target).
func ensureHCLPresent(outDir string) error {
	if _, err := os.Stat(filepath.Join(outDir, "main.tf")); err != nil {
		return fmt.Errorf("no HCL found at %s (main.tf missing)", outDir)
	}
	return nil
}

// runTofu invokes `tofu <subcmd> [extra...]` in outDir, streaming stdio.
// `caravan up` is the user-facing trigger and represents user consent —
// caller passes `-auto-approve` so tofu doesn't deadlock on stdin when
// caravan runs from a non-TTY subprocess (CI, IDE shells, etc.). Caller
// also threads TF_VAR_* from the host env via tfvars.
func runTofu(outDir, subcmd string, extra []string, tfvars map[string]string) error {
	args := []string{"-chdir=" + outDir, subcmd}
	args = append(args, extra...)
	fmt.Fprintf(os.Stderr, "caravan: running tofu %s -chdir=%s %s\n", subcmd, outDir, strings.Join(extra, " "))
	c := exec.Command("tofu", args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	env := os.Environ()
	for k, v := range tfvars {
		env = append(env, "TF_VAR_"+k+"="+v)
	}
	c.Env = env
	if err := c.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "caravan: tofu %s failed: %v\n", subcmd, err)
		return err
	}
	return nil
}

// tofuSecretVars reads the sidecar wiring manifest (caravan.tfwiring.json)
// emitted alongside main.tf, resolves each binding's EnvName from the host
// process env, and returns the map runTofu uses to set TF_VAR_<VarName>.
// Returns an error listing every binding whose backing env var isn't set
// — surfaces the gap before tofu apply fails with a less actionable message.
//
// Replaces the M9-pre HCL string-grep implementation. The manifest is the
// structured source of truth: caravan up no longer reasons about variable
// blocks in textual HCL, so changes to the emitter's whitespace /
// formatting can't silently break this read path. Mirrors the "compile-
// then-up" two-phase design: caravan up reads on-disk artifacts rather
// than re-running the compiler. Loads `.env` from cwd if present (compose
// users source it implicitly; caravan up does it explicitly).
//
// Missing manifest → empty map + nil error (target had no var bindings —
// no Fargate / Lambda compute, or no declared secrets / env_file
// passthroughs). The caller (`runUpFargate` / `runUpHybrid`) treats this
// as the "no TF vars needed" path.
func tofuSecretVars(cwd, outDir string) (map[string]string, error) {
	loadDotEnv(cwd)
	manifestPath := filepath.Join(outDir, "caravan.tfwiring.json")
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("tofuSecretVars: read %s: %w", manifestPath, err)
	}
	var manifest struct {
		Bindings []struct {
			VarName   string `json:"var_name"`
			EnvName   string `json:"env_name"`
			Sensitive bool   `json:"sensitive"`
			Source    string `json:"source"`
		} `json:"bindings"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, fmt.Errorf("tofuSecretVars: parse %s: %w", manifestPath, err)
	}
	out := map[string]string{}
	var missing []string
	for _, b := range manifest.Bindings {
		if b.VarName == "" || b.EnvName == "" {
			continue
		}
		val, ok := os.LookupEnv(b.EnvName)
		if !ok || val == "" {
			missing = append(missing, fmt.Sprintf("%s (host env: %s; source: %s)", b.VarName, b.EnvName, b.Source))
			continue
		}
		out[b.VarName] = val
	}
	if len(missing) > 0 {
		return out, fmt.Errorf("caravan up: host env vars unset for %d binding(s): %s. "+
			"Export them or add to .env in the project root before `caravan up`.",
			len(missing), strings.Join(missing, ", "))
	}
	return out, nil
}

// loadDotEnv reads `.env` from cwd (if present) and merges each
// KEY=VALUE pair into the process env, without overwriting vars
// already set explicitly. Matches docker-compose's `--env-file .env`
// default behavior for non-caravan flows. Silent no-op when absent.
func loadDotEnv(cwd string) {
	body, err := os.ReadFile(filepath.Join(cwd, ".env"))
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		// Strip optional surrounding quotes.
		if len(val) >= 2 && (val[0] == '"' && val[len(val)-1] == '"' || val[0] == '\'' && val[len(val)-1] == '\'') {
			val = val[1 : len(val)-1]
		}
		if _, already := os.LookupEnv(key); !already {
			_ = os.Setenv(key, val)
		}
	}
}

// buildAndPushImages walks the target's container-mode entries (sorted
// alphabetically) and runs docker compose build + tag + ECR login +
// docker push for each. ECR repo names follow the entry-name verbatim
// convention enforced by compute_fargate.go's data lookups. Build is
// delegated to docker compose so the user's hand-authored base compose
// owns the build context + Dockerfile path + additional_contexts.
func buildAndPushImages(rp *compiler.ResolvedPlan, tgt *compiler.Target) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	baseCompose := emit.DiscoverBaseCompose(cwd)
	if baseCompose == "" {
		return fmt.Errorf("caravan up requires a docker-compose.yaml in %s — the build flow delegates to docker compose so the user's compose declares context + Dockerfile + additional_contexts. Searched conventional paths: infra/docker-compose.yaml, docker-compose.yaml, compose.yaml", cwd)
	}
	projectName := composeProjectName(rp.Plan.Name, tgt.Name)
	outDir := filepath.Join(rp.Plan.OutputDir, tgt.Name, "generated")

	names := make([]string, 0, len(tgt.Entries))
	for n, mode := range tgt.Entries {
		if mode == compiler.EntryContainer {
			names = append(names, n)
		}
	}
	sort.Strings(names)

	if len(names) == 0 {
		return fmt.Errorf("no container-mode entries on target %q (validateFargateTarget should have caught this)", tgt.Name)
	}

	for _, entryName := range names {
		entry := rp.Plan.Entries[entryName]
		if entry == nil {
			return fmt.Errorf("entry %q missing from plan", entryName)
		}
		if err := buildAndPushOne(cwd, baseCompose, projectName, entry, tgt); err != nil {
			return fmt.Errorf("entry %q: %w", entryName, err)
		}
	}

	// M7: build + push one image per Lambda seam, tagged `lambda-<seam>`.
	// Reuses the host entry's ECR repo + Dockerfile via caravan-emitted
	// docker-compose.fargate-build.generated.yaml override which inherits
	// the host's context + additional_contexts from base compose and
	// overrides target + args.
	lambdaSeams := make([]string, 0)
	for n, mode := range tgt.Seams {
		if mode == compiler.SeamLambda {
			lambdaSeams = append(lambdaSeams, n)
		}
	}
	sort.Strings(lambdaSeams)
	if len(lambdaSeams) > 0 {
		fargateBuild := filepath.Join(outDir, "docker-compose.fargate-build.generated.yaml")
		if _, statErr := os.Stat(fargateBuild); statErr != nil {
			return fmt.Errorf("Lambda build override missing at %s — run `caravan compile --target=%s` first", fargateBuild, tgt.Name)
		}
		for _, seamName := range lambdaSeams {
			seam := rp.Plan.Seams[seamName]
			if seam == nil {
				return fmt.Errorf("seam %q missing from plan", seamName)
			}
			host := findLambdaHostEntry(rp.Plan, seam)
			if host == nil {
				return fmt.Errorf("seam %q (lambda mode): no entry has path %q", seamName, seam.Path)
			}
			if err := buildAndPushLambdaSeam(cwd, baseCompose, fargateBuild, projectName, host, seam, tgt); err != nil {
				return fmt.Errorf("seam %q: %w", seamName, err)
			}
		}
	}
	return nil
}

// composeProjectName is the `--project-name` value caravan passes to
// every docker compose invocation in a Fargate build. Deterministic so
// the resulting local image names (`<project>-<service>:latest`) are
// predictable across rebuilds for `docker tag` to ECR.
func composeProjectName(planName, targetName string) string {
	return fmt.Sprintf("caravan-%s-%s", dashedName(planName), dashedName(targetName))
}

// findLambdaHostEntry returns the alphabetically-first entry whose path
// matches the seam's path. Mirrors emit/hcl/compute_lambda.go's
// lambdaHostEntry without crossing the import boundary.
func findLambdaHostEntry(plan *compiler.Plan, seam *compiler.Seam) *compiler.Entry {
	names := make([]string, 0, len(plan.Entries))
	for n := range plan.Entries {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		e := plan.Entries[n]
		if e != nil && e.Path != "" && e.Path == seam.Path {
			return e
		}
	}
	return nil
}

// buildAndPushLambdaSeam builds + tags + pushes a Lambda peer image.
// Build is delegated to `docker compose -f <base> -f <fargateBuild>
// build <seam-dashed>-lambda`; the caravan-emitted fargate-build
// override (emit.EmitFargateBuildOverride) defines the lambda service
// with build context + additional_contexts inherited from the host
// entry's base-compose service, plus the slim stage target. Local
// image: `<project>-<seam>-lambda:latest`. Pushed to the host entry's
// ECR repo with tag `lambda-<dashed-seam>` — matches what
// compute_lambda.go references in aws_lambda_function's image_uri.
func buildAndPushLambdaSeam(cwd, baseCompose, fargateBuild, projectName string, entry *compiler.Entry, seam *compiler.Seam, tgt *compiler.Target) error {
	repoName := dashedName(entry.Name)
	repoURI, err := ecrRepoURI(repoName, tgt.Region, tgt.AwsProfile)
	if err != nil {
		return fmt.Errorf("resolve ECR URI for %q: %w", repoName, err)
	}
	seamDashed := emit.DashedSeamName(seam.Name)
	imageURI := fmt.Sprintf("%s:lambda-%s", repoURI, seamDashed)
	composeService := seamDashed + "-lambda"

	fmt.Fprintf(os.Stderr, "caravan up: building lambda image for seam %q via docker compose (service=%s)\n",
		seam.Name, composeService)
	buildArgs := []string{
		"compose",
		"-f", baseCompose,
		"-f", fargateBuild,
		"--project-name", projectName,
		"build",
		composeService,
	}
	if err := runStreaming("docker", buildArgs, cwd, nil); err != nil {
		return fmt.Errorf("docker compose build (lambda): %w", err)
	}

	localImage := fmt.Sprintf("%s-%s:latest", projectName, composeService)
	fmt.Fprintf(os.Stderr, "caravan up: tagging %s -> %s\n", localImage, imageURI)
	if err := runStreaming("docker", []string{"tag", localImage, imageURI}, cwd, nil); err != nil {
		return fmt.Errorf("docker tag: %w", err)
	}

	if err := ecrLoginAndPush(cwd, repoURI, imageURI, tgt); err != nil {
		return err
	}
	return nil
}

// buildAndPushOne builds + tags + pushes a single entry's image to ECR.
// The build is delegated to `docker compose -f <base> --project-name
// <project> build --build-arg CARAVAN_TARGET=<target> <entry>` so the
// user's hand-authored base compose is the single source of truth for
// build context, Dockerfile path, and additional_contexts. The local
// image lands at `<project>-<entry>:latest`; we re-tag it to the ECR
// URL and push. ECR repo name = toDashed(entry.Name) per
// compute_fargate.go's emitECRRepoLookup convention.
func buildAndPushOne(cwd, baseCompose, projectName string, entry *compiler.Entry, tgt *compiler.Target) error {
	repoName := dashedName(entry.Name)
	repoURI, err := ecrRepoURI(repoName, tgt.Region, tgt.AwsProfile)
	if err != nil {
		return fmt.Errorf("resolve ECR URI for %q: %w", repoName, err)
	}
	imageURI := repoURI + ":latest"

	fmt.Fprintf(os.Stderr, "caravan up: building entry %q via docker compose (base=%s)\n", entry.Name, baseCompose)
	buildArgs := []string{
		"compose",
		"-f", baseCompose,
		"--project-name", projectName,
		"build",
		"--build-arg", "CARAVAN_TARGET=" + tgt.Name,
		entry.Name,
	}
	if err := runStreaming("docker", buildArgs, cwd, nil); err != nil {
		return fmt.Errorf("docker compose build: %w", err)
	}

	localImage := fmt.Sprintf("%s-%s:latest", projectName, dashedName(entry.Name))
	fmt.Fprintf(os.Stderr, "caravan up: tagging %s -> %s\n", localImage, imageURI)
	if err := runStreaming("docker", []string{"tag", localImage, imageURI}, cwd, nil); err != nil {
		return fmt.Errorf("docker tag: %w", err)
	}

	if err := ecrLoginAndPush(cwd, repoURI, imageURI, tgt); err != nil {
		return err
	}
	return nil
}

// ecrLoginAndPush is the shared "aws ecr get-login-password | docker
// login | docker push" tail shared by entry and Lambda image flows.
func ecrLoginAndPush(cwd, repoURI, imageURI string, tgt *compiler.Target) error {
	fmt.Fprintf(os.Stderr, "caravan up: logging in to ECR (%s)\n", registryFromURI(repoURI))
	pwd, err := captureOutput("aws", []string{"ecr", "get-login-password", "--region", tgt.Region, "--profile", tgt.AwsProfile}, cwd)
	if err != nil {
		return fmt.Errorf("aws ecr get-login-password: %w", err)
	}
	loginArgs := []string{"login", "--username", "AWS", "--password-stdin", registryFromURI(repoURI)}
	if err := runStreaming("docker", loginArgs, cwd, strings.NewReader(strings.TrimSpace(pwd))); err != nil {
		return fmt.Errorf("docker login: %w", err)
	}
	fmt.Fprintf(os.Stderr, "caravan up: pushing %s\n", imageURI)
	if err := runStreaming("docker", []string{"push", imageURI}, cwd, nil); err != nil {
		return fmt.Errorf("docker push: %w", err)
	}
	return nil
}

// ecrRepoURI resolves the repository_uri (no tag) for the named ECR
// repo. Uses `aws ecr describe-repositories --query`. Surfaces "not
// found" errors cleanly so the user knows to pre-create the repo per
// M4-cloud-prereq.
func ecrRepoURI(repoName, region, profile string) (string, error) {
	out, err := captureOutput("aws", []string{
		"ecr", "describe-repositories",
		"--repository-names", repoName,
		"--query", "repositories[0].repositoryUri",
		"--output", "text",
		"--region", region,
		"--profile", profile,
	}, "")
	if err != nil {
		return "", err
	}
	uri := strings.TrimSpace(out)
	if uri == "" || uri == "None" {
		return "", fmt.Errorf("ECR repo %q not found in region %q (pre-create per M4-cloud-prereq)", repoName, region)
	}
	return uri, nil
}

// registryFromURI strips the trailing `/<repo>` from a repository URI
// to get the bare registry host for `docker login`. Example:
//
//	"351090596944.dkr.ecr.ap-southeast-1.amazonaws.com/code-rag-chat"
//	→ "351090596944.dkr.ecr.ap-southeast-1.amazonaws.com"
func registryFromURI(uri string) string {
	if i := strings.IndexByte(uri, '/'); i > 0 {
		return uri[:i]
	}
	return uri
}

// runStreaming runs cmd with args, streaming stdout/stderr to the user.
// Optional stdin is wired (used for `docker login --password-stdin`).
//
// Sets BUILDX_NO_DEFAULT_ATTESTATIONS=1 inherited into every subprocess
// so `docker compose build` (which uses buildx under the hood) doesn't
// stamp images with the provenance/sbom attestation manifest. Lambda
// rejects images with attestation manifests ("media type … is not
// supported"); ECS Fargate is tolerant either way. Setting it globally
// is harmless for the Fargate flow and necessary for Lambda peers.
func runStreaming(name string, args []string, cwd string, stdin io.Reader) error {
	c := exec.Command(name, args...)
	c.Dir = cwd
	c.Env = append(os.Environ(), "BUILDX_NO_DEFAULT_ATTESTATIONS=1")
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if stdin != nil {
		c.Stdin = stdin
	}
	return c.Run()
}

// captureOutput runs cmd and returns stdout as a string. Used for AWS
// CLI calls whose output is small and structured (URIs, passwords).
func captureOutput(name string, args []string, cwd string) (string, error) {
	c := exec.Command(name, args...)
	if cwd != "" {
		c.Dir = cwd
	}
	out, err := c.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}

// dashedName replicates emit/hcl/naming.go's toDashed so the ECR repo
// name lookups here match the data block emitter exactly. Local copy
// avoids a circular import between cmd/caravan and the internal hcl
// package's private helpers.
func dashedName(s string) string {
	s = strings.ToLower(s)
	return strings.ReplaceAll(s, "_", "-")
}
