package calibration

import (
	"encoding/json"
	"fmt"
	"time"
)

// partialObservedFile uses pointer leaves so JSON omission preserves ObservedValue.Present == false.
type partialObservedFile struct {
	Window *struct {
		Duration string `json:"duration"` // e.g. "5m", "60s"
		Source   string `json:"source"`
	} `json:"window,omitempty"`
	Global *struct {
		RootLatencyP50Ms              *float64 `json:"root_latency_p50_ms,omitempty"`
		RootLatencyP95Ms              *float64 `json:"root_latency_p95_ms,omitempty"`
		RootLatencyP99Ms              *float64 `json:"root_latency_p99_ms,omitempty"`
		RootLatencyMeanMs             *float64 `json:"root_latency_mean_ms,omitempty"`
		IngressThroughputRPS          *float64 `json:"ingress_throughput_rps,omitempty"`
		IngressErrorRate              *float64 `json:"ingress_error_rate,omitempty"`
		LocalityHitRate               *float64 `json:"locality_hit_rate,omitempty"`
		CrossZoneFraction             *float64 `json:"cross_zone_fraction,omitempty"`
		CrossZoneLatencyPenaltyMeanMs *float64 `json:"cross_zone_latency_penalty_mean_ms,omitempty"`
		TopologyLatencyPenaltyMeanMs  *float64 `json:"topology_latency_penalty_mean_ms,omitempty"`
		TotalRequests                 *int64   `json:"total_requests,omitempty"`
		IngressRequests               *int64   `json:"ingress_requests,omitempty"`
		FailedRequests                *int64   `json:"failed_requests,omitempty"`
		RetryAttempts                 *int64   `json:"retry_attempts,omitempty"`
		TimeoutErrors                 *int64   `json:"timeout_errors,omitempty"`
		IngressFailedRequests         *int64   `json:"ingress_failed_requests,omitempty"`
	} `json:"global,omitempty"`
	Endpoints []struct {
		ServiceID    string `json:"service_id"`
		EndpointPath string `json:"endpoint_path"`
		// latencies
		LatencyP50Ms  *float64 `json:"latency_p50_ms,omitempty"`
		LatencyP95Ms  *float64 `json:"latency_p95_ms,omitempty"`
		LatencyP99Ms  *float64 `json:"latency_p99_ms,omitempty"`
		LatencyMeanMs *float64 `json:"latency_mean_ms,omitempty"`
		ProcP50Ms     *float64 `json:"processing_latency_p50_ms,omitempty"`
		ProcP95Ms     *float64 `json:"processing_latency_p95_ms,omitempty"`
		ProcP99Ms     *float64 `json:"processing_latency_p99_ms,omitempty"`
		ProcMeanMs    *float64 `json:"processing_latency_mean_ms,omitempty"`
		QwP50Ms       *float64 `json:"queue_wait_p50_ms,omitempty"`
		QwP95Ms       *float64 `json:"queue_wait_p95_ms,omitempty"`
		QwP99Ms       *float64 `json:"queue_wait_p99_ms,omitempty"`
		QwMeanMs      *float64 `json:"queue_wait_mean_ms,omitempty"`
		RequestCount  *int64   `json:"request_count,omitempty"`
		ErrorCount    *int64   `json:"error_count,omitempty"`
	} `json:"endpoints,omitempty"`
	TopicBrokers []struct {
		BrokerService string   `json:"broker_service"`
		Topic         string   `json:"topic"`
		Partition     int      `json:"partition"`
		ConsumerGroup string   `json:"consumer_group"`
		BacklogDepth  *float64 `json:"backlog_depth,omitempty"`
		ConsumerLag   *float64 `json:"consumer_lag,omitempty"`
	} `json:"topic_brokers,omitempty"`
	QueueBrokers []struct {
		BrokerService string   `json:"broker_service"`
		Topic         string   `json:"topic"`
		DepthMean     *float64 `json:"depth_mean,omitempty"`
	} `json:"queue_brokers,omitempty"`
	InstanceRouting []struct {
		ServiceID    string   `json:"service_id"`
		EndpointPath string   `json:"endpoint_path"`
		InstanceID   string   `json:"instance_id"`
		RequestShare *float64 `json:"request_share,omitempty"`
		RequestCount *int64   `json:"request_count,omitempty"`
	} `json:"instance_routing,omitempty"`
}

// ObservedFromPartialJSON parses documented observed_metrics JSON with pointer leaves (omit = missing, present+0 = explicit zero).
func ObservedFromPartialJSON(data []byte) (*ObservedMetrics, error) {
	var f partialObservedFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("observed_metrics: %w", err)
	}
	out := &ObservedMetrics{}
	if f.Window != nil {
		d := time.Minute
		if f.Window.Duration != "" {
			pd, err := time.ParseDuration(f.Window.Duration)
			if err != nil {
				return nil, fmt.Errorf("observed_metrics: window.duration: %w", err)
			}
			d = pd
		}
		src := f.Window.Source
		if src == "" {
			src = "observed_metrics_json"
		}
		out.Window = ObservationWindow{Duration: d, Source: src}
	}
	if f.Global != nil {
		g := f.Global
		out.Global = GlobalObservation{
			RootLatencyP50Ms:              optF64(g.RootLatencyP50Ms),
			RootLatencyP95Ms:              optF64(g.RootLatencyP95Ms),
			RootLatencyP99Ms:              optF64(g.RootLatencyP99Ms),
			RootLatencyMeanMs:             optF64(g.RootLatencyMeanMs),
			IngressThroughputRPS:          optF64(g.IngressThroughputRPS),
			IngressErrorRate:              optF64(g.IngressErrorRate),
			LocalityHitRate:               optF64(g.LocalityHitRate),
			CrossZoneFraction:             optF64(g.CrossZoneFraction),
			CrossZoneLatencyPenaltyMeanMs: optF64(g.CrossZoneLatencyPenaltyMeanMs),
			TopologyLatencyPenaltyMeanMs:  optF64(g.TopologyLatencyPenaltyMeanMs),
			TotalRequests:                 optI64(g.TotalRequests),
			IngressRequests:               optI64(g.IngressRequests),
			FailedRequests:                optI64(g.FailedRequests),
			RetryAttempts:                 optI64(g.RetryAttempts),
			TimeoutErrors:                 optI64(g.TimeoutErrors),
			IngressFailedRequests:         optI64(g.IngressFailedRequests),
		}
	}
	for _, e := range f.Endpoints {
		out.Endpoints = append(out.Endpoints, EndpointObservation{
			ServiceID:               e.ServiceID,
			EndpointPath:            e.EndpointPath,
			LatencyP50Ms:            optF64(e.LatencyP50Ms),
			LatencyP95Ms:            optF64(e.LatencyP95Ms),
			LatencyP99Ms:            optF64(e.LatencyP99Ms),
			LatencyMeanMs:           optF64(e.LatencyMeanMs),
			ProcessingLatencyP50Ms:  optF64(e.ProcP50Ms),
			ProcessingLatencyP95Ms:  optF64(e.ProcP95Ms),
			ProcessingLatencyP99Ms:  optF64(e.ProcP99Ms),
			ProcessingLatencyMeanMs: optF64(e.ProcMeanMs),
			QueueWaitP50Ms:          optF64(e.QwP50Ms),
			QueueWaitP95Ms:          optF64(e.QwP95Ms),
			QueueWaitP99Ms:          optF64(e.QwP99Ms),
			QueueWaitMeanMs:         optF64(e.QwMeanMs),
			RequestCount:            optI64(e.RequestCount),
			ErrorCount:              optI64(e.ErrorCount),
		})
	}
	for _, t := range f.TopicBrokers {
		out.TopicBrokers = append(out.TopicBrokers, TopicBrokerObservation{
			BrokerService: t.BrokerService,
			Topic:         t.Topic,
			Partition:     t.Partition,
			ConsumerGroup: t.ConsumerGroup,
			BacklogDepth:  optF64(t.BacklogDepth),
			ConsumerLag:   optF64(t.ConsumerLag),
		})
	}
	for _, q := range f.QueueBrokers {
		out.QueueBrokers = append(out.QueueBrokers, QueueBrokerObservation{
			BrokerService: q.BrokerService,
			Topic:         q.Topic,
			DepthMean:     optF64(q.DepthMean),
		})
	}
	for _, r := range f.InstanceRouting {
		out.InstanceRouting = append(out.InstanceRouting, InstanceRoutingObservation{
			ServiceID:    r.ServiceID,
			EndpointPath: r.EndpointPath,
			InstanceID:   r.InstanceID,
			RequestShare: optF64(r.RequestShare),
			RequestCount: optI64(r.RequestCount),
		})
	}
	return out, nil
}

func optF64(p *float64) ObservedValue[float64] {
	if p == nil {
		return ObservedValue[float64]{}
	}
	return F64(*p)
}

func optI64(p *int64) ObservedValue[int64] {
	if p == nil {
		return ObservedValue[int64]{}
	}
	return I64(*p)
}
