package compiler

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHybridTargetValidation covers M4-cloud's surface invariants on a
// target that opts into creds_passthrough. Each case writes a minimal
// caravan.yaml to a tmp dir and asserts the expected diagnostic.
func TestHybridTargetValidation(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string // substring expected in diagnostic; "" means no errors
	}{
		{
			name: "happy path: all fields present",
			yaml: `name: app
default_target: hybrid-dev
resources:
  blobs: { type: bucket }
entries:
  api:
    path: ./api
    uses: [blobs]
targets:
  hybrid-dev:
    runtime: docker-compose
    region: us-east-1
    default_composition: cloud-managed
    creds_passthrough: true
    backend:
      bucket: my-state-bucket
      lock_table: my-lock-table
    entries: { api: container }
`,
			want: "",
		},
		{
			name: "missing backend",
			yaml: `name: app
resources:
  blobs: { type: bucket }
entries:
  api: { path: ./api, uses: [blobs] }
targets:
  hybrid-dev:
    runtime: docker-compose
    region: us-east-1
    default_composition: cloud-managed
    creds_passthrough: true
    entries: { api: container }
`,
			want: "requires `backend:`",
		},
		{
			name: "backend present but bucket empty",
			yaml: `name: app
resources:
  blobs: { type: bucket }
entries:
  api: { path: ./api, uses: [blobs] }
targets:
  hybrid-dev:
    runtime: docker-compose
    region: us-east-1
    default_composition: cloud-managed
    creds_passthrough: true
    backend:
      lock_table: my-lock-table
    entries: { api: container }
`,
			want: "backend.bucket is required",
		},
		{
			name: "backend present but lock_table empty",
			yaml: `name: app
resources:
  blobs: { type: bucket }
entries:
  api: { path: ./api, uses: [blobs] }
targets:
  hybrid-dev:
    runtime: docker-compose
    region: us-east-1
    default_composition: cloud-managed
    creds_passthrough: true
    backend:
      bucket: my-state-bucket
    entries: { api: container }
`,
			want: "backend.lock_table is required",
		},
		{
			name: "missing region",
			yaml: `name: app
resources:
  blobs: { type: bucket }
entries:
  api: { path: ./api, uses: [blobs] }
targets:
  hybrid-dev:
    runtime: docker-compose
    default_composition: cloud-managed
    creds_passthrough: true
    backend: { bucket: b, lock_table: l }
    entries: { api: container }
`,
			want: "requires `region:`",
		},
		{
			name: "no cloud-managed resource",
			yaml: `name: app
resources:
  blobs: { type: bucket, composition: oss-local }
entries:
  api: { path: ./api, uses: [blobs] }
targets:
  hybrid-dev:
    runtime: docker-compose
    region: us-east-1
    creds_passthrough: true
    backend: { bucket: b, lock_table: l }
    entries: { api: container }
`,
			want: "no resource resolves to cloud-managed",
		},
		{
			name: "cloud-managed via per-resource override is enough",
			yaml: `name: app
resources:
  blobs: { type: bucket, composition: oss-local }
entries:
  api: { path: ./api, uses: [blobs] }
targets:
  hybrid-dev:
    runtime: docker-compose
    region: us-east-1
    creds_passthrough: true
    backend: { bucket: b, lock_table: l }
    composition: { blobs: cloud-managed }
    entries: { api: container }
`,
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			path := filepath.Join(tmp, "caravan.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			_, diag, err := CompileFile(path)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			buf := &bytes.Buffer{}
			_, _ = diag.WriteTo(buf)
			body := buf.String()
			if tc.want == "" {
				if diag.HasErrors() {
					t.Fatalf("expected no errors; got:\n%s", body)
				}
				return
			}
			if !strings.Contains(body, tc.want) {
				t.Errorf("expected diagnostic containing %q; got:\n%s", tc.want, body)
			}
		})
	}
}

// TestHybridTargetDefaults covers the defaulter for hybrid targets:
// AwsProfile falls back to DefaultAwsProfile; Backend.Region inherits
// from Target.Region; Backend.Key defaults to "<app>/<target>.tfstate".
func TestHybridTargetDefaults(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "caravan.yaml")
	yamlBody := `name: invoice-parse
resources:
  blobs: { type: bucket }
entries:
  api: { path: ./api, uses: [blobs] }
targets:
  hybrid-dev:
    runtime: docker-compose
    region: us-east-1
    default_composition: cloud-managed
    creds_passthrough: true
    backend: { bucket: my-state, lock_table: my-lock }
    entries: { api: container }
`
	if err := os.WriteFile(path, []byte(yamlBody), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, diag, err := CompileFile(path)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if diag.HasErrors() {
		buf := &bytes.Buffer{}
		_, _ = diag.WriteTo(buf)
		t.Fatalf("unexpected errors:\n%s", buf.String())
	}
	tgt := plan.Targets["hybrid-dev"]
	if tgt == nil {
		t.Fatal("hybrid-dev target missing")
	}
	if got, want := tgt.AwsProfile, DefaultAwsProfile; got != want {
		t.Errorf("AwsProfile: got %q want %q", got, want)
	}
	if got, want := tgt.Backend.Region, "us-east-1"; got != want {
		t.Errorf("Backend.Region: got %q want %q", got, want)
	}
	if got, want := tgt.Backend.Key, "invoice-parse/hybrid-dev.tfstate"; got != want {
		t.Errorf("Backend.Key: got %q want %q", got, want)
	}
}

// TestHybridTargetExplicitOverrides confirms explicit yaml values for
// AwsProfile / Backend.Region / Backend.Key are not stomped by the
// defaulter.
func TestHybridTargetExplicitOverrides(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "caravan.yaml")
	yamlBody := `name: app
resources:
  blobs: { type: bucket }
entries:
  api: { path: ./api, uses: [blobs] }
targets:
  hybrid-dev:
    runtime: docker-compose
    region: us-east-1
    aws_profile: custom-profile
    default_composition: cloud-managed
    creds_passthrough: true
    backend:
      bucket: my-state
      lock_table: my-lock
      region: us-west-2
      key: custom/path.tfstate
    entries: { api: container }
`
	if err := os.WriteFile(path, []byte(yamlBody), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, _, err := CompileFile(path)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	tgt := plan.Targets["hybrid-dev"]
	if got, want := tgt.AwsProfile, "custom-profile"; got != want {
		t.Errorf("AwsProfile: got %q want %q", got, want)
	}
	if got, want := tgt.Backend.Region, "us-west-2"; got != want {
		t.Errorf("Backend.Region: got %q want %q", got, want)
	}
	if got, want := tgt.Backend.Key, "custom/path.tfstate"; got != want {
		t.Errorf("Backend.Key: got %q want %q", got, want)
	}
}
