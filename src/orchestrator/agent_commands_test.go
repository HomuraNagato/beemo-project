package main

import (
	"context"
	"strings"
	"testing"

	pb "eve-beemo/proto/gen/proto"
	"eve-beemo/src/orchestrator/config"
)

func TestParseSlashCommandUsesRegistryForHiddenControls(t *testing.T) {
	tests := map[string]agentControl{
		"/code":         {action: "code"},
		"/normal":       {action: "normal"},
		"/research":     {action: "research"},
		"/research RAG": {action: "research", target: "RAG"},
		"/switch":       {action: "switch"},
		"/switch beemo": {action: "switch", target: "beemo"},
		"/internet":     {action: "internet"},
		"  /INTERNET  ": {action: "internet"},
	}
	for input, expected := range tests {
		got, ok := parseSlashCommand(input)
		if !ok || got != expected {
			t.Fatalf("parseSlashCommand(%q) = %#v, %v; want %#v", input, got, ok, expected)
		}
	}
}

func TestListAgentCommandsReturnsDiscoverableCommandsWithConfigurationStatus(t *testing.T) {
	server := &orchestratorServer{cfg: config.Config{
		InternetProvider: "tavily",
		InternetAPIKey:   "tavily-key",
	}}
	response, err := server.ListAgentCommands(context.Background(), &pb.CommandListRequest{})
	if err != nil {
		t.Fatalf("ListAgentCommands returned error: %v", err)
	}
	if len(response.GetCommands()) != 5 {
		t.Fatalf("unexpected commands: %#v", response.GetCommands())
	}
	if response.GetCommands()[0].GetCommand() != "/code" || response.GetCommands()[1].GetCommand() != "/normal" || response.GetCommands()[2].GetCommand() != "/research" || response.GetCommands()[4].GetCommand() != "/internet" {
		t.Fatalf("unexpected commands: %#v", response.GetCommands())
	}

	server.cfg.InternetAPIKey = ""
	response, err = server.ListAgentCommands(context.Background(), &pb.CommandListRequest{})
	if err != nil {
		t.Fatalf("ListAgentCommands without key returned error: %v", err)
	}
	if len(response.GetCommands()) != 5 || response.GetCommands()[4].GetCommand() != "/internet" {
		t.Fatalf("expected internet command to remain discoverable without a key: %#v", response.GetCommands())
	}
	if !strings.Contains(response.GetCommands()[4].GetDescription(), "TAVILY_API_KEY") {
		t.Fatalf("expected internet configuration guidance, got %#v", response.GetCommands()[4])
	}
}
