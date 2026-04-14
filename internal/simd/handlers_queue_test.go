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

// TestFIFOQueueWaitDESNotSyntheticQueueLength chains three requests on one instance:
// first starts immediately; two are pre-queued. Completions must be +1000ms each (deterministic
// 1000ms CPU, 0 net), not inflated by queue length × mean service behind the running request.
func TestFIFOQueueWaitDESNotSyntheticQueueLength(t *testing.T) {
	eng := engine.NewEngine("fifo-q")
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 1}},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 1,
				Model:    "cpu",
				CPUCores: 1,
				MemoryMB: 512,
				Endpoints: []config.Endpoint{
					{
						Path:            "/test",
						MeanCPUMs:       1000,
						CPUSigmaMs:      0,
						DefaultMemoryMB: 16,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
					},
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
	state, err := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), 42)
	if err != nil {
		t.Fatalf("newScenarioState: %v", err)
	}
	RegisterHandlers(eng, state)

	T0 := eng.GetSimTime()
	inst, err := rm.SelectInstanceForService("svc1")
	if err != nil {
		t.Fatalf("SelectInstanceForService: %v", err)
	}

	makeReq := func(id string) *models.Request {
		return &models.Request{
			ID:          id,
			TraceID:     "trace-1",
			ServiceName: "svc1",
			Endpoint:    "/test",
			Status:      models.RequestStatusPending,
			ArrivalTime: T0,
			Metadata:    make(map[string]interface{}),
		}
	}
	r1 := makeReq("req-1")
	r2 := makeReq("req-2")
	r3 := makeReq("req-3")
	eng.GetRunManager().AddRequest(r1)
	eng.GetRunManager().AddRequest(r2)
	eng.GetRunManager().AddRequest(r3)
	r1.Metadata["instance_id"] = inst.ID()
	r2.Metadata["instance_id"] = inst.ID()
	r3.Metadata["instance_id"] = inst.ID()
	if err := rm.EnqueueRequest(inst.ID(), r2.ID); err != nil {
		t.Fatalf("EnqueueRequest: %v", err)
	}
	if err := rm.EnqueueRequest(inst.ID(), r3.ID); err != nil {
		t.Fatalf("EnqueueRequest: %v", err)
	}

	eng.ScheduleAt(engine.EventTypeRequestStart, T0, r1, "svc1", map[string]interface{}{
		"endpoint_path": "/test",
		"instance_id":   inst.ID(),
	})

	if err := eng.Run(10 * time.Second); err != nil {
		t.Fatalf("Run: %v", err)
	}

	g1, _ := eng.GetRunManager().GetRequest("req-1")
	g2, _ := eng.GetRunManager().GetRequest("req-2")
	g3, _ := eng.GetRunManager().GetRequest("req-3")
	if g1 == nil || g2 == nil || g3 == nil {
		t.Fatal("expected all requests")
	}
	if g1.QueueTimeMs != 0 {
		t.Fatalf("req1 queue wait want 0, got %v", g1.QueueTimeMs)
	}
	if g2.QueueTimeMs < 999 || g2.QueueTimeMs > 1001 {
		t.Fatalf("req2 queue wait want ~1000ms, got %v", g2.QueueTimeMs)
	}
	if g3.QueueTimeMs < 1999 || g3.QueueTimeMs > 2001 {
		t.Fatalf("req3 queue wait want ~2000ms, got %v", g3.QueueTimeMs)
	}

	d1 := g1.CompletionTime.Sub(g1.ArrivalTime)
	d2 := g2.CompletionTime.Sub(g2.ArrivalTime)
	d3 := g3.CompletionTime.Sub(g3.ArrivalTime)
	if d1 != time.Second {
		t.Fatalf("req1 total hop want 1000ms, got %v", d1)
	}
	if d2 != 2*time.Second {
		t.Fatalf("req2 total hop want 2000ms, got %v", d2)
	}
	if d3 != 3*time.Second {
		t.Fatalf("req3 total hop want 3000ms, got %v", d3)
	}

	lbl := labelsForQueueWaitMetrics(r1, "svc1", "/test", inst.ID())
	qw := collector.GetTimeSeries(metrics.MetricQueueWait, lbl)
	if len(qw) != 3 {
		t.Fatalf("expected 3 queue_wait_ms samples, got %d", len(qw))
	}
	if qw[0].Value != 0 || qw[1].Value < 999 || qw[1].Value > 1001 || qw[2].Value < 1999 || qw[2].Value > 2001 {
		t.Fatalf("queue_wait_ms series want 0, ~1000, ~2000 got %v", []float64{qw[0].Value, qw[1].Value, qw[2].Value})
	}
}

func TestQueueWaitMetricIncludesRetryLabels(t *testing.T) {
	req := &models.Request{
		Metadata: map[string]interface{}{
			metaIsRetry:      true,
			metaRetryAttempt: 1,
		},
	}
	l := labelsForQueueWaitMetrics(req, "svc", "/p", "inst-a")
	if l[metrics.LabelIsRetry] != "true" {
		t.Fatalf("expected is_retry label, got %v", l)
	}
	if l[metrics.LabelRetryAttempt] != "1" {
		t.Fatalf("expected attempt label, got %v", l)
	}
	if l["instance"] != "inst-a" {
		t.Fatalf("expected instance label, got %v", l["instance"])
	}
}
