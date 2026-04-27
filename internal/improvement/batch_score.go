package improvement

import (
	"math"
	"strings"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/batchspec"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

const scoreEps = 1e-9

// BatchScore holds feasibility-first ranking components for a candidate.
type BatchScore struct {
	Feasible                 bool
	ViolationScore           float64
	EfficiencyScore          float64
	LatViolation             float64
	P99Violation             float64
	ErrViolation             float64
	TputViolation            float64
	QueueDepthViolation      float64
	TopicBacklogViolation    float64
	TopicLagViolation        float64
	QueueOldestAgeViolation  float64
	TopicOldestAgeViolation  float64
	QueueDropViolation       float64
	TopicDropViolation       float64
	QueueDlqViolation        float64
	TopicDlqViolation        float64
	LocalityViolation        float64
	CrossZoneViolation       float64
	TopologyLatencyViolation float64
	InfraCost                float64
	ServiceCPUBal            float64
	ServiceMemBal            float64
	HostCPUBal               float64
	HostMemBal               float64
	Churn                    float64
	LocalityPenalty          float64
	CrossZonePenalty         float64
	TopologyPenalty          float64
}

func bandPenalty(u, low, high float64) float64 {
	if u >= low && u <= high {
		return 0
	}
	if u < low {
		return low - u
	}
	return u - high
}

// serviceUtilStats returns max and mean CPU/memory util across non-client services.
func serviceUtilStats(m *simulationv1.RunMetrics) (maxCPU, meanCPU, maxMem, meanMem float64, n int) {
	if m == nil || len(m.ServiceMetrics) == 0 {
		return 0, 0, 0, 0, 0
	}
	var sumCPU, sumMem float64
	for _, svc := range m.ServiceMetrics {
		if strings.HasPrefix(svc.GetServiceName(), "client") {
			continue
		}
		n++
		cu := svc.GetCpuUtilization()
		mu := svc.GetMemoryUtilization()
		if cu > maxCPU {
			maxCPU = cu
		}
		if mu > maxMem {
			maxMem = mu
		}
		sumCPU += cu
		sumMem += mu
	}
	if n > 0 {
		meanCPU = sumCPU / float64(n)
		meanMem = sumMem / float64(n)
	}
	return maxCPU, meanCPU, maxMem, meanMem, n
}

// hostUtilStats returns max/mean CPU and memory utilization from RunMetrics.host_metrics when present.
func hostUtilStats(m *simulationv1.RunMetrics) (maxCPU, meanCPU, maxMem, meanMem float64, n int) {
	if m == nil || len(m.GetHostMetrics()) == 0 {
		return 0, 0, 0, 0, 0
	}
	var sumCPU, sumMem float64
	for _, hm := range m.GetHostMetrics() {
		if hm == nil {
			continue
		}
		n++
		cu := hm.GetCpuUtilization()
		mu := hm.GetMemoryUtilization()
		if cu > maxCPU {
			maxCPU = cu
		}
		if mu > maxMem {
			maxMem = mu
		}
		sumCPU += cu
		sumMem += mu
	}
	if n > 0 {
		meanCPU = sumCPU / float64(n)
		meanMem = sumMem / float64(n)
	}
	return maxCPU, meanCPU, maxMem, meanMem, n
}

// balanceTerm implements 0.7*max + 0.3*mean of band penalties.
func balanceTerm(maxU, meanU, low, high float64) float64 {
	return 0.7*bandPenalty(maxU, low, high) + 0.3*bandPenalty(meanU, low, high)
}

// ComputeInfraCostWeighted applies BatchCostWeights to the scenario (excluding churn).
func ComputeInfraCostWeighted(s *config.Scenario, w *simulationv1.BatchCostWeights) float64 {
	if w == nil || s == nil {
		return EvaluateInfrastructureCost(s)
	}
	var sumCPU, sumMemGB, sumRep float64
	for _, svc := range s.Services {
		r := float64(svc.Replicas)
		if r < 1 {
			r = 1
		}
		cpu := svc.CPUCores
		if cpu <= 0 {
			cpu = defaultServiceCPUCores
		}
		mb := svc.MemoryMB
		if mb <= 0 {
			mb = defaultServiceMemoryMB
		}
		sumCPU += r * cpu
		sumMemGB += r * (mb / 1024.0)
		sumRep += r
	}
	var hostCores, hostMem int
	for _, h := range s.Hosts {
		hostCores += h.Cores
		gb := h.MemoryGB
		if gb < 1 {
			gb = 16
		}
		hostMem += gb
	}
	return w.ServiceCpu*sumCPU +
		w.ServiceMemoryGb*sumMemGB +
		w.Replicas*sumRep +
		w.Hosts*float64(len(s.Hosts)) +
		w.HostCpu*float64(hostCores) +
		w.HostMemoryGb*float64(hostMem)
}

// ComputeChurn L1 normalized distance from baseline (replicas, CPU, mem per service; host count).
func ComputeChurn(base, cur *config.Scenario) float64 {
	if base == nil || cur == nil {
		return 0
	}
	var sum float64
	for i := range cur.Services {
		if i >= len(base.Services) {
			break
		}
		b, c := base.Services[i], cur.Services[i]
		if b.ID != c.ID {
			continue
		}
		sum += math.Abs(float64(c.Replicas-b.Replicas)) / math.Max(1, float64(b.Replicas))
		sum += math.Abs(c.CPUCores-b.CPUCores) / math.Max(0.25, b.CPUCores)
		sum += math.Abs(c.MemoryMB-b.MemoryMB) / math.Max(128, b.MemoryMB)
	}
	sum += math.Abs(float64(len(cur.Hosts)-len(base.Hosts))) / math.Max(1, float64(len(base.Hosts)))
	return sum
}

// ComputeBatchScore computes violation and efficiency scores per the batch spec.
func ComputeBatchScore(spec *batchspec.BatchSpec, baseline, scenario *config.Scenario, m *simulationv1.RunMetrics) BatchScore {
	out := BatchScore{}
	if spec == nil {
		return out
	}
	pw := spec.PenaltyWeights
	if pw == nil {
		pw = &simulationv1.BatchPenaltyWeights{
			P95: 1, P99: 1, ErrorRate: 1, Throughput: 1,
			ServiceCpuBalance: 1, ServiceMemoryBalance: 1, HostCpuBalance: 1, HostMemoryBalance: 1,
			QueueDepth: 1, TopicBacklog: 1, TopicLag: 1,
			QueueOldestAge: 1, TopicOldestAge: 1, QueueDrop: 1, TopicDrop: 1, QueueDlq: 1, TopicDlq: 1,
			Locality: 1, CrossZone: 1, TopologyLatency: 1,
		}
	}
	cw := spec.CostWeights
	if cw == nil {
		cw = &simulationv1.BatchCostWeights{ServiceCpu: 1, ServiceMemoryGb: 1, Replicas: 1, Hosts: 1, HostCpu: 1, HostMemoryGb: 1, Churn: 0.5}
	}

	var p95, p99, tput float64
	var errRate float64
	if m != nil {
		p95 = m.GetLatencyP95Ms()
		p99 = m.GetLatencyP99Ms()
		tput = m.GetIngressThroughputRps()
		if tput <= 0 {
			tput = m.GetThroughputRps()
		}
		errRate = UserVisibleErrorRate(m)
	}

	if spec.MaxP95Ms > 0 {
		out.LatViolation = math.Max(0, p95/spec.MaxP95Ms-1)
	}
	if spec.MaxP99Ms > 0 {
		out.P99Violation = math.Max(0, p99/spec.MaxP99Ms-1)
	}
	if spec.MaxErrorRate > 0 {
		out.ErrViolation = math.Max(0, errRate/spec.MaxErrorRate-1)
	}
	if spec.MinThroughput > 0 {
		out.TputViolation = math.Max(0, spec.MinThroughput/math.Max(tput, scoreEps)-1)
	}
	if m != nil {
		if spec.MaxQueueDepthSum > 0 {
			out.QueueDepthViolation = math.Max(0, m.GetQueueDepthSum()/spec.MaxQueueDepthSum-1)
		}
		if spec.MaxTopicBacklogDepthSum > 0 {
			out.TopicBacklogViolation = math.Max(0, m.GetTopicBacklogDepthSum()/spec.MaxTopicBacklogDepthSum-1)
		}
		if spec.MaxTopicConsumerLagSum > 0 {
			out.TopicLagViolation = math.Max(0, m.GetTopicConsumerLagSum()/spec.MaxTopicConsumerLagSum-1)
		}
		if spec.MaxQueueOldestMessageAgeMs > 0 {
			out.QueueOldestAgeViolation = math.Max(0, m.GetQueueOldestMessageAgeMs()/spec.MaxQueueOldestMessageAgeMs-1)
		}
		if spec.MaxTopicOldestMessageAgeMs > 0 {
			out.TopicOldestAgeViolation = math.Max(0, m.GetTopicOldestMessageAgeMs()/spec.MaxTopicOldestMessageAgeMs-1)
		}
		if spec.MaxQueueDropCount > 0 {
			out.QueueDropViolation = math.Max(0, float64(m.GetQueueDropCountTotal())/spec.MaxQueueDropCount-1)
		}
		if spec.MaxTopicDropCount > 0 {
			out.TopicDropViolation = math.Max(0, float64(m.GetTopicDropCountTotal())/spec.MaxTopicDropCount-1)
		}
		if spec.MaxQueueDlqCount > 0 {
			out.QueueDlqViolation = math.Max(0, float64(m.GetQueueDlqCountTotal())/spec.MaxQueueDlqCount-1)
		}
		if spec.MaxTopicDlqCount > 0 {
			out.TopicDlqViolation = math.Max(0, float64(m.GetTopicDlqCountTotal())/spec.MaxTopicDlqCount-1)
		}
		if spec.MinLocalityHitRate > 0 {
			out.LocalityViolation = math.Max(0, spec.MinLocalityHitRate/math.Max(m.GetLocalityHitRate(), scoreEps)-1)
		}
		if spec.MaxCrossZoneRequestFraction > 0 {
			out.CrossZoneViolation = math.Max(0, m.GetCrossZoneRequestFraction()/spec.MaxCrossZoneRequestFraction-1)
		}
		if spec.MaxTopologyLatencyPenaltyMeanMs > 0 {
			out.TopologyLatencyViolation = math.Max(0, m.GetTopologyLatencyPenaltyMsMean()/spec.MaxTopologyLatencyPenaltyMeanMs-1)
		}
	}

	out.ViolationScore = pw.P95*out.LatViolation +
		pw.P99*out.P99Violation +
		pw.ErrorRate*out.ErrViolation +
		pw.Throughput*out.TputViolation +
		pw.QueueDepth*out.QueueDepthViolation +
		pw.TopicBacklog*out.TopicBacklogViolation +
		pw.TopicLag*out.TopicLagViolation +
		pw.QueueOldestAge*out.QueueOldestAgeViolation +
		pw.TopicOldestAge*out.TopicOldestAgeViolation +
		pw.QueueDrop*out.QueueDropViolation +
		pw.TopicDrop*out.TopicDropViolation +
		pw.QueueDlq*out.QueueDlqViolation +
		pw.TopicDlq*out.TopicDlqViolation +
		pw.Locality*out.LocalityViolation +
		pw.CrossZone*out.CrossZoneViolation +
		pw.TopologyLatency*out.TopologyLatencyViolation

	maxCPU, meanCPU, maxMem, meanMem, n := serviceUtilStats(m)
	if n > 0 {
		out.ServiceCPUBal = balanceTerm(maxCPU, meanCPU, spec.ServiceCPUBandLow, spec.ServiceCPUBandHigh)
		out.ServiceMemBal = balanceTerm(maxMem, meanMem, spec.ServiceMemBandLow, spec.ServiceMemBandHigh)
	}
	hMaxCPU, hMeanCPU, hMaxMem, hMeanMem, hn := hostUtilStats(m)
	if hn > 0 {
		out.HostCPUBal = balanceTerm(hMaxCPU, hMeanCPU, spec.HostCPUBandLow, spec.HostCPUBandHigh)
		out.HostMemBal = balanceTerm(hMaxMem, hMeanMem, spec.HostMemBandLow, spec.HostMemBandHigh)
	} else if n > 0 {
		// Fallback when host_metrics are not present: approximate from service aggregates.
		out.HostCPUBal = balanceTerm(maxCPU, meanCPU, spec.HostCPUBandLow, spec.HostCPUBandHigh)
		out.HostMemBal = balanceTerm(maxMem, meanMem, spec.HostMemBandLow, spec.HostMemBandHigh)
	}

	out.InfraCost = ComputeInfraCostWeighted(scenario, cw)
	out.Churn = ComputeChurn(baseline, scenario)
	if m != nil {
		out.LocalityPenalty = math.Max(0, 1-m.GetLocalityHitRate())
		out.CrossZonePenalty = m.GetCrossZoneRequestFraction()
		out.TopologyPenalty = m.GetTopologyLatencyPenaltyMsMean()
	}

	out.EfficiencyScore = out.InfraCost +
		pw.ServiceCpuBalance*out.ServiceCPUBal +
		pw.ServiceMemoryBalance*out.ServiceMemBal +
		pw.HostCpuBalance*out.HostCPUBal +
		pw.HostMemoryBalance*out.HostMemBal +
		pw.Locality*out.LocalityPenalty +
		pw.CrossZone*out.CrossZonePenalty +
		pw.TopologyLatency*out.TopologyPenalty +
		cw.Churn*out.Churn

	out.Feasible = out.ViolationScore <= scoreEps
	return out
}

// CompareBatchScores implements feasibility-first ordering; returns true if a is better than b.
func CompareBatchScores(a, b BatchScore, hashA, hashB uint64) bool {
	if a.Feasible != b.Feasible {
		return a.Feasible && !b.Feasible
	}
	if math.Abs(a.ViolationScore-b.ViolationScore) > scoreEps {
		return a.ViolationScore < b.ViolationScore
	}
	if math.Abs(a.EfficiencyScore-b.EfficiencyScore) > scoreEps {
		return a.EfficiencyScore < b.EfficiencyScore
	}
	return hashA < hashB
}
