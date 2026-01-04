package metrics

import (
	"sort"
	"sync"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

// Collector collects time-series metrics during simulation
type Collector struct {
	mu sync.RWMutex

	startTime time.Time
	endTime   time.Time

	// Time-series data: metric name -> labels -> []MetricPoint
	timeSeries map[string]map[string][]*models.MetricPoint

	// Aggregated data: metric name -> labels -> Aggregation
	aggregations map[string]map[string]*models.Aggregation
}

// NewCollector creates a new metrics collector
func NewCollector() *Collector {
	return &Collector{
		startTime:    time.Now(),
		timeSeries:   make(map[string]map[string][]*models.MetricPoint),
		aggregations: make(map[string]map[string]*models.Aggregation),
	}
}

// Start marks the start of metric collection
func (c *Collector) Start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.startTime = time.Now()
}

// Stop marks the end of metric collection
func (c *Collector) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.endTime = time.Now()
}

// Record records a metric value at a specific timestamp
func (c *Collector) Record(name string, value float64, timestamp time.Time, labels map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	labelKey := labelKey(labels)
	if c.timeSeries[name] == nil {
		c.timeSeries[name] = make(map[string][]*models.MetricPoint)
	}
	if c.timeSeries[name][labelKey] == nil {
		c.timeSeries[name][labelKey] = make([]*models.MetricPoint, 0)
	}

	point := &models.MetricPoint{
		Timestamp: timestamp,
		Name:      name,
		Value:     value,
		Labels:    copyLabels(labels),
	}

	c.timeSeries[name][labelKey] = append(c.timeSeries[name][labelKey], point)
}

// RecordNow records a metric value at the current time
func (c *Collector) RecordNow(name string, value float64, labels map[string]string) {
	c.Record(name, value, time.Now(), labels)
}

// GetTimeSeries returns all time-series points for a metric
func (c *Collector) GetTimeSeries(name string, labels map[string]string) []*models.MetricPoint {
	c.mu.RLock()
	defer c.mu.RUnlock()

	labelKey := labelKey(labels)
	if c.timeSeries[name] == nil {
		return nil
	}
	points := c.timeSeries[name][labelKey]
	if points == nil {
		return nil
	}

	// Return a copy
	result := make([]*models.MetricPoint, len(points))
	for i, p := range points {
		result[i] = &models.MetricPoint{
			Timestamp: p.Timestamp,
			Name:      p.Name,
			Value:     p.Value,
			Labels:    copyLabels(p.Labels),
		}
	}
	return result
}

// GetAggregation calculates and returns aggregated statistics for a metric
func (c *Collector) GetAggregation(name string, labels map[string]string) *models.Aggregation {
	c.mu.RLock()
	defer c.mu.RUnlock()

	labelKey := labelKey(labels)
	points := c.getPointsUnsafe(name, labelKey)
	if len(points) == 0 {
		return nil
	}

	return calculateAggregation(points)
}

// GetOrComputeAggregation gets cached aggregation or computes it
func (c *Collector) GetOrComputeAggregation(name string, labels map[string]string) *models.Aggregation {
	c.mu.Lock()
	defer c.mu.Unlock()

	labelKey := labelKey(labels)
	if c.aggregations[name] == nil {
		c.aggregations[name] = make(map[string]*models.Aggregation)
	}

	// Check cache
	if agg, ok := c.aggregations[name][labelKey]; ok {
		return agg
	}

	// Compute and cache
	points := c.getPointsUnsafe(name, labelKey)
	if len(points) == 0 {
		return nil
	}

	agg := calculateAggregation(points)
	c.aggregations[name][labelKey] = agg
	return agg
}

// ComputeAllAggregations computes aggregations for all metrics
func (c *Collector) ComputeAllAggregations() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for name, labelMap := range c.timeSeries {
		if c.aggregations[name] == nil {
			c.aggregations[name] = make(map[string]*models.Aggregation)
		}
		for labelKey, points := range labelMap {
			if len(points) > 0 {
				c.aggregations[name][labelKey] = calculateAggregation(points)
			}
		}
	}
}

// GetSummary returns a summary of all collected metrics
func (c *Collector) GetSummary() *models.MetricsSummary {
	c.mu.RLock()
	defer c.mu.RUnlock()

	summary := &models.MetricsSummary{
		StartTime:    c.startTime,
		EndTime:      c.endTime,
		Duration:     c.endTime.Sub(c.startTime),
		Metrics:      make(map[string][]float64),
		Aggregations: make(map[string]*models.Aggregation),
	}

	// Collect all metric values
	for name, labelMap := range c.timeSeries {
		allValues := make([]float64, 0)
		for _, points := range labelMap {
			for _, point := range points {
				allValues = append(allValues, point.Value)
			}
		}
		summary.Metrics[name] = allValues
	}

	// Compute aggregations for each metric (using default labels)
	for name := range c.timeSeries {
		agg := c.GetAggregation(name, nil)
		if agg != nil {
			summary.Aggregations[name] = agg
		}
	}

	return summary
}

// GetMetricNames returns all metric names that have been collected
func (c *Collector) GetMetricNames() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	names := make([]string, 0, len(c.timeSeries))
	for name := range c.timeSeries {
		names = append(names, name)
	}
	return names
}

// GetLabelsForMetric returns all label combinations for a metric
func (c *Collector) GetLabelsForMetric(name string) []map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.timeSeries[name] == nil {
		return nil
	}

	labelsList := make([]map[string]string, 0, len(c.timeSeries[name]))
	for labelKey, points := range c.timeSeries[name] {
		if len(points) > 0 {
			labelsList = append(labelsList, points[0].Labels)
		}
		_ = labelKey // labelKey is for internal use
	}
	return labelsList
}

// Clear clears all collected metrics
func (c *Collector) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.timeSeries = make(map[string]map[string][]*models.MetricPoint)
	c.aggregations = make(map[string]map[string]*models.Aggregation)
	c.startTime = time.Now()
	c.endTime = time.Time{}
}

// getPointsUnsafe returns points without locking (caller must hold lock)
func (c *Collector) getPointsUnsafe(name, labelKey string) []*models.MetricPoint {
	if c.timeSeries[name] == nil {
		return nil
	}
	return c.timeSeries[name][labelKey]
}

// labelKey creates a key from labels for map lookup
func labelKey(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}

	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	key := ""
	for _, k := range keys {
		key += k + "=" + labels[k] + ","
	}
	return key
}

// copyLabels creates a copy of the labels map
func copyLabels(labels map[string]string) map[string]string {
	if labels == nil {
		return nil
	}
	copy := make(map[string]string, len(labels))
	for k, v := range labels {
		copy[k] = v
	}
	return copy
}

// calculateAggregation calculates aggregated statistics from metric points
func calculateAggregation(points []*models.MetricPoint) *models.Aggregation {
	if len(points) == 0 {
		return nil
	}

	values := make([]float64, len(points))
	for i, p := range points {
		values[i] = p.Value
	}

	sort.Float64s(values)

	count := int64(len(values))
	sum := 0.0
	min := values[0]
	max := values[len(values)-1]

	for _, v := range values {
		sum += v
	}

	mean := sum / float64(count)
	p50 := calculatePercentile(values, 0.50)
	p95 := calculatePercentile(values, 0.95)
	p99 := calculatePercentile(values, 0.99)

	return &models.Aggregation{
		Count: count,
		Sum:   sum,
		Min:   min,
		Max:   max,
		Mean:  mean,
		P50:   p50,
		P95:   p95,
		P99:   p99,
	}
}

// calculatePercentile calculates the percentile value from a sorted slice
func calculatePercentile(sortedValues []float64, p float64) float64 {
	if len(sortedValues) == 0 {
		return 0.0
	}
	if len(sortedValues) == 1 {
		return sortedValues[0]
	}

	index := p * float64(len(sortedValues)-1)
	lower := int(index)
	upper := lower + 1

	if upper >= len(sortedValues) {
		return sortedValues[len(sortedValues)-1]
	}

	weight := index - float64(lower)
	return sortedValues[lower]*(1-weight) + sortedValues[upper]*weight
}
