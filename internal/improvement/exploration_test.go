package improvement

import (
	"testing"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestDefaultExplorer(t *testing.T) {
	explorer := NewDefaultExplorer()

	base := &config.Scenario{
		Services: []config.Service{
			{ID: "svc1", Replicas: 2, CPUCores: 1.0, MemoryMB: 512.0},
			{ID: "svc2", Replicas: 3, CPUCores: 2.0, MemoryMB: 1024.0},
		},
		Workload: []config.WorkloadPattern{
			{
				From: "client",
				To:   "svc1:/endpoint",
				Arrival: config.ArrivalSpec{
					Type:    "poisson",
					RateRPS: 10.0,
				},
			},
		},
	}

	neighbors := explorer.GenerateNeighbors(base, 1.0)

	if len(neighbors) == 0 {
		t.Fatalf("expected neighbors to be generated")
	}

	// Verify at least some neighbors were generated
	// Should have at least replica adjustments (2 services * 2 directions = 4)
	if len(neighbors) < 4 {
		t.Fatalf("expected at least 4 neighbors (replica adjustments), got %d", len(neighbors))
	}
}

func TestDefaultExplorerWithPolicies(t *testing.T) {
	explorer := NewDefaultExplorer()

	base := &config.Scenario{
		Services: []config.Service{
			{ID: "svc1", Replicas: 2},
		},
		Policies: &config.Policies{
			Autoscaling: &config.AutoscalingPolicy{
				Enabled:       true,
				TargetCPUUtil: 0.7,
				ScaleStep:     1,
			},
			Retries: &config.RetryPolicy{
				Enabled:    true,
				MaxRetries: 3,
				BaseMs:    10,
			},
		},
	}

	neighbors := explorer.GenerateNeighbors(base, 1.0)

	if len(neighbors) == 0 {
		t.Fatalf("expected neighbors to be generated")
	}

	// Should have replica adjustments + policy adjustments
	if len(neighbors) < 2 {
		t.Fatalf("expected at least 2 neighbors, got %d", len(neighbors))
	}
}

func TestDefaultExplorerWithResources(t *testing.T) {
	explorer := NewDefaultExplorer()

	base := &config.Scenario{
		Services: []config.Service{
			{ID: "svc1", Replicas: 2, CPUCores: 1.0, MemoryMB: 512.0},
		},
	}

	neighbors := explorer.GenerateNeighbors(base, 1.0)

	// Should have replica + CPU + memory adjustments
	if len(neighbors) < 4 {
		t.Fatalf("expected at least 4 neighbors (replica + CPU + memory), got %d", len(neighbors))
	}
}

func TestDefaultExplorerWithWorkload(t *testing.T) {
	explorer := NewDefaultExplorer()

	base := &config.Scenario{
		Services: []config.Service{
			{ID: "svc1", Replicas: 2},
		},
		Workload: []config.WorkloadPattern{
			{
				From: "client",
				To:   "svc1:/endpoint",
				Arrival: config.ArrivalSpec{
					Type:    "poisson",
					RateRPS: 10.0,
				},
			},
		},
	}

	neighbors := explorer.GenerateNeighbors(base, 1.0)

	// Should have replica + workload adjustments
	if len(neighbors) < 4 {
		t.Fatalf("expected at least 4 neighbors (replica + workload), got %d", len(neighbors))
	}
}

func TestDefaultExplorerName(t *testing.T) {
	explorer := NewDefaultExplorer()
	if explorer.Name() != "default" {
		t.Fatalf("expected name 'default', got %s", explorer.Name())
	}
}

func TestConservativeExplorer(t *testing.T) {
	explorer := NewConservativeExplorer()

	if explorer.Name() != "conservative" {
		t.Fatalf("expected name 'conservative', got %s", explorer.Name())
	}

	base := &config.Scenario{
		Services: []config.Service{
			{ID: "svc1", Replicas: 2},
		},
	}

	neighbors := explorer.GenerateNeighbors(base, 1.0)
	if len(neighbors) == 0 {
		t.Fatalf("expected neighbors to be generated")
	}
}

func TestAggressiveExplorer(t *testing.T) {
	explorer := NewAggressiveExplorer()

	if explorer.Name() != "aggressive" {
		t.Fatalf("expected name 'aggressive', got %s", explorer.Name())
	}

	base := &config.Scenario{
		Services: []config.Service{
			{ID: "svc1", Replicas: 2},
		},
	}

	neighbors := explorer.GenerateNeighbors(base, 1.0)
	if len(neighbors) == 0 {
		t.Fatalf("expected neighbors to be generated")
	}
}

func TestDefaultExplorerReplicaBounds(t *testing.T) {
	explorer := NewDefaultExplorer().WithMinReplicas(1).WithMaxReplicas(5)

	// Test at minimum
	base := &config.Scenario{
		Services: []config.Service{
			{ID: "svc1", Replicas: 1},
		},
	}
	neighbors := explorer.GenerateNeighbors(base, 1.0)
	// Should only have increase, not decrease
	foundDecrease := false
	for _, n := range neighbors {
		if n.Services[0].Replicas < 1 {
			foundDecrease = true
		}
	}
	if foundDecrease {
		t.Fatalf("should not generate neighbors below minimum replicas")
	}

	// Test at maximum
	base.Services[0].Replicas = 5
	neighbors = explorer.GenerateNeighbors(base, 1.0)
	// Should only have decrease, not increase
	foundIncrease := false
	for _, n := range neighbors {
		if n.Services[0].Replicas > 5 {
			foundIncrease = true
		}
	}
	if foundIncrease {
		t.Fatalf("should not generate neighbors above maximum replicas")
	}
}

