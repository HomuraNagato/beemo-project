package main

import (
	"testing"

	"eve-beemo/src/orchestrator/llm"
)

func TestProgressForCompletionReportsReachedOutputLimit(t *testing.T) {
	progress := progressForCompletion(llm.Completion{
		FinishReason: "length",
		Usage:        llm.Usage{PromptTokens: 4200, CompletionTokens: 1024},
	}, 8192, 1024)
	if progress.Warning != "output_limit" || !progress.Incomplete {
		t.Fatalf("unexpected progress: %+v", progress)
	}
}

func TestCodeStepProgressWarnsAtEightyPercent(t *testing.T) {
	before := codeStepProgress(51, 64)
	if before.Warning != "" {
		t.Fatalf("unexpected early warning: %+v", before)
	}
	atLimit := codeStepProgress(52, 64)
	if atLimit.Warning != "tool_near_limit" {
		t.Fatalf("expected warning at threshold: %+v", atLimit)
	}
}

func TestToolActivityUsesOperationalLabels(t *testing.T) {
	if got, want := toolActivity("code.search"), "Searching files"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
