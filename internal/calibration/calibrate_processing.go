package calibration

import (
	"strings"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

// findEndpointRequestStats returns the predicted endpoint row for (service, path), if present.
func findEndpointRequestStats(pred *models.RunMetrics, serviceID, path string) *models.EndpointRequestStats {
	if pred == nil {
		return nil
	}
	for i := range pred.EndpointRequestStats {
		es := &pred.EndpointRequestStats[i]
		if es.ServiceName == serviceID && es.EndpointPath == path {
			return es
		}
	}
	return nil
}

// fallbackDenomFromScenarioCPU uses configured mean_cpu_ms as a last-resort denominator when
// predicted processing latency is missing. pathPattern "*" selects the max across endpoints.
func fallbackDenomFromScenarioCPU(out *config.Scenario, serviceID, pathPattern string) float64 {
	si := findServiceIndex(out, serviceID)
	if si < 0 {
		return 0
	}
	var denom float64
	for ei := range out.Services[si].Endpoints {
		ep := &out.Services[si].Endpoints[ei]
		if pathPattern != "*" && pathPattern != "" && ep.Path != pathPattern {
			continue
		}
		if ep.MeanCPUMs > denom {
			denom = ep.MeanCPUMs
		}
	}
	if denom < 1e-3 {
		return 1
	}
	return denom
}

// resolveProcessingDenom returns the predicted processing latency (ms) to use as the calibration
// ratio denominator for observed processing mean. For concrete paths it prefers
// EndpointRequestStats[*].ProcessingLatencyMeanMs; otherwise it falls back to the service rollup
// (with a warning) or scenario CPU.
func resolveProcessingDenom(pred *models.RunMetrics, eo EndpointObservation, out *config.Scenario, report *CalibrationReport) float64 {
	if pred == nil {
		return 0
	}
	epPath := strings.TrimSpace(eo.EndpointPath)
	if epPath == "" || epPath == "*" {
		if pred.ServiceMetrics == nil {
			return 0
		}
		pm := pred.ServiceMetrics[eo.ServiceID]
		if pm == nil {
			return 0
		}
		denom := pm.ProcessingLatencyMeanMs
		if denom < 1e-6 {
			denom = fallbackDenomFromScenarioCPU(out, eo.ServiceID, "*")
		}
		if denom < 1e-3 {
			return 1
		}
		return denom
	}
	if st := findEndpointRequestStats(pred, eo.ServiceID, epPath); st != nil && st.ProcessingLatencyMeanMs != nil {
		return *st.ProcessingLatencyMeanMs
	}
	if pred.ServiceMetrics != nil {
		if pm := pred.ServiceMetrics[eo.ServiceID]; pm != nil && pm.ProcessingLatencyMeanMs > 1e-6 {
			if report != nil {
				report.warnf("calibration %s:%s: using service-level predicted processing mean as baseline denominator (endpoint-specific prediction unavailable)", eo.ServiceID, epPath)
			}
			return pm.ProcessingLatencyMeanMs
		}
	}
	d := fallbackDenomFromScenarioCPU(out, eo.ServiceID, epPath)
	if d < 1e-6 {
		return 0
	}
	if report != nil {
		report.warnf("calibration %s:%s: using scenario mean_cpu_ms as predicted processing baseline denominator (endpoint and service predicted processing unavailable)", eo.ServiceID, epPath)
	}
	return d
}
