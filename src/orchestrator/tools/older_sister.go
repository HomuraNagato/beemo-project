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

type olderSisterArgs struct {
	Query     string `json:"query,omitempty"`
	WebSearch *bool  `json:"web_search,omitempty"`
}

type olderSisterRequest struct {
	Model       string                 `json:"model"`
	Input       string                 `json:"input"`
	Tools       []map[string]any       `json:"tools,omitempty"`
	ToolChoice  string                 `json:"tool_choice,omitempty"`
	Include     []string               `json:"include,omitempty"`
	Reasoning   map[string]string      `json:"reasoning,omitempty"`
	Temperature *float64               `json:"temperature,omitempty"`
	Text        map[string]interface{} `json:"text,omitempty"`
}

type olderSisterResponse struct {
	OutputText string `json:"output_text,omitempty"`
	Output     []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

var olderSisterHTTPDo = func(req *http.Request) (*http.Response, error) {
	return http.DefaultClient.Do(req)
}

func executeOlderSister(ctx context.Context, req Request, cfg OlderSisterConfig) (Result, error) {
	args, err := parseOlderSisterArgs(req.Args)
	if err != nil {
		return Result{}, err
	}
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return needsInputResult(req.Action, []string{"query"}, "What should I ask older sister?"), nil
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return Result{}, fmt.Errorf("OPENAI_API_KEY missing for older_sister")
	}
	httpURL := strings.TrimSpace(cfg.HTTPURL)
	if httpURL == "" {
		httpURL = "https://api.openai.com/v1/responses"
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = "gpt-5-mini"
	}
	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	payload := olderSisterRequest{
		Model: model,
		Input: olderSisterPrompt(query),
	}
	useSearch := cfg.WebSearch
	if args.WebSearch != nil {
		useSearch = *args.WebSearch
	}
	if useSearch {
		payload.Tools = []map[string]any{{"type": "web_search"}}
		payload.ToolChoice = "auto"
		payload.Include = []string{"web_search_call.action.sources"}
		payload.Reasoning = map[string]string{"effort": "low"}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return Result{}, err
	}
	httpReq, err := http.NewRequestWithContext(callCtx, http.MethodPost, httpURL, bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.APIKey))

	resp, err := olderSisterHTTPDo(httpReq)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return Result{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}, fmt.Errorf("older_sister http status: %s body=%s", resp.Status, strings.TrimSpace(string(respBody)))
	}

	output, err := parseOlderSisterResponse(respBody)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Action: req.Action,
		Output: output,
	}, nil
}

func parseOlderSisterArgs(raw json.RawMessage) (olderSisterArgs, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var args olderSisterArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return olderSisterArgs{}, fmt.Errorf("invalid older_sister args: %w", err)
	}
	args.Query = strings.TrimSpace(args.Query)
	return args, nil
}

func parseOlderSisterResponse(raw []byte) (string, error) {
	var response olderSisterResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return "", fmt.Errorf("invalid older_sister response: %w", err)
	}
	if text := strings.TrimSpace(response.OutputText); text != "" {
		return text, nil
	}
	for _, item := range response.Output {
		if item.Type != "" && item.Type != "message" {
			continue
		}
		for _, content := range item.Content {
			if text := strings.TrimSpace(content.Text); text != "" {
				return text, nil
			}
		}
	}
	return "", fmt.Errorf("older_sister response had no text output")
}

func olderSisterPrompt(query string) string {
	return "You are Older Sister, a careful external advisor for Beemo. Answer the user's request directly. If you use web search, include concise source links or citations in the answer when the information depends on current internet results.\n\nUser request: " + query
}
