package calibration

import (
	"fmt"
	"strings"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

// CalibrateScenario returns a scenario adjusted toward ObservedMetrics using ratio-based heuristics
// when PredictedRun (baseline simulation of the same scenario being calibrated) is provided.
// Without PredictedRun, it fills zeros from observations where safe (lower confidence).
func CalibrateScenario(base *config.Scenario, obs *ObservedMetrics, opts *CalibrateOptions) (*config.Scenario, *CalibrationReport, error) {
	if base == nil {
		return nil, nil, fmt.Errorf("base scenario is nil")
	}
	if obs == nil {
		return nil, nil, fmt.Errorf("observed metrics is nil")
	}
	if opts == nil {
		opts = defaultCalibrateOptions()
	}
	out, err := cloneScenarioViaYAML(base)
	if err != nil {
		return nil, nil, fmt.Errorf("clone scenario: %w", err)
	}
	report := &CalibrationReport{}

	pred := opts.PredictedRun
	minS := opts.MinScaleFactor
	maxS := opts.MaxScaleFactor
	if minS <= 0 {
		minS = 0.25
	}
	if maxS <= 0 {
		maxS = 4.0
	}

	overwrite := opts.Overwrite
	floor := opts.ConfidenceFloor
	if floor < 0 {
		floor = 0
	}

	predIngress := predictedIngressRPS(pred, obs, out, report)

	handled := make(map[int]struct{})

	mixedTraffic := hasMixedTrafficClasses(out)

	// --- Per-target workload throughput (explicit mapping) ---
	for ti, wt := range obs.WorkloadTargets {
		if !wt.ThroughputRPS.Present {
			continue
		}
		idxs := matchingWorkloadIndices(out, wt)
		if len(idxs) == 0 {
			report.ambiguousf("workload_targets[%d]: no workload row matched To=%q traffic_class=%q source_kind=%q",
				ti, wt.To, wt.TrafficClass, wt.SourceKind)
			continue
		}
		predRate := 0.0
		for _, i := range idxs {
			predRate += out.Workload[i].Arrival.RateRPS
		}
		if predRate < 1e-9 {
			report.warnf("workload_targets[%d]: matched workloads have zero configured rate; skipped", ti)
			continue
		}
		obsRate := wt.ThroughputRPS.Value
		r := clampScale(obsRate/predRate, minS, maxS)
		for _, i := range idxs {
			if _, done := handled[i]; done {
				continue
			}
			old := out.Workload[i].Arrival.RateRPS
			if old <= 0 {
				old = obsRate / float64(len(idxs))
			}
			newR := old * r
			if !shouldApply(overwrite, fieldEmptyFloat(old), ConfidenceHigh, floor) {
				report.skipLowConf("workload[%d].arrival.rate_rps (policy/confidence)", i)
				continue
			}
			out.Workload[i].Arrival.RateRPS = newR
			report.add(fmt.Sprintf("workload[%d].arrival.rate_rps", i), old, newR,
				"scale to workload_targets throughput vs configured ingress split", ConfidenceHigh)
			handled[i] = struct{}{}
		}
	}

	// --- Global ingress rate ratio (remaining workloads) ---
	predDenomGlobal := predIngress
	if mixedTraffic && obs.Window.Source != "simulator_run_metrics" {
		sl := sumIngressLikeArrivalRPS(out)
		if sl > 1e-9 {
			predDenomGlobal = sl
			report.ambiguousf("mixed workload traffic_class: global throughput ratio uses sum of ingress-like rates only as denominator (use workload_targets to disambiguate)")
		}
	}

	if obs.Global.IngressThroughputRPS.Present && pred != nil && predDenomGlobal > 1e-9 {
		r := clampScale(obs.Global.IngressThroughputRPS.Value/predDenomGlobal, minS, maxS)
		var idxs []int
		if mixedTraffic && obs.Window.Source == "simulator_run_metrics" {
			idxs = allWorkloadIndices(out, handled)
			if len(idxs) > 1 {
				report.ambiguousf("simulator_run_metrics with mixed traffic classes: scaling all workloads by global ingress ratio to preserve golden-run compatibility")
			}
		} else {
			idxs = ingressWorkloadIndices(out, handled, report)
		}
		for _, i := range idxs {
			if _, skip := handled[i]; skip {
				continue
			}
			old := out.Workload[i].Arrival.RateRPS
			if old <= 0 {
				old = obs.Global.IngressThroughputRPS.Value / float64(max(1, len(idxs)))
			}
			newR := old * r
			if !shouldApply(overwrite, fieldEmptyFloat(old), ConfidenceHigh, floor) {
				report.skipLowConf("workload[%d].arrival.rate_rps (policy/confidence)", i)
				continue
			}
			out.Workload[i].Arrival.RateRPS = newR
			report.add(fmt.Sprintf("workload[%d].arrival.rate_rps", i), old, newR,
				"scale ingress RPS to match observed global throughput vs predicted", ConfidenceHigh)
		}
	} else if obs.Global.IngressThroughputRPS.Present {
		report.warnf("predicted ingress throughput missing; skipped global workload rate ratio")
	}

	// --- Processing latency target: scale endpoint CPU using observed vs predicted processing mean ---
	if pred != nil {
		for i := range obs.Endpoints {
			eo := &obs.Endpoints[i]
			if eo.ServiceID == "" || !eo.ProcessingLatencyMeanMs.Present {
				continue
			}
			denom := resolveProcessingDenom(pred, *eo, out, report)
			if denom < 1e-6 {
				continue
			}
			r := clampScale(eo.ProcessingLatencyMeanMs.Value/denom, minS, maxS)
			if eo.EndpointPath == "" || eo.EndpointPath == "*" {
				if pred.ServiceMetrics == nil || pred.ServiceMetrics[eo.ServiceID] == nil {
					continue
				}
				si := findServiceIndex(out, eo.ServiceID)
				if si < 0 {
					continue
				}
				for ei := range out.Services[si].Endpoints {
					ep := &out.Services[si].Endpoints[ei]
					old := ep.MeanCPUMs
					if !shouldApply(overwrite, fieldEmptyFloat(old), ConfidenceMedium, floor) {
						report.skipLowConf("%s:%s.mean_cpu_ms (service rollup)", eo.ServiceID, ep.Path)
						continue
					}
					ep.MeanCPUMs = old * r
					report.add(fmt.Sprintf("%s:%s.mean_cpu_ms (service rollup)", eo.ServiceID, ep.Path), old, ep.MeanCPUMs,
						"align processing latency using service-level processing mean", ConfidenceMedium)
				}
				continue
			}
			ep := findEndpoint(out, eo.ServiceID, eo.EndpointPath)
			if ep == nil {
				continue
			}
			old := ep.MeanCPUMs
			if !shouldApply(overwrite, fieldEmptyFloat(old), ConfidenceMedium, floor) {
				report.skipLowConf("%s:%s.mean_cpu_ms", eo.ServiceID, eo.EndpointPath)
				continue
			}
			ep.MeanCPUMs = old * r
			report.add(fmt.Sprintf("%s:%s.mean_cpu_ms", eo.ServiceID, eo.EndpointPath), old, ep.MeanCPUMs,
				"align processing latency (heuristic: CPU scales with observed processing mean)", ConfidenceMedium)
		}
	}

	// --- Net + IO residual: split using queue wait vs processing (crude) ---
	for i := range obs.Endpoints {
		eo := &obs.Endpoints[i]
		if eo.ServiceID == "" || eo.EndpointPath == "" || eo.EndpointPath == "*" {
			continue
		}
		ep := findEndpoint(out, eo.ServiceID, eo.EndpointPath)
		if ep == nil {
			continue
		}
		if !eo.QueueWaitMeanMs.Present && !eo.ProcessingLatencyMeanMs.Present {
			continue
		}
		qw := 0.0
		if eo.QueueWaitMeanMs.Present {
			qw = eo.QueueWaitMeanMs.Value
		}
		pl := 0.0
		if eo.ProcessingLatencyMeanMs.Present {
			pl = eo.ProcessingLatencyMeanMs.Value
		}
		if qw <= 0 && pl <= 0 {
			continue
		}
		if qw > pl*0.5 && qw > 0 {
			old := ep.NetLatencyMs.Mean
			if !shouldApply(overwrite, fieldEmptyFloat(old), ConfidenceLow, floor) {
				report.skipLowConf("%s:%s.net_latency_ms.mean", eo.ServiceID, eo.EndpointPath)
				continue
			}
			add := qw * 0.15
			ep.NetLatencyMs.Mean = old + add
			report.add(fmt.Sprintf("%s:%s.net_latency_ms.mean", eo.ServiceID, eo.EndpointPath), old, ep.NetLatencyMs.Mean,
				"increment net latency mean to reflect elevated queue wait (partial residual)", ConfidenceLow)
		}
	}

	// --- Failure / timeout from observed counts ---
	for i := range obs.Endpoints {
		eo := &obs.Endpoints[i]
		ep := findEndpoint(out, eo.ServiceID, eo.EndpointPath)
		if ep == nil {
			continue
		}
		if !eo.RequestCount.Present || eo.RequestCount.Value <= 10 {
			continue
		}
		if !eo.ErrorCount.Present {
			continue
		}
		fr := float64(eo.ErrorCount.Value) / float64(eo.RequestCount.Value)
		if shouldApply(overwrite, fieldEmptyFloat(ep.FailureRate), ConfidenceMedium, floor) {
			old := ep.FailureRate
			ep.FailureRate = fr
			report.add(fmt.Sprintf("%s:%s.failure_rate", eo.ServiceID, eo.EndpointPath), old, fr,
				"empirical error rate from observation window", ConfidenceMedium)
		} else {
			report.skipLowConf("%s:%s.failure_rate", eo.ServiceID, eo.EndpointPath)
		}
	}

	// --- Downstream edge hints ---
	for _, edge := range obs.DownstreamEdges {
		ep := findEndpoint(out, edge.FromService, edge.FromPath)
		if ep == nil {
			continue
		}
		for i := range ep.Downstream {
			ds := &ep.Downstream[i]
			if strings.TrimSpace(ds.To) != strings.TrimSpace(edge.To) {
				continue
			}
			if edge.ProbabilityHint.Present && shouldApply(overwrite, fieldEmptyFloat(ds.Probability), ConfidenceLow, floor) {
				old := ds.Probability
				ds.Probability = edge.ProbabilityHint.Value
				report.add(fmt.Sprintf("downstream.probability %s", edge.To), old, ds.Probability, "observed edge share", ConfidenceLow)
			} else if edge.ProbabilityHint.Present {
				report.skipLowConf("downstream.probability %s", edge.To)
			}
			if edge.CallCountMeanHint.Present && shouldApply(overwrite, fieldEmptyFloat(ds.CallCountMean), ConfidenceLow, floor) {
				old := ds.CallCountMean
				ds.CallCountMean = edge.CallCountMeanHint.Value
				report.add(fmt.Sprintf("downstream.call_count_mean %s", edge.To), old, ds.CallCountMean, "observed fan-out mean", ConfidenceLow)
			} else if edge.CallCountMeanHint.Present {
				report.skipLowConf("downstream.call_count_mean %s", edge.To)
			}
		}
	}

	// --- Queue / topic capacity hints ---
	for i := range obs.QueueBrokers {
		qb := &obs.QueueBrokers[i]
		svc := findServiceByKind(out, "queue", qb.BrokerService)
		if svc == nil || svc.Behavior == nil || svc.Behavior.Queue == nil {
			report.Skipped = append(report.Skipped, fmt.Sprintf("queue broker %s not found", qb.BrokerService))
			continue
		}
		q := svc.Behavior.Queue
		old := q.Capacity
		if qb.DepthMean.Present && qb.DepthMean.Value > 0 {
			candidate := int(qb.DepthMean.Value*2) + 10
			if candidate < 1 {
				candidate = 1
			}
			if shouldApply(overwrite, fieldEmptyInt(old), ConfidenceLow, floor) {
				q.Capacity = candidate
				report.add(fmt.Sprintf("service[%s].behavior.queue.capacity", qb.BrokerService), old, q.Capacity,
					"capacity hint from observed mean depth (2x + headroom)", ConfidenceLow)
			} else {
				report.skipLowConf("service[%s].behavior.queue.capacity", qb.BrokerService)
			}
		}
	}

	for i := range obs.TopicBrokers {
		tb := &obs.TopicBrokers[i]
		svc := findServiceByKind(out, "topic", tb.BrokerService)
		if svc == nil || svc.Behavior == nil || svc.Behavior.Topic == nil {
			continue
		}
		t := svc.Behavior.Topic
		if tb.BacklogDepth.Present && tb.BacklogDepth.Value > 0 {
			old := t.Capacity
			candidate := int(tb.BacklogDepth.Value*2) + 32
			if candidate < 1 {
				candidate = 1
			}
			if shouldApply(overwrite, fieldEmptyInt(old), ConfidenceLow, floor) {
				t.Capacity = candidate
				report.add(fmt.Sprintf("service[%s].behavior.topic.capacity", tb.BrokerService), old, t.Capacity,
					"subscriber backlog hint", ConfidenceLow)
			} else {
				report.skipLowConf("service[%s].behavior.topic.capacity", tb.BrokerService)
			}
		}
	}

	// --- Cache hit rate ---
	for _, c := range obs.Caches {
		svc := findService(out, c.ServiceID)
		if svc == nil || svc.Behavior == nil || svc.Behavior.Cache == nil {
			continue
		}
		if !c.HitCount.Present || !c.MissCount.Present {
			continue
		}
		total := c.HitCount.Value + c.MissCount.Value
		if total <= 0 {
			continue
		}
		hr := float64(c.HitCount.Value) / float64(total)
		old := svc.Behavior.Cache.HitRate
		if shouldApply(overwrite, fieldEmptyFloat(old), ConfidenceMedium, floor) {
			svc.Behavior.Cache.HitRate = hr
			report.add(fmt.Sprintf("service[%s].behavior.cache.hit_rate", c.ServiceID), old, hr, "empirical hit ratio", ConfidenceMedium)
		} else {
			report.skipLowConf("service[%s].behavior.cache.hit_rate", c.ServiceID)
		}
	}

	return out, report, nil
}

func predictedIngressRPS(pred *models.RunMetrics, obs *ObservedMetrics, out *config.Scenario, report *CalibrationReport) float64 {
	if pred == nil {
		return 0
	}
	predIngress := pred.IngressThroughputRPS
	dsec := obs.Window.Duration.Seconds()
	if predIngress < 1e-9 && dsec > 0 && pred.IngressRequests > 0 {
		predIngress = float64(pred.IngressRequests) / dsec
		if predIngress < 1e-9 {
			predIngress = float64(pred.TotalRequests) / dsec
		}
	}
	if predIngress < 1e-9 {
		for i := range out.Workload {
			wl := &out.Workload[i]
			if wl.Arrival.RateRPS > 1e-9 {
				// Sum ingress-like only for hint
				if isIngressLikeWorkload(wl.TrafficClass) {
					predIngress += wl.Arrival.RateRPS
				}
			}
		}
		if predIngress < 1e-9 {
			for i := range out.Workload {
				wl := &out.Workload[i]
				if wl.Arrival.RateRPS > 1e-9 {
					predIngress = wl.Arrival.RateRPS
					report.warnf("predicted run had no request throughput; using configured workload rate_rps=%v as baseline", predIngress)
					break
				}
			}
		}
	}
	return predIngress
}

func isIngressLikeWorkload(tc string) bool {
	switch strings.ToLower(strings.TrimSpace(tc)) {
	case "background", "replay":
		return false
	default:
		return true
	}
}

func matchingWorkloadIndices(out *config.Scenario, wt WorkloadTargetObservation) []int {
	var idx []int
	for i := range out.Workload {
		w := &out.Workload[i]
		if wt.To != "" && strings.TrimSpace(wt.To) != strings.TrimSpace(w.To) {
			continue
		}
		if wt.TrafficClass != "" && !strings.EqualFold(strings.TrimSpace(wt.TrafficClass), strings.TrimSpace(w.TrafficClass)) {
			continue
		}
		if wt.SourceKind != "" && strings.TrimSpace(wt.SourceKind) != strings.TrimSpace(w.SourceKind) {
			continue
		}
		idx = append(idx, i)
	}
	return idx
}

func ingressWorkloadIndices(out *config.Scenario, handled map[int]struct{}, report *CalibrationReport) []int {
	var ingress []int
	for i := range out.Workload {
		if _, skip := handled[i]; skip {
			continue
		}
		if isIngressLikeWorkload(out.Workload[i].TrafficClass) {
			ingress = append(ingress, i)
		}
	}
	if len(ingress) > 0 {
		if len(ingress) > 1 {
			report.ambiguousf("multiple (%d) ingress-like workload rows; applying global ingress ratio to all of them (check workload_targets for explicit mapping)", len(ingress))
		}
		return ingress
	}
	var all []int
	for i := range out.Workload {
		if _, skip := handled[i]; skip {
			continue
		}
		all = append(all, i)
	}
	if len(all) > 1 {
		report.Warnings = append(report.Warnings, "no ingress-like workload found (traffic_class may be background/replay for all rows); scaling all remaining workloads by global ingress ratio (ambiguous)")
	}
	return all
}

func allWorkloadIndices(out *config.Scenario, handled map[int]struct{}) []int {
	var all []int
	for i := range out.Workload {
		if _, skip := handled[i]; skip {
			continue
		}
		all = append(all, i)
	}
	return all
}

func hasMixedTrafficClasses(out *config.Scenario) bool {
	var ing, non bool
	for i := range out.Workload {
		if isIngressLikeWorkload(out.Workload[i].TrafficClass) {
			ing = true
		} else {
			non = true
		}
	}
	return ing && non
}

func sumIngressLikeArrivalRPS(out *config.Scenario) float64 {
	var s float64
	for i := range out.Workload {
		if isIngressLikeWorkload(out.Workload[i].TrafficClass) {
			s += out.Workload[i].Arrival.RateRPS
		}
	}
	return s
}

func findServiceIndex(sc *config.Scenario, id string) int {
	for i := range sc.Services {
		if sc.Services[i].ID == id {
			return i
		}
	}
	return -1
}

func findService(sc *config.Scenario, id string) *config.Service {
	i := findServiceIndex(sc, id)
	if i < 0 {
		return nil
	}
	return &sc.Services[i]
}

func findServiceByKind(sc *config.Scenario, kind, id string) *config.Service {
	for i := range sc.Services {
		if sc.Services[i].ID != id {
			continue
		}
		if kind != "" && !strings.EqualFold(strings.TrimSpace(sc.Services[i].Kind), strings.TrimSpace(kind)) {
			continue
		}
		return &sc.Services[i]
	}
	return nil
}

func findEndpoint(sc *config.Scenario, serviceID, path string) *config.Endpoint {
	si := findServiceIndex(sc, serviceID)
	if si < 0 {
		return nil
	}
	for i := range sc.Services[si].Endpoints {
		if sc.Services[si].Endpoints[i].Path == path {
			return &sc.Services[si].Endpoints[i]
		}
	}
	return nil
}

// cloneScenarioViaYAML deep-copies a scenario without importing internal/improvement (avoids package cycles).
func cloneScenarioViaYAML(s *config.Scenario) (*config.Scenario, error) {
	if s == nil {
		return nil, fmt.Errorf("scenario is nil")
	}
	raw, err := config.MarshalScenarioYAML(s)
	if err != nil {
		return nil, err
	}
	return config.ParseScenarioYAMLString(raw)
}
