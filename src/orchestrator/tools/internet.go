package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const maxInternetResponseBytes = 4 << 20

type internetSearchArgs struct {
	Query string `json:"query"`
}

type braveContextRequest struct {
	Query          string `json:"q"`
	Country        string `json:"country"`
	SearchLanguage string `json:"search_lang"`
	Count          int    `json:"count"`
	MaxURLs        int    `json:"maximum_number_of_urls"`
	MaxTokens      int    `json:"maximum_number_of_tokens"`
	ThresholdMode  string `json:"context_threshold_mode"`
	SafeSearch     string `json:"safesearch"`
	SourceMetadata bool   `json:"enable_source_metadata"`
}

type braveContextResponse struct {
	Grounding struct {
		Generic []struct {
			URL      string   `json:"url"`
			Title    string   `json:"title"`
			Snippets []string `json:"snippets"`
		} `json:"generic"`
	} `json:"grounding"`
}

var internetHTTPDo = func(req *http.Request) (*http.Response, error) {
	return http.DefaultClient.Do(req)
}

func executeInternetSearch(ctx context.Context, req Request, cfg InternetConfig) (Result, error) {
	started := time.Now()
	args := internetSearchArgs{}
	if len(req.Args) > 0 {
		if err := json.Unmarshal(req.Args, &args); err != nil {
			return Result{}, fmt.Errorf("invalid internet.search args: %w", err)
		}
	}
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return needsInputResult(req.Action, []string{"query"}, "What should I search for?"), nil
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return Result{}, fmt.Errorf("BRAVE_SEARCH_API_KEY is not configured")
	}

	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	payload := braveContextRequest{
		Query: query, Country: "US", SearchLanguage: "en",
		Count:         boundedInt(cfg.ResultCount, 20, 1, 50),
		MaxURLs:       boundedInt(cfg.MaxURLs, 8, 1, 50),
		MaxTokens:     boundedInt(cfg.MaxTokens, 6000, 1024, 32768),
		ThresholdMode: "balanced", SafeSearch: defaultInternetString(cfg.SafeSearch, "moderate"),
		SourceMetadata: true,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Result{}, err
	}
	httpURL := defaultInternetString(cfg.HTTPURL, "https://api.search.brave.com/res/v1/llm/context")
	httpReq, err := http.NewRequestWithContext(callCtx, http.MethodPost, httpURL, bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Subscription-Token", strings.TrimSpace(cfg.APIKey))

	resp, err := internetHTTPDo(httpReq)
	if err != nil {
		return Result{}, fmt.Errorf("Brave search request failed: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxInternetResponseBytes+1))
	if err != nil {
		return Result{}, fmt.Errorf("read Brave search response: %w", err)
	}
	if len(responseBody) > maxInternetResponseBytes {
		return Result{}, fmt.Errorf("Brave search response exceeded %d bytes", maxInternetResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}, fmt.Errorf("Brave search returned %s: %s", resp.Status, compactHTTPError(responseBody))
	}

	var response braveContextResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return Result{}, fmt.Errorf("invalid Brave search response: %w", err)
	}
	result := formatInternetEvidence(req.Action, response)
	slog.Info("tool.internet.search", "session", req.SessionID, "ms", time.Since(started).Milliseconds(), "sources", len(result.Sources), "evidence_chars", len(result.Output))
	return result, nil
}

func formatInternetEvidence(action string, response braveContextResponse) Result {
	var sections []string
	var sources []Source
	seen := map[string]bool{}
	for _, item := range response.Grounding.Generic {
		url := strings.TrimSpace(item.URL)
		if url == "" || seen[url] {
			continue
		}
		var snippets []string
		for _, snippet := range item.Snippets {
			if value := strings.TrimSpace(snippet); value != "" {
				snippets = append(snippets, value)
			}
		}
		if len(snippets) == 0 {
			continue
		}
		seen[url] = true
		title := defaultInternetString(item.Title, url)
		sources = append(sources, Source{Title: title, URL: url})
		sections = append(sections, fmt.Sprintf("[%d] %s\nURL: %s\nEvidence:\n%s", len(sources), title, url, strings.Join(snippets, "\n")))
	}
	return Result{Action: action, Output: strings.Join(sections, "\n\n"), Sources: sources}
}

func boundedInt(value, fallback, minimum, maximum int) int {
	if value == 0 {
		value = fallback
	}
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func defaultInternetString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func compactHTTPError(body []byte) string {
	value := strings.TrimSpace(string(body))
	if len(value) > 500 {
		return value[:500] + "..."
	}
	return value
}
