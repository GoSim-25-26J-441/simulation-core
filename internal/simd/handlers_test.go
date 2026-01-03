package simd

import (
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestNewScenarioState(t *testing.T) {
	scenario := &config.Scenario{
		Services: []config.Service{
			{
				ID:   "svc1",
				Endpoints: []config.Endpoint{
					{Path: "/test"},
				},
			},
		},
	}

	state := newScenarioState(scenario)
	if state == nil {
		t.Fatalf("expected non-nil state")
	}
	if state.services["svc1"] == nil {
		t.Fatalf("expected service to be in map")
	}
	if state.endpoints["svc1:/test"] == nil {
		t.Fatalf("expected endpoint to be in map")
	}
}

func TestParseWorkloadTarget(t *testing.T) {
	tests := []struct {
		name        string
		target      string
		expectError bool
		serviceID   string
		path        string
	}{
		{"valid", "svc1:/path", false, "svc1", "/path"},
		{"valid with colon in path", "svc1:/api/v1/test", false, "svc1", "/api/v1/test"},
		{"invalid no colon", "svc1", true, "", ""},
		{"invalid empty", "", true, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serviceID, path, err := parseWorkloadTarget(tt.target)
			if tt.expectError {
				if err == nil {
					t.Fatalf("expected error")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if serviceID != tt.serviceID {
					t.Fatalf("expected serviceID %s, got %s", tt.serviceID, serviceID)
				}
				if path != tt.path {
					t.Fatalf("expected path %s, got %s", tt.path, path)
				}
			}
		})
	}
}

func TestRegisterHandlers(t *testing.T) {
	eng := engine.NewEngine("test-run")
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{
				ID:   "svc1",
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
		Workload: []config.WorkloadPattern{
			{
				From: "client",
				To:   "svc1:/test",
				Arrival: config.ArrivalSpec{
					Type:    "poisson",
					RateRPS: 100, // Higher rate to ensure events are scheduled
				},
			},
		},
	}

	state := newScenarioState(scenario)
	RegisterHandlers(eng, state)

	// Verify handlers are registered by checking if they exist
	// We can't directly check, but we can trigger an event and see if it's handled
	// For now, just verify no panic
}

func TestScheduleWorkload(t *testing.T) {
	eng := engine.NewEngine("test-run")
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{
				ID:   "svc1",
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
		Workload: []config.WorkloadPattern{
			{
				From: "client",
				To:   "svc1:/test",
				Arrival: config.ArrivalSpec{
					Type:    "poisson",
					RateRPS: 100, // Higher rate to ensure events are scheduled
				},
			},
		},
	}

	err := ScheduleWorkload(eng, scenario, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("ScheduleWorkload error: %v", err)
	}

	// Verify events were scheduled (with higher rate and longer duration, should have events)
	queueSize := eng.GetEventQueue().Size()
	if queueSize == 0 {
		t.Fatalf("expected events to be scheduled, got queue size %d", queueSize)
	}
}

func TestScheduleWorkloadUniform(t *testing.T) {
	eng := engine.NewEngine("test-run")
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{
				ID:   "svc1",
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
		Workload: []config.WorkloadPattern{
			{
				From: "client",
				To:   "svc1:/test",
				Arrival: config.ArrivalSpec{
					Type:    "uniform",
					RateRPS: 10,
				},
			},
		},
	}

	err := ScheduleWorkload(eng, scenario, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("ScheduleWorkload error: %v", err)
	}

	// Verify events were scheduled (with higher rate and longer duration, should have events)
	queueSize := eng.GetEventQueue().Size()
	if queueSize == 0 {
		t.Fatalf("expected events to be scheduled, got queue size %d", queueSize)
	}
}

func TestScheduleWorkloadInvalidTarget(t *testing.T) {
	eng := engine.NewEngine("test-run")
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{
				ID:   "svc1",
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
		Workload: []config.WorkloadPattern{
			{
				From: "client",
				To:   "invalid", // Missing colon
				Arrival: config.ArrivalSpec{
					Type:    "poisson",
					RateRPS: 100, // Higher rate to ensure events are scheduled
				},
			},
		},
	}

	err := ScheduleWorkload(eng, scenario, 100*time.Millisecond)
	if err == nil {
		t.Fatalf("expected error for invalid target")
	}
}

func TestScheduleWorkloadDefaultArrivalType(t *testing.T) {
	eng := engine.NewEngine("test-run")
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{
				ID:   "svc1",
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
		Workload: []config.WorkloadPattern{
			{
				From: "client",
				To:   "svc1:/test",
				Arrival: config.ArrivalSpec{
					Type:    "unknown", // Unknown type defaults to poisson
					RateRPS: 10,
				},
			},
		},
	}

	err := ScheduleWorkload(eng, scenario, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("ScheduleWorkload error: %v", err)
	}
}

