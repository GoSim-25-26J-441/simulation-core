package simd

import (
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/internal/policy"
	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

func TestSyncDownstreamTimeoutFailsRootAroundDeadline(t *testing.T) {
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
	eng := engine.NewEngine("sync-to")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), 99)
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(eng, state)

	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "svcA", map[string]interface{}{
		"service_id":     "svcA",
		"endpoint_path":  "/root",
		"traffic_class":  "ingress",
		"source_kind":    "test-client",
	})
	if err := eng.Run(500 * time.Millisecond); err != nil {
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
	// Ingress T0, A completes +5ms, B arrives +5ms, deadline +25ms => root failure ~25ms
	if maxRoot < 24 || maxRoot > 28 {
		t.Fatalf("expected root latency ~25ms, got %v", maxRoot)
	}

	root, ok := eng.GetRunManager().GetRequest(findIngressRequestID(eng))
	if ok && root.Status != models.RequestStatusFailed {
		t.Fatalf("expected ingress failed, got %v err=%q", root.Status, root.Error)
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
		t.Fatalf("expected timeout error count, got %v", timeoutErr)
	}
}

func TestSyncDownstreamCompletesBeforeTimeout(t *testing.T) {
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
								To:            "svcB:/fast",
								CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
								TimeoutMs:     100,
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
						Path:            "/fast",
						MeanCPUMs:       20,
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 32,
					},
				},
			},
		},
	}
	eng := engine.NewEngine("sync-ok")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), 100)
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(eng, state)

	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "svcA", map[string]interface{}{
		"service_id":    "svcA",
		"endpoint_path": "/root",
	})
	if err := eng.Run(200 * time.Millisecond); err != nil {
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
	if maxRoot < 24 || maxRoot > 32 {
		t.Fatalf("expected root latency ~25ms (5+20), got %v", maxRoot)
	}

	root, ok := eng.GetRunManager().GetRequest(findIngressRequestID(eng))
	if ok && root.Status != models.RequestStatusCompleted {
		t.Fatalf("expected ingress completed, got %v", root.Status)
	}
}

func TestAsyncTimeoutDoesNotBlockIngressRoot(t *testing.T) {
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
	eng := engine.NewEngine("async-to")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), 101)
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

	var maxRoot float64
	for _, labels := range collector.GetLabelsForMetric(metrics.MetricRootRequestLatency) {
		for _, p := range collector.GetTimeSeries(metrics.MetricRootRequestLatency, labels) {
			if p.Value > maxRoot {
				maxRoot = p.Value
			}
		}
	}
	if maxRoot > 12 {
		t.Fatalf("expected ingress root ~5ms, got %v", maxRoot)
	}

	root, _ := eng.GetRunManager().GetRequest(findIngressRequestID(eng))
	if root.Status != models.RequestStatusCompleted {
		t.Fatalf("expected ingress completed, got %v", root.Status)
	}

	var internal int64
	for _, labels := range collector.GetLabelsForMetric(metrics.MetricRequestCount) {
		if labels[metrics.LabelOrigin] != metrics.OriginDownstream {
			continue
		}
		for _, p := range collector.GetTimeSeries(metrics.MetricRequestCount, labels) {
			internal += int64(p.Value)
		}
	}
	if internal < 1 {
		t.Fatalf("expected internal downstream request_count, got %d", internal)
	}
}

func TestNestedSyncTimeoutPropagatesToRoot(t *testing.T) {
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8}},
		Services: []config.Service{
			{
				ID:       "svcA",
				Replicas: 1,
				Endpoints: []config.Endpoint{
					{
						Path:            "/a",
						MeanCPUMs:       5,
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 32,
						Downstream: []config.DownstreamCall{
							{To: "svcB:/b", CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}},
						},
					},
				},
			},
			{
				ID:       "svcB",
				Replicas: 1,
				Endpoints: []config.Endpoint{
					{
						Path:            "/b",
						MeanCPUMs:       5,
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 32,
						Downstream: []config.DownstreamCall{
							{
								To:            "svcC:/c",
								CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
								TimeoutMs:     8,
							},
						},
					},
				},
			},
			{
				ID:       "svcC",
				Replicas: 1,
				Endpoints: []config.Endpoint{
					{
						Path:            "/c",
						MeanCPUMs:       200,
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 32,
					},
				},
			},
		},
	}
	eng := engine.NewEngine("nest-to")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), 102)
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(eng, state)

	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "svcA", map[string]interface{}{
		"service_id":    "svcA",
		"endpoint_path": "/a",
	})
	if err := eng.Run(800 * time.Millisecond); err != nil {
		t.Fatal(err)
	}

	root, ok := eng.GetRunManager().GetRequest(findIngressRequestID(eng))
	if !ok || root.Status != models.RequestStatusFailed {
		t.Fatalf("expected root failed, ok=%v status=%v", ok, root.Status)
	}

	// One root_request_latency sample for failed ingress trace
	var rootSamples int
	for _, labels := range collector.GetLabelsForMetric(metrics.MetricRootRequestLatency) {
		rootSamples += len(collector.GetTimeSeries(metrics.MetricRootRequestLatency, labels))
	}
	if rootSamples != 1 {
		t.Fatalf("expected exactly one root_request_latency sample, got %d", rootSamples)
	}
}

func TestLateCompletionAfterSyncTimeoutIsIdempotent(t *testing.T) {
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
						MeanCPUMs:       80,
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 32,
					},
				},
			},
		},
	}
	eng := engine.NewEngine("late-idem")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), 103)
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

	var rootSamples int
	for _, labels := range collector.GetLabelsForMetric(metrics.MetricRootRequestLatency) {
		rootSamples += len(collector.GetTimeSeries(metrics.MetricRootRequestLatency, labels))
	}
	if rootSamples != 1 {
		t.Fatalf("expected single root latency emission, got %d", rootSamples)
	}
}

func TestTimeoutErrorLabelsIncludeTrafficClassAndSourceKind(t *testing.T) {
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
	eng := engine.NewEngine("lbl-to")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), 104)
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(eng, state)

	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "svcA", map[string]interface{}{
		"service_id":     "svcA",
		"endpoint_path":  "/root",
		"traffic_class":  "gold",
		"source_kind":    "partner",
	})
	if err := eng.Run(300 * time.Millisecond); err != nil {
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
		t.Fatal("expected timeout error series with traffic_class, source_kind, origin=downstream")
	}
}

func findIngressRequestID(eng *engine.Engine) string {
	for _, req := range eng.GetRunManager().ListRequests() {
		if req.ParentID == "" && req.ServiceName == "svcA" {
			return req.ID
		}
	}
	return ""
}
