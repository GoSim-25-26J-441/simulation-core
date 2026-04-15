package config

import (
	"fmt"
	"sort"
	"strings"
)

// DefaultTopicBehavior returns baseline topic semantics when fields are omitted.
func DefaultTopicBehavior() *TopicBehavior {
	return &TopicBehavior{
		Partitions:        1,
		RetentionMs:       600000,
		Capacity:          10000,
		DeliveryLatencyMs: LatencySpec{Mean: 1, Sigma: 0},
		PublishAck:        "leader_ack",
	}
}

// EffectiveTopicBehavior merges user topic config with defaults.
func EffectiveTopicBehavior(t *TopicBehavior) *TopicBehavior {
	d := DefaultTopicBehavior()
	if t == nil {
		return d
	}
	out := *d
	if t.Partitions > 0 {
		out.Partitions = t.Partitions
	}
	if t.RetentionMs > 0 {
		out.RetentionMs = t.RetentionMs
	}
	if t.Capacity == -1 {
		out.Capacity = 0
	} else if t.Capacity > 0 {
		out.Capacity = t.Capacity
	}
	if t.DeliveryLatencyMs.Mean > 0 || t.DeliveryLatencyMs.Sigma > 0 {
		out.DeliveryLatencyMs = t.DeliveryLatencyMs
	}
	if strings.TrimSpace(t.PublishAck) != "" {
		out.PublishAck = strings.TrimSpace(t.PublishAck)
	}
	if t.AsyncFireAndForget {
		out.AsyncFireAndForget = true
	}
	if len(t.Subscribers) > 0 {
		out.Subscribers = append([]TopicSubscriber(nil), t.Subscribers...)
	}
	return &out
}

// ValidateTopicBehavior validates kind: topic behavior; endpointRef maps "svc:path" -> true.
func ValidateTopicBehavior(svcID string, t *TopicBehavior, endpointRef map[string]bool, serviceIDs map[string]bool) error {
	if t == nil {
		return fmt.Errorf("service %s: behavior.topic is required for kind topic", svcID)
	}
	if t.Partitions < 0 {
		return fmt.Errorf("service %s: behavior.topic.partitions cannot be negative", svcID)
	}
	if t.RetentionMs < 0 {
		return fmt.Errorf("service %s: behavior.topic.retention_ms cannot be negative", svcID)
	}
	if t.Capacity < -1 {
		return fmt.Errorf("service %s: behavior.topic.capacity must be >= -1 (-1 = unlimited)", svcID)
	}
	if t.DeliveryLatencyMs.Mean < 0 || t.DeliveryLatencyMs.Sigma < 0 {
		return fmt.Errorf("service %s: behavior.topic.delivery_latency_ms mean/sigma cannot be negative", svcID)
	}
	eff := EffectiveTopicBehavior(t)
	if len(eff.Subscribers) == 0 {
		return fmt.Errorf("service %s: behavior.topic.subscribers must be non-empty", svcID)
	}
	if eff.Partitions < 1 {
		return fmt.Errorf("service %s: behavior.topic.partitions must be >= 1", svcID)
	}

	groups := make(map[string]bool)
	names := make(map[string]bool)
	for i := range eff.Subscribers {
		sub := &eff.Subscribers[i]
		rawSub := &t.Subscribers[i]
		if strings.TrimSpace(sub.ConsumerTarget) == "" {
			return fmt.Errorf("service %s: behavior.topic.subscribers[%d].consumer_target is required", svcID, i)
		}
		cs, cp, err := parseDownstreamTargetForValidation(strings.TrimSpace(sub.ConsumerTarget))
		if err != nil {
			return fmt.Errorf("service %s: behavior.topic.subscribers[%d].consumer_target: %w", svcID, i, err)
		}
		if !serviceIDs[cs] {
			return fmt.Errorf("service %s: topic subscriber consumer_target service %s does not exist", svcID, cs)
		}
		if !endpointRef[cs+":"+cp] {
			return fmt.Errorf("service %s: topic subscriber consumer_target endpoint %s:%s does not exist", svcID, cs, cp)
		}
		g := strings.TrimSpace(sub.ConsumerGroup)
		if g == "" {
			return fmt.Errorf("service %s: behavior.topic.subscribers[%d].consumer_group is required", svcID, i)
		}
		if groups[g] {
			return fmt.Errorf("service %s: duplicate behavior.topic subscriber consumer_group %q", svcID, g)
		}
		groups[g] = true
		nm := strings.TrimSpace(sub.Name)
		if nm != "" {
			if names[nm] {
				return fmt.Errorf("service %s: duplicate behavior.topic subscriber name %q", svcID, nm)
			}
			names[nm] = true
		}
		if rawSub.ConsumerConcurrency < 0 {
			return fmt.Errorf("service %s: behavior.topic.subscribers[%d].consumer_concurrency cannot be negative", svcID, i)
		}
		if rawSub.MinConsumerConcurrency < 0 {
			return fmt.Errorf("service %s: behavior.topic.subscribers[%d].min_consumer_concurrency cannot be negative", svcID, i)
		}
		if rawSub.MaxConsumerConcurrency < 0 {
			return fmt.Errorf("service %s: behavior.topic.subscribers[%d].max_consumer_concurrency cannot be negative", svcID, i)
		}
		if rawSub.AckTimeoutMs < 0 {
			return fmt.Errorf("service %s: behavior.topic.subscribers[%d].ack_timeout_ms cannot be negative", svcID, i)
		}
		if rawSub.MaxRedeliveries < 0 {
			return fmt.Errorf("service %s: behavior.topic.subscribers[%d].max_redeliveries cannot be negative", svcID, i)
		}
		if sub.MinConsumerConcurrency > 0 && sub.MinConsumerConcurrency < 1 {
			return fmt.Errorf("service %s: behavior.topic.subscribers[%d].min_consumer_concurrency must be >= 1 when set", svcID, i)
		}
		if sub.MaxConsumerConcurrency > 0 && sub.MaxConsumerConcurrency < 1 {
			return fmt.Errorf("service %s: behavior.topic.subscribers[%d].max_consumer_concurrency must be >= 1 when set", svcID, i)
		}
		if sub.MinConsumerConcurrency > 0 && sub.MaxConsumerConcurrency > 0 && sub.MinConsumerConcurrency > sub.MaxConsumerConcurrency {
			return fmt.Errorf("service %s: behavior.topic.subscribers[%d] min_consumer_concurrency cannot exceed max_consumer_concurrency", svcID, i)
		}
		if sub.ConsumerConcurrency > 0 && sub.MinConsumerConcurrency > 0 && sub.ConsumerConcurrency < sub.MinConsumerConcurrency {
			return fmt.Errorf("service %s: behavior.topic.subscribers[%d].consumer_concurrency cannot be below min_consumer_concurrency", svcID, i)
		}
		if sub.ConsumerConcurrency > 0 && sub.MaxConsumerConcurrency > 0 && sub.ConsumerConcurrency > sub.MaxConsumerConcurrency {
			return fmt.Errorf("service %s: behavior.topic.subscribers[%d].consumer_concurrency cannot exceed max_consumer_concurrency", svcID, i)
		}
		dp := strings.ToLower(strings.TrimSpace(sub.DropPolicy))
		if dp == "" {
			dp = "block"
		}
		validDrop := map[string]bool{"block": true, "reject": true, "drop_oldest": true, "drop_newest": true}
		if !validDrop[dp] {
			return fmt.Errorf("service %s: behavior.topic.subscribers[%d].drop_policy must be block, reject, drop_oldest, or drop_newest", svcID, i)
		}
		if strings.TrimSpace(sub.DLQ) != "" {
			ds, dpth, err := parseDownstreamTargetForValidation(strings.TrimSpace(sub.DLQ))
			if err != nil {
				return fmt.Errorf("service %s: behavior.topic.subscribers[%d].dlq: %w", svcID, i, err)
			}
			if !serviceIDs[ds] {
				return fmt.Errorf("service %s: topic subscriber dlq service %s does not exist", svcID, ds)
			}
			if !endpointRef[ds+":"+dpth] {
				return fmt.Errorf("service %s: topic subscriber dlq endpoint %s:%s does not exist", svcID, ds, dpth)
			}
		}
	}
	return nil
}

// CanonicalTopicSubscribersForHash returns subscribers sorted by consumer_group for stable hashing.
func CanonicalTopicSubscribersForHash(t *TopicBehavior) []TopicSubscriber {
	if t == nil || len(t.Subscribers) == 0 {
		return nil
	}
	out := append([]TopicSubscriber(nil), t.Subscribers...)
	sort.SliceStable(out, func(i, j int) bool {
		gi := strings.TrimSpace(out[i].ConsumerGroup)
		gj := strings.TrimSpace(out[j].ConsumerGroup)
		if gi != gj {
			return gi < gj
		}
		return strings.TrimSpace(out[i].ConsumerTarget) < strings.TrimSpace(out[j].ConsumerTarget)
	})
	return out
}
