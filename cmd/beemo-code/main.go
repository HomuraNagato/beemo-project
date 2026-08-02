package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"eve-beemo/internal/codetools"
	pb "eve-beemo/proto/gen/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "beemo-code:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := codetools.LoadConfig()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Socket), 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(cfg.Socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("socket path exists and is not a socket: %s", cfg.Socket)
		}
		if err := os.Remove(cfg.Socket); err != nil {
			return err
		}
	}
	listener, err := net.Listen("unix", cfg.Socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(cfg.Socket)
	if err := os.Chmod(cfg.Socket, 0o660); err != nil {
		return err
	}

	server := grpc.NewServer()
	pb.RegisterCodeToolsServer(server, codetools.NewService(cfg))
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus("eve.CodeTools", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(server, healthServer)
	reflection.Register(server)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		server.GracefulStop()
	}()
	slog.Info("beemo-code.listen", "socket", cfg.Socket, "roots", cfg.Roots)
	return server.Serve(listener)
}
