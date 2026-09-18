package tools

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestGetCoreContextUsesTenantScopedEndpoint(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.String() != "http://memory.test/v1/core-context" {
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL)
		}
		if got := request.Header.Get("X-Memory-User-Key"); got != "nagato" {
			t.Fatalf("unexpected user key %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"body":"Prefers concise answers.","updated_at":"2026-09-16T12:00:00Z"}`)),
		}, nil
	})}
	result, err := GetCoreContext(context.Background(), MemoryConfig{BaseURL: "http://memory.test", UserKey: "nagato", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	if result.Body != "Prefers concise answers." {
		t.Fatalf("unexpected core context: %#v", result)
	}
}
