package llm

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

func CallOnce(httpURL, model, prompt string, timeout time.Duration) (string, error) {
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
		Temperature: 0,
		Stream:      false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	fmt.Printf(
		"llm.chat_request model=%s temp=%.2f prompt_chars=%d prompt_preview=%q\n",
		model,
		payload.Temperature,
		len(prompt),
		logPreview(prompt),
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
		MaxTokens:   128,
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
	fmt.Printf(
		"llm.chat_request model=%s max_tokens=%d temp=%.2f grammar=%t prompt_chars=%d prompt_preview=%q\n",
		model,
		payload.MaxTokens,
		payload.Temperature,
		payload.StructuredOutputs != nil,
		len(prompt),
		logPreview(prompt),
	)

	return callChatRequest(httpURL, body, timeout)
}

func callChatRequest(httpURL string, body []byte, timeout time.Duration) (string, error) {
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, httpURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := newHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		msg := strings.TrimSpace(string(body))
		if msg != "" {
			return "", fmt.Errorf("llm http status: %s body=%s", resp.Status, msg)
		}
		return "", fmt.Errorf("llm http status: %s", resp.Status)
	}

	var parsed chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("llm response: no choices")
	}
	return parsed.Choices[0].Message.Content, nil
}

func logPreview(text string) string {
	normalized := strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	const limit = 96
	if len(normalized) <= limit {
		return normalized
	}
	return normalized[:limit] + "..."
}
