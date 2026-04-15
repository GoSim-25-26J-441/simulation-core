package calibration

import (
	"fmt"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/simd"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

// RunBaselinePredictedRun executes the scenario for each seed, then merges RunMetrics conservatively for use as
// CalibrateOptions.PredictedRun (ratio-based calibration vs a reproducible baseline).
func RunBaselinePredictedRun(scenario *config.Scenario, simDurationMs int64, seeds []int64, realTime bool) (*models.RunMetrics, error) {
	if scenario == nil {
		return nil, fmt.Errorf("scenario is nil")
	}
	if simDurationMs <= 0 {
		simDurationMs = 10_000
	}
	if len(seeds) == 0 {
		seeds = []int64{1, 2, 3}
	}
	dur := time.Duration(simDurationMs) * time.Millisecond
	var runs []*models.RunMetrics
	for _, seed := range seeds {
		rm, err := simd.RunScenarioForMetrics(scenario, dur, seed, realTime)
		if err != nil {
			return nil, fmt.Errorf("baseline seed %d: %w", seed, err)
		}
		runs = append(runs, rm)
	}
	return MergeRunMetricsForCalibrationBaseline(runs), nil
}

// MergeRunMetricsForCalibrationBaseline folds multiple simulator RunMetrics into one baseline suitable for
// CalibrateScenario.PredictedRun: central latency and throughput use means across seeds; tails and utilization
// use conservative maxima where that matches validation aggregation.
func MergeRunMetricsForCalibrationBaseline(runs []*models.RunMetrics) *models.RunMetrics {
	if len(runs) == 0 {
		return nil
	}
	if len(runs) == 1 && runs[0] != nil {
		return cloneRunMetricsForBaseline(runs[0])
	}
	n := float64(len(runs))
	var sumIngress, sumP50, sumMean float64
	var maxP95, maxP99, maxIngressErr float64
	var sumIngressReq, sumTotalReq, sumSucc, sumFail int64
	var maxQD, maxTB, maxTL float64
	var maxDrop, maxTDrop float64
	var maxQDlq, maxTDlq int64
	var maxQAge, maxTAge float64
	var maxRetry, maxTimeout int64
	svcMerge := make(map[string]*serviceAgg)
	epMerge := make(map[string]*endpointAgg)

	for _, rm := range runs {
		if rm == nil {
			continue
		}
		sumIngress += rm.IngressThroughputRPS
		sumP50 += rm.LatencyP50
		sumMean += rm.LatencyMean
		if rm.LatencyP95 > maxP95 {
			maxP95 = rm.LatencyP95
		}
		if rm.LatencyP99 > maxP99 {
			maxP99 = rm.LatencyP99
		}
		if rm.IngressErrorRate > maxIngressErr {
			maxIngressErr = rm.IngressErrorRate
		}
		sumIngressReq += rm.IngressRequests
		sumTotalReq += rm.TotalRequests
		sumSucc += rm.SuccessfulRequests
		sumFail += rm.FailedRequests
		if rm.QueueDepthSum > maxQD {
			maxQD = rm.QueueDepthSum
		}
		if rm.TopicBacklogDepthSum > maxTB {
			maxTB = rm.TopicBacklogDepthSum
		}
		if rm.TopicConsumerLagSum > maxTL {
			maxTL = rm.TopicConsumerLagSum
		}
		if rm.QueueDropRate > maxDrop {
			maxDrop = rm.QueueDropRate
		}
		if rm.TopicDropRate > maxTDrop {
			maxTDrop = rm.TopicDropRate
		}
		if rm.QueueDlqCountTotal > maxQDlq {
			maxQDlq = rm.QueueDlqCountTotal
		}
		if rm.TopicDlqCountTotal > maxTDlq {
			maxTDlq = rm.TopicDlqCountTotal
		}
		if rm.QueueOldestMessageAgeMs > maxQAge {
			maxQAge = rm.QueueOldestMessageAgeMs
		}
		if rm.TopicOldestMessageAgeMs > maxTAge {
			maxTAge = rm.TopicOldestMessageAgeMs
		}
		if rm.RetryAttempts > maxRetry {
			maxRetry = rm.RetryAttempts
		}
		if rm.TimeoutErrors > maxTimeout {
			maxTimeout = rm.TimeoutErrors
		}
		mergeServiceMetricsInto(svcMerge, rm.ServiceMetrics)
		mergeEndpointStatsInto(epMerge, rm.EndpointRequestStats)
	}

	out := &models.RunMetrics{
		IngressThroughputRPS: sumIngress / n,
		LatencyP50:           sumP50 / n,
		LatencyMean:          sumMean / n,
		LatencyP95:           maxP95,
		LatencyP99:           maxP99,
		IngressErrorRate:     maxIngressErr,
		IngressRequests:      int64(float64(sumIngressReq) / n),
		TotalRequests:        int64(float64(sumTotalReq) / n),
		SuccessfulRequests:   int64(float64(sumSucc) / n),
		FailedRequests:       int64(float64(sumFail) / n),
		ThroughputRPS:        sumIngress / n,
		QueueDepthSum:            maxQD,
		TopicBacklogDepthSum:     maxTB,
		TopicConsumerLagSum:      maxTL,
		QueueDropRate:            maxDrop,
		TopicDropRate:            maxTDrop,
		QueueDlqCountTotal:       maxQDlq,
		TopicDlqCountTotal:       maxTDlq,
		QueueOldestMessageAgeMs:  maxQAge,
		TopicOldestMessageAgeMs:  maxTAge,
		RetryAttempts:            maxRetry,
		TimeoutErrors:            maxTimeout,
		ServiceMetrics:           finalizeServiceMerge(svcMerge),
		EndpointRequestStats:     finalizeEndpointMerge(epMerge),
	}
	return out
}

type serviceAgg struct {
	n            int
	sumReq, sumErr int64
	sumP50, sumMean float64
	maxP95, maxP99 float64
	sumProcMean float64
	maxProcP95, maxProcP99 float64
	sumQWMean float64
	maxQWP95, maxQWP99 float64
	maxCPU, maxMem float64
	maxQLen int
	maxConc int
	maxReplicas int
}

func mergeServiceMetricsInto(dst map[string]*serviceAgg, smap map[string]*models.ServiceMetrics) {
	for id, sm := range smap {
		if sm == nil {
			continue
		}
		a, ok := dst[id]
		if !ok {
			a = &serviceAgg{}
			dst[id] = a
		}
		a.n++
		a.sumReq += sm.RequestCount
		a.sumErr += sm.ErrorCount
		a.sumP50 += sm.LatencyP50
		a.sumMean += sm.LatencyMean
		if sm.LatencyP95 > a.maxP95 {
			a.maxP95 = sm.LatencyP95
		}
		if sm.LatencyP99 > a.maxP99 {
			a.maxP99 = sm.LatencyP99
		}
		a.sumProcMean += sm.ProcessingLatencyMeanMs
		if sm.ProcessingLatencyP95Ms > a.maxProcP95 {
			a.maxProcP95 = sm.ProcessingLatencyP95Ms
		}
		if sm.ProcessingLatencyP99Ms > a.maxProcP99 {
			a.maxProcP99 = sm.ProcessingLatencyP99Ms
		}
		a.sumQWMean += sm.QueueWaitMeanMs
		if sm.QueueWaitP95Ms > a.maxQWP95 {
			a.maxQWP95 = sm.QueueWaitP95Ms
		}
		if sm.QueueWaitP99Ms > a.maxQWP99 {
			a.maxQWP99 = sm.QueueWaitP99Ms
		}
		if sm.CPUUtilization > a.maxCPU {
			a.maxCPU = sm.CPUUtilization
		}
		if sm.MemoryUtilization > a.maxMem {
			a.maxMem = sm.MemoryUtilization
		}
		if sm.QueueLength > a.maxQLen {
			a.maxQLen = sm.QueueLength
		}
		if sm.ConcurrentRequests > a.maxConc {
			a.maxConc = sm.ConcurrentRequests
		}
		if sm.ActiveReplicas > a.maxReplicas {
			a.maxReplicas = sm.ActiveReplicas
		}
	}
}

func finalizeServiceMerge(dst map[string]*serviceAgg) map[string]*models.ServiceMetrics {
	if len(dst) == 0 {
		return nil
	}
	out := make(map[string]*models.ServiceMetrics, len(dst))
	for id, a := range dst {
		if a.n == 0 {
			continue
		}
		div := float64(a.n)
		out[id] = &models.ServiceMetrics{
			ServiceName:             id,
			RequestCount:            a.sumReq / int64(a.n),
			ErrorCount:              a.sumErr / int64(a.n),
			LatencyP50:              a.sumP50 / div,
			LatencyMean:             a.sumMean / div,
			LatencyP95:              a.maxP95,
			LatencyP99:              a.maxP99,
			ProcessingLatencyMeanMs: a.sumProcMean / div,
			ProcessingLatencyP95Ms:    a.maxProcP95,
			ProcessingLatencyP99Ms:    a.maxProcP99,
			QueueWaitMeanMs:         a.sumQWMean / div,
			QueueWaitP95Ms:          a.maxQWP95,
			QueueWaitP99Ms:          a.maxQWP99,
			CPUUtilization:          a.maxCPU,
			MemoryUtilization:       a.maxMem,
			QueueLength:             a.maxQLen,
			ConcurrentRequests:      a.maxConc,
			ActiveReplicas:          a.maxReplicas,
		}
	}
	return out
}

func cloneRunMetricsForBaseline(rm *models.RunMetrics) *models.RunMetrics {
	if rm == nil {
		return nil
	}
	cp := *rm
	if rm.ServiceMetrics != nil {
		cp.ServiceMetrics = make(map[string]*models.ServiceMetrics, len(rm.ServiceMetrics))
		for k, v := range rm.ServiceMetrics {
			if v == nil {
				continue
			}
			vv := *v
			cp.ServiceMetrics[k] = &vv
		}
	}
	if len(rm.EndpointRequestStats) > 0 {
		cp.EndpointRequestStats = append([]models.EndpointRequestStats(nil), rm.EndpointRequestStats...)
	}
	return &cp
}
