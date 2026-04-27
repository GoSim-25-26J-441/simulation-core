package simd

import (
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/internal/interaction"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/internal/policy"
	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

func retryPolicies(maxRetries int) *policy.Manager {
	pm := policy.NewPolicyManager(&config.Policies{
		Retries: &config.RetryPolicy{
			Enabled:    true,
			MaxRetries: maxRetries,
			Backoff:    "exponential",
			BaseMs:     10,
		},
	})
	pm.SetCircuitBreaker(policy.NewCircuitBreakerPolicy(true, 10, 1, 50*time.Millisecond))
	return pm
}

// Two sync downstreams to svcB: first child allocates most of host RAM; second start fails memory,
// retries after backoff once the first child completes and releases memory.
func TestSyncDownstreamMemoryStartFailureRetriesThenSucceeds(t *testing.T) {
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 1}},
		Services: []config.Service{
			{
				ID:       "svcA",
				Replicas: 1,
				MemoryMB: 32,
				Endpoints: []config.Endpoint{
					{
						Path:            "/fan",
						MeanCPUMs:       2,
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 32,
						Downstream: []config.DownstreamCall{
							{To: "svcB:/b1", CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}},
							{To: "svcB:/b2", CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}},
						},
					},
				},
			},
			{
				ID:       "svcB",
				Replicas: 2,
				MemoryMB: 256,
				Endpoints: []config.Endpoint{
					{
						Path:            "/b1",
						MeanCPUMs:       5,
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 800,
					},
					{
						Path:            "/b2",
						MeanCPUMs:       5,
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 800,
					},
				},
			},
		},
	}
	eng := engine.NewEngine("mem-retry")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, retryPolicies(3), 201)
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(eng, state)

	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "svcA", map[string]interface{}{
		"service_id":    "svcA",
		"endpoint_path": "/fan",
	})
	if err := eng.Run(500 * time.Millisecond); err != nil {
		t.Fatal(err)
	}

	root, ok := eng.GetRunManager().GetRequest(findIngressRequestID(eng))
	if !ok || root.Status != models.RequestStatusCompleted {
		t.Fatalf("expected root completed, ok=%v status=%v err=%q", ok, root.Status, root.Error)
	}

	var memErr float64
	for _, labels := range collector.GetLabelsForMetric(metrics.MetricRequestErrorCount) {
		if labels[metrics.LabelReason] != metrics.ReasonMemoryCapacity {
			continue
		}
		for _, p := range collector.GetTimeSeries(metrics.MetricRequestErrorCount, labels) {
			memErr += p.Value
		}
	}
	if memErr < 1 {
		t.Fatalf("expected at least one memory_capacity error from failed attempt, got %v", memErr)
	}
}

func TestSyncTimeoutRetryExhaustedPropagatesFailure(t *testing.T) {
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8}},
		Services: []config.Service{
			{
				ID:       "svcA",
				Replicas: 1,
				Endpoints: []config.Endpoint{
					{
						Path:            "/root",
						MeanCPUMs:       5,
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 32,
						Downstream: []config.DownstreamCall{
							{
								To:            "svcB:/slow",
								CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
								TimeoutMs:     20,
							},
						},
					},
				},
			},
			{
				ID:       "svcB",
				Replicas: 2,
				Endpoints: []config.Endpoint{
					{
						Path:            "/slow",
						MeanCPUMs:       100,
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 32,
					},
				},
			},
		},
	}
	pm := policy.NewPolicyManager(&config.Policies{
		Retries: &config.RetryPolicy{Enabled: true, MaxRetries: 1, Backoff: "constant", BaseMs: 5},
	})
	pm.SetCircuitBreaker(policy.NewCircuitBreakerPolicy(true, 20, 1, 50*time.Millisecond))

	eng := engine.NewEngine("retry-ex")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, pm, 202)
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(eng, state)

	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "svcA", map[string]interface{}{
		"service_id":    "svcA",
		"endpoint_path": "/root",
	})
	if err := eng.Run(600 * time.Millisecond); err != nil {
		t.Fatal(err)
	}

	root, ok := eng.GetRunManager().GetRequest(findIngressRequestID(eng))
	if !ok || root.Status != models.RequestStatusFailed {
		t.Fatalf("expected root failed after exhausted retries, ok=%v status=%v", ok, root.Status)
	}

	var timeoutErr float64
	for _, labels := range collector.GetLabelsForMetric(metrics.MetricRequestErrorCount) {
		if labels[metrics.LabelReason] != metrics.ReasonTimeout {
			continue
		}
		for _, p := range collector.GetTimeSeries(metrics.MetricRequestErrorCount, labels) {
			timeoutErr += p.Value
		}
	}
	if timeoutErr < 2 {
		t.Fatalf("expected timeout errors for initial + retry attempts, got %v", timeoutErr)
	}
}

func TestSyncRetrySuccessWithVariableCPUSigma(t *testing.T) {
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8}},
		Services: []config.Service{
			{
				ID:       "svcA",
				Replicas: 1,
				Endpoints: []config.Endpoint{
					{
						Path:            "/root",
						MeanCPUMs:       5,
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 32,
						Downstream: []config.DownstreamCall{
							{
								To:            "svcB:/v",
								CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
								TimeoutMs:     25,
							},
						},
					},
				},
			},
			{
				ID:       "svcB",
				Replicas: 2,
				Endpoints: []config.Endpoint{
					{
						Path:            "/v",
						MeanCPUMs:       100,
						CPUSigmaMs:      40,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 32,
					},
				},
			},
		},
	}
	pm := policy.NewPolicyManager(&config.Policies{
		Retries: &config.RetryPolicy{Enabled: true, MaxRetries: 2, Backoff: "constant", BaseMs: 8},
	})
	// No circuit breaker: this test loops thousands of seeds and records many synthetic failures; a CB would open and block ingress.

	var found bool
	for seed := int64(1); seed <= 3000; seed++ {
		eng := engine.NewEngine("rng-retry")
		rm := resource.NewManager()
		if err := rm.InitializeFromScenario(scenario); err != nil {
			t.Fatal(err)
		}
		collector := metrics.NewCollector()
		collector.Start()
		state, err := newScenarioState(scenario, rm, collector, pm, seed)
		if err != nil {
			t.Fatal(err)
		}
		RegisterHandlers(eng, state)
		eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "svcA", map[string]interface{}{
			"service_id":    "svcA",
			"endpoint_path": "/root",
		})
		if err := eng.Run(800 * time.Millisecond); err != nil {
			t.Fatal(err)
		}
		root, ok := eng.GetRunManager().GetRequest(findIngressRequestID(eng))
		if !ok {
			continue
		}
		if root.Status != models.RequestStatusCompleted {
			continue
		}
		var timeoutErr float64
		for _, labels := range collector.GetLabelsForMetric(metrics.MetricRequestErrorCount) {
			if labels[metrics.LabelReason] != metrics.ReasonTimeout {
				continue
			}
			for _, p := range collector.GetTimeSeries(metrics.MetricRequestErrorCount, labels) {
				timeoutErr += p.Value
			}
		}
		if timeoutErr < 1 {
			continue
		}
		var maxRoot float64
		for _, labels := range collector.GetLabelsForMetric(metrics.MetricRootRequestLatency) {
			for _, p := range collector.GetTimeSeries(metrics.MetricRootRequestLatency, labels) {
				if p.Value > maxRoot {
					maxRoot = p.Value
				}
			}
		}
		if maxRoot < 35 {
			continue
		}
		found = true
		break
	}
	if !found {
		t.Fatal("no seed in 1..3000 produced retry timeout then success with extended root latency")
	}
}

func TestAsyncTimeoutRetryDoesNotInflateIngressRootLatency(t *testing.T) {
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8}},
		Services: []config.Service{
			{
				ID:       "svcA",
				Replicas: 1,
				Endpoints: []config.Endpoint{
					{
						Path:            "/root",
						MeanCPUMs:       5,
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 32,
						Downstream: []config.DownstreamCall{
							{
								To:            "svcB:/slow",
								Mode:          "async",
								CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
								TimeoutMs:     15,
							},
						},
					},
				},
			},
			{
				ID:       "svcB",
				Replicas: 1,
				Endpoints: []config.Endpoint{
					{
						Path:            "/slow",
						MeanCPUMs:       200,
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 32,
					},
				},
			},
		},
	}
	pm := policy.NewPolicyManager(&config.Policies{
		Retries: &config.RetryPolicy{Enabled: true, MaxRetries: 2, Backoff: "constant", BaseMs: 12},
	})
	eng := engine.NewEngine("async-retry")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, pm, 303)
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(eng, state)
	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "svcA", map[string]interface{}{
		"service_id":    "svcA",
		"endpoint_path": "/root",
	})
	if err := eng.Run(900 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	var maxRoot float64
	for _, labels := range collector.GetLabelsForMetric(metrics.MetricRootRequestLatency) {
		for _, p := range collector.GetTimeSeries(metrics.MetricRootRequestLatency, labels) {
			if p.Value > maxRoot {
				maxRoot = p.Value
			}
		}
	}
	if maxRoot > 12 {
		t.Fatalf("expected ingress root ~5ms without waiting on async retries, got %v", maxRoot)
	}
	var downstreamReq int64
	for _, labels := range collector.GetLabelsForMetric(metrics.MetricRequestCount) {
		if labels[metrics.LabelOrigin] != metrics.OriginDownstream {
			continue
		}
		for _, p := range collector.GetTimeSeries(metrics.MetricRequestCount, labels) {
			downstreamReq += int64(p.Value)
		}
	}
	if downstreamReq < 2 {
		t.Fatalf("expected at least 2 downstream attempts (retry), got %d", downstreamReq)
	}
}

func TestMaxRetriesZeroMatchesNoRetryTimeout(t *testing.T) {
	base := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8}},
		Services: []config.Service{
			{
				ID:       "svcA",
				Replicas: 1,
				Endpoints: []config.Endpoint{
					{
						Path:            "/root",
						MeanCPUMs:       5,
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 32,
						Downstream: []config.DownstreamCall{
							{
								To:            "svcB:/slow",
								CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
								TimeoutMs:     20,
							},
						},
					},
				},
			},
			{
				ID:       "svcB",
				Replicas: 1,
				Endpoints: []config.Endpoint{
					{
						Path:            "/slow",
						MeanCPUMs:       100,
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 32,
					},
				},
			},
		},
	}
	run := func(pm *policy.Manager, seed int64) (timeoutCount float64, downstream int64) {
		eng := engine.NewEngine("mr0")
		rm := resource.NewManager()
		if err := rm.InitializeFromScenario(base); err != nil {
			t.Fatal(err)
		}
		collector := metrics.NewCollector()
		collector.Start()
		state, err := newScenarioState(base, rm, collector, pm, seed)
		if err != nil {
			t.Fatal(err)
		}
		RegisterHandlers(eng, state)
		eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "svcA", map[string]interface{}{
			"service_id":    "svcA",
			"endpoint_path": "/root",
		})
		if err := eng.Run(500 * time.Millisecond); err != nil {
			t.Fatal(err)
		}
		for _, labels := range collector.GetLabelsForMetric(metrics.MetricRequestErrorCount) {
			if labels[metrics.LabelReason] != metrics.ReasonTimeout {
				continue
			}
			for _, p := range collector.GetTimeSeries(metrics.MetricRequestErrorCount, labels) {
				timeoutCount += p.Value
			}
		}
		for _, labels := range collector.GetLabelsForMetric(metrics.MetricRequestCount) {
			if labels[metrics.LabelOrigin] != metrics.OriginDownstream {
				continue
			}
			for _, p := range collector.GetTimeSeries(metrics.MetricRequestCount, labels) {
				downstream += int64(p.Value)
			}
		}
		return timeoutCount, downstream
	}

	pmNil := policy.NewPolicyManager(&config.Policies{})
	pmZero := policy.NewPolicyManager(&config.Policies{
		Retries: &config.RetryPolicy{Enabled: true, MaxRetries: 0, Backoff: "constant", BaseMs: 10},
	})
	c1, d1 := run(pmNil, 99)
	c2, d2 := run(pmZero, 99)
	if c1 != c2 || d1 != d2 {
		t.Fatalf("max_retries=0 should match nil policy: nil timeouts=%v downstream=%v, zero retries timeouts=%v downstream=%v", c1, d1, c2, d2)
	}
}

func TestTimeoutErrorLabelsPreserveMetadataWithRetry(t *testing.T) {
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8}},
		Services: []config.Service{
			{
				ID:       "svcA",
				Replicas: 1,
				Endpoints: []config.Endpoint{
					{
						Path:            "/root",
						MeanCPUMs:       2,
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 32,
						Downstream: []config.DownstreamCall{
							{
								To:            "svcB:/x",
								CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
								TimeoutMs:     5,
							},
						},
					},
				},
			},
			{
				ID:       "svcB",
				Replicas: 1,
				Endpoints: []config.Endpoint{
					{
						Path:            "/x",
						MeanCPUMs:       100,
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 32,
					},
				},
			},
		},
	}
	pm := policy.NewPolicyManager(&config.Policies{
		Retries: &config.RetryPolicy{Enabled: true, MaxRetries: 1, Backoff: "constant", BaseMs: 3},
	})
	eng := engine.NewEngine("lbl-retry")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, pm, 404)
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(eng, state)
	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "svcA", map[string]interface{}{
		"service_id":    "svcA",
		"endpoint_path": "/root",
		"traffic_class": "gold",
		"source_kind":   "partner",
	})
	if err := eng.Run(400 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, labels := range collector.GetLabelsForMetric(metrics.MetricRequestErrorCount) {
		if labels[metrics.LabelReason] != metrics.ReasonTimeout {
			continue
		}
		if labels[metrics.LabelTrafficClass] == "gold" && labels[metrics.LabelSourceKind] == "partner" && labels[metrics.LabelOrigin] == metrics.OriginDownstream {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected timeout error with traffic_class and source_kind preserved")
	}
}

func TestSyncRetryPreservesSameZonePenaltyAfterCallerRemoval(t *testing.T) {
	scenario := &config.Scenario{
		Network: &config.NetworkConfig{
			SameZoneLatencyMs: config.LatencySpec{Mean: 21, Sigma: 0},
		},
		Hosts: []config.Host{
			{ID: "h1", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "h2", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "h3", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
		},
		Services: []config.Service{
			{
				ID:       "svcA",
				Replicas: 2,
				Model:    "cpu",
				Placement: &config.PlacementPolicy{
					RequiredZones: []string{"zone-a"},
				},
				Endpoints: []config.Endpoint{
					{
						Path:         "/root",
						MeanCPUMs:    2,
						CPUSigmaMs:   0,
						NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
						Downstream: []config.DownstreamCall{
							{
								To:            "svcB:/slow",
								CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
								TimeoutMs:     20,
							},
						},
					},
				},
			},
			{
				ID:       "svcB",
				Replicas: 1,
				Model:    "cpu",
				Placement: &config.PlacementPolicy{
					RequiredZones:        []string{"zone-a"},
					AntiAffinityServices: []string{"svcA"},
				},
				Endpoints: []config.Endpoint{
					{Path: "/slow", MeanCPUMs: 100, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}},
				},
			},
		},
		Policies: &config.Policies{
			Retries: &config.RetryPolicy{Enabled: true, MaxRetries: 1, Backoff: "constant", BaseMs: 50},
		},
	}
	eng := engine.NewEngine("retry-topology-sync")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, retryPolicies(1), 9201)
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(eng, state)
	parent := &models.Request{
		ID:          "sync-parent",
		TraceID:     "sync-trace",
		ServiceName: "svcA",
		Endpoint:    "/root",
		Status:      models.RequestStatusProcessing,
		ArrivalTime: eng.GetSimTime(),
		Metadata: map[string]interface{}{
			"instance_id": "svcA-instance-1",
		},
	}
	eng.GetRunManager().AddRequest(parent)
	scheduleDownstreamCallEvent(state, eng, parent, interaction.ResolvedCall{
		ServiceID: "svcB",
		Path:      "/slow",
		Call:      scenario.Services[0].Endpoints[0].Downstream[0],
	}, eng.GetSimTime(), 1, 0, false)

	if err := eng.Run(30 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	before := metrics.ConvertToRunMetrics(collector, nil, nil)
	tDrain := eng.GetSimTime()
	if err := rm.ScaleServiceWithOptions("svcA", 1, resource.ScaleServiceOptions{SimTime: tDrain, DrainTimeout: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	rm.ProcessDrainingInstances(tDrain.Add(2 * time.Millisecond))
	if err := eng.Run(300 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	after := metrics.ConvertToRunMetrics(collector, nil, nil)
	if after.TopologyLatencyPenaltyMsTotal <= before.TopologyLatencyPenaltyMsTotal {
		t.Fatalf("expected retry to add topology penalty after caller removal, before=%v after=%v", before.TopologyLatencyPenaltyMsTotal, after.TopologyLatencyPenaltyMsTotal)
	}
}

func TestAsyncRetryPreservesSameZonePenaltyAfterCallerRemoval(t *testing.T) {
	scenario := &config.Scenario{
		Network: &config.NetworkConfig{
			SameZoneLatencyMs: config.LatencySpec{Mean: 19, Sigma: 0},
		},
		Hosts: []config.Host{
			{ID: "h1", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "h2", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "h3", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
		},
		Services: []config.Service{
			{
				ID:       "svcA",
				Replicas: 2,
				Model:    "cpu",
				Placement: &config.PlacementPolicy{
					RequiredZones: []string{"zone-a"},
				},
				Endpoints: []config.Endpoint{
					{
						Path:         "/root",
						MeanCPUMs:    2,
						CPUSigmaMs:   0,
						NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
						Downstream: []config.DownstreamCall{
							{
								To:            "svcB:/slow",
								Mode:          "async",
								CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
								TimeoutMs:     20,
							},
						},
					},
				},
			},
			{
				ID:       "svcB",
				Replicas: 1,
				Model:    "cpu",
				Placement: &config.PlacementPolicy{
					RequiredZones:        []string{"zone-a"},
					AntiAffinityServices: []string{"svcA"},
				},
				Endpoints: []config.Endpoint{
					{Path: "/slow", MeanCPUMs: 100, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}},
				},
			},
		},
		Policies: &config.Policies{
			Retries: &config.RetryPolicy{Enabled: true, MaxRetries: 1, Backoff: "constant", BaseMs: 50},
		},
	}
	eng := engine.NewEngine("retry-topology-async")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, retryPolicies(1), 9202)
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(eng, state)
	parent := &models.Request{
		ID:          "async-parent",
		TraceID:     "async-trace",
		ServiceName: "svcA",
		Endpoint:    "/root",
		Status:      models.RequestStatusProcessing,
		ArrivalTime: eng.GetSimTime(),
		Metadata: map[string]interface{}{
			"instance_id": "svcA-instance-1",
		},
	}
	eng.GetRunManager().AddRequest(parent)
	scheduleDownstreamCallEvent(state, eng, parent, interaction.ResolvedCall{
		ServiceID: "svcB",
		Path:      "/slow",
		Call:      scenario.Services[0].Endpoints[0].Downstream[0],
	}, eng.GetSimTime(), 1, 1, true)

	if err := eng.Run(30 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	before := metrics.ConvertToRunMetrics(collector, nil, nil)
	tDrain := eng.GetSimTime()
	if err := rm.ScaleServiceWithOptions("svcA", 1, resource.ScaleServiceOptions{SimTime: tDrain, DrainTimeout: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	rm.ProcessDrainingInstances(tDrain.Add(2 * time.Millisecond))
	if err := eng.Run(300 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	after := metrics.ConvertToRunMetrics(collector, nil, nil)
	if after.TopologyLatencyPenaltyMsTotal <= before.TopologyLatencyPenaltyMsTotal {
		t.Fatalf("expected async retry to add topology penalty after caller removal, before=%v after=%v", before.TopologyLatencyPenaltyMsTotal, after.TopologyLatencyPenaltyMsTotal)
	}
}

func TestQueueRetryPublishPreservesSameZonePenaltyAfterCallerRemoval(t *testing.T) {
	scenario := &config.Scenario{
		Network: &config.NetworkConfig{
			SameZoneLatencyMs: config.LatencySpec{Mean: 23, Sigma: 0},
		},
		Hosts: []config.Host{
			{ID: "h1", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "h2", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "h3", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
		},
		Services: []config.Service{
			{
				ID:       "producer",
				Replicas: 2,
				Model:    "cpu",
				Placement: &config.PlacementPolicy{
					RequiredZones: []string{"zone-a"},
				},
				Endpoints: []config.Endpoint{{Path: "/p", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
			},
			{
				ID:       "consumer",
				Replicas: 1,
				Model:    "cpu",
				Placement: &config.PlacementPolicy{
					RequiredZones:        []string{"zone-a"},
					AntiAffinityServices: []string{"producer"},
				},
				Endpoints: []config.Endpoint{{Path: "/consume", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
			},
			{
				ID:        "queue",
				Kind:      "queue",
				Replicas:  1,
				Model:     "cpu",
				Endpoints: []config.Endpoint{{Path: "/q", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
				Behavior: &config.ServiceBehavior{
					Queue: &config.QueueBehavior{
						ConsumerTarget:      "consumer:/consume",
						ConsumerConcurrency: 1,
						DeliveryLatencyMs:   config.LatencySpec{Mean: 50, Sigma: 0},
					},
				},
			},
		},
	}
	eng, state, rm, collector := setupBrokerTopologyState(t, scenario)
	t0 := eng.GetSimTime()
	parent := &models.Request{
		ID:          "queue-retry-parent",
		TraceID:     "queue-retry-trace",
		ServiceName: "producer",
		Endpoint:    "/p",
		Status:      models.RequestStatusProcessing,
		ArrivalTime: t0,
		Metadata: map[string]interface{}{
			"instance_id": "producer-instance-1",
		},
	}
	eng.GetRunManager().AddRequest(parent)
	if err := rm.ScaleServiceWithOptions("producer", 1, resource.ScaleServiceOptions{SimTime: t0, DrainTimeout: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	rm.ProcessDrainingInstances(t0.Add(2 * time.Millisecond))
	scheduleDownstreamWithCallerOverhead(state, eng, parent, interaction.ResolvedCall{
		ServiceID: "queue",
		Path:      "/q",
		Call:      config.DownstreamCall{Mode: "async"},
	}, t0, 1, 1, true, true, 1, "queue-retry-logical", downstreamCallerTopology{
		CallerInstanceID: "producer-instance-1",
		CallerHostZone:   "zone-a",
		CallerHostID:     "h2",
	})
	if err := eng.Run(300 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	rmOut := metrics.ConvertToRunMetrics(collector, nil, nil)
	if rmOut.SameZoneLatencyPenaltyMsTotal <= 0 {
		t.Fatalf("expected same-zone penalty for queue retry publish after caller removal, got total=%v mean=%v", rmOut.SameZoneLatencyPenaltyMsTotal, rmOut.SameZoneLatencyPenaltyMsMean)
	}
}

func TestTopicRetryPublishPreservesSameZonePenaltyAfterCallerRemoval(t *testing.T) {
	scenario := &config.Scenario{
		Network: &config.NetworkConfig{
			SameZoneLatencyMs: config.LatencySpec{Mean: 25, Sigma: 0},
		},
		Hosts: []config.Host{
			{ID: "h1", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "h2", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "h3", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
		},
		Services: []config.Service{
			{
				ID:       "producer",
				Replicas: 2,
				Model:    "cpu",
				Placement: &config.PlacementPolicy{
					RequiredZones: []string{"zone-a"},
				},
				Endpoints: []config.Endpoint{{Path: "/p", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
			},
			{
				ID:       "consumer",
				Replicas: 1,
				Model:    "cpu",
				Placement: &config.PlacementPolicy{
					RequiredZones:        []string{"zone-a"},
					AntiAffinityServices: []string{"producer"},
				},
				Endpoints: []config.Endpoint{{Path: "/consume", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
			},
			{
				ID:        "topic",
				Kind:      "topic",
				Replicas:  1,
				Model:     "cpu",
				Endpoints: []config.Endpoint{{Path: "/events", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
				Behavior: &config.ServiceBehavior{
					Topic: &config.TopicBehavior{
						DeliveryLatencyMs: config.LatencySpec{Mean: 50, Sigma: 0},
						Subscribers: []config.TopicSubscriber{{
							Name: "sub", ConsumerGroup: "g1", ConsumerTarget: "consumer:/consume", ConsumerConcurrency: 1,
						}},
					},
				},
			},
		},
	}
	eng, state, rm, collector := setupBrokerTopologyState(t, scenario)
	t0 := eng.GetSimTime()
	parent := &models.Request{
		ID:          "topic-retry-parent",
		TraceID:     "topic-retry-trace",
		ServiceName: "producer",
		Endpoint:    "/p",
		Status:      models.RequestStatusProcessing,
		ArrivalTime: t0,
		Metadata: map[string]interface{}{
			"instance_id": "producer-instance-1",
		},
	}
	eng.GetRunManager().AddRequest(parent)
	if err := rm.ScaleServiceWithOptions("producer", 1, resource.ScaleServiceOptions{SimTime: t0, DrainTimeout: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	rm.ProcessDrainingInstances(t0.Add(2 * time.Millisecond))
	scheduleDownstreamWithCallerOverhead(state, eng, parent, interaction.ResolvedCall{
		ServiceID: "topic",
		Path:      "/events",
		Call:      config.DownstreamCall{Mode: "async"},
	}, t0, 1, 1, true, true, 1, "topic-retry-logical", downstreamCallerTopology{
		CallerInstanceID: "producer-instance-1",
		CallerHostZone:   "zone-a",
		CallerHostID:     "h2",
	})
	if err := eng.Run(300 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	rmOut := metrics.ConvertToRunMetrics(collector, nil, nil)
	if rmOut.SameZoneLatencyPenaltyMsTotal <= 0 {
		t.Fatalf("expected same-zone penalty for topic retry publish after caller removal, got total=%v mean=%v", rmOut.SameZoneLatencyPenaltyMsTotal, rmOut.SameZoneLatencyPenaltyMsMean)
	}
}
