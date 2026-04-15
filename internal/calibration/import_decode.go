package calibration

import (
	"fmt"
	"strings"
)

// Observed import formats for DecodeObservedMetrics.
const (
	FormatSimulatorExport = "simulator_export"
	FormatObservedMetrics = "observed_metrics"
	FormatPrometheusJSON  = "prometheus_json"
)

// DecodeObservedMetrics unmarshals vendor-neutral observation payloads into ObservedMetrics.
// Supported formats: FormatSimulatorExport, FormatObservedMetrics, FormatPrometheusJSON.
func DecodeObservedMetrics(format string, payload []byte) (*ObservedMetrics, error) {
	switch strings.TrimSpace(strings.ToLower(format)) {
	case "", FormatSimulatorExport:
		return ObservedFromSimulatorExportJSON(payload)
	case FormatObservedMetrics:
		return ObservedFromPartialJSON(payload)
	case FormatPrometheusJSON:
		return ObservedFromPrometheusLikeJSON(payload)
	default:
		return nil, fmt.Errorf("unknown observed format %q (supported: %s, %s, %s)",
			format, FormatSimulatorExport, FormatObservedMetrics, FormatPrometheusJSON)
	}
}
