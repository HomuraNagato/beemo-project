package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

const maxResponseBytes = 8 << 20

type candidate struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	Text  string `json:"text"`
}

type rerankRequest struct {
	RequestID  string      `json:"request_id,omitempty"`
	Query      string      `json:"query"`
	Candidates []candidate `json:"candidates"`
	TopK       int         `json:"top_k"`
}

type rerankResult struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
	Index int     `json:"index"`
}

type rerankResponse struct {
	Model   string         `json:"model"`
	Results []rerankResult `json:"results"`
}

type vllmRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n"`
}

type vllmResponse struct {
	Model   string `json:"model"`
	Results []struct {
		Index          int     `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
	} `json:"results"`
}

type server struct {
	backendURL string
	model      string
	client     *http.Client
}

func main() {
	port := envOrDefault("RERANKER_PROXY_PORT", "5023")
	s := &server{
		backendURL: strings.TrimRight(envOrDefault("RERANKER_BACKEND_URL", "http://127.0.0.1:8000"), "/"),
		model:      strings.TrimSpace(os.Getenv("RERANKER_MODEL")),
		client:     &http.Client{Timeout: envDuration("RERANKER_PROXY_TIMEOUT", 2*time.Minute)},
	}
	if s.model == "" {
		log.Fatal("RERANKER_MODEL is required")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/rerank", s.handleRerank)
	httpServer := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("reranker proxy listening on :%s backend=%s model=%s", port, s.backendURL, s.model)
	if err := httpServer.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, s.backendURL+"/health", nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	response, err := s.client.Do(request)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("backend returned %s", response.Status))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "model": s.model})
}

func (s *server) handleRerank(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	var input rerankRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxResponseBytes)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request: %w", err))
		return
	}
	input.Query = strings.TrimSpace(input.Query)
	if input.Query == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("query is required"))
		return
	}
	if len(input.Candidates) == 0 {
		writeJSON(w, http.StatusOK, rerankResponse{Model: s.model, Results: []rerankResult{}})
		return
	}
	topK := input.TopK
	if topK <= 0 || topK > len(input.Candidates) {
		topK = len(input.Candidates)
	}
	documents := make([]string, len(input.Candidates))
	for index, item := range input.Candidates {
		documents[index] = strings.TrimSpace(strings.TrimSpace(item.Title) + "\n" + strings.TrimSpace(item.Text))
	}
	payload := vllmRequest{
		Model:     s.model,
		Query:     input.Query,
		Documents: documents,
		TopN:      topK,
	}
	var output vllmResponse
	if err := s.postJSON(r, "/rerank", payload, &output); err != nil {
		log.Printf("reranker.proxy request_id=%q status=error candidates=%d total_ms=%d err=%q", input.RequestID, len(input.Candidates), time.Since(started).Milliseconds(), err)
		writeError(w, http.StatusBadGateway, err)
		return
	}
	results := make([]rerankResult, 0, len(output.Results))
	for _, item := range output.Results {
		if item.Index < 0 || item.Index >= len(input.Candidates) {
			continue
		}
		results = append(results, rerankResult{
			ID:    input.Candidates[item.Index].ID,
			Score: item.RelevanceScore,
			Index: item.Index,
		})
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	log.Printf("reranker.proxy request_id=%q status=ok candidates=%d returned=%d total_ms=%d", input.RequestID, len(input.Candidates), len(results), time.Since(started).Milliseconds())
	writeJSON(w, http.StatusOK, rerankResponse{Model: output.Model, Results: results})
}

func (s *server) postJSON(r *http.Request, path string, input, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.backendURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
		return fmt.Errorf("backend returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(output)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		log.Fatalf("invalid %s: %v", key, err)
	}
	return parsed
}
