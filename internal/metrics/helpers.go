package metrics

import (
	"sort"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

// Common metric names
const (
	MetricRequestLatency    = "request_latency_ms"
	MetricRequestCount      = "request_count"
	MetricRequestErrorCount = "request_error_count"
	MetricCPUUtilization    = "cpu_utilization"
	MetricMemoryUtilization = "memory_utilization"
	MetricQueueLength       = "queue_length"
	MetricThroughputRPS     = "throughput_rps"
)

// RecordLatency records a request latency metric
func RecordLatency(collector *Collector, latencyMs float64, timestamp time.Time, labels map[string]string) {
	collector.Record(MetricRequestLatency, latencyMs, timestamp, labels)
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

// ConvertToRunMetrics converts collector metrics to RunMetrics format
func ConvertToRunMetrics(collector *Collector, serviceLabels []map[string]string) *models.RunMetrics {
	collector.ComputeAllAggregations()

	// Aggregate across all label combinations for global metrics
	// Collect all latency values regardless of labels
	allLatencyValues := make([]float64, 0)
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
			case MetricRequestCount:
				allRequestCountValues = append(allRequestCountValues, point.Value)
			case MetricRequestErrorCount:
				allErrorCountValues = append(allErrorCountValues, point.Value)
			}
		}
	}

	// Calculate latency percentiles
	var latencyP50, latencyP95, latencyP99, latencyMean float64
	if len(allLatencyValues) > 0 {
		sort.Float64s(allLatencyValues)
		latencyP50 = calculatePercentile(allLatencyValues, 0.50)
		latencyP95 = calculatePercentile(allLatencyValues, 0.95)
		latencyP99 = calculatePercentile(allLatencyValues, 0.99)
		latencyMean = mean(allLatencyValues)
	}

	// Sum request counts
	totalRequests := int64(0)
	for _, v := range allRequestCountValues {
		totalRequests += int64(v)
	}

	// Sum error counts
	failedRequests := int64(0)
	for _, v := range allErrorCountValues {
		failedRequests += int64(v)
	}

	successfulRequests := totalRequests - failedRequests

	// Calculate throughput
	summary := collector.GetSummary()
	throughputRPS := 0.0
	if summary.Duration > 0 {
		throughputRPS = float64(totalRequests) / summary.Duration.Seconds()
	}

	// Build service metrics
	serviceMetrics := make(map[string]*models.ServiceMetrics)
	for _, labels := range serviceLabels {
		serviceName := labels["service"]
		if serviceName == "" {
			continue
		}

		// Get service-specific aggregations
		svcLatencyAgg := collector.GetOrComputeAggregation(MetricRequestLatency, labels)
		svcRequestAgg := collector.GetOrComputeAggregation(MetricRequestCount, labels)
		svcErrorAgg := collector.GetOrComputeAggregation(MetricRequestErrorCount, labels)
		svcCPUAgg := collector.GetOrComputeAggregation(MetricCPUUtilization, labels)
		svcMemAgg := collector.GetOrComputeAggregation(MetricMemoryUtilization, labels)

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
		if svcCPUAgg != nil {
			svcMetrics.CPUUtilization = svcCPUAgg.Mean
		}
		if svcMemAgg != nil {
			svcMetrics.MemoryUtilization = svcMemAgg.Mean
		}

		serviceMetrics[serviceName] = svcMetrics
	}

	return &models.RunMetrics{
		TotalRequests:      totalRequests,
		SuccessfulRequests: successfulRequests,
		FailedRequests:     failedRequests,
		LatencyP50:         latencyP50,
		LatencyP95:         latencyP95,
		LatencyP99:         latencyP99,
		LatencyMean:        latencyMean,
		ThroughputRPS:      throughputRPS,
		ServiceMetrics:     serviceMetrics,
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
