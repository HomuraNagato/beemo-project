package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"
)

type internetSearchArgs struct {
	Query string `json:"query"`
}

type internetSearchRequest struct {
	Query       string
	MaxResults  int
	SearchDepth string
	Chunks      int
}

type internetSearchResponse struct {
	Evidence   []internetEvidence
	RequestID  string
	ProviderMs string
	Credits    int
}

type internetEvidence struct {
	Title   string
	URL     string
	Content string
}

type internetSearchProvider interface {
	Name() string
	Search(context.Context, internetSearchRequest) (internetSearchResponse, error)
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

	provider, err := newInternetSearchProvider(cfg)
	if err != nil {
		return Result{}, err
	}
	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	response, err := provider.Search(callCtx, internetSearchRequest{
		Query:       query,
		MaxResults:  boundedInt(cfg.ResultCount, 8, 1, 20),
		SearchDepth: defaultInternetString(cfg.SearchDepth, "fast"),
		Chunks:      boundedInt(cfg.ChunksPerSource, 2, 1, 3),
	})
	if err != nil {
		return Result{}, err
	}
	result := formatInternetEvidence(req.Action, response.Evidence, boundedInt(cfg.MaxTokens, 6000, 512, 32768))
	sourceTitles := make([]string, 0, len(result.Sources))
	sourceURLs := make([]string, 0, len(result.Sources))
	for _, source := range result.Sources {
		sourceTitles = append(sourceTitles, source.Title)
		sourceURLs = append(sourceURLs, source.URL)
	}
	slog.Info("tool.internet.search",
		"session", req.SessionID,
		"provider", provider.Name(),
		"query", query,
		"ms", time.Since(started).Milliseconds(),
		"provider_ms", response.ProviderMs,
		"credits", response.Credits,
		"sources", len(result.Sources),
		"source_titles", sourceTitles,
		"source_urls", sourceURLs,
		"evidence_chars", len(result.Output),
		"request_id", response.RequestID,
	)
	return result, nil
}

func newInternetSearchProvider(cfg InternetConfig) (internetSearchProvider, error) {
	switch strings.ToLower(defaultInternetString(cfg.Provider, "tavily")) {
	case "tavily":
		if strings.TrimSpace(cfg.APIKey) == "" {
			return nil, fmt.Errorf("TAVILY_API_KEY is not configured")
		}
		return &tavilySearchProvider{apiKey: cfg.APIKey, httpURL: cfg.HTTPURL}, nil
	default:
		return nil, fmt.Errorf("unsupported internet search provider %q", cfg.Provider)
	}
}

func formatInternetEvidence(action string, evidence []internetEvidence, maxTokens int) Result {
	maxChars := maxTokens * 4
	var sections []string
	var sources []Source
	seen := map[string]bool{}
	usedChars := 0
	for _, item := range evidence {
		url := strings.TrimSpace(item.URL)
		content := strings.TrimSpace(item.Content)
		if url == "" || content == "" || seen[url] || usedChars >= maxChars {
			continue
		}
		title := defaultInternetString(item.Title, url)
		header := fmt.Sprintf("[%d] %s\nURL: %s\nEvidence:\n", len(sources)+1, title, url)
		remaining := maxChars - usedChars - utf8.RuneCountInString(header)
		if remaining <= 0 {
			break
		}
		content = truncateRunes(content, remaining)
		seen[url] = true
		sources = append(sources, Source{Title: title, URL: url})
		section := header + content
		sections = append(sections, section)
		usedChars += utf8.RuneCountInString(section) + 2
	}
	return Result{Action: action, Output: strings.Join(sections, "\n\n"), Sources: sources}
}

func truncateRunes(value string, maximum int) string {
	if maximum <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	if maximum <= 3 {
		return string(runes[:maximum])
	}
	return string(runes[:maximum-3]) + "..."
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
