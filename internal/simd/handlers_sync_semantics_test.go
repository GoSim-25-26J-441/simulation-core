package simd

import (
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/internal/policy"
	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func baseSyncTestScenario(mode string) *config.Scenario {
	ds := []config.DownstreamCall{
		{To: "svcB:/b", Mode: mode, CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}},
	}
	return &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 8}},
		Services: []config.Service{
			{
				ID:       "svcA",
				Replicas: 1,
				Endpoints: []config.Endpoint{
					{
						Path:            "/a",
						MeanCPUMs:       10,
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 32,
						Downstream:      ds,
					},
				},
			},
			{
				ID:       "svcB",
				Replicas: 1,
				Endpoints: []config.Endpoint{
					{
						Path:            "/b",
						MeanCPUMs:       20,
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 32,
					},
				},
			},
		},
	}
}

func TestSyncDownstreamRootLatencyIncludesChildServiceTime(t *testing.T) {
	eng := engine.NewEngine("sync-chain")
	scenario := baseSyncTestScenario("") // default sync
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("init rm: %v", err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), 42)
	if err != nil {
		t.Fatalf("scenario state: %v", err)
	}
	RegisterHandlers(eng, state)

	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "svcA", map[string]interface{}{
		"service_id":    "svcA",
		"endpoint_path": "/a",
	})

	if err := eng.Run(200 * time.Millisecond); err != nil {
		t.Fatalf("run: %v", err)
	}

	var maxRoot float64
	for _, labels := range collector.GetLabelsForMetric(metrics.MetricRootRequestLatency) {
		for _, p := range collector.GetTimeSeries(metrics.MetricRootRequestLatency, labels) {
			if p.Value > maxRoot {
				maxRoot = p.Value
			}
		}
	}
	if maxRoot < 29.5 {
		t.Fatalf("expected root latency ~30ms (10 local + 20 child), got max root_request_latency_ms=%v", maxRoot)
	}
}

func TestAsyncDownstreamDoesNotInflateIngressRootLatency(t *testing.T) {
	eng := engine.NewEngine("async-chain")
	scenario := baseSyncTestScenario("async")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("init rm: %v", err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), 42)
	if err != nil {
		t.Fatalf("scenario state: %v", err)
	}
	RegisterHandlers(eng, state)

	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "svcA", map[string]interface{}{
		"service_id":    "svcA",
		"endpoint_path": "/a",
	})

	if err := eng.Run(200 * time.Millisecond); err != nil {
		t.Fatalf("run: %v", err)
	}

	var maxRoot float64
	for _, labels := range collector.GetLabelsForMetric(metrics.MetricRootRequestLatency) {
		for _, p := range collector.GetTimeSeries(metrics.MetricRootRequestLatency, labels) {
			if p.Value > maxRoot {
				maxRoot = p.Value
			}
		}
	}
	if maxRoot > 15 {
		t.Fatalf("expected ingress root latency near svcA local (~10ms), got max root_request_latency_ms=%v", maxRoot)
	}

	var internalReq int64
	for _, labels := range collector.GetLabelsForMetric(metrics.MetricRequestCount) {
		if labels[metrics.LabelOrigin] != metrics.OriginDownstream {
			continue
		}
		for _, p := range collector.GetTimeSeries(metrics.MetricRequestCount, labels) {
			internalReq += int64(p.Value)
		}
	}
	if internalReq < 1 {
		t.Fatalf("expected async downstream to record internal request_count, got internal=%d", internalReq)
	}
}

func TestSyncFanOutParentCompletesAfterBothChildren(t *testing.T) {
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 8}},
		Services: []config.Service{
			{
				ID:       "svcA",
				Replicas: 1,
				Endpoints: []config.Endpoint{
					{
						Path:            "/a",
						MeanCPUMs:       10,
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
				Endpoints: []config.Endpoint{
					{
						Path:            "/b1",
						MeanCPUMs:       20,
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 32,
					},
					{
						Path:            "/b2",
						MeanCPUMs:       20,
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 32,
					},
				},
			},
		},
	}

	eng := engine.NewEngine("sync-fanout")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("init rm: %v", err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), 7)
	if err != nil {
		t.Fatalf("scenario state: %v", err)
	}
	RegisterHandlers(eng, state)

	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "svcA", map[string]interface{}{
		"service_id":    "svcA",
		"endpoint_path": "/a",
	})

	if err := eng.Run(300 * time.Millisecond); err != nil {
		t.Fatalf("run: %v", err)
	}

	var maxRoot float64
	for _, labels := range collector.GetLabelsForMetric(metrics.MetricRootRequestLatency) {
		for _, p := range collector.GetTimeSeries(metrics.MetricRootRequestLatency, labels) {
			if p.Value > maxRoot {
				maxRoot = p.Value
			}
		}
	}
	// Parallel sync children: 10ms local + max(20,20)ms children, not sequential 50ms.
	if maxRoot < 29.5 || maxRoot > 35 {
		t.Fatalf("expected root latency ~30ms for parallel sync fan-out, got %v", maxRoot)
	}
}

func TestEventQueueTieBreakBySequence(t *testing.T) {
	q := engine.NewEventQueue()
	t0 := time.Unix(0, 0)
	a := &engine.Event{Time: t0, Priority: 0, Sequence: 2, Type: engine.EventTypeRequestArrival}
	b := &engine.Event{Time: t0, Priority: 0, Sequence: 1, Type: engine.EventTypeRequestComplete}
	q.Schedule(a)
	q.Schedule(b)
	first := q.Next()
	if first.Sequence != 1 {
		t.Fatalf("expected lower sequence first, got seq=%d type=%s", first.Sequence, first.Type)
	}
}
