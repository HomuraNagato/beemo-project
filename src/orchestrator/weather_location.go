package main

import (
	"context"
	"encoding/json"
	"strings"

	"eve-beemo/src/orchestrator/config"
	orchtools "eve-beemo/src/orchestrator/tools"
)

func resolveWeatherLocation(ctx context.Context, tools orchtools.Executor, cfg config.Config, call orchtools.PlannedCall) (orchtools.PlannedCall, error) {
	argsMap := map[string]json.RawMessage{}
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &argsMap); err != nil {
			return orchtools.PlannedCall{}, err
		}
	}
	locationQuery := strings.TrimSpace(orchtools.StringFieldRaw(argsMap["location"]))
	if locationQuery == "" {
		return call, nil
	}
	weatherCfg := orchtools.WeatherConfig{GeocodingURL: cfg.WeatherGeocodingURL}
	if provider, ok := tools.(weatherConfigProvider); ok {
		weatherCfg = provider.WeatherConfig()
		if strings.TrimSpace(weatherCfg.GeocodingURL) == "" {
			weatherCfg.GeocodingURL = cfg.WeatherGeocodingURL
		}
	}
	location, err := orchtools.GeocodeWeatherLocation(ctx, weatherCfg, locationQuery)
	if err != nil {
		return orchtools.PlannedCall{}, err
	}
	args := map[string]any{}
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return orchtools.PlannedCall{}, err
		}
	}
	if strings.TrimSpace(location.Query) != "" {
		args["location"] = strings.TrimSpace(location.Query)
	}
	args["location_name"] = strings.TrimSpace(location.Name)
	args["latitude"] = strings.TrimSpace(location.Latitude)
	args["longitude"] = strings.TrimSpace(location.Longitude)
	if strings.TrimSpace(location.Timezone) != "" {
		args["timezone"] = strings.TrimSpace(location.Timezone)
	}
	updated, err := json.Marshal(args)
	if err != nil {
		return orchtools.PlannedCall{}, err
	}
	call.Args = updated
	return call, nil
}
