package codetools

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	pb "eve-beemo/proto/gen/proto"
)

func TestSandboxedCommandIntegration(t *testing.T) {
	if os.Getenv("BEEMO_CODE_INTEGRATION") != "1" {
		t.Skip("set BEEMO_CODE_INTEGRATION=1 to exercise Bubblewrap")
	}
	root := t.TempDir()
	service := NewService(Config{Roots: []string{root}, MaxOutput: 4096, MaxReadBytes: 4096, CommandTTL: 10})
	result, err := service.Execute(context.Background(), &pb.CodeToolRequest{
		SessionId: "integration", Workspace: root, Action: "code.exec",
		ArgsJson: `{"command":"printf sandbox-ok"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.GetStatus() != "ok" || result.GetOutput() != "sandbox-ok" {
		t.Fatalf("unexpected sandbox result: %#v", result)
	}

	sibling := t.TempDir()
	result, err = service.Execute(context.Background(), &pb.CodeToolRequest{
		SessionId: "integration", Workspace: root, Action: "code.exec",
		ArgsJson: `{"command":"test ! -e ` + sibling + ` && printf sibling-hidden"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.GetStatus() != "ok" || result.GetOutput() != "sibling-hidden" {
		t.Fatalf("expected sibling path to be hidden: %#v", result)
	}

	result, err = service.Execute(context.Background(), &pb.CodeToolRequest{
		SessionId: "integration", Workspace: root, Action: "code.process_start",
		ArgsJson: `{"command":"printf started; sleep 0.05; printf finished"}`,
	})
	if err != nil || result.GetStatus() != "ok" {
		t.Fatalf("start process: result=%#v err=%v", result, err)
	}
	started := struct {
		ProcessID string `json:"process_id"`
	}{}
	if err := json.Unmarshal([]byte(result.GetOutput()), &started); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	result, err = service.Execute(context.Background(), &pb.CodeToolRequest{
		SessionId: "integration", Workspace: root, Action: "code.process_poll",
		ArgsJson: `{"process_id":"` + started.ProcessID + `"}`,
	})
	if err != nil || result.GetStatus() != "ok" || !strings.Contains(result.GetOutput(), "startedfinished") {
		t.Fatalf("poll process: result=%#v err=%v", result, err)
	}
}
