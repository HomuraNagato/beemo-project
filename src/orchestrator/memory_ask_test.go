package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pb "eve-beemo/proto/gen/proto"
	"eve-beemo/src/orchestrator/routing"
	orchtools "eve-beemo/src/orchestrator/tools"
)

func TestBuildMemoryAskPromptUsesMemoryPalaceEvidence(t *testing.T) {
	source := orchtools.MemoryRetrieveSource{
		Title:        "Long source",
		Text:         strings.Repeat("unrelated source text. ", 200),
		EvidenceText: "The note said the architect was Mira Patel.",
		SourceURI:    "memory://test",
		UpdatedAt:    "2026-07-10T00:00:00Z",
	}

	prompt := buildMemoryAskPrompt("who designed the observatory?", []orchtools.MemoryRetrieveSource{source})

	if !strings.Contains(prompt, "Mira Patel") {
		t.Fatalf("prompt did not include relevant answer text:\n%s", prompt)
	}
	if strings.Contains(prompt, "unrelated source text") {
		t.Fatalf("prompt ignored Memory Palace evidence text")
	}
}

func TestMemoryAskExcerptRemovesInternalChunkLabels(t *testing.T) {
	text := "Chunk 41\nEarlier context.\n\n---\n\nChunk 42\nMira Patel designed the observatory."
	excerpt := memoryAskEvidence(orchtools.MemoryRetrieveSource{Text: text})
	if strings.Contains(excerpt, "Chunk 411") || strings.Contains(excerpt, "Chunk 412") || strings.Contains(excerpt, "---") {
		t.Fatalf("expected internal chunk markers removed, got %q", excerpt)
	}
	if !strings.Contains(excerpt, "Mira Patel designed the observatory.") {
		t.Fatalf("expected evidence preserved, got %q", excerpt)
	}
}

func TestMemoryAnswerProfileKeepsExplanationsLong(t *testing.T) {
	if got := memoryAnswerProfileFor("Describe what linear algebra is"); got.name != "expansive" || got.maxTokens != 384 || got.sourceLimit != memoryAskSourceLimit {
		t.Fatalf("unexpected expansive profile: %+v", got)
	}
	if got := memoryAnswerProfileFor("What is El's mother's name?"); got.name != "direct" || got.maxTokens != 64 || got.sourceLimit != 4 {
		t.Fatalf("unexpected direct profile: %+v", got)
	}
	if got := memoryAnswerProfileFor("What is an important book El receives?"); got.name != "direct" || got.maxTokens != 64 {
		t.Fatalf("unexpected direct book profile: %+v", got)
	}
	if got := memoryAnswerProfileFor("What is linear algebra?"); got.name != "standard" || got.maxTokens != 192 {
		t.Fatalf("unexpected standard profile: %+v", got)
	}
}

func TestChatMemoryAnswerRouteRunsRetrievalPipeline(t *testing.T) {
	t.Parallel()

	memoryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/retrieve" {
			http.NotFound(w, r)
			return
		}
		var request orchtools.MemoryRetrieveRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode retrieval request: %v", err)
		}
		if got, want := request.Query, "who designed the observatory?"; got != want {
			t.Fatalf("unexpected retrieval query: got %q want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sources":[{"title":"Observatory notes","text":"Mira Patel designed the observatory.","source_uri":"file:///notes/observatory.md","updated_at":"2026-07-10T00:00:00Z"}],"diagnostics":{"candidate_count":18,"search_ms":12,"rerank_ms":34,"total_ms":49,"reranker":"onnx","reranker_model":"test-reranker"}}`))
	}))
	t.Cleanup(memoryServer.Close)

	server := testServer(t)
	server.cfg.DeterministicToolShortcuts = false
	server.cfg.MemoryBaseURL = memoryServer.URL
	server.cfg.MemoryUserKey = "test-user"
	server.cfg.MemoryTimeoutMs = 500
	server.routeSelector = staticRouteSelector{candidates: []routing.Candidate{{
		Route: routing.Route{
			ID:      "memory.answer",
			Domain:  "memory",
			Handler: routing.Handler{Type: "pipeline", Target: "memory.answer"},
		},
		Score: 0.93,
	}}}
	server.callCompletion = func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
		return `{"route_id":"memory.answer"}`, nil
	}
	server.callFinalMessage = func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
		if !strings.Contains(prompt, "Mira Patel designed the observatory.") {
			t.Fatalf("memory answer prompt missing retrieved evidence: %q", prompt)
		}
		return "Mira Patel designed it [1].", nil
	}

	response, err := server.Chat(context.Background(), &pb.ChatRequest{
		SessionId: "memory-route-test",
		Messages:  []*pb.ChatMessage{{Role: "user", Content: "who designed the observatory?"}},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if got, want := response.GetText(), "Mira Patel designed it [1]."; got != want {
		t.Fatalf("unexpected response: got %q want %q", got, want)
	}
	if got := response.GetTools(); len(got) != 2 || got[0] != "memory.retrieve" || got[1] != "beemo.reason" {
		t.Fatalf("unexpected tools: %#v", got)
	}
}
