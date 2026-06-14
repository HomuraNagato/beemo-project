package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDerivesLlamaCPPDecisionURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`
llm:
  addr: eve-reasoning:5014
  http_url: http://eve-reasoning:5014/v1/chat/completions
  provider: llamacpp
  model: Qwen2.5-1.5B-Instruct.Q4_K_M.gguf
`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("BEEMO_CONFIG", path)

	cfg := Load()
	if got, want := cfg.LLMProvider, "llamacpp"; got != want {
		t.Fatalf("unexpected provider: got %q want %q", got, want)
	}
	if got, want := cfg.LLMDecisionHTTPURL, "http://eve-reasoning:5014/completion"; got != want {
		t.Fatalf("unexpected decision URL: got %q want %q", got, want)
	}
}
