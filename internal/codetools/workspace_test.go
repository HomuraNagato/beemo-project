package codetools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "eve-beemo/proto/gen/proto"
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
	if got, err := resolver.ExistingPath(root, root); err != nil || got != root {
		t.Fatalf("expected absolute workspace path to resolve: got=%q err=%v", got, err)
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

func TestSearchOutputUsesDefaultAndHardResultLimits(t *testing.T) {
	service := NewService(Config{
		Roots:                []string{t.TempDir()},
		SearchDefaultResults: 2,
		SearchMaxResults:     3,
		SearchMaxBytes:       1024,
	})
	output := "one\ntwo\nthree\nfour"
	if got := service.limitSearchOutput(output, 0); got != "one\ntwo\n[truncated: 2 additional results]" {
		t.Fatalf("unexpected default-limited output: %q", got)
	}
	got := service.limitSearchOutput(output, 99)
	if !strings.HasPrefix(got, "one\ntwo\nthree\n") || !strings.Contains(got, "1 additional results") {
		t.Fatalf("expected hard result limit, got %q", got)
	}
}

func TestSearchOutputUsesByteLimit(t *testing.T) {
	got := limitResultLines("12345\n67890\nlast", 50, 11)
	if got != "12345\n67890\n[truncated: 1 additional results]" {
		t.Fatalf("unexpected byte-limited output: %q", got)
	}
}

func TestCreateMakesOnlyNewWorkspaceFile(t *testing.T) {
	root := t.TempDir()
	service := NewService(Config{Roots: []string{root}, MaxReadBytes: 1024})
	request := &pb.CodeToolRequest{
		SessionId: "create-test", Workspace: root, Action: "code.create",
		ArgsJson: `{"path":"cmd/hello/main.go","content":"package main\n"}`,
	}
	result, err := service.Execute(context.Background(), request)
	if err != nil || result.GetStatus() != "ok" || !result.GetChanged() {
		t.Fatalf("create failed: result=%#v err=%v", result, err)
	}
	content, err := os.ReadFile(filepath.Join(root, "cmd/hello/main.go"))
	if err != nil || string(content) != "package main\n" {
		t.Fatalf("unexpected created file: content=%q err=%v", content, err)
	}
	result, err = service.Execute(context.Background(), request)
	if err != nil || result.GetStatus() != "error" || !strings.Contains(result.GetError(), "already exists") {
		t.Fatalf("expected overwrite rejection: result=%#v err=%v", result, err)
	}
}

func TestSearchRejectsNonBooleanFixedStrings(t *testing.T) {
	root := t.TempDir()
	service := NewService(Config{Roots: []string{root}})
	result, err := service.Execute(context.Background(), &pb.CodeToolRequest{
		SessionId: "search-args", Workspace: root, Action: "code.search",
		ArgsJson: `{"query":"needle","fixed_strings":["needle"]}`,
	})
	if err != nil || result.GetStatus() != "error" || !strings.Contains(result.GetError(), "must be a boolean") {
		t.Fatalf("expected fixed_strings validation: result=%#v err=%v", result, err)
	}
}
