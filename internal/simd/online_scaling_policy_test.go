package simd

import (
	"context"
	"testing"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestOnlineControllerDatabaseNilScalingNoReplicaChange(t *testing.T) {
	exec := NewRunExecutor(NewRunStore(), nil)
	runID := "online-db-block"
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 32}},
		Services: []config.Service{
			{
				ID: "db", Kind: "database", Replicas: 1, Model: "cpu",
				Endpoints: []config.Endpoint{
					{Path: "/q", MeanCPUMs: 10, CPUSigmaMs: 2, NetLatencyMs: config.LatencySpec{Mean: 2, Sigma: 0.5}},
				},
			},
		},
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	svcLabels := metrics.CreateServiceLabels("db")
	now := time.Now()
	for i := 0; i < 10; i++ {
		ts := now.Add(time.Duration(i) * time.Millisecond)
		recordCPUUtilizationAllSvc1Instances(t, rm, collector, 0.85, ts)
		metrics.RecordLatency(collector, 500.0, ts, svcLabels)
	}
	exec.mu.Lock()
	exec.resourceManagers[runID] = rm
	exec.mu.Unlock()
	opt := &simulationv1.OptimizationConfig{
		Online:             true,
		TargetP95LatencyMs: 50.0,
		ControlIntervalMs:  10,
		StepSize:           1.0,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	state := mustScenarioState(t, scenario, rm, collector)
	go exec.runOnlineController(ctx, runID, scenario, collector, opt, rm, state)
	time.Sleep(80 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)
	if rm.ActiveReplicas("db") != 1 {
		t.Fatalf("expected database replicas unchanged for nil scaling policy, got %d", rm.ActiveReplicas("db"))
	}
}
