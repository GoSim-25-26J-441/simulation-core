package improvement

import (
	"testing"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestNewOptimizer(t *testing.T) {
	obj := &P95LatencyObjective{}
	opt := NewOptimizer(obj, 10, 1.0)

	if opt == nil {
		t.Fatalf("expected non-nil optimizer")
	}
	if opt.maxIterations != 10 {
		t.Fatalf("expected maxIterations 10, got %d", opt.maxIterations)
	}
	if opt.stepSize != 1.0 {
		t.Fatalf("expected stepSize 1.0, got %f", opt.stepSize)
	}
	if opt.bestScore != 1.7976931348623157e+308 { // math.MaxFloat64
		t.Fatalf("expected initial bestScore to be MaxFloat64")
	}

	// Test default step size
	opt2 := NewOptimizer(obj, 10, 0)
	if opt2.stepSize != 1.0 {
		t.Fatalf("expected default stepSize 1.0, got %f", opt2.stepSize)
	}
}

func TestOptimizerGenerateNeighbors(t *testing.T) {
	obj := &P95LatencyObjective{}
	opt := NewOptimizer(obj, 10, 1.0)

	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "host-1", Cores: 4},
		},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 2,
				Model:    "cpu",
				Endpoints: []config.Endpoint{
					{
						Path:         "/test",
						MeanCPUMs:    10,
						CPUSigmaMs:   2,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0.5},
					},
				},
			},
			{
				ID:       "svc2",
				Replicas: 3,
				Model:    "cpu",
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
					RateRPS: 100,
				},
			},
		},
	}

	neighbors := opt.generateNeighbors(scenario)

	// Should generate neighbors for each service (increase and decrease replicas)
	// With the new explorer, we also explore resources, workload, etc.
	// 2 services * 2 replica changes = 4 neighbors minimum
	// But with comprehensive exploration, we'll get many more
	if len(neighbors) < 4 {
		t.Fatalf("expected at least 4 neighbors, got %d", len(neighbors))
	}

	// Verify neighbors are different from original
	for i, neighbor := range neighbors {
		if neighbor == scenario {
			t.Fatalf("neighbor %d should be a copy, not the same pointer", i)
		}
		// Check that at least one replica count changed
		replicaChanged := false
		for j := range scenario.Services {
			if neighbor.Services[j].Replicas != scenario.Services[j].Replicas {
				replicaChanged = true
				break
			}
		}
		if !replicaChanged {
			t.Fatalf("neighbor %d should have different replica counts", i)
		}
	}
}

func TestOptimizerGenerateNeighborsWithPolicies(t *testing.T) {
	obj := &P95LatencyObjective{}
	opt := NewOptimizer(obj, 10, 1.0)

	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "host-1", Cores: 4},
		},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 2,
				Model:    "cpu",
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
		Policies: &config.Policies{
			Autoscaling: &config.AutoscalingPolicy{
				Enabled:       true,
				TargetCPUUtil: 0.7,
				ScaleStep:     1,
			},
		},
	}

	neighbors := opt.generateNeighbors(scenario)

	// Should generate neighbors for replicas (2) + autoscaling target (2) + resources + workload = many more
	// With comprehensive exploration, we'll get many more than just 4
	if len(neighbors) < 4 {
		t.Fatalf("expected at least 4 neighbors with policies, got %d", len(neighbors))
	}
}

func TestOptimizerOptimize(t *testing.T) {
	obj := &P95LatencyObjective{}
	opt := NewOptimizer(obj, 5, 1.0)

	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "host-1", Cores: 4},
		},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 2,
				Model:    "cpu",
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
					RateRPS: 100,
				},
			},
		},
	}

	// Mock evaluation function that returns decreasing scores (simulating improvement)
	iteration := 0
	evaluateFunc := func(sc *config.Scenario) (float64, error) {
		// Simulate that more replicas = better (lower) score
		score := 1000.0 - float64(sc.Services[0].Replicas)*100.0
		iteration++
		return score, nil
	}

	result, err := opt.Optimize(scenario, evaluateFunc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatalf("expected non-nil result")
	}

	if result.Iterations < 1 {
		t.Fatalf("expected at least 1 iteration, got %d", result.Iterations)
	}

	if len(result.History) < 1 {
		t.Fatalf("expected at least 1 history entry, got %d", len(result.History))
	}

	// Best score should be better (lower) than initial
	if result.BestScore >= 1000.0 {
		t.Fatalf("expected best score to improve, got %f", result.BestScore)
	}
}

func TestOptimizerOptimizeWithError(t *testing.T) {
	obj := &P95LatencyObjective{}
	opt := NewOptimizer(obj, 5, 1.0)

	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "host-1", Cores: 4},
		},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 2,
				Model:    "cpu",
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

	// Test with nil initial config
	_, err := opt.Optimize(nil, func(*config.Scenario) (float64, error) { return 0, nil })
	if err == nil {
		t.Fatalf("expected error for nil initial config")
	}

	// Test with nil evaluation function
	_, err = opt.Optimize(scenario, nil)
	if err == nil {
		t.Fatalf("expected error for nil evaluation function")
	}

	// Test with evaluation function that returns error
	evaluateFunc := func(*config.Scenario) (float64, error) {
		return 0, &InvalidMetricsError{Reason: "test error"}
	}
	_, err = opt.Optimize(scenario, evaluateFunc)
	if err == nil {
		t.Fatalf("expected error from evaluation function")
	}
}

func TestCloneScenario(t *testing.T) {
	original := &config.Scenario{
		Hosts: []config.Host{
			{ID: "host-1", Cores: 4},
		},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 2,
				Model:    "cpu",
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
		Policies: &config.Policies{
			Autoscaling: &config.AutoscalingPolicy{
				Enabled:       true,
				TargetCPUUtil: 0.7,
				ScaleStep:     1,
			},
		},
	}

	cloned := cloneScenario(original)

	if cloned == original {
		t.Fatalf("cloned scenario should be a different pointer")
	}

	if cloned.Services[0].Replicas != original.Services[0].Replicas {
		t.Fatalf("cloned scenario should have same replica count")
	}

	// Modify cloned and verify original is unchanged
	cloned.Services[0].Replicas = 5
	if original.Services[0].Replicas != 2 {
		t.Fatalf("modifying clone should not affect original")
	}

	// Test with nil
	if cloneScenario(nil) != nil {
		t.Fatalf("cloning nil should return nil")
	}
}
