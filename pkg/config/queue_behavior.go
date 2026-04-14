package config

import (
	"fmt"
	"strings"
)

// DefaultQueueBehavior returns baseline queue semantics when behavior.queue is omitted
// but the service kind is queue (safe defaults; consumer_target must still be set explicitly).
func DefaultQueueBehavior() *QueueBehavior {
	return &QueueBehavior{
		Capacity:            10000,
		ConsumerConcurrency: 1,
		DeliveryLatencyMs:   LatencySpec{Mean: 1, Sigma: 0},
		AckTimeoutMs:        30000,
		MaxRedeliveries:     3,
		DropPolicy:          "block",
	}
}

// EffectiveQueueBehavior merges user queue config with defaults. capacity -1 means unlimited (0 in runtime).
func EffectiveQueueBehavior(q *QueueBehavior) *QueueBehavior {
	d := DefaultQueueBehavior()
	if q == nil {
		return d
	}
	out := *d
	if q.Capacity == -1 {
		out.Capacity = 0
	} else if q.Capacity > 0 {
		out.Capacity = q.Capacity
	}
	if q.ConsumerConcurrency > 0 {
		out.ConsumerConcurrency = q.ConsumerConcurrency
	}
	if strings.TrimSpace(q.ConsumerTarget) != "" {
		out.ConsumerTarget = strings.TrimSpace(q.ConsumerTarget)
	}
	if q.DeliveryLatencyMs.Mean > 0 || q.DeliveryLatencyMs.Sigma > 0 {
		out.DeliveryLatencyMs = q.DeliveryLatencyMs
	}
	if q.AckTimeoutMs > 0 {
		out.AckTimeoutMs = q.AckTimeoutMs
	}
	if q.MaxRedeliveries >= 0 {
		out.MaxRedeliveries = q.MaxRedeliveries
	}
	if strings.TrimSpace(q.DLQTarget) != "" {
		out.DLQTarget = strings.TrimSpace(q.DLQTarget)
	}
	if strings.TrimSpace(q.DropPolicy) != "" {
		out.DropPolicy = strings.TrimSpace(q.DropPolicy)
	}
	if q.AsyncFireAndForget {
		out.AsyncFireAndForget = true
	}
	return &out
}

// ValidateQueueBehavior checks queue fields; endpointRef maps "svc:path" -> true.
func ValidateQueueBehavior(svcID string, q *QueueBehavior, endpointRef map[string]bool, serviceIDs map[string]bool) error {
	if q == nil {
		return fmt.Errorf("service %s: behavior.queue is required for kind queue", svcID)
	}
	eff := EffectiveQueueBehavior(q)
	if strings.TrimSpace(eff.ConsumerTarget) == "" {
		return fmt.Errorf("service %s: behavior.queue.consumer_target is required (format serviceID:path)", svcID)
	}
	ct := strings.TrimSpace(eff.ConsumerTarget)
	cs, cp, err := parseDownstreamTargetForValidation(ct)
	if err != nil {
		return fmt.Errorf("service %s: behavior.queue.consumer_target: %w", svcID, err)
	}
	if !serviceIDs[cs] {
		return fmt.Errorf("service %s: queue consumer_target service %s does not exist", svcID, cs)
	}
	if !endpointRef[cs+":"+cp] {
		return fmt.Errorf("service %s: queue consumer_target endpoint %s:%s does not exist", svcID, cs, cp)
	}
	if q.Capacity < -1 {
		return fmt.Errorf("service %s: behavior.queue.capacity must be >= -1 (-1 = unlimited)", svcID)
	}
	if eff.ConsumerConcurrency < 0 {
		return fmt.Errorf("service %s: behavior.queue.consumer_concurrency cannot be negative", svcID)
	}
	if eff.DeliveryLatencyMs.Mean < 0 || eff.DeliveryLatencyMs.Sigma < 0 {
		return fmt.Errorf("service %s: behavior.queue.delivery_latency_ms mean/sigma cannot be negative", svcID)
	}
	if eff.AckTimeoutMs < 0 {
		return fmt.Errorf("service %s: behavior.queue.ack_timeout_ms cannot be negative", svcID)
	}
	if eff.MaxRedeliveries < 0 {
		return fmt.Errorf("service %s: behavior.queue.max_redeliveries cannot be negative", svcID)
	}
	dp := strings.ToLower(strings.TrimSpace(eff.DropPolicy))
	validDrop := map[string]bool{"block": true, "reject": true, "drop_oldest": true, "drop_newest": true}
	if !validDrop[dp] {
		return fmt.Errorf("service %s: behavior.queue.drop_policy must be block, reject, drop_oldest, or drop_newest", svcID)
	}
	if strings.TrimSpace(eff.DLQTarget) != "" {
		ds, dpth, err := parseDownstreamTargetForValidation(strings.TrimSpace(eff.DLQTarget))
		if err != nil {
			return fmt.Errorf("service %s: behavior.queue.dlq: %w", svcID, err)
		}
		if !serviceIDs[ds] {
			return fmt.Errorf("service %s: queue dlq service %s does not exist", svcID, ds)
		}
		if !endpointRef[ds+":"+dpth] {
			return fmt.Errorf("service %s: queue dlq endpoint %s:%s does not exist", svcID, ds, dpth)
		}
	}
	return nil
}
