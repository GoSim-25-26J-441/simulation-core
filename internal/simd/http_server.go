package simd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/interaction"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/logger"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
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
	s.mux.HandleFunc("/v1/scenarios:validate", s.handleValidateScenario)

	return s
}

// handleValidateScenario handles POST /v1/scenarios:validate.
func (s *HTTPServer) handleValidateScenario(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		ScenarioYAML string `json:"scenario_yaml"`
		Mode         string `json:"mode,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = "preflight"
	}
	if mode != "preflight" {
		s.writeError(w, http.StatusBadRequest, "unsupported validation mode: "+mode+" (supported: preflight)")
		return
	}
	if strings.TrimSpace(req.ScenarioYAML) == "" {
		z := ScenarioValidationSummary{}
		s.writeJSON(w, http.StatusBadRequest, &ScenarioValidationResult{
			Valid: false,
			Errors: []ScenarioValidationIssue{{
				Code:    "SCENARIO_YAML_REQUIRED",
				Message: "scenario_yaml is required and must be non-empty after trimming whitespace",
				Path:    "scenario_yaml",
			}},
			Warnings: []ScenarioValidationIssue{},
			Summary:  &z,
		})
		return
	}

	result := ValidateScenarioPreflight(req.ScenarioYAML)
	status := scenarioValidateHTTPStatus(result)
	s.writeJSON(w, status, result)
}

func scenarioValidateHTTPStatus(result *ScenarioValidationResult) int {
	if result.Valid {
		return http.StatusOK
	}
	for _, e := range result.Errors {
		if e.Code == "SCENARIO_PARSE_INVALID" {
			return http.StatusBadRequest
		}
	}
	return http.StatusUnprocessableEntity
}

func (s *HTTPServer) Handler() http.Handler {
	return s.mux
}

// ServeMux exposes the underlying mux for compatibility.
func (s *HTTPServer) ServeMux() *http.ServeMux {
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
	case http.MethodGet:
		s.handleListRuns(w, r)
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

	// Check for /export suffix
	if strings.HasSuffix(path, "/export") {
		runID := strings.TrimSuffix(path, "/export")
		if r.Method == http.MethodGet {
			s.handleExportRun(w, r, runID)
		} else {
			s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	// Check for /metrics/stream suffix (SSE)
	if strings.HasSuffix(path, "/metrics/stream") {
		runID := strings.TrimSuffix(path, "/metrics/stream")
		if r.Method == http.MethodGet {
			s.handleMetricsStream(w, r, runID)
		} else {
			s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	// Check for /metrics/timeseries suffix
	if strings.HasSuffix(path, "/metrics/timeseries") {
		runID := strings.TrimSuffix(path, "/metrics/timeseries")
		if r.Method == http.MethodGet {
			s.handleTimeSeries(w, r, runID)
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

	// Check for /online/renew-lease suffix
	if strings.HasSuffix(path, "/online/renew-lease") {
		runID := strings.TrimSuffix(path, "/online/renew-lease")
		if r.Method == http.MethodPost {
			s.handleRenewOnlineLease(w, r, runID)
		} else {
			s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	// Check for /workload suffix
	if strings.HasSuffix(path, "/workload") {
		runID := strings.TrimSuffix(path, "/workload")
		if r.Method == http.MethodPatch {
			s.handleUpdateWorkload(w, r, runID)
		} else {
			s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	// Check for /configuration suffix
	if strings.HasSuffix(path, "/configuration") {
		runID := strings.TrimSuffix(path, "/configuration")
		switch r.Method {
		case http.MethodGet:
			s.handleGetRunConfiguration(w, r, runID)
		case http.MethodPatch:
			s.handleUpdateRunConfiguration(w, r, runID)
		default:
			s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	// Otherwise it's GET /v1/runs/{id} or POST /v1/runs/{id} (start run)
	switch r.Method {
	case http.MethodGet:
		s.handleGetRun(w, r, path)
	case http.MethodPost:
		s.handleStartRun(w, r, path)
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleCreateRun handles POST /v1/runs
func (s *HTTPServer) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	normalizedBody, err := normalizeCreateRunAliasesJSON(r.Body)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	var req struct {
		RunID string                 `json:"run_id,omitempty"`
		Input *simulationv1.RunInput `json:"input"`
	}

	if err := json.Unmarshal(normalizedBody, &req); err != nil {
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
		case errors.Is(err, ErrInvalidOnlineRunInput):
			s.writeError(w, http.StatusBadRequest, err.Error())
		case strings.Contains(err.Error(), "already exists"):
			s.writeError(w, http.StatusConflict, err.Error())
		case strings.Contains(err.Error(), "cannot contain"):
			s.writeError(w, http.StatusBadRequest, err.Error())
		default:
			s.writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}

	logger.Info("run created (HTTP)", "run_id", rec.Run.Id)
	s.writeJSON(w, http.StatusCreated, map[string]any{
		"run": convertRunToJSON(rec.Run, rec.Input),
	})
}

// handleListRuns handles GET /v1/runs with pagination and filtering
func (s *HTTPServer) handleListRuns(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	limit := 50
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
			limit = parsed
			// Cap at reasonable maximum
			if limit > 1000 {
				limit = 1000
			}
		}
	}

	offset := 0
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if parsed, err := strconv.Atoi(offsetStr); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	// Parse status filter
	var statusFilter simulationv1.RunStatus = simulationv1.RunStatus_RUN_STATUS_UNSPECIFIED
	if statusStr := r.URL.Query().Get("status"); statusStr != "" {
		statusFilter = parseRunStatus(statusStr)
	}

	// Get filtered and paginated runs
	runs := s.store.ListFiltered(limit, offset, statusFilter)

	// Convert to JSON format
	runsJSON := make([]map[string]any, 0, len(runs))
	for _, rec := range runs {
		runsJSON = append(runsJSON, convertRunToJSON(rec.Run, rec.Input))
	}

	// Return response with pagination metadata
	response := map[string]any{
		"runs": runsJSON,
		"pagination": map[string]any{
			"limit":  limit,
			"offset": offset,
			"count":  len(runs),
		},
	}

	s.writeJSON(w, http.StatusOK, response)
}

// parseRunStatus parses a status string to RunStatus enum
func parseRunStatus(statusStr string) simulationv1.RunStatus {
	switch strings.ToUpper(statusStr) {
	case "PENDING":
		return simulationv1.RunStatus_RUN_STATUS_PENDING
	case "RUNNING":
		return simulationv1.RunStatus_RUN_STATUS_RUNNING
	case "COMPLETED":
		return simulationv1.RunStatus_RUN_STATUS_COMPLETED
	case "FAILED":
		return simulationv1.RunStatus_RUN_STATUS_FAILED
	case "CANCELLED":
		return simulationv1.RunStatus_RUN_STATUS_CANCELLED
	case "STOPPED":
		return simulationv1.RunStatus_RUN_STATUS_STOPPED
	default:
		return simulationv1.RunStatus_RUN_STATUS_UNSPECIFIED
	}
}

// handleGetRun handles GET /v1/runs/{id}
func (s *HTTPServer) handleGetRun(w http.ResponseWriter, _ *http.Request, runID string) {
	rec, ok := s.store.Get(runID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "run not found")
		return
	}

	runJSON := convertRunToJSON(rec.Run, rec.Input)
	if len(rec.OptimizationHistory) > 0 {
		runJSON["optimization_history"] = convertOptimizationHistoryToJSON(rec.OptimizationHistory)
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"run": runJSON,
	})
}

// handleStartRun handles POST /v1/runs/{id}
func (s *HTTPServer) handleStartRun(w http.ResponseWriter, _ *http.Request, runID string) {
	updated, err := s.Executor.Start(runID)
	if err != nil {
		switch {
		case errors.Is(err, ErrRunNotFound):
			s.writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, ErrRunTerminal):
			s.writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, ErrRunIDMissing):
			s.writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, ErrOnlineRunConcurrencyLimit):
			s.writeError(w, http.StatusTooManyRequests, err.Error())
		default:
			s.writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	logger.Info("run started (HTTP)", "run_id", runID)
	rec, _ := s.store.Get(runID)
	s.writeJSON(w, http.StatusOK, map[string]any{
		"run": convertRunToJSON(updated.Run, rec.Input),
	})
}

// handleRenewOnlineLease handles POST /v1/runs/{id}/online/renew-lease
func (s *HTTPServer) handleRenewOnlineLease(w http.ResponseWriter, _ *http.Request, runID string) {
	rec, err := s.Executor.RenewOnlineLease(runID)
	if err != nil {
		switch {
		case errors.Is(err, ErrRunNotFound):
			s.writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, ErrRunIDMissing):
			s.writeError(w, http.StatusBadRequest, err.Error())
		default:
			s.writeError(w, http.StatusConflict, err.Error())
		}
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"run": convertRunToJSON(rec.Run, rec.Input),
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
	rec, _ := s.store.Get(runID)
	s.writeJSON(w, http.StatusOK, map[string]any{
		"run": convertRunToJSON(updated.Run, rec.Input),
	})
}

// handleUpdateWorkload handles PATCH /v1/runs/{id}/workload
func (s *HTTPServer) handleUpdateWorkload(w http.ResponseWriter, r *http.Request, runID string) {
	// Parse request body - supports two modes:
	// 1. Rate update: {"pattern_key": "client:svc1:/test", "rate_rps": 50.0}
	// 2. Pattern update: {"pattern_key": "client:svc1:/test", "pattern": {...}}
	var req struct {
		PatternKey string                      `json:"pattern_key"`
		RateRPS    *float64                    `json:"rate_rps,omitempty"`
		Pattern    *httpWorkloadPatternRequest `json:"pattern,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if req.PatternKey == "" {
		s.writeError(w, http.StatusBadRequest, "pattern_key is required")
		return
	}

	// Check if run exists and is running
	rec, ok := s.store.Get(runID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "run not found")
		return
	}

	if rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_RUNNING {
		s.writeError(w, http.StatusBadRequest, "run is not running (status: "+rec.Run.Status.String()+")")
		return
	}

	// Update rate or pattern
	var err error
	switch {
	case req.RateRPS != nil:
		// Rate update
		if *req.RateRPS <= 0 {
			s.writeError(w, http.StatusBadRequest, "rate_rps must be positive")
			return
		}
		err = s.Executor.UpdateWorkloadRate(runID, req.PatternKey, *req.RateRPS)
	case req.Pattern != nil:
		// Pattern update
		pattern, validateErr := req.Pattern.toConfigWorkloadPattern()
		if validateErr != nil {
			s.writeError(w, http.StatusBadRequest, validateErr.Error())
			return
		}
		err = s.Executor.UpdateWorkloadPattern(runID, req.PatternKey, pattern)
	default:
		s.writeError(w, http.StatusBadRequest, "either rate_rps or pattern must be provided")
		return
	}

	if err != nil {
		switch {
		case errors.Is(err, ErrRunNotFound):
			s.writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, ErrRunIDMissing):
			s.writeError(w, http.StatusBadRequest, err.Error())
		case strings.Contains(err.Error(), "workload pattern not found"):
			s.writeError(w, http.StatusNotFound, err.Error())
		case strings.Contains(err.Error(), "invalid workload target"):
			s.writeError(w, http.StatusBadRequest, err.Error())
		default:
			s.writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	logger.Info("workload updated (HTTP)", "run_id", runID, "pattern_key", req.PatternKey)
	s.writeJSON(w, http.StatusOK, map[string]any{
		"message":     "workload updated successfully",
		"run_id":      runID,
		"pattern_key": req.PatternKey,
	})
}

type httpWorkloadPatternRequest struct {
	From         string                             `json:"from"`
	SourceKind   string                             `json:"source_kind,omitempty"`
	TrafficClass string                             `json:"traffic_class,omitempty"`
	Metadata     map[string]string                  `json:"metadata,omitempty"`
	To           string                             `json:"to"`
	Arrival      *httpWorkloadArrivalSpecRequest    `json:"arrival"`
}

type httpWorkloadArrivalSpecRequest struct {
	Type                 string  `json:"type"`
	RateRPS              float64 `json:"rate_rps"`
	StdDevRPS            float64 `json:"stddev_rps,omitempty"`
	BurstRateRPS         float64 `json:"burst_rate_rps,omitempty"`
	BurstDurationSeconds float64 `json:"burst_duration_seconds,omitempty"`
	QuietDurationSeconds float64 `json:"quiet_duration_seconds,omitempty"`
}

func (p *httpWorkloadPatternRequest) toConfigWorkloadPattern() (config.WorkloadPattern, error) {
	if p == nil {
		return config.WorkloadPattern{}, fmt.Errorf("pattern is required")
	}
	if strings.TrimSpace(p.From) == "" {
		return config.WorkloadPattern{}, fmt.Errorf("pattern.from is required")
	}
	if strings.TrimSpace(p.To) == "" {
		return config.WorkloadPattern{}, fmt.Errorf("pattern.to is required")
	}
	if _, _, err := interaction.ParseDownstreamTarget(p.To); err != nil {
		return config.WorkloadPattern{}, fmt.Errorf("pattern.to is invalid: %w", err)
	}
	if p.Arrival == nil {
		return config.WorkloadPattern{}, fmt.Errorf("pattern.arrival is required")
	}
	arrivalType, err := config.NormalizeArrivalType(p.Arrival.Type)
	if err != nil {
		return config.WorkloadPattern{}, fmt.Errorf("pattern.arrival.type is invalid: %w", err)
	}
	if p.Arrival.RateRPS <= 0 {
		return config.WorkloadPattern{}, fmt.Errorf("pattern.arrival.rate_rps must be positive")
	}
	return config.WorkloadPattern{
		From:         p.From,
		SourceKind:   p.SourceKind,
		TrafficClass: p.TrafficClass,
		Metadata:     p.Metadata,
		To:           p.To,
		Arrival: config.ArrivalSpec{
			Type:                 arrivalType,
			RateRPS:              p.Arrival.RateRPS,
			StdDevRPS:            p.Arrival.StdDevRPS,
			BurstRateRPS:         p.Arrival.BurstRateRPS,
			BurstDurationSeconds: p.Arrival.BurstDurationSeconds,
			QuietDurationSeconds: p.Arrival.QuietDurationSeconds,
		},
	}, nil
}

func normalizeCreateRunAliasesJSON(bodyReader io.Reader) ([]byte, error) {
	var body map[string]any
	if err := json.NewDecoder(bodyReader).Decode(&body); err != nil {
		return nil, err
	}
	applyCreateRunOptimizationAliases(body)
	return json.Marshal(body)
}

func applyCreateRunOptimizationAliases(body map[string]any) {
	input, ok := body["input"].(map[string]any)
	if !ok || input == nil {
		return
	}
	optimization, ok := input["optimization"].(map[string]any)
	if !ok || optimization == nil {
		return
	}
	if _, hasCanonical := optimization["drain_timeout_ms"]; !hasCanonical {
		if aliasValue, hasAlias := optimization["host_drain_timeout_ms"]; hasAlias {
			optimization["drain_timeout_ms"] = aliasValue
		}
	}
	if _, hasCanonical := optimization["memory_downsize_headroom_mb"]; !hasCanonical {
		if aliasValue, hasAlias := optimization["memory_headroom_mb"]; hasAlias {
			optimization["memory_downsize_headroom_mb"] = aliasValue
		}
	}
}

// handleUpdateRunConfiguration handles PATCH /v1/runs/{id}/configuration
// Body may include services (replicas) and/or workload (rate_rps per pattern_key).
func (s *HTTPServer) handleUpdateRunConfiguration(w http.ResponseWriter, r *http.Request, runID string) {
	var req struct {
		Services []struct {
			ID       string   `json:"id"`
			Replicas int      `json:"replicas"`
			CPUCores *float64 `json:"cpu_cores,omitempty"`
			MemoryMB *float64 `json:"memory_mb,omitempty"`
		} `json:"services"`
		Workload []struct {
			PatternKey string  `json:"pattern_key"`
			RateRPS    float64 `json:"rate_rps"`
		} `json:"workload"`
		Policies *struct {
			Autoscaling *struct {
				Enabled       bool    `json:"enabled"`
				TargetCPUUtil float64 `json:"target_cpu_util"`
				ScaleStep     int     `json:"scale_step"`
			} `json:"autoscaling,omitempty"`
		} `json:"policies,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if len(req.Services) == 0 && len(req.Workload) == 0 && req.Policies == nil {
		s.writeError(w, http.StatusBadRequest, "at least one of services, workload, or policies must be provided")
		return
	}

	rec, ok := s.store.Get(runID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "run not found")
		return
	}
	if rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_RUNNING {
		s.writeError(w, http.StatusBadRequest, "run is not running (status: "+rec.Run.Status.String()+")")
		return
	}

	for _, svc := range req.Services {
		if svc.ID == "" {
			s.writeError(w, http.StatusBadRequest, "service id is required")
			return
		}
		if svc.Replicas < 1 {
			s.writeError(w, http.StatusBadRequest, "replicas must be at least 1 for service "+svc.ID)
			return
		}
		if err := s.Executor.UpdateServiceReplicas(runID, svc.ID, svc.Replicas); err != nil {
			switch {
			case errors.Is(err, ErrRunNotFound):
				s.writeError(w, http.StatusNotFound, err.Error())
			case errors.Is(err, ErrRunIDMissing):
				s.writeError(w, http.StatusBadRequest, err.Error())
			case errors.Is(err, ErrRunTerminal):
				s.writeError(w, http.StatusBadRequest, err.Error())
			default:
				s.writeError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}

		// Optional vertical scaling for this service
		if (svc.CPUCores != nil && *svc.CPUCores > 0) || (svc.MemoryMB != nil && *svc.MemoryMB > 0) {
			var cpuVal, memVal float64
			if svc.CPUCores != nil {
				cpuVal = *svc.CPUCores
			}
			if svc.MemoryMB != nil {
				memVal = *svc.MemoryMB
			}
			if err := s.Executor.UpdateServiceResources(runID, svc.ID, cpuVal, memVal); err != nil {
				switch {
				case errors.Is(err, ErrRunNotFound):
					s.writeError(w, http.StatusNotFound, err.Error())
				case errors.Is(err, ErrRunIDMissing):
					s.writeError(w, http.StatusBadRequest, err.Error())
				case errors.Is(err, ErrRunTerminal):
					s.writeError(w, http.StatusBadRequest, err.Error())
				default:
					s.writeError(w, http.StatusInternalServerError, err.Error())
				}
				return
			}
		}
	}

	for _, wl := range req.Workload {
		if wl.PatternKey == "" {
			s.writeError(w, http.StatusBadRequest, "pattern_key is required in workload entry")
			return
		}
		if wl.RateRPS <= 0 {
			s.writeError(w, http.StatusBadRequest, "rate_rps must be positive for pattern "+wl.PatternKey)
			return
		}
		if err := s.Executor.UpdateWorkloadRate(runID, wl.PatternKey, wl.RateRPS); err != nil {
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
	}

	if req.Policies != nil && req.Policies.Autoscaling != nil {
		cfg := &config.AutoscalingPolicy{
			Enabled:       req.Policies.Autoscaling.Enabled,
			TargetCPUUtil: req.Policies.Autoscaling.TargetCPUUtil,
			ScaleStep:     req.Policies.Autoscaling.ScaleStep,
		}
		if cfg.TargetCPUUtil <= 0 {
			cfg.TargetCPUUtil = 0.7
		}
		if cfg.ScaleStep <= 0 {
			cfg.ScaleStep = 1
		}
		if err := s.Executor.UpdatePolicies(runID, &config.Policies{Autoscaling: cfg}); err != nil {
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
	}

	logger.Info("run configuration updated (HTTP)", "run_id", runID)
	s.writeJSON(w, http.StatusOK, map[string]any{
		"message": "configuration updated successfully",
		"run_id":  runID,
	})
}

// handleGetRunConfiguration handles GET /v1/runs/{id}/configuration
func (s *HTTPServer) handleGetRunConfiguration(w http.ResponseWriter, _ *http.Request, runID string) {
	rec, ok := s.store.Get(runID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "run not found")
		return
	}
	if rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_RUNNING {
		s.writeError(w, http.StatusPreconditionFailed, "configuration is only available for running runs (status: "+rec.Run.Status.String()+")")
		return
	}

	cfg, ok := s.Executor.GetRunConfiguration(runID)
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "run configuration not available")
		return
	}
	var scen *config.Scenario
	if s.Executor != nil {
		scen, _ = s.Executor.GetScenarioSnapshot(runID)
	}
	if scen == nil && rec.Input != nil && rec.Input.ScenarioYaml != "" {
		if parsed, err := config.ParseScenarioYAMLString(rec.Input.ScenarioYaml); err == nil {
			scen = parsed
		}
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"configuration": convertRunConfigurationToJSON(cfg, scen),
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

	resp := map[string]any{
		"metrics": convertMetricsToJSON(rec.Metrics),
	}
	if queues, topics, ok := s.brokerShardResourcesJSON(runID); ok {
		resp["resources"] = map[string]any{
			"queues": queues,
			"topics": topics,
		}
	}
	s.writeJSON(w, http.StatusOK, resp)
}

// handleTimeSeries handles GET /v1/runs/{id}/metrics/timeseries
func (s *HTTPServer) handleTimeSeries(w http.ResponseWriter, r *http.Request, runID string) {
	// Check if run exists
	if _, ok := s.store.Get(runID); !ok {
		s.writeError(w, http.StatusNotFound, "run not found")
		return
	}

	collector, ok := s.store.GetCollector(runID)
	if !ok || collector == nil {
		s.writeError(w, http.StatusPreconditionFailed, "time-series metrics not available")
		return
	}

	// Parse query parameters
	metricName := r.URL.Query().Get("metric")
	service := r.URL.Query().Get("service")
	instance := r.URL.Query().Get("instance")
	startTimeStr := r.URL.Query().Get("start_time")
	endTimeStr := r.URL.Query().Get("end_time")

	// Parse time range
	var startTime, endTime time.Time
	var err error
	if startTimeStr != "" {
		startTime, err = parseTime(startTimeStr)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid start_time format: "+err.Error())
			return
		}
	}
	if endTimeStr != "" {
		endTime, err = parseTime(endTimeStr)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid end_time format: "+err.Error())
			return
		}
	}

	// Build labels filter
	labels := make(map[string]string)
	if service != "" {
		labels["service"] = service
	}
	if instance != "" {
		labels["instance"] = instance
	}

	// Collect time-series points
	var allPoints []*models.MetricPoint

	// Determine which metrics to query
	var metricNames []string
	if metricName != "" {
		metricNames = []string{metricName}
	} else {
		metricNames = collector.GetMetricNames()
	}

	// For each metric, get all label combinations and filter
	for _, name := range metricNames {
		labelCombos := collector.GetLabelsForMetric(name)
		if len(labelCombos) == 0 {
			// Try with empty labels (for metrics without labels)
			points := collector.GetTimeSeries(name, nil)
			allPoints = append(allPoints, points...)
			continue
		}

		// Check each label combination against our filter
		for _, labelCombo := range labelCombos {
			// Check if this label combination matches our filter
			matches := true
			if service != "" && labelCombo["service"] != service {
				matches = false
			}
			if instance != "" && labelCombo["instance"] != instance {
				matches = false
			}

			if matches {
				points := collector.GetTimeSeries(name, labelCombo)
				allPoints = append(allPoints, points...)
			}
		}
	}

	// Filter by time range if specified
	if !startTime.IsZero() || !endTime.IsZero() {
		filtered := make([]*models.MetricPoint, 0, len(allPoints))
		for _, point := range allPoints {
			if !startTime.IsZero() && point.Timestamp.Before(startTime) {
				continue
			}
			if !endTime.IsZero() && point.Timestamp.After(endTime) {
				continue
			}
			filtered = append(filtered, point)
		}
		allPoints = filtered
	}

	// For request_count and request_error_count, convert to cumulative values per (metric, labels)
	// so the response matches SSE semantics and the backend can persist without client-side summing.
	allPoints = convertTimeseriesToCumulative(allPoints)

	// Convert to JSON format
	pointsJSON := make([]map[string]any, 0, len(allPoints))
	for _, point := range allPoints {
		pointsJSON = append(pointsJSON, map[string]any{
			"timestamp": point.Timestamp.Format(time.RFC3339Nano),
			"metric":    point.Name,
			"value":     point.Value,
			"labels":    point.Labels,
		})
	}

	s.writeJSON(w, http.StatusOK, map[string]any{
		"run_id": runID,
		"points": pointsJSON,
	})
}

// handleExportRun handles GET /v1/runs/{id}/export
func (s *HTTPServer) handleExportRun(w http.ResponseWriter, _ *http.Request, runID string) {
	rec, ok := s.store.Get(runID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "run not found")
		return
	}

	runJSON := convertRunToJSON(rec.Run, rec.Input)
	if len(rec.OptimizationHistory) > 0 {
		runJSON["optimization_history"] = convertOptimizationHistoryToJSON(rec.OptimizationHistory)
	}
	// Build export data
	export := map[string]any{
		"run": runJSON,
	}
	var exportScenario *config.Scenario
	if rec.Input != nil && rec.Input.ScenarioYaml != "" {
		if s, err := config.ParseScenarioYAMLString(rec.Input.ScenarioYaml); err == nil {
			exportScenario = s
		}
	}
	if rec.FinalConfig != nil {
		export["final_config"] = convertRunConfigurationToJSON(rec.FinalConfig, exportScenario)
	} else if len(rec.OptimizationHistory) > 0 {
		lastStep := rec.OptimizationHistory[len(rec.OptimizationHistory)-1]
		if lastStep != nil && lastStep.CurrentConfig != nil {
			export["final_config"] = convertRunConfigurationToJSON(lastStep.CurrentConfig, exportScenario)
		}
	}

	// Include input/scenario configuration
	if rec.Input != nil {
		export["input"] = map[string]any{
			"scenario_yaml": rec.Input.ScenarioYaml,
			"duration_ms":   rec.Input.DurationMs,
		}
	}

	// Include aggregated metrics if available
	if rec.Metrics != nil {
		export["metrics"] = convertMetricsToJSON(rec.Metrics)
	}
	if queues, topics, ok := s.brokerShardResourcesJSON(runID); ok {
		export["resources"] = map[string]any{
			"queues": queues,
			"topics": topics,
		}
	}

	// Include time-series data if collector is available
	collector, hasCollector := s.store.GetCollector(runID)
	if hasCollector && collector != nil {
		timeSeriesData := s.exportTimeSeriesData(collector)
		if len(timeSeriesData) > 0 {
			export["time_series"] = timeSeriesData
		}
	}

	s.writeJSON(w, http.StatusOK, export)
}

// exportTimeSeriesData exports all time-series data from collector
func (s *HTTPServer) exportTimeSeriesData(collector *metrics.Collector) []map[string]any {
	metricNames := collector.GetMetricNames()
	if len(metricNames) == 0 {
		return nil
	}

	result := make([]map[string]any, 0)

	for _, metricName := range metricNames {
		labelCombos := collector.GetLabelsForMetric(metricName)
		if len(labelCombos) == 0 {
			// Try with empty labels
			points := collector.GetTimeSeries(metricName, nil)
			if len(points) > 0 {
				pointsJSON := make([]map[string]any, 0, len(points))
				for _, point := range points {
					pointsJSON = append(pointsJSON, map[string]any{
						"timestamp": point.Timestamp.Format(time.RFC3339Nano),
						"value":     point.Value,
						"labels":    point.Labels,
					})
				}
				result = append(result, map[string]any{
					"metric": metricName,
					"points": pointsJSON,
				})
			}
		} else {
			// Collect all points for this metric across all label combinations
			allPoints := make([]map[string]any, 0)
			for _, labels := range labelCombos {
				points := collector.GetTimeSeries(metricName, labels)
				for _, point := range points {
					allPoints = append(allPoints, map[string]any{
						"timestamp": point.Timestamp.Format(time.RFC3339Nano),
						"value":     point.Value,
						"labels":    point.Labels,
					})
				}
			}
			if len(allPoints) > 0 {
				result = append(result, map[string]any{
					"metric": metricName,
					"points": allPoints,
				})
			}
		}
	}

	return result
}

// parseTime parses time from ISO 8601 or Unix milliseconds
func parseTime(timeStr string) (time.Time, error) {
	// Try Unix milliseconds first
	if unixMs, err := strconv.ParseInt(timeStr, 10, 64); err == nil {
		return time.Unix(0, unixMs*int64(time.Millisecond)).UTC(), nil
	}

	// Try RFC3339 formats
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04:05",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, timeStr); err == nil {
			return t.UTC(), nil
		}
	}

	return time.Time{}, errors.New("unable to parse time format")
}

// sseWriteMetricsSnapshot sends a metrics_snapshot SSE event when persisted final metrics exist
// or (for real-time runs) live aggregates from the collector.
func (s *HTTPServer) sseWriteMetricsSnapshot(w http.ResponseWriter, runID string, rec *RunRecord, streamLive, hasCollector bool, collector *metrics.Collector) {
	var metricsToSend *simulationv1.RunMetrics
	if rec.Metrics != nil {
		metricsToSend = rec.Metrics
	} else if streamLive && hasCollector && collector != nil {
		serviceLabels := serviceLabelsFromInput(rec.Input)
		var convOpts *metrics.RunMetricsOptions
		if s.Executor != nil {
			convOpts = s.Executor.runMetricsOptsForRun(runID)
		}
		engineMetrics := metrics.ConvertToRunMetrics(collector, serviceLabels, convOpts)
		metricsToSend = convertMetricsToProto(engineMetrics)
	}
	if metricsToSend == nil {
		return
	}
	cfg, cfgOk := s.Executor.GetRunConfiguration(runID)
	if cfgOk && cfg != nil {
		for _, sm := range metricsToSend.ServiceMetrics {
			for _, svc := range cfg.Services {
				if svc.ServiceId == sm.ServiceName {
					replicas := svc.Replicas
					if replicas < 0 {
						replicas = 0
					}
					sm.ActiveReplicas = replicas
					break
				}
			}
		}
	}
	metricsJSON := convertMetricsToJSON(metricsToSend)
	payload := map[string]any{"metrics": metricsJSON}
	if cfgOk && cfg != nil {
		var scen *config.Scenario
		if s.Executor != nil {
			scen, _ = s.Executor.GetScenarioSnapshot(runID)
		}
		if scen == nil && rec.Input != nil && rec.Input.ScenarioYaml != "" {
			if parsed, err := config.ParseScenarioYAMLString(rec.Input.ScenarioYaml); err == nil {
				scen = parsed
			}
		}
		serviceResources := make([]map[string]any, 0, len(cfg.Services))
		for _, svc := range cfg.Services {
			entry := map[string]any{
				"service_id": svc.ServiceId,
				"replicas":   svc.Replicas,
				"cpu_cores":  svc.CpuCores,
				"memory_mb":  svc.MemoryMb,
			}
			enrichServiceConfigEntryJSON(entry, scen, svc.ServiceId)
			serviceResources = append(serviceResources, entry)
		}
		hostResources := make([]map[string]any, 0, len(cfg.Hosts))
		for _, h := range cfg.Hosts {
			hostResources = append(hostResources, map[string]any{
				"host_id":   h.HostId,
				"cpu_cores": h.CpuCores,
				"memory_gb": h.MemoryGb,
			})
		}
		resources := map[string]any{
			"services":   serviceResources,
			"hosts":      hostResources,
			"placements": instancePlacementsToJSON(cfg.Placements),
		}
		if queues, topics, ok := s.brokerShardResourcesJSON(runID); ok {
			resources["queues"] = queues
			resources["topics"] = topics
		}
		payload["resources"] = resources
	}
	if streamLive && hasCollector && collector != nil {
		var invHostIDs []string
		if s.Executor != nil {
			invHostIDs = s.Executor.hostIDsForRun(runID)
		}
		if hostMetrics := hostMetricsFromCollector(collector, invHostIDs); len(hostMetrics) > 0 {
			payload["host_metrics"] = hostMetrics
		}
	}
	s.sendSSEEvent(w, "metrics_snapshot", payload)
}

func (s *HTTPServer) brokerShardResourcesJSON(runID string) ([]map[string]any, []map[string]any, bool) {
	if s == nil || s.Executor == nil || runID == "" {
		return nil, nil, false
	}
	s.Executor.mu.Lock()
	rm, ok := s.Executor.resourceManagers[runID]
	s.Executor.mu.Unlock()
	if !ok || rm == nil {
		return nil, nil, false
	}
	snapshotAt := rm.LastSimTime()
	if snapshotAt.IsZero() {
		snapshotAt = time.Now()
	}
	queueSnaps := rm.QueueBrokerHealthSnapshots(snapshotAt)
	topicSnaps := rm.TopicBrokerHealthSnapshots(snapshotAt)
	queues := make([]map[string]any, 0, len(queueSnaps))
	for _, q := range queueSnaps {
		queues = append(queues, map[string]any{
			"broker_service":        q.BrokerID,
			"topic":                 q.Topic,
			"depth":                 q.Depth,
			"in_flight":             q.InFlight,
			"max_concurrency":       q.MaxConcurrency,
			"consumer_target":       q.ConsumerTarget,
			"oldest_message_age_ms": q.OldestMessageAgeMs,
			"drop_count":            q.DropCount,
			"redelivery_count":      q.RedeliveryCount,
			"dlq_count":             q.DlqCount,
		})
	}
	topics := make([]map[string]any, 0, len(topicSnaps))
	for _, t := range topicSnaps {
		topics = append(topics, map[string]any{
			"broker_service":        t.BrokerID,
			"topic":                 t.Topic,
			"partition":             t.Partition,
			"subscriber":            t.Subscriber,
			"consumer_group":        t.ConsumerGroup,
			"depth":                 t.Depth,
			"in_flight":             t.InFlight,
			"max_concurrency":       t.MaxConcurrency,
			"consumer_target":       t.ConsumerTarget,
			"oldest_message_age_ms": t.OldestMessageAgeMs,
			"drop_count":            t.DropCount,
			"redelivery_count":      t.RedeliveryCount,
			"dlq_count":             t.DlqCount,
		})
	}
	return queues, topics, true
}

// handleMetricsStream handles GET /v1/runs/{id}/metrics/stream (SSE)
func (s *HTTPServer) handleMetricsStream(w http.ResponseWriter, r *http.Request, runID string) {
	// Check if run exists
	rec, ok := s.store.Get(runID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "run not found")
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	// Disable write timeout for SSE streams (long-lived connections)
	// Use ResponseController to continuously reset write deadline
	rc := http.NewResponseController(w)
	if rc != nil {
		// Set write deadline to zero (no timeout) - needs to be reset periodically
		if err := rc.SetWriteDeadline(time.Time{}); err != nil {
			logger.Debug("SetWriteDeadline failed", "error", err)
		}
	}

	// Flush headers immediately
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	// Get collector (may be nil if simulation hasn't started)
	collector, hasCollector := s.store.GetCollector(runID)
	logger.Debug("SSE stream started", "run_id", runID, "has_collector", hasCollector, "collector_nil", collector == nil)

	// Send initial status event
	previousStatus := rec.Run.Status
	s.sendSSEEvent(w, "status_change", map[string]any{
		"status": rec.Run.Status.String(),
	})

	// Parse interval from query parameter (default: 1s)
	interval := 1 * time.Second
	if intervalStr := r.URL.Query().Get("interval_ms"); intervalStr != "" {
		if intervalMs, err := strconv.ParseInt(intervalStr, 10, 64); err == nil && intervalMs > 0 {
			interval = time.Duration(intervalMs) * time.Millisecond
		}
	}

	// Flush headers
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	// Track last sent metrics to avoid duplicates (by timestamp)
	lastSentTimestamps := make(map[string]map[string]time.Time) // metric -> labelKey -> timestamp

	// Track last sent optimization progress (for optimization runs)
	var lastOptIteration int32 = -1
	var lastOptBestScore float64 = -1

	// Track last sent optimization step count (for optimization_step SSE events)
	lastOptStepCount := 0

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Create a context that cancels when client disconnects
	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			// Client disconnected
			return
		case <-ticker.C:
			// Get current run state
			rec, ok := s.store.Get(runID)
			if !ok {
				s.sendSSEEvent(w, "error", map[string]any{
					"error": "run not found",
				})
				return
			}

			// Refresh collector check (it might become available after simulation starts)
			// The collector is created asynchronously in runSimulation(), so it might not be
			// available immediately when SSE stream connects
			if !hasCollector || collector == nil {
				collector, hasCollector = s.store.GetCollector(runID)
				if hasCollector && collector != nil {
					logger.Debug("collector now available for SSE", "run_id", runID)
				}
			}

			streamLive := rec.Input != nil && rec.Input.GetRealTimeMode()

			// Check for status changes
			if rec.Run.Status != previousStatus {
				s.sendSSEEvent(w, "status_change", map[string]any{
					"status": rec.Run.Status.String(),
				})
				previousStatus = rec.Run.Status

				// Exit if terminal status
				if rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_COMPLETED ||
					rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_FAILED ||
					rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_CANCELLED ||
					rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_STOPPED {
					// Refresh record so final metrics persisted with completion are visible before complete.
					if r2, ok2 := s.store.Get(runID); ok2 {
						rec = r2
					}
					s.sseWriteMetricsSnapshot(w, runID, rec, streamLive, hasCollector, collector)
					s.sendSSEEvent(w, "complete", map[string]any{
						"status": rec.Run.Status.String(),
					})
					if rc != nil {
						if err := rc.SetWriteDeadline(time.Time{}); err != nil {
							logger.Debug("SetWriteDeadline failed", "error", err)
						}
					}
					if flusher, ok := w.(http.Flusher); ok {
						flusher.Flush()
					}
					return
				}
			}

			// Send optimization progress for optimization runs
			if rec.Input != nil && rec.Input.Optimization != nil {
				if rec.Run.Iterations != lastOptIteration || rec.Run.BestScore != lastOptBestScore {
					lastOptIteration = rec.Run.Iterations
					lastOptBestScore = rec.Run.BestScore
					opt := rec.Input.Optimization
					objective, unit := ObjectiveAndUnitForProgress(opt)
					s.sendSSEEvent(w, "optimization_progress", map[string]any{
						"iteration":   rec.Run.Iterations,
						"best_score":  rec.Run.BestScore,
						"best_run_id": rec.Run.BestRunId,
						"objective":   objective,
						"unit":        unit,
					})
				}
				// Send new optimization steps (online controller config changes)
				stepCount := len(rec.OptimizationHistory)
				if stepCount > lastOptStepCount {
					for i := lastOptStepCount; i < stepCount; i++ {
						step := rec.OptimizationHistory[i]
						if step != nil {
							s.sendSSEEvent(w, "optimization_step", convertOptimizationStepToJSON(step))
						}
					}
					lastOptStepCount = stepCount
				}
			}

			// Send aggregated metrics snapshot: final metrics from store when persisted; during run, live
			// collector snapshots only for real-time runs (non-real-time runs can take wall-clock time to
			// finish simulation work without throttling — partial metrics would be misleading as "live").
			s.sseWriteMetricsSnapshot(w, runID, rec, streamLive, hasCollector, collector)

			// Send time-series metric updates if collector is available (real-time runs only)
			if streamLive && hasCollector && collector != nil {
				// Get all metrics and send new/updated points
				metricNames := collector.GetMetricNames()
				if len(metricNames) == 0 {
					logger.Debug("no metrics available yet", "run_id", runID)
				}
				for _, metricName := range metricNames {
					labelCombos := collector.GetLabelsForMetric(metricName)
					if len(labelCombos) == 0 {
						// Try with empty labels
						points := collector.GetTimeSeries(metricName, nil)
						if len(points) > 0 {
							latest := points[len(points)-1]
							labelKey := ""
							if lastSentTimestamps[metricName] == nil {
								lastSentTimestamps[metricName] = make(map[string]time.Time)
							}
							if lastTs, exists := lastSentTimestamps[metricName][labelKey]; !exists || !latest.Timestamp.Equal(lastTs) {
								value := metricUpdateValue(metricName, points, latest)
								s.sendSSEEvent(w, "metric_update", map[string]any{
									"timestamp": latest.Timestamp.Format(time.RFC3339Nano),
									"metric":    latest.Name,
									"value":     value,
									"labels":    latest.Labels,
								})
								lastSentTimestamps[metricName][labelKey] = latest.Timestamp
							}
						}
					} else {
						for _, labels := range labelCombos {
							points := collector.GetTimeSeries(metricName, labels)
							if len(points) > 0 {
								latest := points[len(points)-1]
								labelKey := createLabelKey(labels)
								if lastSentTimestamps[metricName] == nil {
									lastSentTimestamps[metricName] = make(map[string]time.Time)
								}
								if lastTs, exists := lastSentTimestamps[metricName][labelKey]; !exists || !latest.Timestamp.Equal(lastTs) {
									value := metricUpdateValue(metricName, points, latest)
									s.sendSSEEvent(w, "metric_update", map[string]any{
										"timestamp": latest.Timestamp.Format(time.RFC3339Nano),
										"metric":    latest.Name,
										"value":     value,
										"labels":    latest.Labels,
									})
									lastSentTimestamps[metricName][labelKey] = latest.Timestamp
								}
							}
						}
					}
				}
			}

			// Reset write deadline before each flush to prevent timeouts
			// This is critical for SSE streams with many events
			if rc != nil {
				if err := rc.SetWriteDeadline(time.Time{}); err != nil {
					logger.Debug("SetWriteDeadline failed", "error", err)
				}
			}
			// Flush to send data immediately
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}
}

// sendSSEEvent sends a Server-Sent Event
func (s *HTTPServer) sendSSEEvent(w http.ResponseWriter, eventType string, data map[string]any) {
	// Format: event: <type>\ndata: <json>\n\n
	jsonData, err := json.Marshal(data)
	if err != nil {
		logger.Error("failed to marshal SSE event data", "error", err)
		return
	}

	// Reset write deadline before EVERY write to prevent timeouts
	// This is critical when sending many events rapidly
	if rc := http.NewResponseController(w); rc != nil {
		if err := rc.SetWriteDeadline(time.Time{}); err != nil {
			logger.Debug("SetWriteDeadline failed", "error", err)
		}
	}

	// Write event in SSE format
	// Note: Errors are logged but not returned as SSE streams are best-effort
	if _, err := w.Write([]byte("event: " + eventType + "\n")); err != nil {
		// Check if it's a timeout error - downgrade to warning if so
		if errStr := err.Error(); strings.Contains(errStr, "timeout") || strings.Contains(errStr, "i/o timeout") {
			logger.Warn("SSE write timeout (client may be slow or connection lost)", "error", err)
		} else {
			logger.Error("failed to write SSE event header", "error", err)
		}
		return
	}

	// Reset deadline again before writing data
	if rc := http.NewResponseController(w); rc != nil {
		if err := rc.SetWriteDeadline(time.Time{}); err != nil {
			logger.Debug("SetWriteDeadline failed", "error", err)
		}
	}

	if _, err := w.Write([]byte("data: " + string(jsonData) + "\n\n")); err != nil {
		// Check if it's a timeout error - downgrade to warning if so
		if errStr := err.Error(); strings.Contains(errStr, "timeout") || strings.Contains(errStr, "i/o timeout") {
			logger.Warn("SSE write timeout (client may be slow or connection lost)", "error", err)
		} else {
			logger.Error("failed to write SSE event data", "error", err)
		}
		return
	}
}

// createLabelKey creates a key from labels for tracking
func createLabelKey(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}

	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	key := ""
	for _, k := range keys {
		key += k + "=" + labels[k] + ","
	}
	return key
}

// convertTimeseriesToCumulative returns a new slice of points where request_count and
// request_error_count are converted to cumulative values per (metric, labels), matching
// SSE metric_update semantics. Other metrics are returned unchanged.
func convertTimeseriesToCumulative(points []*models.MetricPoint) []*models.MetricPoint {
	if len(points) == 0 {
		return points
	}
	type key struct {
		name string
		lb   string
	}
	groups := make(map[key][]*models.MetricPoint)
	for _, p := range points {
		k := key{name: p.Name, lb: createLabelKey(p.Labels)}
		groups[k] = append(groups[k], p)
	}

	var out []*models.MetricPoint
	for k, group := range groups {
		if k.name != metrics.MetricRequestCount && k.name != metrics.MetricRequestErrorCount {
			out = append(out, group...)
			continue
		}
		sorted := make([]*models.MetricPoint, len(group))
		copy(sorted, group)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].Timestamp.Before(sorted[j].Timestamp) })
		var sum float64
		for _, p := range sorted {
			sum += p.Value
			out = append(out, &models.MetricPoint{
				Timestamp: p.Timestamp,
				Name:      p.Name,
				Value:     sum,
				Labels:    p.Labels,
			})
		}
	}
	return out
}

// metricUpdateValue returns the value to send in a metric_update event.
// For request_count and request_error_count we send cumulative sum so the frontend can plot (timestamp, total) directly.
// For other metrics we send the latest point value (gauges / per-observation).
func metricUpdateValue(metricName string, points []*models.MetricPoint, latest *models.MetricPoint) float64 {
	if metricName == metrics.MetricRequestCount || metricName == metrics.MetricRequestErrorCount {
		sum := 0.0
		for _, p := range points {
			sum += p.Value
		}
		return sum
	}
	return latest.Value
}

// serviceLabelsFromInput parses scenario YAML from run input and returns service label maps for aggregation.
// Returns nil if input is nil, scenario is missing, or parsing fails.
func serviceLabelsFromInput(input *simulationv1.RunInput) []map[string]string {
	if input == nil || input.ScenarioYaml == "" {
		return nil
	}
	scenario, err := config.ParseScenarioYAMLString(input.ScenarioYaml)
	if err != nil {
		return nil
	}
	if len(scenario.Services) == 0 {
		return nil
	}
	out := make([]map[string]string, 0, len(scenario.Services))
	for _, svc := range scenario.Services {
		out = append(out, metrics.CreateServiceLabels(svc.ID))
	}
	return out
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

func convertRunToJSON(run *simulationv1.Run, input *simulationv1.RunInput) map[string]any {
	result := map[string]any{
		"id":                 run.Id,
		"status":             run.Status.String(),
		"created_at_unix_ms": run.CreatedAtUnixMs,
		"started_at_unix_ms": run.StartedAtUnixMs,
		"ended_at_unix_ms":   run.EndedAtUnixMs,
		"error":              run.Error,
	}

	// Calculate real-world duration (wall-clock time)
	if run.StartedAtUnixMs > 0 && run.EndedAtUnixMs > 0 {
		realDurationMs := run.EndedAtUnixMs - run.StartedAtUnixMs
		result["real_duration_ms"] = realDurationMs
		result["real_duration_seconds"] = float64(realDurationMs) / 1000.0
	}

	// Include simulation duration from input (this is the actual simulation time)
	if input != nil && input.DurationMs > 0 {
		result["simulation_duration_ms"] = input.DurationMs
		result["simulation_duration_seconds"] = float64(input.DurationMs) / 1000.0
	}

	// Optimization run results
	if run.BestRunId != "" {
		result["best_run_id"] = run.BestRunId
		result["best_score"] = run.BestScore
		result["iterations"] = run.Iterations
	}
	if len(run.CandidateRunIds) > 0 {
		result["candidate_run_ids"] = run.CandidateRunIds
	}
	if run.OnlineCompletionReason != "" {
		result["online_completion_reason"] = run.OnlineCompletionReason
	}
	if run.GetBatchRecommendationSummary() != "" || run.GetBatchRecommendationFeasible() ||
		run.GetBatchViolationScore() != 0 || run.GetBatchEfficiencyScore() != 0 {
		result["batch_recommendation_feasible"] = run.GetBatchRecommendationFeasible()
		result["batch_violation_score"] = run.GetBatchViolationScore()
		result["batch_efficiency_score"] = run.GetBatchEfficiencyScore()
		result["batch_recommendation_summary"] = run.GetBatchRecommendationSummary()
		result["batch_score_breakdown"] = map[string]any{
			"feasible":         run.GetBatchRecommendationFeasible(),
			"violation_score":  run.GetBatchViolationScore(),
			"efficiency_score": run.GetBatchEfficiencyScore(),
			"summary":          run.GetBatchRecommendationSummary(),
		}
	}

	return result
}

func scalingPolicyToJSON(p *config.ScalingPolicy) map[string]any {
	if p == nil {
		return nil
	}
	return map[string]any{
		"horizontal":      p.Horizontal,
		"vertical_cpu":    p.VerticalCPU,
		"vertical_memory": p.VerticalMemory,
	}
}

func enrichServiceConfigEntryJSON(m map[string]any, scenario *config.Scenario, serviceID string) {
	if scenario == nil || serviceID == "" {
		return
	}
	for i := range scenario.Services {
		if scenario.Services[i].ID != serviceID {
			continue
		}
		svc := &scenario.Services[i]
		if svc.Kind != "" {
			m["kind"] = svc.Kind
		}
		if svc.Role != "" {
			m["role"] = svc.Role
		}
		if svc.Model != "" {
			m["model"] = svc.Model
		}
		if sp := scalingPolicyToJSON(svc.Scaling); sp != nil {
			m["scaling"] = sp
		}
		return
	}
}

func convertRunConfigurationToJSON(cfg *simulationv1.RunConfiguration, scenario *config.Scenario) map[string]any {
	if cfg == nil {
		return nil
	}
	services := make([]map[string]any, 0, len(cfg.Services))
	for _, srv := range cfg.Services {
		entry := map[string]any{
			"service_id": srv.ServiceId,
			"replicas":   srv.Replicas,
			"cpu_cores":  srv.CpuCores,
			"memory_mb":  srv.MemoryMb,
		}
		enrichServiceConfigEntryJSON(entry, scenario, srv.ServiceId)
		services = append(services, entry)
	}
	workload := make([]map[string]any, 0, len(cfg.Workload))
	for _, w := range cfg.Workload {
		workload = append(workload, map[string]any{
			"pattern_key": w.PatternKey,
			"rate_rps":    w.RateRps,
		})
	}
	hosts := make([]map[string]any, 0, len(cfg.Hosts))
	for _, h := range cfg.Hosts {
		hosts = append(hosts, map[string]any{
			"host_id":   h.HostId,
			"cpu_cores": h.CpuCores,
			"memory_gb": h.MemoryGb,
		})
	}
	placements := instancePlacementsToJSON(cfg.Placements)
	return map[string]any{
		"services":   services,
		"workload":   workload,
		"hosts":      hosts,
		"placements": placements,
	}
}

func instancePlacementsToJSON(entries []*simulationv1.InstancePlacementEntry) []map[string]any {
	if len(entries) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(entries))
	for _, p := range entries {
		if p == nil {
			continue
		}
		out = append(out, map[string]any{
			"instance_id":        p.InstanceId,
			"service_id":         p.ServiceId,
			"host_id":            p.HostId,
			"lifecycle":          p.Lifecycle,
			"cpu_cores":          p.CpuCores,
			"memory_mb":          p.MemoryMb,
			"cpu_utilization":    p.CpuUtilization,
			"memory_utilization": p.MemoryUtilization,
			"active_requests":    p.ActiveRequests,
			"queue_length":       p.QueueLength,
		})
	}
	return out
}

func convertOptimizationStepToJSON(step *simulationv1.OptimizationStep) map[string]any {
	if step == nil {
		return nil
	}
	result := map[string]any{
		"iteration_index": step.IterationIndex,
		"target_p95_ms":   step.TargetP95Ms,
		"score_p95_ms":    step.ScoreP95Ms,
		"reason":          step.Reason,
	}
	if step.PreviousConfig != nil {
		result["previous_config"] = convertRunConfigurationToJSON(step.PreviousConfig, nil)
	}
	if step.CurrentConfig != nil {
		result["current_config"] = convertRunConfigurationToJSON(step.CurrentConfig, nil)
	}
	if details := parseOptimizationReasonDetails(step.Reason); len(details) > 0 {
		result["reason_details"] = details
	}
	return result
}

func parseOptimizationReasonDetails(reason string) map[string]any {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil
	}
	parts := strings.Fields(reason)
	if len(parts) == 0 {
		return nil
	}
	out := map[string]any{"type": parts[0]}
	for _, p := range parts[1:] {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) != 2 {
			continue
		}
		k, v := kv[0], kv[1]
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			out[k] = f
			continue
		}
		out[k] = v
	}
	return out
}

func convertOptimizationHistoryToJSON(history []*simulationv1.OptimizationStep) []map[string]any {
	if len(history) == 0 {
		return nil
	}
	result := make([]map[string]any, 0, len(history))
	for _, step := range history {
		if step != nil {
			result = append(result, convertOptimizationStepToJSON(step))
		}
	}
	return result
}

// hostMetricsFromCollector builds a list of per-host metrics from the collector's
// host-labelled time series (cpu_utilization, memory_utilization).
// When inventoryHostIDs is non-nil, the list is exactly those IDs (resource-manager order);
// hosts with no samples yet appear with cpu/memory utilization 0.
// When inventoryHostIDs is nil, only hosts that have emitted host-labelled samples are included.
func hostMetricsFromCollector(collector *metrics.Collector, inventoryHostIDs []string) []map[string]any {
	type hostVals struct {
		cpuUtil float64
		memUtil float64
	}
	byHost := make(map[string]*hostVals)

	for _, metricName := range []string{"cpu_utilization", "memory_utilization"} {
		labelCombos := collector.GetLabelsForMetric(metricName)
		for _, labels := range labelCombos {
			hostID, ok := labels["host"]
			if !ok || hostID == "" {
				continue
			}
			points := collector.GetTimeSeries(metricName, labels)
			if len(points) == 0 {
				continue
			}
			latest := points[len(points)-1].Value
			if byHost[hostID] == nil {
				byHost[hostID] = &hostVals{}
			}
			switch metricName {
			case "cpu_utilization":
				byHost[hostID].cpuUtil = latest
			case "memory_utilization":
				byHost[hostID].memUtil = latest
			}
		}
	}

	if len(inventoryHostIDs) > 0 {
		out := make([]map[string]any, 0, len(inventoryHostIDs))
		for _, hostID := range inventoryHostIDs {
			v := byHost[hostID]
			cpu, mem := 0.0, 0.0
			if v != nil {
				cpu, mem = v.cpuUtil, v.memUtil
			}
			out = append(out, map[string]any{
				"host_id":            hostID,
				"cpu_utilization":    cpu,
				"memory_utilization": mem,
			})
		}
		return out
	}

	if len(byHost) == 0 {
		return nil
	}
	hostIDs := make([]string, 0, len(byHost))
	for h := range byHost {
		hostIDs = append(hostIDs, h)
	}
	sort.Strings(hostIDs)
	out := make([]map[string]any, 0, len(hostIDs))
	for _, hostID := range hostIDs {
		v := byHost[hostID]
		out = append(out, map[string]any{
			"host_id":            hostID,
			"cpu_utilization":    v.cpuUtil,
			"memory_utilization": v.memUtil,
		})
	}
	return out
}

func convertMetricsToJSON(metrics *simulationv1.RunMetrics) map[string]any {
	result := map[string]any{
		"total_requests":                      metrics.TotalRequests,
		"successful_requests":                 metrics.SuccessfulRequests,
		"failed_requests":                     metrics.FailedRequests,
		"latency_p50_ms":                      metrics.LatencyP50Ms,
		"latency_p95_ms":                      metrics.LatencyP95Ms,
		"latency_p99_ms":                      metrics.LatencyP99Ms,
		"latency_mean_ms":                     metrics.LatencyMeanMs,
		"throughput_rps":                      metrics.ThroughputRps,
		"ingress_requests":                    metrics.IngressRequests,
		"internal_requests":                   metrics.InternalRequests,
		"ingress_throughput_rps":              metrics.IngressThroughputRps,
		"ingress_failed_requests":             metrics.IngressFailedRequests,
		"ingress_error_rate":                  metrics.IngressErrorRate,
		"attempt_failed_requests":             metrics.AttemptFailedRequests,
		"attempt_error_rate":                  metrics.AttemptErrorRate,
		"retry_attempts":                      metrics.RetryAttempts,
		"timeout_errors":                      metrics.TimeoutErrors,
		"queue_enqueue_count_total":           metrics.QueueEnqueueCountTotal,
		"queue_dequeue_count_total":           metrics.QueueDequeueCountTotal,
		"queue_drop_count_total":              metrics.QueueDropCountTotal,
		"queue_redelivery_count_total":        metrics.QueueRedeliveryCountTotal,
		"queue_dlq_count_total":               metrics.QueueDlqCountTotal,
		"queue_depth_sum":                     metrics.QueueDepthSum,
		"topic_publish_count_total":           metrics.TopicPublishCountTotal,
		"topic_deliver_count_total":           metrics.TopicDeliverCountTotal,
		"topic_drop_count_total":              metrics.TopicDropCountTotal,
		"topic_redelivery_count_total":        metrics.TopicRedeliveryCountTotal,
		"topic_dlq_count_total":               metrics.TopicDlqCountTotal,
		"topic_backlog_depth_sum":             metrics.TopicBacklogDepthSum,
		"topic_consumer_lag_sum":              metrics.TopicConsumerLagSum,
		"queue_oldest_message_age_ms":         metrics.QueueOldestMessageAgeMs,
		"topic_oldest_message_age_ms":         metrics.TopicOldestMessageAgeMs,
		"max_queue_depth":                     metrics.MaxQueueDepth,
		"max_topic_backlog_depth":             metrics.MaxTopicBacklogDepth,
		"max_topic_consumer_lag":              metrics.MaxTopicConsumerLag,
		"queue_drop_rate":                     metrics.QueueDropRate,
		"topic_drop_rate":                     metrics.TopicDropRate,
		"locality_hit_rate":                   metrics.LocalityHitRate,
		"cross_zone_request_count_total":      metrics.CrossZoneRequestCountTotal,
		"same_zone_request_count_total":       metrics.SameZoneRequestCountTotal,
		"cross_zone_request_fraction":         metrics.CrossZoneRequestFraction,
		"cross_zone_latency_penalty_ms_total": metrics.CrossZoneLatencyPenaltyMsTotal,
		"cross_zone_latency_penalty_ms_mean":  metrics.CrossZoneLatencyPenaltyMsMean,
		"same_zone_latency_penalty_ms_total":  metrics.SameZoneLatencyPenaltyMsTotal,
		"same_zone_latency_penalty_ms_mean":   metrics.SameZoneLatencyPenaltyMsMean,
		"external_latency_ms_total":           metrics.ExternalLatencyMsTotal,
		"external_latency_ms_mean":            metrics.ExternalLatencyMsMean,
		"topology_latency_penalty_ms_total":   metrics.TopologyLatencyPenaltyMsTotal,
		"topology_latency_penalty_ms_mean":    metrics.TopologyLatencyPenaltyMsMean,
	}

	if len(metrics.ServiceMetrics) > 0 {
		serviceMetrics := make([]map[string]any, 0, len(metrics.ServiceMetrics))
		for _, sm := range metrics.ServiceMetrics {
			serviceMetrics = append(serviceMetrics, map[string]any{
				"service_name":               sm.ServiceName,
				"request_count":              sm.RequestCount,
				"error_count":                sm.ErrorCount,
				"latency_p50_ms":             sm.LatencyP50Ms,
				"latency_p95_ms":             sm.LatencyP95Ms,
				"latency_p99_ms":             sm.LatencyP99Ms,
				"latency_mean_ms":            sm.LatencyMeanMs,
				"cpu_utilization":            sm.CpuUtilization,
				"memory_utilization":         sm.MemoryUtilization,
				"active_replicas":            sm.ActiveReplicas,
				"concurrent_requests":        sm.ConcurrentRequests,
				"queue_length":               sm.QueueLength,
				"queue_wait_p50_ms":          sm.QueueWaitP50Ms,
				"queue_wait_p95_ms":          sm.QueueWaitP95Ms,
				"queue_wait_p99_ms":          sm.QueueWaitP99Ms,
				"queue_wait_mean_ms":         sm.QueueWaitMeanMs,
				"processing_latency_p50_ms":  sm.ProcessingLatencyP50Ms,
				"processing_latency_p95_ms":  sm.ProcessingLatencyP95Ms,
				"processing_latency_p99_ms":  sm.ProcessingLatencyP99Ms,
				"processing_latency_mean_ms": sm.ProcessingLatencyMeanMs,
			})
		}
		result["service_metrics"] = serviceMetrics
	}

	if len(metrics.HostMetrics) > 0 {
		hm := make([]map[string]any, 0, len(metrics.HostMetrics))
		for _, h := range metrics.HostMetrics {
			if h == nil {
				continue
			}
			hm = append(hm, map[string]any{
				"host_id":            h.HostId,
				"cpu_utilization":    h.CpuUtilization,
				"memory_utilization": h.MemoryUtilization,
			})
		}
		if len(hm) > 0 {
			result["host_metrics"] = hm
		}
	}

	if len(metrics.EndpointRequestStats) > 0 {
		endpointStats := make([]map[string]any, 0, len(metrics.EndpointRequestStats))
		for _, es := range metrics.EndpointRequestStats {
			if es == nil {
				continue
			}
			row := map[string]any{
				"service_name":  es.ServiceName,
				"endpoint_path": es.EndpointPath,
				"request_count": es.RequestCount,
				"error_count":   es.ErrorCount,
			}
			if es.LatencyP50Ms != nil {
				row["latency_p50_ms"] = es.GetLatencyP50Ms()
			}
			if es.LatencyP95Ms != nil {
				row["latency_p95_ms"] = es.GetLatencyP95Ms()
			}
			if es.LatencyP99Ms != nil {
				row["latency_p99_ms"] = es.GetLatencyP99Ms()
			}
			if es.LatencyMeanMs != nil {
				row["latency_mean_ms"] = es.GetLatencyMeanMs()
			}
			if es.RootLatencyP50Ms != nil {
				row["root_latency_p50_ms"] = es.GetRootLatencyP50Ms()
			}
			if es.RootLatencyP95Ms != nil {
				row["root_latency_p95_ms"] = es.GetRootLatencyP95Ms()
			}
			if es.RootLatencyP99Ms != nil {
				row["root_latency_p99_ms"] = es.GetRootLatencyP99Ms()
			}
			if es.RootLatencyMeanMs != nil {
				row["root_latency_mean_ms"] = es.GetRootLatencyMeanMs()
			}
			if es.QueueWaitP50Ms != nil {
				row["queue_wait_p50_ms"] = es.GetQueueWaitP50Ms()
			}
			if es.QueueWaitP95Ms != nil {
				row["queue_wait_p95_ms"] = es.GetQueueWaitP95Ms()
			}
			if es.QueueWaitP99Ms != nil {
				row["queue_wait_p99_ms"] = es.GetQueueWaitP99Ms()
			}
			if es.QueueWaitMeanMs != nil {
				row["queue_wait_mean_ms"] = es.GetQueueWaitMeanMs()
			}
			if es.ProcessingLatencyP50Ms != nil {
				row["processing_latency_p50_ms"] = es.GetProcessingLatencyP50Ms()
			}
			if es.ProcessingLatencyP95Ms != nil {
				row["processing_latency_p95_ms"] = es.GetProcessingLatencyP95Ms()
			}
			if es.ProcessingLatencyP99Ms != nil {
				row["processing_latency_p99_ms"] = es.GetProcessingLatencyP99Ms()
			}
			if es.ProcessingLatencyMeanMs != nil {
				row["processing_latency_mean_ms"] = es.GetProcessingLatencyMeanMs()
			}
			endpointStats = append(endpointStats, row)
		}
		if len(endpointStats) > 0 {
			result["endpoint_request_stats"] = endpointStats
		}
	}

	if len(metrics.InstanceRouteStats) > 0 {
		routeStats := make([]map[string]any, 0, len(metrics.InstanceRouteStats))
		for _, rs := range metrics.InstanceRouteStats {
			if rs == nil {
				continue
			}
			routeStats = append(routeStats, map[string]any{
				"service_name":    rs.ServiceName,
				"endpoint_path":   rs.EndpointPath,
				"instance_id":     rs.InstanceId,
				"strategy":        rs.Strategy,
				"selection_count": rs.SelectionCount,
			})
		}
		if len(routeStats) > 0 {
			result["instance_route_stats"] = routeStats
		}
	}

	return result
}
