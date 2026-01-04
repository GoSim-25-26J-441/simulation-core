package simd

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/logger"
)

type HTTPServer struct {
	mux      *http.ServeMux
	store    *RunStore
	Executor *RunExecutor
}

func NewHTTPServer(store *RunStore, executor *RunExecutor) *HTTPServer {
	s := &HTTPServer{
		mux:      http.NewServeMux(),
		store:    store,
		Executor: executor,
	}

	s.mux.HandleFunc("/healthz", s.handleHealthz)
	s.mux.HandleFunc("/v1/runs", s.handleRuns)
	s.mux.HandleFunc("/v1/runs/", s.handleRunByID)

	return s
}

func (s *HTTPServer) Handler() http.Handler {
	return s.mux
}

func (s *HTTPServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"status":    "ok",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		http.Error(w, `{"error":"encode failed"}`, http.StatusInternalServerError)
	}
}

// handleRuns handles /v1/runs endpoint
func (s *HTTPServer) handleRuns(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleCreateRun(w, r)
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleRunByID handles /v1/runs/{id} and related endpoints
func (s *HTTPServer) handleRunByID(w http.ResponseWriter, r *http.Request) {
	// Parse path: /v1/runs/{id} or /v1/runs/{id}:stop or /v1/runs/{id}/metrics
	path := strings.TrimPrefix(r.URL.Path, "/v1/runs/")
	if path == "" {
		s.writeError(w, http.StatusBadRequest, "run ID is required")
		return
	}

	// Check for :stop suffix
	if strings.HasSuffix(path, ":stop") {
		runID := strings.TrimSuffix(path, ":stop")
		if r.Method == http.MethodPost {
			s.handleStopRun(w, r, runID)
		} else {
			s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	// Check for /metrics suffix
	if strings.HasSuffix(path, "/metrics") {
		runID := strings.TrimSuffix(path, "/metrics")
		if r.Method == http.MethodGet {
			s.handleGetRunMetrics(w, r, runID)
		} else {
			s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	// Otherwise it's GET /v1/runs/{id}
	if r.Method == http.MethodGet {
		s.handleGetRun(w, r, path)
	} else {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleCreateRun handles POST /v1/runs
func (s *HTTPServer) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RunID string                 `json:"run_id,omitempty"`
		Input *simulationv1.RunInput `json:"input"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if req.Input == nil {
		s.writeError(w, http.StatusBadRequest, "input is required")
		return
	}

	rec, err := s.store.Create(req.RunID, req.Input)
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "already exists"):
			s.writeError(w, http.StatusConflict, err.Error())
		case strings.Contains(err.Error(), "cannot contain"):
			s.writeError(w, http.StatusBadRequest, err.Error())
		default:
			s.writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	logger.Info("run created (HTTP)", "run_id", rec.Run.Id)
	s.writeJSON(w, http.StatusCreated, map[string]any{
		"run": convertRunToJSON(rec.Run),
	})
}

// handleGetRun handles GET /v1/runs/{id}
func (s *HTTPServer) handleGetRun(w http.ResponseWriter, _ *http.Request, runID string) {
	rec, ok := s.store.Get(runID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "run not found")
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]any{
		"run": convertRunToJSON(rec.Run),
	})
}

// handleStopRun handles POST /v1/runs/{id}:stop
func (s *HTTPServer) handleStopRun(w http.ResponseWriter, _ *http.Request, runID string) {
	updated, err := s.Executor.Stop(runID)
	if err != nil {
		switch {
		case errors.Is(err, ErrRunNotFound):
			s.writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, ErrRunIDMissing):
			s.writeError(w, http.StatusBadRequest, err.Error())
		default:
			s.writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	logger.Info("run cancelled (HTTP)", "run_id", runID)
	s.writeJSON(w, http.StatusOK, map[string]any{
		"run": convertRunToJSON(updated.Run),
	})
}

// handleGetRunMetrics handles GET /v1/runs/{id}/metrics
func (s *HTTPServer) handleGetRunMetrics(w http.ResponseWriter, _ *http.Request, runID string) {
	rec, ok := s.store.Get(runID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "run not found")
		return
	}

	if rec.Metrics == nil {
		s.writeError(w, http.StatusPreconditionFailed, "metrics not available")
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]any{
		"metrics": convertMetricsToJSON(rec.Metrics),
	})
}

// Helper functions

func (s *HTTPServer) writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		logger.Error("failed to encode JSON response", "error", err)
	}
}

func (s *HTTPServer) writeError(w http.ResponseWriter, status int, message string) {
	s.writeJSON(w, status, map[string]any{
		"error": message,
	})
}

func convertRunToJSON(run *simulationv1.Run) map[string]any {
	return map[string]any{
		"id":                 run.Id,
		"status":             run.Status.String(),
		"created_at_unix_ms": run.CreatedAtUnixMs,
		"started_at_unix_ms": run.StartedAtUnixMs,
		"ended_at_unix_ms":   run.EndedAtUnixMs,
		"error":              run.Error,
	}
}

func convertMetricsToJSON(metrics *simulationv1.RunMetrics) map[string]any {
	result := map[string]any{
		"total_requests":      metrics.TotalRequests,
		"successful_requests": metrics.SuccessfulRequests,
		"failed_requests":     metrics.FailedRequests,
		"latency_p50_ms":      metrics.LatencyP50Ms,
		"latency_p95_ms":      metrics.LatencyP95Ms,
		"latency_p99_ms":      metrics.LatencyP99Ms,
		"latency_mean_ms":     metrics.LatencyMeanMs,
		"throughput_rps":      metrics.ThroughputRps,
	}

	if len(metrics.ServiceMetrics) > 0 {
		serviceMetrics := make([]map[string]any, 0, len(metrics.ServiceMetrics))
		for _, sm := range metrics.ServiceMetrics {
			serviceMetrics = append(serviceMetrics, map[string]any{
				"service_name":       sm.ServiceName,
				"request_count":      sm.RequestCount,
				"error_count":        sm.ErrorCount,
				"latency_p50_ms":     sm.LatencyP50Ms,
				"latency_p95_ms":     sm.LatencyP95Ms,
				"latency_p99_ms":     sm.LatencyP99Ms,
				"latency_mean_ms":    sm.LatencyMeanMs,
				"cpu_utilization":    sm.CpuUtilization,
				"memory_utilization": sm.MemoryUtilization,
				"active_replicas":    sm.ActiveReplicas,
			})
		}
		result["service_metrics"] = serviceMetrics
	}

	return result
}
