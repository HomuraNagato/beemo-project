package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type WeatherFetchFunc func(ctx context.Context, url string) ([]byte, error)

type WeatherLocation struct {
	Query     string `json:"query,omitempty"`
	Name      string `json:"name"`
	Latitude  string `json:"latitude"`
	Longitude string `json:"longitude"`
	Timezone  string `json:"timezone"`
}

type weatherArgs struct {
	When         string `json:"when,omitempty"`
	Focus        string `json:"focus,omitempty"`
	HourLocal    int    `json:"hour_local,omitempty"`
	Location     string `json:"location,omitempty"`
	LocationName string `json:"location_name,omitempty"`
	Latitude     string `json:"latitude,omitempty"`
	Longitude    string `json:"longitude,omitempty"`
	Timezone     string `json:"timezone,omitempty"`
}

type openMeteoResponse struct {
	Timezone     string `json:"timezone"`
	CurrentUnits struct {
		Temperature2M string `json:"temperature_2m"`
	} `json:"current_units"`
	Current struct {
		Time          string  `json:"time"`
		Temperature2M float64 `json:"temperature_2m"`
		WeatherCode   int     `json:"weather_code"`
	} `json:"current"`
	HourlyUnits struct {
		Temperature2M            string `json:"temperature_2m"`
		PrecipitationProbability string `json:"precipitation_probability"`
	} `json:"hourly_units"`
	Hourly struct {
		Time                     []string  `json:"time"`
		Temperature2M            []float64 `json:"temperature_2m"`
		PrecipitationProbability []float64 `json:"precipitation_probability"`
		WeatherCode              []int     `json:"weather_code"`
	} `json:"hourly"`
	DailyUnits struct {
		Temperature2MMax            string `json:"temperature_2m_max"`
		Temperature2MMin            string `json:"temperature_2m_min"`
		PrecipitationProbabilityMax string `json:"precipitation_probability_max"`
		PrecipitationSum            string `json:"precipitation_sum"`
	} `json:"daily_units"`
	Daily struct {
		Time                        []string  `json:"time"`
		WeatherCode                 []int     `json:"weather_code"`
		Temperature2MMax            []float64 `json:"temperature_2m_max"`
		Temperature2MMin            []float64 `json:"temperature_2m_min"`
		PrecipitationProbabilityMax []float64 `json:"precipitation_probability_max"`
		PrecipitationSum            []float64 `json:"precipitation_sum"`
	} `json:"daily"`
}

type openMeteoGeocodeResponse struct {
	Results []struct {
		Name      string  `json:"name"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		Timezone  string  `json:"timezone"`
		Admin1    string  `json:"admin1"`
		Country   string  `json:"country"`
	} `json:"results"`
}

var weatherLocationPattern = regexp.MustCompile(`(?i)\b(?:weather|temperature|forecast|rain)\b.*?\b(?:in|for)\s+([a-z0-9][a-z0-9 .,'-]{1,80})[?.!]*$`)

var usStateAbbrev = map[string]string{
	"al": "alabama", "ak": "alaska", "az": "arizona", "ar": "arkansas", "ca": "california",
	"co": "colorado", "ct": "connecticut", "de": "delaware", "fl": "florida", "ga": "georgia",
	"hi": "hawaii", "id": "idaho", "il": "illinois", "in": "indiana", "ia": "iowa",
	"ks": "kansas", "ky": "kentucky", "la": "louisiana", "me": "maine", "md": "maryland",
	"ma": "massachusetts", "mi": "michigan", "mn": "minnesota", "ms": "mississippi", "mo": "missouri",
	"mt": "montana", "ne": "nebraska", "nv": "nevada", "nh": "new hampshire", "nj": "new jersey",
	"nm": "new mexico", "ny": "new york", "nc": "north carolina", "nd": "north dakota", "oh": "ohio",
	"ok": "oklahoma", "or": "oregon", "pa": "pennsylvania", "ri": "rhode island", "sc": "south carolina",
	"sd": "south dakota", "tn": "tennessee", "tx": "texas", "ut": "utah", "vt": "vermont",
	"va": "virginia", "wa": "washington", "wv": "west virginia", "wi": "wisconsin", "wy": "wyoming",
	"dc": "district of columbia",
}

func executeWeather(ctx context.Context, req Request, cfg WeatherConfig) (Result, error) {
	args, err := parseWeatherArgs(req.Args)
	if err != nil {
		return Result{}, err
	}
	args = normalizeWeatherArgs(args)

	location := args.location(cfg)
	if strings.TrimSpace(location.Name) == "" || strings.TrimSpace(location.Latitude) == "" || strings.TrimSpace(location.Longitude) == "" {
		return needsInputResult(req.Action, []string{"location"}, "What location should I use for the weather?"), nil
	}

	if strings.TrimSpace(cfg.HTTPURL) == "" {
		return Result{}, fmt.Errorf("weather api url is not configured")
	}

	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	fetch := cfg.Fetch
	if fetch == nil {
		fetch = defaultWeatherFetch
	}

	requestURL, err := buildOpenMeteoURL(cfg, location)
	if err != nil {
		return Result{}, err
	}
	body, err := fetch(ctx, requestURL)
	if err != nil {
		return Result{}, err
	}

	var forecast openMeteoResponse
	if err := json.Unmarshal(body, &forecast); err != nil {
		return Result{}, fmt.Errorf("invalid weather response: %w", err)
	}

	output, err := formatWeatherResult(args, location, cfg, forecast, nowFn())
	if err != nil {
		return Result{}, err
	}
	return Result{
		Action: req.Action,
		Output: output,
	}, nil
}

func parseWeatherArgs(raw json.RawMessage) (weatherArgs, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var args weatherArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return weatherArgs{}, fmt.Errorf("invalid weather args: %w", err)
	}
	return args, nil
}

func normalizeWeatherArgs(args weatherArgs) weatherArgs {
	args.When = normalizeWeatherWhen(args.When)
	args.Focus = normalizeWeatherFocus(args.Focus)
	args.HourLocal = normalizeWeatherHour(args.HourLocal)
	if args.When == "" {
		switch {
		case args.HourLocal > 0:
			args.When = "today"
		default:
			switch args.Focus {
			case "rain":
				args.When = "today"
			case "temperature":
				args.When = "current"
			default:
				args.When = "current"
			}
		}
	}
	if args.When == "current" && args.HourLocal > 0 {
		args.When = "today"
	}
	if args.Focus == "" {
		args.Focus = "general"
	}
	args.Location = strings.TrimSpace(args.Location)
	args.LocationName = strings.TrimSpace(args.LocationName)
	args.Latitude = strings.TrimSpace(args.Latitude)
	args.Longitude = strings.TrimSpace(args.Longitude)
	args.Timezone = strings.TrimSpace(args.Timezone)
	return args
}

func normalizeWeatherHour(value int) int {
	if value < 0 || value > 23 {
		return 0
	}
	return value
}

func (args weatherArgs) location(cfg WeatherConfig) WeatherLocation {
	name := args.LocationName
	if name == "" {
		name = args.Location
	}
	return WeatherLocation{
		Query:     strings.TrimSpace(args.Location),
		Name:      strings.TrimSpace(name),
		Latitude:  strings.TrimSpace(args.Latitude),
		Longitude: strings.TrimSpace(args.Longitude),
		Timezone:  strings.TrimSpace(args.Timezone),
	}
}

func buildOpenMeteoURL(cfg WeatherConfig, location WeatherLocation) (string, error) {
	base, err := url.Parse(cfg.HTTPURL)
	if err != nil {
		return "", fmt.Errorf("invalid weather api url: %w", err)
	}
	query := base.Query()
	query.Set("latitude", strings.TrimSpace(location.Latitude))
	query.Set("longitude", strings.TrimSpace(location.Longitude))
	query.Set("timezone", defaultString(location.Timezone, "auto"))
	query.Set("forecast_days", "2")
	query.Set("current", "temperature_2m,weather_code")
	query.Set("hourly", "temperature_2m,precipitation_probability,weather_code")
	query.Set("daily", "weather_code,temperature_2m_max,temperature_2m_min,precipitation_probability_max,precipitation_sum")
	query.Set("temperature_unit", defaultString(cfg.TemperatureUnit, "fahrenheit"))
	query.Set("wind_speed_unit", defaultString(cfg.WindSpeedUnit, "mph"))
	query.Set("precipitation_unit", defaultString(cfg.PrecipitationUnit, "inch"))
	base.RawQuery = query.Encode()
	return base.String(), nil
}

func GeocodeWeatherLocation(ctx context.Context, cfg WeatherConfig, queryText string) (WeatherLocation, error) {
	queryText = strings.TrimSpace(queryText)
	if queryText == "" {
		return WeatherLocation{}, fmt.Errorf("location query is empty")
	}
	if strings.TrimSpace(cfg.GeocodingURL) == "" {
		return WeatherLocation{}, fmt.Errorf("weather geocoding url is not configured")
	}
	fetch := cfg.Fetch
	if fetch == nil {
		fetch = defaultWeatherFetch
	}
	for _, variant := range weatherLocationQueryVariants(queryText) {
		response, err := fetchGeocodingResponse(ctx, fetch, cfg.GeocodingURL, variant)
		if err != nil {
			return WeatherLocation{}, err
		}
		if len(response.Results) == 0 {
			continue
		}
		best := response.Results[0]
		name := strings.TrimSpace(best.Name)
		if best.Admin1 != "" && !strings.EqualFold(best.Admin1, best.Name) {
			name += ", " + strings.TrimSpace(best.Admin1)
		}
		if best.Country != "" {
			name += ", " + strings.TrimSpace(best.Country)
		}
		return WeatherLocation{
			Query:     queryText,
			Name:      name,
			Latitude:  formatNumber(best.Latitude),
			Longitude: formatNumber(best.Longitude),
			Timezone:  strings.TrimSpace(best.Timezone),
		}, nil
	}
	return WeatherLocation{}, fmt.Errorf("no weather location found for %q", queryText)
}

func fetchGeocodingResponse(ctx context.Context, fetch WeatherFetchFunc, geocodingURL, queryText string) (openMeteoGeocodeResponse, error) {
	base, err := url.Parse(geocodingURL)
	if err != nil {
		return openMeteoGeocodeResponse{}, fmt.Errorf("invalid weather geocoding url: %w", err)
	}
	values := base.Query()
	values.Set("name", queryText)
	values.Set("count", "1")
	values.Set("language", "en")
	values.Set("format", "json")
	base.RawQuery = values.Encode()

	body, err := fetch(ctx, base.String())
	if err != nil {
		return openMeteoGeocodeResponse{}, err
	}
	var response openMeteoGeocodeResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return openMeteoGeocodeResponse{}, fmt.Errorf("invalid weather geocoding response: %w", err)
	}
	return response, nil
}

func weatherLocationQueryVariants(queryText string) []string {
	cleaned := strings.TrimSpace(strings.ToLower(queryText))
	cleaned = strings.ReplaceAll(cleaned, ".", "")
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	baseVariants := []string{cleaned}
	if cleaned == "nyc" {
		baseVariants = append(baseVariants, "new york city", "new york")
	}
	parts := strings.Split(cleaned, ",")
	if len(parts) >= 2 {
		city := strings.TrimSpace(parts[0])
		region := strings.TrimSpace(parts[1])
		if city != "" {
			baseVariants = append(baseVariants, city)
			if expanded, ok := usStateAbbrev[region]; ok {
				baseVariants = append(baseVariants, city+" "+expanded)
			}
		}
	}

	seen := map[string]struct{}{}
	variants := make([]string, 0, len(baseVariants))
	for _, variant := range baseVariants {
		variant = strings.Trim(variant, " ,")
		if variant == "" {
			continue
		}
		if _, ok := seen[variant]; ok {
			continue
		}
		seen[variant] = struct{}{}
		variants = append(variants, variant)
	}
	return variants
}

func ExtractWeatherLocation(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	matches := weatherLocationPattern.FindStringSubmatch(strings.TrimSpace(text))
	if len(matches) < 2 {
		return ""
	}
	location := strings.Trim(matches[1], " .,?!")
	lower := strings.ToLower(location)
	switch lower {
	case "today", "tomorrow", "this evening", "evening", "tonight":
		return ""
	}
	if isWeatherTimePhrase(lower) {
		return ""
	}
	return location
}

func WeatherLocationRawJSON(query string) (json.RawMessage, error) {
	return json.Marshal(strings.TrimSpace(query))
}

func WeatherLocationCanonicalJSON(location WeatherLocation) (json.RawMessage, error) {
	return json.Marshal(location)
}

func DecodeWeatherLocation(raw json.RawMessage) (WeatherLocation, bool) {
	if len(raw) == 0 {
		return WeatherLocation{}, false
	}
	var location WeatherLocation
	if err := json.Unmarshal(raw, &location); err == nil && strings.TrimSpace(location.Name) != "" {
		return location, true
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil && strings.TrimSpace(text) != "" {
		return WeatherLocation{Name: strings.TrimSpace(text), Query: strings.TrimSpace(text)}, true
	}
	return WeatherLocation{}, false
}

func defaultWeatherFetch(ctx context.Context, requestURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("weather api returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return io.ReadAll(resp.Body)
}

func formatWeatherResult(args weatherArgs, location WeatherLocation, cfg WeatherConfig, forecast openMeteoResponse, now time.Time) (string, error) {
	locationName := strings.TrimSpace(location.Name)
	if locationName == "" {
		locationName = "the selected location"
	}
	now = localizeWeatherTime(now, forecast.Timezone, location.Timezone, cfg.Timezone)

	switch args.When {
	case "current":
		return formatCurrentWeather(args.Focus, locationName, forecast), nil
	case "today":
		if args.HourLocal > 0 || strings.Contains(strings.ToLower(location.Query), " at ") {
			return formatHourlyWeather(args.Focus, locationName, forecast, now, "today", args.HourLocal, "Today")
		}
		return formatDailyWeather(args.Focus, locationName, forecast, 0, "Today")
	case "tomorrow":
		if args.HourLocal > 0 {
			return formatHourlyWeather(args.Focus, locationName, forecast, now, "tomorrow", args.HourLocal, "Tomorrow")
		}
		return formatDailyWeather(args.Focus, locationName, forecast, 1, "Tomorrow")
	case "evening":
		return formatEveningWeather(locationName, forecast, now)
	default:
		return formatCurrentWeather(args.Focus, locationName, forecast), nil
	}
}

func localizeWeatherTime(now time.Time, timezones ...string) time.Time {
	for _, candidate := range timezones {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || strings.EqualFold(candidate, "auto") {
			continue
		}
		if loc, err := time.LoadLocation(candidate); err == nil {
			return now.In(loc)
		}
	}
	return now
}

func formatCurrentWeather(focus, location string, forecast openMeteoResponse) string {
	tempUnit := defaultString(forecast.CurrentUnits.Temperature2M, "°F")
	switch focus {
	case "temperature":
		return fmt.Sprintf("Current temperature in %s: %s%s.", location, formatNumber(forecast.Current.Temperature2M), tempUnit)
	default:
		return fmt.Sprintf("Current weather in %s: %s and %s%s.", location, describeWeatherCode(forecast.Current.WeatherCode), formatNumber(forecast.Current.Temperature2M), tempUnit)
	}
}

func formatDailyWeather(focus, location string, forecast openMeteoResponse, dayIndex int, dayLabel string) (string, error) {
	if dayIndex >= len(forecast.Daily.Time) ||
		dayIndex >= len(forecast.Daily.WeatherCode) ||
		dayIndex >= len(forecast.Daily.Temperature2MMax) ||
		dayIndex >= len(forecast.Daily.Temperature2MMin) {
		return "", fmt.Errorf("weather forecast is unavailable for %s", strings.ToLower(dayLabel))
	}
	tempUnit := defaultString(forecast.DailyUnits.Temperature2MMax, "°F")
	switch focus {
	case "temperature":
		return fmt.Sprintf("%s in %s: high %s%s, low %s%s.", dayLabel, location, formatNumber(forecast.Daily.Temperature2MMax[dayIndex]), tempUnit, formatNumber(forecast.Daily.Temperature2MMin[dayIndex]), tempUnit), nil
	case "rain":
		pop := valueAt(forecast.Daily.PrecipitationProbabilityMax, dayIndex)
		rainSum := valueAt(forecast.Daily.PrecipitationSum, dayIndex)
		rainUnit := defaultString(forecast.DailyUnits.PrecipitationSum, "in")
		if pop >= 40 {
			return fmt.Sprintf("Yes, rain is possible in %s %s. Chance up to %s%% with about %s %s expected.", location, strings.ToLower(dayLabel), formatNumber(pop), formatNumber(rainSum), rainUnit), nil
		}
		return fmt.Sprintf("Rain looks unlikely in %s %s. Chance up to %s%% with about %s %s expected.", location, strings.ToLower(dayLabel), formatNumber(pop), formatNumber(rainSum), rainUnit), nil
	default:
		pop := valueAt(forecast.Daily.PrecipitationProbabilityMax, dayIndex)
		return fmt.Sprintf("%s in %s: %s, high %s%s, low %s%s, rain chance up to %s%%.", dayLabel, location, describeWeatherCode(forecast.Daily.WeatherCode[dayIndex]), formatNumber(forecast.Daily.Temperature2MMax[dayIndex]), tempUnit, formatNumber(forecast.Daily.Temperature2MMin[dayIndex]), tempUnit, formatNumber(pop)), nil
	}
}

func formatEveningWeather(location string, forecast openMeteoResponse, now time.Time) (string, error) {
	if len(forecast.Hourly.Time) == 0 {
		return "", fmt.Errorf("weather forecast is unavailable for this evening")
	}
	index, stamp, err := selectEveningHour(forecast.Hourly.Time, now)
	if err != nil {
		return "", err
	}
	if index >= len(forecast.Hourly.Temperature2M) || index >= len(forecast.Hourly.WeatherCode) {
		return "", fmt.Errorf("weather forecast is unavailable for this evening")
	}
	pop := valueAt(forecast.Hourly.PrecipitationProbability, index)
	tempUnit := defaultString(forecast.HourlyUnits.Temperature2M, "°F")
	return fmt.Sprintf("This evening in %s around %s: %s and %s%s with a %s%% chance of precipitation.", location, stamp.Format("3 PM"), describeWeatherCode(forecast.Hourly.WeatherCode[index]), formatNumber(forecast.Hourly.Temperature2M[index]), tempUnit, formatNumber(pop)), nil
}

func formatHourlyWeather(focus, location string, forecast openMeteoResponse, now time.Time, day string, hourLocal int, dayLabel string) (string, error) {
	if len(forecast.Hourly.Time) == 0 {
		return "", fmt.Errorf("weather forecast is unavailable for %s at %s", strings.ToLower(dayLabel), formatHourLabel(hourLocal))
	}
	index, stamp, err := selectHourlyForecast(forecast.Hourly.Time, now, day, hourLocal)
	if err != nil {
		return "", err
	}
	if index >= len(forecast.Hourly.Temperature2M) || index >= len(forecast.Hourly.WeatherCode) {
		return "", fmt.Errorf("weather forecast is unavailable for %s at %s", strings.ToLower(dayLabel), formatHourLabel(hourLocal))
	}
	tempUnit := defaultString(forecast.HourlyUnits.Temperature2M, "°F")
	pop := valueAt(forecast.Hourly.PrecipitationProbability, index)
	timeLabel := stamp.Format("3 PM")
	switch focus {
	case "temperature":
		return fmt.Sprintf("%s in %s at %s: %s%s.", dayLabel, location, timeLabel, formatNumber(forecast.Hourly.Temperature2M[index]), tempUnit), nil
	case "rain":
		if pop >= 40 {
			return fmt.Sprintf("Rain is possible in %s %s at %s. Chance around %s%%.", location, strings.ToLower(dayLabel), timeLabel, formatNumber(pop)), nil
		}
		return fmt.Sprintf("Rain looks unlikely in %s %s at %s. Chance around %s%%.", location, strings.ToLower(dayLabel), timeLabel, formatNumber(pop)), nil
	default:
		return fmt.Sprintf("%s in %s at %s: %s and %s%s with a %s%% chance of precipitation.", dayLabel, location, timeLabel, describeWeatherCode(forecast.Hourly.WeatherCode[index]), formatNumber(forecast.Hourly.Temperature2M[index]), tempUnit, formatNumber(pop)), nil
	}
}

func selectEveningHour(times []string, now time.Time) (int, time.Time, error) {
	bestIdx := -1
	var bestTime time.Time
	for idx, value := range times {
		stamp, err := time.Parse("2006-01-02T15:04", value)
		if err != nil {
			continue
		}
		if stamp.Year() != now.Year() || stamp.YearDay() != now.YearDay() {
			continue
		}
		hour := stamp.Hour()
		if hour < 17 || hour > 21 {
			continue
		}
		if bestIdx == -1 || absInt(hour-18) < absInt(bestTime.Hour()-18) {
			bestIdx = idx
			bestTime = stamp
		}
	}
	if bestIdx == -1 {
		return -1, time.Time{}, fmt.Errorf("weather forecast is unavailable for this evening")
	}
	return bestIdx, bestTime, nil
}

func selectHourlyForecast(times []string, now time.Time, day string, hourLocal int) (int, time.Time, error) {
	targetDay := now
	if day == "tomorrow" {
		targetDay = targetDay.AddDate(0, 0, 1)
	}
	for idx, value := range times {
		stamp, err := time.Parse("2006-01-02T15:04", value)
		if err != nil {
			continue
		}
		if stamp.Year() != targetDay.Year() || stamp.YearDay() != targetDay.YearDay() {
			continue
		}
		if stamp.Hour() != hourLocal {
			continue
		}
		return idx, stamp, nil
	}
	return -1, time.Time{}, fmt.Errorf("weather forecast is unavailable for %s at %s", day, formatHourLabel(hourLocal))
}

func formatHourLabel(hour int) string {
	stamp := time.Date(2000, 1, 1, hour, 0, 0, 0, time.UTC)
	return stamp.Format("3 PM")
}

func normalizeWeatherWhen(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "current", "now":
		return "current"
	case "today":
		return "today"
	case "tomorrow":
		return "tomorrow"
	case "evening", "tonight", "this_evening":
		return "evening"
	default:
		return ""
	}
}

func normalizeWeatherFocus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "general", "weather":
		return "general"
	case "temperature", "temp":
		return "temperature"
	case "rain", "precipitation":
		return "rain"
	default:
		return ""
	}
}

func isWeatherTimePhrase(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return false
	}
	matched, _ := regexp.MatchString(`^\d{1,2}(?::\d{2})?\s*(am|pm)?$`, value)
	return matched
}

func describeWeatherCode(code int) string {
	switch code {
	case 0:
		return "clear skies"
	case 1:
		return "mostly clear skies"
	case 2:
		return "partly cloudy skies"
	case 3:
		return "overcast skies"
	case 45, 48:
		return "fog"
	case 51, 53, 55, 56, 57:
		return "drizzle"
	case 61, 63, 65, 66, 67, 80, 81, 82:
		return "rain"
	case 71, 73, 75, 77, 85, 86:
		return "snow"
	case 95, 96, 99:
		return "thunderstorms"
	default:
		return "mixed weather"
	}
}

func valueAt(values []float64, idx int) float64 {
	if idx < 0 || idx >= len(values) {
		return 0
	}
	return values[idx]
}

func defaultString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
