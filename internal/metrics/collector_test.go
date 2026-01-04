package metrics

import (
	"sort"
	"testing"
	"time"
)

func TestNewCollector(t *testing.T) {
	c := NewCollector()
	if c == nil {
		t.Fatalf("expected non-nil collector")
	}
}

func TestCollectorRecordAndGetTimeSeries(t *testing.T) {
	c := NewCollector()
	c.Start()

	now := time.Now()
	c.Record("test_metric", 10.0, now, nil)
	c.Record("test_metric", 20.0, now.Add(time.Second), nil)
	c.Record("test_metric", 30.0, now.Add(2*time.Second), nil)

	points := c.GetTimeSeries("test_metric", nil)
	if len(points) != 3 {
		t.Fatalf("expected 3 points, got %d", len(points))
	}

	if points[0].Value != 10.0 {
		t.Fatalf("expected first point value 10.0, got %f", points[0].Value)
	}
	if points[1].Value != 20.0 {
		t.Fatalf("expected second point value 20.0, got %f", points[1].Value)
	}
	if points[2].Value != 30.0 {
		t.Fatalf("expected third point value 30.0, got %f", points[2].Value)
	}
}

func TestCollectorRecordWithLabels(t *testing.T) {
	c := NewCollector()
	c.Start()

	labels := map[string]string{
		"service":  "svc1",
		"endpoint": "/test",
	}

	now := time.Now()
	c.Record("latency", 10.0, now, labels)

	points := c.GetTimeSeries("latency", labels)
	if len(points) != 1 {
		t.Fatalf("expected 1 point, got %d", len(points))
	}

	if points[0].Labels["service"] != "svc1" {
		t.Fatalf("expected service label svc1, got %s", points[0].Labels["service"])
	}
}

func TestCollectorGetAggregation(t *testing.T) {
	c := NewCollector()
	c.Start()

	values := []float64{10.0, 20.0, 30.0, 40.0, 50.0}
	now := time.Now()
	for i, v := range values {
		c.Record("test_metric", v, now.Add(time.Duration(i)*time.Second), nil)
	}

	agg := c.GetAggregation("test_metric", nil)
	if agg == nil {
		t.Fatalf("expected non-nil aggregation")
	}

	if agg.Count != 5 {
		t.Fatalf("expected count 5, got %d", agg.Count)
	}
	if agg.Min != 10.0 {
		t.Fatalf("expected min 10.0, got %f", agg.Min)
	}
	if agg.Max != 50.0 {
		t.Fatalf("expected max 50.0, got %f", agg.Max)
	}
	if agg.Mean != 30.0 {
		t.Fatalf("expected mean 30.0, got %f", agg.Mean)
	}
}

func TestCollectorPercentiles(t *testing.T) {
	c := NewCollector()
	c.Start()

	// Create 100 values for better percentile accuracy
	now := time.Now()
	for i := 0; i < 100; i++ {
		c.Record("test_metric", float64(i+1), now.Add(time.Duration(i)*time.Millisecond), nil)
	}

	agg := c.GetAggregation("test_metric", nil)
	if agg == nil {
		t.Fatalf("expected non-nil aggregation")
	}

	// P50 should be around 50.5 (median of 1-100)
	if agg.P50 < 50.0 || agg.P50 > 51.0 {
		t.Fatalf("expected P50 around 50.5, got %f", agg.P50)
	}

	// P95 should be around 95.5
	if agg.P95 < 95.0 || agg.P95 > 96.0 {
		t.Fatalf("expected P95 around 95.5, got %f", agg.P95)
	}

	// P99 should be around 99.5
	if agg.P99 < 99.0 || agg.P99 > 100.0 {
		t.Fatalf("expected P99 around 99.5, got %f", agg.P99)
	}
}

func TestCollectorGetOrComputeAggregation(t *testing.T) {
	c := NewCollector()
	c.Start()

	now := time.Now()
	c.Record("test_metric", 10.0, now, nil)
	c.Record("test_metric", 20.0, now, nil)

	// First call computes
	agg1 := c.GetOrComputeAggregation("test_metric", nil)
	if agg1 == nil {
		t.Fatalf("expected non-nil aggregation")
	}

	// Second call should return cached
	agg2 := c.GetOrComputeAggregation("test_metric", nil)
	if agg2 == nil {
		t.Fatalf("expected non-nil aggregation")
	}

	if agg1.Count != agg2.Count {
		t.Fatalf("expected same count from cache")
	}
}

func TestCollectorComputeAllAggregations(t *testing.T) {
	c := NewCollector()
	c.Start()

	now := time.Now()
	c.Record("metric1", 10.0, now, nil)
	c.Record("metric2", 20.0, now, nil)

	c.ComputeAllAggregations()

	agg1 := c.GetOrComputeAggregation("metric1", nil)
	if agg1 == nil {
		t.Fatalf("expected non-nil aggregation for metric1")
	}

	agg2 := c.GetOrComputeAggregation("metric2", nil)
	if agg2 == nil {
		t.Fatalf("expected non-nil aggregation for metric2")
	}
}

func TestCollectorGetSummary(t *testing.T) {
	c := NewCollector()
	c.Start()

	now := time.Now()
	c.Record("metric1", 10.0, now, nil)
	c.Record("metric1", 20.0, now, nil)
	c.Record("metric2", 30.0, now, nil)

	// Wait a bit to ensure duration is positive
	time.Sleep(10 * time.Millisecond)
	c.Stop()

	summary := c.GetSummary()
	if summary == nil {
		t.Fatalf("expected non-nil summary")
	}

	if len(summary.Metrics) == 0 {
		t.Fatalf("expected metrics in summary")
	}

	if summary.Metrics["metric1"] == nil {
		t.Fatalf("expected metric1 in summary")
	}

	if len(summary.Metrics["metric1"]) != 2 {
		t.Fatalf("expected 2 values for metric1, got %d", len(summary.Metrics["metric1"]))
	}

	if summary.Duration <= 0 {
		t.Fatalf("expected positive duration, got %v", summary.Duration)
	}
}

func TestCollectorGetMetricNames(t *testing.T) {
	c := NewCollector()
	c.Start()

	c.RecordNow("metric1", 10.0, nil)
	c.RecordNow("metric2", 20.0, nil)
	c.RecordNow("metric3", 30.0, nil)

	names := c.GetMetricNames()
	if len(names) != 3 {
		t.Fatalf("expected 3 metric names, got %d", len(names))
	}
}

func TestCollectorGetLabelsForMetric(t *testing.T) {
	c := NewCollector()
	c.Start()

	now := time.Now()
	c.Record("metric1", 10.0, now, map[string]string{"service": "svc1"})
	c.Record("metric1", 20.0, now, map[string]string{"service": "svc2"})
	c.Record("metric1", 30.0, now, nil)

	labelsList := c.GetLabelsForMetric("metric1")
	if len(labelsList) != 3 {
		t.Fatalf("expected 3 label combinations, got %d", len(labelsList))
	}
}

func TestCollectorClear(t *testing.T) {
	c := NewCollector()
	c.Start()

	c.RecordNow("metric1", 10.0, nil)
	c.RecordNow("metric2", 20.0, nil)

	c.Clear()

	names := c.GetMetricNames()
	if len(names) != 0 {
		t.Fatalf("expected 0 metric names after clear, got %d", len(names))
	}
}

func TestCollectorEmptyAggregation(t *testing.T) {
	c := NewCollector()
	c.Start()

	agg := c.GetAggregation("nonexistent", nil)
	if agg != nil {
		t.Fatalf("expected nil aggregation for non-existent metric")
	}
}

func TestPercentileCalculation(t *testing.T) {
	// Test with single value
	values := []float64{10.0}
	sort.Float64s(values)
	p50 := calculatePercentile(values, 0.50)
	if p50 != 10.0 {
		t.Fatalf("expected P50 10.0 for single value, got %f", p50)
	}

	// Test with two values
	values = []float64{10.0, 20.0}
	sort.Float64s(values)
	p50 = calculatePercentile(values, 0.50)
	if p50 != 15.0 {
		t.Fatalf("expected P50 15.0 for [10,20], got %f", p50)
	}

	// Test with multiple values
	values = []float64{10.0, 20.0, 30.0, 40.0, 50.0}
	sort.Float64s(values)
	p50 = calculatePercentile(values, 0.50)
	if p50 != 30.0 {
		t.Fatalf("expected P50 30.0, got %f", p50)
	}
}

func TestHelperFunctions(t *testing.T) {
	c := NewCollector()
	c.Start()

	now := time.Now()

	// Test RecordLatency
	RecordLatency(c, 10.5, now, nil)
	points := c.GetTimeSeries(MetricRequestLatency, nil)
	if len(points) != 1 || points[0].Value != 10.5 {
		t.Fatalf("RecordLatency failed")
	}

	// Test RecordCPUUtilization
	RecordCPUUtilization(c, 0.75, now, nil)
	points = c.GetTimeSeries(MetricCPUUtilization, nil)
	if len(points) != 1 || points[0].Value != 0.75 {
		t.Fatalf("RecordCPUUtilization failed")
	}

	// Test label creation
	labels := CreateServiceLabels("svc1")
	if labels["service"] != "svc1" {
		t.Fatalf("CreateServiceLabels failed")
	}

	labels = CreateEndpointLabels("svc1", "/test")
	if labels["service"] != "svc1" || labels["endpoint"] != "/test" {
		t.Fatalf("CreateEndpointLabels failed")
	}

	labels = CreateInstanceLabels("svc1", "inst-1")
	if labels["service"] != "svc1" || labels["instance"] != "inst-1" {
		t.Fatalf("CreateInstanceLabels failed")
	}

	labels = CreateHostLabels("host-1")
	if labels["host"] != "host-1" {
		t.Fatalf("CreateHostLabels failed")
	}
}

func TestConvertToRunMetrics(t *testing.T) {
	c := NewCollector()
	c.Start()

	now := time.Now()

	// Record some metrics
	svc1Labels := CreateServiceLabels("svc1")
	svc2Labels := CreateServiceLabels("svc2")

	RecordLatency(c, 10.0, now, svc1Labels)
	RecordLatency(c, 20.0, now, svc1Labels)
	RecordLatency(c, 30.0, now, svc2Labels)

	// Record request counts - each request is recorded as 1.0
	// The aggregation will sum these values
	RecordRequestCount(c, 1.0, now, svc1Labels)
	RecordRequestCount(c, 1.0, now, svc1Labels)
	RecordRequestCount(c, 1.0, now, svc2Labels)

	time.Sleep(10 * time.Millisecond)
	c.Stop()

	runMetrics := ConvertToRunMetrics(c, []map[string]string{svc1Labels, svc2Labels})
	if runMetrics == nil {
		t.Fatalf("expected non-nil RunMetrics")
	}

	// Total requests should be sum of all request count values (3.0)
	if runMetrics.TotalRequests != 3 {
		t.Fatalf("expected 3 total requests, got %d", runMetrics.TotalRequests)
	}

	if runMetrics.LatencyMean == 0 {
		t.Fatalf("expected non-zero latency mean")
	}

	if len(runMetrics.ServiceMetrics) != 2 {
		t.Fatalf("expected 2 service metrics, got %d", len(runMetrics.ServiceMetrics))
	}

	// Check service-specific metrics
	svc1Metrics := runMetrics.ServiceMetrics["svc1"]
	if svc1Metrics == nil {
		t.Fatalf("expected svc1 metrics")
	}
	if svc1Metrics.RequestCount != 2 {
		t.Fatalf("expected svc1 to have 2 requests, got %d", svc1Metrics.RequestCount)
	}
}
