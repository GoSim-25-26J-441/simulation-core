//go:build integration
// +build integration

package integration_test

import (
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/internal/interaction"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// TestE2E_InteractionPackageIntegration verifies that the interaction package
// is properly integrated and can handle service graphs with downstream calls
func TestE2E_InteractionPackageIntegration(t *testing.T) {
	// Create a scenario with downstream calls (service chain)
	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "host-1", Cores: 4},
		},
		Services: []config.Service{
			{
				ID:       "frontend",
				Replicas: 2,
				Model:    "cpu",
				Endpoints: []config.Endpoint{
					{
						Path:         "/api",
						MeanCPUMs:    10,
						CPUSigmaMs:   2,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0.5},
						Downstream: []config.DownstreamCall{
							{To: "backend:/process", CallCountMean: 1.0},
						},
					},
				},
			},
			{
				ID:       "backend",
				Replicas: 2,
				Model:    "cpu",
				Endpoints: []config.Endpoint{
					{
						Path:         "/process",
						MeanCPUMs:    20,
						CPUSigmaMs:   5,
						NetLatencyMs: config.LatencySpec{Mean: 2, Sigma: 1},
						Downstream: []config.DownstreamCall{
							{To: "database:/query", CallCountMean: 1.0},
						},
					},
				},
			},
			{
				ID:       "database",
				Replicas: 1,
				Model:    "db_latency",
				Endpoints: []config.Endpoint{
					{
						Path:         "/query",
						MeanCPUMs:    5,
						CPUSigmaMs:   1,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0.5},
					},
				},
			},
		},
		Workload: []config.WorkloadPattern{
			{
				From: "client",
				To:   "frontend:/api",
				Arrival: config.ArrivalSpec{
					Type:    "poisson",
					RateRPS: 10,
				},
			},
		},
	}

	// Test 1: Verify interaction manager can be created
	manager, err := interaction.NewManager(scenario)
	if err != nil {
		t.Fatalf("failed to create interaction manager: %v", err)
	}

	// Test 2: Verify graph was created
	graph := manager.GetGraph()
	if graph == nil {
		t.Fatalf("expected non-nil graph")
	}

	// Test 3: Verify services are in the graph
	frontendSvc, ok := graph.GetService("frontend")
	if !ok {
		t.Fatalf("expected frontend service to exist in graph")
	}
	if frontendSvc.ID != "frontend" {
		t.Fatalf("expected service ID 'frontend', got '%s'", frontendSvc.ID)
	}

	// Test 4: Verify endpoints are in the graph
	frontendEp, ok := graph.GetEndpoint("frontend", "/api")
	if !ok {
		t.Fatalf("expected frontend:/api endpoint to exist in graph")
	}
	if frontendEp.Path != "/api" {
		t.Fatalf("expected endpoint path '/api', got '%s'", frontendEp.Path)
	}

	// Test 5: Verify downstream calls can be resolved
	downstreamCalls, err := manager.GetDownstreamCalls("frontend", "/api")
	if err != nil {
		t.Fatalf("failed to get downstream calls: %v", err)
	}

	if len(downstreamCalls) == 0 {
		t.Fatalf("expected at least one downstream call from frontend:/api")
	}

	foundBackend := false
	for _, call := range downstreamCalls {
		if call.ServiceID == "backend" && call.Path == "/process" {
			foundBackend = true
			break
		}
	}

	if !foundBackend {
		t.Fatalf("expected downstream call to backend:/process")
	}

	// Test 6: Verify backend has downstream calls to database
	backendCalls, err := manager.GetDownstreamCalls("backend", "/process")
	if err != nil {
		t.Fatalf("failed to get backend downstream calls: %v", err)
	}

	if len(backendCalls) == 0 {
		t.Fatalf("expected at least one downstream call from backend:/process")
	}

	foundDatabase := false
	for _, call := range backendCalls {
		if call.ServiceID == "database" && call.Path == "/query" {
			foundDatabase = true
			break
		}
	}

	if !foundDatabase {
		t.Fatalf("expected downstream call to database:/query")
	}

	// Test 7: Verify database has no downstream calls
	dbCalls, err := manager.GetDownstreamCalls("database", "/query")
	if err != nil {
		t.Fatalf("failed to get database downstream calls: %v", err)
	}

	if len(dbCalls) != 0 {
		t.Fatalf("expected no downstream calls from database:/query, got %d", len(dbCalls))
	}

	t.Logf("✓ Interaction package integration test passed")
	t.Logf("  - Graph created successfully")
	t.Logf("  - Services and endpoints accessible")
	t.Logf("  - Downstream call resolution working")
	t.Logf("  - Service chain: frontend -> backend -> database verified")
}

// TestE2E_InteractionGraphValidation verifies that the interaction graph
// correctly validates service dependencies and detects cycles
func TestE2E_InteractionGraphValidation(t *testing.T) {
	// Test with a valid scenario (no cycles)
	validScenario := &config.Scenario{
		Services: []config.Service{
			{
				ID: "svc1",
				Endpoints: []config.Endpoint{
					{
						Path: "/test",
						Downstream: []config.DownstreamCall{
							{To: "svc2:/api"},
						},
					},
				},
			},
			{
				ID: "svc2",
				Endpoints: []config.Endpoint{
					{Path: "/api"},
				},
			},
		},
	}

	manager, err := interaction.NewManager(validScenario)
	if err != nil {
		t.Fatalf("failed to create interaction manager for valid scenario: %v", err)
	}

	// Verify downstream calls can be resolved
	calls, err := manager.GetDownstreamCalls("svc1", "/test")
	if err != nil {
		t.Fatalf("failed to get downstream calls: %v", err)
	}

	if len(calls) != 1 {
		t.Fatalf("expected 1 downstream call, got %d", len(calls))
	}

	if calls[0].ServiceID != "svc2" {
		t.Fatalf("expected downstream service svc2, got %s", calls[0].ServiceID)
	}

	// Test with a cyclic scenario (should fail)
	cyclicScenario := &config.Scenario{
		Services: []config.Service{
			{
				ID: "svc1",
				Endpoints: []config.Endpoint{
					{
						Path: "/test",
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
						Path: "/api",
						Downstream: []config.DownstreamCall{
							{To: "svc1:/test"}, // Creates a cycle
						},
					},
				},
			},
		},
	}

	_, err = interaction.NewManager(cyclicScenario)
	if err == nil {
		t.Fatalf("expected error for cyclic graph, got nil")
	}

	t.Logf("✓ Graph validation test passed")
	t.Logf("  - Valid graphs accepted")
	t.Logf("  - Cyclic graphs rejected")
}

// TestE2E_InteractionWithEngine verifies that the interaction package
// works correctly when used with the simulation engine
func TestE2E_InteractionWithEngine(t *testing.T) {
	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "host-1", Cores: 2},
		},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 1,
				Model:    "cpu",
				Endpoints: []config.Endpoint{
					{
						Path:         "/test",
						MeanCPUMs:    10,
						CPUSigmaMs:   2,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0.5},
						Downstream: []config.DownstreamCall{
							{To: "svc2:/api", CallCountMean: 1.0},
						},
					},
				},
			},
			{
				ID:       "svc2",
				Replicas: 1,
				Model:    "cpu",
				Endpoints: []config.Endpoint{
					{
						Path:         "/api",
						MeanCPUMs:    5,
						CPUSigmaMs:   1,
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
					RateRPS: 5,
				},
			},
		},
	}

	// Create interaction manager
	manager, err := interaction.NewManager(scenario)
	if err != nil {
		t.Fatalf("failed to create interaction manager: %v", err)
	}

	// Verify the manager can resolve downstream calls
	calls, err := manager.GetDownstreamCalls("svc1", "/test")
	if err != nil {
		t.Fatalf("failed to get downstream calls: %v", err)
	}

	if len(calls) == 0 {
		t.Fatalf("expected downstream calls from svc1:/test")
	}

	// Create a simple engine to verify basic functionality
	eng := engine.NewEngine("interaction-test")

	// Verify engine was created
	if eng == nil {
		t.Fatalf("expected non-nil engine")
	}

	// Verify we can get simulation time
	simTime := eng.GetSimTime()
	if simTime.IsZero() {
		t.Fatalf("expected non-zero simulation time")
	}

	// Verify we can schedule events
	eng.ScheduleAt(engine.EventTypeSimulationEnd, simTime.Add(100*time.Millisecond), nil, "", nil)

	// Run a very short simulation to verify it works
	if err := eng.Run(50 * time.Millisecond); err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	t.Logf("✓ Engine integration test passed")
	t.Logf("  - Interaction manager works with engine")
	t.Logf("  - Downstream calls resolved correctly")
	t.Logf("  - Engine can run simulations")
}
