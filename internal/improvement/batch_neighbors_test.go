package improvement

import (
	"testing"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/batchspec"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestOrderNeighborsForExpansion_StressPrefersHigherCapacity(t *testing.T) {
	base := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 32, MemoryGB: 64}},
		Services: []config.Service{
			{ID: "svc1", Replicas: 3, CPUCores: 1, MemoryMB: 512, Model: "cpu"},
		},
	}
	spec := batchspec.DefaultBatchSpec(base)

	scaleUp := cloneScenario(base)
	scaleUp.Services[0].Replicas = 4
	scaleDown := cloneScenario(base)
	scaleDown.Services[0].Replicas = 2

	loStress := &simulationv1.RunMetrics{
		LatencyP95Ms: 900,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{ServiceName: "svc1", CpuUtilization: 0.85, MemoryUtilization: 0.5},
		},
	}
	ordered := orderNeighborsForExpansion(spec, base, loStress, []*config.Scenario{scaleDown, scaleUp})
	if len(ordered) != 2 {
		t.Fatalf("len=%d", len(ordered))
	}
	if ordered[0].Services[0].Replicas != 4 {
		t.Fatalf("under stress, expected scale-out neighbor first, got replicas=%d", ordered[0].Services[0].Replicas)
	}
}

func TestOrderNeighborsForExpansion_ColdPrefersLowerCapacity(t *testing.T) {
	base := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 32, MemoryGB: 64}},
		Services: []config.Service{
			{ID: "svc1", Replicas: 3, CPUCores: 1, MemoryMB: 512, Model: "cpu"},
		},
	}
	spec := batchspec.DefaultBatchSpec(base)

	scaleUp := cloneScenario(base)
	scaleUp.Services[0].Replicas = 4
	scaleDown := cloneScenario(base)
	scaleDown.Services[0].Replicas = 2

	calm := &simulationv1.RunMetrics{
		LatencyP95Ms: 100,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{ServiceName: "svc1", CpuUtilization: 0.55, MemoryUtilization: 0.55},
		},
	}
	ordered := orderNeighborsForExpansion(spec, base, calm, []*config.Scenario{scaleUp, scaleDown})
	if len(ordered) != 2 {
		t.Fatalf("len=%d", len(ordered))
	}
	if ordered[0].Services[0].Replicas != 2 {
		t.Fatalf("when not under stress, expected scale-in neighbor first, got replicas=%d", ordered[0].Services[0].Replicas)
	}
}
