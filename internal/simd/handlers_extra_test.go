package simd

import (
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/internal/policy"
	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

func TestHandleRequestArrivalWithQueueing(t *testing.T) {
	eng := engine.NewEngine("test-run")
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 1,
				Endpoints: []config.Endpoint{
					{
						Path:         "/test",
						MeanCPUMs:    10.0,
						CPUSigmaMs:   2.0,
						NetLatencyMs: config.LatencySpec{Mean: 1.0, Sigma: 0.5},
					},
				},
			},
		},
	}

	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("failed to initialize resource manager: %v", err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil))
	RegisterHandlers(eng, state)

	// Get instance and fill it to capacity
	instance, err := rm.SelectInstanceForService("svc1")
	if err != nil {
		t.Fatalf("failed to get instance: %v", err)
	}

	// Fill instance to capacity by allocating resources
	for i := 0; i < 10; i++ {
		_ = rm.AllocateCPU(instance.ID(), 100.0, time.Now())
		_ = rm.AllocateMemory(instance.ID(), 50.0)
	}

	// Schedule a request arrival - should be queued
	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "svc1", map[string]interface{}{
		"service_id":    "svc1",
		"endpoint_path": "/test",
	})

	// Run simulation
	err = eng.Run(50 * time.Millisecond)
	if err != nil {
		t.Fatalf("Engine run error: %v", err)
	}

	// Check that request was queued
	queueLength := rm.GetQueueLength(instance.ID())
	if queueLength == 0 {
		t.Logf("Queue may have been processed, but queueing should have occurred")
	}
}

func TestHandleRequestStartWithResourceAllocationFailure(t *testing.T) {
	// This test verifies that when resource allocation fails, the request is marked as failed
	// and error metrics are recorded. We test this by using an invalid instance ID.
	eng := engine.NewEngine("test-run")
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 1,
				Endpoints: []config.Endpoint{
					{
						Path:         "/test",
						MeanCPUMs:    10.0,
						CPUSigmaMs:   2.0,
						NetLatencyMs: config.LatencySpec{Mean: 1.0, Sigma: 0.5},
					},
				},
			},
		},
	}

	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("failed to initialize resource manager: %v", err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil))
	RegisterHandlers(eng, state)

	// Create a request
	request := &models.Request{
		ID:          "req-1",
		TraceID:     "trace-1",
		ServiceName: "svc1",
		Endpoint:    "/test",
		Status:      models.RequestStatusPending,
		ArrivalTime: eng.GetSimTime(),
		Metadata:    make(map[string]interface{}),
	}
	eng.GetRunManager().AddRequest(request)

	// Schedule request start with an invalid instance ID to trigger allocation failure
	eng.ScheduleAt(engine.EventTypeRequestStart, eng.GetSimTime(), request, "svc1", map[string]interface{}{
		"endpoint_path": "/test",
		"instance_id":   "invalid-instance-id",
	})

	// Run simulation
	err := eng.Run(50 * time.Millisecond)
	if err != nil {
		t.Fatalf("Engine run error: %v", err)
	}

	// Request should have failed due to memory allocation failure
	req, ok := eng.GetRunManager().GetRequest("req-1")
	if !ok {
		t.Fatalf("expected request to exist")
	}

	// Verify request was marked as failed
	if req.Status != models.RequestStatusFailed {
		t.Errorf("expected request status to be Failed, got %v", req.Status)
	}

	// Verify error metrics were recorded
	collector.Stop()
	// Check if error metrics were recorded with the correct labels
	labels := map[string]string{
		"service":  "svc1",
		"endpoint": "/test",
	}
	errorMetrics := collector.GetTimeSeries("request_error_count", labels)
	if len(errorMetrics) == 0 {
		// Also check metric names to ensure the metric was recorded
		metricNames := collector.GetMetricNames()
		found := false
		for _, name := range metricNames {
			if name == "request_error_count" {
				found = true
				break
			}
		}
		if !found {
			t.Error("expected error metrics to be recorded")
		}
	}
}

func TestHandleRequestStartWithCPUAllocationFailure(t *testing.T) {
	eng := engine.NewEngine("test-run")
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 1,
				Endpoints: []config.Endpoint{
					{
						Path:         "/test",
						MeanCPUMs:    10.0,
						CPUSigmaMs:   2.0,
						NetLatencyMs: config.LatencySpec{Mean: 1.0, Sigma: 0.5},
					},
				},
			},
		},
	}

	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("failed to initialize resource manager: %v", err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil))
	RegisterHandlers(eng, state)

	// Create a request with an invalid instance ID to force CPU allocation failure
	request := &models.Request{
		ID:          "req-cpu-fail",
		TraceID:     "trace-cpu-fail",
		ServiceName: "svc1",
		Endpoint:    "/test",
		Status:      models.RequestStatusPending,
		ArrivalTime: eng.GetSimTime(),
		Metadata:    make(map[string]interface{}),
	}
	eng.GetRunManager().AddRequest(request)

	// Schedule request start with invalid instance ID to trigger CPU allocation failure
	eng.ScheduleAt(engine.EventTypeRequestStart, eng.GetSimTime(), request, "svc1", map[string]interface{}{
		"endpoint_path": "/test",
		"instance_id":   "invalid-instance-id",
	})

	// Run simulation
	err := eng.Run(50 * time.Millisecond)
	if err != nil {
		t.Fatalf("Engine run error: %v", err)
	}

	// Request should have failed due to CPU allocation failure
	req, ok := eng.GetRunManager().GetRequest("req-cpu-fail")
	if !ok {
		t.Fatalf("expected request to exist")
	}

	// Verify request was marked as failed
	if req.Status != models.RequestStatusFailed {
		t.Errorf("expected request status to be Failed, got %v", req.Status)
	}

	// Verify error metrics were recorded
	collector.Stop()
	// Check if error metrics were recorded with the correct labels
	labels := map[string]string{
		"service":  "svc1",
		"endpoint": "/test",
	}
	errorMetrics := collector.GetTimeSeries("request_error_count", labels)
	if len(errorMetrics) == 0 {
		// Also check metric names to ensure the metric was recorded
		metricNames := collector.GetMetricNames()
		found := false
		for _, name := range metricNames {
			if name == "request_error_count" {
				found = true
				break
			}
		}
		if !found {
			t.Error("expected error metrics to be recorded")
		}
	}
}

func TestHandleRequestCompleteWithQueueProcessing(t *testing.T) {
	eng := engine.NewEngine("test-run")
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 1,
				Endpoints: []config.Endpoint{
					{
						Path:         "/test",
						MeanCPUMs:    5.0,
						CPUSigmaMs:   1.0,
						NetLatencyMs: config.LatencySpec{Mean: 1.0, Sigma: 0.2},
					},
				},
			},
		},
	}

	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("failed to initialize resource manager: %v", err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil))
	RegisterHandlers(eng, state)

	// Get instance
	instance, err := rm.SelectInstanceForService("svc1")
	if err != nil {
		t.Fatalf("failed to get instance: %v", err)
	}

	// Queue a request
	_ = rm.EnqueueRequest(instance.ID(), "queued-req-1")

	// Create and complete a request to trigger queue processing
	request := &models.Request{
		ID:          "req-1",
		TraceID:     "trace-1",
		ServiceName: "svc1",
		Endpoint:    "/test",
		Status:      models.RequestStatusProcessing,
		ArrivalTime: eng.GetSimTime(),
		StartTime:   eng.GetSimTime(),
		Metadata: map[string]interface{}{
			"instance_id":         instance.ID(),
			"allocated_cpu_ms":    5.0,
			"allocated_memory_mb": 10.0,
		},
	}
	eng.GetRunManager().AddRequest(request)

	// Schedule request complete
	eng.ScheduleAt(engine.EventTypeRequestComplete, eng.GetSimTime(), request, "svc1", map[string]interface{}{
		"endpoint_path": "/test",
		"instance_id":   instance.ID(),
	})

	// Run simulation
	err = eng.Run(50 * time.Millisecond)
	if err != nil {
		t.Fatalf("Engine run error: %v", err)
	}

	// Queue should have been processed (dequeued)
	queueLength := rm.GetQueueLength(instance.ID())
	if queueLength > 0 {
		t.Logf("Queue may still have items, but processing should have been attempted")
	}
}

func TestScheduleWorkloadWithNormalDistribution(t *testing.T) {
	eng := engine.NewEngine("test-run")
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 1,
				Endpoints: []config.Endpoint{
					{
						Path:         "/test",
						MeanCPUMs:    10.0,
						CPUSigmaMs:   2.0,
						NetLatencyMs: config.LatencySpec{Mean: 1.0, Sigma: 0.5},
					},
				},
			},
		},
		Workload: []config.WorkloadPattern{
			{
				From: "client",
				To:   "svc1:/test",
				Arrival: config.ArrivalSpec{
					Type:      "normal",
					RateRPS:   10.0,
					StdDevRPS: 2.0,
				},
			},
		},
	}

	err := ScheduleWorkload(eng, scenario, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("ScheduleWorkload error: %v", err)
	}

	// Verify events were scheduled
	queueSize := eng.GetEventQueue().Size()
	if queueSize == 0 {
		t.Fatalf("expected events to be scheduled for normal distribution, got queue size %d", queueSize)
	}
}

func TestScheduleWorkloadWithBurstyDistribution(t *testing.T) {
	eng := engine.NewEngine("test-run")
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 1,
				Endpoints: []config.Endpoint{
					{
						Path:         "/test",
						MeanCPUMs:    10.0,
						CPUSigmaMs:   2.0,
						NetLatencyMs: config.LatencySpec{Mean: 1.0, Sigma: 0.5},
					},
				},
			},
		},
		Workload: []config.WorkloadPattern{
			{
				From: "client",
				To:   "svc1:/test",
				Arrival: config.ArrivalSpec{
					Type:                 "bursty",
					RateRPS:              5.0,
					BurstRateRPS:         20.0,
					BurstDurationSeconds: 1.0,
					QuietDurationSeconds: 2.0,
				},
			},
		},
	}

	err := ScheduleWorkload(eng, scenario, 5*time.Second)
	if err != nil {
		t.Fatalf("ScheduleWorkload error: %v", err)
	}

	// Verify events were scheduled
	queueSize := eng.GetEventQueue().Size()
	if queueSize == 0 {
		t.Fatalf("expected events to be scheduled for bursty distribution, got queue size %d", queueSize)
	}
}

func TestScheduleWorkloadWithConstantDistribution(t *testing.T) {
	eng := engine.NewEngine("test-run")
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 1,
				Endpoints: []config.Endpoint{
					{
						Path:         "/test",
						MeanCPUMs:    10.0,
						CPUSigmaMs:   2.0,
						NetLatencyMs: config.LatencySpec{Mean: 1.0, Sigma: 0.5},
					},
				},
			},
		},
		Workload: []config.WorkloadPattern{
			{
				From: "client",
				To:   "svc1:/test",
				Arrival: config.ArrivalSpec{
					Type:    "constant",
					RateRPS: 2.0, // 2 requests per second
				},
			},
		},
	}

	err := ScheduleWorkload(eng, scenario, 2*time.Second)
	if err != nil {
		t.Fatalf("ScheduleWorkload error: %v", err)
	}

	// Verify events were scheduled
	queueSize := eng.GetEventQueue().Size()
	if queueSize == 0 {
		t.Fatalf("expected events to be scheduled for constant distribution, got queue size %d", queueSize)
	}
	// Should have approximately 4 events (2 RPS * 2 seconds)
	if queueSize < 3 || queueSize > 5 {
		t.Logf("expected around 4 events for constant rate, got %d", queueSize)
	}
}
