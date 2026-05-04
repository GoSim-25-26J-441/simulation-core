package simd

import (
	"strings"
	"testing"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

func hostCPUFromRunCfg(t *testing.T, cfg *simulationv1.RunConfiguration, hostID string) int32 {
	t.Helper()
	if cfg == nil {
		t.Fatal("nil config")
	}
	for _, h := range cfg.Hosts {
		if h != nil && h.HostId == hostID {
			return h.CpuCores
		}
	}
	t.Fatalf("host %q not in config", hostID)
	return 0
}

func hostMemGBFromRunCfg(t *testing.T, cfg *simulationv1.RunConfiguration, hostID string) int32 {
	t.Helper()
	if cfg == nil {
		t.Fatal("nil config")
	}
	for _, h := range cfg.Hosts {
		if h != nil && h.HostId == hostID {
			return h.MemoryGb
		}
	}
	t.Fatalf("host %q not in config", hostID)
	return 0
}

func lastStepWithAction(rec *RunRecord, action string) *simulationv1.OptimizationStep {
	if rec == nil {
		return nil
	}
	for i := len(rec.OptimizationHistory) - 1; i >= 0; i-- {
		s := rec.OptimizationHistory[i]
		if s != nil && s.GetAction() == action {
			return s
		}
	}
	return nil
}

func TestCPUPrimaryHostCPUVerticalScaleUpAtMaxHosts(t *testing.T) {
	t.Parallel()
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	runID := "cpu-vert-up"
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
	h, ok := rm.GetHost("h1")
	if !ok {
		t.Fatal("missing h1")
	}
	h.SetCPUUtilization(0.85)

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

	maxHC := int32(8)
	opt := &simulationv1.OptimizationConfig{
		Online:                    true,
		StepSize:                  1,
		MaxHosts:                  1,
		MinHosts:                  1,
		TargetUtilHigh:            0.7,
		TargetUtilLow:             0.4,
		TargetP95LatencyMs:        100,
		OptimizationTargetPrimary: "cpu_utilization",
		MaxHostCpuCores:           maxHC,
		HostCpuStepCores:          2,
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

	rec, ok := store.Get(runID)
	if !ok {
		t.Fatal("missing run")
	}
	step := lastStepWithAction(rec, "host_cpu_scale_up")
	if step == nil {
		t.Fatalf("expected host_cpu_scale_up in history, got %d steps", len(rec.OptimizationHistory))
	}
	if step.GetPrimaryTarget() != "cpu_utilization" {
		t.Fatalf("primary_target=%q", step.GetPrimaryTarget())
	}
	if step.GetDecisionMetric() != "max_host_cpu_utilization" {
		t.Fatalf("decision_metric=%q", step.GetDecisionMetric())
	}
	if step.GetObjectiveUnit() != "ratio" {
		t.Fatalf("objective_unit=%q", step.GetObjectiveUnit())
	}
	got := hostCPUFromRunCfg(t, step.GetCurrentConfig(), "h1")
	if got <= 1 {
		t.Fatalf("expected host CPU increased in replay current_config, got %d", got)
	}
	if h2, ok2 := rm.GetHost("h1"); !ok2 || h2.CPUCores() != int(got) {
		t.Fatalf("rm host cores mismatch replay")
	}
}

func TestCPUPrimaryHostCPUVerticalScaleUpRespectsMaxHostCpuCores(t *testing.T) {
	t.Parallel()
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	runID := "cpu-vert-cap"
	if _, err := store.Create(runID, &simulationv1.RunInput{ScenarioYaml: testScenarioYAML, DurationMs: 100}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 3}},
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
		t.Fatal("missing h1")
	}
	h.SetCPUUtilization(0.9)

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
		MaxHosts:                  1,
		MinHosts:                  1,
		TargetUtilHigh:            0.7,
		TargetP95LatencyMs:        100,
		OptimizationTargetPrimary: "cpu_utilization",
		MaxHostCpuCores:           4,
		HostCpuStepCores:          4,
	}
	runMetrics := &models.RunMetrics{
		LatencyP95: 5,
		ServiceMetrics: map[string]*models.ServiceMetrics{
			"svc1": {CPUUtilization: 0.8},
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

	if c, ok := rm.GetHost("h1"); ok && c.CPUCores() != 4 {
		t.Fatalf("host cores should cap at max_host_cpu_cores=4, got %d", c.CPUCores())
	}
	rec, ok := store.Get(runID)
	if !ok {
		t.Fatal("missing run")
	}
	step := lastStepWithAction(rec, "host_cpu_scale_up")
	if step == nil {
		t.Fatalf("expected host_cpu_scale_up")
	}
	if hostCPUFromRunCfg(t, step.GetCurrentConfig(), "h1") != 4 {
		t.Fatalf("replay current_config host cores want 4")
	}
}

func TestCPUPrimaryHostCPUVerticalScaleDown(t *testing.T) {
	t.Parallel()
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	runID := "cpu-vert-down"
	if _, err := store.Create(runID, &simulationv1.RunInput{ScenarioYaml: testScenarioYAML, DurationMs: 100}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 4}},
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
	if _, err := rm.IncreaseOnlineHostCPUCapacity(2, 12); err != nil {
		t.Fatalf("IncreaseOnlineHostCPUCapacity: %v", err)
	}
	h, ok := rm.GetHost("h1")
	if !ok {
		t.Fatal("missing h1")
	}
	h.SetCPUUtilization(0.1)

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
		MaxHosts:                  1,
		MinHosts:                  1,
		TargetUtilHigh:            0.7,
		TargetP95LatencyMs:        100,
		OptimizationTargetPrimary: "cpu_utilization",
		ScaleDownHostCpuUtilMax:   0.55,
		MinHostCpuCores:           4,
		HostCpuStepCores:          1,
	}
	runMetrics := &models.RunMetrics{
		LatencyP95:     50,
		TotalRequests:  10,
		FailedRequests: 0,
		ServiceMetrics: map[string]*models.ServiceMetrics{
			"svc1": {CPUUtilization: 0.2},
		},
	}
	tick := testOnlineTickInput(t, scenario, rm, runMetrics, opt)
	tick.stabTicks = 2
	loop := &onlineCtrlLoopState{
		stableRepDown:     make(map[string]int),
		stableVertCPUDown: make(map[string]int),
		stableVertMemDown: make(map[string]int),
		prevErrFrac:       -1,
	}
	exec.runOnlineControllerPolicyStep(runID, scenario, opt, rm, state, loop, tick, "cpu_utilization", true, &cpuPrimaryScaleUpScratch{})
	exec.runOnlineControllerPolicyStep(runID, scenario, opt, rm, state, loop, tick, "cpu_utilization", true, &cpuPrimaryScaleUpScratch{})

	rec, ok := store.Get(runID)
	if !ok {
		t.Fatal("missing run")
	}
	step := lastStepWithAction(rec, "host_cpu_scale_down")
	if step == nil {
		t.Fatalf("expected host_cpu_scale_down, history=%d", len(rec.OptimizationHistory))
	}
	if step.GetPrimaryTarget() != "cpu_utilization" {
		t.Fatalf("primary_target=%q", step.GetPrimaryTarget())
	}
	if hostCPUFromRunCfg(t, step.GetCurrentConfig(), "h1") < 4 {
		t.Fatalf("must not go below min_host_cpu_cores=4, got %d", hostCPUFromRunCfg(t, step.GetCurrentConfig(), "h1"))
	}
}

func TestMemoryPrimaryHostMemoryVerticalScaleUpAtMaxHosts(t *testing.T) {
	t.Parallel()
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	runID := "mem-vert-up"
	if _, err := store.Create(runID, &simulationv1.RunInput{ScenarioYaml: testScenarioYAML, DurationMs: 100}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 4}},
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
		t.Fatal("missing h1")
	}
	h.SetMemoryUtilization(0.85)

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
		MaxHosts:                  1,
		MinHosts:                  1,
		TargetUtilHigh:            0.7,
		TargetP95LatencyMs:        100,
		OptimizationTargetPrimary: "memory_utilization",
		MaxHostMemoryGb:           16,
		HostMemoryStepGb:          2,
	}
	runMetrics := &models.RunMetrics{
		LatencyP95: 5,
		ServiceMetrics: map[string]*models.ServiceMetrics{
			"svc1": {MemoryUtilization: 0.8},
		},
	}
	tick := testOnlineTickInput(t, scenario, rm, runMetrics, opt)
	loop := &onlineCtrlLoopState{
		stableRepDown:     make(map[string]int),
		stableVertCPUDown: make(map[string]int),
		stableVertMemDown: make(map[string]int),
		prevErrFrac:       -1,
	}
	exec.runOnlineControllerPolicyStep(runID, scenario, opt, rm, state, loop, tick, "memory_utilization", false, nil)

	rec, ok := store.Get(runID)
	if !ok {
		t.Fatal("missing run")
	}
	step := lastStepWithAction(rec, "host_memory_scale_up")
	if step == nil {
		t.Fatalf("expected host_memory_scale_up, history=%d", len(rec.OptimizationHistory))
	}
	if step.GetPrimaryTarget() != "memory_utilization" {
		t.Fatalf("primary_target=%q", step.GetPrimaryTarget())
	}
	if step.GetDecisionMetric() != "max_host_memory_utilization" {
		t.Fatalf("decision_metric=%q", step.GetDecisionMetric())
	}
	if got := hostMemGBFromRunCfg(t, step.GetCurrentConfig(), "h1"); got <= 4 {
		t.Fatalf("expected host memory increased in replay, got %d", got)
	}
}

func TestMemoryPrimaryHostMemoryVerticalScaleUpRespectsMaxHostMemoryGb(t *testing.T) {
	t.Parallel()
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	runID := "mem-vert-cap"
	if _, err := store.Create(runID, &simulationv1.RunInput{ScenarioYaml: testScenarioYAML, DurationMs: 100}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 5}},
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
		t.Fatal("missing h1")
	}
	h.SetMemoryUtilization(0.9)

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
		MaxHosts:                  1,
		MinHosts:                  1,
		TargetUtilHigh:            0.7,
		TargetP95LatencyMs:        100,
		OptimizationTargetPrimary: "memory_utilization",
		MaxHostMemoryGb:           6,
		HostMemoryStepGb:          4,
	}
	runMetrics := &models.RunMetrics{
		LatencyP95: 5,
		ServiceMetrics: map[string]*models.ServiceMetrics{
			"svc1": {MemoryUtilization: 0.85},
		},
	}
	tick := testOnlineTickInput(t, scenario, rm, runMetrics, opt)
	loop := &onlineCtrlLoopState{
		stableRepDown:     make(map[string]int),
		stableVertCPUDown: make(map[string]int),
		stableVertMemDown: make(map[string]int),
		prevErrFrac:       -1,
	}
	exec.runOnlineControllerPolicyStep(runID, scenario, opt, rm, state, loop, tick, "memory_utilization", false, nil)

	if h2, ok2 := rm.GetHost("h1"); !ok2 || h2.MemoryGB() != 6 {
		t.Fatalf("host memory should cap at 6 GB, got %v", ok2)
	}
	rec, ok := store.Get(runID)
	if !ok {
		t.Fatal("missing run")
	}
	step := lastStepWithAction(rec, "host_memory_scale_up")
	if step == nil {
		t.Fatal("expected host_memory_scale_up")
	}
	if hostMemGBFromRunCfg(t, step.GetCurrentConfig(), "h1") != 6 {
		t.Fatalf("replay current_config memory want 6")
	}
}

func TestMemoryPrimaryHostMemoryVerticalScaleDown(t *testing.T) {
	t.Parallel()
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	runID := "mem-vert-down"
	if _, err := store.Create(runID, &simulationv1.RunInput{ScenarioYaml: testScenarioYAML, DurationMs: 100}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 4}},
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
	if _, err := rm.IncreaseOnlineHostMemoryCapacity(3, 32); err != nil {
		t.Fatalf("IncreaseOnlineHostMemoryCapacity: %v", err)
	}
	h, ok := rm.GetHost("h1")
	if !ok {
		t.Fatal("missing h1")
	}
	h.SetMemoryUtilization(0.05)

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
		MaxHosts:                  1,
		MinHosts:                  1,
		TargetUtilHigh:            0.7,
		TargetP95LatencyMs:        100,
		OptimizationTargetPrimary: "memory_utilization",
		ScaleDownHostMemUtilMax:   0.2,
		MinHostMemoryGb:           4,
		HostMemoryStepGb:          1,
	}
	runMetrics := &models.RunMetrics{
		LatencyP95:     50,
		TotalRequests:  10,
		FailedRequests: 0,
		ServiceMetrics: map[string]*models.ServiceMetrics{
			"svc1": {MemoryUtilization: 0.1},
		},
	}
	tick := testOnlineTickInput(t, scenario, rm, runMetrics, opt)
	tick.stabTicks = 2
	loop := &onlineCtrlLoopState{
		stableRepDown:     make(map[string]int),
		stableVertCPUDown: make(map[string]int),
		stableVertMemDown: make(map[string]int),
		prevErrFrac:       -1,
	}
	exec.runOnlineControllerPolicyStep(runID, scenario, opt, rm, state, loop, tick, "memory_utilization", false, nil)
	exec.runOnlineControllerPolicyStep(runID, scenario, opt, rm, state, loop, tick, "memory_utilization", false, nil)

	rec, ok := store.Get(runID)
	if !ok {
		t.Fatal("missing run")
	}
	step := lastStepWithAction(rec, "host_memory_scale_down")
	if step == nil {
		t.Fatalf("expected host_memory_scale_down, history=%d", len(rec.OptimizationHistory))
	}
	if hostMemGBFromRunCfg(t, step.GetCurrentConfig(), "h1") < 4 {
		t.Fatalf("must not go below min_host_memory_gb=4, got %d", hostMemGBFromRunCfg(t, step.GetCurrentConfig(), "h1"))
	}
}

func TestP95PrimaryHostCPUVerticalReplayUsesMaxHostCpuUtilDecisionMetric(t *testing.T) {
	t.Parallel()
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	runID := "p95-vert-replay"
	if _, err := store.Create(runID, &simulationv1.RunInput{ScenarioYaml: testScenarioYAML, DurationMs: 100}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 2}},
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
		t.Fatal("missing h1")
	}
	h.SetCPUUtilization(0.9)

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
		MaxHosts:           1,
		MinHosts:           1,
		TargetP95LatencyMs: 50,
		MaxHostCpuCores:    8,
		HostCpuStepCores:   1,
	}
	runMetrics := &models.RunMetrics{LatencyP95: 100}
	tick := testOnlineTickInput(t, scenario, rm, runMetrics, opt)
	loop := &onlineCtrlLoopState{
		stableRepDown:     make(map[string]int),
		stableVertCPUDown: make(map[string]int),
		stableVertMemDown: make(map[string]int),
		prevErrFrac:       -1,
	}
	exec.runOnlineControllerPolicyStep(runID, scenario, opt, rm, state, loop, tick, "p95_latency", false, nil)

	rec, ok := store.Get(runID)
	if !ok {
		t.Fatal("missing run")
	}
	step := lastStepWithAction(rec, "host_cpu_scale_up")
	if step == nil {
		t.Fatalf("expected p95-primary host_cpu_scale_up, history=%d", len(rec.OptimizationHistory))
	}
	if step.GetPrimaryTarget() != "p95_latency" {
		t.Fatalf("primary_target=%q", step.GetPrimaryTarget())
	}
	if step.GetDecisionMetric() != "max_host_cpu_utilization" {
		t.Fatalf("decision_metric=%q", step.GetDecisionMetric())
	}
	if step.GetObjectiveUnit() != "ms" {
		t.Fatalf("objective_unit=%q", step.GetObjectiveUnit())
	}
}

func TestOnlineSequentialCPUPressureThenRelaxHostVertical(t *testing.T) {
	t.Parallel()
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	runID := "cpu-seq-vert"
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
	exec.mu.Lock()
	exec.resourceManagers[runID] = rm
	exec.workloadStates[runID] = wsStub
	exec.mu.Unlock()

	opt := &simulationv1.OptimizationConfig{
		Online:                    true,
		StepSize:                  1,
		MaxHosts:                  1,
		MinHosts:                  1,
		TargetUtilHigh:            0.7,
		TargetP95LatencyMs:        100,
		OptimizationTargetPrimary: "cpu_utilization",
		MaxHostCpuCores:           6,
		MinHostCpuCores:           1,
		HostCpuStepCores:          1,
		ScaleDownHostCpuUtilMax:   0.45,
	}
	loop := &onlineCtrlLoopState{
		stableRepDown:     make(map[string]int),
		stableVertCPUDown: make(map[string]int),
		stableVertMemDown: make(map[string]int),
		prevErrFrac:       -1,
	}

	h, _ := rm.GetHost("h1")
	h.SetCPUUtilization(0.9)
	hot := &models.RunMetrics{
		LatencyP95:     10,
		TotalRequests:  10,
		FailedRequests: 0,
		ServiceMetrics: map[string]*models.ServiceMetrics{"svc1": {CPUUtilization: 0.85}},
	}
	tickHot := testOnlineTickInput(t, scenario, rm, hot, opt)
	exec.runOnlineControllerPolicyStep(runID, scenario, opt, rm, state, loop, tickHot, "cpu_utilization", true, &cpuPrimaryScaleUpScratch{})

	if c, ok := rm.GetHost("h1"); ok && c.CPUCores() <= 1 {
		t.Fatalf("expected host CPU vertical scale-up under pressure, cores=%d", c.CPUCores())
	}

	h.SetCPUUtilization(0.1)
	cold := &models.RunMetrics{
		LatencyP95:     40,
		TotalRequests:  20,
		FailedRequests: 0,
		ServiceMetrics: map[string]*models.ServiceMetrics{"svc1": {CPUUtilization: 0.15}},
	}
	tickCold := testOnlineTickInput(t, scenario, rm, cold, opt)
	tickCold.stabTicks = 2
	exec.runOnlineControllerPolicyStep(runID, scenario, opt, rm, state, loop, tickCold, "cpu_utilization", true, &cpuPrimaryScaleUpScratch{})
	exec.runOnlineControllerPolicyStep(runID, scenario, opt, rm, state, loop, tickCold, "cpu_utilization", true, &cpuPrimaryScaleUpScratch{})

	rec, ok := store.Get(runID)
	if !ok {
		t.Fatal("missing run")
	}
	if lastStepWithAction(rec, "host_cpu_scale_down") == nil {
		var actions []string
		for _, s := range rec.OptimizationHistory {
			if s != nil {
				actions = append(actions, s.GetAction())
			}
		}
		t.Fatalf("expected host_cpu_scale_down after relax, actions=%v", strings.Join(actions, ","))
	}
}
