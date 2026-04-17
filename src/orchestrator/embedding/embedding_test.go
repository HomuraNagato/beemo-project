package embedding

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func withMockHTTPClient(t *testing.T, fn roundTripFunc) {
	t.Helper()
	old := newHTTPClient
	newHTTPClient = func() *http.Client {
		return &http.Client{Transport: fn}
	}
	t.Cleanup(func() {
		newHTTPClient = old
	})
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestCallSendsExpectedEmbeddingRequest(t *testing.T) {
	var got request
	withMockHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", req.Method)
		}
		if got, want := req.Header.Get("Content-Type"), "application/json"; got != want {
			t.Fatalf("unexpected content type: got %q want %q", got, want)
		}
		if got, want := req.URL.String(), "http://embed.test/v1/embeddings"; got != want {
			t.Fatalf("unexpected URL: got %q want %q", got, want)
		}
		if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return jsonResponse(http.StatusOK, `{"data":[{"embedding":[0.25,0.5],"index":0},{"embedding":[0.75,1.0],"index":1}]}`), nil
	})

	embeddings, err := Call("http://embed.test/v1/embeddings", "Qwen3-Embedding-0.6B", []string{"hello", "world"}, time.Second)
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	if got, want := got.Model, "Qwen3-Embedding-0.6B"; got != want {
		t.Fatalf("unexpected model: got %q want %q", got, want)
	}
	if got, want := got.EncodingFormat, "float"; got != want {
		t.Fatalf("unexpected encoding_format: got %q want %q", got, want)
	}
	if got, want := len(got.Input), 2; got != want {
		t.Fatalf("unexpected input count: got %d want %d", got, want)
	}
	if got, want := len(embeddings), 2; got != want {
		t.Fatalf("unexpected embedding count: got %d want %d", got, want)
	}
	if got, want := len(embeddings[0]), 2; got != want {
		t.Fatalf("unexpected embedding size: got %d want %d", got, want)
	}
	if got, want := embeddings[1][1], float32(1.0); got != want {
		t.Fatalf("unexpected embedding value: got %v want %v", got, want)
	}
}

func TestCallIncludesHTTPErrorBody(t *testing.T) {
	withMockHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Status:     "400 Bad Request",
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString(`{"error":"bad request"}`)),
		}, nil
	})

	_, err := Call("http://embed.test/v1/embeddings", "Qwen3-Embedding-0.6B", []string{"hello"}, time.Second)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "400 Bad Request") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), `body={"error":"bad request"}`) {
		t.Fatalf("unexpected error body: %v", err)
	}
}

func TestCallSingleRejectsEmptyData(t *testing.T) {
	withMockHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"data":[]}`), nil
	})

	_, err := CallSingle("http://embed.test/v1/embeddings", "Qwen3-Embedding-0.6B", "hello", time.Second)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no data") {
		t.Fatalf("unexpected error: %v", err)
	}
}
