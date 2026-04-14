package improvement

import simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"

// UserVisibleErrorRate is the SLO-oriented error rate: ingress_failed_requests / ingress_requests
// when ingress_requests > 0; otherwise it falls back to attempt-level failed_requests / total_requests
// for legacy or partially-labeled metrics.
func UserVisibleErrorRate(m *simulationv1.RunMetrics) float64 {
	if m == nil {
		return 0
	}
	if m.GetIngressRequests() > 0 {
		return float64(m.GetIngressFailedRequests()) / float64(m.GetIngressRequests())
	}
	if m.GetTotalRequests() > 0 {
		return float64(m.GetFailedRequests()) / float64(m.GetTotalRequests())
	}
	return 0
}
