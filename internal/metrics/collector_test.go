package metrics

import (
	"sort"
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
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

func TestConvertToRunMetricsIngressAndInternalCounts(t *testing.T) {
	c := NewCollector()
	c.Start()
	now := time.Now()
	RecordRequestCount(c, 1.0, now, EndpointLabelsWithOrigin("gw", "/a", OriginIngress))
	RecordRequestCount(c, 1.0, now, EndpointLabelsWithOrigin("svc", "/b", OriginDownstream))
	RecordRequestCount(c, 1.0, now, EndpointLabelsWithOrigin("svc", "/b", OriginDownstream))
	c.Stop()
	run := ConvertToRunMetrics(c, []map[string]string{{"service": "gw"}, {"service": "svc"}}, nil)
	if run.TotalRequests != 3 {
		t.Fatalf("total requests: %d", run.TotalRequests)
	}
	if run.IngressRequests != 1 || run.InternalRequests != 2 {
		t.Fatalf("ingress=%d internal=%d", run.IngressRequests, run.InternalRequests)
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

	runMetrics := ConvertToRunMetrics(c, []map[string]string{svc1Labels, svc2Labels}, nil)
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

// TestConvertToRunMetricsWithEndpointLabels verifies that per-service metrics are
// populated when recording uses endpoint-level labels (service+endpoint).
func TestConvertToRunMetricsWithEndpointLabels(t *testing.T) {
	c := NewCollector()
	c.Start()
	now := time.Now()

	// Record with endpoint labels (as handlers do)
	RecordLatency(c, 10.0, now, CreateEndpointLabels("auth", "/auth/login"))
	RecordLatency(c, 20.0, now, CreateEndpointLabels("auth", "/auth/login"))
	RecordLatency(c, 5.0, now, CreateEndpointLabels("auth", "/auth/verify"))
	RecordRequestCount(c, 1.0, now, CreateEndpointLabels("auth", "/auth/login"))
	RecordRequestCount(c, 1.0, now, CreateEndpointLabels("auth", "/auth/login"))
	RecordRequestCount(c, 1.0, now, CreateEndpointLabels("auth", "/auth/verify"))

	RecordLatency(c, 30.0, now, CreateEndpointLabels("user", "/user/get"))
	RecordRequestCount(c, 1.0, now, CreateEndpointLabels("user", "/user/get"))

	c.Stop()

	// Convert with service-only labels (as executor does)
	serviceLabels := []map[string]string{
		{"service": "auth"},
		{"service": "user"},
	}
	runMetrics := ConvertToRunMetrics(c, serviceLabels, nil)
	if runMetrics == nil {
		t.Fatalf("expected non-nil RunMetrics")
	}
	if runMetrics.TotalRequests != 4 {
		t.Fatalf("expected 4 total requests, got %d", runMetrics.TotalRequests)
	}
	if len(runMetrics.ServiceMetrics) != 2 {
		t.Fatalf("expected 2 service metrics, got %d", len(runMetrics.ServiceMetrics))
	}
	authMetrics := runMetrics.ServiceMetrics["auth"]
	if authMetrics == nil {
		t.Fatalf("expected auth metrics")
	}
	if authMetrics.RequestCount != 3 {
		t.Fatalf("expected auth to have 3 requests, got %d", authMetrics.RequestCount)
	}
	if authMetrics.LatencyMean == 0 {
		t.Fatalf("expected auth to have non-zero latency mean")
	}
	userMetrics := runMetrics.ServiceMetrics["user"]
	if userMetrics == nil {
		t.Fatalf("expected user metrics")
	}
	if userMetrics.RequestCount != 1 {
		t.Fatalf("expected user to have 1 request, got %d", userMetrics.RequestCount)
	}
	if userMetrics.LatencyMean != 30.0 {
		t.Fatalf("expected user latency mean 30, got %f", userMetrics.LatencyMean)
	}
}

// TestConvertToRunMetricsConcurrentRequests verifies that per-service ConcurrentRequests
// is the sum of the latest value per instance (gauge-style aggregation).
func TestConvertToRunMetricsConcurrentRequests(t *testing.T) {
	c := NewCollector()
	c.Start()
	now := time.Now()

	// Record concurrent_requests per instance (as handlers do)
	RecordConcurrentRequests(c, 2.0, now, CreateInstanceLabels("svc1", "inst1"))
	RecordConcurrentRequests(c, 1.0, now, CreateInstanceLabels("svc1", "inst2"))
	RecordConcurrentRequests(c, 3.0, now, CreateInstanceLabels("svc2", "inst1"))

	// Later gauge updates: only latest per instance should be summed
	later := now.Add(time.Second)
	RecordConcurrentRequests(c, 1.0, later, CreateInstanceLabels("svc1", "inst1"))
	RecordConcurrentRequests(c, 2.0, later, CreateInstanceLabels("svc1", "inst2"))

	c.Stop()

	serviceLabels := []map[string]string{
		{"service": "svc1"},
		{"service": "svc2"},
	}
	runMetrics := ConvertToRunMetrics(c, serviceLabels, nil)
	if runMetrics == nil {
		t.Fatalf("expected non-nil RunMetrics")
	}
	if len(runMetrics.ServiceMetrics) != 2 {
		t.Fatalf("expected 2 service metrics, got %d", len(runMetrics.ServiceMetrics))
	}
	// svc1: latest per instance = 1 (inst1) + 2 (inst2) = 3
	svc1 := runMetrics.ServiceMetrics["svc1"]
	if svc1 == nil {
		t.Fatalf("expected svc1 metrics")
	}
	if svc1.ConcurrentRequests != 3 {
		t.Fatalf("expected svc1 ConcurrentRequests 3 (sum of latest per instance), got %d", svc1.ConcurrentRequests)
	}
	// svc2: single instance latest = 3
	svc2 := runMetrics.ServiceMetrics["svc2"]
	if svc2 == nil {
		t.Fatalf("expected svc2 metrics")
	}
	if svc2.ConcurrentRequests != 3 {
		t.Fatalf("expected svc2 ConcurrentRequests 3, got %d", svc2.ConcurrentRequests)
	}
}

func TestConvertToRunMetricsGaugeUtilizationMeanLatestPerInstance(t *testing.T) {
	c := NewCollector()
	c.Start()
	t0 := time.Now()
	t1 := t0.Add(time.Millisecond)
	// One instance: non-zero then zero (e.g. allocation vs completion refresh)
	RecordMemoryUtilization(c, 0.03125, t0, CreateInstanceLabels("svc1", "inst1"))
	RecordMemoryUtilization(c, 0.0, t1, CreateInstanceLabels("svc1", "inst1"))
	RecordCPUUtilization(c, 0.5, t0, CreateInstanceLabels("svc1", "inst1"))
	RecordCPUUtilization(c, 0.0, t1, CreateInstanceLabels("svc1", "inst1"))
	c.Stop()

	runMetrics := ConvertToRunMetrics(c, []map[string]string{{"service": "svc1"}}, nil)
	sm := runMetrics.ServiceMetrics["svc1"]
	if sm == nil {
		t.Fatal("expected svc1 metrics")
	}
	if sm.MemoryUtilization != 0 {
		t.Fatalf("expected MemoryUtilization from latest instance sample (0), got %v", sm.MemoryUtilization)
	}
	if sm.CPUUtilization != 0 {
		t.Fatalf("expected CPUUtilization from latest instance sample (0), got %v", sm.CPUUtilization)
	}
}

func TestConvertToRunMetricsIdleReplicasCountedInInventoryAverage(t *testing.T) {
	c := NewCollector()
	c.Start()
	now := time.Now()
	// Only "busy" has samples; "idle" exists in RM but never handled traffic.
	RecordMemoryUtilization(c, 0.6, now, CreateInstanceLabels("svc1", "busy"))
	RecordCPUUtilization(c, 0.4, now, CreateInstanceLabels("svc1", "busy"))
	c.Stop()

	opts := &RunMetricsOptions{
		InstanceIDsByService: map[string][]string{"svc1": {"busy", "idle"}},
	}
	runMetrics := ConvertToRunMetrics(c, []map[string]string{{"service": "svc1"}}, opts)
	sm := runMetrics.ServiceMetrics["svc1"]
	if sm == nil {
		t.Fatal("expected svc1 metrics")
	}
	if sm.MemoryUtilization != 0.3 {
		t.Fatalf("expected MemoryUtilization (0.6+0)/2 = 0.3, got %v", sm.MemoryUtilization)
	}
	if sm.CPUUtilization != 0.2 {
		t.Fatalf("expected CPUUtilization (0.4+0)/2 = 0.2, got %v", sm.CPUUtilization)
	}
}

func TestConvertToRunMetricsConcurrentRequestsSumsInventoryIncludingIdle(t *testing.T) {
	c := NewCollector()
	c.Start()
	now := time.Now()
	RecordConcurrentRequests(c, 2.0, now, CreateInstanceLabels("svc1", "inst1"))
	// inst2 idle: no concurrent_requests series
	c.Stop()

	opts := &RunMetricsOptions{
		InstanceIDsByService: map[string][]string{"svc1": {"inst1", "inst2"}},
	}
	runMetrics := ConvertToRunMetrics(c, []map[string]string{{"service": "svc1"}}, opts)
	sm := runMetrics.ServiceMetrics["svc1"]
	if sm == nil || sm.ConcurrentRequests != 2 {
		t.Fatalf("expected ConcurrentRequests 2 (2+0), got %+v", sm)
	}
}

func TestAttachHostUtilizationUsesLatestSample(t *testing.T) {
	c := NewCollector()
	c.Start()
	t0 := time.Now()
	RecordCPUUtilization(c, 0.9, t0, CreateHostLabels("h1"))
	RecordCPUUtilization(c, 0.1, t0.Add(time.Millisecond), CreateHostLabels("h1"))
	RecordMemoryUtilization(c, 0.8, t0, CreateHostLabels("h1"))
	RecordMemoryUtilization(c, 0.2, t0.Add(time.Millisecond), CreateHostLabels("h1"))
	c.Stop()

	rm := &models.RunMetrics{}
	AttachHostUtilization(rm, c, []string{"h1"})
	h := rm.HostMetrics["h1"]
	if h == nil {
		t.Fatal("expected host metrics")
	}
	if h.CPUUtilization != 0.1 || h.MemoryUtilization != 0.2 {
		t.Fatalf("expected latest host gauges cpu=0.1 mem=0.2, got cpu=%v mem=%v", h.CPUUtilization, h.MemoryUtilization)
	}
}

func TestAttachHostUtilizationScaledOutHostNoSamplesIsZero(t *testing.T) {
	c := NewCollector()
	c.Start()
	RecordCPUUtilization(c, 0.5, time.Now(), CreateHostLabels("h-known"))
	c.Stop()

	rm := &models.RunMetrics{}
	AttachHostUtilization(rm, c, []string{"h-known", "host-auto-1"})
	if rm.HostMetrics["host-auto-1"].CPUUtilization != 0 || rm.HostMetrics["host-auto-1"].MemoryUtilization != 0 {
		t.Fatalf("expected zero gauges for host with no series, got %+v", rm.HostMetrics["host-auto-1"])
	}
}

func TestCollectorDownsampledSeriesIsBounded(t *testing.T) {
	t.Setenv("SIMD_MAX_METRIC_SERIES_POINTS", "8")
	c := NewCollector()
	c.Start()
	now := time.Now()
	for i := 0; i < 100; i++ {
		c.Record("bounded_metric", float64(i), now.Add(time.Duration(i)*time.Millisecond), map[string]string{"service": "s1"})
	}
	points := c.GetTimeSeries("bounded_metric", map[string]string{"service": "s1"})
	if len(points) > 8 {
		t.Fatalf("expected downsampled points <= 8, got %d", len(points))
	}
	agg := c.GetOrComputeAggregation("bounded_metric", map[string]string{"service": "s1"})
	if agg == nil || agg.Count != 100 {
		t.Fatalf("expected streaming aggregate count=100, got %+v", agg)
	}
}

func TestCollectorGetTimeSeriesReturnsBoundedData(t *testing.T) {
	t.Setenv("SIMD_MAX_METRIC_SERIES_POINTS", "5")
	c := NewCollector()
	c.Start()
	now := time.Now()
	for i := 0; i < 30; i++ {
		c.Record("m", float64(i), now.Add(time.Duration(i)*time.Millisecond), nil)
	}
	if got := len(c.GetTimeSeries("m", nil)); got > 5 {
		t.Fatalf("expected bounded series length <= 5, got %d", got)
	}
}

func TestCollectorStreamingAggregationFields(t *testing.T) {
	c := NewCollector()
	c.Start()
	now := time.Now()
	for _, v := range []float64{2, 4, 6, 8} {
		c.Record("agg", v, now, map[string]string{"service": "svc1"})
	}
	agg := c.GetOrComputeAggregation("agg", map[string]string{"service": "svc1"})
	if agg == nil {
		t.Fatalf("expected aggregation")
	}
	if agg.Count != 4 || agg.Sum != 20 || agg.Min != 2 || agg.Max != 8 || agg.Mean != 5 {
		t.Fatalf("unexpected aggregation values: %+v", agg)
	}
}

func TestCollectorGetSeriesSumExactLabelsUsesAggregate(t *testing.T) {
	t.Setenv("SIMD_MAX_METRIC_SERIES_POINTS", "8")
	c := NewCollector()
	c.Start()
	now := time.Now()
	labelsA := map[string]string{"service": "svc-a"}
	labelsB := map[string]string{"service": "svc-b"}
	for i := 0; i < 100; i++ {
		c.Record("request_count", 1, now.Add(time.Duration(i)*time.Millisecond), labelsA)
	}
	for i := 0; i < 5; i++ {
		c.Record("request_count", 1, now.Add(time.Duration(i)*time.Millisecond), labelsB)
	}

	sumA, ok := c.GetSeriesSum("request_count", labelsA)
	if !ok || sumA != 100 {
		t.Fatalf("expected exact series sum 100 for labelsA, got ok=%v sum=%v", ok, sumA)
	}
	sumB, ok := c.GetSeriesSum("request_count", labelsB)
	if !ok || sumB != 5 {
		t.Fatalf("expected exact series sum 5 for labelsB, got ok=%v sum=%v", ok, sumB)
	}
	if got := len(c.GetTimeSeries("request_count", labelsA)); got > 8 {
		t.Fatalf("expected retained points downsampled <= 8, got %d", got)
	}
}

func TestCollectorPercentilesAreRecomputedLazily(t *testing.T) {
	t.Setenv("SIMD_METRIC_RESERVOIR_SIZE", "128")
	c := NewCollector()
	c.Start()
	now := time.Now()

	for i := 0; i < 20; i++ {
		c.Record("lazy_pct", float64(i+1), now.Add(time.Duration(i)*time.Millisecond), nil)
	}

	c.mu.RLock()
	s := c.series["lazy_pct"][""]
	if s == nil {
		c.mu.RUnlock()
		t.Fatal("expected series")
	}
	if !s.dirtyPct {
		c.mu.RUnlock()
		t.Fatal("expected percentiles marked dirty after writes")
	}
	c.mu.RUnlock()

	agg1 := c.GetAggregation("lazy_pct", nil)
	if agg1 == nil {
		t.Fatal("expected aggregation after lazy recompute")
	}

	c.mu.RLock()
	if s.dirtyPct {
		c.mu.RUnlock()
		t.Fatal("expected dirty flag cleared after aggregation read")
	}
	c.mu.RUnlock()

	c.Record("lazy_pct", 1000, now.Add(50*time.Millisecond), nil)
	c.mu.RLock()
	if !s.dirtyPct {
		c.mu.RUnlock()
		t.Fatal("expected dirty flag set after additional write")
	}
	c.mu.RUnlock()

	agg2 := c.GetAggregation("lazy_pct", nil)
	if agg2 == nil {
		t.Fatal("expected aggregation")
	}
	if agg2.P95 < agg1.P95 {
		t.Fatalf("expected updated percentile after lazy recompute: old p95=%f new p95=%f", agg1.P95, agg2.P95)
	}
}

func TestCollectorLabelSeparationAndMergedAggregation(t *testing.T) {
	c := NewCollector()
	c.Start()
	ts := time.Now()
	c.Record("req", 1, ts, map[string]string{"service": "a"})
	c.Record("req", 2, ts, map[string]string{"service": "b"})
	aggA := c.GetOrComputeAggregation("req", map[string]string{"service": "a"})
	aggB := c.GetOrComputeAggregation("req", map[string]string{"service": "b"})
	if aggA == nil || aggA.Sum != 1 || aggB == nil || aggB.Sum != 2 {
		t.Fatalf("unexpected per-label agg values: a=%+v b=%+v", aggA, aggB)
	}
	merged := c.GetMetricAggregation("req")
	if merged == nil || merged.Sum != 3 || merged.Count != 2 {
		t.Fatalf("unexpected merged agg: %+v", merged)
	}
}

func TestConvertToRunMetricsQueueLengthSumsLatestPerInstanceWithInventory(t *testing.T) {
	c := NewCollector()
	c.Start()
	now := time.Now()
	RecordQueueLength(c, 3.0, now, CreateInstanceLabels("svc1", "a"))
	RecordQueueLength(c, 1.0, now, CreateInstanceLabels("svc1", "b"))
	c.Stop()

	opts := &RunMetricsOptions{
		InstanceIDsByService: map[string][]string{"svc1": {"a", "b", "idle"}},
	}
	runMetrics := ConvertToRunMetrics(c, []map[string]string{{"service": "svc1"}}, opts)
	sm := runMetrics.ServiceMetrics["svc1"]
	if sm == nil || sm.QueueLength != 4 {
		t.Fatalf("expected QueueLength 3+1+0 = 4, got %+v", sm)
	}
}

func TestCollectorMaxPointsLimit(t *testing.T) {
	c := NewCollector()
	c.Start()
	hit := false
	c.SetMaxPoints(1, func(currentCount, max int) {
		hit = true
		if currentCount <= max {
			t.Fatalf("expected currentCount > max, got %d <= %d", currentCount, max)
		}
	})
	now := time.Now()
	c.Record("m", 1, now, nil)
	c.Record("m", 2, now.Add(time.Millisecond), nil)
	if !hit {
		t.Fatalf("expected max points callback")
	}
	points := c.GetTimeSeries("m", nil)
	if len(points) != 1 {
		t.Fatalf("expected only first point retained, got %d", len(points))
	}
}

func TestCollectorMaxSeriesLimitRejectsNewSeriesButAllowsExisting(t *testing.T) {
	t.Setenv("SIMD_MAX_METRIC_SERIES", "2")
	c := NewCollector()
	c.Start()
	limitHit := false
	c.SetLimitCallback(func(limit string, currentCount, max int) {
		if limit == "max_metric_series" {
			limitHit = true
		}
	})
	now := time.Now()
	c.Record("m", 1, now, map[string]string{"service": "a"})
	c.Record("m", 2, now.Add(time.Millisecond), map[string]string{"service": "b"})
	// Third distinct series should be rejected.
	c.Record("m", 3, now.Add(2*time.Millisecond), map[string]string{"service": "c"})
	if !limitHit {
		t.Fatalf("expected max_metric_series limit callback")
	}
	// Existing series should still record.
	c.Record("m", 4, now.Add(3*time.Millisecond), map[string]string{"service": "a"})

	if got := c.totalSeries; got != 2 {
		t.Fatalf("expected 2 series retained, got %d", got)
	}
	if pts := c.GetTimeSeries("m", map[string]string{"service": "a"}); len(pts) != 2 {
		t.Fatalf("expected existing series to continue recording; got %d points", len(pts))
	}
	if pts := c.GetTimeSeries("m", map[string]string{"service": "c"}); len(pts) != 0 {
		t.Fatalf("expected rejected third series to have no points, got %d", len(pts))
	}
}

func TestCollectorSeriesReservoirAllocatedLazily(t *testing.T) {
	c := NewCollector()
	c.Start()
	now := time.Now()
	labels := map[string]string{"service": "lazy"}
	c.Record("m_lazy", 1, now, labels)
	c.mu.RLock()
	s := c.series["m_lazy"][labelKey(labels)]
	c.mu.RUnlock()
	if s == nil {
		t.Fatal("expected series")
	}
	if s.reservoir == nil {
		t.Fatal("expected reservoir allocated on first value")
	}
	if cap(s.reservoir) > 64 {
		t.Fatalf("expected small initial reservoir cap, got %d", cap(s.reservoir))
	}
}
