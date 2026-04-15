package calibration

import (
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/simd"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestEndpointErrorRateUsesPerEndpointPrediction(t *testing.T) {
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{{
			ID: "api", Replicas: 1, Model: "cpu",
			Endpoints: []config.Endpoint{
				{Path: "/ok", MeanCPUMs: 0.5, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}, FailureRate: 0},
				{Path: "/bad", MeanCPUMs: 0.5, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}, FailureRate: 0.9},
			},
		}},
		Workload: []config.WorkloadPattern{
			{From: "c", To: "api:/ok", Arrival: config.ArrivalSpec{Type: "constant", RateRPS: 20}},
			{From: "c", To: "api:/bad", Arrival: config.ArrivalSpec{Type: "constant", RateRPS: 20}},
		},
	}
	durMs := int64(800)
	dur := time.Duration(durMs) * time.Millisecond
	rm, err := simd.RunScenarioForMetrics(sc, dur, 101, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rm.EndpointRequestStats) < 2 {
		t.Fatalf("expected endpoint rollups, got %+v", rm.EndpointRequestStats)
	}
	obs := &ObservedMetrics{}
	for _, es := range rm.EndpointRequestStats {
		if es.RequestCount <= 0 {
			continue
		}
		obs.Endpoints = append(obs.Endpoints, EndpointObservation{
			ServiceID:    es.ServiceName,
			EndpointPath: es.EndpointPath,
			RequestCount: I64(es.RequestCount),
			ErrorCount:   I64(es.ErrorCount),
		})
	}
	rep, err := ValidateScenario(sc, obs, durMs, &ValidateOptions{
		Seeds:      []int64{101},
		Tolerances: DefaultValidationTolerances(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Pass {
		t.Fatalf("self validation should pass, checks=%+v warnings=%v", rep.Checks, rep.Warnings)
	}
}

func TestEndpointErrorRateMismatchOnlyOnOnePath(t *testing.T) {
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{{
			ID: "api", Replicas: 1, Model: "cpu",
			Endpoints: []config.Endpoint{
				{Path: "/ok", MeanCPUMs: 0.5, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}, FailureRate: 0},
				{Path: "/bad", MeanCPUMs: 0.5, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}, FailureRate: 0.9},
			},
		}},
		Workload: []config.WorkloadPattern{
			{From: "c", To: "api:/ok", Arrival: config.ArrivalSpec{Type: "constant", RateRPS: 20}},
			{From: "c", To: "api:/bad", Arrival: config.ArrivalSpec{Type: "constant", RateRPS: 20}},
		},
	}
	durMs := int64(800)
	dur := time.Duration(durMs) * time.Millisecond
	rm, err := simd.RunScenarioForMetrics(sc, dur, 202, false)
	if err != nil {
		t.Fatal(err)
	}
	var okCount, badCount, okErr int64
	for _, es := range rm.EndpointRequestStats {
		switch es.EndpointPath {
		case "/ok":
			okCount = es.RequestCount
			okErr = es.ErrorCount
		case "/bad":
			badCount = es.RequestCount
		}
	}
	if okCount == 0 || badCount == 0 {
		t.Fatalf("unexpected rollups %+v", rm.EndpointRequestStats)
	}
	obs := &ObservedMetrics{
		Endpoints: []EndpointObservation{
			{ServiceID: "api", EndpointPath: "/ok", RequestCount: I64(okCount), ErrorCount: I64(okErr)},
			{ServiceID: "api", EndpointPath: "/bad", RequestCount: I64(badCount), ErrorCount: I64(0)},
		},
	}
	rep, err := ValidateScenario(sc, obs, durMs, &ValidateOptions{
		Seeds:      []int64{202},
		Tolerances: DefaultValidationTolerances(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pass {
		t.Fatal("expected fail on /bad error rate lie")
	}
	var sawBad bool
	for _, c := range rep.Checks {
		if c.Name == "endpoint_error_rate:api:/bad" && !c.Pass {
			sawBad = true
		}
		if c.Name == "endpoint_error_rate:api:/ok" && !c.Pass {
			t.Fatalf("/ok should still pass, got %+v", c)
		}
	}
	if !sawBad {
		t.Fatalf("expected failing check for /bad, checks=%+v", rep.Checks)
	}
}
