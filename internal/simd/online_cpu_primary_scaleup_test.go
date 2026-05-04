package simd

import (
	"fmt"
	"strings"
	"testing"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

func testOnlineTickInput(t *testing.T, scenario *config.Scenario, rm *resource.Manager, runMetrics *models.RunMetrics, opt *simulationv1.OptimizationConfig) *onlineCtrlTickInput {
	t.Helper()
	initialHostCores := ScenarioInitialHostCPUCores(scenario)
	initialHostMemGB := ScenarioInitialHostMemoryGB(scenario)
	initialHosts := len(scenario.Hosts)
	minHosts := int(opt.MinHosts)
	if minHosts <= 0 {
		minHosts = initialHosts
	}
	maxHosts := int(opt.MaxHosts)
	if maxHosts <= 0 {
		maxHosts = initialHosts
	}
	if maxHosts < minHosts {
		maxHosts = minHosts
	}
	return &onlineCtrlTickInput{
		runMetrics:           runMetrics,
		currentP95:           runMetrics.LatencyP95,
		targetP95:            opt.TargetP95LatencyMs,
		p95Guard:             opt.TargetP95LatencyMs > 0,
		brokerPressure:       map[string]brokerPressureSignal{},
		hostCount:            rm.HostCount(),
		maxHostCPU:           rm.MaxHostCPUUtilization(),
		maxHostMem:           rm.MaxHostMemoryUtilization(),
		stabTicks:            1,
		minReplicasCtl:       1,
		minCPUCtl:            opt.GetMinCpuCoresPerInstance(),
		minMemCtl:            opt.GetMinMemoryMbPerInstance(),
		memHeadroomCtl:       opt.GetMemoryDownsizeHeadroomMb(),
		scaleDownCPUMax:      opt.GetScaleDownCpuUtilMax(),
		scaleDownMemMax:      opt.GetScaleDownMemUtilMax(),
		scaleDownHostCPUMax:  opt.GetScaleDownHostCpuUtilMax(),
		scaleDownHostMemMax:  opt.GetScaleDownHostMemUtilMax(),
		initialHostCores:     initialHostCores,
		initialHostMemGB:     initialHostMemGB,
		minHosts:             minHosts,
		maxHosts:             maxHosts,
		cpuHighThreshold:     0.8,
		hostCPUHighThreshold: 0.8,
		minHostCPUCores:      EffectiveOnlineMinHostCPUCores(opt, initialHostCores),
		maxHostCPUCores:      EffectiveOnlineMaxHostCPUCores(opt, initialHostCores),
		minHostMemGB:         EffectiveOnlineMinHostMemoryGb(opt, initialHostMemGB),
		maxHostMemGB:         EffectiveOnlineMaxHostMemoryGb(opt, initialHostMemGB),
		hostCPUStepCores:     EffectiveOnlineHostCPUStepCores(opt),
		hostMemoryStepGB:     EffectiveOnlineHostMemoryStepGb(opt),
	}
}

func TestOnlineReplicaScaleUpCapacityBlocked(t *testing.T) {
	t.Parallel()
	if !onlineReplicaScaleUpCapacityBlocked(resource.ErrPlacementInfeasible) {
		t.Fatal("expected ErrPlacementInfeasible to classify as capacity/placement blocked")
	}
	if onlineReplicaScaleUpCapacityBlocked(nil) {
		t.Fatal("nil error should not classify")
	}
	if !onlineReplicaScaleUpCapacityBlocked(fmt.Errorf("wrap: %w", resource.ErrPlacementInfeasible)) {
		t.Fatal("expected wrapped placement errors to match")
	}
	if !onlineReplicaScaleUpCapacityBlocked(errorString("no host has capacity for new instance (need 1.00 CPU cores, 512.00 MB memory)")) {
		t.Fatal("expected no-host-capacity message to classify")
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }

func replicasForServiceInRunConfig(t *testing.T, cfg *simulationv1.RunConfiguration, svcID string) int32 {
	t.Helper()
	if cfg == nil {
		t.Fatal("nil RunConfiguration")
	}
	for _, ent := range cfg.GetServices() {
		if ent.GetServiceId() == svcID {
			return ent.GetReplicas()
		}
	}
	t.Fatalf("service %q not found in config", svcID)
	return 0
}

func TestCPUPrimaryPlacementBlockedAddsHostAndRetriesReplicas(t *testing.T) {
	t.Parallel()
	runID := "cpu-p3-placement-host"
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
	yamlText, err := config.MarshalScenarioYAML(scenario)
	if err != nil {
		t.Fatalf("MarshalScenarioYAML: %v", err)
	}
	store := NewRunStore()
	if _, err := store.Create(runID, &simulationv1.RunInput{ScenarioYaml: yamlText, DurationMs: 100}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	exec := NewRunExecutor(store, nil)
	collector := metrics.NewCollector()
	collector.Start()
	state := mustScenarioState(t, scenario, rm, collector)
	wsStub, err := newWorkloadStateWithPatternsStub(runID, scenario, time.Now())
	if err != nil {
		t.Fatalf("newWorkloadStateWithPatternsStub: %v", err)
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
		LatencyP95: 5,
		ServiceMetrics: map[string]*models.ServiceMetrics{
			"svc1": {CPUUtilization: 0.75},
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

	if n := rm.HostCount(); n < 2 {
		t.Fatalf("expected host scale-out after placement failure, hostCount=%d", n)
	}
	if r := rm.ActiveReplicas("svc1"); r < 2 {
		t.Fatalf("expected replica retry after host add, replicas=%d", r)
	}

	rec, ok := store.Get(runID)
	if !ok {
		t.Fatal("expected run in store")
	}
	var retryStep *simulationv1.OptimizationStep
	for _, s := range rec.OptimizationHistory {
		if s == nil {
			continue
		}
		if s.GetAction() == "service_scale_out" && strings.Contains(s.GetReason(), reasonCPUReplicaScaleUp) {
			retryStep = s
			break
		}
	}
	if retryStep == nil {
		t.Fatalf("expected optimization step for replica retry (action=service_scale_out, reason contains %q), history=%d",
			reasonCPUReplicaScaleUp, len(rec.OptimizationHistory))
	}
	prevN := replicasForServiceInRunConfig(t, retryStep.GetPreviousConfig(), "svc1")
	currN := replicasForServiceInRunConfig(t, retryStep.GetCurrentConfig(), "svc1")
	if prevN >= currN {
		t.Fatalf("replay previous_config replicas should be < current_config for retry step: prev=%d curr=%d", prevN, currN)
	}
	if int(currN) != rm.ActiveReplicas("svc1") || currN != int32(scratch.blockedTargetReplicas) {
		t.Fatalf("current_config replicas=%d want active=%d target=%d", currN, rm.ActiveReplicas("svc1"), scratch.blockedTargetReplicas)
	}
}

func TestCPUPrimaryHostScaleOutCondBWithoutPlacementFailure(t *testing.T) {
	t.Parallel()
	exec := NewRunExecutor(NewRunStore(), nil)
	runID := "cpu-p3-cond-b"
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
		t.Fatal("missing host h1")
	}
	h.SetCPUUtilization(0.85)

	collector := metrics.NewCollector()
	collector.Start()
	state := mustScenarioState(t, scenario, rm, collector)
	wsStub, err := newWorkloadStateWithPatternsStub(runID, scenario, time.Now())
	if err != nil {
		t.Fatalf("newWorkloadStateWithPatternsStub: %v", err)
	}
	exec.mu.Lock()
	exec.resourceManagers[runID] = rm
	exec.workloadStates[runID] = wsStub
	exec.mu.Unlock()

	opt := &simulationv1.OptimizationConfig{
		Online:             true,
		StepSize:           1,
		MaxHosts:           3,
		MinHosts:           1,
		TargetUtilHigh:     0.7,
		TargetP95LatencyMs: 50,
	}
	runMetrics := &models.RunMetrics{
		LatencyP95: 5,
		ServiceMetrics: map[string]*models.ServiceMetrics{
			"svc1": {CPUUtilization: 0.75},
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

	if rm.HostCount() < 2 {
		t.Fatalf("expected host scale-out from cond B, got hostCount=%d", rm.HostCount())
	}
}

func TestCPUPrimaryNoHostScaleWhenAtMaxHosts(t *testing.T) {
	t.Parallel()
	exec := NewRunExecutor(NewRunStore(), nil)
	runID := "cpu-p3-max-hosts"
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
		t.Fatalf("newWorkloadStateWithPatternsStub: %v", err)
	}
	exec.mu.Lock()
	exec.resourceManagers[runID] = rm
	exec.workloadStates[runID] = wsStub
	exec.mu.Unlock()

	opt := &simulationv1.OptimizationConfig{
		Online:             true,
		StepSize:           1,
		MaxHosts:           1,
		MinHosts:           1,
		TargetUtilHigh:     0.7,
		TargetP95LatencyMs: 100,
	}
	runMetrics := &models.RunMetrics{
		LatencyP95: 5,
		ServiceMetrics: map[string]*models.ServiceMetrics{
			"svc1": {CPUUtilization: 0.75},
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
		t.Fatalf("expected no host scale-out when max_hosts==initial, got %d", rm.HostCount())
	}
}

func TestP95PrimaryDoesNotScaleHostsWithoutP95Breach(t *testing.T) {
	t.Parallel()
	exec := NewRunExecutor(NewRunStore(), nil)
	runID := "p95-p3-no-host"
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
	h.SetCPUUtilization(0.95)

	collector := metrics.NewCollector()
	collector.Start()
	state := mustScenarioState(t, scenario, rm, collector)
	wsStub, err := newWorkloadStateWithPatternsStub(runID, scenario, time.Now())
	if err != nil {
		t.Fatalf("newWorkloadStateWithPatternsStub: %v", err)
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
		LatencyP95: 10,
		ServiceMetrics: map[string]*models.ServiceMetrics{
			"svc1": {CPUUtilization: 0.9},
		},
	}
	tick := testOnlineTickInput(t, scenario, rm, runMetrics, opt)
	loop := &onlineCtrlLoopState{
		stableRepDown:     make(map[string]int),
		stableVertCPUDown: make(map[string]int),
		stableVertMemDown: make(map[string]int),
		prevErrFrac:       -1,
	}
	exec.runOnlineControllerPolicyStep(runID, scenario, opt, rm, state, loop, tick, "p95_latency", false, nil)

	if rm.HostCount() != 1 {
		t.Fatalf("expected no host scale-out when P95 below breach threshold, hostCount=%d", rm.HostCount())
	}
}
