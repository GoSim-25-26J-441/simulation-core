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
		// Check that at least something changed (replicas, resources, workload, or policies)
		changed := false
		// Check replica counts
		for j := range scenario.Services {
			if neighbor.Services[j].Replicas != scenario.Services[j].Replicas {
				changed = true
				break
			}
			// Check CPU/memory resources
			if neighbor.Services[j].CPUCores != scenario.Services[j].CPUCores ||
				neighbor.Services[j].MemoryMB != scenario.Services[j].MemoryMB {
				changed = true
				break
			}
		}
		// Check workload
		if len(neighbor.Workload) == len(scenario.Workload) {
			for j := range scenario.Workload {
				if neighbor.Workload[j].Arrival.RateRPS != scenario.Workload[j].Arrival.RateRPS {
					changed = true
					break
				}
			}
		}
		// Check policies
		if neighbor.Policies != nil && scenario.Policies != nil {
			if neighbor.Policies.Autoscaling != nil && scenario.Policies.Autoscaling != nil {
				if neighbor.Policies.Autoscaling.TargetCPUUtil != scenario.Policies.Autoscaling.TargetCPUUtil ||
					neighbor.Policies.Autoscaling.ScaleStep != scenario.Policies.Autoscaling.ScaleStep {
					changed = true
				}
			}
		}
		if !changed {
			t.Fatalf("neighbor %d should have at least one parameter changed", i)
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

	// Cover getters after optimization
	if opt.GetBestConfig() == nil {
		t.Error("GetBestConfig should return non-nil after Optimize")
	}
	if opt.GetBestScore() >= 1000.0 {
		t.Errorf("GetBestScore should reflect improvement, got %f", opt.GetBestScore())
	}
	if opt.GetIteration() < 1 {
		t.Errorf("GetIteration should be at least 1 after Optimize, got %d", opt.GetIteration())
	}
}

func TestOptimizerWithProgressReporter(t *testing.T) {
	obj := &P95LatencyObjective{}
	opt := NewOptimizer(obj, 3, 1.0)

	var reported []int
	opt = opt.WithProgressReporter(func(iter int, bestScore float64) {
		reported = append(reported, iter)
	})

	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 4}},
		Services: []config.Service{
			{ID: "svc1", Replicas: 2, Endpoints: []config.Endpoint{{Path: "/", MeanCPUMs: 10, CPUSigmaMs: 1, NetLatencyMs: config.LatencySpec{Mean: 1}}}},
		},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1:/", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 10}},
		},
	}
	evaluateFunc := func(sc *config.Scenario) (float64, error) {
		return 100.0 - float64(sc.Services[0].Replicas)*10.0, nil
	}

	_, err := opt.Optimize(scenario, evaluateFunc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reported) < 1 {
		t.Errorf("expected progress reporter to be called at least once, got %d", len(reported))
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
