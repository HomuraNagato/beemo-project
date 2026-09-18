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

func TestLoadConfiguresLLMContextLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("llm:\n  context_tokens: 16384\n"), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("BEEMO_CONFIG", path)

	if got, want := Load().LLMContextTokens, 16384; got != want {
		t.Fatalf("unexpected context limit: got %d want %d", got, want)
	}
}

func TestLoadConfiguresAgentProfilesAndCoreContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`
agent:
  normal_max_rounds: 5
  research_max_rounds: 9
memory:
  core_context_max_chars: 4096
`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("BEEMO_CONFIG", path)

	cfg := Load()
	if cfg.NormalMaxRounds != 5 || cfg.ResearchMaxRounds != 9 || cfg.MemoryCoreContextMaxChars != 4096 {
		t.Fatalf("unexpected profile config: %+v", cfg)
	}
}

func TestLoadConfiguresTavilyInternetProviderFromEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`
internet:
  provider: tavily
  search_depth: advanced
  chunks_per_source: 3
`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("BEEMO_CONFIG", path)
	t.Setenv("TAVILY_API_KEY", "tavily-key")

	cfg := Load()
	if got, want := cfg.InternetProvider, "tavily"; got != want {
		t.Fatalf("unexpected provider: got %q want %q", got, want)
	}
	if got, want := cfg.InternetAPIKey, "tavily-key"; got != want {
		t.Fatalf("unexpected API key: got %q want %q", got, want)
	}
	if cfg.InternetSearchDepth != "advanced" || cfg.InternetChunksPerSource != 3 {
		t.Fatalf("unexpected Tavily settings: depth=%q chunks=%d", cfg.InternetSearchDepth, cfg.InternetChunksPerSource)
	}
}
