package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRerankMapsVLLMIndexesToCandidateIDs(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request vllmRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if len(request.Documents) != 2 || request.Documents[0] != "First\nAlpha" {
			t.Fatalf("unexpected documents: %#v", request.Documents)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"model": "test-model",
			"results": []map[string]any{
				{"index": 1, "relevance_score": 0.9},
				{"index": 0, "relevance_score": 0.2},
			},
		})
	}))
	defer backend.Close()

	s := &server{
		backendURL: backend.URL,
		model:      "test-model",
		client:     &http.Client{Timeout: time.Second},
	}
	request := httptest.NewRequest(http.MethodPost, "/rerank", strings.NewReader(`{
		"query":"question",
		"candidates":[
			{"id":"a","title":"First","text":"Alpha"},
			{"id":"b","title":"Second","text":"Beta"}
		],
		"top_k":2
	}`))
	response := httptest.NewRecorder()
	s.handleRerank(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var output rerankResponse
	if err := json.NewDecoder(response.Body).Decode(&output); err != nil {
		t.Fatal(err)
	}
	if len(output.Results) != 2 || output.Results[0].ID != "b" || output.Results[1].ID != "a" {
		t.Fatalf("unexpected results: %#v", output.Results)
	}
}
