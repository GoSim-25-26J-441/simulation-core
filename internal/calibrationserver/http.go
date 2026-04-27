// Package calibrationserver registers HTTP routes for calibration and validation without importing internal/simd,
// avoiding import cycles with internal/calibration.
package calibrationserver

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/GoSim-25-26J-441/simulation-core/internal/calibration"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/logger"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

// Register adds POST /v1/calibrate and POST /v1/validate to mux.
func Register(mux *http.ServeMux) {
	mux.HandleFunc("/v1/calibrate", handleCalibrate)
	mux.HandleFunc("/v1/validate", handleValidate)
}

type calibrateHTTPRequest struct {
	ScenarioYAML   string          `json:"scenario_yaml"`
	ObservedFormat string          `json:"observed_format"`
	Observed       json.RawMessage `json:"observed"`
	PredictedRun   json.RawMessage `json:"predicted_run,omitempty"`
	// SimDurationMs and Seeds are used when predicted_run is absent and auto_predict is true (default):
	// the server runs a baseline simulation per seed and merges metrics conservatively for ratio calibration.
	SimDurationMs int64   `json:"sim_duration_ms"`
	Seeds         []int64 `json:"seeds,omitempty"`
	// AutoPredict defaults to true when predicted_run is omitted. Set false to calibrate without a baseline run
	// (scenario-only heuristics; lower confidence for throughput ratios).
	AutoPredict      *bool `json:"auto_predict,omitempty"`
	CalibrateOptions *struct {
		Overwrite       string  `json:"overwrite,omitempty"`
		ConfidenceFloor float64 `json:"confidence_floor,omitempty"`
		MinScaleFactor  float64 `json:"min_scale_factor,omitempty"`
		MaxScaleFactor  float64 `json:"max_scale_factor,omitempty"`
	} `json:"calibrate_options,omitempty"`
}

type validateHTTPRequest struct {
	ScenarioYAML    string          `json:"scenario_yaml"`
	ObservedFormat  string          `json:"observed_format"`
	Observed        json.RawMessage `json:"observed"`
	SimDurationMs   int64           `json:"sim_duration_ms"`
	Seeds           []int64         `json:"seeds,omitempty"`
	ValidateOptions *struct {
		RealTimeWorkload bool `json:"real_time_workload,omitempty"`
		// ToleranceProfile: default | strict | loose (defaults to default).
		ToleranceProfile string          `json:"tolerance_profile,omitempty"`
		Tolerances       json.RawMessage `json:"tolerances,omitempty"` // partial overrides, see calibration.ApplyToleranceJSON
	} `json:"validate_options,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		logger.Error("failed to encode JSON response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}

func handleCalibrate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body failed")
		return
	}
	var req calibrateHTTPRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.ScenarioYAML) == "" {
		writeError(w, http.StatusBadRequest, "scenario_yaml is required")
		return
	}
	if len(req.Observed) == 0 {
		writeError(w, http.StatusBadRequest, "observed is required")
		return
	}
	scenario, err := config.ParseScenarioYAMLString(req.ScenarioYAML)
	if err != nil {
		writeError(w, http.StatusBadRequest, "scenario_yaml: "+err.Error())
		return
	}
	obs, err := calibration.DecodeObservedMetrics(req.ObservedFormat, req.Observed)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	opts := calibrateOptionsFromJSON(req.CalibrateOptions)
	var autoPredictNote string
	auto := true
	if req.AutoPredict != nil {
		auto = *req.AutoPredict
	}
	if len(req.PredictedRun) > 0 {
		var rm models.RunMetrics
		if err := json.Unmarshal(req.PredictedRun, &rm); err == nil &&
			(rm.TotalRequests > 0 || rm.ServiceMetrics != nil || len(rm.EndpointRequestStats) > 0) {
			opts.PredictedRun = &rm
		} else {
			var env struct {
				RunMetrics *models.RunMetrics `json:"run_metrics"`
			}
			if err := json.Unmarshal(req.PredictedRun, &env); err == nil && env.RunMetrics != nil {
				opts.PredictedRun = env.RunMetrics
			}
		}
	} else if auto {
		simMs := req.SimDurationMs
		if simMs <= 0 {
			simMs = 10_000
		}
		seeds := req.Seeds
		if len(seeds) == 0 {
			seeds = []int64{1, 2, 3}
		}
		pred, err := calibration.RunBaselinePredictedRun(scenario, simMs, seeds, false)
		if err != nil {
			writeError(w, http.StatusBadRequest, "auto baseline prediction: "+err.Error())
			return
		}
		opts.PredictedRun = pred
		autoPredictNote = "baseline predicted_run computed from simulator (seeds and sim_duration_ms); merged conservatively across seeds"
	}
	outScenario, rep, err := calibration.CalibrateScenario(scenario, obs, opts)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	yamlOut, err := config.MarshalScenarioYAML(outScenario)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "marshal calibrated scenario: "+err.Error())
		return
	}
	resp := map[string]any{
		"calibrated_scenario_yaml": yamlOut,
		"calibration_report":       rep,
		"warnings":                 mergeCalibrationReportWarnings(rep),
		"field_changes":            rep.Changes,
	}
	if autoPredictNote != "" {
		resp["auto_predict"] = true
		resp["auto_predict_note"] = autoPredictNote
	}
	writeJSON(w, http.StatusOK, resp)
}

func handleValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body failed")
		return
	}
	var req validateHTTPRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.ScenarioYAML) == "" {
		writeError(w, http.StatusBadRequest, "scenario_yaml is required")
		return
	}
	if len(req.Observed) == 0 {
		writeError(w, http.StatusBadRequest, "observed is required")
		return
	}
	scenario, err := config.ParseScenarioYAMLString(req.ScenarioYAML)
	if err != nil {
		writeError(w, http.StatusBadRequest, "scenario_yaml: "+err.Error())
		return
	}
	obs, err := calibration.DecodeObservedMetrics(req.ObservedFormat, req.Observed)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	tol := calibration.DefaultValidationTolerances()
	if req.ValidateOptions != nil {
		if p := strings.TrimSpace(req.ValidateOptions.ToleranceProfile); p != "" {
			tol = calibration.ResolveToleranceProfile(p)
		}
		var err error
		tol, err = calibration.ApplyToleranceJSON(tol, req.ValidateOptions.Tolerances)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	vo := &calibration.ValidateOptions{Seeds: req.Seeds, Tolerances: tol}
	if len(vo.Seeds) == 0 {
		vo.Seeds = []int64{1, 2, 3}
	}
	if req.ValidateOptions != nil {
		vo.RealTimeWorkload = req.ValidateOptions.RealTimeWorkload
	}
	simMs := req.SimDurationMs
	if simMs <= 0 {
		simMs = 10_000
	}
	rep, err := calibration.ValidateScenario(scenario, obs, simMs, vo)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"validation_report": rep,
		"warnings":          rep.Warnings,
		"largest_errors":    rep.LargestErrors,
		"pass":              rep.Pass,
	})
}

func calibrateOptionsFromJSON(j *struct {
	Overwrite       string  `json:"overwrite,omitempty"`
	ConfidenceFloor float64 `json:"confidence_floor,omitempty"`
	MinScaleFactor  float64 `json:"min_scale_factor,omitempty"`
	MaxScaleFactor  float64 `json:"max_scale_factor,omitempty"`
}) *calibration.CalibrateOptions {
	o := calibration.CalibrateOptions{
		Overwrite:       calibration.OverwriteWhenHigherConfidence,
		ConfidenceFloor: 0.2,
		MinScaleFactor:  0.25,
		MaxScaleFactor:  4.0,
	}
	if j == nil {
		return &o
	}
	switch strings.ToLower(strings.TrimSpace(j.Overwrite)) {
	case "never":
		o.Overwrite = calibration.OverwriteNever
	case "always":
		o.Overwrite = calibration.OverwriteAlways
	case "when_higher_confidence", "":
		o.Overwrite = calibration.OverwriteWhenHigherConfidence
	}
	if j.ConfidenceFloor > 0 {
		o.ConfidenceFloor = j.ConfidenceFloor
	}
	if j.MinScaleFactor > 0 {
		o.MinScaleFactor = j.MinScaleFactor
	}
	if j.MaxScaleFactor > 0 {
		o.MaxScaleFactor = j.MaxScaleFactor
	}
	return &o
}

func mergeCalibrationReportWarnings(rep *calibration.CalibrationReport) []string {
	if rep == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, s := range rep.Warnings {
		add(s)
	}
	for _, s := range rep.AmbiguousMappings {
		add("ambiguous: " + s)
	}
	for _, s := range rep.SkippedLowConfidence {
		add("skipped_low_confidence: " + s)
	}
	return out
}
