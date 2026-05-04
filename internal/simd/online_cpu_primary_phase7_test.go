package simd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

// cpuPrimaryPolicyTick is like testOnlineTickInput but allows overriding stabilization ticks and min replicas.
func cpuPrimaryPolicyTick(t *testing.T, scenario *config.Scenario, rm *resource.Manager, runMetrics *models.RunMetrics, opt *simulationv1.OptimizationConfig, stabTicks int, minReplicas int) *onlineCtrlTickInput {
	t.Helper()
	tick := testOnlineTickInput(t, scenario, rm, runMetrics, opt)
	if stabTicks > 0 {
		tick.stabTicks = stabTicks
	}
	if minReplicas > 0 {
		tick.minReplicasCtl = minReplicas
	}
	return tick
}

// TestCPUPrimaryServiceReplicaScaleUpWhenP95Safe proves scale-up is driven by CPU vs target_util_high
// while P95 remains under target_p95_latency_ms (no P95 breach required).
func TestCPUPrimaryServiceReplicaScaleUpWhenP95Safe(t *testing.T) {
	t.Parallel()
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	runID := "cpu-p7-svc-up-p95-safe"
	if _, err := store.Create(runID, &simulationv1.RunInput{ScenarioYaml: testScenarioYAML, DurationMs: 100}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8}},
		Services: []config.Service{
			{
				ID: "svc1", Replicas: 1, Model: "cpu", CPUCores: 1, MemoryMB: 128,
				Endpoints: []config.Endpoint{
					{Path: "/t", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}},
				},
			},
		},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1:/t", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}},
		},
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
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
		Online:                    true,
		StepSize:                  1,
		MaxHosts:                  4,
		MinHosts:                  1,
		TargetUtilHigh:            0.55,
		TargetUtilLow:             0.4,
		TargetP95LatencyMs:        200,
		OptimizationTargetPrimary: "cpu_utilization",
	}
	runMetrics := &models.RunMetrics{
		LatencyP95:     12,
		ServiceMetrics: map[string]*models.ServiceMetrics{"svc1": {CPUUtilization: 0.72}},
	}
	tick := testOnlineTickInput(t, scenario, rm, runMetrics, opt)
	loop := &onlineCtrlLoopState{
		stableRepDown:     make(map[string]int),
		stableVertCPUDown: make(map[string]int),
		stableVertMemDown: make(map[string]int),
		prevErrFrac:       -1,
	}
	exec.runOnlineControllerPolicyStep(runID, scenario, opt, rm, state, loop, tick, "cpu_utilization", true, &cpuPrimaryScaleUpScratch{})

	if rm.ActiveReplicas("svc1") < 2 {
		t.Fatalf("expected replica scale-up, got %d", rm.ActiveReplicas("svc1"))
	}
	rec, _ := store.Get(runID)
	if len(rec.OptimizationHistory) < 1 {
		t.Fatalf("expected history, got %d steps", len(rec.OptimizationHistory))
	}
	found := false
	for _, s := range rec.OptimizationHistory {
		if s == nil {
			continue
		}
		if strings.Contains(s.GetReason(), reasonCPUReplicaScaleUp) || s.GetAction() == "service_scale_out" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected CPU-primary replica scale-up reason or action in history, got %#v", rec.OptimizationHistory[0])
	}
}

// TestCPUPrimaryHostHotOnlyDoesNotTriggerCondBHostScaleOut documents that cond B host scale-out
// requires hot service CPU or broker pressure, not host CPU alone (see cpuPrimaryPostServiceHostScaleOutAndRetry).
func TestCPUPrimaryHostHotOnlyDoesNotTriggerCondBHostScaleOut(t *testing.T) {
	t.Parallel()
	exec := NewRunExecutor(NewRunStore(), nil)
	runID := "cpu-p7-host-hot-only"
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8}},
		Services: []config.Service{
			{
				ID: "svc1", Replicas: 1, Model: "cpu", CPUCores: 1, MemoryMB: 128,
				Endpoints: []config.Endpoint{
					{Path: "/t", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}},
				},
			},
		},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1:/t", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}},
		},
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	h, ok := rm.GetHost("h1")
	if !ok {
		t.Fatal("missing host")
	}
	h.SetCPUUtilization(0.92)

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
		TargetP95LatencyMs: 100,
	}
	runMetrics := &models.RunMetrics{
		LatencyP95:     5,
		ServiceMetrics: map[string]*models.ServiceMetrics{"svc1": {CPUUtilization: 0.5}},
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
	exec.cpuPrimaryPostServiceHostScaleOutAndRetry(runID, scenario, opt, rm, loop, tick, scratch)

	if rm.HostCount() != 1 {
		t.Fatalf("expected no cond-B host scale-out when service CPU is cold and no broker pressure, got %d hosts", rm.HostCount())
	}
}

// TestP95PrimaryNoReplicaScaleUpWhenP95SafeDespiteHighCPU ensures P95-primary does not scale replicas
// on CPU alone when latency guard is satisfied.
func TestP95PrimaryNoReplicaScaleUpWhenP95SafeDespiteHighCPU(t *testing.T) {
	t.Parallel()
	exec := NewRunExecutor(NewRunStore(), nil)
	runID := "cpu-p7-p95-no-cpu-only-up"
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 16}},
		Services: []config.Service{
			{
				ID: "svc1", Replicas: 1, Model: "cpu", CPUCores: 1, MemoryMB: 128,
				Endpoints: []config.Endpoint{
					{Path: "/t", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}},
				},
			},
		},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1:/t", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}},
		},
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
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
		TargetP95LatencyMs: 100,
	}
	runMetrics := &models.RunMetrics{
		LatencyP95:     10,
		ServiceMetrics: map[string]*models.ServiceMetrics{"svc1": {CPUUtilization: 0.95}},
	}
	tick := testOnlineTickInput(t, scenario, rm, runMetrics, opt)
	loop := &onlineCtrlLoopState{
		stableRepDown:     make(map[string]int),
		stableVertCPUDown: make(map[string]int),
		stableVertMemDown: make(map[string]int),
		prevErrFrac:       -1,
	}
	exec.runOnlineControllerPolicyStep(runID, scenario, opt, rm, state, loop, tick, "p95_latency", false, nil)

	if rm.ActiveReplicas("svc1") != 1 {
		t.Fatalf("expected no replica scale-up on P95-primary when P95 is safe, got replicas=%d", rm.ActiveReplicas("svc1"))
	}
}

// TestP95PrimaryReplicaScaleUpWhenP95Breached regression: explicit p95_latency primary still scales on P95 breach.
func TestP95PrimaryReplicaScaleUpWhenP95Breached(t *testing.T) {
	t.Parallel()
	exec := NewRunExecutor(NewRunStore(), nil)
	runID := "cpu-p7-p95-breach-up"
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 16}},
		Services: []config.Service{
			{
				ID: "svc1", Replicas: 1, Model: "cpu", CPUCores: 1, MemoryMB: 128,
				Endpoints: []config.Endpoint{
					{Path: "/t", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}},
				},
			},
		},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1:/t", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}},
		},
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
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
		Online:                    true,
		StepSize:                  1,
		MaxHosts:                  4,
		TargetP95LatencyMs:        100,
		OptimizationTargetPrimary: "p95_latency",
	}
	runMetrics := &models.RunMetrics{
		LatencyP95:     120,
		ServiceMetrics: map[string]*models.ServiceMetrics{"svc1": {CPUUtilization: 0.1}},
	}
	tick := testOnlineTickInput(t, scenario, rm, runMetrics, opt)
	loop := &onlineCtrlLoopState{
		stableRepDown:     make(map[string]int),
		stableVertCPUDown: make(map[string]int),
		stableVertMemDown: make(map[string]int),
		prevErrFrac:       -1,
	}
	exec.runOnlineControllerPolicyStep(runID, scenario, opt, rm, state, loop, tick, "p95_latency", false, nil)

	if rm.ActiveReplicas("svc1") < 2 {
		t.Fatalf("expected P95-primary scale-up when P95 above threshold, got replicas=%d", rm.ActiveReplicas("svc1"))
	}
}

// TestPrimaryTargetEmptyStringUsesP95PolicyBranch high CPU with empty primary behaves like default P95 path (no util-only scale-up).
func TestPrimaryTargetEmptyStringUsesP95PolicyBranch(t *testing.T) {
	t.Parallel()
	exec := NewRunExecutor(NewRunStore(), nil)
	runID := "cpu-p7-empty-primary"
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 16}},
		Services: []config.Service{
			{
				ID: "svc1", Replicas: 1, Model: "cpu", CPUCores: 1, MemoryMB: 128,
				Endpoints: []config.Endpoint{
					{Path: "/t", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}},
				},
			},
		},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1:/t", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}},
		},
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
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
		TargetP95LatencyMs: 100,
	}
	runMetrics := &models.RunMetrics{
		LatencyP95:     10,
		ServiceMetrics: map[string]*models.ServiceMetrics{"svc1": {CPUUtilization: 0.99}},
	}
	tick := testOnlineTickInput(t, scenario, rm, runMetrics, opt)
	loop := &onlineCtrlLoopState{
		stableRepDown:     make(map[string]int),
		stableVertCPUDown: make(map[string]int),
		stableVertMemDown: make(map[string]int),
		prevErrFrac:       -1,
	}
	exec.runOnlineControllerPolicyStep(runID, scenario, opt, rm, state, loop, tick, "", false, nil)

	if rm.ActiveReplicas("svc1") != 1 {
		t.Fatalf("expected empty primary to follow P95 branch (no scale when P95 safe), got replicas=%d", rm.ActiveReplicas("svc1"))
	}
}

// TestCPUPrimaryReplicaScaleDownRespectsMinReplicasPerService ensures floor from tick.minReplicasCtl.
func TestCPUPrimaryReplicaScaleDownRespectsMinReplicasPerService(t *testing.T) {
	t.Parallel()
	exec := NewRunExecutor(NewRunStore(), nil)
	runID := "cpu-p7-min-rep"
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8}},
		Services: []config.Service{
			{
				ID: "svc1", Replicas: 2, Model: "cpu", CPUCores: 1, MemoryMB: 128,
				Endpoints: []config.Endpoint{
					{Path: "/t", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}},
				},
			},
		},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1:/t", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}},
		},
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
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
		Online:                    true,
		StepSize:                  1,
		MaxHosts:                  4,
		MinHosts:                  1,
		TargetUtilHigh:            0.7,
		TargetUtilLow:             0.4,
		TargetP95LatencyMs:        100,
		MinReplicasPerService:     2,
		OptimizationTargetPrimary: "cpu_utilization",
	}
	runMetrics := &models.RunMetrics{
		LatencyP95:     5,
		ServiceMetrics: map[string]*models.ServiceMetrics{"svc1": {CPUUtilization: 0.1}},
	}
	tick := testOnlineTickInput(t, scenario, rm, runMetrics, opt)
	tick.minReplicasCtl = 2
	loop := &onlineCtrlLoopState{
		stableRepDown:     make(map[string]int),
		stableVertCPUDown: make(map[string]int),
		stableVertMemDown: make(map[string]int),
		prevErrFrac:       -1,
	}
	exec.runOnlineControllerPolicyStep(runID, scenario, opt, rm, state, loop, tick, "cpu_utilization", true, &cpuPrimaryScaleUpScratch{})

	if rm.ActiveReplicas("svc1") < 2 {
		t.Fatalf("expected replicas never below min (2), got %d", rm.ActiveReplicas("svc1"))
	}
}

// TestCPUPrimaryReplicaScaleDownRequiresStabilizationTicks proves replica scale-down waits for stabTicks.
func TestCPUPrimaryReplicaScaleDownRequiresStabilizationTicks(t *testing.T) {
	t.Parallel()
	exec := NewRunExecutor(NewRunStore(), nil)
	runID := "cpu-p7-stab-down"
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8}},
		Services: []config.Service{
			{
				ID: "svc1", Replicas: 3, Model: "cpu", CPUCores: 1, MemoryMB: 128,
				Endpoints: []config.Endpoint{
					{Path: "/t", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}},
				},
			},
		},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1:/t", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}},
		},
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
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
		Online:                    true,
		StepSize:                  1,
		MaxHosts:                  4,
		MinHosts:                  1,
		TargetUtilHigh:            0.7,
		TargetUtilLow:             0.4,
		TargetP95LatencyMs:        100,
		OptimizationTargetPrimary: "cpu_utilization",
	}
	runMetrics := &models.RunMetrics{
		LatencyP95:     5,
		ServiceMetrics: map[string]*models.ServiceMetrics{"svc1": {CPUUtilization: 0.1}},
	}
	tick := cpuPrimaryPolicyTick(t, scenario, rm, runMetrics, opt, 3, 1)
	loop := &onlineCtrlLoopState{
		stableRepDown:     make(map[string]int),
		stableVertCPUDown: make(map[string]int),
		stableVertMemDown: make(map[string]int),
		prevErrFrac:       -1,
	}
	scratch := &cpuPrimaryScaleUpScratch{}
	exec.runOnlineControllerPolicyStep(runID, scenario, opt, rm, state, loop, tick, "cpu_utilization", true, scratch)
	if rm.ActiveReplicas("svc1") != 3 {
		t.Fatalf("after first tick expected replicas still 3, got %d", rm.ActiveReplicas("svc1"))
	}
	exec.runOnlineControllerPolicyStep(runID, scenario, opt, rm, state, loop, tick, "cpu_utilization", true, scratch)
	if rm.ActiveReplicas("svc1") != 3 {
		t.Fatalf("after second tick expected replicas still 3, got %d", rm.ActiveReplicas("svc1"))
	}
	exec.runOnlineControllerPolicyStep(runID, scenario, opt, rm, state, loop, tick, "cpu_utilization", true, scratch)
	if rm.ActiveReplicas("svc1") != 2 {
		t.Fatalf("after third tick expected scale-down to 2, got %d", rm.ActiveReplicas("svc1"))
	}
}

func TestCpuPrimaryHostScaleInBlockedByMinHostsEqualsCluster(t *testing.T) {
	t.Parallel()
	exec := NewRunExecutor(NewRunStore(), nil)
	runID := "cpu-p7-host-in-min"
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
		if h, ok := rm.GetHost(hid); ok {
			h.SetCPUUtilization(0.05)
		}
	}
	opt := &simulationv1.OptimizationConfig{
		Online:             true,
		StepSize:           1,
		MaxHosts:           4,
		MinHosts:           2,
		TargetUtilHigh:     0.7,
		TargetUtilLow:      0.4,
		TargetP95LatencyMs: 100,
	}
	runMetrics := &models.RunMetrics{LatencyP95: 5, ServiceMetrics: map[string]*models.ServiceMetrics{"svc1": {CPUUtilization: 0.1}}}
	tick := testOnlineTickInput(t, scenario, rm, runMetrics, opt)
	loop := &onlineCtrlLoopState{}
	exec.cpuPrimaryPostServiceHostScaleIn(runID, scenario, opt, rm, loop, tick)
	if rm.HostCount() != 2 {
		t.Fatalf("expected host count unchanged when hostCount <= min_hosts, got %d", rm.HostCount())
	}
}

func TestCpuPrimaryHostScaleInBlockedByP95Guard(t *testing.T) {
	t.Parallel()
	exec := NewRunExecutor(NewRunStore(), nil)
	runID := "cpu-p7-host-in-p95"
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
		if h, ok := rm.GetHost(hid); ok {
			h.SetCPUUtilization(0.05)
		}
	}
	opt := &simulationv1.OptimizationConfig{
		Online:             true,
		StepSize:           1,
		MaxHosts:           4,
		MinHosts:           1,
		TargetUtilHigh:     0.7,
		TargetUtilLow:      0.4,
		TargetP95LatencyMs: 100,
	}
	tick := testOnlineTickInput(t, scenario, rm, &models.RunMetrics{
		LatencyP95:     200,
		ServiceMetrics: map[string]*models.ServiceMetrics{"svc1": {CPUUtilization: 0.1}},
	}, opt)
	tick.currentP95 = 200
	loop := &onlineCtrlLoopState{}
	exec.cpuPrimaryPostServiceHostScaleIn(runID, scenario, opt, rm, loop, tick)
	if rm.HostCount() != 2 {
		t.Fatalf("expected no host scale-in when P95 guard violated, got %d", rm.HostCount())
	}
}

func TestCpuPrimaryHostScaleInBlockedByBrokerBacklog(t *testing.T) {
	t.Parallel()
	exec := NewRunExecutor(NewRunStore(), nil)
	runID := "cpu-p7-host-in-broker"
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
		if h, ok := rm.GetHost(hid); ok {
			h.SetCPUUtilization(0.05)
		}
	}
	opt := &simulationv1.OptimizationConfig{
		Online:             true,
		StepSize:           1,
		MaxHosts:           4,
		MinHosts:           1,
		TargetUtilHigh:     0.7,
		TargetUtilLow:      0.4,
		TargetP95LatencyMs: 100,
	}
	tick := testOnlineTickInput(t, scenario, rm, &models.RunMetrics{
		LatencyP95:     5,
		ServiceMetrics: map[string]*models.ServiceMetrics{"svc1": {CPUUtilization: 0.1}},
	}, opt)
	tick.brokerPressure = map[string]brokerPressureSignal{"svc1": {HasBacklog: true}}
	loop := &onlineCtrlLoopState{}
	exec.cpuPrimaryPostServiceHostScaleIn(runID, scenario, opt, rm, loop, tick)
	if rm.HostCount() != 2 {
		t.Fatalf("expected no host scale-in with broker backlog, got %d", rm.HostCount())
	}
}

// TestCPUPrimaryOptimizationHistoryExportDetailParity checks GET export vs GET run for the same live-recorded step.
func TestCPUPrimaryOptimizationHistoryExportDetailParity(t *testing.T) {
	t.Parallel()
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))
	runID := "cpu-p7-export-parity"
	if _, err := store.Create(runID, &simulationv1.RunInput{ScenarioYaml: testScenarioYAML, DurationMs: 100}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 1}},
		Services: []config.Service{
			{
				ID: "svc1", Replicas: 1, Model: "cpu", CPUCores: 1, MemoryMB: 128,
				Endpoints: []config.Endpoint{
					{Path: "/t", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}},
				},
			},
		},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1:/t", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}},
		},
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state := mustScenarioState(t, scenario, rm, collector)
	wsStub, err := newWorkloadStateWithPatternsStub(runID, scenario, time.Now())
	if err != nil {
		t.Fatalf("stub: %v", err)
	}
	xe := srv.Executor
	if xe == nil {
		t.Fatal("nil executor")
	}
	xe.mu.Lock()
	xe.resourceManagers[runID] = rm
	xe.workloadStates[runID] = wsStub
	xe.mu.Unlock()

	opt := &simulationv1.OptimizationConfig{
		Online:                    true,
		StepSize:                  1,
		MaxHosts:                  4,
		MinHosts:                  1,
		TargetUtilHigh:            0.7,
		TargetUtilLow:             0.4,
		TargetP95LatencyMs:        100,
		OptimizationTargetPrimary: "cpu_utilization",
	}
	runMetrics := &models.RunMetrics{
		LatencyP95:     5,
		TotalRequests:  50,
		FailedRequests: 0,
		ServiceMetrics: map[string]*models.ServiceMetrics{"svc1": {CPUUtilization: 0.75}},
	}
	tick := testOnlineTickInput(t, scenario, rm, runMetrics, opt)
	loop := &onlineCtrlLoopState{
		stableRepDown:     make(map[string]int),
		stableVertCPUDown: make(map[string]int),
		stableVertMemDown: make(map[string]int),
		prevErrFrac:       -1,
	}
	xe.runOnlineControllerPolicyStep(runID, scenario, opt, rm, state, loop, tick, "cpu_utilization", true, &cpuPrimaryScaleUpScratch{})

	if err := store.SetMetrics(runID, &simulationv1.RunMetrics{TotalRequests: 50}); err != nil {
		t.Fatalf("SetMetrics: %v", err)
	}

	rrE := httptest.NewRecorder()
	reqE := httptest.NewRequest(http.MethodGet, "/v1/runs/"+runID+"/export", nil)
	srv.Handler().ServeHTTP(rrE, reqE)
	if rrE.Code != http.StatusOK {
		t.Fatalf("export %d: %s", rrE.Code, rrE.Body.String())
	}
	var export map[string]any
	if err := json.Unmarshal(rrE.Body.Bytes(), &export); err != nil {
		t.Fatalf("export json: %v", err)
	}
	rrG := httptest.NewRecorder()
	reqG := httptest.NewRequest(http.MethodGet, "/v1/runs/"+runID, nil)
	srv.Handler().ServeHTTP(rrG, reqG)
	if rrG.Code != http.StatusOK {
		t.Fatalf("get %d: %s", rrG.Code, rrG.Body.String())
	}
	var detail map[string]any
	if err := json.Unmarshal(rrG.Body.Bytes(), &detail); err != nil {
		t.Fatalf("detail json: %v", err)
	}
	hE := export["run"].(map[string]any)["optimization_history"].([]any)
	hG := detail["run"].(map[string]any)["optimization_history"].([]any)
	if len(hE) < 1 || len(hG) < 1 {
		t.Fatalf("expected history in both responses")
	}
	sE := hE[0].(map[string]any)
	sG := hG[0].(map[string]any)
	for _, k := range []string{"primary_target", "objective_unit", "target_util_low", "target_util_high", "target_p95_ms", "score_p95_ms"} {
		if sE[k] != sG[k] {
			t.Fatalf("parity mismatch key %s: export=%v get=%v", k, sE[k], sG[k])
		}
	}
}
