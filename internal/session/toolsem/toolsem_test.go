package toolsem

import (
	"testing"

	"github.com/gzzyj/sesscope/internal/session"
)

func TestClassifyGlobUsesPatternAsTarget(t *testing.T) {
	kind, target, _ := Classify(session.Opencode, "glob", []byte(`{"pattern":"tools/*.mjs"}`))
	if kind != session.ToolList {
		t.Errorf("kind = %s", kind)
	}
	if target != "tools/*.mjs" {
		t.Errorf("target = %q，期望 tools/*.mjs", target)
	}
}

func TestClassifyApplyPatchUsesPatchHeader(t *testing.T) {
	args := []byte(`"*** Begin Patch
*** Update File: pkg/x.go
@@
-old
+new
*** End Patch"`)
	kind, target, _ := Classify(session.Codex, "apply_patch", args)
	if kind != session.ToolEdit {
		t.Errorf("kind = %s", kind)
	}
	if target != "pkg/x.go" {
		t.Errorf("target = %q，期望 pkg/x.go", target)
	}
}

func TestClassifyShellKeepsCommand(t *testing.T) {
	kind, target, command := Classify(session.DSH, "bash", []byte(`{"command":"go test ./...","workdir":"/repo"}`))
	if kind != session.ToolShell || command != "go test ./..." || target != "/repo" {
		t.Errorf("kind=%s target=%q command=%q", kind, target, command)
	}
}
