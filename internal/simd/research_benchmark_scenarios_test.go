package simd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/internal/policy"
	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

const researchBenchSeed = int64(20260416)

func mustRunScenarioForMetrics(t *testing.T, sc *config.Scenario, dur time.Duration, seed int64) *models.RunMetrics {
	t.Helper()
	rm, err := RunScenarioForMetrics(sc, dur, seed, false)
	if err != nil {
		t.Fatalf("RunScenarioForMetrics failed: %v", err)
	}
	return rm
}

func TestResearchScenarioFixturesLoadRunAndDocumentExpectations(t *testing.T) {
	dir := filepath.Clean(filepath.Join("..", "..", "config", "research_scenarios"))
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		t.Fatalf("glob research scenarios: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("expected research scenario fixtures in %s", dir)
	}
	for _, file := range files {
		file := file
		t.Run(strings.TrimSuffix(filepath.Base(file), ".yaml"), func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			text := string(raw)
			if !strings.Contains(text, "Research purpose:") {
				t.Fatalf("fixture must document Research purpose")
			}
			if !strings.Contains(text, "Expected behavior:") {
				t.Fatalf("fixture must document Expected behavior")
			}
			sc, err := config.ParseScenarioYAML(raw)
			if err != nil {
				t.Fatalf("parse fixture: %v", err)
			}
			rm := mustRunScenarioForMetrics(t, sc, 1*time.Second, researchBenchSeed)
			if rm.IngressRequests <= 0 {
				t.Fatalf("expected ingress requests > 0, got %+v", rm)
			}
			if rm.TotalRequests < rm.IngressRequests {
				t.Fatalf("expected total requests >= ingress requests, total=%v ingress=%v", rm.TotalRequests, rm.IngressRequests)
			}
			if rm.LatencyP95 < rm.LatencyP50 {
				t.Fatalf("expected p95 >= p50, p50=%v p95=%v", rm.LatencyP50, rm.LatencyP95)
			}
			if rm.IngressThroughputRPS <= 0 {
				t.Fatalf("expected ingress throughput > 0, got %v", rm.IngressThroughputRPS)
			}
		})
	}
}

func mustMaxRootLatencyFromCollector(t *testing.T, collector *metrics.Collector) float64 {
	t.Helper()
	var maxRoot float64
	for _, labels := range collector.GetLabelsForMetric(metrics.MetricRootRequestLatency) {
		for _, p := range collector.GetTimeSeries(metrics.MetricRootRequestLatency, labels) {
			if p.Value > maxRoot {
				maxRoot = p.Value
			}
		}
	}
	return maxRoot
}

func TestResearchBenchmark_CpuSaturationQueueWaitGrowth(t *testing.T) {
	net := &config.NetworkConfig{}

	lowArrival := func(rps float64) *config.Scenario {
		return &config.Scenario{
			Network: net,
			Hosts: []config.Host{
				{ID: "h1", Cores: 2, MemoryGB: 16, Zone: "zone-a"},
			},
			Services: []config.Service{
				{
					ID:       "worker",
					Replicas: 1,
					Model:    "cpu",
					Endpoints: []config.Endpoint{{
						Path:            "/work",
						MeanCPUMs:       30,
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 32,
					}},
				},
				{
					ID:       "api",
					Replicas: 1,
					Model:    "cpu",
					Endpoints: []config.Endpoint{{
						Path:            "/in",
						MeanCPUMs:       1,
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 32,
						Downstream: []config.DownstreamCall{{
							To:                    "worker:/work",
							Mode:                  "sync",
							CallLatencyMs:         config.LatencySpec{Mean: 0, Sigma: 0},
							TimeoutMs:             0,
							FailureRate:           0,
							Probability:           1,
							Retryable:             nil,
							DownstreamFractionCPU: 0,
						}},
					}},
				},
			},
			Workload: []config.WorkloadPattern{{
				From: "c",
				To:   "api:/in",
				Metadata: map[string]string{
					"client_zone": "zone-a",
				},
				Arrival: config.ArrivalSpec{Type: "constant", RateRPS: rps},
			}},
		}
	}

	dur := time.Second
	rmLow := mustRunScenarioForMetrics(t, lowArrival(5), dur, researchBenchSeed)
	rmHigh := mustRunScenarioForMetrics(t, lowArrival(80), dur, researchBenchSeed)

	if rmHigh.LatencyP95 <= rmLow.LatencyP95 {
		t.Fatalf("expected p95 latency to not decrease under load: low=%v high=%v", rmLow.LatencyP95, rmHigh.LatencyP95)
	}
	lowWorkerQW := rmLow.ServiceMetrics["worker"].QueueWaitMeanMs
	highWorkerQW := rmHigh.ServiceMetrics["worker"].QueueWaitMeanMs
	if highWorkerQW <= lowWorkerQW+10 {
		t.Fatalf("expected worker queue wait to grow under CPU saturation: low=%v high=%v", lowWorkerQW, highWorkerQW)
	}
}

func TestResearchBenchmark_SyncFanOutRootLatency(t *testing.T) {
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 8}},
		Services: []config.Service{
			{
				ID:       "svcA",
				Replicas: 1,
				Model:    "cpu",
				Endpoints: []config.Endpoint{{
					Path:            "/a",
					MeanCPUMs:       10,
					CPUSigmaMs:      0,
					NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
					DefaultMemoryMB: 32,
					Downstream: []config.DownstreamCall{
						{To: "svcB:/b1", Mode: "sync", CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}},
						{To: "svcB:/b2", Mode: "sync", CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}},
					},
				}},
			},
			{
				ID:       "svcB",
				Replicas: 2,
				Model:    "cpu",
				Endpoints: []config.Endpoint{
					{Path: "/b1", MeanCPUMs: 20, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}, DefaultMemoryMB: 32},
					{Path: "/b2", MeanCPUMs: 20, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}, DefaultMemoryMB: 32},
				},
			},
		},
	}

	eng := engine.NewEngine("research-sync-fanout")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), researchBenchSeed)
	if err != nil {
		t.Fatalf("scenario state: %v", err)
	}
	RegisterHandlers(eng, state)
	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "svcA", map[string]interface{}{
		"service_id":    "svcA",
		"endpoint_path": "/a",
	})

	if err := eng.Run(250 * time.Millisecond); err != nil {
		t.Fatalf("run: %v", err)
	}

	maxRoot := mustMaxRootLatencyFromCollector(t, collector)
	// Parallel sync fan-out: 10ms local + max(20,20)ms children ~ 30ms, plus small scheduling overhead.
	if maxRoot < 27 || maxRoot > 40 {
		t.Fatalf("expected root latency ~30ms; got max root_request_latency_ms=%v", maxRoot)
	}
	collector.Stop()
}

func TestResearchBenchmark_AsyncDoesNotInflateRootLatency(t *testing.T) {
	ds := []config.DownstreamCall{{
		To:   "svcB:/b",
		Mode: "async",
		CallLatencyMs: config.LatencySpec{
			Mean:  0,
			Sigma: 0,
		},
	}}

	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 8}},
		Services: []config.Service{
			{
				ID:       "svcA",
				Replicas: 1,
				Model:    "cpu",
				Endpoints: []config.Endpoint{{
					Path:            "/a",
					MeanCPUMs:       10,
					CPUSigmaMs:      0,
					NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
					DefaultMemoryMB: 32,
					Downstream:      ds,
				}},
			},
			{
				ID:       "svcB",
				Replicas: 1,
				Model:    "cpu",
				Endpoints: []config.Endpoint{{
					Path:            "/b",
					MeanCPUMs:       20,
					CPUSigmaMs:      0,
					NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
					DefaultMemoryMB: 32,
				}},
			},
		},
	}

	eng := engine.NewEngine("research-async-root")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), researchBenchSeed)
	if err != nil {
		t.Fatalf("scenario state: %v", err)
	}
	RegisterHandlers(eng, state)
	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "svcA", map[string]interface{}{
		"service_id":    "svcA",
		"endpoint_path": "/a",
	})

	if err := eng.Run(220 * time.Millisecond); err != nil {
		t.Fatalf("run: %v", err)
	}

	maxRoot := mustMaxRootLatencyFromCollector(t, collector)
	// Async downstream should not inflate ingress root latency: ~10ms local only.
	if maxRoot > 15 {
		t.Fatalf("expected root latency near svcA local (~10ms); got max root_request_latency_ms=%v", maxRoot)
	}
	collector.Stop()
}

func TestResearchBenchmark_RetryTimeoutBackoffIncreasesLatencyAndEmitsRetries(t *testing.T) {
	baseScenario := func(enableRetries bool) *config.Scenario {
		retries := (*config.Policies)(nil)
		if enableRetries {
			retries = &config.Policies{
				Retries: &config.RetryPolicy{
					Enabled:    true,
					MaxRetries: 1,
					Backoff:    "constant",
					BaseMs:     20,
				},
			}
		}
		return &config.Scenario{
			Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16, Zone: "zone-a"}},
			Services: []config.Service{
				{
					ID:       "caller",
					Replicas: 1,
					Model:    "cpu",
					Endpoints: []config.Endpoint{{
						Path:            "/call",
						MeanCPUMs:       1,
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 32,
						Downstream: []config.DownstreamCall{{
							To:            "callee:/work",
							Mode:          "sync",
							CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
							TimeoutMs:     20,
							FailureRate:   0,
							Probability:   1,
						}},
					}},
				},
				{
					ID:       "callee",
					Replicas: 1,
					Model:    "cpu",
					Endpoints: []config.Endpoint{{
						Path:            "/work",
						MeanCPUMs:       120, // ensure timeout triggers deterministically
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 32,
					}},
				},
			},
			Policies: retries,
			Workload: []config.WorkloadPattern{{
				From: "c",
				To:   "caller:/call",
				Arrival: config.ArrivalSpec{
					Type:    "constant",
					RateRPS: 8,
				},
			}},
		}
	}

	dur := 420 * time.Millisecond
	rmNoRetry := mustRunScenarioForMetrics(t, baseScenario(false), dur, researchBenchSeed)
	rmWithRetry := mustRunScenarioForMetrics(t, baseScenario(true), dur, researchBenchSeed)

	if rmWithRetry.RetryAttempts <= 0 {
		t.Fatalf("expected retry attempts > 0 when retries enabled; got %v", rmWithRetry.RetryAttempts)
	}
	if rmWithRetry.TimeoutErrors <= 0 {
		t.Fatalf("expected timeout errors > 0 when timeouts enabled; got %v", rmWithRetry.TimeoutErrors)
	}
	if rmWithRetry.LatencyP95 <= rmNoRetry.LatencyP95 {
		t.Fatalf("expected p95 latency to increase with retries; no_retry=%v with_retry=%v", rmNoRetry.LatencyP95, rmWithRetry.LatencyP95)
	}
}

func TestResearchBenchmark_QueueAckTimeoutEmitsRedeliveryAndDLQ(t *testing.T) {
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{
			{
				ID:       "api",
				Replicas: 1,
				Model:    "cpu",
				Endpoints: []config.Endpoint{{
					Path:            "/pub",
					MeanCPUMs:       1,
					CPUSigmaMs:      0,
					NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
					DefaultMemoryMB: 32,
					Downstream: []config.DownstreamCall{{
						To:            "mq:/orders",
						Kind:          "queue",
						Mode:          "sync",
						CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
					}},
				}},
			},
			{
				ID:       "mq",
				Kind:     "queue",
				Model:    "cpu",
				Replicas: 1,
				Behavior: &config.ServiceBehavior{Queue: &config.QueueBehavior{
					ConsumerTarget:      "worker:/handle",
					DeliveryLatencyMs:   config.LatencySpec{Mean: 0, Sigma: 0},
					ConsumerConcurrency: 1,
					AckTimeoutMs:        5,
					MaxRedeliveries:     0, // deterministic DLQ on first timeout
				}},
				Endpoints: []config.Endpoint{{
					Path:            "/orders",
					MeanCPUMs:       1,
					CPUSigmaMs:      0,
					NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
					DefaultMemoryMB: 32,
				}},
			},
			{
				ID:       "worker",
				Replicas: 1,
				Model:    "cpu",
				Endpoints: []config.Endpoint{{
					Path:            "/handle",
					MeanCPUMs:       120, // ensure ack timeout fires before completion
					CPUSigmaMs:      0,
					NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
					DefaultMemoryMB: 32,
				}},
			},
		},
		Workload: []config.WorkloadPattern{{
			From: "c",
			To:   "api:/pub",
			Arrival: config.ArrivalSpec{
				Type:    "constant",
				RateRPS: 3,
			},
		}},
	}

	dur := 520 * time.Millisecond
	rm := mustRunScenarioForMetrics(t, scenario, dur, researchBenchSeed)

	// Exact counts can vary with scheduling; benchmark focuses on reliability of timeout/requeue pressure signals.
	if rm.QueueRedeliveryCountTotal+rm.QueueDlqCountTotal <= 0 {
		t.Fatalf("expected queue redelivery or DLQ activity > 0, got redelivery=%v dlq=%v", rm.QueueRedeliveryCountTotal, rm.QueueDlqCountTotal)
	}
	if rm.QueueDepthSum <= 0 {
		t.Fatalf("expected queue depth sum > 0 under backlog, got %v", rm.QueueDepthSum)
	}
}

func TestResearchBenchmark_LocalityVsCrossZonePenalties(t *testing.T) {
	net := &config.NetworkConfig{
		CrossZoneLatencyMs: map[string]map[string]config.LatencySpec{
			"zone-a": {"zone-b": {Mean: 80, Sigma: 0}},
		},
	}
	base := func(apiZone string) *config.Scenario {
		return &config.Scenario{
			Network: net,
			Hosts: []config.Host{
				{ID: "h-a", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
				{ID: "h-b", Cores: 8, MemoryGB: 16, Zone: "zone-b"},
			},
			Services: []config.Service{
				{
					ID:       "edge",
					Replicas: 1,
					Model:    "cpu",
					Placement: &config.PlacementPolicy{
						RequiredZones: []string{"zone-a"},
					},
					Endpoints: []config.Endpoint{{
						Path:            "/in",
						MeanCPUMs:       1,
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 32,
						Downstream: []config.DownstreamCall{{
							To:            "api:/x",
							Mode:          "sync",
							CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
						}},
					}},
				},
				{
					ID:       "api",
					Replicas: 1,
					Model:    "cpu",
					Placement: &config.PlacementPolicy{
						RequiredZones: []string{apiZone},
					},
					Routing: &config.RoutingPolicy{
						Strategy:         "round_robin",
						LocalityZoneFrom: "client_zone",
					},
					Endpoints: []config.Endpoint{{
						Path:            "/x",
						MeanCPUMs:       2,
						CPUSigmaMs:      0,
						NetLatencyMs:    config.LatencySpec{Mean: 0, Sigma: 0},
						DefaultMemoryMB: 32,
					}},
				},
			},
			Workload: []config.WorkloadPattern{{
				From:     "c",
				To:       "edge:/in",
				Metadata: map[string]string{"client_zone": "zone-a"},
				Arrival:  config.ArrivalSpec{Type: "constant", RateRPS: 18},
			}},
		}
	}

	dur := 440 * time.Millisecond
	rmSame := mustRunScenarioForMetrics(t, base("zone-a"), dur, researchBenchSeed)
	rmCross := mustRunScenarioForMetrics(t, base("zone-b"), dur, researchBenchSeed)

	if rmSame.CrossZoneRequestFraction > 0.1 {
		t.Fatalf("expected near-zero cross-zone fraction for same-zone; got %v", rmSame.CrossZoneRequestFraction)
	}
	if rmCross.CrossZoneRequestFraction < 0.9 {
		t.Fatalf("expected near-1 cross-zone fraction for forced cross-zone; got %v", rmCross.CrossZoneRequestFraction)
	}
	if rmCross.TopologyLatencyPenaltyMsTotal <= rmSame.TopologyLatencyPenaltyMsTotal {
		t.Fatalf("expected topology latency penalties to be higher for cross-zone; same=%v cross=%v",
			rmSame.TopologyLatencyPenaltyMsTotal, rmCross.TopologyLatencyPenaltyMsTotal)
	}
}

func TestResearchBenchmark_TopologyAwareOnlineScaleDownBlockDeterministic(t *testing.T) {
	scenario := &config.Scenario{
		Services: []config.Service{{ID: "svc1"}},
	}
	opt := &simulationv1.OptimizationConfig{
		MinLocalityHitRate:              0.5,
		MaxCrossZoneRequestFraction:     0.4,
		MaxTopologyLatencyPenaltyMeanMs: 10,
	}
	runMetrics := &models.RunMetrics{
		LocalityHitRate:              0.95,
		CrossZoneRequestFraction:     0.75,
		TopologyLatencyPenaltyMsMean: 2,
	}
	blocked, reason := onlineTopologyGuard(runMetrics, scenario, "", nil, opt, -1)
	if !blocked {
		t.Fatalf("expected topology guard to block scale-down under high cross-zone fraction")
	}
	if reason != "cross_zone_fraction_above_max" {
		t.Fatalf("expected cross_zone_fraction_above_max, got %q", reason)
	}
}
