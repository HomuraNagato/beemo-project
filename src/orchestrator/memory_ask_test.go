package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

func TestParseMemoryFollowupQueryRequiresExplicitMarker(t *testing.T) {
	query, ok := parseMemoryFollowupQuery("RETRIEVE: passages connecting El with her mother")
	if !ok || query != "passages connecting El with her mother" {
		t.Fatalf("unexpected follow-up parse: query=%q ok=%t", query, ok)
	}
	if _, ok := parseMemoryFollowupQuery("The context is incomplete."); ok {
		t.Fatal("expected ordinary answer text not to trigger retrieval")
	}
}

func TestCitationOnlyMemoryAnswer(t *testing.T) {
	for _, value := range []string{"[1]", "[1][3]", " [12]. "} {
		if !citationOnlyMemoryAnswer(value) {
			t.Fatalf("expected %q to be citation-only", value)
		}
	}
	for _, value := range []string{"", "Mira Patel [1].", "[source]", "1"} {
		if citationOnlyMemoryAnswer(value) {
			t.Fatalf("did not expect %q to be citation-only", value)
		}
	}
}

func TestBuildMemoryAskPlanningPromptHasNoConflictingFallback(t *testing.T) {
	prompt := buildMemoryAskPlanningPrompt("Who is Mira's mother?", []orchtools.MemoryRetrieveSource{{
		Title: "Family notes",
		Text:  "Mira attended the observatory.",
	}})

	if strings.Contains(prompt, "say what is missing") {
		t.Fatalf("planning prompt contains the answer prompt's insufficient-context instruction:\n%s", prompt)
	}
	if !strings.Contains(prompt, "reply only with `RETRIEVE: `") {
		t.Fatalf("planning prompt does not require a retrieval marker:\n%s", prompt)
	}
}

func TestChatMemoryAnswerRetriesCitationOnlyOutputOnce(t *testing.T) {
	t.Parallel()

	memoryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sources":[{"title":"Roommate questions","text":"How clean are you in a kitchen?","source_uri":"file:///roommate.md"}],"diagnostics":{}}`))
	}))
	t.Cleanup(memoryServer.Close)

	server := testServer(t)
	server.cfg.MemoryBaseURL = memoryServer.URL
	server.cfg.MemoryUserKey = "test-user"
	server.routeSelector = staticRouteSelector{candidates: []routing.Candidate{{
		Route: routing.Route{ID: "memory.answer", Domain: "memory", Handler: routing.Handler{Type: "pipeline", Target: "memory.answer"}},
		Score: 0.93,
	}}}
	server.callCompletion = func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
		return `{"route_id":"memory.answer"}`, nil
	}
	reasonCalls := 0
	server.callFinalMessage = func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
		reasonCalls++
		if reasonCalls == 1 {
			return "[1]", nil
		}
		return "You saved the question, \"How clean are you in a kitchen?\" [1].", nil
	}

	response, err := server.Chat(context.Background(), &pb.ChatRequest{
		SessionId: "memory-citation-retry-test",
		Messages:  []*pb.ChatMessage{{Role: "user", Content: "What question did I save?"}},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if reasonCalls != 2 {
		t.Fatalf("expected one retry, got %d reasoning calls", reasonCalls)
	}
	if !strings.Contains(response.GetText(), "How clean are you") {
		t.Fatalf("unexpected response %q", response.GetText())
	}
}

func TestChatMemoryAnswerAllowsOneBoundedFollowup(t *testing.T) {
	t.Parallel()

	var retrievals atomic.Int32
	memoryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request orchtools.MemoryRetrieveRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode retrieval request: %v", err)
		}
		retrieval := retrievals.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if retrieval == 1 {
			_, _ = w.Write([]byte(`{"sources":[{"id":"first","title":"School notes","text":"El attends the Scholomance.","source_uri":"file:///school.md"}],"diagnostics":{"plan":"lookup"}}`))
			return
		}
		if request.Query != "El mother identity name" {
			t.Fatalf("unexpected follow-up query %q", request.Query)
		}
		_, _ = w.Write([]byte(`{"sources":[{"id":"second","title":"Family notes","text":"Gwen Higgins is El's mother.","source_uri":"file:///family.md"}],"diagnostics":{"plan":"lookup"}}`))
	}))
	t.Cleanup(memoryServer.Close)

	server := testServer(t)
	server.cfg.DeterministicToolShortcuts = false
	server.cfg.MemoryBaseURL = memoryServer.URL
	server.cfg.MemoryUserKey = "test-user"
	server.cfg.MemoryTimeoutMs = 500
	server.routeSelector = staticRouteSelector{candidates: []routing.Candidate{{
		Route: routing.Route{ID: "memory.answer", Domain: "memory", Handler: routing.Handler{Type: "pipeline", Target: "memory.answer"}},
		Score: 0.93,
	}}}
	server.callCompletion = func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
		return `{"route_id":"memory.answer"}`, nil
	}
	reasonCalls := 0
	server.callFinalMessage = func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
		reasonCalls++
		if reasonCalls == 1 {
			return "RETRIEVE: El mother identity name", nil
		}
		if !strings.Contains(prompt, "El attends the Scholomance.") || !strings.Contains(prompt, "Gwen Higgins is El's mother.") {
			t.Fatalf("final prompt missing combined evidence: %q", prompt)
		}
		return "El's mother is Gwen Higgins [2].", nil
	}

	response, err := server.Chat(context.Background(), &pb.ChatRequest{
		SessionId: "memory-followup-test",
		Messages:  []*pb.ChatMessage{{Role: "user", Content: "What is El's mother's name?"}},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if response.GetText() != "El's mother is Gwen Higgins [2]." {
		t.Fatalf("unexpected response %q", response.GetText())
	}
	if retrievals.Load() != 2 || reasonCalls != 2 {
		t.Fatalf("expected one bounded follow-up, retrievals=%d reason_calls=%d", retrievals.Load(), reasonCalls)
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
