// Package emit holds phase-5 emitters. M1 emits the docker-compose
// override for `runtime: docker-compose` targets; M4-cloud will add HCL.
package emit

import (
	"fmt"
	"strings"

	"github.com/paulxiep/caravan/internal/compiler"
)

// SeamServerCommand returns the argv slice that docker compose's
// `command:` should carry for a peer service hosting `seam` in container
// mode. Each implementation owns one language's wire convention:
//
//	Python (M1):  python -m caravan_rpc.serve --interface NAME --impl module:Class --port N
//	Rust   (M2):  <binary> --caravan-serve --interface NAME (TBD at M2)
//
// `port` is the TCP port the peer listens on; M1 uses 8080 for every
// seam. Multi-seam-per-host or per-seam port overrides are post-PoC.
type SeamServerCommand interface {
	Command(seam *compiler.Seam, port int) ([]string, error)
}

// SeamServerCommands is the registry of language emitters. The
// compose-emit looks up the right one by inspecting the seam's `impl:`
// field shape — see `detectLanguage`.
//
// Keyed by a small set of language constants (Python at M1; Rust / TS
// / Go follow at M2+). A nil entry signals "deferred to a later
// milestone"; the emitter surfaces that as a clear error.
var SeamServerCommands = map[Language]SeamServerCommand{
	LanguagePython: pythonSeamServer{},
}

// Language tags a seam's implementation language. Determined from the
// `seams.X.impl:` field shape at emit time.
type Language string

const (
	LanguagePython  Language = "python"
	LanguageRust    Language = "rust"    // M2
	LanguageTS      Language = "ts"      // post-PoC
	LanguageGo      Language = "go"      // post-PoC
	LanguageUnknown Language = "unknown"
)

// detectLanguage inspects a seam's `impl:` field and returns the
// implementation language. The shape conventions:
//
//	"module.path:ClassName"   → Python
//	"binary-name"             → Rust (M2 — same-binary mode-flipped)
//
// Heuristic is intentionally narrow at M1 (only Python ships); the M2
// expansion adds the Rust branch.
func detectLanguage(seam *compiler.Seam) Language {
	switch {
	case seam == nil || seam.Impl == "":
		return LanguageUnknown
	case strings.Contains(seam.Impl, ":") && strings.Contains(seam.Impl, "."):
		return LanguagePython
	default:
		return LanguageUnknown
	}
}

// --- Python -----------------------------------------------------------------

type pythonSeamServer struct{}

// Command emits the argv slice for `python -m caravan_rpc.serve ...`.
// The impl ref shape is `module.path:ClassName`.
func (pythonSeamServer) Command(seam *compiler.Seam, port int) ([]string, error) {
	if seam == nil {
		return nil, fmt.Errorf("nil seam")
	}
	if seam.Impl == "" {
		return nil, fmt.Errorf("seam %s missing impl field", seam.Name)
	}
	return []string{
		"python",
		"-m",
		"caravan_rpc.serve",
		"--interface", seam.Name,
		"--impl", seam.Impl,
		"--port", fmt.Sprintf("%d", port),
	}, nil
}
