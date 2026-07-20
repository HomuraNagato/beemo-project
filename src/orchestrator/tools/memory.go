package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type memorySearchArgs struct {
	Query      string `json:"query,omitempty"`
	Kind       string `json:"kind,omitempty"`
	SourceType string `json:"source_type,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

type MemoryRetrieveRequest struct {
	RequestID           string `json:"request_id,omitempty"`
	Query               string `json:"query,omitempty"`
	Mode                string `json:"mode,omitempty"`
	TimeFilter          string `json:"time_filter,omitempty"`
	Scope               string `json:"scope,omitempty"`
	Collection          string `json:"collection,omitempty"`
	Limit               int    `json:"limit,omitempty"`
	CandidateLimit      int    `json:"candidate_limit,omitempty"`
	ChunkLimitPerSource int    `json:"chunk_limit_per_source,omitempty"`
}

type MemoryRetrieveResponse struct {
	Query       string                    `json:"query"`
	Sources     []MemoryRetrieveSource    `json:"sources"`
	Diagnostics MemoryRetrieveDiagnostics `json:"diagnostics"`
}

type MemoryRetrieveSource struct {
	ID             string   `json:"id"`
	MemoryID       string   `json:"memory_id"`
	Title          string   `json:"title"`
	Text           string   `json:"text"`
	EvidenceText   string   `json:"evidence_text,omitempty"`
	Kind           string   `json:"kind"`
	SourceType     string   `json:"source_type"`
	SourceURI      string   `json:"source_uri"`
	UpdatedAt      string   `json:"updated_at"`
	RetrievalScore float64  `json:"retrieval_score"`
	RerankScore    *float64 `json:"rerank_score,omitempty"`
}

type MemoryRetrieveDiagnostics struct {
	Status               string `json:"status"`
	Reason               string `json:"reason,omitempty"`
	CandidateCount       int    `json:"candidate_count"`
	RerankCandidateCount int    `json:"rerank_candidate_count"`
	SelectedCount        int    `json:"selected_count"`
	QueryCount           int    `json:"query_count"`
	SearchMs             int64  `json:"search_ms,omitempty"`
	RerankMs             int64  `json:"rerank_ms,omitempty"`
	TotalMs              int64  `json:"total_ms,omitempty"`
	Reranker             string `json:"reranker,omitempty"`
	RerankerModel        string `json:"reranker_model,omitempty"`
	Error                string `json:"error,omitempty"`
}

type memoryRememberArgs struct {
	Text       string         `json:"text,omitempty"`
	Title      string         `json:"title,omitempty"`
	Kind       string         `json:"kind,omitempty"`
	SourceType string         `json:"source_type,omitempty"`
	SourceURI  string         `json:"source_uri,omitempty"`
	Tags       []string       `json:"tags,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type memorySearchResponse struct {
	Results []memorySearchResult `json:"results"`
}

type memorySearchResult struct {
	MemoryID   string   `json:"memory_id"`
	Title      string   `json:"title"`
	Text       string   `json:"text"`
	Kind       string   `json:"kind"`
	SourceType string   `json:"source_type"`
	SourceURI  string   `json:"source_uri"`
	Tags       []string `json:"tags"`
	Similarity float64  `json:"similarity"`
}

type memoryItemResponse struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

func executeMemorySearch(ctx context.Context, req Request, cfg MemoryConfig) (Result, error) {
	args, err := parseMemorySearchArgs(req.Args)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(args.Query) == "" {
		return needsInputResult(req.Action, []string{"query"}, "What should I look up in memory?"), nil
	}
	payload := map[string]any{
		"query":       args.Query,
		"kind":        args.Kind,
		"source_type": args.SourceType,
		"limit":       limitOrDefault(args.Limit, 5),
	}
	var response memorySearchResponse
	if err := callMemory(ctx, cfg, http.MethodPost, "/v1/search", payload, &response); err != nil {
		return Result{}, err
	}
	return Result{
		Action: req.Action,
		Output: formatMemorySearchResults(response.Results),
	}, nil
}

func RetrieveMemory(ctx context.Context, cfg MemoryConfig, req MemoryRetrieveRequest) (MemoryRetrieveResponse, error) {
	req.Query = strings.TrimSpace(req.Query)
	req.RequestID = strings.TrimSpace(req.RequestID)
	req.Mode = strings.TrimSpace(req.Mode)
	req.TimeFilter = strings.TrimSpace(req.TimeFilter)
	req.Scope = strings.TrimSpace(req.Scope)
	req.Collection = strings.TrimSpace(req.Collection)
	if req.Query == "" {
		return MemoryRetrieveResponse{}, fmt.Errorf("query is required")
	}
	if req.Limit <= 0 {
		req.Limit = 12
	}
	if req.CandidateLimit <= 0 {
		req.CandidateLimit = 30
	}
	var response MemoryRetrieveResponse
	if err := callMemory(ctx, cfg, http.MethodPost, "/v1/retrieve", req, &response); err != nil {
		return MemoryRetrieveResponse{}, err
	}
	return response, nil
}

func executeMemoryRemember(ctx context.Context, req Request, cfg MemoryConfig) (Result, error) {
	args, err := parseMemoryRememberArgs(req.Args)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(args.Text) == "" {
		return needsInputResult(req.Action, []string{"text"}, "What should I remember?"), nil
	}
	payload := map[string]any{
		"kind":        firstNonBlank(args.Kind, "fact"),
		"source_type": firstNonBlank(args.SourceType, "beemo"),
		"source_uri":  args.SourceURI,
		"title":       args.Title,
		"body":        args.Text,
		"tags":        args.Tags,
		"metadata":    args.Metadata,
	}
	var response memoryItemResponse
	if err := callMemory(ctx, cfg, http.MethodPost, "/v1/memories", payload, &response); err != nil {
		return Result{}, err
	}
	title := strings.TrimSpace(response.Title)
	if title == "" {
		title = "memory"
	}
	return Result{
		Action: req.Action,
		Output: "Remembered: " + title,
	}, nil
}

func parseMemorySearchArgs(raw json.RawMessage) (memorySearchArgs, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var args memorySearchArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return memorySearchArgs{}, fmt.Errorf("invalid memory.search args: %w", err)
	}
	args.Query = strings.TrimSpace(args.Query)
	args.Kind = strings.TrimSpace(args.Kind)
	args.SourceType = strings.TrimSpace(args.SourceType)
	return args, nil
}

func parseMemoryRememberArgs(raw json.RawMessage) (memoryRememberArgs, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var args memoryRememberArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return memoryRememberArgs{}, fmt.Errorf("invalid memory.remember args: %w", err)
	}
	args.Text = strings.TrimSpace(args.Text)
	args.Title = strings.TrimSpace(args.Title)
	args.Kind = strings.TrimSpace(args.Kind)
	args.SourceType = strings.TrimSpace(args.SourceType)
	args.SourceURI = strings.TrimSpace(args.SourceURI)
	return args, nil
}

func callMemory(ctx context.Context, cfg MemoryConfig, method, path string, payload any, out any) error {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return fmt.Errorf("memory palace base URL missing")
	}
	userKey := strings.TrimSpace(cfg.UserKey)
	if userKey == "" {
		return fmt.Errorf("memory palace user key missing")
	}
	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(callCtx, method, baseURL+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Memory-User-Key", userKey)

	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("memory palace status %s: %s", response.Status, strings.TrimSpace(string(responseBody)))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(responseBody, out); err != nil {
		return fmt.Errorf("invalid memory palace response: %w", err)
	}
	return nil
}

func formatMemorySearchResults(results []memorySearchResult) string {
	if len(results) == 0 {
		return "No relevant memories found."
	}
	lines := make([]string, 0, len(results))
	for i, result := range results {
		title := strings.TrimSpace(result.Title)
		if title == "" {
			title = "Untitled memory"
		}
		text := strings.Join(strings.Fields(result.Text), " ")
		if len([]rune(text)) > 420 {
			text = string([]rune(text)[:420]) + "..."
		}
		lines = append(lines, fmt.Sprintf("%d. %s [%s/%s similarity=%.3f]\n%s", i+1, title, result.Kind, result.SourceType, result.Similarity, text))
	}
	return strings.Join(lines, "\n\n")
}

func limitOrDefault(value int, fallback int) int {
	if value <= 0 {
		return fallback
	}
	if value > 20 {
		return 20
	}
	return value
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
