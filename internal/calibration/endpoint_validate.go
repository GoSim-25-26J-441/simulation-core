package calibration

import (
	"fmt"
	"strings"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

// stat4 groups optional percentile fields from EndpointRequestStats (hop, queue, or processing).
type stat4 struct {
	p50, mean, p95, p99 *float64
}

type epLatencyPred struct {
	sumP50, sumMean float64
	nP50, nMean     int
	maxP95, maxP99  float64
	hasP95, hasP99  bool
}

func (a *epLatencyPred) add(s stat4) {
	if s.p50 != nil {
		a.sumP50 += *s.p50
		a.nP50++
	}
	if s.mean != nil {
		a.sumMean += *s.mean
		a.nMean++
	}
	if s.p95 != nil {
		if !a.hasP95 || *s.p95 > a.maxP95 {
			a.maxP95 = *s.p95
		}
		a.hasP95 = true
	}
	if s.p99 != nil {
		if !a.hasP99 || *s.p99 > a.maxP99 {
			a.maxP99 = *s.p99
		}
		a.hasP99 = true
	}
}

func (a epLatencyPred) meanP50() (float64, bool) {
	if a.nP50 == 0 {
		return 0, false
	}
	return a.sumP50 / float64(a.nP50), true
}

func (a epLatencyPred) meanMean() (float64, bool) {
	if a.nMean == 0 {
		return 0, false
	}
	return a.sumMean / float64(a.nMean), true
}

func (a epLatencyPred) tailP95() (float64, bool) {
	if !a.hasP95 {
		return 0, false
	}
	return a.maxP95, true
}

func (a epLatencyPred) tailP99() (float64, bool) {
	if !a.hasP99 {
		return 0, false
	}
	return a.maxP99, true
}

func foldEndpointStat(runs []*models.RunMetrics, pick func(*models.EndpointRequestStats) stat4) map[string]epLatencyPred {
	out := make(map[string]epLatencyPred)
	for _, rm := range runs {
		if rm == nil {
			continue
		}
		for i := range rm.EndpointRequestStats {
			es := &rm.EndpointRequestStats[i]
			k := endpointObservationKey(es.ServiceName, es.EndpointPath)
			a := out[k]
			a.add(pick(es))
			out[k] = a
		}
	}
	return out
}

func serviceHopStat4(runs []*models.RunMetrics, service string) epLatencyPred {
	var a epLatencyPred
	for _, rm := range runs {
		if rm == nil {
			continue
		}
		sm := rm.ServiceMetrics[service]
		if sm == nil || sm.RequestCount <= 0 {
			continue
		}
		a.add(stat4{
			p50:  &sm.LatencyP50,
			mean: &sm.LatencyMean,
			p95:  &sm.LatencyP95,
			p99:  &sm.LatencyP99,
		})
	}
	return a
}

func serviceQueueWaitStat4(runs []*models.RunMetrics, service string) epLatencyPred {
	var a epLatencyPred
	for _, rm := range runs {
		if rm == nil {
			continue
		}
		sm := rm.ServiceMetrics[service]
		if sm == nil || sm.RequestCount <= 0 {
			continue
		}
		a.add(stat4{
			p50:  &sm.QueueWaitP50Ms,
			mean: &sm.QueueWaitMeanMs,
			p95:  &sm.QueueWaitP95Ms,
			p99:  &sm.QueueWaitP99Ms,
		})
	}
	return a
}

func serviceProcStat4(runs []*models.RunMetrics, service string) epLatencyPred {
	var a epLatencyPred
	for _, rm := range runs {
		if rm == nil {
			continue
		}
		sm := rm.ServiceMetrics[service]
		if sm == nil || sm.RequestCount <= 0 {
			continue
		}
		a.add(stat4{
			p50:  &sm.ProcessingLatencyP50Ms,
			mean: &sm.ProcessingLatencyMeanMs,
			p95:  &sm.ProcessingLatencyP95Ms,
			p99:  &sm.ProcessingLatencyP99Ms,
		})
	}
	return a
}

func predictHop(
	service, path string,
	star bool,
	ep map[string]epLatencyPred,
	runs []*models.RunMetrics,
	rollup bool,
	warns *[]string,
	name string,
) epLatencyPred {
	if star {
		return serviceHopStat4(runs, service)
	}
	k := endpointObservationKey(service, path)
	if p, ok := ep[k]; ok && (p.nP50 > 0 || p.nMean > 0 || p.hasP95 || p.hasP99) {
		return p
	}
	if rollup {
		*warns = append(*warns, fmt.Sprintf("%s:%s:%s: no endpoint-level hop latency samples in predictions; using service-level aggregate", name, service, path))
	}
	return serviceHopStat4(runs, service)
}

func predictQueue(
	service, path string,
	star bool,
	ep map[string]epLatencyPred,
	runs []*models.RunMetrics,
	rollup bool,
	warns *[]string,
	name string,
) epLatencyPred {
	if star {
		return serviceQueueWaitStat4(runs, service)
	}
	k := endpointObservationKey(service, path)
	if p, ok := ep[k]; ok && (p.nP50 > 0 || p.nMean > 0 || p.hasP95 || p.hasP99) {
		return p
	}
	if rollup {
		*warns = append(*warns, fmt.Sprintf("%s:%s:%s: no endpoint-level queue wait samples in predictions; using service-level aggregate", name, service, path))
	}
	return serviceQueueWaitStat4(runs, service)
}

func predictProc(
	service, path string,
	star bool,
	ep map[string]epLatencyPred,
	runs []*models.RunMetrics,
	rollup bool,
	warns *[]string,
	name string,
) epLatencyPred {
	if star {
		return serviceProcStat4(runs, service)
	}
	k := endpointObservationKey(service, path)
	if p, ok := ep[k]; ok && (p.nP50 > 0 || p.nMean > 0 || p.hasP95 || p.hasP99) {
		return p
	}
	if rollup {
		*warns = append(*warns, fmt.Sprintf("%s:%s:%s: no endpoint-level processing latency samples in predictions; using service-level aggregate", name, service, path))
	}
	return serviceProcStat4(runs, service)
}

func appendEpCompare(
	out []MetricCheckResult,
	ov ObservedValue[float64],
	name string,
	pred float64,
	predOK bool,
	relTol float64,
) []MetricCheckResult {
	if !ov.Present {
		return out
	}
	if !predOK {
		return append(out, MetricCheckResult{
			Name: name, Observed: ov.Value, Predicted: 0, Pass: false,
			Detail: "observation present but no matching predicted aggregate (see warnings)",
		})
	}
	return append(out, compareOne(name, ov.Value, pred, relTol, 0, compareRel))
}

// validateEndpointLatencyAndQueue compares endpoint hop latency, queue wait, and processing latency
// to predictions. Aggregates across seeds: mean for P50 and mean; max for P95 and P99.
// Service-level fallbacks are warning-backed when endpoint label rollups are missing.
func validateEndpointLatencyAndQueue(obs *ObservedMetrics, runs []*models.RunMetrics, tol *ValidationTolerances) (results []MetricCheckResult, warnings []string) {
	var out []MetricCheckResult
	var warns []string
	if obs == nil || tol == nil {
		return out, warns
	}
	rollup := endpointRollupAvailable(runs)
	if !rollup {
		warns = append(warns, "endpoint latency/queue/processing: RunMetrics.EndpointRequestStats is empty; predictions use service-level aggregates only")
	}

	hop := foldEndpointStat(runs, func(es *models.EndpointRequestStats) stat4 {
		return stat4{p50: es.LatencyP50Ms, mean: es.LatencyMeanMs, p95: es.LatencyP95Ms, p99: es.LatencyP99Ms}
	})
	qw := foldEndpointStat(runs, func(es *models.EndpointRequestStats) stat4 {
		return stat4{p50: es.QueueWaitP50Ms, mean: es.QueueWaitMeanMs, p95: es.QueueWaitP95Ms, p99: es.QueueWaitP99Ms}
	})
	proc := foldEndpointStat(runs, func(es *models.EndpointRequestStats) stat4 {
		return stat4{p50: es.ProcessingLatencyP50Ms, mean: es.ProcessingLatencyMeanMs, p95: es.ProcessingLatencyP95Ms, p99: es.ProcessingLatencyP99Ms}
	})

	var usedStar bool
	for _, eo := range obs.Endpoints {
		if eo.ServiceID == "" {
			continue
		}
		path := strings.TrimSpace(eo.EndpointPath)
		star := path == "" || path == "*"
		if star {
			usedStar = true
		}

		if anyHopLatencyPresent(eo) {
			ph := predictHop(eo.ServiceID, path, star, hop, runs, rollup, &warns, "endpoint_hop_latency")
			p50, ok50 := ph.meanP50()
			pMean, okMean := ph.meanMean()
			p95, ok95 := ph.tailP95()
			p99, ok99 := ph.tailP99()

			nbase := fmt.Sprintf("endpoint_hop_latency:%s:%s", eo.ServiceID, eo.EndpointPath)
			out = appendEpCompare(out, eo.LatencyP50Ms, nbase+":p50_ms", p50, ok50, tol.LatencyP50Rel)
			out = appendEpCompare(out, eo.LatencyMeanMs, nbase+":mean_ms", pMean, okMean, tol.LatencyP50Rel)
			out = appendEpCompare(out, eo.LatencyP95Ms, nbase+":p95_ms", p95, ok95, tol.LatencyP95Rel)
			out = appendEpCompare(out, eo.LatencyP99Ms, nbase+":p99_ms", p99, ok99, tol.LatencyP99Rel)
		}

		if anyQueueWaitPresent(eo) {
			pq := predictQueue(eo.ServiceID, path, star, qw, runs, rollup, &warns, "endpoint_queue_wait")
			q50, qok50 := pq.meanP50()
			qMean, qokMean := pq.meanMean()
			q95, qok95 := pq.tailP95()
			q99, qok99 := pq.tailP99()
			qbase := fmt.Sprintf("endpoint_queue_wait:%s:%s", eo.ServiceID, eo.EndpointPath)
			out = appendEpCompare(out, eo.QueueWaitP50Ms, qbase+":p50_ms", q50, qok50, tol.LatencyP50Rel)
			out = appendEpCompare(out, eo.QueueWaitMeanMs, qbase+":mean_ms", qMean, qokMean, tol.LatencyP50Rel)
			out = appendEpCompare(out, eo.QueueWaitP95Ms, qbase+":p95_ms", q95, qok95, tol.LatencyP95Rel)
			out = appendEpCompare(out, eo.QueueWaitP99Ms, qbase+":p99_ms", q99, qok99, tol.LatencyP99Rel)
		}

		if anyProcessingLatencyPresent(eo) {
			pp := predictProc(eo.ServiceID, path, star, proc, runs, rollup, &warns, "endpoint_processing_latency")
			pr50, pok50 := pp.meanP50()
			prMean, pokMean := pp.meanMean()
			pr95, pok95 := pp.tailP95()
			pr99, pok99 := pp.tailP99()
			pibase := fmt.Sprintf("endpoint_processing_latency:%s:%s", eo.ServiceID, eo.EndpointPath)
			out = appendEpCompare(out, eo.ProcessingLatencyP50Ms, pibase+":p50_ms", pr50, pok50, tol.LatencyP50Rel)
			out = appendEpCompare(out, eo.ProcessingLatencyMeanMs, pibase+":mean_ms", prMean, pokMean, tol.LatencyP50Rel)
			out = appendEpCompare(out, eo.ProcessingLatencyP95Ms, pibase+":p95_ms", pr95, pok95, tol.LatencyP95Rel)
			out = appendEpCompare(out, eo.ProcessingLatencyP99Ms, pibase+":p99_ms", pr99, pok99, tol.LatencyP99Rel)
		}
	}
	if usedStar {
		warns = append(warns, "endpoint latency/queue/processing: at least one observation uses endpoint path \"*\" (service rollup) compared to service-level predicted aggregates")
	}
	results = out
	warnings = dedupeStrings(warns)
	return results, warnings
}

func anyHopLatencyPresent(eo EndpointObservation) bool {
	return eo.LatencyP50Ms.Present || eo.LatencyMeanMs.Present || eo.LatencyP95Ms.Present || eo.LatencyP99Ms.Present
}

func anyQueueWaitPresent(eo EndpointObservation) bool {
	return eo.QueueWaitP50Ms.Present || eo.QueueWaitMeanMs.Present || eo.QueueWaitP95Ms.Present || eo.QueueWaitP99Ms.Present
}

func anyProcessingLatencyPresent(eo EndpointObservation) bool {
	return eo.ProcessingLatencyP50Ms.Present || eo.ProcessingLatencyMeanMs.Present ||
		eo.ProcessingLatencyP95Ms.Present || eo.ProcessingLatencyP99Ms.Present
}
