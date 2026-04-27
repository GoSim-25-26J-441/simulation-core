package calibrationserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/batchspec"
	"github.com/GoSim-25-26J-441/simulation-core/internal/calibration"
	"github.com/GoSim-25-26J-441/simulation-core/internal/simd"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

func TestValidateEndpointReturnsWarnings(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux)
	body := map[string]any{
		"scenario_yaml": strings.TrimSpace(`
hosts: [{id: h1, cores: 8, memory_gb: 16}]
services:
  - id: api
    replicas: 1
    model: cpu
    endpoints:
      - path: /a
        mean_cpu_ms: 0.5
        cpu_sigma_ms: 0
        net_latency_ms: {mean: 0, sigma: 0}
        failure_rate: 0
workload:
  - from: c
    to: "api:/a"
    arrival: {type: constant, rate_rps: 10}
`),
		"observed_format": calibration.FormatSimulatorExport,
		"observed": map[string]any{
			"window_seconds": 60,
			"run_metrics": map[string]any{
				"total_requests":         100,
				"ingress_requests":       100,
				"ingress_error_rate":     0,
				"latency_p50_ms":         5,
				"latency_p95_ms":         10,
				"latency_p99_ms":         20,
				"latency_mean_ms":        6,
				"throughput_rps":         10,
				"ingress_throughput_rps": 10,
			},
		},
		"sim_duration_ms": 500,
		"seeds":           []int64{42},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/validate", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := out["warnings"]; !ok {
		t.Fatal("expected warnings key in response")
	}
}

const testScenarioYAMLRate10 = `
hosts: [{id: h1, cores: 8, memory_gb: 16}]
services:
  - id: api
    replicas: 1
    model: cpu
    endpoints:
      - path: /a
        mean_cpu_ms: 0.5
        cpu_sigma_ms: 0
        net_latency_ms: {mean: 0, sigma: 0}
        failure_rate: 0
workload:
  - from: c
    to: "api:/a"
    arrival: {type: constant, rate_rps: 10}
`

const testScenarioYAMLRate1 = `
hosts: [{id: h1, cores: 8, memory_gb: 16}]
services:
  - id: api
    replicas: 1
    model: cpu
    endpoints:
      - path: /a
        mean_cpu_ms: 0.5
        cpu_sigma_ms: 0
        net_latency_ms: {mean: 0, sigma: 0}
        failure_rate: 0
workload:
  - from: c
    to: "api:/a"
    arrival: {type: constant, rate_rps: 1}
`

func postCalibrate(t *testing.T, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux)
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/calibrate", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func postValidate(t *testing.T, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux)
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/validate", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestCalibrateHTTP_BadJSON(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/v1/calibrate", bytes.NewReader([]byte(`{`)))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: status %d %s", rec.Code, rec.Body.String())
	}
}

func TestCalibrateHTTP_MissingScenario(t *testing.T) {
	rec := postCalibrate(t, map[string]any{
		"observed_format": calibration.FormatSimulatorExport,
		"observed":        map[string]any{"window": "1s", "run_metrics": map[string]any{"total_requests": 1}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
}

func TestCalibrateHTTP_MissingObserved(t *testing.T) {
	rec := postCalibrate(t, map[string]any{
		"scenario_yaml":   strings.TrimSpace(testScenarioYAMLRate1),
		"observed_format": calibration.FormatSimulatorExport,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
}

func TestCalibrateHTTP_PredictedRunDirect(t *testing.T) {
	durMs := int64(400)
	d := time.Duration(durMs) * time.Millisecond
	truth, err := config.ParseScenarioYAMLString(strings.TrimSpace(testScenarioYAMLRate10))
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := config.ParseScenarioYAMLString(strings.TrimSpace(testScenarioYAMLRate1))
	if err != nil {
		t.Fatal(err)
	}
	rmTruth, err := simd.RunScenarioForMetrics(truth, d, 42, false)
	if err != nil {
		t.Fatal(err)
	}
	rmWrong, err := simd.RunScenarioForMetrics(wrong, d, 42, false)
	if err != nil {
		t.Fatal(err)
	}
	observed := map[string]any{
		"window":      "400ms",
		"run_metrics": rmTruth,
	}
	var predWire any
	predBytes, _ := json.Marshal(rmWrong)
	if err := json.Unmarshal(predBytes, &predWire); err != nil {
		t.Fatal(err)
	}
	rec := postCalibrate(t, map[string]any{
		"scenario_yaml":   strings.TrimSpace(testScenarioYAMLRate1),
		"observed_format": calibration.FormatSimulatorExport,
		"observed":        observed,
		"predicted_run":   predWire,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := out["calibrated_scenario_yaml"]; !ok {
		t.Fatal("expected calibrated_scenario_yaml")
	}
	if _, ok := out["warnings"]; !ok {
		t.Fatal("expected warnings")
	}
	if _, ok := out["field_changes"]; !ok {
		t.Fatal("expected field_changes")
	}
}

func TestCalibrateHTTP_PredictedRunEnvelope(t *testing.T) {
	durMs := int64(400)
	d := time.Duration(durMs) * time.Millisecond
	truth, err := config.ParseScenarioYAMLString(strings.TrimSpace(testScenarioYAMLRate10))
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := config.ParseScenarioYAMLString(strings.TrimSpace(testScenarioYAMLRate1))
	if err != nil {
		t.Fatal(err)
	}
	rmTruth, err := simd.RunScenarioForMetrics(truth, d, 42, false)
	if err != nil {
		t.Fatal(err)
	}
	rmWrong, err := simd.RunScenarioForMetrics(wrong, d, 42, false)
	if err != nil {
		t.Fatal(err)
	}
	observed := map[string]any{
		"window":      "400ms",
		"run_metrics": rmTruth,
	}
	var envWire any
	envBytes, _ := json.Marshal(map[string]any{"run_metrics": rmWrong})
	if err := json.Unmarshal(envBytes, &envWire); err != nil {
		t.Fatal(err)
	}
	rec := postCalibrate(t, map[string]any{
		"scenario_yaml":   strings.TrimSpace(testScenarioYAMLRate1),
		"observed_format": calibration.FormatSimulatorExport,
		"observed":        observed,
		"predicted_run":   envWire,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
}

func TestCalibrateHTTP_AutoPredict(t *testing.T) {
	durMs := int64(400)
	d := time.Duration(durMs) * time.Millisecond
	truth, err := config.ParseScenarioYAMLString(strings.TrimSpace(testScenarioYAMLRate10))
	if err != nil {
		t.Fatal(err)
	}
	rmTruth, err := simd.RunScenarioForMetrics(truth, d, 42, false)
	if err != nil {
		t.Fatal(err)
	}
	observed := map[string]any{
		"window":      "400ms",
		"run_metrics": rmTruth,
	}
	rec := postCalibrate(t, map[string]any{
		"scenario_yaml":   strings.TrimSpace(testScenarioYAMLRate1),
		"observed_format": calibration.FormatSimulatorExport,
		"observed":        observed,
		"sim_duration_ms": durMs,
		"seeds":           []int64{42},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["auto_predict"] != true {
		t.Fatalf("expected auto_predict true, got %#v", out["auto_predict"])
	}
	if _, ok := out["auto_predict_note"]; !ok {
		t.Fatal("expected auto_predict_note")
	}
}

func TestCalibrateHTTP_AutoPredictCustomSeedsAndDuration(t *testing.T) {
	durMs := int64(300)
	d := time.Duration(durMs) * time.Millisecond
	truth, err := config.ParseScenarioYAMLString(strings.TrimSpace(testScenarioYAMLRate10))
	if err != nil {
		t.Fatal(err)
	}
	rmTruth, err := simd.RunScenarioForMetrics(truth, d, 42, false)
	if err != nil {
		t.Fatal(err)
	}
	observed := map[string]any{
		"window":      "300ms",
		"run_metrics": rmTruth,
	}
	rec := postCalibrate(t, map[string]any{
		"scenario_yaml":   strings.TrimSpace(testScenarioYAMLRate1),
		"observed_format": calibration.FormatSimulatorExport,
		"observed":        observed,
		"sim_duration_ms": durMs,
		"seeds":           []int64{99, 100},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	note, _ := out["auto_predict_note"].(string)
	if note == "" {
		t.Fatal("expected non-empty auto_predict_note")
	}
}

func TestCalibrateHTTP_ReparseScenarioUnchanged(t *testing.T) {
	yaml := strings.TrimSpace(testScenarioYAMLRate1)
	sc1, err := config.ParseScenarioYAMLString(yaml)
	if err != nil {
		t.Fatal(err)
	}
	h1 := batchspec.ConfigHash(sc1)

	durMs := int64(350)
	d := time.Duration(durMs) * time.Millisecond
	truth, err := config.ParseScenarioYAMLString(strings.TrimSpace(testScenarioYAMLRate10))
	if err != nil {
		t.Fatal(err)
	}
	rmTruth, err := simd.RunScenarioForMetrics(truth, d, 42, false)
	if err != nil {
		t.Fatal(err)
	}
	observed := map[string]any{
		"window":      "350ms",
		"run_metrics": rmTruth,
	}
	rec := postCalibrate(t, map[string]any{
		"scenario_yaml":   yaml,
		"observed_format": calibration.FormatSimulatorExport,
		"observed":        observed,
		"sim_duration_ms": durMs,
		"seeds":           []int64{7},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	sc2, err := config.ParseScenarioYAMLString(yaml)
	if err != nil {
		t.Fatal(err)
	}
	h2 := batchspec.ConfigHash(sc2)
	if h1 != h2 {
		t.Fatal("re-parsing scenario YAML after calibrate request should yield identical semantics hash")
	}
}

func TestCalibrateHTTP_PredictedRunUsesModelsRunMetrics(t *testing.T) {
	// Minimal JSON that unmarshals into RunMetrics with TotalRequests > 0 (no simd).
	rm := &models.RunMetrics{TotalRequests: 10, IngressRequests: 10, IngressThroughputRPS: 1}
	truth := &models.RunMetrics{TotalRequests: 100, IngressRequests: 100, IngressThroughputRPS: 10}
	observed := map[string]any{
		"window":      "1s",
		"run_metrics": truth,
	}
	var predWire any
	b, _ := json.Marshal(rm)
	_ = json.Unmarshal(b, &predWire)
	rec := postCalibrate(t, map[string]any{
		"scenario_yaml":   strings.TrimSpace(testScenarioYAMLRate1),
		"observed_format": calibration.FormatSimulatorExport,
		"observed":        observed,
		"predicted_run":   predWire,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
}

func TestCalibrateOptionsFromJSONDefaultsAndOverrides(t *testing.T) {
	def := calibrateOptionsFromJSON(nil)
	if def == nil {
		t.Fatal("expected defaults")
	}
	if def.ConfidenceFloor <= 0 || def.MinScaleFactor <= 0 || def.MaxScaleFactor <= 0 {
		t.Fatalf("unexpected default options: %#v", def)
	}

	in := &struct {
		Overwrite       string  `json:"overwrite,omitempty"`
		ConfidenceFloor float64 `json:"confidence_floor,omitempty"`
		MinScaleFactor  float64 `json:"min_scale_factor,omitempty"`
		MaxScaleFactor  float64 `json:"max_scale_factor,omitempty"`
	}{
		Overwrite:       "always",
		ConfidenceFloor: 0.4,
		MinScaleFactor:  0.5,
		MaxScaleFactor:  3.5,
	}
	got := calibrateOptionsFromJSON(in)
	if got.Overwrite != calibration.OverwriteAlways {
		t.Fatalf("expected overwrite always, got %v", got.Overwrite)
	}
	if got.ConfidenceFloor != 0.4 || got.MinScaleFactor != 0.5 || got.MaxScaleFactor != 3.5 {
		t.Fatalf("unexpected override options: %#v", got)
	}
}

func TestMergeCalibrationReportWarnings_DedupesAndPrefixes(t *testing.T) {
	rep := &calibration.CalibrationReport{
		Warnings:             []string{"w1", " w1 ", ""},
		AmbiguousMappings:    []string{"m1"},
		SkippedLowConfidence: []string{"s1"},
	}
	got := mergeCalibrationReportWarnings(rep)
	want := []string{"w1", "ambiguous: m1", "skipped_low_confidence: s1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merge warnings = %v, want %v", got, want)
	}
}

func TestValidateHTTPMethodNotAllowed(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/v1/validate", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestValidateHTTPBadRequestPaths(t *testing.T) {
	t.Run("bad json", func(t *testing.T) {
		mux := http.NewServeMux()
		Register(mux)
		req := httptest.NewRequest(http.MethodPost, "/v1/validate", bytes.NewReader([]byte(`{`)))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("missing scenario", func(t *testing.T) {
		rec := postValidate(t, map[string]any{
			"observed_format": calibration.FormatSimulatorExport,
			"observed":        map[string]any{"window": "1s", "run_metrics": map[string]any{"total_requests": 1}},
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("missing observed", func(t *testing.T) {
		rec := postValidate(t, map[string]any{
			"scenario_yaml":   strings.TrimSpace(testScenarioYAMLRate1),
			"observed_format": calibration.FormatSimulatorExport,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("invalid tolerance overrides", func(t *testing.T) {
		rec := postValidate(t, map[string]any{
			"scenario_yaml":   strings.TrimSpace(testScenarioYAMLRate1),
			"observed_format": calibration.FormatSimulatorExport,
			"observed": map[string]any{
				"window":      "1s",
				"run_metrics": map[string]any{"total_requests": 10, "ingress_requests": 10, "ingress_throughput_rps": 1},
			},
			"validate_options": map[string]any{
				"tolerance_profile": "strict",
				"tolerances": map[string]any{
					"latency_p95_pct": "not-a-number",
				},
			},
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for invalid tolerance override, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}
