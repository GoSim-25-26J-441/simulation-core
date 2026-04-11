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
	var p50, p95, p99 float64
	var meanNum, meanDen float64
	var tput float64
	firstPerc := true
	for _, m := range runs {
		if m == nil {
			continue
		}
		tr += m.GetTotalRequests()
		sr += m.GetSuccessfulRequests()
		fr += m.GetFailedRequests()
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
	}
	out.TotalRequests = int64(float64(tr) / n)
	out.SuccessfulRequests = int64(float64(sr) / n)
	out.FailedRequests = int64(float64(fr) / n)
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
		firstL := true
		for _, sm := range list {
			rc += sm.GetRequestCount()
			ec += sm.GetErrorCount()
			if firstL {
				lp50 = sm.GetLatencyP50Ms()
				lp95 = sm.GetLatencyP95Ms()
				lp99 = sm.GetLatencyP99Ms()
				firstL = false
			} else {
				lp50 = maxFloat(lp50, sm.GetLatencyP50Ms())
				lp95 = maxFloat(lp95, sm.GetLatencyP95Ms())
				lp99 = maxFloat(lp99, sm.GetLatencyP99Ms())
			}
			w := sm.GetRequestCount() - sm.GetErrorCount()
			if w < 0 {
				w = 0
			}
			if w > 0 {
				lmeanNum += sm.GetLatencyMeanMs() * float64(w)
				lmeanDen += float64(w)
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
		var ar, cr int32
		for _, sm := range list {
			ar += sm.GetActiveReplicas()
			cr += sm.GetConcurrentRequests()
		}
		out.ServiceMetrics = append(out.ServiceMetrics, &simulationv1.ServiceMetrics{
			ServiceName:        name,
			RequestCount:       int64(float64(rc) / k),
			ErrorCount:         int64(float64(ec) / k),
			LatencyP50Ms:       lp50,
			LatencyP95Ms:       lp95,
			LatencyP99Ms:       lp99,
			LatencyMeanMs:      lmean,
			CpuUtilization:     cpu / k,
			MemoryUtilization:  mem / k,
			ActiveReplicas:     int32(float64(ar) / k),
			ConcurrentRequests: int32(float64(cr) / k),
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
