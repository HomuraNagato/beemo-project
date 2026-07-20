package main

import (
	"database/sql"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	pb "eve-beemo/proto/gen/proto"
	"eve-beemo/src/orchestrator/config"
	orchestrdb "eve-beemo/src/orchestrator/db"
	"eve-beemo/src/orchestrator/routing"
	orchtools "eve-beemo/src/orchestrator/tools"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	slog.SetDefault(logger)
	logger.Info("orchestrator.start")

	cfg := config.Load()
	var routeDB *sql.DB
	if strings.TrimSpace(cfg.DatabaseURL) != "" {
		db, err := orchestrdb.OpenAndMigrate(cfg.DatabaseURL, cfg.DBMigrationsDir)
		if err != nil {
			logger.Error("orchestrator.database", "status", "error", "database_url", cfg.DatabaseURL, "err", err)
			return
		}
		defer db.Close()
		routeDB = db
		logger.Info("orchestrator.database", "status", "ok", "migrations_dir", cfg.DBMigrationsDir)
	}

	orchAddr := ":5013"
	if cfg.OrchAddr != "" {
		orchAddr = cfg.OrchAddr
	}
	lis, err := net.Listen("tcp", orchAddr)
	if err != nil {
		logger.Error("orchestrator.listen", "status", "error", "addr", orchAddr, "err", err)
		return
	}

	grpcServer := grpc.NewServer()
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	healthpb.RegisterHealthServer(grpcServer, healthServer)
	reflection.Register(grpcServer)

	selector := routing.NewSelectorWithDB(cfg.RoutesPath, cfg.EmbeddingHTTPURL, cfg.EmbeddingModel, cfg.RouteTopK, cfg.RouteDomainTopK, routeDB)
	if selector.Enabled() {
		timeout := time.Duration(cfg.EmbeddingTimeoutMs) * time.Millisecond
		if err := selector.Warmup(timeout); err != nil {
			logger.Error("orchestrator.route_warmup", "status", "error", "err", err)
			return
		}
		logger.Info("orchestrator.route_warmup", "status", "ok", "routes_path", cfg.RoutesPath)
	}

	pb.RegisterOrchestratorServer(grpcServer, &orchestratorServer{
		cfg: cfg,
		tools: orchtools.NewLocalExecutorWithAllConfigs(orchtools.WeatherConfig{
			HTTPURL:           cfg.WeatherHTTPURL,
			GeocodingURL:      cfg.WeatherGeocodingURL,
			Latitude:          cfg.WeatherLatitude,
			Longitude:         cfg.WeatherLongitude,
			Timezone:          cfg.WeatherTimezone,
			LocationName:      cfg.WeatherLocationName,
			TemperatureUnit:   cfg.WeatherTemperatureUnit,
			WindSpeedUnit:     cfg.WeatherWindSpeedUnit,
			PrecipitationUnit: cfg.WeatherPrecipitationUnit,
		}, orchtools.OlderSisterConfig{
			APIKey:    cfg.OlderSisterAPIKey,
			HTTPURL:   cfg.OlderSisterHTTPURL,
			Model:     cfg.OlderSisterModel,
			TimeoutMs: cfg.OlderSisterTimeoutMs,
			WebSearch: cfg.OlderSisterWebSearch,
		}, orchtools.MemoryConfig{
			BaseURL:   cfg.MemoryBaseURL,
			UserKey:   cfg.MemoryUserKey,
			TimeoutMs: cfg.MemoryTimeoutMs,
			AutoSave:  cfg.MemoryAutoSave,
		}),
		routeSelector: selector,
		logger:        logger,
	})
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus("eve.Orchestrator", healthpb.HealthCheckResponse_SERVING)
	logger.Info("orchestrator.listen", "status", "serving", "addr", orchAddr)
	if err := grpcServer.Serve(lis); err != nil {
		healthServer.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
		logger.Error("orchestrator.serve", "status", "error", "err", err)
	}
}
