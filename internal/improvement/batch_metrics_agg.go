package improvement

import (
	"sort"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// AggregateRunMetrics combines metrics from multiple evaluation runs (same scenario, different seeds).
// Counts and throughput are averaged across runs. Latency percentiles use the maximum across runs
// (conservative across seeds; averaging percentiles is not statistically valid). Latency mean uses a
// request-weighted average of per-run means when successful-request counts are available.
func AggregateRunMetrics(runs []*simulationv1.RunMetrics) *simulationv1.RunMetrics {
	nonNil := 0
	for _, m := range runs {
		if m != nil {
			nonNil++
		}
	}
	if nonNil == 0 {
		return nil
	}
	if nonNil == 1 {
		for _, m := range runs {
			if m != nil {
				return m
			}
		}
	}
	n := float64(nonNil)
	out := &simulationv1.RunMetrics{}
	var tr, sr, fr int64
	var ir, intr int64
	var ifail, attFail, retry, timeout int64
	var p50, p95, p99 float64
	var meanNum, meanDen float64
	var tput float64
	var ingressTput float64
	var maxIngressErr, maxAttemptErr float64
	var qe, qd, qdrop, qred, qdlq int64
	var qdepth float64
	var tp, td, tdrop, tred, tdlq int64
	var tbd, tcl float64
	var maxQueueDepth, maxTopicBacklog, maxTopicLag float64
	var maxQueueOldestAge, maxTopicOldestAge float64
	var maxQueueDropRate, maxTopicDropRate float64
	var locHitMin, crossZoneFracMax float64
	var crossZoneReqCount, sameZoneReqCount int64
	var crossZonePenaltyTotal, sameZonePenaltyTotal, externalPenaltyTotal, topologyPenaltyTotal float64
	var crossZonePenaltyMeanMax, sameZonePenaltyMeanMax, externalPenaltyMeanMax, topologyPenaltyMeanMax float64
	firstTopo := true
	firstPerc := true
	for _, m := range runs {
		if m == nil {
			continue
		}
		tr += m.GetTotalRequests()
		sr += m.GetSuccessfulRequests()
		fr += m.GetFailedRequests()
		ir += m.GetIngressRequests()
		intr += m.GetInternalRequests()
		ifail += m.GetIngressFailedRequests()
		attFail += m.GetAttemptFailedRequests()
		retry += m.GetRetryAttempts()
		timeout += m.GetTimeoutErrors()
		if v := m.GetIngressErrorRate(); v > maxIngressErr {
			maxIngressErr = v
		}
		if v := m.GetAttemptErrorRate(); v > maxAttemptErr {
			maxAttemptErr = v
		}
		if firstPerc {
			p50 = m.GetLatencyP50Ms()
			p95 = m.GetLatencyP95Ms()
			p99 = m.GetLatencyP99Ms()
			firstPerc = false
		} else {
			p50 = maxFloat(p50, m.GetLatencyP50Ms())
			p95 = maxFloat(p95, m.GetLatencyP95Ms())
			p99 = maxFloat(p99, m.GetLatencyP99Ms())
		}
		suc := m.GetSuccessfulRequests()
		if suc > 0 {
			meanNum += m.GetLatencyMeanMs() * float64(suc)
			meanDen += float64(suc)
		}
		tput += m.GetThroughputRps()
		ingressTput += m.GetIngressThroughputRps()
		qe += m.GetQueueEnqueueCountTotal()
		qd += m.GetQueueDequeueCountTotal()
		qdrop += m.GetQueueDropCountTotal()
		qred += m.GetQueueRedeliveryCountTotal()
		qdlq += m.GetQueueDlqCountTotal()
		qdepth += m.GetQueueDepthSum()
		tp += m.GetTopicPublishCountTotal()
		td += m.GetTopicDeliverCountTotal()
		tdrop += m.GetTopicDropCountTotal()
		tred += m.GetTopicRedeliveryCountTotal()
		tdlq += m.GetTopicDlqCountTotal()
		tbd += m.GetTopicBacklogDepthSum()
		tcl += m.GetTopicConsumerLagSum()
		if v := m.GetMaxQueueDepth(); v > maxQueueDepth {
			maxQueueDepth = v
		}
		if v := m.GetMaxTopicBacklogDepth(); v > maxTopicBacklog {
			maxTopicBacklog = v
		}
		if v := m.GetMaxTopicConsumerLag(); v > maxTopicLag {
			maxTopicLag = v
		}
		if v := m.GetQueueOldestMessageAgeMs(); v > maxQueueOldestAge {
			maxQueueOldestAge = v
		}
		if v := m.GetTopicOldestMessageAgeMs(); v > maxTopicOldestAge {
			maxTopicOldestAge = v
		}
		if v := m.GetQueueDropRate(); v > maxQueueDropRate {
			maxQueueDropRate = v
		}
		if v := m.GetTopicDropRate(); v > maxTopicDropRate {
			maxTopicDropRate = v
		}
		if firstTopo {
			locHitMin = m.GetLocalityHitRate()
			crossZoneFracMax = m.GetCrossZoneRequestFraction()
			crossZonePenaltyMeanMax = m.GetCrossZoneLatencyPenaltyMsMean()
			sameZonePenaltyMeanMax = m.GetSameZoneLatencyPenaltyMsMean()
			externalPenaltyMeanMax = m.GetExternalLatencyMsMean()
			topologyPenaltyMeanMax = m.GetTopologyLatencyPenaltyMsMean()
			firstTopo = false
		} else {
			locHitMin = minFloat(locHitMin, m.GetLocalityHitRate())
			crossZoneFracMax = maxFloat(crossZoneFracMax, m.GetCrossZoneRequestFraction())
			crossZonePenaltyMeanMax = maxFloat(crossZonePenaltyMeanMax, m.GetCrossZoneLatencyPenaltyMsMean())
			sameZonePenaltyMeanMax = maxFloat(sameZonePenaltyMeanMax, m.GetSameZoneLatencyPenaltyMsMean())
			externalPenaltyMeanMax = maxFloat(externalPenaltyMeanMax, m.GetExternalLatencyMsMean())
			topologyPenaltyMeanMax = maxFloat(topologyPenaltyMeanMax, m.GetTopologyLatencyPenaltyMsMean())
		}
		crossZoneReqCount += m.GetCrossZoneRequestCountTotal()
		sameZoneReqCount += m.GetSameZoneRequestCountTotal()
		crossZonePenaltyTotal += m.GetCrossZoneLatencyPenaltyMsTotal()
		sameZonePenaltyTotal += m.GetSameZoneLatencyPenaltyMsTotal()
		externalPenaltyTotal += m.GetExternalLatencyMsTotal()
		topologyPenaltyTotal += m.GetTopologyLatencyPenaltyMsTotal()
	}
	out.TotalRequests = int64(float64(tr) / n)
	out.SuccessfulRequests = int64(float64(sr) / n)
	out.FailedRequests = int64(float64(fr) / n)
	out.IngressRequests = int64(float64(ir) / n)
	out.InternalRequests = int64(float64(intr) / n)
	out.IngressFailedRequests = int64(float64(ifail) / n)
	out.AttemptFailedRequests = int64(float64(attFail) / n)
	out.RetryAttempts = int64(float64(retry) / n)
	out.TimeoutErrors = int64(float64(timeout) / n)
	out.IngressErrorRate = maxIngressErr
	out.AttemptErrorRate = maxAttemptErr
	out.LatencyP50Ms = p50
	out.LatencyP95Ms = p95
	out.LatencyP99Ms = p99
	if meanDen > 0 {
		out.LatencyMeanMs = meanNum / meanDen
	} else {
		var ms float64
		for _, m := range runs {
			if m != nil {
				ms += m.GetLatencyMeanMs()
			}
		}
		out.LatencyMeanMs = ms / n
	}
	out.ThroughputRps = tput / n
	out.IngressThroughputRps = ingressTput / n
	out.QueueEnqueueCountTotal = int64(float64(qe) / n)
	out.QueueDequeueCountTotal = int64(float64(qd) / n)
	out.QueueDropCountTotal = int64(float64(qdrop) / n)
	out.QueueRedeliveryCountTotal = int64(float64(qred) / n)
	out.QueueDlqCountTotal = int64(float64(qdlq) / n)
	out.QueueDepthSum = qdepth / n
	out.TopicPublishCountTotal = int64(float64(tp) / n)
	out.TopicDeliverCountTotal = int64(float64(td) / n)
	out.TopicDropCountTotal = int64(float64(tdrop) / n)
	out.TopicRedeliveryCountTotal = int64(float64(tred) / n)
	out.TopicDlqCountTotal = int64(float64(tdlq) / n)
	out.TopicBacklogDepthSum = tbd / n
	out.TopicConsumerLagSum = tcl / n
	out.MaxQueueDepth = maxQueueDepth
	out.MaxTopicBacklogDepth = maxTopicBacklog
	out.MaxTopicConsumerLag = maxTopicLag
	out.QueueOldestMessageAgeMs = maxQueueOldestAge
	out.TopicOldestMessageAgeMs = maxTopicOldestAge
	out.QueueDropRate = maxQueueDropRate
	out.TopicDropRate = maxTopicDropRate
	out.LocalityHitRate = locHitMin
	out.CrossZoneRequestFraction = crossZoneFracMax
	out.CrossZoneRequestCountTotal = int64(float64(crossZoneReqCount) / n)
	out.SameZoneRequestCountTotal = int64(float64(sameZoneReqCount) / n)
	out.CrossZoneLatencyPenaltyMsTotal = crossZonePenaltyTotal / n
	out.SameZoneLatencyPenaltyMsTotal = sameZonePenaltyTotal / n
	out.ExternalLatencyMsTotal = externalPenaltyTotal / n
	out.TopologyLatencyPenaltyMsTotal = topologyPenaltyTotal / n
	out.CrossZoneLatencyPenaltyMsMean = crossZonePenaltyMeanMax
	out.SameZoneLatencyPenaltyMsMean = sameZonePenaltyMeanMax
	out.ExternalLatencyMsMean = externalPenaltyMeanMax
	out.TopologyLatencyPenaltyMsMean = topologyPenaltyMeanMax

	byName := make(map[string][]*simulationv1.ServiceMetrics)
	for _, m := range runs {
		if m == nil {
			continue
		}
		for _, sm := range m.ServiceMetrics {
			if sm == nil {
				continue
			}
			name := sm.GetServiceName()
			byName[name] = append(byName[name], sm)
		}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		list := byName[name]
		k := float64(len(list))
		var rc, ec int64
		var lp50, lp95, lp99 float64
		var lmeanNum, lmeanDen float64
		var cpu, mem float64
		var qw50, qw95, qw99, qwMeanNum, qwMeanDen float64
		var pr50, pr95, pr99, prMeanNum, prMeanDen float64
		firstL := true
		for _, sm := range list {
			rc += sm.GetRequestCount()
			ec += sm.GetErrorCount()
			if firstL {
				lp50 = sm.GetLatencyP50Ms()
				lp95 = sm.GetLatencyP95Ms()
				lp99 = sm.GetLatencyP99Ms()
				qw50 = sm.GetQueueWaitP50Ms()
				qw95 = sm.GetQueueWaitP95Ms()
				qw99 = sm.GetQueueWaitP99Ms()
				pr50 = sm.GetProcessingLatencyP50Ms()
				pr95 = sm.GetProcessingLatencyP95Ms()
				pr99 = sm.GetProcessingLatencyP99Ms()
				firstL = false
			} else {
				lp50 = maxFloat(lp50, sm.GetLatencyP50Ms())
				lp95 = maxFloat(lp95, sm.GetLatencyP95Ms())
				lp99 = maxFloat(lp99, sm.GetLatencyP99Ms())
				qw50 = maxFloat(qw50, sm.GetQueueWaitP50Ms())
				qw95 = maxFloat(qw95, sm.GetQueueWaitP95Ms())
				qw99 = maxFloat(qw99, sm.GetQueueWaitP99Ms())
				pr50 = maxFloat(pr50, sm.GetProcessingLatencyP50Ms())
				pr95 = maxFloat(pr95, sm.GetProcessingLatencyP95Ms())
				pr99 = maxFloat(pr99, sm.GetProcessingLatencyP99Ms())
			}
			w := sm.GetRequestCount() - sm.GetErrorCount()
			if w < 0 {
				w = 0
			}
			if w > 0 {
				lmeanNum += sm.GetLatencyMeanMs() * float64(w)
				lmeanDen += float64(w)
				qwMeanNum += sm.GetQueueWaitMeanMs() * float64(w)
				qwMeanDen += float64(w)
				prMeanNum += sm.GetProcessingLatencyMeanMs() * float64(w)
				prMeanDen += float64(w)
			}
			cpu += sm.GetCpuUtilization()
			mem += sm.GetMemoryUtilization()
		}
		lmean := 0.0
		if lmeanDen > 0 {
			lmean = lmeanNum / lmeanDen
		} else if k > 0 {
			for _, sm := range list {
				lmean += sm.GetLatencyMeanMs()
			}
			lmean /= k
		}
		qwMean := 0.0
		if qwMeanDen > 0 {
			qwMean = qwMeanNum / qwMeanDen
		} else if k > 0 {
			for _, sm := range list {
				qwMean += sm.GetQueueWaitMeanMs()
			}
			qwMean /= k
		}
		prMean := 0.0
		if prMeanDen > 0 {
			prMean = prMeanNum / prMeanDen
		} else if k > 0 {
			for _, sm := range list {
				prMean += sm.GetProcessingLatencyMeanMs()
			}
			prMean /= k
		}
		var ar, cr, ql int32
		for _, sm := range list {
			ar += sm.GetActiveReplicas()
			cr += sm.GetConcurrentRequests()
			ql += sm.GetQueueLength()
		}
		out.ServiceMetrics = append(out.ServiceMetrics, &simulationv1.ServiceMetrics{
			ServiceName:             name,
			RequestCount:            int64(float64(rc) / k),
			ErrorCount:              int64(float64(ec) / k),
			LatencyP50Ms:            lp50,
			LatencyP95Ms:            lp95,
			LatencyP99Ms:            lp99,
			LatencyMeanMs:           lmean,
			CpuUtilization:          cpu / k,
			MemoryUtilization:       mem / k,
			ActiveReplicas:          int32(float64(ar) / k),
			ConcurrentRequests:      int32(float64(cr) / k),
			QueueLength:             int32(float64(ql) / k),
			QueueWaitP50Ms:          qw50,
			QueueWaitP95Ms:          qw95,
			QueueWaitP99Ms:          qw99,
			QueueWaitMeanMs:         qwMean,
			ProcessingLatencyP50Ms:  pr50,
			ProcessingLatencyP95Ms:  pr95,
			ProcessingLatencyP99Ms:  pr99,
			ProcessingLatencyMeanMs: prMean,
		})
	}

	byHost := make(map[string][]*simulationv1.HostMetrics)
	for _, m := range runs {
		if m == nil {
			continue
		}
		for _, hm := range m.GetHostMetrics() {
			if hm == nil {
				continue
			}
			id := hm.GetHostId()
			if id == "" {
				continue
			}
			byHost[id] = append(byHost[id], hm)
		}
	}
	hNames := make([]string, 0, len(byHost))
	for id := range byHost {
		hNames = append(hNames, id)
	}
	sort.Strings(hNames)
	for _, id := range hNames {
		list := byHost[id]
		k := float64(len(list))
		var cpu, mem float64
		for _, hm := range list {
			cpu += hm.GetCpuUtilization()
			mem += hm.GetMemoryUtilization()
		}
		out.HostMetrics = append(out.HostMetrics, &simulationv1.HostMetrics{
			HostId:            id,
			CpuUtilization:    cpu / k,
			MemoryUtilization: mem / k,
		})
	}
	return out
}
