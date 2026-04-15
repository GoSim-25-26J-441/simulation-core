package calibration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestObservedFromSimulatorExportJSONGolden(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "simulator_export_min.json"))
	if err != nil {
		t.Fatal(err)
	}
	obs, err := ObservedFromSimulatorExportJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if !obs.Global.RootLatencyP50Ms.Present {
		t.Fatal("expected root p50 present from snapshot")
	}
	if obs.Window.Duration <= 0 {
		t.Fatal("expected positive window")
	}
}

func TestObservedFromPartialJSONPresence(t *testing.T) {
	raw := `{
	  "window": {"duration": "2m", "source": "test"},
	  "global": {"ingress_error_rate": 0},
	  "topic_brokers": [{"broker_service": "q", "topic": "t", "partition": 0, "consumer_group": "g", "consumer_lag": 0}]
	}`
	obs, err := ObservedFromPartialJSON([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !obs.Global.IngressErrorRate.Present || obs.Global.IngressErrorRate.Value != 0 {
		t.Fatalf("explicit zero must be present: %+v", obs.Global.IngressErrorRate)
	}
	if len(obs.TopicBrokers) != 1 || !obs.TopicBrokers[0].ConsumerLag.Present || obs.TopicBrokers[0].ConsumerLag.Value != 0 {
		t.Fatalf("topic lag explicit zero: %+v", obs.TopicBrokers)
	}
}

func TestObservedFromPrometheusLikeJSON(t *testing.T) {
	raw := `{
	  "window_seconds": 120,
	  "samples": [
	    {"metric": "sim_ingress_throughput_rps", "value": 12},
	    {"metric": "sim_endpoint_latency_p95_ms", "labels":{"service":"api","endpoint":"/x"}, "value": 40}
	  ]
	}`
	obs, err := ObservedFromPrometheusLikeJSON([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !obs.Global.IngressThroughputRPS.Present || obs.Global.IngressThroughputRPS.Value != 12 {
		t.Fatal(obs.Global.IngressThroughputRPS)
	}
	if len(obs.Endpoints) != 1 || !obs.Endpoints[0].LatencyP95Ms.Present {
		t.Fatalf("endpoint %+v", obs.Endpoints)
	}
}

func TestObservedFromPartialJSON_MatrixFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "observed_metrics_matrix.json"))
	if err != nil {
		t.Fatal(err)
	}
	obs, err := ObservedFromPartialJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	exp := observedMatrixExpectation()
	exp.hasService = false
	assertAdapterMatrix(t, obs, exp)
}

func TestObservedFromSimulatorExportJSON_MatrixFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "simulator_export_matrix.json"))
	if err != nil {
		t.Fatal(err)
	}
	obs, err := ObservedFromSimulatorExportJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	exp := observedMatrixExpectation()
	exp.hasQueue = false
	exp.hasTopic = false
	assertAdapterMatrix(t, obs, exp)
	assertPresentFloatEqual(t, "global.ingress_error_rate", obs.Global.IngressErrorRate, F64(0.025), 1e-9)
}

func TestDecodeObservedMetricsRoundTripJSON(t *testing.T) {
	m := map[string]any{
		"window_seconds": 30.0,
		"run_metrics": map[string]any{
			"total_requests": 100.0,
			"ingress_requests": 50.0,
			"ingress_error_rate": 0.01,
			"latency_p50_ms": 5.0,
			"latency_p95_ms": 10.0,
			"latency_p99_ms": 20.0,
			"latency_mean_ms": 6.0,
			"throughput_rps": 10.0,
		},
	}
	b, _ := json.Marshal(m)
	obs, err := DecodeObservedMetrics(FormatSimulatorExport, b)
	if err != nil {
		t.Fatal(err)
	}
	if !obs.Global.IngressErrorRate.Present {
		t.Fatal("expected ingress error rate")
	}
}

func TestDecodeObservedMetrics_CrossAdapterConsistency(t *testing.T) {
	promRaw, err := os.ReadFile(filepath.Join("testdata", "prometheus_expanded_matrix.json"))
	if err != nil {
		t.Fatal(err)
	}
	obsProm, err := DecodeObservedMetrics(FormatPrometheusJSON, promRaw)
	if err != nil {
		t.Fatal(err)
	}

	partialRaw, err := os.ReadFile(filepath.Join("testdata", "observed_metrics_matrix.json"))
	if err != nil {
		t.Fatal(err)
	}
	obsPartial, err := DecodeObservedMetrics(FormatObservedMetrics, partialRaw)
	if err != nil {
		t.Fatal(err)
	}

	exportRaw, err := os.ReadFile(filepath.Join("testdata", "simulator_export_matrix.json"))
	if err != nil {
		t.Fatal(err)
	}
	obsExport, err := DecodeObservedMetrics(FormatSimulatorExport, exportRaw)
	if err != nil {
		t.Fatal(err)
	}

	// Overlapping scalar fields across all adapters
	assertPresentFloatEqual(t, "ingress_throughput_rps(prom,partial)", obsProm.Global.IngressThroughputRPS, obsPartial.Global.IngressThroughputRPS, 1e-9)
	assertPresentFloatEqual(t, "ingress_throughput_rps(prom,export)", obsProm.Global.IngressThroughputRPS, obsExport.Global.IngressThroughputRPS, 1e-9)
	assertPresentFloatEqual(t, "root_latency_p95_ms(prom,partial)", obsProm.Global.RootLatencyP95Ms, obsPartial.Global.RootLatencyP95Ms, 1e-9)
	assertPresentFloatEqual(t, "root_latency_p95_ms(prom,export)", obsProm.Global.RootLatencyP95Ms, obsExport.Global.RootLatencyP95Ms, 1e-9)
	assertPresentIntEqual(t, "retry_attempts(prom,partial)", obsProm.Global.RetryAttempts, obsPartial.Global.RetryAttempts)
	assertPresentIntEqual(t, "retry_attempts(prom,export)", obsProm.Global.RetryAttempts, obsExport.Global.RetryAttempts)
	assertPresentIntEqual(t, "timeout_errors(prom,partial)", obsProm.Global.TimeoutErrors, obsPartial.Global.TimeoutErrors)
	assertPresentIntEqual(t, "timeout_errors(prom,export)", obsProm.Global.TimeoutErrors, obsExport.Global.TimeoutErrors)

	// Endpoint overlap by service/path
	epProm := findEndpointObs(t, obsProm, "api", "/a")
	epPartial := findEndpointObs(t, obsPartial, "api", "/a")
	epExport := findEndpointObs(t, obsExport, "api", "/a")
	assertPresentFloatEqual(t, "endpoint_latency_p95(prom,partial)", epProm.LatencyP95Ms, epPartial.LatencyP95Ms, 1e-9)
	assertPresentFloatEqual(t, "endpoint_latency_p95(prom,export)", epProm.LatencyP95Ms, epExport.LatencyP95Ms, 1e-9)
	assertPresentFloatEqual(t, "endpoint_queue_wait_mean(prom,partial)", epProm.QueueWaitMeanMs, epPartial.QueueWaitMeanMs, 1e-9)
	assertPresentFloatEqual(t, "endpoint_queue_wait_mean(prom,export)", epProm.QueueWaitMeanMs, epExport.QueueWaitMeanMs, 1e-9)
	assertPresentFloatEqual(t, "endpoint_processing_mean(prom,partial)", epProm.ProcessingLatencyMeanMs, epPartial.ProcessingLatencyMeanMs, 1e-9)
	assertPresentFloatEqual(t, "endpoint_processing_mean(prom,export)", epProm.ProcessingLatencyMeanMs, epExport.ProcessingLatencyMeanMs, 1e-9)
	assertPresentIntEqual(t, "endpoint_request_count(prom,partial)", epProm.RequestCount, epPartial.RequestCount)
	assertPresentIntEqual(t, "endpoint_request_count(prom,export)", epProm.RequestCount, epExport.RequestCount)
	assertPresentIntEqual(t, "endpoint_error_count(prom,partial)", epProm.ErrorCount, epPartial.ErrorCount)
	assertPresentIntEqual(t, "endpoint_error_count(prom,export)", epProm.ErrorCount, epExport.ErrorCount)
}
