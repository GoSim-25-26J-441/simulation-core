package calibration

import (
	"sort"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

func epKey(serviceName, path string) string {
	return serviceName + "\x00" + path
}

// rollup4 aggregates optional *float64 percentile fields across seeds: mean for p50 and mean;
// max for p95 and p99. Nil pointers are ignored (no contribution).
type rollup4 struct {
	sum50, sumMean float64
	n50, nMean     int
	max95, max99   float64
	ok95, ok99     bool
}

func (r *rollup4) addP50(v *float64) {
	if v != nil {
		r.sum50 += *v
		r.n50++
	}
}
func (r *rollup4) addMean(v *float64) {
	if v != nil {
		r.sumMean += *v
		r.nMean++
	}
}
func (r *rollup4) addP95(v *float64) {
	if v != nil {
		if !r.ok95 || *v > r.max95 {
			r.max95 = *v
			r.ok95 = true
		}
	}
}
func (r *rollup4) addP99(v *float64) {
	if v != nil {
		if !r.ok99 || *v > r.max99 {
			r.max99 = *v
			r.ok99 = true
		}
	}
}

func mergeRollup4(r *rollup4, p50, p95, p99, mean *float64) {
	r.addP50(p50)
	r.addP95(p95)
	r.addP99(p99)
	r.addMean(mean)
}

func (r *rollup4) finP50() *float64 {
	if r.n50 == 0 {
		return nil
	}
	v := r.sum50 / float64(r.n50)
	return &v
}
func (r *rollup4) finMean() *float64 {
	if r.nMean == 0 {
		return nil
	}
	v := r.sumMean / float64(r.nMean)
	return &v
}
func (r *rollup4) finP95() *float64 {
	if !r.ok95 {
		return nil
	}
	v := r.max95
	return &v
}
func (r *rollup4) finP99() *float64 {
	if !r.ok99 {
		return nil
	}
	v := r.max99
	return &v
}

type endpointAgg struct {
	serviceName, endpointPath string
	n                         int
	sumReq, sumErr            int64
	hop, root, qwait, proc    rollup4
}

func mergeEndpointStatsInto(dst map[string]*endpointAgg, stats []models.EndpointRequestStats) {
	for i := range stats {
		es := &stats[i]
		key := epKey(es.ServiceName, es.EndpointPath)
		a := dst[key]
		if a == nil {
			a = &endpointAgg{serviceName: es.ServiceName, endpointPath: es.EndpointPath}
			dst[key] = a
		}
		a.n++
		a.sumReq += es.RequestCount
		a.sumErr += es.ErrorCount
		mergeRollup4(&a.hop, es.LatencyP50Ms, es.LatencyP95Ms, es.LatencyP99Ms, es.LatencyMeanMs)
		mergeRollup4(&a.root, es.RootLatencyP50Ms, es.RootLatencyP95Ms, es.RootLatencyP99Ms, es.RootLatencyMeanMs)
		mergeRollup4(&a.qwait, es.QueueWaitP50Ms, es.QueueWaitP95Ms, es.QueueWaitP99Ms, es.QueueWaitMeanMs)
		mergeRollup4(&a.proc, es.ProcessingLatencyP50Ms, es.ProcessingLatencyP95Ms, es.ProcessingLatencyP99Ms, es.ProcessingLatencyMeanMs)
	}
}

func finalizeEndpointMerge(dst map[string]*endpointAgg) []models.EndpointRequestStats {
	if len(dst) == 0 {
		return nil
	}
	keys := make([]string, 0, len(dst))
	for k := range dst {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]models.EndpointRequestStats, 0, len(dst))
	for _, k := range keys {
		a := dst[k]
		if a.n == 0 {
			continue
		}
		n := int64(a.n)
		out = append(out, models.EndpointRequestStats{
			ServiceName:             a.serviceName,
			EndpointPath:            a.endpointPath,
			RequestCount:            a.sumReq / n,
			ErrorCount:              a.sumErr / n,
			LatencyP50Ms:            a.hop.finP50(),
			LatencyP95Ms:            a.hop.finP95(),
			LatencyP99Ms:            a.hop.finP99(),
			LatencyMeanMs:           a.hop.finMean(),
			RootLatencyP50Ms:        a.root.finP50(),
			RootLatencyP95Ms:        a.root.finP95(),
			RootLatencyP99Ms:        a.root.finP99(),
			RootLatencyMeanMs:       a.root.finMean(),
			QueueWaitP50Ms:          a.qwait.finP50(),
			QueueWaitP95Ms:          a.qwait.finP95(),
			QueueWaitP99Ms:          a.qwait.finP99(),
			QueueWaitMeanMs:         a.qwait.finMean(),
			ProcessingLatencyP50Ms:  a.proc.finP50(),
			ProcessingLatencyP95Ms:  a.proc.finP95(),
			ProcessingLatencyP99Ms:  a.proc.finP99(),
			ProcessingLatencyMeanMs: a.proc.finMean(),
		})
	}
	return out
}
