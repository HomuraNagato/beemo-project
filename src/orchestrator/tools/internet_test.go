package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestTavilyInternetSearchReturnsBoundedNumberedEvidence(t *testing.T) {
	originalDo := internetHTTPDo
	t.Cleanup(func() { internetHTTPDo = originalDo })
	internetHTTPDo = func(req *http.Request) (*http.Response, error) {
		if got, want := req.Header.Get("Authorization"), "Bearer tavily-key"; got != want {
			t.Fatalf("unexpected authorization: got %q want %q", got, want)
		}
		var payload tavilySearchRequest
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if payload.Query != "current Svelte release" || payload.MaxResults != 8 || payload.SearchDepth != "fast" || payload.ChunksPerSource != 2 {
			t.Fatalf("unexpected request payload: %#v", payload)
		}
		if payload.IncludeAnswer || payload.IncludeRawContent || payload.IncludeImages {
			t.Fatalf("unexpected optional Tavily content: %#v", payload)
		}
		body := `{"results":[{"url":"https://example.com/svelte","title":"Svelte release","content":"Version details.","score":0.9},{"url":"https://example.com/svelte","title":"duplicate","content":"Duplicate."}],"response_time":"0.42","request_id":"request-a","usage":{"credits":1}}`
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(bytes.NewBufferString(body))}, nil
	}

	raw, _ := json.Marshal(internetSearchArgs{Query: "current Svelte release"})
	result, err := executeInternetSearch(context.Background(), Request{SessionID: "session-a", Action: "internet.search", Args: raw}, InternetConfig{Provider: "tavily", APIKey: "tavily-key"})
	if err != nil {
		t.Fatalf("executeInternetSearch returned error: %v", err)
	}
	if len(result.Sources) != 1 || result.Sources[0].URL != "https://example.com/svelte" {
		t.Fatalf("unexpected sources: %#v", result.Sources)
	}
	for _, expected := range []string{"[1] Svelte release", "URL: https://example.com/svelte", "Version details."} {
		if !strings.Contains(result.Output, expected) {
			t.Fatalf("evidence missing %q: %s", expected, result.Output)
		}
	}
}

func TestInternetEvidenceHonorsContextLimit(t *testing.T) {
	result := formatInternetEvidence("internet.search", []internetEvidence{{
		Title: "Long result", URL: "https://example.com", Content: strings.Repeat("x", 3000),
	}}, 512)
	if runes := len([]rune(result.Output)); runes > 512*4 {
		t.Fatalf("evidence exceeded context limit: %d runes", runes)
	}
	if !strings.HasSuffix(result.Output, "...") {
		t.Fatalf("expected truncated evidence, got %d bytes", len(result.Output))
	}
}

func TestInternetSearchRequiresConfiguredProvider(t *testing.T) {
	request := Request{Action: "internet.search", Args: json.RawMessage(`{"query":"test"}`)}
	if _, err := executeInternetSearch(context.Background(), request, InternetConfig{Provider: "tavily"}); err == nil || !strings.Contains(err.Error(), "TAVILY_API_KEY") {
		t.Fatalf("expected missing-key error, got %v", err)
	}
	if _, err := executeInternetSearch(context.Background(), request, InternetConfig{Provider: "unknown", APIKey: "key"}); err == nil || !strings.Contains(err.Error(), "unsupported internet search provider") {
		t.Fatalf("expected unsupported-provider error, got %v", err)
	}
}

func TestTavilyBasicSearchOmitsChunksPerSource(t *testing.T) {
	originalDo := internetHTTPDo
	t.Cleanup(func() { internetHTTPDo = originalDo })
	internetHTTPDo = func(req *http.Request) (*http.Response, error) {
		var payload map[string]any
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if _, ok := payload["chunks_per_source"]; ok {
			t.Fatalf("basic search must omit chunks_per_source: %#v", payload)
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(`{"results":[]}`))}, nil
	}

	raw, _ := json.Marshal(internetSearchArgs{Query: "test"})
	_, err := executeInternetSearch(context.Background(), Request{Action: "internet.search", Args: raw}, InternetConfig{
		Provider: "tavily", APIKey: "key", SearchDepth: "basic",
	})
	if err != nil {
		t.Fatalf("executeInternetSearch returned error: %v", err)
	}
}

func TestTavilyResponseTimeAcceptsStringAndNumber(t *testing.T) {
	for input, expected := range map[string]string{
		`"0.42"`: "0.42",
		`0.42`:   "0.42",
		`null`:   "",
	} {
		if got := jsonScalarText(json.RawMessage(input)); got != expected {
			t.Fatalf("jsonScalarText(%s) = %q, want %q", input, got, expected)
		}
	}
}
