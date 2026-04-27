package calibration

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// prometheusLikeFile is a minimal JSON shape inspired by OTL/Prometheus exposition, without pulling vendor clients.
// Example:
//
//	{
//	  "window_seconds": 120,
//	  "samples": [
//	    {"metric": "sim_ingress_throughput_rps", "value": 12.5},
//	    {"metric": "sim_root_latency_p95_ms", "value": 80},
//	    {"metric": "sim_endpoint_latency_p95_ms", "labels":{"service":"api","endpoint":"/x"}, "value": 40}
//	  ]
//	}
type prometheusLikeFile struct {
	WindowSeconds float64 `json:"window_seconds"`
	Samples       []struct {
		Metric string            `json:"metric"`
		Labels map[string]string `json:"labels"`
		Value  json.RawMessage   `json:"value"` // number or string
	} `json:"samples"`
}

// ObservedFromPrometheusLikeJSON maps documented sim_* metrics (see dispatchPrometheusSample) into ObservedMetrics.
func ObservedFromPrometheusLikeJSON(data []byte) (*ObservedMetrics, error) {
	var f prometheusLikeFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("prometheus_json: %w", err)
	}
	window := time.Minute
	if f.WindowSeconds > 0 {
		window = time.Duration(f.WindowSeconds * float64(time.Second))
	}
	out := &ObservedMetrics{
		Window: ObservationWindow{Duration: window, Source: "prometheus_like_json"},
	}
	for _, s := range f.Samples {
		v, err := parseJSONNumber(s.Value)
		if err != nil {
			return nil, fmt.Errorf("prometheus_json: sample %s: %w", s.Metric, err)
		}
		if err := dispatchPrometheusSample(s.Metric, s.Labels, v, out); err != nil {
			return nil, fmt.Errorf("prometheus_json: %w", err)
		}
	}
	return out, nil
}

func parseJSONNumber(raw json.RawMessage) (float64, error) {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return 0, fmt.Errorf("empty value")
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return 0, err
		}
		return strconv.ParseFloat(s, 64)
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, err
	}
	return f, nil
}
