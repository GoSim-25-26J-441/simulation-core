package simd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

func TestEffectiveOnlineTargetUtilDefaults(t *testing.T) {
	t.Parallel()
	if g, w := EffectiveOnlineTargetUtilLow(nil), DefaultOnlineTargetUtilLow; g != w {
		t.Fatalf("low nil: got %v want %v", g, w)
	}
	if g, w := EffectiveOnlineTargetUtilHigh(nil), DefaultOnlineTargetUtilHigh; g != w {
		t.Fatalf("high nil: got %v want %v", g, w)
	}
	opt := &simulationv1.OptimizationConfig{}
	if g, w := EffectiveOnlineTargetUtilLow(opt), DefaultOnlineTargetUtilLow; g != w {
		t.Fatalf("low unset: got %v want %v", g, w)
	}
	if g, w := EffectiveOnlineTargetUtilHigh(opt), DefaultOnlineTargetUtilHigh; g != w {
		t.Fatalf("high unset: got %v want %v", g, w)
	}
}

func TestValidateOnlinePrimaryUtilizationBandAcceptsDefaultsAndValidPairs(t *testing.T) {
	t.Parallel()
	opt := &simulationv1.OptimizationConfig{
		Online:                    true,
		OptimizationTargetPrimary: "cpu_utilization",
		TargetP95LatencyMs:        50,
	}
	if err := validateOnlineOptimizationConfig(opt); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	opt2 := &simulationv1.OptimizationConfig{
		Online:                    true,
		OptimizationTargetPrimary: "cpu_utilization",
		TargetP95LatencyMs:        50,
		TargetUtilLow:             0.35,
		TargetUtilHigh:            0.6,
	}
	if err := validateOnlineOptimizationConfig(opt2); err != nil {
		t.Fatalf("explicit band: %v", err)
	}
}

func TestValidateOnlinePrimaryUtilizationBandRejectsInvalid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		opt  *simulationv1.OptimizationConfig
	}{
		{
			name: "inverted",
			opt: &simulationv1.OptimizationConfig{
				Online:                    true,
				OptimizationTargetPrimary: "cpu_utilization",
				TargetP95LatencyMs:        50,
				TargetUtilLow:             0.8,
				TargetUtilHigh:            0.2,
			},
		},
		{
			name: "low_negative",
			opt: &simulationv1.OptimizationConfig{
				Online:                    true,
				OptimizationTargetPrimary: "memory_utilization",
				TargetP95LatencyMs:        50,
				TargetUtilLow:             -0.05,
				TargetUtilHigh:            0.5,
			},
		},
		{
			name: "high_above_one",
			opt: &simulationv1.OptimizationConfig{
				Online:                    true,
				OptimizationTargetPrimary: "cpu_utilization",
				TargetP95LatencyMs:        50,
				TargetUtilLow:             0.2,
				TargetUtilHigh:            1.01,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateOnlineOptimizationConfig(tc.opt)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), "online optimization") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestP95PrimaryOnlineIgnoresUtilizationBandValues(t *testing.T) {
	t.Parallel()
	opt := &simulationv1.OptimizationConfig{
		Online:                    true,
		OptimizationTargetPrimary: "p95_latency",
		TargetP95LatencyMs:        100,
		TargetUtilLow:             0.9,
		TargetUtilHigh:            0.1,
	}
	if err := validateOnlineOptimizationConfig(opt); err != nil {
		t.Fatalf("p95-primary should not validate utilization band: %v", err)
	}
}

func TestRunStoreCreateCPUPrimaryEmitsHostBoundWarnings(t *testing.T) {
	t.Parallel()
	store := NewRunStore()
	opt := &simulationv1.OptimizationConfig{
		Online:                    true,
		OptimizationTargetPrimary: "cpu_utilization",
		TargetP95LatencyMs:        100,
	}
	rec, err := store.Create("cpu-warn-1", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   1000,
		Optimization: opt,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(rec.ServerWarnings) != 2 {
		t.Fatalf("expected 2 warnings, got %#v", rec.ServerWarnings)
	}
	if rec.ServerWarnings[0] != warnCPUPrimaryHostScaleOutDisabled {
		t.Fatalf("warning0: %q", rec.ServerWarnings[0])
	}
	if rec.ServerWarnings[1] != warnCPUPrimaryHostScaleInDisabled {
		t.Fatalf("warning1: %q", rec.ServerWarnings[1])
	}

	twoHostYAML := `hosts:
  - id: host-1
    cores: 2
  - id: host-2
    cores: 2
services:
  - id: svc1
    replicas: 1
    model: cpu
    endpoints:
      - path: /test
        mean_cpu_ms: 10
        cpu_sigma_ms: 2
        downstream: []
        net_latency_ms: {mean: 1, sigma: 0.5}
workload:
  - from: client
    to: svc1:/test
    arrival: {type: poisson, rate_rps: 10}
`
	opt2 := &simulationv1.OptimizationConfig{
		Online:                    true,
		OptimizationTargetPrimary: "cpu_utilization",
		TargetP95LatencyMs:        100,
		MinHosts:                  1,
		MaxHosts:                  8,
	}
	rec2, err := store.Create("cpu-warn-2", &simulationv1.RunInput{
		ScenarioYaml: twoHostYAML,
		DurationMs:   1000,
		Optimization: opt2,
	})
	if err != nil {
		t.Fatalf("Create2: %v", err)
	}
	if len(rec2.ServerWarnings) != 0 {
		t.Fatalf("expected no warnings with room to scale, got %#v", rec2.ServerWarnings)
	}

	rec3, err := store.Create("cpu-warn-3", &simulationv1.RunInput{
		ScenarioYaml: twoHostYAML,
		DurationMs:   1000,
		Optimization: &simulationv1.OptimizationConfig{
			Online:                    true,
			OptimizationTargetPrimary: "cpu_utilization",
			TargetP95LatencyMs:        100,
			MinHosts:                  2,
			MaxHosts:                  8,
		},
	})
	if err != nil {
		t.Fatalf("Create3: %v", err)
	}
	if len(rec3.ServerWarnings) != 1 || rec3.ServerWarnings[0] != warnCPUPrimaryHostScaleInDisabled {
		t.Fatalf("expected single scale-in warning, got %#v", rec3.ServerWarnings)
	}
}

func TestHTTPServerGetRunIncludesCPUPrimaryWarnings(t *testing.T) {
	t.Parallel()
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))
	if _, err := store.Create("cpu-warn-http", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   1000,
		Optimization: &simulationv1.OptimizationConfig{
			Online:                    true,
			OptimizationTargetPrimary: "cpu_utilization",
			TargetP95LatencyMs:        100,
		},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/cpu-warn-http", nil)
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	run := body["run"].(map[string]any)
	warns, ok := run["warnings"].([]any)
	if !ok || len(warns) != 2 {
		t.Fatalf("expected run.warnings with 2 entries, got %#v", run["warnings"])
	}
}

func TestRunStoreCreateRejectsInvalidCPUUtilizationBand(t *testing.T) {
	t.Parallel()
	store := NewRunStore()
	_, err := store.Create("bad-band", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   1000,
		Optimization: &simulationv1.OptimizationConfig{
			Online:                    true,
			OptimizationTargetPrimary: "cpu_utilization",
			TargetP95LatencyMs:        100,
			TargetUtilLow:             0.85,
			TargetUtilHigh:            0.2,
		},
	})
	if err == nil {
		t.Fatal("expected create error")
	}
}
