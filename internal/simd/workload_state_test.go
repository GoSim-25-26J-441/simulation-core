package simd

import (
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestNewWorkloadState(t *testing.T) {
	eng := engine.NewEngine("test-run")
	endTime := time.Now().Add(10 * time.Second)

	ws := NewWorkloadState("test-run", eng, endTime)
	if ws == nil {
		t.Fatal("NewWorkloadState returned nil")
	}

	if ws.runID != "test-run" {
		t.Errorf("Expected runID 'test-run', got '%s'", ws.runID)
	}
	if ws.engine != eng {
		t.Error("Engine not set correctly")
	}
	if !ws.endTime.Equal(endTime) {
		t.Error("EndTime not set correctly")
	}
}

func TestWorkloadStateStart(t *testing.T) {
	eng := engine.NewEngine("test-run")
	startTime := eng.GetSimTime()
	endTime := startTime.Add(5 * time.Second)

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
		Workload: []config.WorkloadPattern{
			{
				From: "client",
				To:   "svc1:/test",
				Arrival: config.ArrivalSpec{
					Type:    "poisson",
					RateRPS: 10.0,
				},
			},
		},
	}

	ws := NewWorkloadState("test-run", eng, endTime)
	err := ws.Start(scenario, startTime)
	if err != nil {
		t.Fatalf("Start() returned error: %v", err)
	}

	// Check that patterns were initialized
	patterns := ws.GetAllPatterns()
	if len(patterns) != 1 {
		t.Fatalf("Expected 1 pattern, got %d", len(patterns))
	}

	patternKey := patternKey("client", "svc1:/test")
	patternState, ok := ws.GetPattern(patternKey)
	if !ok {
		t.Fatal("Pattern not found")
	}

	if patternState.ServiceID != "svc1" {
		t.Errorf("Expected ServiceID 'svc1', got '%s'", patternState.ServiceID)
	}
	if patternState.EndpointPath != "/test" {
		t.Errorf("Expected EndpointPath '/test', got '%s'", patternState.EndpointPath)
	}
	if !patternState.Active {
		t.Error("Pattern should be active")
	}

	// Cleanup
	ws.Stop()
}

func TestWorkloadStateUpdateRate(t *testing.T) {
	eng := engine.NewEngine("test-run")
	startTime := eng.GetSimTime()
	endTime := startTime.Add(5 * time.Second)

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
		Workload: []config.WorkloadPattern{
			{
				From: "client",
				To:   "svc1:/test",
				Arrival: config.ArrivalSpec{
					Type:    "poisson",
					RateRPS: 10.0,
				},
			},
		},
	}

	ws := NewWorkloadState("test-run", eng, endTime)
	err := ws.Start(scenario, startTime)
	if err != nil {
		t.Fatalf("Start() returned error: %v", err)
	}

	patternKey := patternKey("client", "svc1:/test")

	// Update rate
	newRate := 50.0
	err = ws.UpdateRate(patternKey, newRate)
	if err != nil {
		t.Fatalf("UpdateRate() returned error: %v", err)
	}

	// Verify rate was updated
	patternState, ok := ws.GetPattern(patternKey)
	if !ok {
		t.Fatal("Pattern not found")
	}

	if patternState.Pattern.Arrival.RateRPS != newRate {
		t.Errorf("Expected rate %f, got %f", newRate, patternState.Pattern.Arrival.RateRPS)
	}

	// Cleanup
	ws.Stop()
}

func TestWorkloadStateUpdateRateNotFound(t *testing.T) {
	eng := engine.NewEngine("test-run")
	endTime := time.Now().Add(5 * time.Second)

	ws := NewWorkloadState("test-run", eng, endTime)

	// Try to update non-existent pattern
	err := ws.UpdateRate("nonexistent:pattern", 50.0)
	if err == nil {
		t.Error("Expected error for non-existent pattern")
	}

	ws.Stop()
}

func TestWorkloadStateUpdateRateInvalidValues(t *testing.T) {
	eng := engine.NewEngine("test-run")
	startTime := eng.GetSimTime()
	endTime := startTime.Add(5 * time.Second)

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
		Workload: []config.WorkloadPattern{
			{
				From: "client",
				To:   "svc1:/test",
				Arrival: config.ArrivalSpec{
					Type:    "poisson",
					RateRPS: 10.0,
				},
			},
		},
	}

	ws := NewWorkloadState("test-run", eng, endTime)
	err := ws.Start(scenario, startTime)
	if err != nil {
		t.Fatalf("Start() returned error: %v", err)
	}

	patternKey := patternKey("client", "svc1:/test")

	// Test with negative rate - executor rejects rates <= 0, so this should return an error
	err = ws.UpdateRate(patternKey, -10.0)
	if err == nil {
		t.Errorf("UpdateRate() with negative value should return an error")
	}

	// Test with zero rate - executor rejects rates <= 0, so this should also return an error
	err = ws.UpdateRate(patternKey, 0.0)
	if err == nil {
		t.Errorf("UpdateRate() with zero value should return an error")
	}

	// Cleanup
	ws.Stop()
}

func TestWorkloadStateStop(t *testing.T) {
	eng := engine.NewEngine("test-run")
	startTime := eng.GetSimTime()
	endTime := startTime.Add(5 * time.Second)

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
		Workload: []config.WorkloadPattern{
			{
				From: "client",
				To:   "svc1:/test",
				Arrival: config.ArrivalSpec{
					Type:    "poisson",
					RateRPS: 10.0,
				},
			},
		},
	}

	ws := NewWorkloadState("test-run", eng, endTime)
	err := ws.Start(scenario, startTime)
	if err != nil {
		t.Fatalf("Start() returned error: %v", err)
	}

	// Stop should not panic
	ws.Stop()

	// Calling Stop multiple times should be safe
	ws.Stop()
}

func TestWorkloadStateUpdatePattern(t *testing.T) {
	eng := engine.NewEngine("test-run")
	startTime := eng.GetSimTime()
	endTime := startTime.Add(5 * time.Second)

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
		Workload: []config.WorkloadPattern{
			{
				From: "client",
				To:   "svc1:/test",
				Arrival: config.ArrivalSpec{
					Type:    "poisson",
					RateRPS: 10.0,
				},
			},
		},
	}

	ws := NewWorkloadState("test-run", eng, endTime)
	err := ws.Start(scenario, startTime)
	if err != nil {
		t.Fatalf("Start() returned error: %v", err)
	}

	patternKey := patternKey("client", "svc1:/test")
	newPattern := config.WorkloadPattern{
		From: "client",
		To:   "svc1:/test",
		Arrival: config.ArrivalSpec{
			Type:    "poisson",
			RateRPS: 25.0,
		},
	}

	err = ws.UpdatePattern(patternKey, newPattern)
	if err != nil {
		t.Fatalf("UpdatePattern() returned error: %v", err)
	}

	patternState, ok := ws.GetPattern(patternKey)
	if !ok {
		t.Fatal("Pattern not found")
	}
	if patternState.Pattern.Arrival.RateRPS != 25.0 {
		t.Errorf("Expected rate 25.0, got %f", patternState.Pattern.Arrival.RateRPS)
	}

	ws.Stop()
}

func TestWorkloadStateStartInvalidTarget(t *testing.T) {
	eng := engine.NewEngine("test-run")
	startTime := eng.GetSimTime()
	endTime := startTime.Add(5 * time.Second)

	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{ID: "svc1", Endpoints: []config.Endpoint{{Path: "/test"}}},
		},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1:", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 10}}, // empty path fails
		},
	}

	ws := NewWorkloadState("test-run", eng, endTime)
	err := ws.Start(scenario, startTime)
	if err == nil {
		t.Fatal("Expected error for invalid workload target")
	}
	ws.Stop()
}

func TestPatternKey(t *testing.T) {
	key := patternKey("client", "svc1:/test")
	expected := "client:svc1:/test"
	if key != expected {
		t.Errorf("Expected pattern key '%s', got '%s'", expected, key)
	}
}
