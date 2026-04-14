package improvement

import (
	"testing"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

func TestUserVisibleErrorRateIngressPreferred(t *testing.T) {
	m := &simulationv1.RunMetrics{
		IngressRequests:       100,
		IngressFailedRequests: 3,
		FailedRequests:        99,
		TotalRequests:         1000,
	}
	if got := UserVisibleErrorRate(m); got != 0.03 {
		t.Fatalf("want 0.03 got %v", got)
	}
}

func TestUserVisibleErrorRateLegacyFallback(t *testing.T) {
	m := &simulationv1.RunMetrics{
		FailedRequests: 2,
		TotalRequests:  10,
	}
	if got := UserVisibleErrorRate(m); got != 0.2 {
		t.Fatalf("want 0.2 got %v", got)
	}
}
