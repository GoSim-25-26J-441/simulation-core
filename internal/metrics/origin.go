package metrics

// Labels for request_count / error_count origin (ingress vs internal downstream hops).
const (
	LabelOrigin = "origin"

	OriginIngress    = "ingress"
	OriginDownstream = "downstream"

	LabelTrafficClass = "traffic_class"
	LabelSourceKind   = "source_kind"
	LabelReason       = "reason"
	LabelIsRetry      = "is_retry"
	LabelRetryAttempt = "attempt"
)

// Standard error reason values for request_error_count labels.
const (
	ReasonTimeout             = "timeout"
	ReasonDownstreamFailure   = "downstream_failure"
	ReasonCPUCapacity         = "cpu_capacity"
	ReasonMemoryCapacity      = "memory_capacity"
	ReasonRateLimited         = "rate_limited"
	ReasonCircuitOpen         = "circuit_open"
	ReasonNoInstance          = "no_instance"
	ReasonDrainEvicted        = "drain_evicted"
)

// EndpointLabelsWithOrigin adds an origin label to endpoint-scoped metrics.
func EndpointLabelsWithOrigin(serviceName, endpointPath, origin string) map[string]string {
	labels := CreateEndpointLabels(serviceName, endpointPath)
	if origin != "" {
		labels[LabelOrigin] = origin
	}
	return labels
}

// EndpointErrorLabels augments endpoint labels with a failure reason (and preserves origin/traffic_class/source_kind if present).
func EndpointErrorLabels(base map[string]string, reason string) map[string]string {
	out := make(map[string]string, len(base)+1)
	for k, v := range base {
		out[k] = v
	}
	if reason != "" {
		out[LabelReason] = reason
	}
	return out
}
