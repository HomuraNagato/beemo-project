package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const maxInternetResponseBytes = 4 << 20

type tavilySearchProvider struct {
	apiKey  string
	httpURL string
}

type tavilySearchRequest struct {
	Query             string `json:"query"`
	SearchDepth       string `json:"search_depth"`
	ChunksPerSource   int    `json:"chunks_per_source,omitempty"`
	MaxResults        int    `json:"max_results"`
	IncludeAnswer     bool   `json:"include_answer"`
	IncludeRawContent bool   `json:"include_raw_content"`
	IncludeImages     bool   `json:"include_images"`
}

type tavilySearchResponse struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	} `json:"results"`
	ResponseTime json.RawMessage `json:"response_time"`
	RequestID    string          `json:"request_id"`
	Usage        struct {
		Credits int `json:"credits"`
	} `json:"usage"`
}

var internetHTTPDo = func(req *http.Request) (*http.Response, error) {
	return http.DefaultClient.Do(req)
}

func (p *tavilySearchProvider) Name() string {
	return "tavily"
}

func (p *tavilySearchProvider) Search(ctx context.Context, request internetSearchRequest) (internetSearchResponse, error) {
	depth := normalizeTavilySearchDepth(request.SearchDepth)
	payload := tavilySearchRequest{
		Query: request.Query, SearchDepth: depth, MaxResults: request.MaxResults,
		IncludeAnswer: false, IncludeRawContent: false, IncludeImages: false,
	}
	if depth == "fast" || depth == "advanced" {
		payload.ChunksPerSource = request.Chunks
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return internetSearchResponse{}, err
	}
	httpURL := defaultInternetString(p.httpURL, "https://api.tavily.com/search")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, httpURL, bytes.NewReader(body))
	if err != nil {
		return internetSearchResponse{}, err
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(p.apiKey))

	resp, err := internetHTTPDo(httpReq)
	if err != nil {
		return internetSearchResponse{}, fmt.Errorf("Tavily search request failed: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxInternetResponseBytes+1))
	if err != nil {
		return internetSearchResponse{}, fmt.Errorf("read Tavily search response: %w", err)
	}
	if len(responseBody) > maxInternetResponseBytes {
		return internetSearchResponse{}, fmt.Errorf("Tavily search response exceeded %d bytes", maxInternetResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return internetSearchResponse{}, fmt.Errorf("Tavily search returned %s: %s", resp.Status, compactHTTPError(responseBody))
	}

	var response tavilySearchResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return internetSearchResponse{}, fmt.Errorf("invalid Tavily search response: %w", err)
	}
	result := internetSearchResponse{
		RequestID: response.RequestID, ProviderMs: jsonScalarText(response.ResponseTime), Credits: response.Usage.Credits,
		Evidence: make([]internetEvidence, 0, len(response.Results)),
	}
	for _, item := range response.Results {
		result.Evidence = append(result.Evidence, internetEvidence{Title: item.Title, URL: item.URL, Content: item.Content})
	}
	return result, nil
}

func jsonScalarText(value json.RawMessage) string {
	trimmed := strings.TrimSpace(string(value))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	if strings.HasPrefix(trimmed, "\"") {
		var decoded string
		if err := json.Unmarshal(value, &decoded); err == nil {
			return decoded
		}
	}
	return trimmed
}

func normalizeTavilySearchDepth(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ultra-fast", "fast", "basic", "advanced":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "fast"
	}
}

func compactHTTPError(body []byte) string {
	value := strings.TrimSpace(string(body))
	if len(value) > 500 {
		return value[:500] + "..."
	}
	return value
}
