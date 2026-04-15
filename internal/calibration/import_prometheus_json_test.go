package calibration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestObservedFromPrometheusLikeJSON_ExpandedMetricsMatrix(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "prometheus_expanded_matrix.json"))
	if err != nil {
		t.Fatal(err)
	}
	obs, err := ObservedFromPrometheusLikeJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	assertAdapterMatrix(t, obs, observedMatrixExpectation())
	q := findQueueObs(t, obs, "q", "/orders")
	assertPresentIntEqual(t, "queue.drop_count", q.DropCount, I64(5))
	assertPresentIntEqual(t, "queue.publish_attempt_count", q.QueuePublishAttemptCount, I64(120))
	assertPresentFloatEqual(t, "queue.oldest_age_ms", q.OldestAgeMs, F64(18), 1e-9)
	assertPresentIntEqual(t, "queue.dlq_count", q.DLQCount, I64(1))
	tb := findTopicObs(t, obs, "t", "/ev", 2, "g1")
	assertPresentIntEqual(t, "topic.deliver_count", tb.TopicDeliverCount, I64(200))
	assertPresentIntEqual(t, "topic.drop_count", tb.DropCount, I64(10))
	assertPresentFloatEqual(t, "topic.oldest_age_ms", tb.OldestAgeMs, F64(33), 1e-9)
	assertPresentIntEqual(t, "topic.dlq_count", tb.DLQCount, I64(3))
}

func TestObservedFromPrometheusLikeJSON_LabelRequirements(t *testing.T) {
	tests := []struct {
		name     string
		fixture  string
		wantErr  string
	}{
		{
			name:    "service metric missing service label",
			fixture: "prometheus_missing_service_label.json",
			wantErr: "requires label service",
		},
		{
			name:    "endpoint metric missing endpoint label",
			fixture: "prometheus_missing_endpoint_label.json",
			wantErr: "requires labels service and endpoint",
		},
		{
			name:    "queue metric missing topic label",
			fixture: "prometheus_missing_queue_topic_label.json",
			wantErr: "requires broker_service and topic labels",
		},
		{
			name:    "topic metric missing broker label",
			fixture: "prometheus_missing_topic_broker_label.json",
			wantErr: "requires broker_service and topic labels",
		},
		{
			name:    "unknown metric",
			fixture: "prometheus_unknown_metric.json",
			wantErr: `unknown metric "sim_not_real_metric"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", tc.fixture))
			if err != nil {
				t.Fatal(err)
			}
			_, err = ObservedFromPrometheusLikeJSON(raw)
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q missing %q", err.Error(), tc.wantErr)
			}
		})
	}
}
