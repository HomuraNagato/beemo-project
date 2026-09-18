package main

import (
	"testing"

	pb "eve-beemo/proto/gen/proto"
)

func TestConversationEvidencePolicy(t *testing.T) {
	tests := []struct {
		name     string
		messages []*pb.ChatMessage
		level    evidenceLevel
	}{
		{
			name: "local recommendation", level: evidenceRequired,
			messages: []*pb.ChatMessage{{Role: "user", Content: "What's a good place for dinner near Elmhurst NY?"}},
		},
		{
			name: "mutable status", level: evidenceRequired,
			messages: []*pb.ChatMessage{{Role: "user", Content: "Is it still open?"}},
		},
		{
			name: "business closed question", level: evidenceRequired,
			messages: []*pb.ChatMessage{{Role: "user", Content: "Is Example Garden closed?"}},
		},
		{
			name: "correction in local thread", level: evidenceRequired,
			messages: []*pb.ChatMessage{
				{Role: "user", Content: "Find a restaurant near Elmhurst"},
				{Role: "assistant", Content: "Try Example Garden."},
				{Role: "user", Content: "I can't find that address."},
			},
		},
		{
			name: "general recommendation", level: evidencePreferred,
			messages: []*pb.ChatMessage{{Role: "user", Content: "What is a good Eldrazi commander?"}},
		},
		{
			name: "stable knowledge", level: evidenceNone,
			messages: []*pb.ChatMessage{{Role: "user", Content: "Explain how Markov chains work."}},
		},
		{
			name: "technical address", level: evidenceNone,
			messages: []*pb.ChatMessage{{Role: "user", Content: "What is an address space in C?"}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := conversationEvidencePolicy(test.messages).Level; got != test.level {
				t.Fatalf("level = %q, want %q", got, test.level)
			}
		})
	}
}

func TestEvidencePolicyNeedsInternet(t *testing.T) {
	required := evidencePolicy{Level: evidenceRequired, Tool: "internet.search"}
	preferred := evidencePolicy{Level: evidencePreferred, Tool: "internet.search"}
	if !required.needsInternet(false) || !preferred.needsInternet(true) || preferred.needsInternet(false) {
		t.Fatal("unexpected internet enforcement behavior")
	}
}

func TestHasValidInternetCitation(t *testing.T) {
	if !hasValidInternetCitation("Supported by [2].", 3) {
		t.Fatal("expected valid citation")
	}
	if hasValidInternetCitation("Unsupported [4].", 3) || hasValidInternetCitation("No citation.", 3) {
		t.Fatal("accepted invalid citation")
	}
}
