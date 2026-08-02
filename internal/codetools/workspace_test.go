package codetools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolverRejectsPathsOutsideRootsAndSymlinkEscapes(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver([]string{root})
	if _, err := resolver.Workspace(outside); err == nil {
		t.Fatal("expected outside workspace to be rejected")
	}
	if _, err := resolver.ExistingPath(root, "escape"); err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
}

func TestApprovalReasonCoversDependencyAndDestructiveCommands(t *testing.T) {
	tests := []string{
		"pnpm add lucide-svelte",
		"go get example.com/module",
		"git reset --hard HEAD",
		"rm -rf build",
	}
	for _, command := range tests {
		if reason := approvalReason(command); reason == "" {
			t.Fatalf("expected approval for %q", command)
		}
	}
	if reason := approvalReason("go test ./..."); reason != "" {
		t.Fatalf("did not expect approval for tests: %s", reason)
	}
}

func TestValidatePatchRejectsTraversal(t *testing.T) {
	patch := "--- a/file\n+++ b/../../outside\n@@ -1 +1 @@\n-old\n+new\n"
	if err := validatePatch(patch); err == nil {
		t.Fatal("expected traversal patch to be rejected")
	}
}

func TestSensitivePathsRequireApproval(t *testing.T) {
	for _, path := range []string{".env", ".env.local", "service-account.key", "api_secrets.json"} {
		if !sensitivePath(path) {
			t.Fatalf("expected %q to be sensitive", path)
		}
	}
	if sensitivePath(".env.example") {
		t.Fatal("example environment file should remain readable")
	}
}
