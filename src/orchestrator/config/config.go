package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	LLMAddr                    string
	LLMHTTPURL                 string
	LLMDecisionHTTPURL         string
	LLMProvider                string
	LLMModel                   string
	LLMTimeoutMs               int
	DatabaseURL                string
	DBMigrationsDir            string
	EmbeddingAddr              string
	EmbeddingHTTPURL           string
	EmbeddingModel             string
	EmbeddingTimeoutMs         int
	RoutesPath                 string
	RouteTopK                  int
	RouteDomainTopK            int
	DeterministicToolShortcuts bool
	OrchAddr                   string
	DecisionGrammarPath        string
	HistoryDir                 string
	DirectToolResponses        bool
	VisionAddr                 string
	UIAddr                     string
	TTSAddr                    string
	ASRAddr                    string
	WakeWordAddr               string
	WeatherLatitude            string
	WeatherLongitude           string
	WeatherTimezone            string
	WeatherLocationName        string
	WeatherHTTPURL             string
	WeatherGeocodingURL        string
	WeatherTemperatureUnit     string
	WeatherWindSpeedUnit       string
	WeatherPrecipitationUnit   string
	OlderSisterAPIKey          string
	OlderSisterHTTPURL         string
	OlderSisterModel           string
	OlderSisterTimeoutMs       int
	OlderSisterWebSearch       bool
	MemoryBaseURL              string
	MemoryUserKey              string
	MemoryTimeoutMs            int
	MemoryAutoSave             bool
}

func Load() Config {
	cfg := defaultConfig()
	if loaded, err := loadYAML(getenvOrDefault("BEEMO_CONFIG", "config/config.yaml")); err == nil {
		cfg = loaded
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "beemo config load warning: %v\n", err)
	}

	// Secrets stay out of checked-in config.
	if apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); apiKey != "" {
		cfg.OlderSisterAPIKey = apiKey
	}
	setString(&cfg.MemoryBaseURL, os.Getenv("MEMORY_PALACE_BASE_URL"))
	setString(&cfg.MemoryUserKey, os.Getenv("MEMORY_PALACE_USER_KEY"))

	if cfg.LLMHTTPURL == "" && cfg.LLMAddr != "" {
		cfg.LLMHTTPURL = "http://" + strings.TrimSpace(cfg.LLMAddr) + "/v1/chat/completions"
	}
	if cfg.LLMDecisionHTTPURL == "" {
		cfg.LLMDecisionHTTPURL = defaultDecisionHTTPURL(cfg.LLMProvider, cfg.LLMAddr, cfg.LLMHTTPURL)
	}
	if cfg.EmbeddingHTTPURL == "" && cfg.EmbeddingAddr != "" {
		cfg.EmbeddingHTTPURL = "http://" + strings.TrimSpace(cfg.EmbeddingAddr) + "/v1/embeddings"
	}
	return cfg
}

func defaultConfig() Config {
	llmAddr := "eve-reasoning:5014"
	llmHTTPURL := ""
	if llmAddr != "" {
		llmHTTPURL = "http://" + strings.TrimSpace(llmAddr) + "/v1/chat/completions"
	}
	embeddingAddr := "eve-embedding:5021"
	embeddingHTTPURL := ""
	if embeddingAddr != "" {
		embeddingHTTPURL = "http://" + strings.TrimSpace(embeddingAddr) + "/v1/embeddings"
	}

	return Config{
		LLMAddr:                    llmAddr,
		LLMHTTPURL:                 llmHTTPURL,
		LLMDecisionHTTPURL:         "",
		LLMProvider:                "vllm",
		LLMModel:                   "Qwen3-1.7B",
		LLMTimeoutMs:               120000,
		DatabaseURL:                "",
		DBMigrationsDir:            "db/migrations",
		EmbeddingAddr:              embeddingAddr,
		EmbeddingHTTPURL:           embeddingHTTPURL,
		EmbeddingModel:             "Qwen3-Embedding-0.6B",
		EmbeddingTimeoutMs:         30000,
		RoutesPath:                 "routes.yaml",
		RouteTopK:                  5,
		RouteDomainTopK:            5,
		DeterministicToolShortcuts: false,
		DecisionGrammarPath:        "scripts/grammars/tool_list.gbnf",
		HistoryDir:                 "history",
		DirectToolResponses:        false,
		VisionAddr:                 "eve-vision:5016",
		UIAddr:                     "eve-ui:5017",
		TTSAddr:                    "eve-tts:5018",
		ASRAddr:                    "eve-asr:5019",
		WakeWordAddr:               "eve-wakeword:5020",
		WeatherTimezone:            "auto",
		WeatherHTTPURL:             "https://api.open-meteo.com/v1/forecast",
		WeatherGeocodingURL:        "https://geocoding-api.open-meteo.com/v1/search",
		WeatherTemperatureUnit:     "fahrenheit",
		WeatherWindSpeedUnit:       "mph",
		WeatherPrecipitationUnit:   "inch",
		OlderSisterHTTPURL:         "https://api.openai.com/v1/responses",
		OlderSisterModel:           "gpt-5-mini",
		OlderSisterTimeoutMs:       120000,
		OlderSisterWebSearch:       true,
		MemoryBaseURL:              "http://host.docker.internal:8013",
		MemoryUserKey:              "local",
		MemoryTimeoutMs:            30000,
		MemoryAutoSave:             true,
	}
}

type yamlConfig struct {
	LLM struct {
		Addr            string `yaml:"addr"`
		HTTPURL         string `yaml:"http_url"`
		DecisionHTTPURL string `yaml:"decision_http_url"`
		Provider        string `yaml:"provider"`
		Model           string `yaml:"model"`
		TimeoutMs       int    `yaml:"timeout_ms"`
	} `yaml:"llm"`
	Embedding struct {
		Addr      string `yaml:"addr"`
		HTTPURL   string `yaml:"http_url"`
		Model     string `yaml:"model"`
		TimeoutMs int    `yaml:"timeout_ms"`
	} `yaml:"embedding"`
	Database struct {
		URL           string `yaml:"url"`
		MigrationsDir string `yaml:"migrations_dir"`
	} `yaml:"database"`
	Routing struct {
		RoutesPath                 string `yaml:"routes_path"`
		TopK                       int    `yaml:"top_k"`
		DomainTopK                 int    `yaml:"domain_top_k"`
		DeterministicToolShortcuts *bool  `yaml:"deterministic_tool_shortcuts"`
	} `yaml:"routing"`
	Orchestrator struct {
		Addr                string `yaml:"addr"`
		DecisionGrammarPath string `yaml:"decision_grammar_path"`
		HistoryDir          string `yaml:"history_dir"`
		DirectToolResponses *bool  `yaml:"direct_tool_responses"`
	} `yaml:"orchestrator"`
	Services struct {
		VisionAddr   string `yaml:"vision_addr"`
		UIAddr       string `yaml:"ui_addr"`
		TTSAddr      string `yaml:"tts_addr"`
		ASRAddr      string `yaml:"asr_addr"`
		WakeWordAddr string `yaml:"wakeword_addr"`
	} `yaml:"services"`
	Weather struct {
		Latitude          string `yaml:"latitude"`
		Longitude         string `yaml:"longitude"`
		Timezone          string `yaml:"timezone"`
		LocationName      string `yaml:"location_name"`
		HTTPURL           string `yaml:"http_url"`
		GeocodingURL      string `yaml:"geocoding_url"`
		TemperatureUnit   string `yaml:"temperature_unit"`
		WindSpeedUnit     string `yaml:"wind_speed_unit"`
		PrecipitationUnit string `yaml:"precipitation_unit"`
	} `yaml:"weather"`
	OlderSister struct {
		HTTPURL   string `yaml:"http_url"`
		Model     string `yaml:"model"`
		TimeoutMs int    `yaml:"timeout_ms"`
		WebSearch *bool  `yaml:"web_search"`
	} `yaml:"older_sister"`
	Memory struct {
		BaseURL   string `yaml:"base_url"`
		UserKey   string `yaml:"user_key"`
		TimeoutMs int    `yaml:"timeout_ms"`
		AutoSave  *bool  `yaml:"auto_save"`
	} `yaml:"memory"`
}

func loadYAML(path string) (Config, error) {
	cfg := defaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	raw := yamlConfig{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Config{}, err
	}

	setString(&cfg.LLMAddr, raw.LLM.Addr)
	setString(&cfg.LLMHTTPURL, raw.LLM.HTTPURL)
	setString(&cfg.LLMDecisionHTTPURL, raw.LLM.DecisionHTTPURL)
	setString(&cfg.LLMProvider, raw.LLM.Provider)
	setString(&cfg.LLMModel, raw.LLM.Model)
	setInt(&cfg.LLMTimeoutMs, raw.LLM.TimeoutMs)
	setString(&cfg.EmbeddingAddr, raw.Embedding.Addr)
	setString(&cfg.EmbeddingHTTPURL, raw.Embedding.HTTPURL)
	setString(&cfg.EmbeddingModel, raw.Embedding.Model)
	setInt(&cfg.EmbeddingTimeoutMs, raw.Embedding.TimeoutMs)
	setString(&cfg.DatabaseURL, raw.Database.URL)
	setString(&cfg.DBMigrationsDir, raw.Database.MigrationsDir)
	setString(&cfg.RoutesPath, raw.Routing.RoutesPath)
	setInt(&cfg.RouteTopK, raw.Routing.TopK)
	setInt(&cfg.RouteDomainTopK, raw.Routing.DomainTopK)
	if raw.Routing.DeterministicToolShortcuts != nil {
		cfg.DeterministicToolShortcuts = *raw.Routing.DeterministicToolShortcuts
	}
	setString(&cfg.OrchAddr, raw.Orchestrator.Addr)
	setString(&cfg.DecisionGrammarPath, raw.Orchestrator.DecisionGrammarPath)
	setString(&cfg.HistoryDir, raw.Orchestrator.HistoryDir)
	if raw.Orchestrator.DirectToolResponses != nil {
		cfg.DirectToolResponses = *raw.Orchestrator.DirectToolResponses
	}
	setString(&cfg.VisionAddr, raw.Services.VisionAddr)
	setString(&cfg.UIAddr, raw.Services.UIAddr)
	setString(&cfg.TTSAddr, raw.Services.TTSAddr)
	setString(&cfg.ASRAddr, raw.Services.ASRAddr)
	setString(&cfg.WakeWordAddr, raw.Services.WakeWordAddr)
	setString(&cfg.WeatherLatitude, raw.Weather.Latitude)
	setString(&cfg.WeatherLongitude, raw.Weather.Longitude)
	setString(&cfg.WeatherTimezone, raw.Weather.Timezone)
	setString(&cfg.WeatherLocationName, raw.Weather.LocationName)
	setString(&cfg.WeatherHTTPURL, raw.Weather.HTTPURL)
	setString(&cfg.WeatherGeocodingURL, raw.Weather.GeocodingURL)
	setString(&cfg.WeatherTemperatureUnit, raw.Weather.TemperatureUnit)
	setString(&cfg.WeatherWindSpeedUnit, raw.Weather.WindSpeedUnit)
	setString(&cfg.WeatherPrecipitationUnit, raw.Weather.PrecipitationUnit)
	setString(&cfg.OlderSisterHTTPURL, raw.OlderSister.HTTPURL)
	setString(&cfg.OlderSisterModel, raw.OlderSister.Model)
	setInt(&cfg.OlderSisterTimeoutMs, raw.OlderSister.TimeoutMs)
	if raw.OlderSister.WebSearch != nil {
		cfg.OlderSisterWebSearch = *raw.OlderSister.WebSearch
	}
	setString(&cfg.MemoryBaseURL, raw.Memory.BaseURL)
	setString(&cfg.MemoryUserKey, raw.Memory.UserKey)
	setInt(&cfg.MemoryTimeoutMs, raw.Memory.TimeoutMs)
	if raw.Memory.AutoSave != nil {
		cfg.MemoryAutoSave = *raw.Memory.AutoSave
	}
	return cfg, nil
}

func setString(dst *string, value string) {
	if strings.TrimSpace(value) != "" {
		*dst = value
	}
}

func setInt(dst *int, value int) {
	if value > 0 {
		*dst = value
	}
}

func getenvOrDefault(key, def string) string {
	val := os.Getenv(key)
	if val == "" {
		return def
	}
	return val
}

func defaultDecisionHTTPURL(provider, addr, fallback string) string {
	if strings.EqualFold(strings.TrimSpace(provider), "llamacpp") && strings.TrimSpace(addr) != "" {
		return "http://" + strings.TrimSpace(addr) + "/completion"
	}
	return fallback
}
