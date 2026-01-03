package main

import (
	"context"
	"flag"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/simd"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/logger"
	"google.golang.org/grpc"
)

func main() {
	var grpcAddr string
	var httpAddr string
	var logLevel string

	flag.StringVar(&grpcAddr, "grpc-addr", ":50051", "gRPC listen address")
	flag.StringVar(&httpAddr, "http-addr", ":8080", "HTTP listen address")
	flag.StringVar(&logLevel, "log-level", "info", "log level (debug, info, warn, error)")
	flag.Parse()

	logger.SetDefault(logger.NewText(logLevel, os.Stdout))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	store := simd.NewRunStore()
	executor := simd.NewRunExecutor(store)

	// TODO: Configure gRPC server security (e.g., TLS, authentication, rate limiting)
	// before using this service in a production environment.
	grpcServer := grpc.NewServer()
	simulationv1.RegisterSimulationServiceServer(grpcServer, simd.NewSimulationGRPCServer(store, executor))

	grpcLis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		logger.Error("failed to listen for gRPC", "addr", grpcAddr, "error", err)
		stop()
		os.Exit(1)
	}

	httpSrv := &http.Server{
		Addr:              httpAddr,
		Handler:           simd.NewHTTPServer(store, executor).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	// Start servers.
	go func() {
		logger.Info("gRPC server listening", "addr", grpcAddr)
		if err := grpcServer.Serve(grpcLis); err != nil {
			logger.Error("gRPC server error", "error", err)
			stop()
		}
	}()

	go func() {
		logger.Info("HTTP server listening", "addr", httpAddr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server error", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutdown requested")
	stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	grpcServer.GracefulStop()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP shutdown error", "error", err)
	}
}
