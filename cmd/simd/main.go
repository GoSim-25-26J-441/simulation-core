package main

import (
	"context"
	"fmt"
	"flag"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/calibrationserver"
	"github.com/GoSim-25-26J-441/simulation-core/internal/improvement"
	"github.com/GoSim-25-26J-441/simulation-core/internal/simd"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/logger"
	"google.golang.org/grpc"
)

// Env name for limiting candidate_run_ids to top N by score (0 or unset = all).
const envTopCandidates = "SIMD_OPTIMIZATION_TOP_CANDIDATES"

// Env name for optional comma-separated hostnames or IPs allowed for callback URL (even if private).
const envCallbackWhitelist = "SIMULATION_CALLBACK_WHITELIST"

func getCallbackWhitelist() []string {
	s := os.Getenv(envCallbackWhitelist)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

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
// For batch optimization, ordering follows CompareBatchScores (feasible first, then violation, efficiency).
// If n <= 0, all candidates are returned. The best run is always included when n > 0.
// Each RunID appears at most once (duplicates from multiple history steps are removed).
func buildTopCandidateRunIDs(r *improvement.ExperimentResult, n int) []string {
	if r != nil && len(r.BatchCandidateRunIDs) > 0 {
		return trimTopBatchCandidates(r.BatchCandidateRunIDs, r.BestRunID, n)
	}
	runs := make([]*improvement.RunContext, 0, len(r.Runs))
	for _, rc := range r.Runs {
		if rc != nil && rc.RunID != "" {
			runs = append(runs, rc)
		}
	}
	if n <= 0 || len(runs) <= n {
		// Return all unique RunIDs, preserving order of first occurrence
		seen := make(map[string]bool)
		out := make([]string, 0, len(runs))
		for _, rc := range runs {
			if !seen[rc.RunID] {
				seen[rc.RunID] = true
				out = append(out, rc.RunID)
			}
		}
		return out
	}
	// Sort by score ascending (lower score = better for p95)
	sort.Slice(runs, func(i, j int) bool { return runs[i].Score < runs[j].Score })
	out := make([]string, 0, n+1)
	seen := make(map[string]bool)
	for i := 0; i < len(runs) && len(out) < n; i++ {
		id := runs[i].RunID
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	if r.BestRunID != "" && !seen[r.BestRunID] {
		out = append(out, r.BestRunID)
	}
	return out
}

func trimTopBatchCandidates(ordered []string, bestRunID string, n int) []string {
	if n <= 0 || len(ordered) <= n {
		out := make([]string, 0, len(ordered))
		seen := make(map[string]bool)
		for _, id := range ordered {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
		return out
	}
	out := make([]string, 0, n+1)
	seen := make(map[string]bool)
	for i := 0; i < len(ordered) && len(out) < n; i++ {
		id := ordered[i]
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if bestRunID != "" && !seen[bestRunID] {
		out = append(out, bestRunID)
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
	safety := improvement.OptimizationSafetyConfigFromEnv()
	objName := params.Objective
	if objName == "" {
		objName = "p95_latency_ms"
	}
	var utilTarget *improvement.UtilizationTarget
	if (objName == "cpu_utilization" || objName == "memory_utilization") &&
		params.TargetUtilLow >= 0 && params.TargetUtilHigh <= 1 && params.TargetUtilLow < params.TargetUtilHigh {
		utilTarget = &improvement.UtilizationTarget{Low: params.TargetUtilLow, High: params.TargetUtilHigh}
	}
	objective, err := improvement.NewObjectiveFunction(objName, utilTarget)
	if err != nil {
		return "", 0, 0, nil, err
	}

	maxIter := int(params.MaxIterations)
	if maxIter <= 0 {
		maxIter = 10
	}
	if safety.MaxIterations > 0 && int32(maxIter) > safety.MaxIterations {
		return "", 0, 0, nil, fmt.Errorf("optimization max_iterations %d exceeds server limit %d", maxIter, safety.MaxIterations)
	}
	stepSize := params.StepSize
	if stepSize <= 0 {
		stepSize = 1.0
	}

	var orchestrator *improvement.Orchestrator

	type result struct {
		bestRunID       string
		bestScore       float64
		iterations      int32
		candidateRunIDs []string
		err             error
	}
	done := make(chan result, 1)

	if params.Batch != nil {
		if !safety.AllowBatch {
			return "", 0, 0, nil, fmt.Errorf("batch optimization disabled by server")
		}
		orchestrator = improvement.NewOrchestrator(a.store, a.executor, improvement.NewOptimizer(objective, 1, 1.0), objective)
		orchestrator.WithMaxParallelRuns(safety.MaxConcurrentCandidates)
		go func() {
			maxEval := int(params.MaxEvaluations)
			if maxEval <= 0 {
				maxEval = int(safety.MaxEvaluations)
			}
			if safety.MaxEvaluations > 0 && maxEval > int(safety.MaxEvaluations) {
				done <- result{err: fmt.Errorf("optimization max_evaluations %d exceeds server limit %d", maxEval, safety.MaxEvaluations)}
				return
			}
			r, err := orchestrator.RunBatchExperiment(ctx, scenario, durationMs, params.Batch, maxEval)
			if err != nil {
				done <- result{err: err}
				return
			}
			if r.Batch != nil {
				// Structured batch fields (feasible, violation, efficiency, summary) are authoritative for UI/API.
				if err := a.store.SetBatchRecommendation(runID, r.Batch.Feasible, r.Batch.BestScore.ViolationScore, r.Batch.BestScore.EfficiencyScore, r.Batch.Summary); err != nil {
					logger.Warn("failed to store batch recommendation", "run_id", runID, "error", err)
				}
			}
			iterClamped := int32(math.Max(0, math.Min(float64(r.Iterations), float64(math.MaxInt32))))
			candidates := buildTopCandidateRunIDs(r, getTopCandidatesN())
			// r.BestScore is efficiency-only for batch (legacy scalar path); do not use it alone for batch semantics.
			done <- result{bestRunID: r.BestRunID, bestScore: r.BestScore, iterations: iterClamped, candidateRunIDs: candidates}
		}()
	} else {
		optimizer := improvement.NewOptimizer(objective, maxIter, stepSize).
			WithProgressReporter(func(iter int, score float64) {
				iterClamped := int32(math.Max(0, math.Min(float64(iter), float64(math.MaxInt32))))
				a.store.SetOptimizationProgress(runID, iterClamped, score)
			})
		if params.MaxEvaluations > 0 {
			if safety.MaxEvaluations > 0 && params.MaxEvaluations > safety.MaxEvaluations {
				return "", 0, 0, nil, fmt.Errorf("optimization max_evaluations %d exceeds server limit %d", params.MaxEvaluations, safety.MaxEvaluations)
			}
			optimizer = optimizer.WithMaxEvaluations(int(params.MaxEvaluations))
		} else if safety.MaxEvaluations > 0 {
			optimizer = optimizer.WithMaxEvaluations(int(safety.MaxEvaluations))
		}
		orchestrator = improvement.NewOrchestrator(a.store, a.executor, optimizer, objective)
		orchestrator.WithMaxParallelRuns(safety.MaxConcurrentCandidates)
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
	}

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
		<-done
		return "", 0, 0, nil, ctx.Err()
	}
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "validate":
			os.Exit(runValidateCLI(os.Args[2:]))
		case "calibrate":
			os.Exit(runCalibrateCLI(os.Args[2:]))
		}
	}

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
	store.SetOnlineLimits(simd.OnlineRunLimitsFromEnv())
	executor := simd.NewRunExecutor(store, getCallbackWhitelist())
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

	httpS := simd.NewHTTPServer(store, executor)
	calibrationserver.Register(httpS.ServeMux())
	httpSrv := &http.Server{
		Addr:              httpAddr,
		Handler:           httpS.Handler(),
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
	store.Stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	grpcServer.GracefulStop()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP shutdown error", "error", err)
	}
}
