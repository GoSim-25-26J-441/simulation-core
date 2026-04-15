package calibration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/simd"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestE2ECalibrateThenValidateSelfConsistent(t *testing.T) {
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{{
			ID: "api", Replicas: 1, Model: "cpu",
			Endpoints: []config.Endpoint{
				{Path: "/a", MeanCPUMs: 0.5, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}, FailureRate: 0},
			},
		}},
		Workload: []config.WorkloadPattern{
			{From: "c", To: "api:/a", Arrival: config.ArrivalSpec{Type: "constant", RateRPS: 15}},
		},
	}
	durMs := int64(800)
	dur := time.Duration(durMs) * time.Millisecond
	rm, err := simd.RunScenarioForMetrics(sc, dur, 77, false)
	if err != nil {
		t.Fatal(err)
	}
	obs := FromRunMetrics(rm, dur)
	out, calRep, err := CalibrateScenario(sc, obs, &CalibrateOptions{
		Overwrite:       OverwriteNever,
		ConfidenceFloor: 0.0,
		PredictedRun:    rm,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || calRep == nil {
		t.Fatal("expected scenario and report")
	}
	valRep, err := ValidateScenario(out, obs, durMs, &ValidateOptions{
		Seeds:      []int64{77},
		Tolerances: DefaultValidationTolerances(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !valRep.Pass {
		t.Fatalf("expected pass after self-calibrate, checks=%+v warnings=%v", valRep.Checks, valRep.Warnings)
	}
}

func TestE2EFixtureObservedMetricsCalibrateValidateWarnings(t *testing.T) {
	sc := mustLoadFixtureScenario(t, "scenario_e2e.yaml")
	obs := mustLoadObservedFixture(t, FormatObservedMetrics, "observed_metrics_e2e.json")
	durMs := int64(obs.Window.Duration / time.Millisecond)
	if durMs <= 0 {
		durMs = 600
	}
	pred, err := simd.RunScenarioForMetrics(sc, time.Duration(durMs)*time.Millisecond, 13, false)
	if err != nil {
		t.Fatal(err)
	}
	out, calRep, err := CalibrateScenario(sc, obs, &CalibrateOptions{
		Overwrite:    OverwriteAlways,
		PredictedRun: pred,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || calRep == nil {
		t.Fatal("expected calibrated scenario and report")
	}
	if !containsWarning(calRep.Warnings, "service-level predicted processing mean") {
		t.Fatalf("expected explicit endpoint->service fallback warning, got %v", calRep.Warnings)
	}
	valRep, err := ValidateScenario(out, obs, durMs, &ValidateOptions{
		Seeds:      []int64{13},
		Tolerances: DefaultValidationTolerances(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(valRep.Checks) == 0 {
		t.Fatalf("expected validation checks, got %+v", valRep)
	}
	if !containsWarning(valRep.Warnings, "no endpoint-level processing latency samples") {
		t.Fatalf("expected explicit endpoint processing fallback warning, got %v", valRep.Warnings)
	}
}

func TestE2EFixtureSimulatorExportCalibrateValidateWarnings(t *testing.T) {
	sc := mustLoadFixtureScenario(t, "scenario_e2e.yaml")
	obs := mustLoadObservedFixture(t, FormatSimulatorExport, "simulator_export_e2e.json")
	durMs := int64(obs.Window.Duration / time.Millisecond)
	if durMs <= 0 {
		durMs = 600
	}
	pred, err := simd.RunScenarioForMetrics(sc, time.Duration(durMs)*time.Millisecond, 17, false)
	if err != nil {
		t.Fatal(err)
	}
	out, calRep, err := CalibrateScenario(sc, obs, &CalibrateOptions{
		Overwrite:    OverwriteAlways,
		PredictedRun: pred,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || calRep == nil {
		t.Fatal("expected calibrated scenario and report")
	}
	if !containsWarning(calRep.Warnings, "service-level predicted processing mean") {
		t.Fatalf("expected explicit endpoint->service fallback warning, got %v", calRep.Warnings)
	}
	valRep, err := ValidateScenario(out, obs, durMs, &ValidateOptions{
		Seeds:      []int64{17},
		Tolerances: DefaultValidationTolerances(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(valRep.Checks) == 0 {
		t.Fatalf("expected validation checks, got %+v", valRep)
	}
}

func TestE2EFixturePrometheusValidateWarnings(t *testing.T) {
	sc := mustLoadFixtureScenario(t, "scenario_e2e.yaml")
	obs := mustLoadObservedFixture(t, FormatPrometheusJSON, "prometheus_e2e.json")
	durMs := int64(obs.Window.Duration / time.Millisecond)
	if durMs <= 0 {
		durMs = 600
	}
	valRep, err := ValidateScenario(sc, obs, durMs, &ValidateOptions{
		Seeds:      []int64{23},
		Tolerances: DefaultValidationTolerances(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(valRep.Checks) == 0 {
		t.Fatalf("expected validation checks, got %+v", valRep)
	}
	if !containsWarning(valRep.Warnings, "queue_drop_rate: skipped") {
		t.Fatalf("expected explicit queue drop warning, got %v", valRep.Warnings)
	}
	if !containsWarning(valRep.Warnings, "topic_drop_rate: skipped") {
		t.Fatalf("expected explicit topic drop warning, got %v", valRep.Warnings)
	}
	if !containsWarning(valRep.Warnings, "no endpoint-level processing latency samples") {
		t.Fatalf("expected explicit endpoint processing fallback warning, got %v", valRep.Warnings)
	}
}

func TestE2EFixtureSimpleThroughputCalibrationPass(t *testing.T) {
	sc := mustLoadFixtureScenario(t, "e2e_scenario.yaml")
	obs := mustLoadObservedFixture(t, FormatObservedMetrics, "e2e_observed_metrics.json")
	durMs := int64(10_000)
	before, err := ValidateScenario(sc, obs, durMs, &ValidateOptions{
		Seeds:      []int64{1, 2, 3},
		Tolerances: DefaultValidationTolerances(),
	})
	if err != nil {
		t.Fatal(err)
	}
	pred, err := RunBaselinePredictedRun(sc, durMs, []int64{1, 2, 3}, false)
	if err != nil {
		t.Fatal(err)
	}
	out, calRep, err := CalibrateScenario(sc, obs, &CalibrateOptions{
		Overwrite:       OverwriteAlways,
		ConfidenceFloor: 0.0,
		PredictedRun:    pred,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || calRep == nil {
		t.Fatal("expected calibrated scenario and report")
	}
	if out.Workload[0].Arrival.RateRPS <= sc.Workload[0].Arrival.RateRPS {
		t.Fatalf("expected calibration to increase workload rate, before=%v after=%v", sc.Workload[0].Arrival.RateRPS, out.Workload[0].Arrival.RateRPS)
	}
	valRep, err := ValidateScenario(out, obs, durMs, &ValidateOptions{
		Seeds:      []int64{1, 2, 3},
		Tolerances: DefaultValidationTolerances(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Checks) == 0 || len(valRep.Checks) == 0 {
		t.Fatalf("expected checks before/after calibration, before=%+v after=%+v", before.Checks, valRep.Checks)
	}
	beforeErr := before.Checks[0].AbsError
	afterErr := valRep.Checks[0].AbsError
	if !(afterErr < beforeErr) {
		t.Fatalf("expected throughput error to improve after calibration, before=%v after=%v checks=%+v", beforeErr, afterErr, valRep.Checks)
	}
}

func mustLoadFixtureScenario(t *testing.T, name string) *config.Scenario {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	sc, err := config.ParseScenarioYAML(b)
	if err != nil {
		t.Fatal(err)
	}
	return sc
}

func mustLoadObservedFixture(t *testing.T, format, name string) *ObservedMetrics {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	obs, err := DecodeObservedMetrics(format, b)
	if err != nil {
		t.Fatal(err)
	}
	return obs
}

func containsWarning(warnings []string, needle string) bool {
	for _, w := range warnings {
		if strings.Contains(w, needle) {
			return true
		}
	}
	return false
}
