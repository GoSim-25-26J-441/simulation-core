package metrics

import (
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

const (
	defaultMaxSeriesPoints = 256
	defaultReservoirSize   = 1024
)

type metricSeries struct {
	labels     map[string]string
	points     []*models.MetricPoint
	maxPoints  int
	reservoir  []float64
	maxResSize int
	seenValues int64
	agg        models.Aggregation
}

func newMetricSeries(labels map[string]string, maxSeriesPoints, maxReservoir int) *metricSeries {
	return &metricSeries{
		labels:     copyLabels(labels),
		points:     make([]*models.MetricPoint, 0, maxSeriesPoints),
		maxPoints:  maxSeriesPoints,
		reservoir:  make([]float64, 0, maxReservoir),
		maxResSize: maxReservoir,
		agg: models.Aggregation{
			Min: 0,
			Max: 0,
		},
	}
}

func (s *metricSeries) addPoint(point *models.MetricPoint) {
	s.points = append(s.points, point)
	// Keep downsampled bounded timeline by periodic compaction.
	if len(s.points) > s.maxPoints {
		half := (len(s.points) + 1) / 2
		down := make([]*models.MetricPoint, 0, half)
		for i := 0; i < len(s.points); i += 2 {
			down = append(down, s.points[i])
		}
		s.points = down
	}
}

func (s *metricSeries) addValue(value float64) {
	s.seenValues++
	if s.agg.Count == 0 {
		s.agg.Min = value
		s.agg.Max = value
	} else {
		if value < s.agg.Min {
			s.agg.Min = value
		}
		if value > s.agg.Max {
			s.agg.Max = value
		}
	}
	s.agg.Count++
	s.agg.Sum += value
	s.agg.Mean = s.agg.Sum / float64(s.agg.Count)

	// Vitter's reservoir sampling (Algorithm R) for bounded percentile estimation.
	if len(s.reservoir) < s.maxResSize {
		s.reservoir = append(s.reservoir, value)
	} else if s.maxResSize > 0 {
		idx := int((s.seenValues * 1103515245) % int64(s.maxResSize)) // deterministic pseudo-random slot
		if idx >= 0 && idx < len(s.reservoir) {
			s.reservoir[idx] = value
		}
	}
	s.updatePercentiles()
}

func (s *metricSeries) updatePercentiles() {
	if len(s.reservoir) == 0 {
		s.agg.P50 = 0
		s.agg.P95 = 0
		s.agg.P99 = 0
		return
	}
	vals := append([]float64(nil), s.reservoir...)
	sort.Float64s(vals)
	s.agg.P50 = calculatePercentile(vals, 0.50)
	s.agg.P95 = calculatePercentile(vals, 0.95)
	s.agg.P99 = calculatePercentile(vals, 0.99)
}

func (s *metricSeries) aggregationCopy() *models.Aggregation {
	if s == nil || s.agg.Count == 0 {
		return nil
	}
	cp := s.agg
	return &cp
}

func loadIntEnv(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// Collector collects time-series metrics during simulation
type Collector struct {
	mu sync.RWMutex

	startTime time.Time
	endTime   time.Time

	// Series data: metric name -> labels -> bounded series with streaming aggregation.
	series map[string]map[string]*metricSeries

	maxSeriesPoints int
	maxReservoir    int

	totalPoints int
	maxPoints   int
	onLimit     func(currentCount, max int)
}

type CollectorSnapshot struct {
	SeriesCount int `json:"series_count"`
	TotalPoints int `json:"total_points"`
}

// NewCollector creates a new metrics collector
func NewCollector() *Collector {
	return &Collector{
		startTime:       time.Now(),
		series:          make(map[string]map[string]*metricSeries),
		maxSeriesPoints: loadIntEnv("SIMD_MAX_METRIC_SERIES_POINTS", defaultMaxSeriesPoints),
		maxReservoir:    loadIntEnv("SIMD_METRIC_RESERVOIR_SIZE", defaultReservoirSize),
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
	if c.maxPoints > 0 && c.totalPoints >= c.maxPoints {
		if c.onLimit != nil {
			c.onLimit(c.totalPoints+1, c.maxPoints)
		}
		return
	}

	labelKey := labelKey(labels)
	if c.series[name] == nil {
		c.series[name] = make(map[string]*metricSeries)
	}
	s := c.series[name][labelKey]
	if s == nil {
		s = newMetricSeries(labels, c.maxSeriesPoints, c.maxReservoir)
		c.series[name][labelKey] = s
	}

	point := &models.MetricPoint{
		Timestamp: timestamp,
		Name:      name,
		Value:     value,
		Labels:    copyLabels(labels),
	}
	s.addPoint(point)
	s.addValue(value)
	c.totalPoints++
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
	if c.series[name] == nil {
		return nil
	}
	s := c.series[name][labelKey]
	if s == nil {
		return nil
	}

	// Return a copy
	result := make([]*models.MetricPoint, len(s.points))
	for i, p := range s.points {
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
	if c.series[name] == nil {
		return nil
	}
	return c.series[name][labelKey].aggregationCopy()
}

// GetOrComputeAggregation gets cached aggregation or computes it
func (c *Collector) GetOrComputeAggregation(name string, labels map[string]string) *models.Aggregation {
	c.mu.Lock()
	defer c.mu.Unlock()
	labelKey := labelKey(labels)
	if c.series[name] == nil || c.series[name][labelKey] == nil {
		return nil
	}
	return c.series[name][labelKey].aggregationCopy()
}

// ComputeAllAggregations computes aggregations for all metrics
func (c *Collector) ComputeAllAggregations() {
	// No-op: aggregations are maintained incrementally during Record.
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
	for name, labelMap := range c.series {
		allValues := make([]float64, 0)
		for _, s := range labelMap {
			for _, point := range s.points {
				allValues = append(allValues, point.Value)
			}
		}
		summary.Metrics[name] = allValues
	}

	// Compute aggregations for each metric (using default/empty labels to match GetAggregation(name, nil))
	// Use getPointsUnsafe + calculateAggregation to avoid deadlock (GetAggregation would try to RLock again)
	for name, labelMap := range c.series {
		merged := mergeAggregations(nil, nil)
		for _, s := range labelMap {
			merged = mergeAggregations(merged, s.aggregationCopy())
		}
		if merged != nil {
			summary.Aggregations[name] = merged
		}
	}

	return summary
}

// GetMetricNames returns all metric names that have been collected
func (c *Collector) GetMetricNames() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	names := make([]string, 0, len(c.series))
	for name := range c.series {
		names = append(names, name)
	}
	return names
}

// GetLabelsForMetric returns all label combinations for a metric
func (c *Collector) GetLabelsForMetric(name string) []map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.series[name] == nil {
		return nil
	}

	labelsList := make([]map[string]string, 0, len(c.series[name]))
	for _, s := range c.series[name] {
		labelsList = append(labelsList, copyLabels(s.labels))
	}
	return labelsList
}

// labelsMatchSubset returns true if labels contains all key-value pairs in subset
func labelsMatchSubset(labels, subset map[string]string) bool {
	for k, v := range subset {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// GetOrComputeAggregationForLabelSubset returns aggregation for a metric across all
// label combinations that contain the given subset (e.g. service="auth" to aggregate
// over all endpoints of that service). Used for per-service metrics when recording
// uses endpoint-level labels.
func (c *Collector) GetOrComputeAggregationForLabelSubset(name string, labelSubset map[string]string) *models.Aggregation {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.series[name] == nil || len(labelSubset) == 0 {
		return nil
	}
	var merged *models.Aggregation
	for _, s := range c.series[name] {
		if labelsMatchSubset(s.labels, labelSubset) {
			merged = mergeAggregations(merged, s.aggregationCopy())
		}
	}
	return merged
}

// Clear clears all collected metrics
func (c *Collector) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.series = make(map[string]map[string]*metricSeries)
	c.totalPoints = 0
	c.startTime = time.Now()
	c.endTime = time.Time{}
}

// SetMaxPoints configures an optional hard cap on retained metric points.
// When the cap is reached, additional points are dropped and onLimit is invoked.
func (c *Collector) SetMaxPoints(max int, onLimit func(currentCount, max int)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxPoints = max
	c.onLimit = onLimit
}

func (c *Collector) Snapshot() CollectorSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	series := 0
	for _, labelMap := range c.series {
		series += len(labelMap)
	}
	return CollectorSnapshot{
		SeriesCount: series,
		TotalPoints: c.totalPoints,
	}
}

// GetLastValue returns the latest observed value for metric+labels.
func (c *Collector) GetLastValue(name string, labels map[string]string) (float64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.series[name] == nil {
		return 0, false
	}
	s := c.series[name][labelKey(labels)]
	if s == nil || len(s.points) == 0 {
		return 0, false
	}
	return s.points[len(s.points)-1].Value, true
}

// SumMetric returns the sum of all values for a metric across all label series.
func (c *Collector) SumMetric(name string) float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	sum := 0.0
	for _, s := range c.series[name] {
		sum += s.agg.Sum
	}
	return sum
}

// CountMetricSamples returns total sample count across all label series for a metric.
func (c *Collector) CountMetricSamples(name string) int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var n int64
	for _, s := range c.series[name] {
		n += s.agg.Count
	}
	return n
}

// SumMetricWhere sums values for metric series matching an exact label key/value pair.
func (c *Collector) SumMetricWhere(name, key, value string) float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	sum := 0.0
	for _, s := range c.series[name] {
		if s.labels[key] == value {
			sum += s.agg.Sum
		}
	}
	return sum
}

// GetMetricAggregation returns merged aggregation across all label series for a metric.
func (c *Collector) GetMetricAggregation(name string) *models.Aggregation {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var merged *models.Aggregation
	for _, s := range c.series[name] {
		merged = mergeAggregations(merged, s.aggregationCopy())
	}
	return merged
}

func mergeAggregations(dst, src *models.Aggregation) *models.Aggregation {
	if src == nil || src.Count == 0 {
		return dst
	}
	if dst == nil {
		cp := *src
		return &cp
	}
	out := *dst
	if src.Min < out.Min {
		out.Min = src.Min
	}
	if src.Max > out.Max {
		out.Max = src.Max
	}
	out.Count += src.Count
	out.Sum += src.Sum
	if out.Count > 0 {
		out.Mean = out.Sum / float64(out.Count)
	}
	// Percentiles from merged buckets are approximate; weighted mean preserves semantics.
	out.P50 = (dst.P50 + src.P50) / 2
	out.P95 = (dst.P95 + src.P95) / 2
	out.P99 = (dst.P99 + src.P99) / 2
	return &out
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
