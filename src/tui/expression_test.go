package main

import "testing"

func TestExpressionForAssistantReplyUsesExplicitTag(t *testing.T) {
	t.Parallel()

	expr := expressionForAssistantReply("[emotion: curious] What detail should I look up?")
	if expr.Emotion != "curious" {
		t.Fatalf("expected curious expression, got %q", expr.Emotion)
	}
}

func TestExpressionForAssistantReplyInfersFromText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		reply string
		want  string
	}{
		{
			name:  "clarification",
			reply: "I need more detail before I can answer.",
			want:  "concerned",
		},
		{
			name:  "question",
			reply: "What detail should I look up?",
			want:  "curious",
		},
		{
			name:  "happy",
			reply: "Great, that works.",
			want:  "happy",
		},
		{
			name:  "failure",
			reply: "request failed: unavailable",
			want:  "error",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			expr := expressionForAssistantReply(tt.reply)
			if expr.Emotion != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, expr.Emotion)
			}
		})
	}
}

func TestStripExpressionTag(t *testing.T) {
	t.Parallel()

	got := stripExpressionTag("[face: happy] Done.")
	if got != "Done." {
		t.Fatalf("expected clean reply, got %q", got)
	}
}

func TestUnknownExpressionFallsBackToNeutral(t *testing.T) {
	t.Parallel()

	expr := expressionForEmotion("not-real")
	if expr.Emotion != "neutral" {
		t.Fatalf("expected neutral fallback, got %q", expr.Emotion)
	}
}
