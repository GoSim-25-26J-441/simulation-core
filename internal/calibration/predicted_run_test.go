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
