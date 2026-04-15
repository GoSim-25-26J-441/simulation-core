package improvement

import (
	"fmt"
	"sort"
	"strings"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/batchspec"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// StaticCapacityOK applies cheap feasibility checks (aggregate CPU/memory vs hosts).
func StaticCapacityOK(s *config.Scenario) bool {
	if s == nil || len(s.Services) == 0 {
		return false
	}
	var needCPU float64
	var needMemMB float64
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
		needCPU += r * cpu
		needMemMB += r * mb
	}
	var capCPU float64
	var capMemMB float64
	for _, h := range s.Hosts {
		capCPU += float64(h.Cores)
		gb := h.MemoryGB
		if gb < 1 {
			gb = 16
		}
		capMemMB += float64(gb) * 1024
	}
	if capCPU <= 0 {
		capCPU = needCPU
	}
	if capMemMB <= 0 {
		capMemMB = needMemMB
	}
	return needCPU <= capCPU*1.001 && needMemMB <= capMemMB*1.001
}

// neighborStress is true when the current state looks overloaded vs SLOs or utilization bands.
func neighborStress(spec *batchspec.BatchSpec, m *simulationv1.RunMetrics) bool {
	if m == nil {
		return true
	}
	if spec.MaxP95Ms > 0 && m.GetLatencyP95Ms() > spec.MaxP95Ms*1.005 {
		return true
	}
	maxCPU, _, _, _, n := serviceUtilStats(m)
	if n > 0 && maxCPU > spec.ServiceCPUBandHigh {
		return true
	}
	tput := m.GetIngressThroughputRps()
	if tput <= 0 {
		tput = m.GetThroughputRps()
	}
	if spec.MinThroughput > 0 && tput < spec.MinThroughput*0.95 {
		return true
	}
	if spec.MaxQueueDepthSum > 0 && m.GetQueueDepthSum() > spec.MaxQueueDepthSum*1.01 {
		return true
	}
	if spec.MaxTopicBacklogDepthSum > 0 && m.GetTopicBacklogDepthSum() > spec.MaxTopicBacklogDepthSum*1.01 {
		return true
	}
	if spec.MaxTopicConsumerLagSum > 0 && m.GetTopicConsumerLagSum() > spec.MaxTopicConsumerLagSum*1.01 {
		return true
	}
	if spec.MaxQueueOldestMessageAgeMs > 0 && m.GetQueueOldestMessageAgeMs() > spec.MaxQueueOldestMessageAgeMs*1.01 {
		return true
	}
	if spec.MaxTopicOldestMessageAgeMs > 0 && m.GetTopicOldestMessageAgeMs() > spec.MaxTopicOldestMessageAgeMs*1.01 {
		return true
	}
	if spec.MaxQueueDropCount > 0 && float64(m.GetQueueDropCountTotal()) > spec.MaxQueueDropCount*1.01 {
		return true
	}
	if spec.MaxTopicDropCount > 0 && float64(m.GetTopicDropCountTotal()) > spec.MaxTopicDropCount*1.01 {
		return true
	}
	if spec.MaxQueueDlqCount > 0 && float64(m.GetQueueDlqCountTotal()) > spec.MaxQueueDlqCount*1.01 {
		return true
	}
	if spec.MaxTopicDlqCount > 0 && float64(m.GetTopicDlqCountTotal()) > spec.MaxTopicDlqCount*1.01 {
		return true
	}
	return false
}

func capacityDelta(cur, nb *config.Scenario) float64 {
	if cur == nil || nb == nil {
		return 0
	}
	var d float64
	for i := range nb.Services {
		if i >= len(cur.Services) {
			break
		}
		dr := float64(nb.Services[i].Replicas - cur.Services[i].Replicas)
		dc := nb.Services[i].CPUCores - cur.Services[i].CPUCores
		dm := nb.Services[i].MemoryMB - cur.Services[i].MemoryMB
		d += dr*50 + dc*2 + dm/256
	}
	d += float64(len(nb.Hosts)-len(cur.Hosts)) * 80
	return d
}

func serviceIndexByID(s *config.Scenario) map[string]int {
	out := make(map[string]int, len(s.Services))
	for i, svc := range s.Services {
		out[svc.ID] = i
	}
	return out
}

func servicePressureScore(lastMetrics *simulationv1.RunMetrics) map[string]float64 {
	scores := map[string]float64{}
	if lastMetrics == nil {
		return scores
	}
	for _, sm := range lastMetrics.GetServiceMetrics() {
		if sm == nil || strings.TrimSpace(sm.GetServiceName()) == "" {
			continue
		}
		scores[sm.GetServiceName()] = sm.GetCpuUtilization() + sm.GetMemoryUtilization() +
			float64(sm.GetConcurrentRequests())*0.01 + float64(sm.GetQueueLength())*0.05
	}
	return scores
}

func brokerConsumerTargetServiceIndices(cur *config.Scenario, lastMetrics *simulationv1.RunMetrics) []int {
	if cur == nil {
		return nil
	}
	byID := serviceIndexByID(cur)
	seen := map[int]bool{}
	for _, svc := range cur.Services {
		if svc.Behavior == nil {
			continue
		}
		if svc.Behavior.Queue != nil {
			target := strings.TrimSpace(svc.Behavior.Queue.ConsumerTarget)
			if parts := strings.SplitN(target, ":", 2); len(parts) == 2 {
				if idx, ok := byID[strings.TrimSpace(parts[0])]; ok {
					seen[idx] = true
				}
			}
		}
		if svc.Behavior.Topic != nil {
			for _, sub := range svc.Behavior.Topic.Subscribers {
				target := strings.TrimSpace(sub.ConsumerTarget)
				if parts := strings.SplitN(target, ":", 2); len(parts) == 2 {
					if idx, ok := byID[strings.TrimSpace(parts[0])]; ok {
						seen[idx] = true
					}
				}
			}
		}
	}
	indices := make([]int, 0, len(seen))
	for idx := range seen {
		indices = append(indices, idx)
	}
	scores := servicePressureScore(lastMetrics)
	sort.SliceStable(indices, func(i, j int) bool {
		si := cur.Services[indices[i]].ID
		sj := cur.Services[indices[j]].ID
		pi := scores[si]
		pj := scores[sj]
		if pi == pj {
			return si < sj
		}
		return pi > pj
	})
	return indices
}

func orderNeighborsForExpansion(spec *batchspec.BatchSpec, cur *config.Scenario, lastMetrics *simulationv1.RunMetrics, neighbors []*config.Scenario) []*config.Scenario {
	if len(neighbors) < 2 {
		return neighbors
	}
	stress := neighborStress(spec, lastMetrics)
	sort.SliceStable(neighbors, func(i, j int) bool {
		pi := capacityDelta(cur, neighbors[i])
		pj := capacityDelta(cur, neighbors[j])
		if stress {
			return pi > pj
		}
		return pi < pj
	})
	return neighbors
}

// GenerateBatchNeighbors expands one step from cur according to spec and baseline bounds.
// lastMetrics, when non-nil, influences neighbor ordering (scale-out/up first under stress).
func GenerateBatchNeighbors(spec *batchspec.BatchSpec, baseline, cur *config.Scenario, lastMetrics *simulationv1.RunMetrics) []*config.Scenario {
	if spec == nil || cur == nil {
		return nil
	}
	var out []*config.Scenario
	add := func(ns *config.Scenario) {
		if ns == nil || !StaticCapacityOK(ns) {
			return
		}
		if !withinBatchBounds(spec, baseline, ns) {
			return
		}
		out = append(out, ns)
	}

	actions := spec.AllowedActionsOrdered
	if len(actions) == 0 {
		for a := range spec.AllowedActions {
			actions = append(actions, a)
		}
		sort.Slice(actions, func(i, j int) bool { return actions[i] < actions[j] })
	}
	for _, act := range actions {
		switch act {
		case simulationv1.BatchScalingAction_SERVICE_SCALE_OUT:
			scaleIndices := make([]int, 0, len(cur.Services))
			if neighborStress(spec, lastMetrics) {
				scaleIndices = brokerConsumerTargetServiceIndices(cur, lastMetrics)
			}
			if len(scaleIndices) == 0 {
				for i := range cur.Services {
					scaleIndices = append(scaleIndices, i)
				}
			}
			for _, i := range scaleIndices {
				if !config.ServiceAllowsBatchScalingAction(&cur.Services[i], act) {
					continue
				}
				ns := cloneScenario(cur)
				if int32(ns.Services[i].Replicas) >= spec.MaxReplicasPerSvc {
					continue
				}
				ns.Services[i].Replicas += int(spec.ReplicaStep)
				if int32(ns.Services[i].Replicas) > spec.MaxReplicasPerSvc {
					ns.Services[i].Replicas = int(spec.MaxReplicasPerSvc)
				}
				add(ns)
			}
		case simulationv1.BatchScalingAction_SERVICE_SCALE_IN:
			for i := range cur.Services {
				if !config.ServiceAllowsBatchScalingAction(&cur.Services[i], act) {
					continue
				}
				ns := cloneScenario(cur)
				if ns.Services[i].Replicas-int(spec.ReplicaStep) < int(spec.MinReplicasPerSvc) {
					continue
				}
				ns.Services[i].Replicas -= int(spec.ReplicaStep)
				if ns.Services[i].Replicas < int(spec.MinReplicasPerSvc) {
					ns.Services[i].Replicas = int(spec.MinReplicasPerSvc)
				}
				add(ns)
			}
		case simulationv1.BatchScalingAction_SERVICE_SCALE_UP_CPU:
			scaleIndices := make([]int, 0, len(cur.Services))
			if neighborStress(spec, lastMetrics) {
				scaleIndices = brokerConsumerTargetServiceIndices(cur, lastMetrics)
			}
			if len(scaleIndices) == 0 {
				for i := range cur.Services {
					scaleIndices = append(scaleIndices, i)
				}
			}
			for _, i := range scaleIndices {
				if !config.ServiceAllowsBatchScalingAction(&cur.Services[i], act) {
					continue
				}
				ns := cloneScenario(cur)
				cpu := ns.Services[i].CPUCores
				if cpu <= 0 {
					cpu = defaultServiceCPUCores
				}
				ns.Services[i].CPUCores = cpu * (1 + spec.ServiceCPURatio)
				if ns.Services[i].CPUCores > spec.MaxCPUPerInst {
					ns.Services[i].CPUCores = spec.MaxCPUPerInst
				}
				add(ns)
			}
		case simulationv1.BatchScalingAction_SERVICE_SCALE_DOWN_CPU:
			for i := range cur.Services {
				if !config.ServiceAllowsBatchScalingAction(&cur.Services[i], act) {
					continue
				}
				ns := cloneScenario(cur)
				cpu := ns.Services[i].CPUCores
				if cpu <= 0 {
					cpu = defaultServiceCPUCores
				}
				ns.Services[i].CPUCores = cpu * (1 - spec.ServiceCPURatio)
				if ns.Services[i].CPUCores < spec.MinCPUPerInst {
					ns.Services[i].CPUCores = spec.MinCPUPerInst
				}
				add(ns)
			}
		case simulationv1.BatchScalingAction_SERVICE_SCALE_UP_MEMORY:
			scaleIndices := make([]int, 0, len(cur.Services))
			if neighborStress(spec, lastMetrics) {
				scaleIndices = brokerConsumerTargetServiceIndices(cur, lastMetrics)
			}
			if len(scaleIndices) == 0 {
				for i := range cur.Services {
					scaleIndices = append(scaleIndices, i)
				}
			}
			for _, i := range scaleIndices {
				if !config.ServiceAllowsBatchScalingAction(&cur.Services[i], act) {
					continue
				}
				ns := cloneScenario(cur)
				mb := ns.Services[i].MemoryMB
				if mb <= 0 {
					mb = defaultServiceMemoryMB
				}
				ns.Services[i].MemoryMB = mb * (1 + spec.ServiceMemRatio)
				if ns.Services[i].MemoryMB > spec.MaxMemPerInst {
					ns.Services[i].MemoryMB = spec.MaxMemPerInst
				}
				add(ns)
			}
		case simulationv1.BatchScalingAction_SERVICE_SCALE_DOWN_MEMORY:
			for i := range cur.Services {
				if !config.ServiceAllowsBatchScalingAction(&cur.Services[i], act) {
					continue
				}
				ns := cloneScenario(cur)
				mb := ns.Services[i].MemoryMB
				if mb <= 0 {
					mb = defaultServiceMemoryMB
				}
				ns.Services[i].MemoryMB = mb * (1 - spec.ServiceMemRatio)
				if ns.Services[i].MemoryMB < spec.MinMemPerInst {
					ns.Services[i].MemoryMB = spec.MinMemPerInst
				}
				add(ns)
			}
		case simulationv1.BatchScalingAction_HOST_SCALE_OUT:
			if len(cur.Hosts) == 0 {
				break
			}
			ns := cloneScenario(cur)
			if int32(len(ns.Hosts)) >= spec.MaxHosts {
				break
			}
			h0 := ns.Hosts[0]
			nh := len(ns.Hosts) + 1
			ns.Hosts = append(ns.Hosts, config.Host{
				ID:       fmt.Sprintf("host-%d", nh),
				Cores:    h0.Cores,
				MemoryGB: h0.MemoryGB,
			})
			add(ns)
		case simulationv1.BatchScalingAction_HOST_SCALE_IN:
			if len(cur.Hosts) <= int(spec.MinHosts) {
				break
			}
			ns := cloneScenario(cur)
			ns.Hosts = ns.Hosts[:len(ns.Hosts)-1]
			add(ns)
		case simulationv1.BatchScalingAction_HOST_SCALE_UP_CPU:
			for i := range cur.Hosts {
				ns := cloneScenario(cur)
				ns.Hosts[i].Cores += int(spec.HostCPUStepCores)
				if int32(ns.Hosts[i].Cores) > spec.MaxHostCPUCores {
					ns.Hosts[i].Cores = int(spec.MaxHostCPUCores)
				}
				add(ns)
			}
		case simulationv1.BatchScalingAction_HOST_SCALE_DOWN_CPU:
			for i := range cur.Hosts {
				ns := cloneScenario(cur)
				ns.Hosts[i].Cores -= int(spec.HostCPUStepCores)
				if ns.Hosts[i].Cores < int(spec.MinHostCPUCores) {
					ns.Hosts[i].Cores = int(spec.MinHostCPUCores)
				}
				add(ns)
			}
		case simulationv1.BatchScalingAction_HOST_SCALE_UP_MEMORY:
			for i := range cur.Hosts {
				ns := cloneScenario(cur)
				ns.Hosts[i].MemoryGB += int(spec.HostMemStepGB)
				if int32(ns.Hosts[i].MemoryGB) > spec.MaxHostMemGB {
					ns.Hosts[i].MemoryGB = int(spec.MaxHostMemGB)
				}
				add(ns)
			}
		case simulationv1.BatchScalingAction_HOST_SCALE_DOWN_MEMORY:
			for i := range cur.Hosts {
				ns := cloneScenario(cur)
				ns.Hosts[i].MemoryGB -= int(spec.HostMemStepGB)
				if ns.Hosts[i].MemoryGB < int(spec.MinHostMemGB) {
					ns.Hosts[i].MemoryGB = int(spec.MinHostMemGB)
				}
				add(ns)
			}
		case simulationv1.BatchScalingAction_QUEUE_SCALE_UP_CONCURRENCY:
			for i := range cur.Services {
				svc := &cur.Services[i]
				if svc.Behavior == nil || svc.Behavior.Queue == nil {
					continue
				}
				ns := cloneScenario(cur)
				q := ns.Services[i].Behavior.Queue
				curC := q.ConsumerConcurrency
				if curC <= 0 {
					curC = 1
				}
				maxC := q.MaxConsumerConcurrency
				if maxC <= 0 {
					maxC = int(spec.MaxReplicasPerSvc)
					if maxC < 1 {
						maxC = 64
					}
				}
				if curC >= maxC {
					continue
				}
				next := curC + int(spec.ReplicaStep)
				if next > maxC {
					next = maxC
				}
				q.ConsumerConcurrency = next
				add(ns)
			}
		case simulationv1.BatchScalingAction_QUEUE_SCALE_DOWN_CONCURRENCY:
			for i := range cur.Services {
				svc := &cur.Services[i]
				if svc.Behavior == nil || svc.Behavior.Queue == nil {
					continue
				}
				ns := cloneScenario(cur)
				q := ns.Services[i].Behavior.Queue
				curC := q.ConsumerConcurrency
				if curC <= 0 {
					curC = 1
				}
				minC := q.MinConsumerConcurrency
				if minC <= 0 {
					minC = 1
				}
				if curC <= minC {
					continue
				}
				next := curC - int(spec.ReplicaStep)
				if next < minC {
					next = minC
				}
				q.ConsumerConcurrency = next
				add(ns)
			}
		case simulationv1.BatchScalingAction_TOPIC_SUBSCRIBER_SCALE_UP_CONCURRENCY:
			for i := range cur.Services {
				svc := &cur.Services[i]
				if svc.Behavior == nil || svc.Behavior.Topic == nil {
					continue
				}
				for j := range svc.Behavior.Topic.Subscribers {
					ns := cloneScenario(cur)
					sub := &ns.Services[i].Behavior.Topic.Subscribers[j]
					curC := sub.ConsumerConcurrency
					if curC <= 0 {
						curC = 1
					}
					maxC := sub.MaxConsumerConcurrency
					if maxC <= 0 {
						maxC = int(spec.MaxReplicasPerSvc)
						if maxC < 1 {
							maxC = 64
						}
					}
					if curC >= maxC {
						continue
					}
					next := curC + int(spec.ReplicaStep)
					if next > maxC {
						next = maxC
					}
					sub.ConsumerConcurrency = next
					add(ns)
				}
			}
		case simulationv1.BatchScalingAction_TOPIC_SUBSCRIBER_SCALE_DOWN_CONCURRENCY:
			for i := range cur.Services {
				svc := &cur.Services[i]
				if svc.Behavior == nil || svc.Behavior.Topic == nil {
					continue
				}
				for j := range svc.Behavior.Topic.Subscribers {
					ns := cloneScenario(cur)
					sub := &ns.Services[i].Behavior.Topic.Subscribers[j]
					curC := sub.ConsumerConcurrency
					if curC <= 0 {
						curC = 1
					}
					minC := sub.MinConsumerConcurrency
					if minC <= 0 {
						minC = 1
					}
					if curC <= minC {
						continue
					}
					next := curC - int(spec.ReplicaStep)
					if next < minC {
						next = minC
					}
					sub.ConsumerConcurrency = next
					add(ns)
				}
			}
		}
	}

	// Workload / policy neighbors when not frozen (capacity and bounds still apply).
	if !spec.FreezeWorkload {
		for i := range cur.Workload {
			for _, mul := range []float64{1.1, 0.9} {
				ns := cloneScenario(cur)
				ns.Workload[i].Arrival.RateRPS *= mul
				if ns.Workload[i].Arrival.RateRPS < 0.01 {
					ns.Workload[i].Arrival.RateRPS = 0.01
				}
				add(ns)
			}
		}
	}
	if !spec.FreezePolicies && cur.Policies != nil && cur.Policies.Autoscaling != nil {
		ns := cloneScenario(cur)
		ns.Policies.Autoscaling.Enabled = !ns.Policies.Autoscaling.Enabled
		add(ns)
	}

	out = orderNeighborsForExpansion(spec, cur, lastMetrics, out)

	// Cap neighbor count
	maxN := int(spec.MaxNeighborsPerState)
	if maxN <= 0 {
		maxN = 24
	}
	if len(out) > maxN {
		out = out[:maxN]
	}
	return out
}

func withinBatchBounds(spec *batchspec.BatchSpec, base, cur *config.Scenario) bool {
	if spec == nil || cur == nil {
		return false
	}
	if int32(len(cur.Hosts)) < spec.MinHosts || int32(len(cur.Hosts)) > spec.MaxHosts {
		return false
	}
	for _, svc := range cur.Services {
		if int32(svc.Replicas) < spec.MinReplicasPerSvc || int32(svc.Replicas) > spec.MaxReplicasPerSvc {
			return false
		}
		if svc.CPUCores < spec.MinCPUPerInst || svc.CPUCores > spec.MaxCPUPerInst {
			return false
		}
		if svc.MemoryMB < spec.MinMemPerInst || svc.MemoryMB > spec.MaxMemPerInst {
			return false
		}
		if svc.Behavior != nil && svc.Behavior.Queue != nil {
			q := svc.Behavior.Queue
			if q.MinConsumerConcurrency > 0 && q.ConsumerConcurrency > 0 && q.ConsumerConcurrency < q.MinConsumerConcurrency {
				return false
			}
			if q.MaxConsumerConcurrency > 0 && q.ConsumerConcurrency > 0 && q.ConsumerConcurrency > q.MaxConsumerConcurrency {
				return false
			}
		}
		if svc.Behavior != nil && svc.Behavior.Topic != nil {
			for _, sub := range svc.Behavior.Topic.Subscribers {
				if sub.MinConsumerConcurrency > 0 && sub.ConsumerConcurrency > 0 && sub.ConsumerConcurrency < sub.MinConsumerConcurrency {
					return false
				}
				if sub.MaxConsumerConcurrency > 0 && sub.ConsumerConcurrency > 0 && sub.ConsumerConcurrency > sub.MaxConsumerConcurrency {
					return false
				}
			}
		}
	}
	for _, h := range cur.Hosts {
		if int32(h.Cores) < spec.MinHostCPUCores || int32(h.Cores) > spec.MaxHostCPUCores {
			return false
		}
		gb := h.MemoryGB
		if gb < 1 {
			gb = 16
		}
		if int32(gb) < spec.MinHostMemGB || int32(gb) > spec.MaxHostMemGB {
			return false
		}
	}
	_ = base
	return true
}
