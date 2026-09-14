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

func TestInternetSearchReturnsNumberedEvidenceAndSources(t *testing.T) {
	originalDo := internetHTTPDo
	t.Cleanup(func() { internetHTTPDo = originalDo })
	internetHTTPDo = func(req *http.Request) (*http.Response, error) {
		if got, want := req.Header.Get("X-Subscription-Token"), "brave-key"; got != want {
			t.Fatalf("unexpected API key: got %q want %q", got, want)
		}
		var payload braveContextRequest
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if payload.Query != "current Svelte release" || payload.MaxURLs != 8 || payload.MaxTokens != 6000 {
			t.Fatalf("unexpected request payload: %#v", payload)
		}
		body := `{"grounding":{"generic":[{"url":"https://example.com/svelte","title":"Svelte release","snippets":["Version details."]},{"url":"https://example.com/svelte","title":"duplicate","snippets":["Duplicate."]}]}}`
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(bytes.NewBufferString(body))}, nil
	}

	raw, _ := json.Marshal(internetSearchArgs{Query: "current Svelte release"})
	result, err := executeInternetSearch(context.Background(), Request{SessionID: "session-a", Action: "internet.search", Args: raw}, InternetConfig{APIKey: "brave-key"})
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

func TestInternetSearchRequiresAPIKey(t *testing.T) {
	_, err := executeInternetSearch(context.Background(), Request{Action: "internet.search", Args: json.RawMessage(`{"query":"test"}`)}, InternetConfig{})
	if err == nil || !strings.Contains(err.Error(), "BRAVE_SEARCH_API_KEY") {
		t.Fatalf("expected missing-key error, got %v", err)
	}
}
