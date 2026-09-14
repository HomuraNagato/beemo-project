package main

import (
	"strings"
	"testing"

	orchtools "eve-beemo/src/orchestrator/tools"
)

func TestToolResultForPromptFormatsGetTime(t *testing.T) {
	t.Parallel()

	formatted := toolResultForPrompt(orchtools.Result{
		Action: "get_time",
		Output: "2026-06-21T19:31:05Z",
	})

	if !strings.Contains(formatted, "June 21, 2026 at 7:31 PM UTC") {
		t.Fatalf("formatted time missing human text: %q", formatted)
	}
	if !strings.Contains(formatted, "2026-06-21T19:31:05Z") {
		t.Fatalf("formatted time missing raw timestamp: %q", formatted)
	}
}

func TestAppendInternetSourcesDeduplicatesURLs(t *testing.T) {
	result := appendInternetSources("Answer [1].", []orchtools.Result{{
		Action: "internet.search",
		Sources: []orchtools.Source{
			{Title: "Primary", URL: "https://example.com/one"},
			{Title: "Duplicate", URL: "https://example.com/one"},
			{Title: "Secondary", URL: "https://example.com/two"},
		},
	}})
	for _, expected := range []string{"Answer [1].", "[1] Primary: https://example.com/one", "[2] Secondary: https://example.com/two"} {
		if !strings.Contains(result, expected) {
			t.Fatalf("result missing %q: %s", expected, result)
		}
	}
	if strings.Contains(result, "Duplicate") {
		t.Fatalf("duplicate source was retained: %s", result)
	}
}
