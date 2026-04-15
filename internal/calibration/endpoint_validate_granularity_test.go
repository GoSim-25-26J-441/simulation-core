package calibration

import (
	"strings"
	"testing"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

func TestEndpointLatencyFallbackWarnsWhenEndpointRollupHasNoLatency(t *testing.T) {
	runs := []*models.RunMetrics{
		{
			EndpointRequestStats: []models.EndpointRequestStats{
				{ServiceName: "api", EndpointPath: "/x", RequestCount: 10},
			},
			ServiceMetrics: map[string]*models.ServiceMetrics{
				"api": {
					ServiceName: "api", RequestCount: 10, LatencyP50: 5, LatencyP95: 40, LatencyP99: 50, LatencyMean: 6,
				},
			},
		},
	}
	obs := &ObservedMetrics{
		Endpoints: []EndpointObservation{
			{ServiceID: "api", EndpointPath: "/x", LatencyP95Ms: F64(40)},
		},
	}
	_, warns := validateEndpointLatencyAndQueue(obs, runs, DefaultValidationTolerances())
	joined := strings.Join(warns, " ")
	if !strings.Contains(joined, "using service-level") {
		t.Fatalf("expected service-level fallback warning, got %v", warns)
	}
}

func TestEndpointLatencyNoFallbackWarningWhenEndpointHasLatencySamples(t *testing.T) {
	p50 := 5.0
	p95 := 10.0
	runs := []*models.RunMetrics{
		{
			EndpointRequestStats: []models.EndpointRequestStats{
				{
					ServiceName: "api", EndpointPath: "/z", RequestCount: 10,
					LatencyP50Ms: &p50, LatencyP95Ms: &p95,
				},
			},
			ServiceMetrics: map[string]*models.ServiceMetrics{
				"api": {ServiceName: "api", RequestCount: 10, LatencyP95: 99},
			},
		},
	}
	obs := &ObservedMetrics{
		Endpoints: []EndpointObservation{
			{ServiceID: "api", EndpointPath: "/z", LatencyP95Ms: F64(10)},
		},
	}
	_, warns := validateEndpointLatencyAndQueue(obs, runs, DefaultValidationTolerances())
	for _, w := range warns {
		if strings.Contains(w, "/z") && strings.Contains(w, "using service-level") {
			t.Fatalf("unexpected fallback for /z: %v", warns)
		}
	}
}
