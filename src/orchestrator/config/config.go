package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	LLMAddr      string
	LLMHTTPURL   string
	LLMCompletionsURL string
	LLMModel     string
	LLMTimeoutMs int
	OrchAddr     string
	DecisionGrammarPath string
	HistoryDir  string
	ToolsAddr    string
	VisionAddr   string
	UIAddr       string
	TTSAddr      string
	ASRAddr      string
	WakeWordAddr string
}

func Load() Config {
	llmAddr := os.Getenv("LLM_ADDR")
	llmHTTPURL := os.Getenv("LLM_HTTP_URL")
	if llmHTTPURL == "" && llmAddr != "" {
		llmHTTPURL = "http://" + strings.TrimSpace(llmAddr) + "/v1/chat/completions"
	}
	llmCompletionsURL := os.Getenv("LLM_COMPLETIONS_URL")
	if llmCompletionsURL == "" && llmHTTPURL != "" {
		llmCompletionsURL = strings.Replace(llmHTTPURL, "/v1/chat/completions", "/v1/completions", 1)
	}

	return Config{
		LLMAddr:      llmAddr,
		LLMHTTPURL:   llmHTTPURL,
		LLMCompletionsURL: llmCompletionsURL,
		LLMModel:     os.Getenv("REASONING_MODEL"),
		LLMTimeoutMs: atoiOrDefault(os.Getenv("LLM_TIMEOUT_MS"), 120000),
		OrchAddr:     os.Getenv("ORCH_ADDR"),
		DecisionGrammarPath: getenvOrDefault("DECISION_GRAMMAR_PATH", "requests/grammars/tool_list.gbnf"),
		HistoryDir:  getenvOrDefault("HISTORY_DIR", "memory"),
		ToolsAddr:    os.Getenv("TOOLS_ADDR"),
		VisionAddr:   os.Getenv("VISION_ADDR"),
		UIAddr:       os.Getenv("UI_ADDR"),
		TTSAddr:      os.Getenv("TTS_ADDR"),
		ASRAddr:      os.Getenv("ASR_ADDR"),
		WakeWordAddr: os.Getenv("WAKEWORD_ADDR"),
	}
}

func getenvOrDefault(key, def string) string {
	val := os.Getenv(key)
	if val == "" {
		return def
	}
	return val
}

func atoiOrDefault(v string, def int) int {
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
