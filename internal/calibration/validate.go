package calibration

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/simd"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

// ValidationReport summarizes predicted vs observed comparisons with pass/fail per check.
type ValidationReport struct {
	Pass          bool
	Summary       string
	Checks        []MetricCheckResult
	LargestErrors []MetricCheckResult
	SeedsUsed     []int64
	PredictedNote string
	// Warnings lists skipped or approximate validations (denominator missing, service-level error fallback, etc.).
	Warnings []string
}

// MetricCheckResult is one compared quantity.
type MetricCheckResult struct {
	Name       string
	Observed   float64
	Predicted  float64
	AbsError   float64
	RelError   float64
	Pass       bool
	Detail     string
	Importance float64 // for sorting largest drivers
}

// ValidateScenario runs the scenario for each seed, aggregates predictions (mean for throughput and central latency;
// max across seeds for tail latency, broker stress), and compares to ObservedMetrics using tolerances.
// Comparisons run only for observation fields marked Present (explicit zero is validated).
// Non-fatal issues (approximate broker denominators, skipped drop-rate checks, service-level endpoint error fallback)
// are appended to ValidationReport.Warnings.
func ValidateScenario(scenario *config.Scenario, obs *ObservedMetrics, simDuration int64, opts *ValidateOptions) (*ValidationReport, error) {
	if scenario == nil {
		return nil, fmt.Errorf("scenario is nil")
	}
	if obs == nil {
		return nil, fmt.Errorf("observed metrics is nil")
	}
	if opts == nil {
		opts = defaultValidateOptions()
	}
	if simDuration <= 0 {
		simDuration = 10_000
	}
	dur := time.Duration(simDuration) * time.Millisecond
	tol := opts.Tolerances
	if tol == nil {
		tol = DefaultValidationTolerances()
	}
	seeds := opts.Seeds
	if len(seeds) == 0 {
		seeds = []int64{1}
	}

	var runs []*models.RunMetrics
	for _, seed := range seeds {
		rm, err := simd.RunScenarioForMetrics(scenario, dur, seed, opts.RealTimeWorkload)
		if err != nil {
			return nil, err
		}
		runs = append(runs, rm)
	}

	agg := aggregateRunsConservative(runs)
	svcErr := maxServiceErrorRateByService(runs)
	endpErr := maxEndpointErrorRateByEndpoint(runs)

	report := &ValidationReport{
		SeedsUsed: seeds,
		PredictedNote: "throughput, P50, root mean latency: mean across seeds; " +
			"P95/P99, ingress error, drops, topic lag/depth, DLQ counts, oldest message age, retries, timeouts: max across seeds (conservative)",
	}

	var checks []MetricCheckResult
	var valWarnings []string

	if obs.Global.IngressThroughputRPS.Present {
		checks = append(checks, compareOne("ingress_throughput_rps", obs.Global.IngressThroughputRPS.Value, agg.IngressMean,
			tol.ThroughputRel, 0, compareRel))
	}

	if obs.Global.RootLatencyP50Ms.Present {
		checks = append(checks, compareOne("root_latency_p50_ms", obs.Global.RootLatencyP50Ms.Value, agg.LatencyP50Mean, tol.LatencyP50Rel, 0, compareRel))
	}
	if obs.Global.RootLatencyP95Ms.Present {
		checks = append(checks, compareOne("root_latency_p95_ms", obs.Global.RootLatencyP95Ms.Value, agg.LatencyP95Max, tol.LatencyP95Rel, 0, compareRel))
	}
	if obs.Global.RootLatencyP99Ms.Present {
		checks = append(checks, compareOne("root_latency_p99_ms", obs.Global.RootLatencyP99Ms.Value, agg.LatencyP99Max, tol.LatencyP99Rel, 0, compareRel))
	}
	if obs.Global.RootLatencyMeanMs.Present {
		checks = append(checks, compareOne("root_latency_mean_ms", obs.Global.RootLatencyMeanMs.Value, agg.LatencyMeanMean, tol.LatencyP50Rel, 0, compareRel))
	}

	if obs.Global.IngressErrorRate.Present {
		checks = append(checks, compareOne("ingress_error_rate", obs.Global.IngressErrorRate.Value, agg.IngressErrMax,
			tol.IngressErrorRateRel, tol.IngressErrorRateAbs, compareErrRate))
	}
	if obs.Global.LocalityHitRate.Present {
		checks = append(checks, compareOne("locality_hit_rate", obs.Global.LocalityHitRate.Value, agg.LocalityHitRateMean, tol.IngressErrorRateRel, tol.LocalityRateAbs, compareErrRate))
	}
	if obs.Global.CrossZoneFraction.Present {
		checks = append(checks, compareOne("cross_zone_fraction", obs.Global.CrossZoneFraction.Value, agg.CrossZoneFractionMean, tol.IngressErrorRateRel, tol.CrossZoneRateAbs, compareErrRate))
	}
	if obs.Global.CrossZoneLatencyPenaltyMeanMs.Present {
		checks = append(checks, compareOne("cross_zone_latency_penalty_mean_ms", obs.Global.CrossZoneLatencyPenaltyMeanMs.Value, agg.CrossZoneLatencyPenaltyMeanMean,
			tol.LatencyP50Rel, tol.CrossZonePenaltyMeanAbs, compareHybrid))
	}
	if obs.Global.TopologyLatencyPenaltyMeanMs.Present {
		checks = append(checks, compareOne("topology_latency_penalty_mean_ms", obs.Global.TopologyLatencyPenaltyMeanMs.Value, agg.TopologyLatencyPenaltyMeanMean,
			tol.LatencyP50Rel, tol.TopologyPenaltyMeanAbs, compareHybrid))
	}

	if v, ok := maxPresentServiceUtil(obs.Services, utilCPU); ok {
		checks = append(checks, compareOne("max_service_cpu_util", v, agg.MaxServiceCPU, 0, tol.UtilizationAbsPP, compareAbsPP))
	}
	if v, ok := maxPresentServiceUtil(obs.Services, utilMem); ok {
		checks = append(checks, compareOne("max_service_memory_util", v, agg.MaxServiceMem, 0, tol.UtilizationAbsPP, compareAbsPP))
	}

	if mq, ok := meanQueueWaitPresent(obs); ok {
		checks = append(checks, compareOne("queue_wait_mean_ms", mq, agg.QueueWaitMeanMax, tol.LatencyP50Rel, 0, compareRel))
	}

	if sqd, ok := sumQueueDepthPresent(obs); ok {
		checks = append(checks, compareOne("queue_depth_sum_proxy", sqd, agg.QueueDepthSumMax, tol.QueueDepthRel, tol.QueueDepthAbsSmall, compareHybrid))
	}
	if stb, ok := sumTopicBacklogPresent(obs); ok {
		checks = append(checks, compareOne("topic_backlog_depth_sum_proxy", stb, agg.TopicBacklogSumMax, tol.TopicLagRel, tol.TopicLagAbsSmall, compareHybrid))
	}
	if stl, ok := sumTopicLagPresent(obs); ok {
		checks = append(checks, compareOne("topic_consumer_lag_sum", stl, agg.TopicLagSumMax, tol.TopicLagRel, tol.TopicLagAbsSmall, compareHybrid))
	}

	if rate, ok, w := aggregateQueueDropRateObserved(obs); ok {
		checks = append(checks, compareOne("queue_drop_rate", rate, agg.QueueDropRateMax, tol.IngressErrorRateRel, tol.QueueDropRateAbs, compareErrRate))
		valWarnings = append(valWarnings, w...)
	} else {
		valWarnings = append(valWarnings, w...)
	}
	if rate, ok, w := aggregateTopicDropRateObserved(obs); ok {
		checks = append(checks, compareOne("topic_drop_rate", rate, agg.TopicDropRateMax, tol.IngressErrorRateRel, tol.TopicDropRateAbs, compareErrRate))
		valWarnings = append(valWarnings, w...)
	} else {
		valWarnings = append(valWarnings, w...)
	}

	if qdlq, ok := sumPresentIntQueue(obs.QueueBrokers, func(q QueueBrokerObservation) ObservedValue[int64] {
		return q.DLQCount
	}); ok {
		checks = append(checks, compareOne("queue_dlq_count_total", float64(qdlq), float64(agg.QueueDlqMax), tol.TopicLagRel, maxFloat(5.0, tol.QueueDepthAbsSmall), compareHybrid))
	}
	if tdlq, ok := sumPresentIntTopic(obs.TopicBrokers, func(t TopicBrokerObservation) ObservedValue[int64] {
		return t.DLQCount
	}); ok {
		checks = append(checks, compareOne("topic_dlq_count_total", float64(tdlq), float64(agg.TopicDlqMax), tol.TopicLagRel, maxFloat(5.0, tol.QueueDepthAbsSmall), compareHybrid))
	}

	if qa, ok := maxPresentFloatQueue(obs.QueueBrokers, func(q QueueBrokerObservation) ObservedValue[float64] {
		return q.OldestAgeMs
	}); ok {
		checks = append(checks, compareOne("queue_oldest_message_age_ms", qa, agg.QueueOldestAgeMax, tol.TopicLagRel, tol.TopicLagAbsSmall, compareHybrid))
	}
	if ta, ok := maxPresentFloatTopicOldest(obs.TopicBrokers); ok {
		checks = append(checks, compareOne("topic_oldest_message_age_ms", ta, agg.TopicOldestAgeMax, tol.TopicLagRel, tol.TopicLagAbsSmall, compareHybrid))
	}

	if obs.Global.RetryAttempts.Present {
		checks = append(checks, compareOne("retry_attempts", float64(obs.Global.RetryAttempts.Value), float64(agg.RetryMax), tol.TopicLagRel, maxFloat(10.0, tol.TopicLagAbsSmall), compareHybrid))
	}
	if obs.Global.TimeoutErrors.Present {
		checks = append(checks, compareOne("timeout_errors", float64(obs.Global.TimeoutErrors.Value), float64(agg.TimeoutMax), tol.TopicLagRel, maxFloat(5.0, tol.TopicLagAbsSmall), compareHybrid))
	}

	echecks, ewarn := validateEndpointErrorRates(obs, runs, svcErr, endpErr, tol)
	checks = append(checks, echecks...)
	valWarnings = append(valWarnings, ewarn...)

	lchecks, lwarn := validateEndpointLatencyAndQueue(obs, runs, tol)
	checks = append(checks, lchecks...)
	valWarnings = append(valWarnings, lwarn...)

	rchecks, rwarn := validateInstanceRoutingSkew(obs, runs, tol)
	checks = append(checks, rchecks...)
	valWarnings = append(valWarnings, rwarn...)

	passAll := true
	for _, ch := range checks {
		if !ch.Pass {
			passAll = false
			break
		}
	}
	report.Pass = passAll
	report.Checks = checks
	report.LargestErrors = largestDrivers(checks, 5)
	report.Warnings = dedupeStrings(valWarnings)
	if report.Pass {
		report.Summary = "Validation passed: predicted metrics within configured tolerances (conservative tail aggregation)."
	} else {
		report.Summary = "Validation failed: one or more metrics exceed tolerances. See checks and largest_errors."
	}
	return report, nil
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

type aggRuns struct {
	IngressMean        float64
	LatencyP50Mean     float64
	LatencyMeanMean    float64
	LatencyP95Max      float64
	LatencyP99Max      float64
	IngressErrMax      float64
	MaxServiceCPU      float64
	MaxServiceMem      float64
	QueueWaitMeanMax   float64
	QueueDepthSumMax   float64
	QueueDropRateMax   float64
	TopicDropRateMax   float64
	TopicBacklogSumMax float64
	TopicLagSumMax     float64
	QueueDlqMax        int64
	TopicDlqMax        int64
	QueueOldestAgeMax  float64
	TopicOldestAgeMax  float64
	RetryMax           int64
	TimeoutMax         int64
	LocalityHitRateMean   float64
	CrossZoneFractionMean float64
	CrossZoneLatencyPenaltyMeanMean float64
	TopologyLatencyPenaltyMeanMean  float64
}

func aggregateRunsConservative(runs []*models.RunMetrics) aggRuns {
	if len(runs) == 0 {
		return aggRuns{}
	}
	var sumIngress, sumP50, sumMean float64
	var maxP95, maxP99, maxErr float64
	var maxQD, maxTB, maxTL, maxQW float64
	var maxDrop, maxTDrop float64
	var maxCPU, maxMem float64
	var maxQDlq, maxTDlq int64
	var maxQAge, maxTAge float64
	var maxRetry, maxTimeout int64
	var sumLocalityHit, sumCrossZoneFrac, sumCrossZonePenMean, sumTopoPenMean float64
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
		if rm.IngressErrorRate > maxErr {
			maxErr = rm.IngressErrorRate
		}
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
		sumLocalityHit += rm.LocalityHitRate
		sumCrossZoneFrac += rm.CrossZoneRequestFraction
		sumCrossZonePenMean += rm.CrossZoneLatencyPenaltyMsMean
		sumTopoPenMean += rm.TopologyLatencyPenaltyMsMean
		for _, sm := range rm.ServiceMetrics {
			if sm == nil {
				continue
			}
			if sm.QueueWaitMeanMs > maxQW {
				maxQW = sm.QueueWaitMeanMs
			}
			if sm.CPUUtilization > maxCPU {
				maxCPU = sm.CPUUtilization
			}
			if sm.MemoryUtilization > maxMem {
				maxMem = sm.MemoryUtilization
			}
		}
	}
	n := float64(len(runs))
	return aggRuns{
		IngressMean:        sumIngress / n,
		LatencyP50Mean:     sumP50 / n,
		LatencyMeanMean:    sumMean / n,
		LatencyP95Max:      maxP95,
		LatencyP99Max:      maxP99,
		IngressErrMax:      maxErr,
		MaxServiceCPU:      maxCPU,
		MaxServiceMem:      maxMem,
		QueueWaitMeanMax:   maxQW,
		QueueDepthSumMax:   maxQD,
		QueueDropRateMax:   maxDrop,
		TopicDropRateMax:   maxTDrop,
		TopicBacklogSumMax: maxTB,
		TopicLagSumMax:     maxTL,
		QueueDlqMax:        maxQDlq,
		TopicDlqMax:        maxTDlq,
		QueueOldestAgeMax:  maxQAge,
		TopicOldestAgeMax:  maxTAge,
		RetryMax:           maxRetry,
		TimeoutMax:         maxTimeout,
		LocalityHitRateMean:   sumLocalityHit / n,
		CrossZoneFractionMean: sumCrossZoneFrac / n,
		CrossZoneLatencyPenaltyMeanMean: sumCrossZonePenMean / n,
		TopologyLatencyPenaltyMeanMean:  sumTopoPenMean / n,
	}
}

func maxServiceErrorRateByService(runs []*models.RunMetrics) map[string]float64 {
	out := make(map[string]float64)
	for _, rm := range runs {
		if rm == nil {
			continue
		}
		for name, sm := range rm.ServiceMetrics {
			if sm == nil || sm.RequestCount <= 0 {
				continue
			}
			r := float64(sm.ErrorCount) / float64(sm.RequestCount)
			if prev, ok := out[name]; !ok || r > prev {
				out[name] = r
			}
		}
	}
	return out
}

func endpointObservationKey(serviceID, path string) string {
	return serviceID + ":" + path
}

func maxEndpointErrorRateByEndpoint(runs []*models.RunMetrics) map[string]float64 {
	out := make(map[string]float64)
	for _, rm := range runs {
		if rm == nil {
			continue
		}
		for i := range rm.EndpointRequestStats {
			es := &rm.EndpointRequestStats[i]
			if es.RequestCount <= 0 {
				continue
			}
			r := float64(es.ErrorCount) / float64(es.RequestCount)
			k := endpointObservationKey(es.ServiceName, es.EndpointPath)
			if prev, ok := out[k]; !ok || r > prev {
				out[k] = r
			}
		}
	}
	return out
}

func endpointRollupAvailable(runs []*models.RunMetrics) bool {
	for _, rm := range runs {
		if rm != nil && len(rm.EndpointRequestStats) > 0 {
			return true
		}
	}
	return false
}

func validateEndpointErrorRates(obs *ObservedMetrics, runs []*models.RunMetrics, svcErr, endpErr map[string]float64, tol *ValidationTolerances) ([]MetricCheckResult, []string) {
	var out []MetricCheckResult
	var warns []string
	if obs == nil {
		return out, warns
	}
	rollup := endpointRollupAvailable(runs)
	if !rollup {
		warns = append(warns, "endpoint error rates: RunMetrics.EndpointRequestStats is empty; predictions use service-level max error rate only")
	}
	var usedStarPath bool
	for _, eo := range obs.Endpoints {
		if eo.ServiceID == "" {
			continue
		}
		if !eo.RequestCount.Present || !eo.ErrorCount.Present {
			continue
		}
		if eo.RequestCount.Value <= 0 {
			continue
		}
		obsRate := float64(eo.ErrorCount.Value) / float64(eo.RequestCount.Value)
		name := fmt.Sprintf("endpoint_error_rate:%s:%s", eo.ServiceID, eo.EndpointPath)

		path := strings.TrimSpace(eo.EndpointPath)
		var pred float64
		switch path {
		case "", "*":
			pred = svcErr[eo.ServiceID]
			usedStarPath = true
		default:
			k := endpointObservationKey(eo.ServiceID, path)
			if p, ok := endpErr[k]; ok && rollup {
				pred = p
			} else {
				pred = svcErr[eo.ServiceID]
				warns = append(warns, fmt.Sprintf("%s: no matching endpoint-level prediction; using service-level max rate", name))
			}
		}
		out = append(out, compareOne(name, obsRate, pred, tol.IngressErrorRateRel, tol.IngressErrorRateAbs, compareErrRate))
	}
	if usedStarPath {
		warns = append(warns, "endpoint error rates: at least one observation uses endpoint path \"*\" (service rollup) compared to service-level max predicted error rate")
	}
	return out, dedupeStrings(warns)
}

func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func maxPresentServiceUtil(svcs []ServiceObservation, pick int) (float64, bool) {
	var m float64
	var any bool
	for _, s := range svcs {
		var ov ObservedValue[float64]
		switch pick {
		case utilCPU:
			ov = s.CPUUtilization
		case utilMem:
			ov = s.MemoryUtilization
		}
		if !ov.Present {
			continue
		}
		any = true
		if ov.Value > m {
			m = ov.Value
		}
	}
	return m, any
}

const (
	utilCPU = iota
	utilMem
)

func meanQueueWaitPresent(obs *ObservedMetrics) (float64, bool) {
	if obs == nil {
		return 0, false
	}
	var sum float64
	var n float64
	for _, e := range obs.Endpoints {
		if !e.QueueWaitMeanMs.Present {
			continue
		}
		sum += e.QueueWaitMeanMs.Value
		n++
	}
	if n == 0 {
		return 0, false
	}
	return sum / n, true
}

func sumQueueDepthPresent(obs *ObservedMetrics) (float64, bool) {
	if obs == nil {
		return 0, false
	}
	var s float64
	var any bool
	for _, q := range obs.QueueBrokers {
		if !q.DepthMean.Present {
			continue
		}
		any = true
		s += q.DepthMean.Value
	}
	return s, any
}

func sumTopicBacklogPresent(obs *ObservedMetrics) (float64, bool) {
	if obs == nil {
		return 0, false
	}
	var s float64
	var any bool
	for _, t := range obs.TopicBrokers {
		if !t.BacklogDepth.Present {
			continue
		}
		any = true
		s += t.BacklogDepth.Value
	}
	return s, any
}

func sumTopicLagPresent(obs *ObservedMetrics) (float64, bool) {
	if obs == nil {
		return 0, false
	}
	var s float64
	var any bool
	for _, t := range obs.TopicBrokers {
		if !t.ConsumerLag.Present {
			continue
		}
		any = true
		s += t.ConsumerLag.Value
	}
	return s, any
}

// aggregateQueueDropRateObserved matches RunMetrics: drops / queue_publish_attempt_count.
// When publish attempts are absent but enqueue+drop are present, uses enqueue+drop as an approximate attempt count.
func aggregateQueueDropRateObserved(obs *ObservedMetrics) (rate float64, ok bool, warnings []string) {
	if obs == nil || len(obs.QueueBrokers) == 0 {
		return 0, false, nil
	}
	var dropSum, attemptSum int64
	approx := false
	sawDrop := false
	for _, q := range obs.QueueBrokers {
		if !q.DropCount.Present {
			continue
		}
		sawDrop = true
		d := q.DropCount.Value
		dropSum += d
		switch {
		case q.QueuePublishAttemptCount.Present:
			attemptSum += q.QueuePublishAttemptCount.Value
		case q.EnqueueCount.Present:
			attemptSum += q.EnqueueCount.Value + d
			approx = true
		default:
			return 0, false, []string{"queue_drop_rate: skipped — drop_count present without queue_publish_attempt_count or enqueue_count to infer attempts (matches RunMetrics denominator)"}
		}
	}
	if !sawDrop {
		return 0, false, nil
	}
	if attemptSum <= 0 {
		return 0, false, []string{"queue_drop_rate: skipped — inferred publish attempts sum to zero"}
	}
	if approx {
		warnings = append(warnings, "queue_drop_rate: approximate denominator (enqueue_count + drop_count) used where queue_publish_attempt_count was absent")
	}
	return float64(dropSum) / float64(attemptSum), true, warnings
}

// aggregateTopicDropRateObserved matches RunMetrics: topic_drop_count / (topic_deliver_count + topic_drop_count).
// PublishCount is not used as a denominator.
func aggregateTopicDropRateObserved(obs *ObservedMetrics) (rate float64, ok bool, warnings []string) {
	if obs == nil || len(obs.TopicBrokers) == 0 {
		return 0, false, nil
	}
	var dropSum, attemptSum int64
	sawDrop := false
	for _, t := range obs.TopicBrokers {
		if !t.DropCount.Present {
			continue
		}
		sawDrop = true
		d := t.DropCount.Value
		if !t.TopicDeliverCount.Present {
			return 0, false, []string{"topic_drop_rate: skipped — drop_count present without topic_deliver_count (denominator must be deliver + drop, not publish)"}
		}
		del := t.TopicDeliverCount.Value
		dropSum += d
		attemptSum += del + d
	}
	if !sawDrop {
		return 0, false, nil
	}
	if attemptSum <= 0 {
		return 0, false, []string{"topic_drop_rate: skipped — deliver+drop attempt sum is zero"}
	}
	return float64(dropSum) / float64(attemptSum), true, warnings
}

func sumPresentIntQueue(rows []QueueBrokerObservation, pick func(QueueBrokerObservation) ObservedValue[int64]) (int64, bool) {
	var s int64
	var any bool
	for _, q := range rows {
		ov := pick(q)
		if !ov.Present {
			continue
		}
		any = true
		s += ov.Value
	}
	return s, any
}

func sumPresentIntTopic(rows []TopicBrokerObservation, pick func(TopicBrokerObservation) ObservedValue[int64]) (int64, bool) {
	var s int64
	var any bool
	for _, t := range rows {
		ov := pick(t)
		if !ov.Present {
			continue
		}
		any = true
		s += ov.Value
	}
	return s, any
}

func maxPresentFloatQueue(rows []QueueBrokerObservation, pick func(QueueBrokerObservation) ObservedValue[float64]) (float64, bool) {
	var m float64
	var any bool
	for _, q := range rows {
		ov := pick(q)
		if !ov.Present {
			continue
		}
		if !any || ov.Value > m {
			m = ov.Value
		}
		any = true
	}
	return m, any
}

func maxPresentFloatTopicOldest(rows []TopicBrokerObservation) (float64, bool) {
	var m float64
	var any bool
	for _, t := range rows {
		if !t.OldestAgeMs.Present {
			continue
		}
		if !any || t.OldestAgeMs.Value > m {
			m = t.OldestAgeMs.Value
		}
		any = true
	}
	return m, any
}

type routeSkewAgg struct {
	countByInstance map[string]float64
	total           float64
}

func validateInstanceRoutingSkew(obs *ObservedMetrics, runs []*models.RunMetrics, tol *ValidationTolerances) ([]MetricCheckResult, []string) {
	if obs == nil || len(obs.InstanceRouting) == 0 {
		return nil, nil
	}
	var checks []MetricCheckResult
	var warnings []string
	byRoute := aggregatePredictedRouteDistributions(runs)
	for _, r := range obs.InstanceRouting {
		if strings.TrimSpace(r.ServiceID) == "" || strings.TrimSpace(r.EndpointPath) == "" || strings.TrimSpace(r.InstanceID) == "" {
			warnings = append(warnings, "instance_routing row skipped: service_id, endpoint_path, and instance_id must be non-empty")
			continue
		}
		k := r.ServiceID + "|" + r.EndpointPath
		agg, ok := byRoute[k]
		if !ok || agg.total <= 0 {
			warnings = append(warnings, "instance_routing skipped for "+r.ServiceID+":"+r.EndpointPath+": no predicted route_selection_count samples available")
			continue
		}
		predCount := agg.countByInstance[r.InstanceID]
		if r.RequestShare.Present {
			predShare := predCount / agg.total
			checks = append(checks, compareOne(
				"instance_routing_share:"+r.ServiceID+":"+r.EndpointPath+":"+r.InstanceID,
				r.RequestShare.Value,
				predShare,
				tol.RouteShareRel,
				tol.RouteShareAbsSmall,
				compareHybrid,
			))
		}
		if r.RequestCount.Present {
			checks = append(checks, compareOne(
				"instance_routing_count:"+r.ServiceID+":"+r.EndpointPath+":"+r.InstanceID,
				float64(r.RequestCount.Value),
				predCount,
				tol.RouteCountRel,
				tol.RouteCountAbsSmall,
				compareHybrid,
			))
		}
		if !r.RequestShare.Present && !r.RequestCount.Present {
			warnings = append(warnings, "instance_routing row ignored for "+r.ServiceID+":"+r.EndpointPath+":"+r.InstanceID+": neither request_share nor request_count was present")
		}
	}
	return checks, warnings
}

func aggregatePredictedRouteDistributions(runs []*models.RunMetrics) map[string]routeSkewAgg {
	// key: "service|endpoint"
	byRoute := map[string]routeSkewAgg{}
	if len(runs) == 0 {
		return byRoute
	}
	for _, rm := range runs {
		if rm == nil || len(rm.InstanceRouteStats) == 0 {
			continue
		}
		for _, st := range rm.InstanceRouteStats {
			if strings.TrimSpace(st.ServiceName) == "" || strings.TrimSpace(st.EndpointPath) == "" || strings.TrimSpace(st.InstanceID) == "" {
				continue
			}
			k := st.ServiceName + "|" + st.EndpointPath
			agg, ok := byRoute[k]
			if !ok {
				agg = routeSkewAgg{countByInstance: map[string]float64{}}
			}
			n := float64(st.SelectionCount)
			agg.countByInstance[st.InstanceID] += n
			agg.total += n
			byRoute[k] = agg
		}
	}
	// Mean across seeds for skew prediction.
	nRuns := float64(len(runs))
	if nRuns <= 1 {
		return byRoute
	}
	for k, agg := range byRoute {
		for inst, c := range agg.countByInstance {
			agg.countByInstance[inst] = c / nRuns
		}
		agg.total = agg.total / nRuns
		byRoute[k] = agg
	}
	return byRoute
}

type compareMode int

const (
	compareRel compareMode = iota
	compareAbsPP
	compareErrRate
	compareHybrid
	compareAbsOnly
)

func compareOne(name string, obs, pred float64, relTol, absTol float64, mode compareMode) MetricCheckResult {
	ch := MetricCheckResult{Name: name, Observed: obs, Predicted: pred}
	ch.AbsError = math.Abs(pred - obs)
	if math.Abs(obs) > 1e-9 {
		ch.RelError = ch.AbsError / math.Abs(obs)
	}
	ch.Importance = ch.AbsError
	switch mode {
	case compareRel:
		if math.Abs(obs) < 1e-12 && math.Abs(pred) < 1e-9 {
			ch.Pass = true
		} else {
			ch.Pass = withinRel(obs, pred, relTol)
		}
		ch.Detail = "relative tolerance"
	case compareAbsPP:
		ch.Pass = math.Abs(pred-obs) <= absTol
		ch.Detail = "absolute percentage-point tolerance"
	case compareErrRate:
		passR := obs == 0 && pred == 0
		if !passR {
			passR = math.Abs(pred-obs) <= absTol || withinRel(obs, pred, relTol)
		}
		ch.Pass = passR
		ch.Detail = "error rate absolute or relative"
	case compareHybrid:
		ch.Pass = withinAbsRatio(obs, pred, absTol, relTol)
		ch.Detail = "small-absolute or relative"
	case compareAbsOnly:
		ch.Pass = math.Abs(pred-obs) <= relTol
		ch.Detail = "absolute"
	}
	return ch
}

func largestDrivers(cs []MetricCheckResult, n int) []MetricCheckResult {
	out := make([]MetricCheckResult, len(cs))
	copy(out, cs)
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Importance > out[i].Importance {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	if len(out) > n {
		out = out[:n]
	}
	return out
}
