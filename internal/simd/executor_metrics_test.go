package simd

import (
	"testing"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

func TestConvertMetricsToProtoWithServiceMetrics(t *testing.T) {
	engineMetrics := &models.RunMetrics{
		TotalRequests:      100,
		SuccessfulRequests: 95,
		FailedRequests:     5,
		LatencyP50:         10.5,
		LatencyP95:         25.3,
		LatencyP99:         50.1,
		LatencyMean:        15.2,
		ThroughputRPS:      10.0,
		ServiceMetrics: map[string]*models.ServiceMetrics{
			"service1": {
				ServiceName:       "service1",
				RequestCount:      50,
				ErrorCount:        2,
				LatencyP50:        8.0,
				LatencyP95:        20.0,
				LatencyP99:        40.0,
				LatencyMean:       12.0,
				CPUUtilization:    0.75,
				MemoryUtilization: 0.60,
				ActiveReplicas:    3,
			},
			"service2": {
				ServiceName:       "service2",
				RequestCount:      50,
				ErrorCount:        3,
				LatencyP50:        12.0,
				LatencyP95:        30.0,
				LatencyP99:        60.0,
				LatencyMean:       18.0,
				CPUUtilization:    0.80,
				MemoryUtilization: 0.70,
				ActiveReplicas:    2,
			},
		},
	}

	pbMetrics := convertMetricsToProto(engineMetrics)

	if pbMetrics.TotalRequests != 100 {
		t.Fatalf("expected TotalRequests 100, got %d", pbMetrics.TotalRequests)
	}
	if pbMetrics.SuccessfulRequests != 95 {
		t.Fatalf("expected SuccessfulRequests 95, got %d", pbMetrics.SuccessfulRequests)
	}
	if pbMetrics.FailedRequests != 5 {
		t.Fatalf("expected FailedRequests 5, got %d", pbMetrics.FailedRequests)
	}
	if pbMetrics.LatencyP50Ms != 10.5 {
		t.Fatalf("expected LatencyP50Ms 10.5, got %f", pbMetrics.LatencyP50Ms)
	}
	if pbMetrics.ThroughputRps != 10.0 {
		t.Fatalf("expected ThroughputRps 10.0, got %f", pbMetrics.ThroughputRps)
	}

	if len(pbMetrics.ServiceMetrics) != 2 {
		t.Fatalf("expected 2 service metrics, got %d", len(pbMetrics.ServiceMetrics))
	}

	// Check service1 metrics
	svc1Found := false
	for _, svc := range pbMetrics.ServiceMetrics {
		if svc.ServiceName == "service1" {
			svc1Found = true
			if svc.RequestCount != 50 {
				t.Fatalf("expected service1 RequestCount 50, got %d", svc.RequestCount)
			}
			if svc.ActiveReplicas != 3 {
				t.Fatalf("expected service1 ActiveReplicas 3, got %d", svc.ActiveReplicas)
			}
			if svc.CpuUtilization != 0.75 {
				t.Fatalf("expected service1 CpuUtilization 0.75, got %f", svc.CpuUtilization)
			}
		}
	}
	if !svc1Found {
		t.Fatalf("expected service1 metrics to be present")
	}
}

func TestConvertMetricsToProtoWithNilServiceMetrics(t *testing.T) {
	engineMetrics := &models.RunMetrics{
		TotalRequests:      10,
		SuccessfulRequests: 10,
		FailedRequests:     0,
		LatencyP50:         5.0,
		LatencyP95:         10.0,
		LatencyP99:         20.0,
		LatencyMean:        6.0,
		ThroughputRPS:      5.0,
		ServiceMetrics:     nil,
	}

	pbMetrics := convertMetricsToProto(engineMetrics)

	if pbMetrics.TotalRequests != 10 {
		t.Fatalf("expected TotalRequests 10, got %d", pbMetrics.TotalRequests)
	}
	if len(pbMetrics.ServiceMetrics) != 0 {
		t.Fatalf("expected 0 service metrics, got %d", len(pbMetrics.ServiceMetrics))
	}
}

func TestRunExecutorResourceInitializationFailure(t *testing.T) {
	store := NewRunStore()
	invalidScenario := `
hosts: []
services:
  - id: svc1
    replicas: 1
    endpoints:
      - path: /test
        mean_cpu_ms: 10
`
	_, err := store.Create("run-1", &simulationv1.RunInput{
		ScenarioYaml: invalidScenario,
		DurationMs:   50,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	exec := NewRunExecutor(store)
	_, err := exec.Start("run-1")
	if err != nil {
		t.Fatalf("Start should not error immediately: %v", err)
	}

	// Wait for simulation to fail
	time.Sleep(200 * time.Millisecond)

	rec, ok := store.Get("run-1")
	if !ok {
		t.Fatalf("expected run to exist")
	}
	// Should be failed due to resource initialization error
	if rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_FAILED {
		t.Fatalf("expected failed status, got %v", rec.Run.Status)
	}
}

func TestRunExecutorMetricsConversionWithEmptyMetrics(t *testing.T) {
	store := NewRunStore()
	validScenario := `
hosts:
  - id: host-1
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
    arrival: {type: poisson, rate_rps: 0.1}
`
	_, err := store.Create("run-1", &simulationv1.RunInput{
		ScenarioYaml: validScenario,
		DurationMs:   50,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	exec := NewRunExecutor(store)
	_, err = exec.Start("run-1")
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}

	// Wait for completion
	time.Sleep(200 * time.Millisecond)

	rec, ok := store.Get("run-1")
	if !ok {
		t.Fatalf("expected run to exist")
	}
	if rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_COMPLETED {
		t.Fatalf("expected completed status, got %v", rec.Run.Status)
	}
	// Metrics should exist even with minimal workload
	if rec.Metrics == nil {
		t.Fatalf("expected metrics to exist")
	}
}
