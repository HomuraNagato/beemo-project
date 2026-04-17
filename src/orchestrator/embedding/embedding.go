package embedding

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

type request struct {
	Model          string   `json:"model,omitempty"`
	Input          []string `json:"input"`
	EncodingFormat string   `json:"encoding_format,omitempty"`
}

type response struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

func Call(httpURL, model string, inputs []string, timeout time.Duration) ([][]float32, error) {
	if httpURL == "" {
		return nil, fmt.Errorf("EMBEDDING_HTTP_URL missing")
	}
	if len(inputs) == 0 {
		return nil, fmt.Errorf("no embedding inputs provided")
	}

	payload := request{
		Input:          inputs,
		EncodingFormat: "float",
	}
	if strings.TrimSpace(model) != "" {
		payload.Model = model
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	return callEmbeddingRequest(httpURL, body, timeout)
}

func CallSingle(httpURL, model, input string, timeout time.Duration) ([]float32, error) {
	embeddings, err := Call(httpURL, model, []string{input}, timeout)
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("embedding response: no data")
	}
	return embeddings[0], nil
}

func callEmbeddingRequest(httpURL string, body []byte, timeout time.Duration) ([][]float32, error) {
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
			return nil, fmt.Errorf("embedding http status: %s body=%s", resp.Status, msg)
		}
		return nil, fmt.Errorf("embedding http status: %s", resp.Status)
	}

	var parsed response
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if len(parsed.Data) == 0 {
		return nil, fmt.Errorf("embedding response: no data")
	}

	embeddings := make([][]float32, len(parsed.Data))
	for i, item := range parsed.Data {
		embeddings[i] = item.Embedding
	}
	return embeddings, nil
}
