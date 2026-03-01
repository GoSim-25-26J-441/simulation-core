package main

import (
	"context"
	"flag"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"syscall"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/improvement"
	"github.com/GoSim-25-26J-441/simulation-core/internal/simd"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/logger"
	"google.golang.org/grpc"
)

// Env name for limiting candidate_run_ids to top N by score (0 or unset = all).
const envTopCandidates = "SIMD_OPTIMIZATION_TOP_CANDIDATES"

func getTopCandidatesN() int {
	s := os.Getenv(envTopCandidates)
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// buildTopCandidateRunIDs returns run IDs for candidates, optionally limited to top N by score (lower is better).
// If n <= 0, all candidates are returned. The best run is always included when n > 0.
func buildTopCandidateRunIDs(r *improvement.ExperimentResult, n int) []string {
	runs := make([]*improvement.RunContext, 0, len(r.Runs))
	for _, rc := range r.Runs {
		if rc != nil && rc.RunID != "" {
			runs = append(runs, rc)
		}
	}
	if n <= 0 || len(runs) <= n {
		// Return all, preserving order (or take all if within limit)
		out := make([]string, 0, len(runs))
		for _, rc := range runs {
			out = append(out, rc.RunID)
		}
		return out
	}
	// Sort by score ascending (lower score = better for p95)
	sort.Slice(runs, func(i, j int) bool { return runs[i].Score < runs[j].Score })
	out := make([]string, 0, n+1)
	seen := make(map[string]bool)
	for i := 0; i < n && i < len(runs); i++ {
		id := runs[i].RunID
		out = append(out, id)
		seen[id] = true
	}
	if r.BestRunID != "" && !seen[r.BestRunID] {
		out = append(out, r.BestRunID)
	}
	return out
}

// optimizationRunnerAdapter adapts improvement.Orchestrator to simd.OptimizationRunner.
// It creates a fresh orchestrator per run with the requested params.
type optimizationRunnerAdapter struct {
	store    *simd.RunStore
	executor *simd.RunExecutor
}

func (a *optimizationRunnerAdapter) RunExperiment(ctx context.Context, runID string, scenario *config.Scenario, durationMs int64, params *simd.OptimizationParams) (bestRunID string, bestScore float64, iterations int32, candidateRunIDs []string, err error) {
	objective, err := improvement.NewObjectiveFunction(params.Objective)
	if err != nil {
		return "", 0, 0, nil, err
	}

	maxIter := int(params.MaxIterations)
	if maxIter <= 0 {
		maxIter = 10
	}
	stepSize := params.StepSize
	if stepSize <= 0 {
		stepSize = 1.0
	}

	optimizer := improvement.NewOptimizer(objective, maxIter, stepSize).
		WithProgressReporter(func(iter int, score float64) {
			iterClamped := int32(math.Max(0, math.Min(float64(iter), float64(math.MaxInt32))))
			a.store.SetOptimizationProgress(runID, iterClamped, score)
		})
	orchestrator := improvement.NewOrchestrator(a.store, a.executor, optimizer, objective)

	// Run in goroutine so we can cancel active sub-runs when ctx is done
	type result struct {
		bestRunID        string
		bestScore        float64
		iterations       int32
		candidateRunIDs  []string
		err              error
	}
	done := make(chan result, 1)
	go func() {
		r, err := orchestrator.RunExperiment(ctx, scenario, durationMs)
		if err != nil {
			done <- result{err: err}
			return
		}
		iterClamped := int32(math.Max(0, math.Min(float64(r.Iterations), float64(math.MaxInt32))))
		candidates := buildTopCandidateRunIDs(r, getTopCandidatesN())
		done <- result{bestRunID: r.BestRunID, bestScore: r.BestScore, iterations: iterClamped, candidateRunIDs: candidates}
	}()

	select {
	case res := <-done:
		if res.err != nil {
			return "", 0, 0, nil, res.err
		}
		return res.bestRunID, res.bestScore, res.iterations, res.candidateRunIDs, nil
	case <-ctx.Done():
		if cancelErr := orchestrator.CancelActiveRuns(); cancelErr != nil {
			logger.Warn("cancel active runs failed during optimization cancellation", "error", cancelErr)
		}
		<-done // Wait for RunExperiment to return
		return "", 0, 0, nil, ctx.Err()
	}
}

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
	executor.SetOptimizationRunner(&optimizationRunnerAdapter{store: store, executor: executor})

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
		WriteTimeout:      0, // Disabled for SSE streaming (long-lived connections)
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
