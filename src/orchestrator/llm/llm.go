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

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type completionRequest struct {
	Model   string `json:"model"`
	Prompt  string `json:"prompt"`
	Grammar string `json:"grammar,omitempty"`
	Stream  bool   `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type completionResponse struct {
	Choices []struct {
		Text string `json:"text"`
	} `json:"choices"`
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
		Stream: false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	fmt.Printf("llm.request_body=%s\n", string(body))

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

	client := &http.Client{}
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

func CallCompletionWithGrammar(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
	if httpURL == "" {
		return "", fmt.Errorf("LLM_COMPLETIONS_URL missing")
	}
	if model == "" {
		model = "llama-3.2.gguf"
	}

	payload := completionRequest{
		Model:   model,
		Prompt:  prompt,
		Grammar: grammar,
		Stream:  false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	fmt.Printf("llm.request_body=%s\n", string(body))

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

	client := &http.Client{}
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

	var parsed completionResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("llm response: no choices")
	}
	return parsed.Choices[0].Text, nil
}
