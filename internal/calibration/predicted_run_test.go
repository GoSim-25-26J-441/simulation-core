package calibration

import (
	"math"
	"testing"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

func TestMergeRunMetricsEndpointStatsOneSeed(t *testing.T) {
	proc := 5.0
	rm := &models.RunMetrics{
		EndpointRequestStats: []models.EndpointRequestStats{
			{
				ServiceName: "a", EndpointPath: "/x", RequestCount: 100, ErrorCount: 10,
				ProcessingLatencyMeanMs: &proc,
			},
		},
	}
	merged := MergeRunMetricsForCalibrationBaseline([]*models.RunMetrics{rm})
	if merged == nil {
		t.Fatal("nil merged")
	}
	if len(merged.EndpointRequestStats) != 1 {
		t.Fatalf("endpoint stats: %+v", merged.EndpointRequestStats)
	}
	es := merged.EndpointRequestStats[0]
	if es.RequestCount != 100 || es.ErrorCount != 10 {
		t.Fatalf("counts: %+v", es)
	}
	if es.ProcessingLatencyMeanMs == nil || math.Abs(*es.ProcessingLatencyMeanMs-5) > 1e-9 {
		t.Fatalf("proc mean: %+v", es.ProcessingLatencyMeanMs)
	}
}

func TestMergeRunMetricsEndpointStatsMultiSeed(t *testing.T) {
	p1, p2 := 10.0, 30.0
	p95a, p95b := 20.0, 100.0
	rm1 := &models.RunMetrics{
		EndpointRequestStats: []models.EndpointRequestStats{
			{
				ServiceName: "a", EndpointPath: "/x", RequestCount: 100, ErrorCount: 0,
				ProcessingLatencyP95Ms:  &p95a,
				ProcessingLatencyMeanMs: &p1,
			},
		},
	}
	rm2 := &models.RunMetrics{
		EndpointRequestStats: []models.EndpointRequestStats{
			{
				ServiceName: "a", EndpointPath: "/x", RequestCount: 200, ErrorCount: 10,
				ProcessingLatencyP95Ms:  &p95b,
				ProcessingLatencyMeanMs: &p2,
			},
		},
	}
	merged := MergeRunMetricsForCalibrationBaseline([]*models.RunMetrics{rm1, rm2})
	if len(merged.EndpointRequestStats) != 1 {
		t.Fatalf("got %+v", merged.EndpointRequestStats)
	}
	es := merged.EndpointRequestStats[0]
	if es.RequestCount != 150 || es.ErrorCount != 5 {
		t.Fatalf("mean counts: req=%d err=%d", es.RequestCount, es.ErrorCount)
	}
	if es.ProcessingLatencyMeanMs == nil || math.Abs(*es.ProcessingLatencyMeanMs-20) > 1e-9 {
		t.Fatalf("mean proc: %v", es.ProcessingLatencyMeanMs)
	}
	if es.ProcessingLatencyP95Ms == nil || math.Abs(*es.ProcessingLatencyP95Ms-100) > 1e-9 {
		t.Fatalf("max p95: %v", es.ProcessingLatencyP95Ms)
	}
}

func TestMergeRunMetricsForCalibrationBaselineServiceMetrics(t *testing.T) {
	rm1 := &models.RunMetrics{
		IngressThroughputRPS: 100,
		LatencyP50:           10,
		LatencyMean:          12,
		LatencyP95:           20,
		LatencyP99:           30,
		ServiceMetrics: map[string]*models.ServiceMetrics{
			"svc1": {
				ServiceName:             "svc1",
				RequestCount:            100,
				ErrorCount:              2,
				LatencyP50:              10,
				LatencyMean:             11,
				LatencyP95:              20,
				LatencyP99:              30,
				ProcessingLatencyMeanMs: 5,
				ProcessingLatencyP95Ms:  8,
				ProcessingLatencyP99Ms:  9,
				QueueWaitMeanMs:         1,
				QueueWaitP95Ms:          2,
				QueueWaitP99Ms:          3,
				CPUUtilization:          0.4,
				MemoryUtilization:       0.5,
				QueueLength:             4,
				ConcurrentRequests:      5,
				ActiveReplicas:          2,
			},
		},
	}
	rm2 := &models.RunMetrics{
		IngressThroughputRPS: 60,
		LatencyP50:           14,
		LatencyMean:          16,
		LatencyP95:           28,
		LatencyP99:           40,
		ServiceMetrics: map[string]*models.ServiceMetrics{
			"svc1": {
				ServiceName:             "svc1",
				RequestCount:            50,
				ErrorCount:              4,
				LatencyP50:              14,
				LatencyMean:             15,
				LatencyP95:              28,
				LatencyP99:              40,
				ProcessingLatencyMeanMs: 7,
				ProcessingLatencyP95Ms:  10,
				ProcessingLatencyP99Ms:  12,
				QueueWaitMeanMs:         2,
				QueueWaitP95Ms:          5,
				QueueWaitP99Ms:          6,
				CPUUtilization:          0.8,
				MemoryUtilization:       0.7,
				QueueLength:             8,
				ConcurrentRequests:      9,
				ActiveReplicas:          3,
			},
			"nil-entry": nil,
		},
	}

	merged := MergeRunMetricsForCalibrationBaseline([]*models.RunMetrics{rm1, nil, rm2})
	if merged == nil {
		t.Fatal("expected merged metrics")
	}
	if merged.IngressThroughputRPS != 160.0/3.0 {
		t.Fatalf("unexpected mean throughput: %v", merged.IngressThroughputRPS)
	}
	if merged.LatencyP95 != 28 || merged.LatencyP99 != 40 {
		t.Fatalf("expected max tail latencies, got p95=%v p99=%v", merged.LatencyP95, merged.LatencyP99)
	}
	svc := merged.ServiceMetrics["svc1"]
	if svc == nil {
		t.Fatalf("expected merged service metrics")
	}
	if svc.RequestCount != 75 || svc.ErrorCount != 3 {
		t.Fatalf("unexpected averaged counts: req=%d err=%d", svc.RequestCount, svc.ErrorCount)
	}
	if svc.CPUUtilization != 0.8 || svc.MemoryUtilization != 0.7 {
		t.Fatalf("expected max utilization values, got cpu=%v mem=%v", svc.CPUUtilization, svc.MemoryUtilization)
	}
	if svc.ActiveReplicas != 3 || svc.ConcurrentRequests != 9 || svc.QueueLength != 8 {
		t.Fatalf("expected max queue/concurrency/replica values, got %+v", svc)
	}
}

func TestMergeRunMetricsForCalibrationBaselineEmptyAndClone(t *testing.T) {
	if got := MergeRunMetricsForCalibrationBaseline(nil); got != nil {
		t.Fatalf("expected nil for nil runs")
	}

	orig := &models.RunMetrics{
		LatencyP50: 11,
		ServiceMetrics: map[string]*models.ServiceMetrics{
			"svc": {ServiceName: "svc", RequestCount: 9},
		},
		EndpointRequestStats: []models.EndpointRequestStats{{ServiceName: "svc", EndpointPath: "/x", RequestCount: 1}},
	}
	got := MergeRunMetricsForCalibrationBaseline([]*models.RunMetrics{orig})
	if got == nil {
		t.Fatalf("expected cloned metrics")
	}
	got.ServiceMetrics["svc"].RequestCount = 123
	got.EndpointRequestStats[0].RequestCount = 456
	if orig.ServiceMetrics["svc"].RequestCount != 9 {
		t.Fatalf("expected original service metrics to remain unchanged")
	}
	if orig.EndpointRequestStats[0].RequestCount != 1 {
		t.Fatalf("expected original endpoint stats to remain unchanged")
	}
}
