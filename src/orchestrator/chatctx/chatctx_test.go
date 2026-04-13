package chatctx

import (
	"strings"
	"testing"

	pb "eve-beemo/proto/gen/proto"
)

func TestBuildKeepsMostRecentProjectThread(t *testing.T) {
	t.Parallel()

	ctx := Build([]*pb.ChatMessage{
		{Role: "user", Content: "help me debug src/project-a/server.go"},
		{Role: "assistant", Content: "What error are you seeing?"},
		{Role: "user", Content: "now I need help with apps/project-b/main.ts"},
		{Role: "assistant", Content: "What is failing there?"},
		{Role: "user", Content: "what about the tests?"},
	}, 24, 6)

	if strings.Contains(ctx.Transcript, "project-a/server.go") {
		t.Fatalf("active transcript should not keep the older project thread: %q", ctx.Transcript)
	}
	if !strings.Contains(ctx.Transcript, "project-b/main.ts") {
		t.Fatalf("active transcript missing latest project thread: %q", ctx.Transcript)
	}
	if !strings.Contains(ctx.Transcript, "what about the tests?") {
		t.Fatalf("active transcript missing follow-up question: %q", ctx.Transcript)
	}
}

func TestBuildSelectsLatestMeasurementThread(t *testing.T) {
	t.Parallel()

	ctx := Build([]*pb.ChatMessage{
		{Role: "user", Content: "what is the bmi of 45kg?"},
		{Role: "assistant", Content: "What is the height?"},
		{Role: "user", Content: "64inches"},
		{Role: "assistant", Content: "64 inches, BMI 17.03"},
		{Role: "user", Content: "what is the bmi of 134lbs and 172cm?"},
		{Role: "assistant", Content: "The BMI is 20.55."},
		{Role: "user", Content: "what is the bmr?"},
	}, 24, 6)

	if strings.Contains(ctx.Transcript, "45kg") || strings.Contains(ctx.Transcript, "64inches") {
		t.Fatalf("active transcript should drop stale measurement thread: %q", ctx.Transcript)
	}
	if !strings.Contains(ctx.Transcript, "134lbs and 172cm") {
		t.Fatalf("active transcript missing latest measurements: %q", ctx.Transcript)
	}
	if !strings.Contains(ctx.Transcript, "what is the bmr?") {
		t.Fatalf("active transcript missing latest follow-up: %q", ctx.Transcript)
	}
}
