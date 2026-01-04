package simd

import (
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

func TestHandleRequestArrival(t *testing.T) {
	eng := engine.NewEngine("test-run")
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{
				ID: "svc1",
				Endpoints: []config.Endpoint{
					{
						Path:         "/test",
						MeanCPUMs:    10,
						CPUSigmaMs:   2,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0.5},
					},
				},
			},
		},
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("failed to initialize resource manager: %v", err)
	}
	state := newScenarioState(scenario, rm)
	RegisterHandlers(eng, state)

	// Create arrival event
	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "svc1", map[string]interface{}{
		"service_id":    "svc1",
		"endpoint_path": "/test",
	})

	// Run for a short time to process the event
	err := eng.Run(50 * time.Millisecond)
	if err != nil {
		t.Fatalf("Engine run error: %v", err)
	}

	// Check that a request was created
	runMgr := eng.GetRunManager()
	stats := runMgr.GetStats()
	if stats["total_requests"].(int) == 0 {
		t.Fatalf("expected at least one request")
	}
}

func TestHandleRequestArrivalMissingData(t *testing.T) {
	eng := engine.NewEngine("test-run")
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{
				ID: "svc1",
				Endpoints: []config.Endpoint{
					{
						Path:         "/test",
						MeanCPUMs:    10,
						CPUSigmaMs:   2,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0.5},
					},
				},
			},
		},
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("failed to initialize resource manager: %v", err)
	}
	state := newScenarioState(scenario, rm)
	RegisterHandlers(eng, state)

	// Create arrival event with missing service_id
	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "svc1", map[string]interface{}{
		"endpoint_path": "/test",
		// Missing service_id
	})

	// Run for a short time
	err := eng.Run(50 * time.Millisecond)
	// Should complete but handler should log error
	if err != nil {
		t.Fatalf("Engine run should complete even with handler error")
	}
}

func TestHandleRequestStart(t *testing.T) {
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
						MeanCPUMs:    10,
						CPUSigmaMs:   2,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0.5},
					},
				},
			},
		},
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("failed to initialize resource manager: %v", err)
	}
	state := newScenarioState(scenario, rm)
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

	// Get instance ID for the service
	instance, err := state.rm.SelectInstanceForService("svc1")
	if err != nil {
		t.Fatalf("failed to get instance: %v", err)
	}

	// Schedule request start with instance ID
	eng.ScheduleAt(engine.EventTypeRequestStart, eng.GetSimTime(), request, "svc1", map[string]interface{}{
		"endpoint_path": "/test",
		"instance_id":   instance.ID(),
	})

	// Run for a short time
	err = eng.Run(50 * time.Millisecond)
	if err != nil {
		t.Fatalf("Engine run error: %v", err)
	}

	// Check request was processed
	req, ok := eng.GetRunManager().GetRequest("req-1")
	if !ok {
		t.Fatalf("expected request to exist")
	}
	if req.Status != models.RequestStatusProcessing && req.Status != models.RequestStatusCompleted {
		t.Fatalf("expected request to be processing or completed, got %v", req.Status)
	}
}

func TestHandleRequestCompleteWithDownstream(t *testing.T) {
	eng := engine.NewEngine("test-run")
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{
				ID: "svc1",
				Endpoints: []config.Endpoint{
					{
						Path:         "/test",
						MeanCPUMs:    10,
						CPUSigmaMs:   2,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0.5},
						Downstream: []config.DownstreamCall{
							{To: "svc2:/api"},
						},
					},
				},
			},
			{
				ID: "svc2",
				Endpoints: []config.Endpoint{
					{
						Path:         "/api",
						MeanCPUMs:    5,
						CPUSigmaMs:   1,
						NetLatencyMs: config.LatencySpec{Mean: 0.5, Sigma: 0.2},
					},
				},
			},
		},
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("failed to initialize resource manager: %v", err)
	}
	state := newScenarioState(scenario, rm)
	RegisterHandlers(eng, state)

	// Create a request
	request := &models.Request{
		ID:          "req-1",
		TraceID:     "trace-1",
		ServiceName: "svc1",
		Endpoint:    "/test",
		Status:      models.RequestStatusProcessing,
		ArrivalTime: eng.GetSimTime(),
		StartTime:   eng.GetSimTime(),
		Metadata:    make(map[string]interface{}),
	}
	eng.GetRunManager().AddRequest(request)

	// Schedule request complete
	eng.ScheduleAt(engine.EventTypeRequestComplete, eng.GetSimTime(), request, "svc1", map[string]interface{}{
		"endpoint_path": "/test",
	})

	// Run for a short time
	err := eng.Run(100 * time.Millisecond)
	if err != nil {
		t.Fatalf("Engine run error: %v", err)
	}

	// Check request was completed
	req, ok := eng.GetRunManager().GetRequest("req-1")
	if !ok {
		t.Fatalf("expected request to exist")
	}
	if req.Status != models.RequestStatusCompleted {
		t.Fatalf("expected request to be completed, got %v", req.Status)
	}
}

func TestHandleDownstreamCall(t *testing.T) {
	eng := engine.NewEngine("test-run")
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{
				ID: "svc1",
				Endpoints: []config.Endpoint{
					{
						Path:         "/test",
						MeanCPUMs:    10,
						CPUSigmaMs:   2,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0.5},
					},
				},
			},
		},
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("failed to initialize resource manager: %v", err)
	}
	state := newScenarioState(scenario, rm)
	RegisterHandlers(eng, state)

	// Create parent request
	parentRequest := &models.Request{
		ID:          "req-1",
		TraceID:     "trace-1",
		ServiceName: "svc1",
		Endpoint:    "/test",
		Status:      models.RequestStatusCompleted,
		ArrivalTime: eng.GetSimTime(),
		Metadata:    make(map[string]interface{}),
	}
	eng.GetRunManager().AddRequest(parentRequest)

	// Schedule downstream call
	eng.ScheduleAt(engine.EventTypeDownstreamCall, eng.GetSimTime(), parentRequest, "svc2", map[string]interface{}{
		"endpoint_path": "/api",
	})

	// Run for a short time
	err := eng.Run(50 * time.Millisecond)
	if err != nil {
		t.Fatalf("Engine run error: %v", err)
	}
}

func TestConvertMetricsToProto(t *testing.T) {
	engineMetrics := &models.RunMetrics{
		TotalRequests:      100,
		SuccessfulRequests: 95,
		FailedRequests:     5,
		LatencyP50:         50.0,
		LatencyP95:         100.0,
		LatencyP99:         200.0,
		LatencyMean:        75.0,
		ThroughputRPS:      10.5,
		ServiceMetrics: map[string]*models.ServiceMetrics{
			"svc1": {
				ServiceName:       "svc1",
				RequestCount:      50,
				ErrorCount:        2,
				LatencyP50:        45.0,
				LatencyP95:        90.0,
				LatencyP99:        180.0,
				LatencyMean:       70.0,
				CPUUtilization:    0.75,
				MemoryUtilization: 0.60,
				ActiveReplicas:    3,
			},
		},
	}

	pbMetrics := convertMetricsToProto(engineMetrics)
	if pbMetrics == nil {
		t.Fatalf("expected non-nil metrics")
	}
	if pbMetrics.TotalRequests != 100 {
		t.Fatalf("expected TotalRequests 100, got %d", pbMetrics.TotalRequests)
	}
	if len(pbMetrics.ServiceMetrics) != 1 {
		t.Fatalf("expected 1 service metric, got %d", len(pbMetrics.ServiceMetrics))
	}
	if pbMetrics.ServiceMetrics[0].ActiveReplicas != 3 {
		t.Fatalf("expected ActiveReplicas 3, got %d", pbMetrics.ServiceMetrics[0].ActiveReplicas)
	}
}

func TestConvertMetricsToProtoWithLargeReplicas(t *testing.T) {
	engineMetrics := &models.RunMetrics{
		ServiceMetrics: map[string]*models.ServiceMetrics{
			"svc1": {
				ServiceName:    "svc1",
				ActiveReplicas: 3000000000, // Very large number > MaxInt32 (2147483647)
			},
		},
	}

	pbMetrics := convertMetricsToProto(engineMetrics)
	if pbMetrics.ServiceMetrics[0].ActiveReplicas != 2147483647 { // math.MaxInt32
		t.Fatalf("expected ActiveReplicas to be clamped to MaxInt32, got %d", pbMetrics.ServiceMetrics[0].ActiveReplicas)
	}
}

func TestHandleRequestCompleteWithoutDownstream(t *testing.T) {
	eng := engine.NewEngine("test-run")
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{
				ID: "svc1",
				Endpoints: []config.Endpoint{
					{
						Path:         "/test",
						MeanCPUMs:    10,
						CPUSigmaMs:   2,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0.5},
						Downstream:   []config.DownstreamCall{}, // No downstream
					},
				},
			},
		},
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("failed to initialize resource manager: %v", err)
	}
	state := newScenarioState(scenario, rm)
	RegisterHandlers(eng, state)

	// Create a request
	request := &models.Request{
		ID:          "req-1",
		TraceID:     "trace-1",
		ServiceName: "svc1",
		Endpoint:    "/test",
		Status:      models.RequestStatusProcessing,
		ArrivalTime: eng.GetSimTime(),
		StartTime:   eng.GetSimTime(),
		Metadata:    make(map[string]interface{}),
	}
	eng.GetRunManager().AddRequest(request)

	// Schedule request complete
	eng.ScheduleAt(engine.EventTypeRequestComplete, eng.GetSimTime(), request, "svc1", map[string]interface{}{
		"endpoint_path": "/test",
	})

	// Run for a short time
	err := eng.Run(50 * time.Millisecond)
	if err != nil {
		t.Fatalf("Engine run error: %v", err)
	}

	// Check request was completed
	req, ok := eng.GetRunManager().GetRequest("req-1")
	if !ok {
		t.Fatalf("expected request to exist")
	}
	if req.Status != models.RequestStatusCompleted {
		t.Fatalf("expected request to be completed, got %v", req.Status)
	}
}

func TestHandleRequestCompleteWithNonExistentEndpoint(t *testing.T) {
	eng := engine.NewEngine("test-run")
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{
				ID: "svc1",
				Endpoints: []config.Endpoint{
					{
						Path:         "/test",
						MeanCPUMs:    10,
						CPUSigmaMs:   2,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0.5},
					},
				},
			},
		},
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("failed to initialize resource manager: %v", err)
	}
	state := newScenarioState(scenario, rm)
	RegisterHandlers(eng, state)

	// Create a request with endpoint that doesn't exist in state
	request := &models.Request{
		ID:          "req-1",
		TraceID:     "trace-1",
		ServiceName: "svc1",
		Endpoint:    "/nonexistent",
		Status:      models.RequestStatusProcessing,
		ArrivalTime: eng.GetSimTime(),
		StartTime:   eng.GetSimTime(),
		Metadata:    make(map[string]interface{}),
	}
	eng.GetRunManager().AddRequest(request)

	// Schedule request complete
	eng.ScheduleAt(engine.EventTypeRequestComplete, eng.GetSimTime(), request, "svc1", map[string]interface{}{
		"endpoint_path": "/nonexistent",
	})

	// Run for a short time - should complete without error
	err := eng.Run(50 * time.Millisecond)
	if err != nil {
		t.Fatalf("Engine run error: %v", err)
	}
}

func TestHandleDownstreamCallWithMissingEndpointPath(t *testing.T) {
	eng := engine.NewEngine("test-run")
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{
				ID: "svc1",
				Endpoints: []config.Endpoint{
					{
						Path:         "/test",
						MeanCPUMs:    10,
						CPUSigmaMs:   2,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0.5},
					},
				},
			},
		},
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("failed to initialize resource manager: %v", err)
	}
	state := newScenarioState(scenario, rm)
	RegisterHandlers(eng, state)

	// Create parent request
	parentRequest := &models.Request{
		ID:          "req-1",
		TraceID:     "trace-1",
		ServiceName: "svc1",
		Endpoint:    "/test",
		Status:      models.RequestStatusCompleted,
		ArrivalTime: eng.GetSimTime(),
		Metadata:    make(map[string]interface{}),
	}
	eng.GetRunManager().AddRequest(parentRequest)

	// Schedule downstream call without endpoint_path (should default to "/")
	eng.ScheduleAt(engine.EventTypeDownstreamCall, eng.GetSimTime(), parentRequest, "svc1", map[string]interface{}{
		// Missing endpoint_path
	})

	// Run for a short time
	err := eng.Run(50 * time.Millisecond)
	if err != nil {
		t.Fatalf("Engine run error: %v", err)
	}
}

func TestConvertMetricsToProtoWithNegativeReplicas(t *testing.T) {
	engineMetrics := &models.RunMetrics{
		ServiceMetrics: map[string]*models.ServiceMetrics{
			"svc1": {
				ActiveReplicas: -5, // Negative number
			},
		},
	}

	pbMetrics := convertMetricsToProto(engineMetrics)
	if pbMetrics.ServiceMetrics[0].ActiveReplicas != 0 {
		t.Fatalf("expected ActiveReplicas to be clamped to 0")
	}
}
