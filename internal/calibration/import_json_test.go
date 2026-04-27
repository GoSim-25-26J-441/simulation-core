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
	  "topic_brokers": [{"broker_service": "q", "topic": "t", "partition": 0, "consumer_group": "g", "consumer_lag": 0}],
	  "instance_routing": [{"service_id":"api","endpoint_path":"/x","instance_id":"api-instance-0","request_share":0.6,"request_count":120}]
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
	if len(obs.InstanceRouting) != 1 {
		t.Fatalf("expected instance routing observation, got %+v", obs.InstanceRouting)
	}
	ir := obs.InstanceRouting[0]
	if ir.ServiceID != "api" || ir.EndpointPath != "/x" || ir.InstanceID != "api-instance-0" {
		t.Fatalf("unexpected instance routing identity: %+v", ir)
	}
	if !ir.RequestShare.Present || ir.RequestShare.Value != 0.6 {
		t.Fatalf("expected request_share present, got %+v", ir.RequestShare)
	}
	if !ir.RequestCount.Present || ir.RequestCount.Value != 120 {
		t.Fatalf("expected request_count present, got %+v", ir.RequestCount)
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

func TestObservedFromSimulatorExportJSON_InstanceRouting(t *testing.T) {
	raw := `{
	  "window_seconds": 60,
	  "run_metrics": {
	    "total_requests": 100,
	    "successful_requests": 100,
	    "failed_requests": 0,
	    "latency_p50_ms": 10,
	    "latency_p95_ms": 20,
	    "latency_p99_ms": 30,
	    "latency_mean_ms": 12,
	    "throughput_rps": 1.6667,
	    "instance_route_stats": [
	      {"service_name":"api","endpoint_path":"/x","instance_id":"api-instance-0","strategy":"weighted_round_robin","selection_count":90},
	      {"service_name":"api","endpoint_path":"/x","instance_id":"api-instance-1","strategy":"weighted_round_robin","selection_count":10}
	    ]
	  }
	}`
	obs, err := ObservedFromSimulatorExportJSON([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.InstanceRouting) != 2 {
		t.Fatalf("expected instance routing observations, got %+v", obs.InstanceRouting)
	}
	var hi, lo *InstanceRoutingObservation
	for i := range obs.InstanceRouting {
		r := &obs.InstanceRouting[i]
		if r.InstanceID == "api-instance-0" {
			hi = r
		}
		if r.InstanceID == "api-instance-1" {
			lo = r
		}
	}
	if hi == nil || lo == nil {
		t.Fatalf("missing instance routing rows: %+v", obs.InstanceRouting)
	}
	if !hi.RequestCount.Present || hi.RequestCount.Value != 90 || !hi.RequestShare.Present || hi.RequestShare.Value < 0.89 {
		t.Fatalf("unexpected high-share row %+v", *hi)
	}
	if !lo.RequestCount.Present || lo.RequestCount.Value != 10 || !lo.RequestShare.Present || lo.RequestShare.Value > 0.11 {
		t.Fatalf("unexpected low-share row %+v", *lo)
	}
}

func TestObservedFromSimulatorExportJSON_TopologyFields(t *testing.T) {
	raw := `{
	  "window_seconds": 60,
	  "run_metrics": {
	    "total_requests": 100,
	    "successful_requests": 100,
	    "failed_requests": 0,
	    "latency_p50_ms": 10,
	    "latency_p95_ms": 20,
	    "latency_p99_ms": 30,
	    "latency_mean_ms": 12,
	    "throughput_rps": 1.6667,
	    "locality_hit_rate": 0.88,
	    "cross_zone_request_fraction": 0.12
	  }
	}`
	obs, err := ObservedFromSimulatorExportJSON([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !obs.Global.LocalityHitRate.Present || obs.Global.LocalityHitRate.Value != 0.88 {
		t.Fatalf("expected locality_hit_rate present from simulator export, got %+v", obs.Global.LocalityHitRate)
	}
	if !obs.Global.CrossZoneFraction.Present || obs.Global.CrossZoneFraction.Value != 0.12 {
		t.Fatalf("expected cross_zone_fraction present from simulator export, got %+v", obs.Global.CrossZoneFraction)
	}
}

func TestDecodeObservedMetricsRoundTripJSON(t *testing.T) {
	m := map[string]any{
		"window_seconds": 30.0,
		"run_metrics": map[string]any{
			"total_requests":     100.0,
			"ingress_requests":   50.0,
			"ingress_error_rate": 0.01,
			"latency_p50_ms":     5.0,
			"latency_p95_ms":     10.0,
			"latency_p99_ms":     20.0,
			"latency_mean_ms":    6.0,
			"throughput_rps":     10.0,
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
