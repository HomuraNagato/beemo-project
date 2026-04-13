package llm

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

func TestCallOnceSendsExpectedChatRequest(t *testing.T) {
	var got chatRequest
	withMockHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", req.Method)
		}
		if got, want := req.Header.Get("Content-Type"), "application/json"; got != want {
			t.Fatalf("unexpected content type: got %q want %q", got, want)
		}
		if got, want := req.URL.String(), "http://llm.test/v1/chat/completions"; got != want {
			t.Fatalf("unexpected URL: got %q want %q", got, want)
		}
		if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return jsonResponse(http.StatusOK, `{"choices":[{"message":{"content":"final answer"}}]}`), nil
	})

	text, err := CallOnce("http://llm.test/v1/chat/completions", "Qwen2.5-1.5B-Instruct", "hello there", time.Second)
	if err != nil {
		t.Fatalf("CallOnce returned error: %v", err)
	}
	if got, want := text, "final answer"; got != want {
		t.Fatalf("unexpected response text: got %q want %q", got, want)
	}
	if got, want := got.Model, "Qwen2.5-1.5B-Instruct"; got != want {
		t.Fatalf("unexpected model: got %q want %q", got, want)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("unexpected messages: %+v", got.Messages)
	}
	if got, want := got.Messages[0].Role, "user"; got != want {
		t.Fatalf("unexpected role: got %q want %q", got, want)
	}
	if got, want := got.Messages[0].Content, "hello there"; got != want {
		t.Fatalf("unexpected content: got %q want %q", got, want)
	}
	if got, want := got.Temperature, 0.0; got != want {
		t.Fatalf("unexpected temperature: got %v want %v", got, want)
	}
	if got.Stream {
		t.Fatal("expected stream to be false")
	}
}

func TestCallChatWithGrammarSendsStructuredOutputs(t *testing.T) {
	var got chatRequest
	withMockHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", req.Method)
		}
		if got, want := req.URL.String(), "http://llm.test/v1/chat/completions"; got != want {
			t.Fatalf("unexpected URL: got %q want %q", got, want)
		}
		if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return jsonResponse(http.StatusOK, `{"choices":[{"message":{"content":"[{\"tool\":\"get_time\",\"args\":{}}]"}}]}`), nil
	})

	text, err := CallChatWithGrammar("http://llm.test/v1/chat/completions", "Qwen2.5-1.5B-Instruct", "decide", `root ::= "[]"`, time.Second)
	if err != nil {
		t.Fatalf("CallChatWithGrammar returned error: %v", err)
	}
	if got, want := text, `[{"tool":"get_time","args":{}}]`; got != want {
		t.Fatalf("unexpected response text: got %q want %q", got, want)
	}
	if got, want := got.Model, "Qwen2.5-1.5B-Instruct"; got != want {
		t.Fatalf("unexpected model: got %q want %q", got, want)
	}
	if got, want := got.Messages[0].Content, "decide"; got != want {
		t.Fatalf("unexpected prompt: got %q want %q", got, want)
	}
	if got, want := got.MaxTokens, 128; got != want {
		t.Fatalf("unexpected max_tokens: got %d want %d", got, want)
	}
	if got.StructuredOutputs == nil {
		t.Fatal("expected structured_outputs to be set")
	}
	if got, want := got.StructuredOutputs.Grammar, `root ::= "[]"`; got != want {
		t.Fatalf("unexpected grammar: got %q want %q", got, want)
	}
}

func TestCallChatWithGrammarOmitsStructuredOutputsWhenGrammarBlank(t *testing.T) {
	var got chatRequest
	withMockHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return jsonResponse(http.StatusOK, `{"choices":[{"message":{"content":"[]"}}]}`), nil
	})

	_, err := CallChatWithGrammar("http://llm.test/v1/chat/completions", "Qwen2.5-1.5B-Instruct", "decide", "   ", time.Second)
	if err != nil {
		t.Fatalf("CallChatWithGrammar returned error: %v", err)
	}
	if got.StructuredOutputs != nil {
		t.Fatalf("expected structured_outputs to be omitted, got %+v", got.StructuredOutputs)
	}
}

func TestCallChatWithGrammarIncludesHTTPErrorBody(t *testing.T) {
	withMockHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Status:     "400 Bad Request",
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString(`{"error":"bad request"}`)),
		}, nil
	})

	_, err := CallChatWithGrammar("http://llm.test/v1/chat/completions", "Qwen2.5-1.5B-Instruct", "decide", `root ::= "[]"`, time.Second)
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
