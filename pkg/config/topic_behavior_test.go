package config

import (
	"strings"
	"testing"
)

func TestEffectiveTopicBehaviorDefaultsAndOverrides(t *testing.T) {
	def := EffectiveTopicBehavior(nil)
	if def.Partitions != 1 || def.RetentionMs != 600000 || def.Capacity != 10000 {
		t.Fatalf("unexpected defaults: %+v", def)
	}

	in := &TopicBehavior{
		Partitions:         3,
		RetentionMs:        120000,
		Capacity:           -1, // unlimited path
		DeliveryLatencyMs:  LatencySpec{Mean: 4, Sigma: 1},
		PublishAck:         " leader_ack ",
		AsyncFireAndForget: true,
		Subscribers: []TopicSubscriber{{
			Name:             "sub-a",
			ConsumerGroup:    "g-a",
			ConsumerTarget:   "svc:/work",
			ConsumerConcurrency: 2,
		}},
	}
	got := EffectiveTopicBehavior(in)
	if got.Partitions != 3 || got.RetentionMs != 120000 || got.Capacity != 0 {
		t.Fatalf("unexpected overridden topic behavior: %+v", got)
	}
	if got.DeliveryLatencyMs.Mean != 4 || got.PublishAck != "leader_ack" || !got.AsyncFireAndForget {
		t.Fatalf("expected delivery/publish/async overrides, got %+v", got)
	}

	// Ensure subscribers are copied (not aliased to input).
	got.Subscribers[0].ConsumerGroup = "changed"
	if in.Subscribers[0].ConsumerGroup != "g-a" {
		t.Fatal("expected subscribers to be deep-copied")
	}
}

func TestCanonicalTopicSubscribersForHashSortsStableByGroupThenTarget(t *testing.T) {
	topic := &TopicBehavior{
		Subscribers: []TopicSubscriber{
			{ConsumerGroup: "b", ConsumerTarget: "svc:/z"},
			{ConsumerGroup: "a", ConsumerTarget: "svc:/y"},
			{ConsumerGroup: "a", ConsumerTarget: "svc:/x"},
		},
	}
	got := CanonicalTopicSubscribersForHash(topic)
	if len(got) != 3 {
		t.Fatalf("expected 3 subscribers, got %d", len(got))
	}
	if got[0].ConsumerGroup != "a" || got[0].ConsumerTarget != "svc:/x" {
		t.Fatalf("unexpected first subscriber ordering: %+v", got[0])
	}
	if got[1].ConsumerGroup != "a" || got[1].ConsumerTarget != "svc:/y" {
		t.Fatalf("unexpected second subscriber ordering: %+v", got[1])
	}
	if got[2].ConsumerGroup != "b" {
		t.Fatalf("unexpected third subscriber ordering: %+v", got[2])
	}
}

func TestValidateTopicBehaviorSuccessAndRepresentativeErrors(t *testing.T) {
	serviceIDs := map[string]bool{"consumer": true, "dlq-svc": true}
	endpointRef := map[string]bool{"consumer:/handle": true, "dlq-svc:/dead": true}

	valid := &TopicBehavior{
		Partitions: 1,
		Subscribers: []TopicSubscriber{{
			Name:                 "sub1",
			ConsumerGroup:        "group-a",
			ConsumerTarget:       "consumer:/handle",
			ConsumerConcurrency:  2,
			MinConsumerConcurrency: 1,
			MaxConsumerConcurrency: 4,
			AckTimeoutMs:         1000,
			MaxRedeliveries:      2,
			DropPolicy:           "block",
			DLQ:                  "dlq-svc:/dead",
		}},
	}
	if err := ValidateTopicBehavior("topic-svc", valid, endpointRef, serviceIDs); err != nil {
		t.Fatalf("expected valid topic behavior, got error: %v", err)
	}

	tests := []struct {
		name string
		tb   *TopicBehavior
		want string
	}{
		{"nil behavior", nil, "behavior.topic is required"},
		{"no subscribers", &TopicBehavior{Partitions: 1}, "subscribers must be non-empty"},
		{"missing consumer target", &TopicBehavior{Partitions: 1, Subscribers: []TopicSubscriber{{ConsumerGroup: "g"}}}, "consumer_target is required"},
		{"unknown consumer service", &TopicBehavior{Partitions: 1, Subscribers: []TopicSubscriber{{ConsumerGroup: "g", ConsumerTarget: "missing:/x"}}}, "consumer_target service missing"},
		{"unknown consumer endpoint", &TopicBehavior{Partitions: 1, Subscribers: []TopicSubscriber{{ConsumerGroup: "g", ConsumerTarget: "consumer:/missing"}}}, "consumer_target endpoint consumer:/missing does not exist"},
		{"duplicate group", &TopicBehavior{Partitions: 1, Subscribers: []TopicSubscriber{{ConsumerGroup: "g", ConsumerTarget: "consumer:/handle"}, {ConsumerGroup: "g", ConsumerTarget: "consumer:/handle"}}}, "duplicate behavior.topic subscriber consumer_group"},
		{"invalid drop policy", &TopicBehavior{Partitions: 1, Subscribers: []TopicSubscriber{{ConsumerGroup: "g", ConsumerTarget: "consumer:/handle", DropPolicy: "bogus"}}}, "drop_policy must be"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTopicBehavior("topic-svc", tc.tb, endpointRef, serviceIDs)
			if err == nil {
				t.Fatalf("expected error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestValidateTopicBehaviorConcurrencyAndDLQValidation(t *testing.T) {
	serviceIDs := map[string]bool{"consumer": true, "dlq-svc": true}
	endpointRef := map[string]bool{"consumer:/handle": true, "dlq-svc:/dead": true}

	t.Run("min cannot exceed max", func(t *testing.T) {
		err := ValidateTopicBehavior("topic-svc", &TopicBehavior{
			Partitions: 1,
			Subscribers: []TopicSubscriber{{
				ConsumerGroup:        "g",
				ConsumerTarget:       "consumer:/handle",
				MinConsumerConcurrency: 5,
				MaxConsumerConcurrency: 2,
			}},
		}, endpointRef, serviceIDs)
		if err == nil || !strings.Contains(err.Error(), "min_consumer_concurrency cannot exceed max_consumer_concurrency") {
			t.Fatalf("expected min/max concurrency error, got %v", err)
		}
	})

	t.Run("invalid dlq target format", func(t *testing.T) {
		err := ValidateTopicBehavior("topic-svc", &TopicBehavior{
			Partitions: 1,
			Subscribers: []TopicSubscriber{{
				ConsumerGroup:  "g",
				ConsumerTarget: "consumer:/handle",
				DLQ:            "bad-target",
			}},
		}, endpointRef, serviceIDs)
		if err == nil || !strings.Contains(err.Error(), "topic subscriber dlq service") {
			t.Fatalf("expected dlq validation error, got %v", err)
		}
	})
}

