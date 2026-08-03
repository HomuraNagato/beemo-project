package lifecycle

import (
	"os"
	"strings"
	"testing"
)

func TestRotateSessionPersistsNewSession(t *testing.T) {
	paths := Paths{BeemoRoot: t.TempDir()}
	first, err := RotateSession(paths)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RotateSession(paths)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("expected unique sessions, got %q", first)
	}
	active, err := ReadSession(paths)
	if err != nil {
		t.Fatal(err)
	}
	if active != second || !strings.HasPrefix(active, "beemo-") {
		t.Fatalf("unexpected active session %q", active)
	}
	if info, err := os.Stat(SessionPath(paths)); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("unexpected session file: info=%v err=%v", info, err)
	}
}
