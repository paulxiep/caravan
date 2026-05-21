package emit

import (
	"strings"
	"testing"
)

// TestAccumulator_PreservesInsertionOrder verifies that services are
// rendered in the order they were first added (M1 emit order: consumer
// first, peer second), not alphabetic. This is what keeps the M1
// golden byte-identical through the refactor.
func TestAccumulator_PreservesInsertionOrder(t *testing.T) {
	acc := newComposeAccumulator()
	// Add in non-alphabetic order to prove insertion-order is the rule
	acc.AddService("zeta", composeService{})
	acc.AddService("alpha", composeService{})
	acc.AddService("middle", composeService{})

	out, err := acc.Render("test", "caravan-out")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(out)
	idxZeta := strings.Index(body, "zeta")
	idxAlpha := strings.Index(body, "alpha")
	idxMiddle := strings.Index(body, "middle")
	if idxZeta < 0 || idxAlpha < 0 || idxMiddle < 0 {
		t.Fatalf("missing service in output:\n%s", body)
	}
	if !(idxZeta < idxAlpha && idxAlpha < idxMiddle) {
		t.Errorf("services not in insertion order:\n%s", body)
	}
}

// TestAccumulator_EnvBandOrdering verifies the documented merge order:
// resource-source env vars flush first (alphabetic), seam-source second
// (alphabetic). Within a single source, alphabetic order applies.
func TestAccumulator_EnvBandOrdering(t *testing.T) {
	acc := newComposeAccumulator()
	// Add seam-side first, resource-side second — Render should reorder
	// so resource band flushes first in output.
	_ = acc.AddEnv("svc", "CARAVAN_RPC_SHARED_SECRET", "x", envSourceSeam)
	_ = acc.AddEnv("svc", "CARAVAN_RPC_PEERS", "y", envSourceSeam)
	_ = acc.AddEnv("svc", "REDIS_URL", "redis://localhost", envSourceResource)
	_ = acc.AddEnv("svc", "DATABASE_URL", "postgres://localhost", envSourceResource)

	svc := acc.services["svc"]
	// After Render, the slice should be reordered. We invoke Render
	// to trigger the sort (Render mutates in-place).
	_, err := acc.Render("test", "caravan-out")
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	wantKeys := []string{
		"DATABASE_URL",              // resource band, alphabetic
		"REDIS_URL",                 // resource band, alphabetic
		"CARAVAN_RPC_PEERS",         // seam band, alphabetic
		"CARAVAN_RPC_SHARED_SECRET", // seam band, alphabetic
	}
	if len(svc.Environment) != len(wantKeys) {
		t.Fatalf("env count: got %d, want %d", len(svc.Environment), len(wantKeys))
	}
	for i, want := range wantKeys {
		if svc.Environment[i].Key != want {
			t.Errorf("env[%d]: got %q, want %q", i, svc.Environment[i].Key, want)
		}
	}
}

// TestAccumulator_RejectsCaravanRpcKeyFromResource verifies the
// precedence safety rule: resource emit may not write into the
// CARAVAN_RPC_ namespace.
func TestAccumulator_RejectsCaravanRpcKeyFromResource(t *testing.T) {
	acc := newComposeAccumulator()
	err := acc.AddEnv("svc", "CARAVAN_RPC_FOO", "bar", envSourceResource)
	if err == nil {
		t.Fatal("expected error for resource writing CARAVAN_RPC_ key, got nil")
	}
	if !strings.Contains(err.Error(), "CARAVAN_RPC_") {
		t.Errorf("error message should mention CARAVAN_RPC_ namespace; got: %v", err)
	}
}

// TestAccumulator_LastWriteWins verifies that re-adding the same env
// key (any source) overwrites the prior value.
func TestAccumulator_LastWriteWins(t *testing.T) {
	acc := newComposeAccumulator()
	_ = acc.AddEnv("svc", "FOO", "first", envSourceResource)
	_ = acc.AddEnv("svc", "FOO", "second", envSourceSeam)
	_, _ = acc.Render("test", "caravan-out")
	svc := acc.services["svc"]
	if len(svc.Environment) != 1 {
		t.Fatalf("env count: got %d, want 1", len(svc.Environment))
	}
	if svc.Environment[0].Value != "second" {
		t.Errorf("value: got %q, want %q", svc.Environment[0].Value, "second")
	}
}

// TestAccumulator_MergeService verifies that AddService called twice
// on the same name merges fields (Build / Command replace; profiles /
// depends_on / env_file append-deduped).
func TestAccumulator_MergeService(t *testing.T) {
	acc := newComposeAccumulator()
	acc.AddService("svc", composeService{
		Profiles:  []string{"app"},
		DependsOn: []composeDependsOn{{Service: "a", Condition: "service_started"}},
	})
	acc.AddService("svc", composeService{
		Build:    &composeBuild{Context: ".."},
		Profiles: []string{"app", "ingest"},
		DependsOn: []composeDependsOn{
			{Service: "b", Condition: "service_started"},
		},
	})
	svc := acc.services["svc"]
	if svc.Build == nil || svc.Build.Context != ".." {
		t.Errorf("build not set: %+v", svc.Build)
	}
	if len(svc.Profiles) != 2 || svc.Profiles[0] != "app" || svc.Profiles[1] != "ingest" {
		t.Errorf("profiles dedup-merge failed: %v", svc.Profiles)
	}
	if len(svc.DependsOn) != 2 {
		t.Errorf("depends_on append failed: %v", svc.DependsOn)
	}
}
