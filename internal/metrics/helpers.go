package metrics

import (
	"sort"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

// Common metric names
const (
	MetricRequestLatency            = "request_latency_ms"
	MetricServiceRequestLatency     = "service_request_latency_ms"
	MetricServiceProcessingLatency  = "service_processing_latency_ms"
	MetricQueueWait                 = "queue_wait_ms"
	MetricRootRequestLatency        = "root_request_latency_ms"
	MetricRequestCount              = "request_count"
	MetricRequestErrorCount         = "request_error_count"
	MetricCPUUtilization            = "cpu_utilization"
	MetricMemoryUtilization         = "memory_utilization"
	MetricQueueLength               = "queue_length"
	MetricThroughputRPS             = "throughput_rps"
	MetricConcurrentRequests        = "concurrent_requests"
	// MetricIngressLogicalFailure counts user-visible ingress/root trace failures (SLO error rate numerator).
	MetricIngressLogicalFailure = "ingress_logical_failure_count"
	MetricDbWaitMs                = "db_wait_ms"
	MetricActiveConnections       = "active_connections"
	MetricCacheHitCount           = "cache_hit_count"
	MetricCacheMissCount          = "cache_miss_count"
)

// RecordLatency records end-to-end latency for a completed request (per-hop total duration when the request node finishes).
func RecordLatency(collector *Collector, latencyMs float64, timestamp time.Time, labels map[string]string) {
	collector.Record(MetricRequestLatency, latencyMs, timestamp, labels)
}

// RecordServiceRequestLatency records one hop's total local time from this request's ArrivalTime
// at this service to local completion (queue wait + CPU + network for that hop).
func RecordServiceRequestLatency(collector *Collector, latencyMs float64, timestamp time.Time, labels map[string]string) {
	collector.Record(MetricServiceRequestLatency, latencyMs, timestamp, labels)
}

// RecordServiceProcessingLatency records local processing time only (StartTime to completion),
// excluding queue wait before StartTime.
func RecordServiceProcessingLatency(collector *Collector, latencyMs float64, timestamp time.Time, labels map[string]string) {
	collector.Record(MetricServiceProcessingLatency, latencyMs, timestamp, labels)
}

// RecordQueueWait records simulated queue wait at request start (DES time from ArrivalTime to StartTime).
func RecordQueueWait(collector *Collector, queueWaitMs float64, timestamp time.Time, labels map[string]string) {
	collector.Record(MetricQueueWait, queueWaitMs, timestamp, labels)
}

// RecordRootRequestLatency records ingress/root trace latency (external request; full synchronous subtree).
func RecordRootRequestLatency(collector *Collector, latencyMs float64, timestamp time.Time, labels map[string]string) {
	collector.Record(MetricRootRequestLatency, latencyMs, timestamp, labels)
}

// RecordRequestCount records a request count metric
func RecordRequestCount(collector *Collector, count float64, timestamp time.Time, labels map[string]string) {
	collector.Record(MetricRequestCount, count, timestamp, labels)
}

// RecordErrorCount records an error count metric
func RecordErrorCount(collector *Collector, count float64, timestamp time.Time, labels map[string]string) {
	collector.Record(MetricRequestErrorCount, count, timestamp, labels)
}

// RecordCPUUtilization records CPU utilization metric
func RecordCPUUtilization(collector *Collector, utilization float64, timestamp time.Time, labels map[string]string) {
	collector.Record(MetricCPUUtilization, utilization, timestamp, labels)
}

// RecordMemoryUtilization records memory utilization metric
func RecordMemoryUtilization(collector *Collector, utilization float64, timestamp time.Time, labels map[string]string) {
	collector.Record(MetricMemoryUtilization, utilization, timestamp, labels)
}

// RecordQueueLength records queue length metric
func RecordQueueLength(collector *Collector, length float64, timestamp time.Time, labels map[string]string) {
	collector.Record(MetricQueueLength, length, timestamp, labels)
}

// RecordThroughput records throughput metric
func RecordThroughput(collector *Collector, rps float64, timestamp time.Time, labels map[string]string) {
	collector.Record(MetricThroughputRPS, rps, timestamp, labels)
}

// RecordConcurrentRequests records the current in-flight request count (gauge) per instance.
func RecordConcurrentRequests(collector *Collector, count float64, timestamp time.Time, labels map[string]string) {
	collector.Record(MetricConcurrentRequests, count, timestamp, labels)
}

// RecordDbWait records time spent waiting for a datastore connection slot after CPU work.
func RecordDbWait(collector *Collector, dbWaitMs float64, timestamp time.Time, labels map[string]string) {
	collector.Record(MetricDbWaitMs, dbWaitMs, timestamp, labels)
}

// RecordActiveConnections records pooled connection usage (e.g. datastore pool gauge).
func RecordActiveConnections(collector *Collector, count float64, timestamp time.Time, labels map[string]string) {
	collector.Record(MetricActiveConnections, count, timestamp, labels)
}

// RecordCacheHitCount records cache hit events (low-cardinality endpoint labels).
func RecordCacheHitCount(collector *Collector, count float64, timestamp time.Time, labels map[string]string) {
	collector.Record(MetricCacheHitCount, count, timestamp, labels)
}

// RecordCacheMissCount records cache miss events.
func RecordCacheMissCount(collector *Collector, count float64, timestamp time.Time, labels map[string]string) {
	collector.Record(MetricCacheMissCount, count, timestamp, labels)
}

// RecordIngressLogicalFailure records one user-visible ingress/root logical failure (for SLO error rate).
func RecordIngressLogicalFailure(collector *Collector, count float64, timestamp time.Time, labels map[string]string) {
	collector.Record(MetricIngressLogicalFailure, count, timestamp, labels)
}

// CreateServiceLabels creates a labels map for a service
func CreateServiceLabels(serviceName string) map[string]string {
	return map[string]string{
		"service": serviceName,
	}
}

// CreateEndpointLabels creates a labels map for a service endpoint
func CreateEndpointLabels(serviceName, endpoint string) map[string]string {
	return map[string]string{
		"service":  serviceName,
		"endpoint": endpoint,
	}
}

// CreateInstanceLabels creates a labels map for a service instance
func CreateInstanceLabels(serviceName, instanceID string) map[string]string {
	return map[string]string{
		"service":  serviceName,
		"instance": instanceID,
	}
}

// CreateHostLabels creates a labels map for a host
func CreateHostLabels(hostID string) map[string]string {
	return map[string]string{
		"host": hostID,
	}
}

// RunMetricsOptions configures ConvertToRunMetrics when optional resource-manager inventory is available.
type RunMetricsOptions struct {
	// InstanceIDsByService lists each service's instance IDs (from the resource manager).
	// When set, CPU/memory utilization rollups use the mean of the latest gauge per listed
	// instance, treating missing series as 0 (idle replicas that never emitted samples).
	// ConcurrentRequests and QueueLength use the sum of the latest gauge per listed instance.
	InstanceIDsByService map[string][]string
}

func optsInstanceIDs(opts *RunMetricsOptions, serviceName string) []string {
	if opts == nil || opts.InstanceIDsByService == nil {
		return nil
	}
	return opts.InstanceIDsByService[serviceName]
}

// sumSampleValuesForMetric sums all recorded samples for a counter-like metric across label combinations.
func sumSampleValuesForMetric(collector *Collector, metricName string) float64 {
	var sum float64
	for _, labels := range collector.GetLabelsForMetric(metricName) {
		for _, p := range collector.GetTimeSeries(metricName, labels) {
			sum += p.Value
		}
	}
	for _, p := range collector.GetTimeSeries(metricName, nil) {
		sum += p.Value
	}
	return sum
}

// sumRequestCountWithLabel sums request_count samples where labels[key] == value.
func sumRequestCountWithLabel(collector *Collector, key, value string) int64 {
	var sum float64
	for _, labels := range collector.GetLabelsForMetric(MetricRequestCount) {
		if labels[key] != value {
			continue
		}
		for _, p := range collector.GetTimeSeries(MetricRequestCount, labels) {
			sum += p.Value
		}
	}
	return int64(sum)
}

// sumErrorCountWithReason sums request_error_count samples for a given reason label.
func sumErrorCountWithReason(collector *Collector, reason string) int64 {
	var sum float64
	for _, labels := range collector.GetLabelsForMetric(MetricRequestErrorCount) {
		if labels[LabelReason] != reason {
			continue
		}
		for _, p := range collector.GetTimeSeries(MetricRequestErrorCount, labels) {
			sum += p.Value
		}
	}
	return int64(sum)
}

// meanLatestGaugePerInstanceWithInventory averages the latest sample per instance ID;
// instances with no series count as 0 (idle / never sampled).
func meanLatestGaugePerInstanceWithInventory(collector *Collector, metricName, serviceName string, instanceIDs []string) float64 {
	if len(instanceIDs) == 0 {
		return 0
	}
	sum := 0.0
	for _, id := range instanceIDs {
		labels := CreateInstanceLabels(serviceName, id)
		points := collector.GetTimeSeries(metricName, labels)
		if len(points) == 0 {
			continue
		}
		sum += points[len(points)-1].Value
	}
	return sum / float64(len(instanceIDs))
}

// meanLatestGaugePerInstance returns the arithmetic mean of the last sample per instance
// for metricName filtered by service (labels must include "service" and "instance").
// ok is false when no instance-level series exist for that service.
func meanLatestGaugePerInstance(collector *Collector, metricName, serviceName string) (float64, bool) {
	labelCombos := collector.GetLabelsForMetric(metricName)
	var latest []float64
	for _, l := range labelCombos {
		if l["service"] != serviceName || l["instance"] == "" {
			continue
		}
		points := collector.GetTimeSeries(metricName, l)
		if len(points) == 0 {
			continue
		}
		latest = append(latest, points[len(points)-1].Value)
	}
	if len(latest) == 0 {
		return 0, false
	}
	sum := 0.0
	for _, v := range latest {
		sum += v
	}
	return sum / float64(len(latest)), true
}

// sumLatestGaugePerInstance sums the latest sample per instance for metricName (gauge).
func sumLatestGaugePerInstance(collector *Collector, metricName, serviceName string) int {
	labelCombos := collector.GetLabelsForMetric(metricName)
	sum := 0
	for _, l := range labelCombos {
		if l["service"] != serviceName || l["instance"] == "" {
			continue
		}
		points := collector.GetTimeSeries(metricName, l)
		if len(points) > 0 {
			sum += int(points[len(points)-1].Value)
		}
	}
	return sum
}

// sumLatestGaugePerInstanceWithInventory sums the latest gauge per instance; missing series count as 0.
func sumLatestGaugePerInstanceWithInventory(collector *Collector, metricName, serviceName string, instanceIDs []string) int {
	sum := 0
	for _, instID := range instanceIDs {
		lbl := CreateInstanceLabels(serviceName, instID)
		points := collector.GetTimeSeries(metricName, lbl)
		if len(points) > 0 {
			sum += int(points[len(points)-1].Value)
		}
	}
	return sum
}

// ConvertToRunMetrics converts collector metrics to RunMetrics format.
// opts may be nil; when opts.InstanceIDsByService is set, utilization rollups align with
// resource-manager inventory (idle replicas contribute 0).
func ConvertToRunMetrics(collector *Collector, serviceLabels []map[string]string, opts *RunMetricsOptions) *models.RunMetrics {
	collector.ComputeAllAggregations()

	// Aggregate across all label combinations for global metrics
	// Collect latency: prefer ingress/root samples when present (SLO-aligned), else hop totals.
	allLatencyValues := make([]float64, 0)
	allRootLatencyValues := make([]float64, 0)
	allRequestCountValues := make([]float64, 0)
	allErrorCountValues := make([]float64, 0)

	// Get all metric names and aggregate across all labels
	metricNames := collector.GetMetricNames()
	for _, name := range metricNames {
		labelCombos := collector.GetLabelsForMetric(name)
		for _, labels := range labelCombos {
			points := collector.GetTimeSeries(name, labels)
			for _, point := range points {
				switch name {
				case MetricRequestLatency:
					allLatencyValues = append(allLatencyValues, point.Value)
				case MetricRootRequestLatency:
					allRootLatencyValues = append(allRootLatencyValues, point.Value)
				case MetricRequestCount:
					allRequestCountValues = append(allRequestCountValues, point.Value)
				case MetricRequestErrorCount:
					allErrorCountValues = append(allErrorCountValues, point.Value)
				}
			}
		}
		// Also check for points with no labels
		points := collector.GetTimeSeries(name, nil)
		for _, point := range points {
			switch name {
			case MetricRequestLatency:
				allLatencyValues = append(allLatencyValues, point.Value)
			case MetricRootRequestLatency:
				allRootLatencyValues = append(allRootLatencyValues, point.Value)
			case MetricRequestCount:
				allRequestCountValues = append(allRequestCountValues, point.Value)
			case MetricRequestErrorCount:
				allErrorCountValues = append(allErrorCountValues, point.Value)
			}
		}
	}

	ingressReq, internalReq := splitRequestCountByOrigin(collector)

	// Latency percentiles: use root_request_latency_ms when emitted (ingress traces), else request_latency_ms.
	latencyPool := allLatencyValues
	if len(allRootLatencyValues) > 0 {
		latencyPool = allRootLatencyValues
	}
	var latencyP50, latencyP95, latencyP99, latencyMean float64
	if len(latencyPool) > 0 {
		sort.Float64s(latencyPool)
		latencyP50 = calculatePercentile(latencyPool, 0.50)
		latencyP95 = calculatePercentile(latencyPool, 0.95)
		latencyP99 = calculatePercentile(latencyPool, 0.99)
		latencyMean = mean(latencyPool)
	}

	// Sum request counts (all label series; should match ingress+internal when origin labels are present)
	totalRequests := int64(0)
	for _, v := range allRequestCountValues {
		totalRequests += int64(v)
	}

	// Sum error counts (attempt-level: includes downstream retries and internal failures).
	failedRequests := int64(0)
	for _, v := range allErrorCountValues {
		failedRequests += int64(v)
	}

	ingressFailed := int64(sumSampleValuesForMetric(collector, MetricIngressLogicalFailure))
	retryAttempts := sumRequestCountWithLabel(collector, LabelIsRetry, "true")
	timeoutErrors := sumErrorCountWithReason(collector, ReasonTimeout)

	successfulRequests := totalRequests - failedRequests

	// Calculate throughput (use elapsed time when collector not stopped yet, so in-run snapshot has non-zero RPS)
	summary := collector.GetSummary()
	throughputRPS := 0.0
	ingressThroughputRPS := 0.0
	if summary != nil {
		duration := summary.Duration
		if duration <= 0 && !summary.StartTime.IsZero() {
			duration = time.Since(summary.StartTime)
		}
		if duration > 0 {
			throughputRPS = float64(totalRequests) / duration.Seconds()
			ingressThroughputRPS = float64(ingressReq) / duration.Seconds()
		}
	}

	// Build service metrics (aggregate by service; handlers record with endpoint labels so we use label subset)
	serviceMetrics := make(map[string]*models.ServiceMetrics)
	for _, labels := range serviceLabels {
		serviceName := labels["service"]
		if serviceName == "" {
			continue
		}

		// Aggregate across all label combinations that match this service (e.g. all endpoints)
		svcLatencyAgg := collector.GetOrComputeAggregationForLabelSubset(MetricServiceRequestLatency, labels)
		if svcLatencyAgg == nil {
			svcLatencyAgg = collector.GetOrComputeAggregationForLabelSubset(MetricRequestLatency, labels)
		}
		svcQueueAgg := collector.GetOrComputeAggregationForLabelSubset(MetricQueueWait, labels)
		svcProcAgg := collector.GetOrComputeAggregationForLabelSubset(MetricServiceProcessingLatency, labels)
		svcRequestAgg := collector.GetOrComputeAggregationForLabelSubset(MetricRequestCount, labels)
		svcErrorAgg := collector.GetOrComputeAggregationForLabelSubset(MetricRequestErrorCount, labels)
		svcCPUAgg := collector.GetOrComputeAggregationForLabelSubset(MetricCPUUtilization, labels)
		svcMemAgg := collector.GetOrComputeAggregationForLabelSubset(MetricMemoryUtilization, labels)

		svcMetrics := &models.ServiceMetrics{
			ServiceName: serviceName,
		}

		if svcRequestAgg != nil {
			svcMetrics.RequestCount = int64(svcRequestAgg.Sum)
			if svcMetrics.RequestCount == 0 && svcRequestAgg.Count > 0 {
				svcMetrics.RequestCount = svcRequestAgg.Count
			}
		}
		if svcErrorAgg != nil {
			svcMetrics.ErrorCount = int64(svcErrorAgg.Sum)
			if svcMetrics.ErrorCount == 0 && svcErrorAgg.Count > 0 {
				svcMetrics.ErrorCount = svcErrorAgg.Count
			}
		}
		if svcLatencyAgg != nil {
			svcMetrics.LatencyP50 = svcLatencyAgg.P50
			svcMetrics.LatencyP95 = svcLatencyAgg.P95
			svcMetrics.LatencyP99 = svcLatencyAgg.P99
			svcMetrics.LatencyMean = svcLatencyAgg.Mean
		}
		if svcQueueAgg != nil {
			svcMetrics.QueueWaitP50Ms = svcQueueAgg.P50
			svcMetrics.QueueWaitP95Ms = svcQueueAgg.P95
			svcMetrics.QueueWaitP99Ms = svcQueueAgg.P99
			svcMetrics.QueueWaitMeanMs = svcQueueAgg.Mean
		}
		if svcProcAgg != nil {
			svcMetrics.ProcessingLatencyP50Ms = svcProcAgg.P50
			svcMetrics.ProcessingLatencyP95Ms = svcProcAgg.P95
			svcMetrics.ProcessingLatencyP99Ms = svcProcAgg.P99
			svcMetrics.ProcessingLatencyMeanMs = svcProcAgg.Mean
		}
		instIDs := optsInstanceIDs(opts, serviceName)
		// CPU/memory: gauges recorded per instance — mean of latest sample per instance.
		// With inventory, include idle replicas (no series) as 0 to match placements.
		if len(instIDs) > 0 {
			svcMetrics.CPUUtilization = meanLatestGaugePerInstanceWithInventory(collector, MetricCPUUtilization, serviceName, instIDs)
			svcMetrics.MemoryUtilization = meanLatestGaugePerInstanceWithInventory(collector, MetricMemoryUtilization, serviceName, instIDs)
		} else {
			if v, ok := meanLatestGaugePerInstance(collector, MetricCPUUtilization, serviceName); ok {
				svcMetrics.CPUUtilization = v
			} else if svcCPUAgg != nil {
				svcMetrics.CPUUtilization = svcCPUAgg.Mean
			}
			if v, ok := meanLatestGaugePerInstance(collector, MetricMemoryUtilization, serviceName); ok {
				svcMetrics.MemoryUtilization = v
			} else if svcMemAgg != nil {
				svcMetrics.MemoryUtilization = svcMemAgg.Mean
			}
		}

		// Concurrent requests: sum of latest value per instance (gauge-style, not sum over time)
		if len(instIDs) > 0 {
			svcMetrics.ConcurrentRequests = sumLatestGaugePerInstanceWithInventory(collector, MetricConcurrentRequests, serviceName, instIDs)
		} else {
			svcMetrics.ConcurrentRequests = sumLatestGaugePerInstance(collector, MetricConcurrentRequests, serviceName)
		}

		// Queue length: same gauge semantics as concurrent_requests (sum per instance)
		if len(instIDs) > 0 {
			svcMetrics.QueueLength = sumLatestGaugePerInstanceWithInventory(collector, MetricQueueLength, serviceName, instIDs)
		} else {
			svcMetrics.QueueLength = sumLatestGaugePerInstance(collector, MetricQueueLength, serviceName)
		}

		serviceMetrics[serviceName] = svcMetrics
	}

	var ingressErrRate, attemptErrRate float64
	if ingressReq > 0 {
		ingressErrRate = float64(ingressFailed) / float64(ingressReq)
	}
	if totalRequests > 0 {
		attemptErrRate = float64(failedRequests) / float64(totalRequests)
	}

	return &models.RunMetrics{
		TotalRequests:         totalRequests,
		SuccessfulRequests:    successfulRequests,
		FailedRequests:        failedRequests,
		LatencyP50:            latencyP50,
		LatencyP95:            latencyP95,
		LatencyP99:            latencyP99,
		LatencyMean:           latencyMean,
		ThroughputRPS:         throughputRPS,
		IngressRequests:       ingressReq,
		InternalRequests:      internalReq,
		IngressThroughputRPS:  ingressThroughputRPS,
		IngressFailedRequests: ingressFailed,
		IngressErrorRate:      ingressErrRate,
		AttemptFailedRequests: failedRequests,
		AttemptErrorRate:      attemptErrRate,
		RetryAttempts:         retryAttempts,
		TimeoutErrors:         timeoutErrors,
		ServiceMetrics:        serviceMetrics,
	}
}

// splitRequestCountByOrigin sums request_count series by origin label.
// Series without origin count as ingress (backward compatibility).
func splitRequestCountByOrigin(collector *Collector) (ingress, internal int64) {
	labelCombos := collector.GetLabelsForMetric(MetricRequestCount)
	for _, labels := range labelCombos {
		points := collector.GetTimeSeries(MetricRequestCount, labels)
		var sum float64
		for _, p := range points {
			sum += p.Value
		}
		n := int64(sum)
		switch labels[LabelOrigin] {
		case OriginDownstream:
			internal += n
		case OriginIngress:
			ingress += n
		default:
			ingress += n
		}
	}
	return ingress, internal
}

// AttachHostUtilization fills per-host CPU/memory utilization from the latest gauge sample per host
// (not time-averaged). Hosts with no series appear as 0 (e.g. scaled-out idle hosts).
func AttachHostUtilization(rm *models.RunMetrics, collector *Collector, hostIDs []string) {
	if rm == nil || len(hostIDs) == 0 {
		return
	}
	if rm.HostMetrics == nil {
		rm.HostMetrics = make(map[string]*models.HostMetrics)
	}
	for _, hid := range hostIDs {
		labels := CreateHostLabels(hid)
		hm := &models.HostMetrics{HostID: hid}
		if pts := collector.GetTimeSeries(MetricCPUUtilization, labels); len(pts) > 0 {
			hm.CPUUtilization = pts[len(pts)-1].Value
		}
		if pts := collector.GetTimeSeries(MetricMemoryUtilization, labels); len(pts) > 0 {
			hm.MemoryUtilization = pts[len(pts)-1].Value
		}
		rm.HostMetrics[hid] = hm
	}
}

// mean calculates the mean of a slice of values
func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0.0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}
