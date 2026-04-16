package calibration

import (
	"encoding/json"
	"math"
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

func TestE2EResearchFixtureCalibrateValidateImprovesPrediction(t *testing.T) {
	truth := mustLoadResearchScenario(t, "scenario_06_multi_zone_locality.yaml")
	durMs := int64(2_000)
	dur := time.Duration(durMs) * time.Millisecond
	seed := int64(20260416)
	targetRun, err := simd.RunScenarioForMetrics(truth, dur, seed, false)
	if err != nil {
		t.Fatal(err)
	}
	obs := FromRunMetrics(targetRun, dur)
	if !obs.Global.LocalityHitRate.Present || !obs.Global.CrossZoneFraction.Present {
		t.Fatalf("expected topology observations from research fixture, got %+v", obs.Global)
	}
	if len(obs.InstanceRouting) == 0 {
		t.Fatalf("expected routing observations from research fixture")
	}
	wrong := mustLoadResearchScenario(t, "scenario_06_multi_zone_locality.yaml")
	for i := range wrong.Workload {
		wrong.Workload[i].Arrival.RateRPS *= 0.55
	}
	for si := range wrong.Services {
		for ei := range wrong.Services[si].Endpoints {
			wrong.Services[si].Endpoints[ei].MeanCPUMs *= 0.55
		}
	}
	before, err := ValidateScenario(wrong, obs, durMs, &ValidateOptions{
		Seeds:      []int64{seed},
		Tolerances: DefaultValidationTolerances(),
	})
	if err != nil {
		t.Fatal(err)
	}
	predWrong, err := simd.RunScenarioForMetrics(wrong, dur, seed, false)
	if err != nil {
		t.Fatal(err)
	}
	calibrated, calRep, err := CalibrateScenario(wrong, obs, &CalibrateOptions{
		Overwrite:       OverwriteAlways,
		ConfidenceFloor: 0,
		PredictedRun:    predWrong,
	})
	if err != nil {
		t.Fatal(err)
	}
	if calibrated == nil || calRep == nil || len(calRep.Changes) == 0 {
		t.Fatalf("expected calibration changes, calibrated=%v report=%+v", calibrated != nil, calRep)
	}
	after, err := ValidateScenario(calibrated, obs, durMs, &ValidateOptions{
		Seeds:      []int64{seed},
		Tolerances: DefaultValidationTolerances(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if afterCheck := absErrorForCheck(t, after, "ingress_throughput_rps"); !(afterCheck < absErrorForCheck(t, before, "ingress_throughput_rps")) {
		t.Fatalf("expected ingress throughput error to improve, before=%v after=%v", absErrorForCheck(t, before, "ingress_throughput_rps"), afterCheck)
	}
	if math.IsInf(meanAbsErrorForCheckPrefix(before, "endpoint_processing_latency:"), 1) ||
		math.IsInf(meanAbsErrorForCheckPrefix(after, "endpoint_processing_latency:"), 1) {
		t.Fatalf("expected endpoint processing validation checks before and after calibration")
	}
	if !hasCheckNamed(before, "locality_hit_rate") || !hasCheckNamed(before, "cross_zone_fraction") {
		t.Fatalf("expected topology validation checks, before=%+v", before.Checks)
	}
	if !hasCheckPrefix(before, "instance_routing_share:") {
		t.Fatalf("expected routing-skew validation checks, before=%+v", before.Checks)
	}
}

func TestE2ERoutingSkewSimulatorExportValidatePassAndFail(t *testing.T) {
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{{
			ID: "api", Replicas: 2, Model: "cpu",
			Routing: &config.RoutingPolicy{
				Strategy: "weighted_round_robin",
				Weights: map[string]float64{
					"api-instance-0": 0.9,
					"api-instance-1": 0.1,
				},
			},
			Endpoints: []config.Endpoint{
				{Path: "/x", MeanCPUMs: 0.5, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}},
			},
		}},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "api:/x", Arrival: config.ArrivalSpec{Type: "constant", RateRPS: 600}},
		},
	}
	durMs := int64(2_000)
	dur := time.Duration(durMs) * time.Millisecond
	rm, err := simd.RunScenarioForMetrics(sc, dur, 77, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rm.InstanceRouteStats) == 0 {
		t.Fatalf("expected instance route stats in run metrics, got %+v", rm)
	}

	exportRaw, err := json.Marshal(map[string]any{
		"window_seconds": float64(durMs) / 1000.0,
		"run_metrics":    rm,
	})
	if err != nil {
		t.Fatal(err)
	}
	obs, err := DecodeObservedMetrics(FormatSimulatorExport, exportRaw)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.InstanceRouting) == 0 {
		t.Fatalf("expected instance_routing from simulator_export, got %+v", obs)
	}
	shareByInst := map[string]float64{}
	for i := range obs.InstanceRouting {
		r := obs.InstanceRouting[i]
		if r.ServiceID == "api" && r.EndpointPath == "/x" && r.RequestShare.Present {
			shareByInst[r.InstanceID] = r.RequestShare.Value
		}
	}
	if len(shareByInst) < 2 {
		t.Fatalf("expected at least 2 route-share rows, got %+v", obs.InstanceRouting)
	}
	if s0 := shareByInst["api-instance-0"]; s0 < 0.85 || s0 > 0.95 {
		t.Fatalf("expected weighted share near 0.9 for api-instance-0, got %v (shares=%v)", s0, shareByInst)
	}
	if s1 := shareByInst["api-instance-1"]; s1 < 0.05 || s1 > 0.15 {
		t.Fatalf("expected weighted share near 0.1 for api-instance-1, got %v (shares=%v)", s1, shareByInst)
	}

	passRep, err := ValidateScenario(sc, obs, durMs, &ValidateOptions{
		Seeds:      []int64{77},
		Tolerances: DefaultValidationTolerances(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !passRep.Pass {
		t.Fatalf("expected routing skew self-validation to pass, checks=%+v warnings=%v", passRep.Checks, passRep.Warnings)
	}

	// Force an obviously wrong skew observation for instance-0; this should fail routing-share checks.
	badObs := *obs
	badObs.InstanceRouting = make([]InstanceRoutingObservation, len(obs.InstanceRouting))
	copy(badObs.InstanceRouting, obs.InstanceRouting)
	for i := range badObs.InstanceRouting {
		r := &badObs.InstanceRouting[i]
		if r.ServiceID == "api" && r.EndpointPath == "/x" && r.InstanceID == "api-instance-0" {
			r.RequestShare = F64(0.2)
			break
		}
	}
	failRep, err := ValidateScenario(sc, &badObs, durMs, &ValidateOptions{
		Seeds:      []int64{77},
		Tolerances: DefaultValidationTolerances(),
	})
	if err != nil {
		t.Fatal(err)
	}
	foundRouteFail := false
	for _, ch := range failRep.Checks {
		if strings.HasPrefix(ch.Name, "instance_routing_share:api:/x:api-instance-0") && !ch.Pass {
			foundRouteFail = true
			break
		}
	}
	if !foundRouteFail {
		t.Fatalf("expected routing skew validation failure for modified share, checks=%+v", failRep.Checks)
	}
}

func TestE2EStickyRoutingSimulatorExportValidatePassAndFail(t *testing.T) {
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{{
			ID: "api", Replicas: 3, Model: "cpu",
			Routing: &config.RoutingPolicy{
				Strategy:      "sticky",
				StickyKeyFrom: "workload_from",
			},
			Endpoints: []config.Endpoint{
				{Path: "/x", MeanCPUMs: 0.5, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}},
			},
		}},
		Workload: []config.WorkloadPattern{
			{From: "web", To: "api:/x", Arrival: config.ArrivalSpec{Type: "constant", RateRPS: 60}},
			{From: "mobile", To: "api:/x", Arrival: config.ArrivalSpec{Type: "constant", RateRPS: 60}},
		},
	}
	durMs := int64(2_000)
	dur := time.Duration(durMs) * time.Millisecond
	rm, err := simd.RunScenarioForMetrics(sc, dur, 91, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rm.InstanceRouteStats) == 0 {
		t.Fatalf("expected instance route stats in run metrics, got %+v", rm)
	}

	exportRaw, err := json.Marshal(map[string]any{
		"window_seconds": float64(durMs) / 1000.0,
		"run_metrics":    rm,
	})
	if err != nil {
		t.Fatal(err)
	}
	obs, err := DecodeObservedMetrics(FormatSimulatorExport, exportRaw)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.InstanceRouting) == 0 {
		t.Fatalf("expected instance_routing from simulator_export, got %+v", obs)
	}

	passRep, err := ValidateScenario(sc, obs, durMs, &ValidateOptions{
		Seeds:      []int64{91},
		Tolerances: DefaultValidationTolerances(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !passRep.Pass {
		t.Fatalf("expected sticky routing self-validation to pass, checks=%+v warnings=%v", passRep.Checks, passRep.Warnings)
	}
	hasStickyCheck := false
	for _, ch := range passRep.Checks {
		if strings.HasPrefix(ch.Name, "instance_routing_share:api:/x:") {
			hasStickyCheck = true
			break
		}
	}
	if !hasStickyCheck {
		t.Fatalf("expected sticky routing checks in validation output, checks=%+v", passRep.Checks)
	}

	// Break one observed sticky share; routing check should fail.
	badObs := *obs
	badObs.InstanceRouting = make([]InstanceRoutingObservation, len(obs.InstanceRouting))
	copy(badObs.InstanceRouting, obs.InstanceRouting)
	target := ""
	for i := range badObs.InstanceRouting {
		r := &badObs.InstanceRouting[i]
		if r.ServiceID == "api" && r.EndpointPath == "/x" && r.RequestShare.Present {
			target = r.InstanceID
			r.RequestShare = F64(0.01)
			break
		}
	}
	if target == "" {
		t.Fatalf("expected at least one instance_routing row with request_share, rows=%+v", badObs.InstanceRouting)
	}
	failRep, err := ValidateScenario(sc, &badObs, durMs, &ValidateOptions{
		Seeds:      []int64{91},
		Tolerances: DefaultValidationTolerances(),
	})
	if err != nil {
		t.Fatal(err)
	}
	foundRouteFail := false
	for _, ch := range failRep.Checks {
		if strings.HasPrefix(ch.Name, "instance_routing_share:api:/x:"+target) && !ch.Pass {
			foundRouteFail = true
			break
		}
	}
	if !foundRouteFail {
		t.Fatalf("expected sticky routing validation failure for modified share, checks=%+v", failRep.Checks)
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

func mustLoadResearchScenario(t *testing.T, name string) *config.Scenario {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "config", "research_scenarios", name))
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

func absErrorForCheck(t *testing.T, report *ValidationReport, name string) float64 {
	t.Helper()
	for _, ch := range report.Checks {
		if ch.Name == name {
			return ch.AbsError
		}
	}
	t.Fatalf("missing validation check %q in %+v", name, report.Checks)
	return math.Inf(1)
}

func meanAbsErrorForCheckPrefix(report *ValidationReport, prefix string) float64 {
	var sum float64
	var n float64
	for _, ch := range report.Checks {
		if strings.HasPrefix(ch.Name, prefix) && strings.HasSuffix(ch.Name, ":mean_ms") {
			sum += ch.AbsError
			n++
		}
	}
	if n == 0 {
		return math.Inf(1)
	}
	return sum / n
}

func hasCheckNamed(report *ValidationReport, name string) bool {
	for _, ch := range report.Checks {
		if ch.Name == name {
			return true
		}
	}
	return false
}

func hasCheckPrefix(report *ValidationReport, prefix string) bool {
	for _, ch := range report.Checks {
		if strings.HasPrefix(ch.Name, prefix) {
			return true
		}
	}
	return false
}

func containsWarning(warnings []string, needle string) bool {
	for _, w := range warnings {
		if strings.Contains(w, needle) {
			return true
		}
	}
	return false
}
