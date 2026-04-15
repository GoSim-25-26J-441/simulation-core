package calibration

import (
	"strings"
	"testing"
)

func TestAggregateQueueDropRatePublishAttempts(t *testing.T) {
	obs := &ObservedMetrics{
		QueueBrokers: []QueueBrokerObservation{{
			BrokerService:            "mq",
			DropCount:                I64(4),
			QueuePublishAttemptCount: I64(4),
		}},
	}
	r, ok, w := aggregateQueueDropRateObserved(obs)
	if !ok || len(w) != 0 {
		t.Fatalf("ok=%v w=%v", ok, w)
	}
	if r < 0.99 || r > 1.01 {
		t.Fatalf("want ~1.0 got %v", r)
	}
}

func TestAggregateQueueDropRateFallbackEnqueuePlusDrop(t *testing.T) {
	obs := &ObservedMetrics{
		QueueBrokers: []QueueBrokerObservation{{
			BrokerService: "mq",
			EnqueueCount:  I64(8),
			DropCount:     I64(2),
		}},
	}
	r, ok, w := aggregateQueueDropRateObserved(obs)
	if !ok {
		t.Fatal("expected ok")
	}
	if len(w) == 0 || !stringsContainsAny(w, "approximate") {
		t.Fatalf("expected approximate warning, got %v", w)
	}
	if r < 0.19 || r > 0.21 {
		t.Fatalf("want 0.2 got %v", r)
	}
}

func TestAggregateQueueDropRateSkipNoDenominator(t *testing.T) {
	obs := &ObservedMetrics{
		QueueBrokers: []QueueBrokerObservation{{
			BrokerService: "mq",
			DropCount:     I64(3),
		}},
	}
	_, ok, w := aggregateQueueDropRateObserved(obs)
	if ok {
		t.Fatal("expected skip")
	}
	if len(w) == 0 || !stringsContainsAny(w, "skipped") {
		t.Fatalf("expected skip warning, got %v", w)
	}
}

func TestAggregateTopicDropRateDeliverPlusDrop(t *testing.T) {
	// Two consumer groups: one delivery, one drop — 1 / (1+1) = 0.5; publish must not affect.
	obs := &ObservedMetrics{
		TopicBrokers: []TopicBrokerObservation{
			{BrokerService: "evt", Topic: "/e", ConsumerGroup: "g1", DropCount: I64(0), TopicDeliverCount: I64(1), PublishCount: I64(100)},
			{BrokerService: "evt", Topic: "/e", ConsumerGroup: "g2", DropCount: I64(1), TopicDeliverCount: I64(0), PublishCount: I64(100)},
		},
	}
	r, ok, w := aggregateTopicDropRateObserved(obs)
	if !ok || len(w) != 0 {
		t.Fatalf("ok=%v w=%v", ok, w)
	}
	if r < 0.49 || r > 0.51 {
		t.Fatalf("want 0.5 got %v", r)
	}
}

func TestAggregateTopicDropRateSkipWithoutDeliver(t *testing.T) {
	obs := &ObservedMetrics{
		TopicBrokers: []TopicBrokerObservation{
			{BrokerService: "evt", Topic: "/e", DropCount: I64(1), PublishCount: I64(50)},
		},
	}
	_, ok, w := aggregateTopicDropRateObserved(obs)
	if ok {
		t.Fatal("expected skip")
	}
	if len(w) == 0 || !stringsContainsAny(w, "skipped") {
		t.Fatalf("want skip warning, got %v", w)
	}
}

func stringsContainsAny(ss []string, sub string) bool {
	for _, s := range ss {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
