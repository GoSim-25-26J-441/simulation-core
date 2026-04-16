package calibration

import (
	"testing"
)

func TestTopicConsumerLagExplicitZeroFailsHighPred(t *testing.T) {
	obs := &ObservedMetrics{
		TopicBrokers: []TopicBrokerObservation{
			{BrokerService: "b", Topic: "t", ConsumerLag: F64(0)},
		},
	}
	agg := aggRuns{TopicLagSumMax: 5.0}
	stl, ok := sumTopicLagPresent(obs)
	if !ok {
		t.Fatal("expected topic lag present")
	}
	ch := compareOne("topic_consumer_lag_sum", stl, agg.TopicLagSumMax,
		DefaultValidationTolerances().TopicLagRel, DefaultValidationTolerances().TopicLagAbsSmall, compareHybrid)
	if ch.Pass {
		t.Fatalf("expected fail when observed explicit zero and pred non-zero: %+v", ch)
	}
}

func TestTopicConsumerLagZeroPassWhenPredZero(t *testing.T) {
	ch := compareOne("topic_consumer_lag_sum", 0, 0,
		DefaultValidationTolerances().TopicLagRel, DefaultValidationTolerances().TopicLagAbsSmall, compareHybrid)
	if !ch.Pass {
		t.Fatalf("expected pass when both zero: %+v", ch)
	}
}

func TestQueueDepthExplicitZeroFailsHighPred(t *testing.T) {
	obs := &ObservedMetrics{
		QueueBrokers: []QueueBrokerObservation{
			{BrokerService: "q", Topic: "/orders", DepthMean: F64(0)},
		},
	}
	agg := aggRuns{QueueDepthSumMax: 4.0}
	qd, ok := sumQueueDepthPresent(obs)
	if !ok {
		t.Fatal("expected queue depth present")
	}
	ch := compareOne("queue_depth_sum", qd, agg.QueueDepthSumMax,
		DefaultValidationTolerances().QueueDepthRel, DefaultValidationTolerances().QueueDepthAbsSmall, compareHybrid)
	if ch.Pass {
		t.Fatalf("expected fail when observed explicit zero queue depth and pred non-zero: %+v", ch)
	}
}

func TestTopologyLocalityExplicitZeroFailsNonZeroPrediction(t *testing.T) {
	ch := compareOne("locality_hit_rate", 0, 0.5,
		DefaultValidationTolerances().IngressErrorRateRel,
		DefaultValidationTolerances().LocalityRateAbs,
		compareErrRate)
	if ch.Pass {
		t.Fatalf("expected fail when observed locality_hit_rate is explicit zero and prediction is non-zero: %+v", ch)
	}
}

func TestTopologyCrossZoneNonZeroValidated(t *testing.T) {
	ch := compareOne("cross_zone_fraction", 0.2, 0.22,
		DefaultValidationTolerances().IngressErrorRateRel,
		DefaultValidationTolerances().CrossZoneRateAbs,
		compareErrRate)
	if !ch.Pass {
		t.Fatalf("expected pass for close non-zero cross_zone_fraction values: %+v", ch)
	}
}
