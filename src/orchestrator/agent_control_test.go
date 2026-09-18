package main

import (
	"strings"
	"testing"

	orchdb "eve-beemo/src/orchestrator/db"
)

func TestParseAgentControlRecognizesExplicitWholeUtterances(t *testing.T) {
	tests := map[string]agentControl{
		"/code":                   {action: "code"},
		"Enter programming mode.": {action: "code"},
		"exit code mode":          {action: "normal"},
		"/research vector search": {action: "research", target: "vector search"},
		"switch projects":         {action: "switch"},
		"/switch memory_palace":   {action: "switch", target: "memory_palace"},
		"/internet":               {action: "internet"},
		"switch to project Beemo": {action: "switch", target: "Beemo"},
	}
	for input, expected := range tests {
		got, ok := parseAgentControl(input)
		if !ok || got != expected {
			t.Fatalf("parseAgentControl(%q) = %#v, %v; want %#v", input, got, ok, expected)
		}
	}
}

func TestParseAgentControlDoesNotCaptureProgrammingDiscussion(t *testing.T) {
	for _, input := range []string{
		"How do I enter code mode in this editor?",
		"Can you explain how to switch projects?",
		"Write documentation for programming mode.",
		"How does /internet work?",
		"/internet on",
	} {
		if command, ok := parseAgentControl(input); ok {
			t.Fatalf("unexpected control for %q: %#v", input, command)
		}
	}
}

func TestInternetConfigurationMessage(t *testing.T) {
	if got := internetConfigurationMessage("tavily", "key"); got != "" {
		t.Fatalf("configured Tavily returned message %q", got)
	}
	if got := internetConfigurationMessage("tavily", ""); !strings.Contains(got, "TAVILY_API_KEY") {
		t.Fatalf("missing Tavily key returned message %q", got)
	}
	if got := internetConfigurationMessage("openai", "key"); !strings.Contains(got, "not supported") {
		t.Fatalf("unsupported provider returned message %q", got)
	}
}

func TestMatchProjectNormalizesNamesAndDetectsAmbiguity(t *testing.T) {
	projects := []orchdb.AgentProject{{Path: "/one/memory_palace", Name: "memory_palace"}, {Path: "/two/memory-palace", Name: "memory-palace"}}
	if _, count := matchProject(projects, "memory palace"); count != 2 {
		t.Fatalf("expected ambiguous normalized project name, got %d matches", count)
	}
	project, count := matchProject(projects, "/one/memory_palace")
	if count != 1 || project.Path != "/one/memory_palace" {
		t.Fatalf("expected exact path match, got %#v (%d matches)", project, count)
	}
}
