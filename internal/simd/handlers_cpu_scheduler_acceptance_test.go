package simd

import (
	"sort"
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/internal/policy"
	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

func baseCPUBurstScenario(cpuCores float64) *config.Scenario {
	return &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 4}},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 1,
				Model:    "cpu",
				CPUCores: cpuCores,
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
}

// Three request_arrival events at the same sim time on one core: hop durations 1s, 2s, 3s (FIFO CPU).
func TestSameTimestampArrivalBurstSerializesCPUFIFO(t *testing.T) {
	eng := engine.NewEngine("burst-fifo")
	scenario := baseCPUBurstScenario(1)
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), 42)
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(eng, state)

	T0 := eng.GetSimTime()
	for i := 0; i < 3; i++ {
		eng.ScheduleAt(engine.EventTypeRequestArrival, T0, nil, "svc1", map[string]interface{}{
			"service_id":    "svc1",
			"endpoint_path": "/test",
		})
	}
	if err := eng.Run(15 * time.Second); err != nil {
		t.Fatal(err)
	}

	var done []*models.Request
	for _, r := range eng.GetRunManager().ListRequests() {
		if r.ServiceName == "svc1" && r.Status == models.RequestStatusCompleted {
			done = append(done, r)
		}
	}
	if len(done) != 3 {
		t.Fatalf("want 3 completed svc1 requests, got %d", len(done))
	}
	sort.Slice(done, func(i, j int) bool {
		return done[i].CompletionTime.Before(done[j].CompletionTime)
	})
	want := []time.Duration{time.Second, 2 * time.Second, 3 * time.Second}
	for i := range done {
		hop := done[i].CompletionTime.Sub(done[i].ArrivalTime)
		if hop != want[i] {
			t.Fatalf("request %d hop latency want %v got %v (queue=%v)", i+1, want[i], hop, done[i].QueueTimeMs)
		}
	}
	if done[0].QueueTimeMs != 0 || done[1].QueueTimeMs < 999 || done[1].QueueTimeMs > 1001 ||
		done[2].QueueTimeMs < 1999 || done[2].QueueTimeMs > 2001 {
		t.Fatalf("queue waits want ~0, ~1000, ~2000 ms got %v %v %v",
			done[0].QueueTimeMs, done[1].QueueTimeMs, done[2].QueueTimeMs)
	}
}

// Two cores: service duration is cpuDemandMs/cores → three completions at 0.5s, 1s, 1.5s wall time.
func TestVerticalCPUTwoCoresFasterThanOneCoreBurst(t *testing.T) {
	eng := engine.NewEngine("burst-2c")
	scenario := baseCPUBurstScenario(2)
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), 43)
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(eng, state)

	T0 := eng.GetSimTime()
	for i := 0; i < 3; i++ {
		eng.ScheduleAt(engine.EventTypeRequestArrival, T0, nil, "svc1", map[string]interface{}{
			"service_id":    "svc1",
			"endpoint_path": "/test",
		})
	}
	if err := eng.Run(15 * time.Second); err != nil {
		t.Fatal(err)
	}

	var done []*models.Request
	for _, r := range eng.GetRunManager().ListRequests() {
		if r.ServiceName == "svc1" && r.Status == models.RequestStatusCompleted {
			done = append(done, r)
		}
	}
	sort.Slice(done, func(i, j int) bool {
		return done[i].CompletionTime.Before(done[j].CompletionTime)
	})
	want := []time.Duration{500 * time.Millisecond, time.Second, 1500 * time.Millisecond}
	for i := range done {
		hop := done[i].CompletionTime.Sub(done[i].ArrivalTime)
		if hop != want[i] {
			t.Fatalf("request %d hop want %v got %v", i+1, want[i], hop)
		}
	}
}

// After vertical scale-up, new reservations use the updated core count (shorter CPU wall for same demand).
func TestRuntimeCPUScaleAffectsNewScheduledWork(t *testing.T) {
	eng := engine.NewEngine("scale-cpu")
	scenario := baseCPUBurstScenario(1)
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), 44)
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(eng, state)

	T0 := eng.GetSimTime()
	eng.ScheduleAt(engine.EventTypeRequestArrival, T0, nil, "svc1", map[string]interface{}{
		"service_id":    "svc1",
		"endpoint_path": "/test",
	})
	if err := eng.Run(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	var first *models.Request
	for _, r := range eng.GetRunManager().ListRequests() {
		if r.ServiceName == "svc1" && r.Status == models.RequestStatusCompleted {
			first = r
			break
		}
	}
	if first == nil {
		t.Fatal("expected first request completed")
	}
	if got := first.CompletionTime.Sub(first.ArrivalTime); got != time.Second {
		t.Fatalf("first hop with 1 core want 1s got %v", got)
	}

	if err := rm.UpdateServiceResources("svc1", 2, 512); err != nil {
		t.Fatal(err)
	}

	T1 := eng.GetSimTime()
	eng.ScheduleAt(engine.EventTypeRequestArrival, T1, nil, "svc1", map[string]interface{}{
		"service_id":    "svc1",
		"endpoint_path": "/test",
	})
	if err := eng.Run(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	var second *models.Request
	for _, r := range eng.GetRunManager().ListRequests() {
		if r.ServiceName == "svc1" && r.Status == models.RequestStatusCompleted && r.ID != first.ID {
			second = r
			break
		}
	}
	if second == nil {
		t.Fatal("expected second request completed")
	}
	if got := second.CompletionTime.Sub(second.ArrivalTime); got != 500*time.Millisecond {
		t.Fatalf("second hop after scale to 2 cores want 500ms wall got %v", got)
	}
}

// Instance CPU utilization gauge samples stay within [0,1].
func TestCPUUtilizationGaugeBoundedUnderFIFO(t *testing.T) {
	eng := engine.NewEngine("cpu-util")
	scenario := baseCPUBurstScenario(1)
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), 45)
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(eng, state)

	T0 := eng.GetSimTime()
	for i := 0; i < 3; i++ {
		eng.ScheduleAt(engine.EventTypeRequestArrival, T0, nil, "svc1", map[string]interface{}{
			"service_id":    "svc1",
			"endpoint_path": "/test",
		})
	}
	if err := eng.Run(15 * time.Second); err != nil {
		t.Fatal(err)
	}

	for _, labels := range collector.GetLabelsForMetric(metrics.MetricCPUUtilization) {
		if labels["service"] != "svc1" {
			continue
		}
		for _, p := range collector.GetTimeSeries(metrics.MetricCPUUtilization, labels) {
			if p.Value < -1e-6 || p.Value > 1.0+1e-6 {
				t.Fatalf("cpu_utilization out of bounds: %v labels=%v", p.Value, labels)
			}
		}
	}
}
