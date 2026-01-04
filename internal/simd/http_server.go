package simd

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
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
		runsJSON = append(runsJSON, convertRunToJSON(rec.Run))
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

	// Build export data
	export := map[string]any{
		"run": convertRunToJSON(rec.Run),
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

	// Get collector (may be nil if simulation hasn't started)
	collector, hasCollector := s.store.GetCollector(runID)

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

			// Check for status changes
			if rec.Run.Status != previousStatus {
				s.sendSSEEvent(w, "status_change", map[string]any{
					"status": rec.Run.Status.String(),
				})
				previousStatus = rec.Run.Status

				// Exit if terminal status
				if rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_COMPLETED ||
					rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_FAILED ||
					rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_CANCELLED {
					s.sendSSEEvent(w, "complete", map[string]any{
						"status": rec.Run.Status.String(),
					})
					return
				}
			}

			// Send aggregated metrics snapshot if available
			if rec.Metrics != nil {
				metricsJSON := convertMetricsToJSON(rec.Metrics)
				s.sendSSEEvent(w, "metrics_snapshot", map[string]any{
					"metrics": metricsJSON,
				})
			}

			// Send time-series metric updates if collector is available
			if hasCollector && collector != nil {
				// Get all metrics and send new/updated points
				metricNames := collector.GetMetricNames()
				for _, metricName := range metricNames {
					labelCombos := collector.GetLabelsForMetric(metricName)
					if len(labelCombos) == 0 {
						// Try with empty labels
						points := collector.GetTimeSeries(metricName, nil)
						if len(points) > 0 {
							// Send latest point
							latest := points[len(points)-1]
							labelKey := ""
							if lastSentTimestamps[metricName] == nil {
								lastSentTimestamps[metricName] = make(map[string]time.Time)
							}
							if lastTs, exists := lastSentTimestamps[metricName][labelKey]; !exists || !latest.Timestamp.Equal(lastTs) {
								s.sendSSEEvent(w, "metric_update", map[string]any{
									"timestamp": latest.Timestamp.Format(time.RFC3339Nano),
									"metric":    latest.Name,
									"value":     latest.Value,
									"labels":    latest.Labels,
								})
								lastSentTimestamps[metricName][labelKey] = latest.Timestamp
							}
						}
					} else {
						// Check each label combination
						for _, labels := range labelCombos {
							points := collector.GetTimeSeries(metricName, labels)
							if len(points) > 0 {
								latest := points[len(points)-1]
								// Create label key for tracking
								labelKey := createLabelKey(labels)
								if lastSentTimestamps[metricName] == nil {
									lastSentTimestamps[metricName] = make(map[string]time.Time)
								}
								if lastTs, exists := lastSentTimestamps[metricName][labelKey]; !exists || !latest.Timestamp.Equal(lastTs) {
									s.sendSSEEvent(w, "metric_update", map[string]any{
										"timestamp": latest.Timestamp.Format(time.RFC3339Nano),
										"metric":    latest.Name,
										"value":     latest.Value,
										"labels":    latest.Labels,
									})
									lastSentTimestamps[metricName][labelKey] = latest.Timestamp
								}
							}
						}
					}
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

	// Write event in SSE format
	// Note: Errors are logged but not returned as SSE streams are best-effort
	if _, err := w.Write([]byte("event: " + eventType + "\n")); err != nil {
		logger.Error("failed to write SSE event header", "error", err)
		return
	}
	if _, err := w.Write([]byte("data: " + string(jsonData) + "\n\n")); err != nil {
		logger.Error("failed to write SSE event data", "error", err)
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
