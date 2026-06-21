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
