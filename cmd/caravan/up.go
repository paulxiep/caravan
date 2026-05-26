package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/paulxiep/caravan/internal/compiler"
)

// runUp implements `caravan up --target=NAME`.
//
// Two-phase flow:
//
//  1. The user runs `caravan compile --target=NAME` to emit HCL into
//     <outputDir>/<target>/generated/. They review and may hand-edit
//     before deploying.
//  2. `caravan up` operates on the **on-disk** HCL — never re-emits.
//     Steps: build + push each entry's image to ECR, then run
//     `tofu init` + `tofu apply` (interactive — tofu shows the plan and
//     prompts before applying). User never types a tofu command.
//
// Errors if the HCL doesn't exist (clear message to run compile first).
// Hybrid-dev / pure-compose targets are rejected — `caravan up` is for
// Fargate only at M4b (M7 will broaden to Lambda).
//
// Failures abort with non-zero exit. The HCL artifacts on disk are
// untouched; the user can re-run after fixing the underlying issue.
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
	if tgt.Runtime != compiler.RuntimeFargate {
		fmt.Fprintf(os.Stderr, "caravan up: target %q has runtime=%q; `caravan up` currently supports Fargate only (M7 will add Lambda). Use `docker compose up` for compose targets.\n",
			*target, tgt.Runtime)
		return 2
	}

	outDir := filepath.Join(rp.Plan.OutputDir, *target, "generated")
	if err := ensureHCLPresent(outDir); err != nil {
		fmt.Fprintf(os.Stderr, "caravan up: %v\nRun `caravan compile --target=%s` first.\n", err, *target)
		return 1
	}

	// Build + push every container-mode entry's image. Seams reuse the
	// host entry's image (dual-role binary pattern); no separate build.
	if err := buildAndPushImages(rp, tgt); err != nil {
		fmt.Fprintln(os.Stderr, "caravan up:", err)
		return 1
	}

	// tofu init + apply. Interactive: tofu shows the plan and prompts
	// before applying. Caravan owns the invocation so the user never
	// has to know the tofu command surface — but the human-review step
	// inside tofu apply is preserved (auditable-HCL principle).
	if err := runTofu(outDir, "init"); err != nil {
		return 1
	}
	if err := runTofu(outDir, "apply"); err != nil {
		return 1
	}

	fmt.Println()
	fmt.Println("caravan up: done. Verify tasks with:")
	fmt.Printf("  aws ecs list-tasks --cluster %s --region %s --profile %s\n",
		tgt.ECSClusterName, tgt.Region, tgt.AwsProfile)
	fmt.Println("Tear down with:")
	fmt.Printf("  caravan down --target=%s\n", *target)
	return 0
}

// runDown implements `caravan down --target=NAME`.
//
// Single-step: `tofu destroy` against the on-disk HCL. Interactive —
// tofu lists the resources to destroy and prompts for confirmation.
// Caravan does not touch the ECR images (they persist across deploys
// and reuse on the next `caravan up`); user deletes the repos manually
// when truly done with the app.
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
	if tgt.Runtime != compiler.RuntimeFargate {
		fmt.Fprintf(os.Stderr, "caravan down: target %q has runtime=%q; `caravan down` currently supports Fargate only (M7 will add Lambda).\n",
			*target, tgt.Runtime)
		return 2
	}

	outDir := filepath.Join(rp.Plan.OutputDir, *target, "generated")
	if err := ensureHCLPresent(outDir); err != nil {
		fmt.Fprintf(os.Stderr, "caravan down: %v\n", err)
		return 1
	}

	// tofu init may be a no-op if the directory was already initialized;
	// caravan re-runs it to be robust against fresh clones / new checkouts.
	if err := runTofu(outDir, "init"); err != nil {
		return 1
	}
	if err := runTofu(outDir, "destroy"); err != nil {
		return 1
	}

	fmt.Println()
	fmt.Println("caravan down: resources destroyed.")
	fmt.Println("Note: ECR images persist for next `caravan up` re-use; delete repos manually for a fresh start.")
	return 0
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

// runTofu invokes `tofu <subcmd>` in outDir, streaming stdin/stdout/stderr
// so the user sees tofu's plan output and answers its confirmation prompts
// directly. Returns the underlying error from exec.Cmd.Run() for upstream
// reporting.
func runTofu(outDir, subcmd string) error {
	fmt.Fprintf(os.Stderr, "caravan: running tofu %s -chdir=%s\n", subcmd, outDir)
	c := exec.Command("tofu", "-chdir="+outDir, subcmd)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "caravan: tofu %s failed: %v\n", subcmd, err)
		return err
	}
	return nil
}

// buildAndPushImages walks the target's container-mode entries (sorted
// alphabetically) and runs docker build + ECR login + docker tag +
// docker push for each. ECR repo names follow the entry-name verbatim
// convention enforced by compute_fargate.go's data lookups.
func buildAndPushImages(rp *compiler.ResolvedPlan, tgt *compiler.Target) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

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
		if err := buildAndPushOne(cwd, entry, tgt); err != nil {
			return fmt.Errorf("entry %q: %w", entryName, err)
		}
	}
	return nil
}

// buildAndPushOne runs the docker build / ECR login / docker push chain
// for a single entry. The ECR repo name = toDashed(entry.Name) — same
// convention compute_fargate.go's emitECRRepoLookup uses. The repo must
// pre-exist (per M4-cloud-prereq's docs/aws_onboarding_checklist.md);
// a missing repo surfaces a clear AWS-CLI error here.
func buildAndPushOne(cwd string, entry *compiler.Entry, tgt *compiler.Target) error {
	repoName := dashedName(entry.Name)
	dockerfile := entry.Dockerfile
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	buildContext := entry.Path
	if buildContext == "" {
		buildContext = "."
	}

	// 1. Resolve ECR repo URI via aws CLI. Done first so a missing repo
	//    fails fast — before the (potentially long) docker build.
	repoURI, err := ecrRepoURI(repoName, tgt.Region, tgt.AwsProfile)
	if err != nil {
		return fmt.Errorf("resolve ECR URI for %q: %w", repoName, err)
	}
	imageURI := repoURI + ":latest"

	fmt.Fprintf(os.Stderr, "caravan up: building image for entry %q (Dockerfile=%s, target=%s)\n", entry.Name, dockerfile, entry.RuntimeTarget)
	buildArgs := []string{"build", "-f", dockerfile, "-t", imageURI}
	if entry.RuntimeTarget != "" {
		buildArgs = append(buildArgs, "--target", entry.RuntimeTarget)
	}
	buildArgs = append(buildArgs, buildContext)
	if err := runStreaming("docker", buildArgs, cwd, nil); err != nil {
		return fmt.Errorf("docker build: %w", err)
	}

	// 2. ECR login. `aws ecr get-login-password | docker login` is the
	//    canonical AWS-recommended flow.
	fmt.Fprintf(os.Stderr, "caravan up: logging in to ECR (%s)\n", registryFromURI(repoURI))
	pwd, err := captureOutput("aws", []string{"ecr", "get-login-password", "--region", tgt.Region, "--profile", tgt.AwsProfile}, cwd)
	if err != nil {
		return fmt.Errorf("aws ecr get-login-password: %w", err)
	}
	loginArgs := []string{"login", "--username", "AWS", "--password-stdin", registryFromURI(repoURI)}
	if err := runStreaming("docker", loginArgs, cwd, strings.NewReader(strings.TrimSpace(pwd))); err != nil {
		return fmt.Errorf("docker login: %w", err)
	}

	// 3. Push.
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
func runStreaming(name string, args []string, cwd string, stdin io.Reader) error {
	c := exec.Command(name, args...)
	c.Dir = cwd
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
