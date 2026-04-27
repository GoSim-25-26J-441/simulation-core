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

func noRetryPolicies() *policy.Manager {
	return policy.NewPolicyManager(&config.Policies{})
}

func sumMetric(collector *metrics.Collector, name string) float64 {
	var s float64
	for _, labels := range collector.GetLabelsForMetric(name) {
		for _, p := range collector.GetTimeSeries(name, labels) {
			s += p.Value
		}
	}
	return s
}

func findRootRequestID(eng *engine.Engine) string {
	for _, req := range eng.GetRunManager().ListRequests() {
		if req.ParentID == "" {
			return req.ID
		}
	}
	return ""
}

func sumErrorWithReason(collector *metrics.Collector, reason string) float64 {
	var s float64
	for _, labels := range collector.GetLabelsForMetric(metrics.MetricRequestErrorCount) {
		if labels[metrics.LabelReason] != reason {
			continue
		}
		for _, p := range collector.GetTimeSeries(metrics.MetricRequestErrorCount, labels) {
			s += p.Value
		}
	}
	return s
}

// Downstream failure_rate=1 to an external service causes ingress-visible failure when retries are off.
func TestDownstreamExternalFailureRateAlwaysFailsIngress(t *testing.T) {
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{
			{
				ID:       "api",
				Replicas: 1,
				Model:    "cpu",
				Endpoints: []config.Endpoint{
					{
						Path:         "/call",
						MeanCPUMs:    1,
						CPUSigmaMs:   0,
						NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
						Downstream: []config.DownstreamCall{
							{
								To:            "ext:/x",
								Kind:          "rest",
								CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
								FailureRate:   1,
							},
						},
					},
				},
			},
			{
				ID:       "ext",
				Kind:     "external",
				Replicas: 1,
				Model:    "cpu",
				Endpoints: []config.Endpoint{
					{
						Path:         "/x",
						MeanCPUMs:    1,
						CPUSigmaMs:   0,
						NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
					},
				},
			},
		},
		Workload: []config.WorkloadPattern{
			{
				From: "c",
				To:   "api:/call",
				Arrival: config.ArrivalSpec{
					Type:    "poisson",
					RateRPS: 1,
				},
			},
		},
	}

	eng := engine.NewEngine("ext-fail")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, noRetryPolicies(), 424242)
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(eng, state)
	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "api", map[string]interface{}{
		"service_id":    "api",
		"endpoint_path": "/call",
	})
	if err := eng.Run(500 * time.Millisecond); err != nil {
		t.Fatal(err)
	}

	root, ok := eng.GetRunManager().GetRequest(findRootRequestID(eng))
	if !ok {
		t.Fatal("missing root")
	}
	if root.Status != models.RequestStatusFailed {
		t.Fatalf("expected root failed, got %v err=%q", root.Status, root.Error)
	}
	extErr := sumErrorWithReason(collector, metrics.ReasonExternalFailure)
	if extErr < 1 {
		t.Fatalf("expected external_failure errors, got %v", extErr)
	}
	ing := sumMetric(collector, metrics.MetricIngressLogicalFailure)
	if ing < 1 {
		t.Fatalf("expected ingress logical failure, got %v", ing)
	}
}

// Two parallel sync downstreams to the same DB with max_connections=1: second child observes db_wait_ms > 0.
func TestDatabaseMaxConnectionsSerializesIOAndEmitsDbWait(t *testing.T) {
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{
			{
				ID:       "api",
				Replicas: 1,
				Model:    "cpu",
				Endpoints: []config.Endpoint{
					{
						Path:         "/fan",
						MeanCPUMs:    1,
						CPUSigmaMs:   0,
						NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
						Downstream: []config.DownstreamCall{
							{To: "db:/q", CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}},
							{To: "db:/q", CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}},
						},
					},
				},
			},
			{
				ID:       "db",
				Kind:     "database",
				Role:     "datastore",
				Model:    "db_latency",
				Replicas: 1,
				Behavior: &config.ServiceBehavior{
					MaxConnections: 1,
				},
				Endpoints: []config.Endpoint{
					{
						Path:         "/q",
						MeanCPUMs:    2,
						CPUSigmaMs:   0,
						IOMs:         config.LatencySpec{Mean: 30, Sigma: 0},
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0},
					},
				},
			},
		},
		Workload: []config.WorkloadPattern{
			{
				From: "c",
				To:   "api:/fan",
				Arrival: config.ArrivalSpec{
					Type:    "poisson",
					RateRPS: 1,
				},
			},
		},
	}

	eng := engine.NewEngine("db-wait")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, noRetryPolicies(), 9001)
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(eng, state)
	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "api", map[string]interface{}{
		"service_id":    "api",
		"endpoint_path": "/fan",
	})
	if err := eng.Run(800 * time.Millisecond); err != nil {
		t.Fatal(err)
	}

	root, ok := eng.GetRunManager().GetRequest(findRootRequestID(eng))
	if !ok || root.Status != models.RequestStatusCompleted {
		t.Fatalf("expected completed root ok=%v status=%v", ok, root.Status)
	}

	var dbWait float64
	for _, labels := range collector.GetLabelsForMetric(metrics.MetricDbWaitMs) {
		for _, p := range collector.GetTimeSeries(metrics.MetricDbWaitMs, labels) {
			dbWait += p.Value
		}
	}
	if dbWait <= 0 {
		t.Fatalf("expected positive db_wait_ms aggregate, got %v", dbWait)
	}
}

// Cache hit_rate=1 skips downstream and emits cache_hit_count (no downstream request_count to leaf).
func TestCacheHitRateOneSkipsDownstream(t *testing.T) {
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{
			{
				ID:       "api",
				Replicas: 1,
				Model:    "cpu",
				Endpoints: []config.Endpoint{
					{
						Path:         "/in",
						MeanCPUMs:    1,
						CPUSigmaMs:   0,
						NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
						Downstream: []config.DownstreamCall{
							{To: "cache:/c", CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}},
						},
					},
				},
			},
			{
				ID:       "cache",
				Kind:     "cache",
				Replicas: 1,
				Model:    "cpu",
				Behavior: &config.ServiceBehavior{
					Cache: &config.CacheBehavior{
						HitRate:       1,
						HitLatencyMs:  config.LatencySpec{Mean: 4, Sigma: 0},
						MissLatencyMs: config.LatencySpec{Mean: 40, Sigma: 0},
					},
				},
				Endpoints: []config.Endpoint{
					{
						Path:         "/c",
						MeanCPUMs:    10,
						CPUSigmaMs:   0,
						NetLatencyMs: config.LatencySpec{Mean: 5, Sigma: 0},
						Downstream: []config.DownstreamCall{
							{To: "leaf:/z", CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}},
						},
					},
				},
			},
			{
				ID:       "leaf",
				Replicas: 1,
				Model:    "cpu",
				Endpoints: []config.Endpoint{
					{
						Path:         "/z",
						MeanCPUMs:    5,
						CPUSigmaMs:   0,
						NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
					},
				},
			},
		},
		Workload: []config.WorkloadPattern{
			{
				From: "c",
				To:   "api:/in",
				Arrival: config.ArrivalSpec{
					Type:    "poisson",
					RateRPS: 1,
				},
			},
		},
	}

	eng := engine.NewEngine("cache-hit")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, noRetryPolicies(), 12345)
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(eng, state)
	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "api", map[string]interface{}{
		"service_id":    "api",
		"endpoint_path": "/in",
	})
	if err := eng.Run(400 * time.Millisecond); err != nil {
		t.Fatal(err)
	}

	root, ok := eng.GetRunManager().GetRequest(findRootRequestID(eng))
	if !ok || root.Status != models.RequestStatusCompleted {
		t.Fatalf("expected completed root ok=%v status=%v", ok, root.Status)
	}

	hit := sumMetric(collector, metrics.MetricCacheHitCount)
	if hit < 1 {
		t.Fatalf("expected cache_hit_count, got %v", hit)
	}

	var leafCount float64
	for _, labels := range collector.GetLabelsForMetric(metrics.MetricRequestCount) {
		if labels["service"] != "leaf" {
			continue
		}
		for _, p := range collector.GetTimeSeries(metrics.MetricRequestCount, labels) {
			leafCount += p.Value
		}
	}
	if leafCount != 0 {
		t.Fatalf("expected leaf request_count 0 on cache hit, got %v", leafCount)
	}
}
