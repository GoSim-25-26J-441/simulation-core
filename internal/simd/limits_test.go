package simd

import (
	"strings"
	"testing"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

func TestSimulationLimitsFromEnvDefaults(t *testing.T) {
	t.Setenv("SIMD_MAX_STANDARD_DURATION", "")
	t.Setenv("SIMD_MAX_EVENTS_SCHEDULED", "")
	t.Setenv("SIMD_MAX_EVENTS_PROCESSED", "")
	t.Setenv("SIMD_MAX_EVENT_QUEUE_SIZE", "")
	t.Setenv("SIMD_MAX_REQUESTS_TRACKED", "")
	t.Setenv("SIMD_MAX_TOTAL_REQUESTS", "")
	t.Setenv("SIMD_MAX_METRIC_POINTS", "")
	t.Setenv("SIMD_MAX_WALL_CLOCK_RUNTIME", "")
	t.Setenv("SIMD_MAX_OPTIMIZATION_EVALUATIONS", "")

	got, err := simulationLimitsFromEnv()
	if err != nil {
		t.Fatalf("simulationLimitsFromEnv error: %v", err)
	}
	want := defaultSimulationLimits()
	if got != want {
		t.Fatalf("defaults mismatch: got %+v want %+v", got, want)
	}
}

func TestSimulationLimitsFromEnvParsesAndRejectsInvalid(t *testing.T) {
	t.Setenv("SIMD_MAX_STANDARD_DURATION", "45s")
	t.Setenv("SIMD_MAX_EVENTS_SCHEDULED", "100")
	t.Setenv("SIMD_MAX_EVENTS_PROCESSED", "200")
	t.Setenv("SIMD_MAX_EVENT_QUEUE_SIZE", "300")
	t.Setenv("SIMD_MAX_REQUESTS_TRACKED", "400")
	t.Setenv("SIMD_MAX_TOTAL_REQUESTS", "450")
	t.Setenv("SIMD_MAX_METRIC_POINTS", "500")
	t.Setenv("SIMD_MAX_WALL_CLOCK_RUNTIME", "2m")
	t.Setenv("SIMD_MAX_OPTIMIZATION_EVALUATIONS", "600")
	got, err := simulationLimitsFromEnv()
	if err != nil {
		t.Fatalf("simulationLimitsFromEnv error: %v", err)
	}
	if got.MaxStandardDuration != 45*time.Second || got.MaxOptimizationEvaluations != 600 || got.MaxTotalRequests != 450 {
		t.Fatalf("unexpected parsed limits: %+v", got)
	}

	t.Setenv("SIMD_MAX_EVENTS_PROCESSED", "-1")
	_, err = simulationLimitsFromEnv()
	if err == nil || !strings.Contains(err.Error(), "SIMD_MAX_EVENTS_PROCESSED") {
		t.Fatalf("expected clear invalid env error, got %v", err)
	}
}

func TestSimulationLimitsValidatePreStartRejectsOversizedInputs(t *testing.T) {
	limits := defaultSimulationLimits()
	limits.MaxStandardDuration = 2 * time.Second
	limits.MaxOptimizationEvaluations = 3
	limits.MaxEventsScheduled = 5

	input := &simulationv1.RunInput{
		ScenarioYaml: `
hosts:
  - id: host-1
    cores: 2
services:
  - id: svc1
    replicas: 1
    model: cpu
    endpoints:
      - path: /x
        mean_cpu_ms: 1
        cpu_sigma_ms: 0
workload:
  - from: client
    to: svc1:/x
    arrival: {type: poisson, rate_rps: 10}
`,
		DurationMs: 3000,
	}
	if err := limits.validatePreStart(input); err == nil || !strings.Contains(err.Error(), "max standard duration") {
		t.Fatalf("expected duration rejection, got %v", err)
	}

	input.DurationMs = 1000
	if err := limits.validatePreStart(input); err == nil || !strings.Contains(err.Error(), "estimated arrivals") {
		t.Fatalf("expected arrival estimate rejection, got %v", err)
	}

	input.Optimization = &simulationv1.OptimizationConfig{
		MaxEvaluations: 10,
	}
	if err := limits.validatePreStart(input); err == nil || !strings.Contains(err.Error(), "max_evaluations") {
		t.Fatalf("expected optimization evaluation rejection, got %v", err)
	}
}
