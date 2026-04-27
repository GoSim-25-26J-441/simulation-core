package simd

import (
	"math"
	"strings"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/utils"
)

// ServiceExecutionProfile captures per-request work and queueing hints derived from
// service kind/role/model and endpoint configuration.
type ServiceExecutionProfile struct {
	CPUTimeMs        float64
	NetworkLatencyMs float64
	MemoryMB         float64
	ConcurrencyCost  float64
	QueueClass       string
	// QueueMeanWorkMs is a deterministic mean service time (ms) for diagnostics / tuning
	// (e.g. queue class hints). DES queue wait is modeled as sim time from ArrivalTime to
	// StartTime, not as queue_length × QueueMeanWorkMs.
	QueueMeanWorkMs float64
}

// resolveServiceExecutionProfile maps scenario metadata + endpoint stats into runtime
// costs. Downstream kind is not wired at request start; pass nil ds when unused.
func resolveServiceExecutionProfile(svc *config.Service, ep *config.Endpoint, ds *config.DownstreamCall, rng *utils.RandSource) ServiceExecutionProfile {
	if svc == nil || ep == nil || rng == nil {
		return ServiceExecutionProfile{QueueClass: "default", QueueMeanWorkMs: 0}
	}

	cpu := rng.NormFloat64(ep.MeanCPUMs, ep.CPUSigmaMs)
	if cpu < 0 {
		cpu = 0
	}
	net := rng.NormFloat64(ep.NetLatencyMs.Mean, ep.NetLatencyMs.Sigma)
	if net < 0 {
		net = 0
	}

	mem := ep.DefaultMemoryMB
	if mem <= 0 {
		mem = 10.0
	}

	model := strings.ToLower(strings.TrimSpace(svc.Model))
	kind := strings.ToLower(strings.TrimSpace(svc.Kind))
	role := strings.ToLower(strings.TrimSpace(svc.Role))

	// Optional downstream hint (e.g. DB hop): adds marginal network/query latency awareness.
	dsKind := ""
	if ds != nil {
		dsKind = strings.ToLower(strings.TrimSpace(ds.Kind))
	}

	queueClass := "default"
	switch {
	case kind == "database" || model == "db_latency" || role == "datastore":
		queueClass = "datastore_io"
	case kind == "api_gateway" || role == "ingress":
		queueClass = "ingress"
	case kind == "cache":
		queueClass = "cache"
	}

	concurrencyCost := 1.0

	switch model {
	case "mixed":
		// Memory pressure increases effective concurrency contention in the model.
		if mem > 0 {
			concurrencyCost = 1.0 + mem/1024.0
		}
	case "db_latency":
		// Latency/IO dominated: keep CPU work below network/query latency unless
		// the endpoint explicitly asks for more CPU than the net mean.
		netMean := ep.NetLatencyMs.Mean
		if netMean < 0 {
			netMean = 0
		}
		ceiling := math.Max(netMean*0.55, ep.MeanCPUMs*0.85)
		if ep.MeanCPUMs > netMean*1.1 {
			ceiling = ep.MeanCPUMs * 1.05
		}
		if cpu > ceiling {
			cpu = ceiling
		}
		// Working-set pressure for datastores
		mem = mem * 1.05
		if dsKind == "db" || kind == "database" {
			net *= 1.02
		}
	default:
		// "cpu" or empty — concurrencyCost stays 1
	}

	// Kind-specific nudges (lightweight; avoids all services behaving identically).
	switch kind {
	case "database":
		if model != "db_latency" {
			// Still treat as IO-ish unless model overrides.
			net *= 1.05
		}
	case "cache":
		cpu *= 0.85
		net *= 0.9
	case "external":
		net *= 1.1
	}

	queueMean := ep.MeanCPUMs + ep.NetLatencyMs.Mean
	if model == "db_latency" {
		// Backlog delay should track query/IO more than CPU.
		io := ep.NetLatencyMs.Mean
		if io < 0 {
			io = 0
		}
		cpuPart := math.Min(ep.MeanCPUMs, io*0.45)
		queueMean = cpuPart + io
	}
	if queueMean < 0 {
		queueMean = 0
	}

	return ServiceExecutionProfile{
		CPUTimeMs:        cpu,
		NetworkLatencyMs: net,
		MemoryMB:         mem,
		ConcurrencyCost:  concurrencyCost,
		QueueClass:       queueClass,
		QueueMeanWorkMs:  queueMean,
	}
}
