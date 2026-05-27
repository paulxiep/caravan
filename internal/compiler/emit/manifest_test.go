package emit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulxiep/caravan/internal/compiler"
)

// TestApplyPythonPatches covers the three append-line cases in the
// manifest patcher: absent (append), compatible (no-op), conflict (error).
func TestApplyPythonPatches(t *testing.T) {
	patches := []ManifestPatch{{
		Distribution: "caravan-rpc",
		Line:         "caravan-rpc>=0.1.0.dev0",
		Reason:       "caravan-rpc SDK — managed by `caravan compile`; do not hand-edit.",
	}}

	cases := []struct {
		name    string
		input   string
		want    string // expected output (only checked when wantErr is empty)
		wantErr string // substring expected in error message (or empty for success)
	}{
		{
			name:  "absent → append with reason comment",
			input: "",
			want: `# caravan-rpc SDK — managed by ` + "`caravan compile`" + `; do not hand-edit.
caravan-rpc>=0.1.0.dev0
`,
		},
		{
			name: "absent with existing deps → append at end",
			input: `pydantic>=2
requests
`,
			want: `pydantic>=2
requests
# caravan-rpc SDK — managed by ` + "`caravan compile`" + `; do not hand-edit.
caravan-rpc>=0.1.0.dev0
`,
		},
		{
			name: "compatible: bare name → no-op",
			input: `caravan-rpc
pydantic
`,
			want: `caravan-rpc
pydantic
`,
		},
		{
			name: "compatible: exact spec → no-op",
			input: `caravan-rpc>=0.1.0
pydantic
`,
			want: `caravan-rpc>=0.1.0
pydantic
`,
		},
		{
			name: "compatible: PEP-503 normalized name → no-op",
			input: `Caravan_RPC>=0.1.0
`,
			want: `Caravan_RPC>=0.1.0
`,
		},
		{
			name:    "conflict: pinned to different version",
			input:   `caravan-rpc==0.2.0`,
			wantErr: "manifest patch conflict",
		},
		{
			name:    "conflict: stricter upper-bound",
			input:   `caravan-rpc>=0.2.0`,
			wantErr: "manifest patch conflict",
		},
		{
			name:    "conflict: incompatible lower-bound",
			input:   `caravan-rpc<0.1.0`,
			wantErr: "manifest patch conflict",
		},
		{
			name: "comment-stripped: existing line with trailing comment counts",
			input: `caravan-rpc>=0.1.0  # pinned by compiler
`,
			want: `caravan-rpc>=0.1.0  # pinned by compiler
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := applyPythonPatches(tc.input, patches)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil. body:\n%s", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error: got %q, want substring %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("body mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, tc.want)
			}
		})
	}
}

// TestEmitManifestPatches_PythonEntryWritesBuildContext exercises the
// full disk-touching path: reads a user's requirements.txt, applies
// patches, writes to <outDir>/build-context/<entry.Path>/requirements.txt.
func TestEmitManifestPatches_PythonEntryWritesBuildContext(t *testing.T) {
	repoRoot := t.TempDir()
	entryPath := "services/processing"
	if err := os.MkdirAll(filepath.Join(repoRoot, entryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// User's on-disk requirements.txt has unrelated deps; the patcher
	// must leave them in place and append caravan-rpc.
	userManifest := "# user manifest — Caravan must not modify this file on disk\npydantic>=2\n"
	if err := os.WriteFile(filepath.Join(repoRoot, entryPath, "requirements.txt"), []byte(userManifest), 0o644); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(repoRoot, "infra", "dev-bootstrap", "generated")
	rp := &compiler.ResolvedPlan{
		Plan: &compiler.Plan{
			Entries: map[string]*compiler.Entry{
				"processing": {
					Name:     "processing",
					Path:     entryPath,
					Language: compiler.LanguagePython,
				},
			},
		},
	}

	wrote, err := EmitManifestPatches(rp, outDir, repoRoot)
	if err != nil {
		t.Fatalf("EmitManifestPatches: %v", err)
	}
	if len(wrote) != 1 {
		t.Fatalf("wrote count: got %d, want 1 (paths=%v)", len(wrote), wrote)
	}
	wantPath := filepath.Join(outDir, "build-context", entryPath, "requirements.txt")
	if wrote[0] != wantPath {
		t.Errorf("wrote path: got %q, want %q", wrote[0], wantPath)
	}

	// Confirm output content
	emitted, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	body := string(emitted)
	if !strings.Contains(body, "pydantic>=2") {
		t.Errorf("emitted manifest dropped user deps:\n%s", body)
	}
	if !strings.Contains(body, "caravan-rpc>=0.1.1") {
		t.Errorf("emitted manifest missing caravan-rpc line:\n%s", body)
	}

	// Confirm user's on-disk file is untouched (poc_yaml_spec.md §137)
	userAfter, err := os.ReadFile(filepath.Join(repoRoot, entryPath, "requirements.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(userAfter) != userManifest {
		t.Errorf("user's on-disk manifest was modified:\n--- before ---\n%s\n--- after ---\n%s", userManifest, string(userAfter))
	}
}

// TestEmitManifestPatches_RustEntrySkipped confirms Rust entries are
// skipped at M3 (manifest patching for Rust is a future story; the M2
// SDK was added via path-dep, not via manifest patching).
func TestEmitManifestPatches_RustEntrySkipped(t *testing.T) {
	repoRoot := t.TempDir()
	outDir := filepath.Join(repoRoot, "infra", "dev-bootstrap", "generated")
	rp := &compiler.ResolvedPlan{
		Plan: &compiler.Plan{
			Entries: map[string]*compiler.Entry{
				"chat": {
					Name:     "chat",
					Path:     "crates/chat",
					Language: compiler.LanguageRust,
				},
			},
		},
	}
	wrote, err := EmitManifestPatches(rp, outDir, repoRoot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(wrote) != 0 {
		t.Errorf("expected no Rust manifest writes; got %v", wrote)
	}
}

// TestEmitManifestPatches_UserManifestMissing confirms the patcher
// works when the user's requirements.txt doesn't exist yet (treats
// as empty, appends caravan-rpc).
func TestEmitManifestPatches_UserManifestMissing(t *testing.T) {
	repoRoot := t.TempDir()
	entryPath := "services/processing"
	if err := os.MkdirAll(filepath.Join(repoRoot, entryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// Note: NO requirements.txt written in user repo

	outDir := filepath.Join(repoRoot, "infra", "x", "generated")
	rp := &compiler.ResolvedPlan{
		Plan: &compiler.Plan{
			Entries: map[string]*compiler.Entry{
				"processing": {
					Name:     "processing",
					Path:     entryPath,
					Language: compiler.LanguagePython,
				},
			},
		},
	}
	wrote, err := EmitManifestPatches(rp, outDir, repoRoot)
	if err != nil {
		t.Fatalf("EmitManifestPatches: %v", err)
	}
	if len(wrote) != 1 {
		t.Fatalf("wrote count: got %d, want 1", len(wrote))
	}
	body, err := os.ReadFile(wrote[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "caravan-rpc>=0.1.1") {
		t.Errorf("missing caravan-rpc line:\n%s", string(body))
	}
}
