package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	LLMAddr                  string
	LLMHTTPURL               string
	LLMModel                 string
	LLMTimeoutMs             int
	DatabaseURL              string
	DBMigrationsDir          string
	EmbeddingAddr            string
	EmbeddingHTTPURL         string
	EmbeddingModel           string
	EmbeddingTimeoutMs       int
	RoutesPath               string
	FactsPath                string
	RouteTopK                int
	RouteDomainTopK          int
	OrchAddr                 string
	DecisionGrammarPath      string
	HistoryDir               string
	VisionAddr               string
	UIAddr                   string
	TTSAddr                  string
	ASRAddr                  string
	WakeWordAddr             string
	WeatherLatitude          string
	WeatherLongitude         string
	WeatherTimezone          string
	WeatherLocationName      string
	WeatherHTTPURL           string
	WeatherGeocodingURL      string
	WeatherTemperatureUnit   string
	WeatherWindSpeedUnit     string
	WeatherPrecipitationUnit string
	OlderSisterAPIKey        string
	OlderSisterHTTPURL       string
	OlderSisterModel         string
	OlderSisterTimeoutMs     int
	OlderSisterWebSearch     bool
}

func Load() Config {
	llmAddr := os.Getenv("LLM_ADDR")
	llmHTTPURL := os.Getenv("LLM_HTTP_URL")
	if llmHTTPURL == "" && llmAddr != "" {
		llmHTTPURL = "http://" + strings.TrimSpace(llmAddr) + "/v1/chat/completions"
	}
	embeddingAddr := os.Getenv("EMBEDDING_ADDR")
	embeddingHTTPURL := os.Getenv("EMBEDDING_HTTP_URL")
	if embeddingHTTPURL == "" && embeddingAddr != "" {
		embeddingHTTPURL = "http://" + strings.TrimSpace(embeddingAddr) + "/v1/embeddings"
	}

	return Config{
		LLMAddr:                  llmAddr,
		LLMHTTPURL:               llmHTTPURL,
		LLMModel:                 os.Getenv("REASONING_MODEL"),
		LLMTimeoutMs:             atoiOrDefault(os.Getenv("LLM_TIMEOUT_MS"), 120000),
		DatabaseURL:              os.Getenv("DATABASE_URL"),
		DBMigrationsDir:          getenvOrDefault("DB_MIGRATIONS_DIR", "db/migrations"),
		EmbeddingAddr:            embeddingAddr,
		EmbeddingHTTPURL:         embeddingHTTPURL,
		EmbeddingModel:           os.Getenv("EMBEDDING_MODEL"),
		EmbeddingTimeoutMs:       atoiOrDefault(os.Getenv("EMBEDDING_TIMEOUT_MS"), 30000),
		RoutesPath:               getenvOrDefault("ROUTES_PATH", "routes.yaml"),
		FactsPath:                getenvOrDefault("FACTS_PATH", "facts.yaml"),
		RouteTopK:                atoiOrDefault(os.Getenv("ROUTE_TOP_K"), 5),
		RouteDomainTopK:          atoiOrDefault(os.Getenv("ROUTE_DOMAIN_TOP_K"), 2),
		OrchAddr:                 os.Getenv("ORCH_ADDR"),
		DecisionGrammarPath:      getenvOrDefault("DECISION_GRAMMAR_PATH", "scripts/grammars/tool_list.gbnf"),
		HistoryDir:               getenvOrDefault("HISTORY_DIR", "memory"),
		VisionAddr:               os.Getenv("VISION_ADDR"),
		UIAddr:                   os.Getenv("UI_ADDR"),
		TTSAddr:                  os.Getenv("TTS_ADDR"),
		ASRAddr:                  os.Getenv("ASR_ADDR"),
		WakeWordAddr:             os.Getenv("WAKEWORD_ADDR"),
		WeatherLatitude:          os.Getenv("WEATHER_LATITUDE"),
		WeatherLongitude:         os.Getenv("WEATHER_LONGITUDE"),
		WeatherTimezone:          getenvOrDefault("WEATHER_TIMEZONE", "auto"),
		WeatherLocationName:      os.Getenv("WEATHER_LOCATION_NAME"),
		WeatherHTTPURL:           getenvOrDefault("WEATHER_HTTP_URL", "https://api.open-meteo.com/v1/forecast"),
		WeatherGeocodingURL:      getenvOrDefault("WEATHER_GEOCODING_URL", "https://geocoding-api.open-meteo.com/v1/search"),
		WeatherTemperatureUnit:   getenvOrDefault("WEATHER_TEMPERATURE_UNIT", "fahrenheit"),
		WeatherWindSpeedUnit:     getenvOrDefault("WEATHER_WIND_SPEED_UNIT", "mph"),
		WeatherPrecipitationUnit: getenvOrDefault("WEATHER_PRECIPITATION_UNIT", "inch"),
		OlderSisterAPIKey:        os.Getenv("OPENAI_API_KEY"),
		OlderSisterHTTPURL:       getenvOrDefault("OLDER_SISTER_HTTP_URL", "https://api.openai.com/v1/responses"),
		OlderSisterModel:         getenvOrDefault("OLDER_SISTER_MODEL", "gpt-5-mini"),
		OlderSisterTimeoutMs:     atoiOrDefault(os.Getenv("OLDER_SISTER_TIMEOUT_MS"), 120000),
		OlderSisterWebSearch:     boolOrDefault(os.Getenv("OLDER_SISTER_WEB_SEARCH"), true),
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

func boolOrDefault(v string, def bool) bool {
	if strings.TrimSpace(v) == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}
