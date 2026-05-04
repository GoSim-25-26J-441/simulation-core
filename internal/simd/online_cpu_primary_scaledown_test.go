package simd

import (
	"testing"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

func TestCpuPrimaryScaleDownSafe(t *testing.T) {
	t.Parallel()
	rm := resource.NewManager()
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 4}},
		Services: []config.Service{
			{ID: "svc1", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/t", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}}},
		},
	}
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("init: %v", err)
	}
	m := &models.RunMetrics{
		LatencyP95:     10,
		TotalRequests:  100,
		FailedRequests: 1,
		ServiceMetrics: map[string]*models.ServiceMetrics{
			"svc1": {ConcurrentRequests: 1},
		},
	}
	bp := map[string]brokerPressureSignal{}
	if !cpuPrimaryScaleDownSafe(m, 10, 50, true, 0.02, bp, "svc1", rm) {
		t.Fatal("expected safe baseline")
	}
	if cpuPrimaryScaleDownSafe(m, 60, 50, true, 0.02, bp, "svc1", rm) {
		t.Fatal("expected unsafe when P95 above guard band")
	}
	if cpuPrimaryScaleDownSafe(m, 10, 50, true, 0.02, map[string]brokerPressureSignal{
		"svc1": {HasBacklog: true},
	}, "svc1", rm) {
		t.Fatal("expected unsafe with broker backlog")
	}
}

func TestCpuPrimaryHostScaleInAfterServiceLoop(t *testing.T) {
	t.Parallel()
	exec := NewRunExecutor(NewRunStore(), nil)
	runID := "cpu-p4-host-scale-in"
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 4}, {ID: "h2", Cores: 4}},
		Services: []config.Service{
			{
				ID: "svc1", Replicas: 1, Model: "cpu", CPUCores: 1, MemoryMB: 128,
				Endpoints: []config.Endpoint{{Path: "/t", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
			},
		},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1:/t", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}},
		},
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("init: %v", err)
	}
	for _, hid := range rm.HostIDs() {
		h, ok := rm.GetHost(hid)
		if ok {
			h.SetCPUUtilization(0.1)
		}
	}
	collector := metrics.NewCollector()
	collector.Start()
	state := mustScenarioState(t, scenario, rm, collector)
	wsStub, err := newWorkloadStateWithPatternsStub(runID, scenario, time.Now())
	if err != nil {
		t.Fatalf("stub: %v", err)
	}
	exec.mu.Lock()
	exec.resourceManagers[runID] = rm
	exec.workloadStates[runID] = wsStub
	exec.mu.Unlock()

	opt := &simulationv1.OptimizationConfig{
		Online:             true,
		StepSize:           1,
		MaxHosts:           4,
		MinHosts:           1,
		TargetUtilHigh:     0.7,
		TargetUtilLow:      0.4,
		TargetP95LatencyMs: 100,
	}
	runMetrics := &models.RunMetrics{
		LatencyP95:     5,
		TotalRequests:  50,
		FailedRequests: 0,
		ServiceMetrics: map[string]*models.ServiceMetrics{
			"svc1": {CPUUtilization: 0.15},
		},
	}
	tick := testOnlineTickInput(t, scenario, rm, runMetrics, opt)
	loop := &onlineCtrlLoopState{
		stableRepDown:     make(map[string]int),
		stableVertCPUDown: make(map[string]int),
		stableVertMemDown: make(map[string]int),
		prevErrFrac:       -1,
	}
	scratch := &cpuPrimaryScaleUpScratch{}
	exec.runOnlineControllerPolicyStep(runID, scenario, opt, rm, state, loop, tick, "cpu_utilization", true, scratch)

	if rm.HostCount() != 1 {
		t.Fatalf("expected one host removed when cluster cold and guardrails safe, got %d", rm.HostCount())
	}
}
