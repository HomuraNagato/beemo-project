package llm

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

var newHTTPClient = func() *http.Client {
	return &http.Client{}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model             string                 `json:"model"`
	Messages          []chatMessage          `json:"messages"`
	MaxTokens         int                    `json:"max_tokens,omitempty"`
	Temperature       float64                `json:"temperature,omitempty"`
	StructuredOutputs *structuredOutputsSpec `json:"structured_outputs,omitempty"`
	Stream            bool                   `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type structuredOutputsSpec struct {
	Grammar string `json:"grammar,omitempty"`
}

type llamaCPPCompletionRequest struct {
	Prompt      string  `json:"prompt"`
	Grammar     string  `json:"grammar,omitempty"`
	NPredict    int     `json:"n_predict,omitempty"`
	Temperature float64 `json:"temperature"`
	Stream      bool    `json:"stream"`
}

type llamaCPPCompletionResponse struct {
	Content string `json:"content"`
	Choices []struct {
		Text    string `json:"text"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func CallOnce(httpURL, model, prompt string, timeout time.Duration) (string, error) {
	return CallOnceWithMaxTokens(httpURL, model, prompt, 384, timeout)
}

func CallOnceWithMaxTokens(httpURL, model, prompt string, maxTokens int, timeout time.Duration) (string, error) {
	if httpURL == "" {
		return "", fmt.Errorf("LLM_HTTP_URL missing")
	}
	if model == "" {
		model = "llama-3.2.gguf"
	}
	if maxTokens <= 0 {
		maxTokens = 384
	}

	payload := chatRequest{
		Model: model,
		Messages: []chatMessage{
			{Role: "user", Content: prompt},
		},
		MaxTokens:   maxTokens,
		Temperature: 0,
		Stream:      false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	slog.Info("llm.chat_request",
		"model", model,
		"max_tokens", payload.MaxTokens,
		"temperature", payload.Temperature,
		"prompt_chars", len(prompt),
		"prompt_preview", logPreview(prompt),
	)

	return callChatRequest(httpURL, body, timeout)
}

func CallChatWithGrammar(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
	if httpURL == "" {
		return "", fmt.Errorf("LLM_HTTP_URL missing")
	}
	if model == "" {
		model = "llama-3.2.gguf"
	}

	payload := chatRequest{
		Model: model,
		Messages: []chatMessage{
			{Role: "user", Content: prompt},
		},
		MaxTokens:   256,
		Temperature: 0,
		Stream:      false,
	}
	if strings.TrimSpace(grammar) != "" {
		payload.StructuredOutputs = &structuredOutputsSpec{
			Grammar: grammar,
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	slog.Info("llm.chat_request",
		"model", model,
		"max_tokens", payload.MaxTokens,
		"temperature", payload.Temperature,
		"grammar", payload.StructuredOutputs != nil,
		"prompt_chars", len(prompt),
		"prompt_preview", logPreview(prompt),
	)

	return callChatRequest(httpURL, body, timeout)
}

func CallDecisionWithGrammar(provider, httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", "vllm":
		return CallChatWithGrammar(httpURL, model, prompt, grammar, timeout)
	case "llamacpp", "llama.cpp", "llama-cpp":
		return CallLlamaCPPWithGrammar(httpURL, prompt, grammar, timeout)
	default:
		return "", fmt.Errorf("unsupported llm provider %q", provider)
	}
}

func CallLlamaCPPWithGrammar(httpURL, prompt, grammar string, timeout time.Duration) (string, error) {
	if httpURL == "" {
		return "", fmt.Errorf("LLM decision HTTP URL missing")
	}

	payload := llamaCPPCompletionRequest{
		Prompt:      prompt,
		NPredict:    256,
		Temperature: 0,
		Stream:      false,
	}
	if strings.TrimSpace(grammar) != "" {
		payload.Grammar = normalizeLlamaCPPGrammar(grammar)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	slog.Info("llm.decision_request",
		"provider", "llamacpp",
		"max_tokens", payload.NPredict,
		"temperature", payload.Temperature,
		"grammar", strings.TrimSpace(payload.Grammar) != "",
		"prompt_chars", len(prompt),
		"prompt_preview", logPreview(prompt),
	)

	respBody, err := callRawRequest(httpURL, body, timeout)
	if err != nil {
		return "", err
	}
	var parsed llamaCPPCompletionResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", err
	}
	if strings.TrimSpace(parsed.Content) != "" {
		return parsed.Content, nil
	}
	if len(parsed.Choices) > 0 {
		if strings.TrimSpace(parsed.Choices[0].Text) != "" {
			return parsed.Choices[0].Text, nil
		}
		if strings.TrimSpace(parsed.Choices[0].Message.Content) != "" {
			return parsed.Choices[0].Message.Content, nil
		}
	}
	return "", fmt.Errorf("llamacpp response missing content")
}

func normalizeLlamaCPPGrammar(grammar string) string {
	var out strings.Builder
	out.Grow(len(grammar))
	inString := false
	inCharClass := false
	escaped := false

	for _, r := range grammar {
		switch {
		case inString:
			out.WriteRune(r)
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
			} else if r == '"' {
				inString = false
			}
		case inCharClass:
			out.WriteRune(r)
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
			} else if r == ']' {
				inCharClass = false
			}
		default:
			switch r {
			case '"':
				inString = true
				out.WriteRune(r)
			case '[':
				inCharClass = true
				out.WriteRune(r)
			case '_':
				out.WriteRune('-')
			default:
				out.WriteRune(r)
			}
		}
	}
	return out.String()
}

func callChatRequest(httpURL string, body []byte, timeout time.Duration) (string, error) {
	respBody, err := callRawRequest(httpURL, body, timeout)
	if err != nil {
		return "", err
	}
	var parsed chatResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("llm response: no choices")
	}
	return parsed.Choices[0].Message.Content, nil
}

func callRawRequest(httpURL string, body []byte, timeout time.Duration) ([]byte, error) {
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, httpURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := newHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		msg := strings.TrimSpace(string(body))
		if msg != "" {
			return nil, fmt.Errorf("llm http status: %s body=%s", resp.Status, msg)
		}
		return nil, fmt.Errorf("llm http status: %s", resp.Status)
	}

	return io.ReadAll(resp.Body)
}

func logPreview(text string) string {
	normalized := strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	const limit = 96
	if len(normalized) <= limit {
		return normalized
	}
	return normalized[:limit] + "..."
}
