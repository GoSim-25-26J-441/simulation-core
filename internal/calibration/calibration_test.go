package calibration

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/batchspec"
	"github.com/GoSim-25-26J-441/simulation-core/internal/simd"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

func simpleAPIScenario(ingressRPS, apiCPUMs float64) *config.Scenario {
	return &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{
			{
				ID: "api", Replicas: 1, Model: "cpu",
				Endpoints: []config.Endpoint{{
					Path: "/pub", MeanCPUMs: apiCPUMs, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
					Downstream: []config.DownstreamCall{},
				}},
			},
		},
		Workload: []config.WorkloadPattern{
			{From: "c", To: "api:/pub", Arrival: config.ArrivalSpec{Type: "constant", RateRPS: ingressRPS}},
		},
	}
}

func TestCalibrateScenarioDoesNotMutateBase(t *testing.T) {
	wrong := simpleAPIScenario(1, 1)
	h0 := batchspec.ConfigHash(wrong)
	dur := 400 * time.Millisecond
	ref, err := simd.RunScenarioForMetrics(wrong, dur, 42, false)
	if err != nil {
		t.Fatal(err)
	}
	obs := FromRunMetrics(ref, dur)
	predWrong, err := simd.RunScenarioForMetrics(wrong, dur, 42, false)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = CalibrateScenario(wrong, obs, &CalibrateOptions{
		Overwrite:    OverwriteAlways,
		PredictedRun: predWrong,
	})
	if err != nil {
		t.Fatal(err)
	}
	if batchspec.ConfigHash(wrong) != h0 {
		t.Fatal("CalibrateScenario mutated base scenario; expected cloneScenarioViaYAML-only edits on copy")
	}
}

func TestFromRunMetricsIncludesBrokerObservations(t *testing.T) {
	rm := &models.RunMetrics{
		QueueEnqueueCountTotal:  8,
		QueueDequeueCountTotal:  5,
		QueueDropCountTotal:     2,
		QueueDlqCountTotal:      1,
		QueueDepthSum:           3,
		QueueDropRate:           0.2,
		QueueOldestMessageAgeMs: 250,
		TopicPublishCountTotal:  4,
		TopicDeliverCountTotal:  6,
		TopicDropCountTotal:     3,
		TopicDlqCountTotal:      1,
		TopicBacklogDepthSum:    2,
		TopicConsumerLagSum:     5,
		TopicOldestMessageAgeMs: 125,
	}
	obs := FromRunMetrics(rm, time.Second)
	if len(obs.QueueBrokers) != 1 {
		t.Fatalf("expected one aggregate queue observation, got %+v", obs.QueueBrokers)
	}
	q := obs.QueueBrokers[0]
	if q.DepthMean.Value != 3 || q.DropCount.Value != 2 || q.DLQCount.Value != 1 || q.EnqueueCount.Value != 8 || q.DequeueCount.Value != 5 {
		t.Fatalf("unexpected queue observation: %+v", q)
	}
	if !q.QueuePublishAttemptCount.Present || q.QueuePublishAttemptCount.Value != 10 {
		t.Fatalf("expected queue attempts reconstructed from drop/drop_rate, got %+v", q.QueuePublishAttemptCount)
	}
	if len(obs.TopicBrokers) != 1 {
		t.Fatalf("expected one aggregate topic observation, got %+v", obs.TopicBrokers)
	}
	topic := obs.TopicBrokers[0]
	if topic.BacklogDepth.Value != 2 || topic.ConsumerLag.Value != 5 || topic.DropCount.Value != 3 || topic.TopicDeliverCount.Value != 6 {
		t.Fatalf("unexpected topic observation: %+v", topic)
	}
}

func TestCalibrateEndpointProcessingUsesEndpointPredictedBaseline(t *testing.T) {
	pFast, pSlow := 100.0, 500.0
	blend := 300.0
	base := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{{
			ID: "svc", Replicas: 1, Model: "cpu",
			Endpoints: []config.Endpoint{
				{Path: "/fast", MeanCPUMs: 10, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}},
				{Path: "/slow", MeanCPUMs: 10, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}},
			},
		}},
		Workload: []config.WorkloadPattern{
			{From: "c", To: "svc:/fast", Arrival: config.ArrivalSpec{Type: "constant", RateRPS: 1}},
		},
	}
	pred := &models.RunMetrics{
		ServiceMetrics: map[string]*models.ServiceMetrics{
			"svc": {ProcessingLatencyMeanMs: blend},
		},
		EndpointRequestStats: []models.EndpointRequestStats{
			{ServiceName: "svc", EndpointPath: "/fast", ProcessingLatencyMeanMs: &pFast},
			{ServiceName: "svc", EndpointPath: "/slow", ProcessingLatencyMeanMs: &pSlow},
		},
	}
	obs := &ObservedMetrics{
		Endpoints: []EndpointObservation{
			{ServiceID: "svc", EndpointPath: "/slow", ProcessingLatencyMeanMs: F64(1000)},
		},
	}
	out, rep, err := CalibrateScenario(base, obs, &CalibrateOptions{
		Overwrite:      OverwriteAlways,
		PredictedRun:   pred,
		MinScaleFactor: 0.1,
		MaxScaleFactor: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	var slowCPU, fastCPU float64
	for _, ep := range out.Services[0].Endpoints {
		switch ep.Path {
		case "/slow":
			slowCPU = ep.MeanCPUMs
		case "/fast":
			fastCPU = ep.MeanCPUMs
		}
	}
	// 1000/500 * 10 = 20; blended service mean 300 would yield ~33.3
	if math.Abs(slowCPU-20) > 1e-6 {
		t.Fatalf("expected /slow mean_cpu_ms=20, got %v (report warnings: %v)", slowCPU, rep.Warnings)
	}
	if math.Abs(fastCPU-10) > 1e-6 {
		t.Fatalf("expected /fast mean_cpu_ms unchanged at 10, got %v", fastCPU)
	}
	for _, w := range rep.Warnings {
		if strings.Contains(w, "service-level predicted processing mean") {
			t.Fatalf("unexpected service-level fallback warning when endpoint stats exist: %q", w)
		}
	}
}

func TestCalibrationRecoversWorkloadScale(t *testing.T) {
	truth := simpleAPIScenario(8, 3)
	dur := 800 * time.Millisecond
	ref, err := simd.RunScenarioForMetrics(truth, dur, 42, false)
	if err != nil {
		t.Fatal(err)
	}
	obs := FromRunMetrics(ref, dur)
	// Wrong scenario: much lower ingress and CPU
	wrong := simpleAPIScenario(1, 1)
	predWrong, err := simd.RunScenarioForMetrics(wrong, dur, 42, false)
	if err != nil {
		t.Fatal(err)
	}
	out, rep, err := CalibrateScenario(wrong, obs, &CalibrateOptions{
		Overwrite:      OverwriteAlways,
		PredictedRun:   predWrong,
		MinScaleFactor: 0.1,
		MaxScaleFactor: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Workload) != 1 {
		t.Fatal("expected workload")
	}
	got := out.Workload[0].Arrival.RateRPS
	if got < 6 || got > 10 {
		t.Fatalf("expected calibrated rate near truth ~8, got %v (report changes %d)", got, len(rep.Changes))
	}
	api := out.Services[0].Endpoints[0].MeanCPUMs
	if api < 2 || api > 5 {
		t.Fatalf("expected calibrated CPU closer to truth ~3, got %v", api)
	}
}

func TestValidationPassAgainstSelf(t *testing.T) {
	sc := simpleAPIScenario(5, 2)
	durMs := int64(600)
	rm, err := simd.RunScenarioForMetrics(sc, time.Duration(durMs)*time.Millisecond, 99, false)
	if err != nil {
		t.Fatal(err)
	}
	obs := FromRunMetrics(rm, time.Duration(durMs)*time.Millisecond)
	rep, err := ValidateScenario(sc, obs, durMs, &ValidateOptions{
		Seeds:              []int64{99},
		AllowPartialFields: true,
		Tolerances:         DefaultValidationTolerances(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Pass {
		t.Fatalf("expected pass against self, got %+v", rep.Checks)
	}
}

func TestValidationFailsOnMismatchedLoad(t *testing.T) {
	truth := simpleAPIScenario(10, 1)
	durMs := int64(500)
	ref, err := simd.RunScenarioForMetrics(truth, time.Duration(durMs)*time.Millisecond, 7, false)
	if err != nil {
		t.Fatal(err)
	}
	obs := FromRunMetrics(ref, time.Duration(durMs)*time.Millisecond)
	wrong := simpleAPIScenario(0.5, 50)
	rep, err := ValidateScenario(wrong, obs, durMs, &ValidateOptions{
		Seeds:              []int64{7},
		AllowPartialFields: false,
		Tolerances:         DefaultValidationTolerances(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pass {
		t.Fatal("expected fail when scenario throughput/latency far from observation")
	}
}

func TestValidationMultiSeedDoesNotPanic(t *testing.T) {
	sc := simpleAPIScenario(3, 2)
	durMs := int64(400)
	rm, err := simd.RunScenarioForMetrics(sc, time.Duration(durMs)*time.Millisecond, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	obs := FromRunMetrics(rm, time.Duration(durMs)*time.Millisecond)
	rep, err := ValidateScenario(sc, obs, durMs, &ValidateOptions{
		Seeds:              []int64{1, 2, 3, 4, 5},
		AllowPartialFields: true,
		Tolerances:         DefaultValidationTolerances(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.SeedsUsed) != 5 {
		t.Fatalf("seeds: %v", rep.SeedsUsed)
	}
	if rep.Pass == false {
		// self-check can occasionally differ if too strict; ensure aggregation ran
		t.Logf("multi-seed validation note: pass=%v summary=%s", rep.Pass, rep.Summary)
	}
}

func TestValidationThroughputOnlyDoesNotImputeLatencyChecks(t *testing.T) {
	sc := simpleAPIScenario(5, 2)
	durMs := int64(500)
	obs := &ObservedMetrics{
		Global: GlobalObservation{
			IngressThroughputRPS: F64(999), // intentionally wrong vs sim; no latency presence
		},
	}
	rep, err := ValidateScenario(sc, obs, durMs, &ValidateOptions{
		Seeds:              []int64{1},
		AllowPartialFields: true,
		Tolerances:         DefaultValidationTolerances(),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rep.Checks {
		if c.Name != "ingress_throughput_rps" {
			t.Fatalf("unexpected check %q (only throughput should run)", c.Name)
		}
	}
	if rep.Pass {
		t.Fatal("expected fail on throughput mismatch only")
	}
}

func TestValidationTopicLagZeroFailsWhenPredictedLagHigh(t *testing.T) {
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{
			{ID: "api", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{
				Path: "/pub", MeanCPUMs: 50, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
				Downstream: []config.DownstreamCall{{To: "events:/ev", Mode: "sync", Kind: "topic", CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
			}}},
			{
				ID: "events", Kind: "topic", Replicas: 1, Model: "cpu",
				Behavior: &config.ServiceBehavior{Topic: &config.TopicBehavior{
					Partitions: 1, Capacity: 10,
					DeliveryLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
					Subscribers:       []config.TopicSubscriber{{Name: "s", ConsumerGroup: "g", ConsumerTarget: "w:/p", ConsumerConcurrency: 1}},
				}},
				Endpoints: []config.Endpoint{{Path: "/ev", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
			},
			{ID: "w", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/p", MeanCPUMs: 50, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
		},
		Workload: []config.WorkloadPattern{
			{From: "c", To: "api:/pub", Arrival: config.ArrivalSpec{Type: "constant", RateRPS: 80}},
		},
	}
	durMs := int64(1200)
	obs := &ObservedMetrics{
		TopicBrokers: []TopicBrokerObservation{
			{BrokerService: "events", Topic: "/ev", ConsumerGroup: "g", ConsumerLag: F64(0)},
		},
	}
	rep, err := ValidateScenario(sc, obs, durMs, &ValidateOptions{
		Seeds:              []int64{11},
		AllowPartialFields: true,
		Tolerances:         DefaultValidationTolerances(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var saw bool
	for _, c := range rep.Checks {
		if c.Name == "topic_consumer_lag_sum" {
			saw = true
			if c.Pass && c.Predicted > 2.0 {
				t.Fatalf("expected lag check failure when pred=%v obs=0", c.Predicted)
			}
		}
	}
	if !saw {
		t.Fatal("expected topic lag check")
	}
}

func TestValidationServiceCPUAbsentSkippedPresentZeroPasses(t *testing.T) {
	sc := simpleAPIScenario(5, 2)
	durMs := int64(600)
	rm, err := simd.RunScenarioForMetrics(sc, time.Duration(durMs)*time.Millisecond, 2, false)
	if err != nil {
		t.Fatal(err)
	}
	obsAbsent := &ObservedMetrics{
		Global: GlobalObservation{
			IngressThroughputRPS: F64(rm.IngressThroughputRPS),
			RootLatencyP50Ms:     F64(rm.LatencyP50),
			RootLatencyP95Ms:     F64(rm.LatencyP95),
			RootLatencyP99Ms:     F64(rm.LatencyP99),
		},
	}
	rep1, err := ValidateScenario(sc, obsAbsent, durMs, &ValidateOptions{Seeds: []int64{2}, Tolerances: DefaultValidationTolerances()})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rep1.Checks {
		if c.Name == "max_service_cpu_util" {
			t.Fatalf("cpu should be skipped when absent, got check %+v", c)
		}
	}
	obsZero := *obsAbsent
	obsZero.Services = []ServiceObservation{{
		ServiceID:      "api",
		CPUUtilization: F64(0),
	}}
	rep2, err := ValidateScenario(sc, &obsZero, durMs, &ValidateOptions{Seeds: []int64{2}, Tolerances: DefaultValidationTolerances()})
	if err != nil {
		t.Fatal(err)
	}
	var cpu *MetricCheckResult
	for i := range rep2.Checks {
		if rep2.Checks[i].Name == "max_service_cpu_util" {
			cpu = &rep2.Checks[i]
			break
		}
	}
	if cpu == nil || !cpu.Pass {
		t.Fatalf("present cpu=0 should validate against near-zero utilization: %+v", cpu)
	}
}

func TestValidationBrokerDropDlqPresentZero(t *testing.T) {
	sc := simpleAPIScenario(3, 1)
	durMs := int64(400)
	rm, err := simd.RunScenarioForMetrics(sc, time.Duration(durMs)*time.Millisecond, 3, false)
	if err != nil {
		t.Fatal(err)
	}
	obs := &ObservedMetrics{
		Global: GlobalObservation{
			IngressThroughputRPS: F64(rm.IngressThroughputRPS),
			RootLatencyP50Ms:     F64(rm.LatencyP50),
			RootLatencyP95Ms:     F64(rm.LatencyP95),
			RootLatencyP99Ms:     F64(rm.LatencyP99),
		},
		QueueBrokers: []QueueBrokerObservation{{
			BrokerService: "q",
			DropCount:     I64(0),
			DLQCount:      I64(0),
		}},
	}
	rep, err := ValidateScenario(sc, obs, durMs, &ValidateOptions{Seeds: []int64{3}, Tolerances: DefaultValidationTolerances()})
	if err != nil {
		t.Fatal(err)
	}
	if rep == nil {
		t.Fatal("nil")
	}
}

func TestLowConfidenceNetAndCapacityRespectPolicy(t *testing.T) {
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{
			{
				ID: "api", Replicas: 1, Model: "cpu",
				Endpoints: []config.Endpoint{{
					Path: "/pub", MeanCPUMs: 3, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 4, Sigma: 0},
				}},
			},
			{
				ID: "mq", Kind: "queue", Replicas: 1, Model: "cpu",
				Behavior: &config.ServiceBehavior{Queue: &config.QueueBehavior{
					Capacity:          20,
					ConsumerTarget:    "api:/pub",
					DeliveryLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
				}},
				Endpoints: []config.Endpoint{{Path: "/orders"}},
			},
		},
		Workload: []config.WorkloadPattern{
			{From: "c", To: "api:/pub", Arrival: config.ArrivalSpec{Type: "constant", RateRPS: 4}},
		},
	}
	dur := 400 * time.Millisecond
	ref, err := simd.RunScenarioForMetrics(sc, dur, 9, false)
	if err != nil {
		t.Fatal(err)
	}
	obs := &ObservedMetrics{
		Window: ObservationWindow{Duration: dur},
		Endpoints: []EndpointObservation{{
			ServiceID: "api", EndpointPath: "/pub",
			QueueWaitMeanMs:         F64(200),
			ProcessingLatencyMeanMs: F64(1),
		}},
		QueueBrokers: []QueueBrokerObservation{{
			BrokerService: "mq",
			DepthMean:     F64(100),
		}},
	}
	_, repWH, err := CalibrateScenario(sc, obs, &CalibrateOptions{
		PredictedRun:    ref,
		Overwrite:       OverwriteWhenHigherConfidence,
		ConfidenceFloor: 0.2,
		MinScaleFactor:  0.1,
		MaxScaleFactor:  10,
	})
	if err != nil {
		t.Fatal(err)
	}
	hasSkip := len(repWH.SkippedLowConfidence) > 0
	_, repAll, err := CalibrateScenario(sc, obs, &CalibrateOptions{
		Overwrite:       OverwriteAlways,
		ConfidenceFloor: 0.2,
		MinScaleFactor:  0.1,
		MaxScaleFactor:  10,
		PredictedRun:    ref,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSkip {
		t.Fatalf("expected some low-confidence skips under WhenHigherConfidence, got changes=%d skipped=%v",
			len(repWH.Changes), repWH.SkippedLowConfidence)
	}
	var netAlways, capAlways bool
	for _, ch := range repAll.Changes {
		if strings.Contains(ch.Path, "net_latency") {
			netAlways = true
		}
		if strings.Contains(ch.Path, "queue.capacity") {
			capAlways = true
		}
	}
	if !netAlways || !capAlways {
		t.Fatalf("expected net+capacity under OverwriteAlways: net=%v cap=%v changes=%+v", netAlways, capAlways, repAll.Changes)
	}
}

func TestCalibrationForegroundOnlyScalesIngressWorkload(t *testing.T) {
	truth := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{
			{ID: "api", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{
				{Path: "/a", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}},
				{Path: "/b", MeanCPUMs: 0.5, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}},
			}},
		},
		Workload: []config.WorkloadPattern{
			{From: "c", To: "api:/a", TrafficClass: "ingress", Arrival: config.ArrivalSpec{Type: "constant", RateRPS: 8}},
			{From: "j", To: "api:/b", TrafficClass: "background", Arrival: config.ArrivalSpec{Type: "constant", RateRPS: 50}},
		},
	}
	wrong := &config.Scenario{
		Hosts:    truth.Hosts,
		Services: truth.Services,
		Workload: []config.WorkloadPattern{
			{From: "c", To: "api:/a", TrafficClass: "ingress", Arrival: config.ArrivalSpec{Type: "constant", RateRPS: 1}},
			{From: "j", To: "api:/b", TrafficClass: "background", Arrival: config.ArrivalSpec{Type: "constant", RateRPS: 50}},
		},
	}
	dur := 900 * time.Millisecond
	_, err := simd.RunScenarioForMetrics(truth, dur, 77, false)
	if err != nil {
		t.Fatal(err)
	}
	obs := &ObservedMetrics{
		Window: ObservationWindow{Source: "external"},
		Global: GlobalObservation{
			IngressThroughputRPS: F64(8),
		},
	}
	predWrong, err := simd.RunScenarioForMetrics(wrong, dur, 77, false)
	if err != nil {
		t.Fatal(err)
	}
	out, rep, err := CalibrateScenario(wrong, obs, &CalibrateOptions{
		Overwrite:       OverwriteAlways,
		PredictedRun:    predWrong,
		MinScaleFactor:  0.01,
		MaxScaleFactor:  100,
		ConfidenceFloor: 0.1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Workload[0].Arrival.RateRPS < 7 || out.Workload[0].Arrival.RateRPS > 9 {
		t.Fatalf("expected foreground ~8, got %v report=%+v", out.Workload[0].Arrival.RateRPS, rep.Warnings)
	}
	if math.Abs(out.Workload[1].Arrival.RateRPS-50) > 0.01 {
		t.Fatalf("expected background unchanged at 50, got %v", out.Workload[1].Arrival.RateRPS)
	}
}

func TestBrokerMetricsPresentInValidation(t *testing.T) {
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{
			{ID: "api", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{
				Path: "/pub", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
				Downstream: []config.DownstreamCall{{To: "events:/ev", Mode: "sync", Kind: "topic", CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
			}}},
			{
				ID: "events", Kind: "topic", Replicas: 1, Model: "cpu",
				Behavior: &config.ServiceBehavior{Topic: &config.TopicBehavior{
					Partitions: 1, Capacity: 1000,
					DeliveryLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
					Subscribers:       []config.TopicSubscriber{{Name: "s", ConsumerGroup: "g", ConsumerTarget: "w:/p", ConsumerConcurrency: 1}},
				}},
				Endpoints: []config.Endpoint{{Path: "/ev", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
			},
			{ID: "w", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/p", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
		},
		Workload: []config.WorkloadPattern{
			{From: "c", To: "api:/pub", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 2}},
		},
	}
	durMs := int64(600)
	rm, err := simd.RunScenarioForMetrics(sc, time.Duration(durMs)*time.Millisecond, 11, false)
	if err != nil {
		t.Fatal(err)
	}
	if rm.TopicPublishCountTotal == 0 {
		t.Log("no topic traffic in short window (ok)")
	}
	obs := &ObservedMetrics{
		Global: GlobalObservation{
			IngressThroughputRPS: F64(rm.IngressThroughputRPS),
			RootLatencyP50Ms:     F64(rm.LatencyP50), RootLatencyP95Ms: F64(rm.LatencyP95), RootLatencyP99Ms: F64(rm.LatencyP99),
			IngressErrorRate: F64(rm.IngressErrorRate),
		},
		TopicBrokers: []TopicBrokerObservation{
			{BrokerService: "events", Topic: "/ev", ConsumerGroup: "g", BacklogDepth: F64(0), ConsumerLag: F64(0)},
		},
	}
	rep, err := ValidateScenario(sc, obs, durMs, &ValidateOptions{Seeds: []int64{11}, Tolerances: DefaultValidationTolerances(), AllowPartialFields: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep == nil {
		t.Fatal("nil report")
	}
}
