//go:build ignore

// dump_research_metrics runs each research scenario and prints a compact
// markdown table of run-level metrics for paper drafts.
//
// Usage:
//
//	go run scripts/dump_research_metrics.go -dir config/research_scenarios -duration-ms 60000 -seeds 1,2,3
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/simd"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/logger"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

type row struct {
	name          string
	throughput    float64
	p50           float64
	p95           float64
	p99           float64
	errorRatePct  float64
	maxCPU        float64
	maxQueueWait  float64
	queueDepth    float64
	amplification float64
}

func main() {
	dir := flag.String("dir", "config/research_scenarios", "directory containing scenario YAML files")
	durationMs := flag.Int64("duration-ms", 60000, "simulation duration in milliseconds")
	seedCSV := flag.String("seeds", "1,2,3", "comma-separated seed list")
	flag.Parse()

	logger.SetDefault(logger.New("error", os.Stderr))

	seeds, err := parseSeeds(*seedCSV)
	if err != nil {
		fatal(err)
	}

	files, err := filepath.Glob(filepath.Join(*dir, "*.yaml"))
	if err != nil {
		fatal(err)
	}
	sort.Strings(files)
	if len(files) == 0 {
		fatal(fmt.Errorf("no scenario YAML files found in %s", *dir))
	}

	fmt.Println("| Scenario | Ingress throughput RPS | Root P50 ms | Root P95 ms | Root P99 ms | Ingress error rate % | Max CPU util | Max queue wait mean ms | Broker queue depth sum | Work amplification |")
	fmt.Println("|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|")
	for i, file := range files {
		scenario, err := loadScenario(file)
		if err != nil {
			fatal(fmt.Errorf("%s: %w", file, err))
		}
		runs := make([]*models.RunMetrics, 0, len(seeds))
		for _, seed := range seeds {
			rm, err := simd.RunScenarioForMetrics(scenario, time.Duration(*durationMs)*time.Millisecond, seed, false)
			if err != nil {
				fatal(fmt.Errorf("%s seed %d: %w", file, seed, err))
			}
			runs = append(runs, rm)
		}
		r := aggregate(fmt.Sprintf("S%d", i+1), runs)
		fmt.Printf("| %s | %.1f | %.1f | %.1f | %.1f | %.2f | %.2f | %.1f | %.0f | %.1f |\n",
			r.name, r.throughput, r.p50, r.p95, r.p99, r.errorRatePct, r.maxCPU, r.maxQueueWait, r.queueDepth, r.amplification)
	}
}

func loadScenario(path string) (*config.Scenario, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return config.ParseScenarioYAML(b)
}

func parseSeeds(csv string) ([]int64, error) {
	parts := strings.Split(csv, ",")
	out := make([]int64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		out = append(out, 1)
	}
	return out, nil
}

func aggregate(name string, runs []*models.RunMetrics) row {
	if len(runs) == 0 {
		return row{name: name}
	}
	var throughputSum, p50Sum, ampSum float64
	var maxP95, maxP99, maxErr, maxCPU, maxQueueWait, maxQueueDepth float64
	for _, rm := range runs {
		if rm == nil {
			continue
		}
		throughputSum += rm.IngressThroughputRPS
		p50Sum += rm.LatencyP50
		if rm.IngressRequests > 0 {
			ampSum += float64(rm.TotalRequests) / float64(rm.IngressRequests)
		}
		if rm.LatencyP95 > maxP95 {
			maxP95 = rm.LatencyP95
		}
		if rm.LatencyP99 > maxP99 {
			maxP99 = rm.LatencyP99
		}
		if rm.IngressErrorRate > maxErr {
			maxErr = rm.IngressErrorRate
		}
		if rm.QueueDepthSum > maxQueueDepth {
			maxQueueDepth = rm.QueueDepthSum
		}
		for _, sm := range rm.ServiceMetrics {
			if sm == nil {
				continue
			}
			if sm.CPUUtilization > maxCPU {
				maxCPU = sm.CPUUtilization
			}
			if sm.QueueWaitMeanMs > maxQueueWait {
				maxQueueWait = sm.QueueWaitMeanMs
			}
		}
	}
	n := float64(len(runs))
	return row{
		name:          name,
		throughput:    throughputSum / n,
		p50:           p50Sum / n,
		p95:           maxP95,
		p99:           maxP99,
		errorRatePct:  maxErr * 100,
		maxCPU:        maxCPU,
		maxQueueWait:  maxQueueWait,
		queueDepth:    maxQueueDepth,
		amplification: ampSum / n,
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
