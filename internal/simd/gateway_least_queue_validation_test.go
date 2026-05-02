package simd

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// TestValidationGatewayLeastQueueHighLoad replays a gateway-1 / least_queue / ~180 RPS scenario
// to confirm post-fix routing, latency, and export of cpu_scheduler_backlog_ms. Historical
// "before" numbers (pre-fix) are documented in the test log output for comparison only.
func TestValidationGatewayLeastQueueHighLoad(t *testing.T) {
	t.Helper()
	repoRoot := filepath.Join("..", "..")
	scPath := filepath.Join(repoRoot, "config", "validation_gateway_least_queue.yaml")
	raw, err := os.ReadFile(scPath)
	if err != nil {
		t.Fatalf("read %s: %v", scPath, err)
	}
	sc, err := config.ParseScenarioYAML(raw)
	if err != nil {
		t.Fatal(err)
	}
	simDur := 10 * time.Second
	seed := int64(77)
	rm, seriesNames, err := RunScenarioForMetricsWithSeriesNames(sc, simDur, seed, false)
	if err != nil {
		t.Fatal(err)
	}

	t.Log("--- Historical pre-fix failure (reference only, not re-measured) ---")
	t.Log("  route_selection ~100% to one gateway replica; queue_wait_mean_ms ~26,460; root p95/p99 ~101,000 ms; queue_depth_sum 0")

	hasBacklog := false
	for _, n := range seriesNames {
		if n == metrics.MetricCPUSchedulerBacklogMs {
			hasBacklog = true
			break
		}
	}
	if !hasBacklog {
		sort.Strings(seriesNames)
		t.Fatalf("expected metric %q in collector series (export/timeseries path), got names=%v", metrics.MetricCPUSchedulerBacklogMs, seriesNames)
	}

	var g0, g1 int64
	for _, row := range rm.InstanceRouteStats {
		if row.ServiceName != "gateway-1" || row.EndpointPath != "/ingress" || row.Strategy != "least_queue" {
			continue
		}
		switch row.InstanceID {
		case "gateway-1-instance-0":
			g0 = row.SelectionCount
		case "gateway-1-instance-1":
			g1 = row.SelectionCount
		}
	}
	total := g0 + g1
	if total < 500 {
		t.Fatalf("expected substantial ingress route samples, got gateway-1 instance-0=%d instance-1=%d (total=%d)", g0, g1, total)
	}
	maxSel := g0
	if g1 > maxSel {
		maxSel = g1
	}
	maxShare := float64(maxSel) / float64(total)
	if maxShare >= 0.95 {
		t.Fatalf("routing still skewed: gateway-1-instance-0=%d instance-1=%d maxShare=%.4f", g0, g1, maxShare)
	}
	// Under steady constant arrivals + deterministic tie-break, expect rough balance; allow headroom for stochastic hops.
	if maxShare > 0.72 {
		t.Logf("note: maxShare=%.3f exceeds ideal 60–70%% band (may occur with short sim or downstream variance); still passes routing-spread check", maxShare)
	}

	gw := rm.ServiceMetrics["gateway-1"]
	if gw == nil {
		t.Fatal("missing gateway-1 service metrics")
	}
	if gw.QueueWaitMeanMs > 5000 {
		t.Fatalf("gateway queue_wait_mean_ms too high: %.1f ms (want well below pre-fix ~26,460 ms)", gw.QueueWaitMeanMs)
	}
	if gw.QueueWaitMeanMs > 2000 {
		t.Logf("warning: gateway queue_wait_mean_ms=%.1f ms still elevated (check capacity)", gw.QueueWaitMeanMs)
	}

	if rm.LatencyP95 > 30_000 {
		t.Fatalf("root/latency p95 too high: %.1f ms (want far below pre-fix ~101,000 ms)", rm.LatencyP95)
	}
	if rm.IngressThroughputRPS < 165 || rm.IngressThroughputRPS > 195 {
		t.Logf("ingress_throughput_rps=%.1f (target ~180)", rm.IngressThroughputRPS)
	}
	if rm.IngressErrorRate > 0.001 {
		t.Fatalf("ingress_error_rate=%.6f (want ~0)", rm.IngressErrorRate)
	}

	t.Log("--- After-fix snapshot (this run) ---")
	t.Logf("  instance_route_stats gateway-1 /ingress: instance-0=%d instance-1=%d total=%d maxShare=%.3f", g0, g1, total, maxShare)
	t.Logf("  gateway-1 queue_wait_mean_ms=%.1f", gw.QueueWaitMeanMs)
	t.Logf("  latency_p95_ms=%.1f latency_p99_ms=%.1f", rm.LatencyP95, rm.LatencyP99)
	t.Logf("  ingress_throughput_rps=%.1f ingress_error_rate=%.6f", rm.IngressThroughputRPS, rm.IngressErrorRate)
	t.Logf("  collector includes %s: %v", metrics.MetricCPUSchedulerBacklogMs, hasBacklog)
}
