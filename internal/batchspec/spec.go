package batchspec

import (
	"errors"
	"fmt"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// BatchSpec is the normalized, server-side batch optimization configuration.
type BatchSpec struct {
	Proto *simulationv1.BatchOptimizationConfig

	SearchStrategy simulationv1.BatchSearchStrategy
	AllowedActions map[simulationv1.BatchScalingAction]struct{}
	// AllowedActionsOrdered is AllowedActions in stable enum order (deterministic neighbor generation).
	AllowedActionsOrdered []simulationv1.BatchScalingAction

	FreezeWorkload bool
	FreezePolicies bool

	MaxP95Ms                   float64
	MaxP99Ms                   float64
	MaxErrorRate               float64
	MinThroughput              float64
	MaxQueueDepthSum           float64
	MaxTopicBacklogDepthSum    float64
	MaxTopicConsumerLagSum     float64
	MaxQueueOldestMessageAgeMs float64
	MaxTopicOldestMessageAgeMs float64
	MaxQueueDropCount          float64
	MaxTopicDropCount          float64
	MaxQueueDlqCount           float64
	MaxTopicDlqCount           float64
	MinLocalityHitRate         float64
	MaxCrossZoneRequestFraction float64
	MaxTopologyLatencyPenaltyMeanMs float64

	ServiceCPUBandLow  float64
	ServiceCPUBandHigh float64
	ServiceMemBandLow  float64
	ServiceMemBandHigh float64
	HostCPUBandLow     float64
	HostCPUBandHigh    float64
	HostMemBandLow     float64
	HostMemBandHigh    float64

	MinHosts          int32
	MaxHosts          int32
	MinReplicasPerSvc int32
	MaxReplicasPerSvc int32
	MinCPUPerInst     float64
	MaxCPUPerInst     float64
	MinMemPerInst     float64
	MaxMemPerInst     float64
	MinHostCPUCores   int32
	MaxHostCPUCores   int32
	MinHostMemGB      int32
	MaxHostMemGB      int32

	ReplicaStep      int32
	ServiceCPURatio  float64
	ServiceMemRatio  float64
	HostCountStep    int32
	HostCPUStepCores int32
	HostMemStepGB    int32

	BeamWidth            int32
	MaxSearchDepth       int32
	MaxNeighborsPerState int32
	ReevalPerCandidate   int32
	InfeasibleBeamWidth  int32

	CostWeights    *simulationv1.BatchCostWeights
	PenaltyWeights *simulationv1.BatchPenaltyWeights

	EnableLocalRefinement       bool
	DeterministicCandidateSeeds bool
}

// DefaultBatchSpec returns recommended defaults merged with baseline-derived bounds.
func DefaultBatchSpec(base *config.Scenario) *BatchSpec {
	s := &BatchSpec{
		SearchStrategy:              simulationv1.BatchSearchStrategy_BATCH_SEARCH_STRATEGY_BEAM,
		FreezeWorkload:              true,
		FreezePolicies:              true,
		MaxP95Ms:                    500,
		MaxP99Ms:                    1000,
		MaxErrorRate:                0.05,
		MinThroughput:               0,
		ServiceCPUBandLow:           0.45,
		ServiceCPUBandHigh:          0.70,
		ServiceMemBandLow:           0.45,
		ServiceMemBandHigh:          0.75,
		HostCPUBandLow:              0.40,
		HostCPUBandHigh:             0.75,
		HostMemBandLow:              0.40,
		HostMemBandHigh:             0.80,
		ReplicaStep:                 1,
		ServiceCPURatio:             0.1,
		ServiceMemRatio:             0.1,
		HostCountStep:               1,
		HostCPUStepCores:            1,
		HostMemStepGB:               1,
		BeamWidth:                   8,
		MaxSearchDepth:              8,
		MaxNeighborsPerState:        24,
		ReevalPerCandidate:          3,
		InfeasibleBeamWidth:         4,
		EnableLocalRefinement:       true,
		DeterministicCandidateSeeds: true,
		CostWeights: &simulationv1.BatchCostWeights{
			ServiceCpu:      1,
			ServiceMemoryGb: 1,
			Replicas:        1,
			Hosts:           1,
			HostCpu:         1,
			HostMemoryGb:    1,
			Churn:           0.5,
		},
		PenaltyWeights: &simulationv1.BatchPenaltyWeights{
			P95:                  1,
			P99:                  1,
			ErrorRate:            1,
			Throughput:           1,
			ServiceCpuBalance:    1,
			ServiceMemoryBalance: 1,
			HostCpuBalance:       1,
			HostMemoryBalance:    1,
			QueueDepth:           1,
			TopicBacklog:         1,
			TopicLag:             1,
			QueueOldestAge:       1,
			TopicOldestAge:       1,
			QueueDrop:            1,
			TopicDrop:            1,
			QueueDlq:             1,
			TopicDlq:             1,
			Locality:             1,
			CrossZone:            1,
			TopologyLatency:      1,
		},
	}
	s.AllowedActions = allBatchScalingActions()
	s.AllowedActionsOrdered = orderedActionsFromMap(s.AllowedActions)

	initBoundsFromBaseline(s, base)
	return s
}

func allBatchScalingActions() map[simulationv1.BatchScalingAction]struct{} {
	m := make(map[simulationv1.BatchScalingAction]struct{})
	for _, a := range batchScalingActionEnumOrder() {
		m[a] = struct{}{}
	}
	return m
}

// batchScalingActionEnumOrder is a fixed order for deterministic search (not map iteration order).
func batchScalingActionEnumOrder() []simulationv1.BatchScalingAction {
	return []simulationv1.BatchScalingAction{
		simulationv1.BatchScalingAction_SERVICE_SCALE_OUT,
		simulationv1.BatchScalingAction_SERVICE_SCALE_IN,
		simulationv1.BatchScalingAction_SERVICE_SCALE_UP_CPU,
		simulationv1.BatchScalingAction_SERVICE_SCALE_DOWN_CPU,
		simulationv1.BatchScalingAction_SERVICE_SCALE_UP_MEMORY,
		simulationv1.BatchScalingAction_SERVICE_SCALE_DOWN_MEMORY,
		simulationv1.BatchScalingAction_HOST_SCALE_OUT,
		simulationv1.BatchScalingAction_HOST_SCALE_IN,
		simulationv1.BatchScalingAction_HOST_SCALE_UP_CPU,
		simulationv1.BatchScalingAction_HOST_SCALE_DOWN_CPU,
		simulationv1.BatchScalingAction_HOST_SCALE_UP_MEMORY,
		simulationv1.BatchScalingAction_HOST_SCALE_DOWN_MEMORY,
		simulationv1.BatchScalingAction_QUEUE_SCALE_UP_CONCURRENCY,
		simulationv1.BatchScalingAction_QUEUE_SCALE_DOWN_CONCURRENCY,
		simulationv1.BatchScalingAction_TOPIC_SUBSCRIBER_SCALE_UP_CONCURRENCY,
		simulationv1.BatchScalingAction_TOPIC_SUBSCRIBER_SCALE_DOWN_CONCURRENCY,
	}
}

func orderedActionsFromMap(m map[simulationv1.BatchScalingAction]struct{}) []simulationv1.BatchScalingAction {
	var out []simulationv1.BatchScalingAction
	for _, a := range batchScalingActionEnumOrder() {
		if _, ok := m[a]; ok {
			out = append(out, a)
		}
	}
	return out
}

func initBoundsFromBaseline(s *BatchSpec, base *config.Scenario) {
	if base == nil {
		return
	}
	nh := int32(len(base.Hosts))
	if nh < 1 {
		nh = 1
	}
	s.MinHosts = nh
	s.MaxHosts = nh + 4

	s.MinReplicasPerSvc = 1
	s.MaxReplicasPerSvc = 32

	s.MinCPUPerInst = 0.25
	s.MaxCPUPerInst = 32
	s.MinMemPerInst = 128
	s.MaxMemPerInst = 65536

	if len(base.Hosts) > 0 {
		c := int32(base.Hosts[0].Cores)
		if c < 1 {
			c = 1
		}
		s.MinHostCPUCores = c
		s.MaxHostCPUCores = c * 4
		gb := int32(base.Hosts[0].MemoryGB)
		if gb < 1 {
			gb = 16
		}
		s.MinHostMemGB = gb
		s.MaxHostMemGB = gb * 4
	}
}

// ParseBatchSpec merges client proto with defaults and validates.
func ParseBatchSpec(pb *simulationv1.BatchOptimizationConfig, base *config.Scenario) (*BatchSpec, error) {
	if pb == nil {
		return nil, errors.New("batch optimization config is nil")
	}
	s := DefaultBatchSpec(base)

	if pb.SearchStrategy != simulationv1.BatchSearchStrategy_BATCH_SEARCH_STRATEGY_UNSPECIFIED {
		s.SearchStrategy = pb.SearchStrategy
	}
	if len(pb.AllowedActions) > 0 {
		s.AllowedActions = make(map[simulationv1.BatchScalingAction]struct{})
		for _, a := range pb.AllowedActions {
			if a == simulationv1.BatchScalingAction_BATCH_SCALING_ACTION_UNSPECIFIED {
				continue
			}
			s.AllowedActions[a] = struct{}{}
		}
		if len(s.AllowedActions) == 0 {
			return nil, fmt.Errorf("allowed_actions has no valid entries")
		}
		s.AllowedActionsOrdered = orderedActionsFromMap(s.AllowedActions)
	}
	if pb.FreezeWorkload != nil {
		s.FreezeWorkload = *pb.FreezeWorkload
	}
	if pb.FreezePolicies != nil {
		s.FreezePolicies = *pb.FreezePolicies
	}

	if pb.GetMaxP95LatencyMs() > 0 {
		s.MaxP95Ms = pb.GetMaxP95LatencyMs()
	}
	if pb.GetMaxP99LatencyMs() > 0 {
		s.MaxP99Ms = pb.GetMaxP99LatencyMs()
	}
	if pb.GetMaxErrorRate() > 0 {
		s.MaxErrorRate = pb.GetMaxErrorRate()
	}
	if pb.GetMinThroughputRps() > 0 {
		s.MinThroughput = pb.GetMinThroughputRps()
	}
	if pb.GetMaxQueueDepthSum() > 0 {
		s.MaxQueueDepthSum = pb.GetMaxQueueDepthSum()
	}
	if pb.GetMaxTopicBacklogDepthSum() > 0 {
		s.MaxTopicBacklogDepthSum = pb.GetMaxTopicBacklogDepthSum()
	}
	if pb.GetMaxTopicConsumerLagSum() > 0 {
		s.MaxTopicConsumerLagSum = pb.GetMaxTopicConsumerLagSum()
	}
	if pb.GetMaxQueueOldestMessageAgeMs() > 0 {
		s.MaxQueueOldestMessageAgeMs = pb.GetMaxQueueOldestMessageAgeMs()
	}
	if pb.GetMaxTopicOldestMessageAgeMs() > 0 {
		s.MaxTopicOldestMessageAgeMs = pb.GetMaxTopicOldestMessageAgeMs()
	}
	if pb.GetMaxQueueDropCount() > 0 {
		s.MaxQueueDropCount = pb.GetMaxQueueDropCount()
	}
	if pb.GetMaxTopicDropCount() > 0 {
		s.MaxTopicDropCount = pb.GetMaxTopicDropCount()
	}
	if pb.GetMaxQueueDlqCount() > 0 {
		s.MaxQueueDlqCount = pb.GetMaxQueueDlqCount()
	}
	if pb.GetMaxTopicDlqCount() > 0 {
		s.MaxTopicDlqCount = pb.GetMaxTopicDlqCount()
	}
	if pb.GetMinLocalityHitRate() > 0 {
		s.MinLocalityHitRate = pb.GetMinLocalityHitRate()
	}
	if pb.GetMaxCrossZoneRequestFraction() > 0 {
		s.MaxCrossZoneRequestFraction = pb.GetMaxCrossZoneRequestFraction()
	}
	if pb.GetMaxTopologyLatencyPenaltyMeanMs() > 0 {
		s.MaxTopologyLatencyPenaltyMeanMs = pb.GetMaxTopologyLatencyPenaltyMeanMs()
	}
	if b := pb.GetServiceCpuUtilizationBand(); b != nil {
		if b.Low > 0 || b.High > 0 {
			s.ServiceCPUBandLow, s.ServiceCPUBandHigh = b.Low, b.High
		}
	}
	if b := pb.GetServiceMemoryUtilizationBand(); b != nil {
		if b.Low > 0 || b.High > 0 {
			s.ServiceMemBandLow, s.ServiceMemBandHigh = b.Low, b.High
		}
	}
	if b := pb.GetHostCpuUtilizationBand(); b != nil {
		if b.Low > 0 || b.High > 0 {
			s.HostCPUBandLow, s.HostCPUBandHigh = b.Low, b.High
		}
	}
	if b := pb.GetHostMemoryUtilizationBand(); b != nil {
		if b.Low > 0 || b.High > 0 {
			s.HostMemBandLow, s.HostMemBandHigh = b.Low, b.High
		}
	}

	if pb.GetMinHosts() > 0 {
		s.MinHosts = pb.GetMinHosts()
	}
	if pb.GetMaxHosts() > 0 {
		s.MaxHosts = pb.GetMaxHosts()
	}
	if pb.GetMinReplicasPerService() > 0 {
		s.MinReplicasPerSvc = pb.GetMinReplicasPerService()
	}
	if pb.GetMaxReplicasPerService() > 0 {
		s.MaxReplicasPerSvc = pb.GetMaxReplicasPerService()
	}
	if pb.GetMinCpuCoresPerInstance() > 0 {
		s.MinCPUPerInst = pb.GetMinCpuCoresPerInstance()
	}
	if pb.GetMaxCpuCoresPerInstance() > 0 {
		s.MaxCPUPerInst = pb.GetMaxCpuCoresPerInstance()
	}
	if pb.GetMinMemoryMbPerInstance() > 0 {
		s.MinMemPerInst = pb.GetMinMemoryMbPerInstance()
	}
	if pb.GetMaxMemoryMbPerInstance() > 0 {
		s.MaxMemPerInst = pb.GetMaxMemoryMbPerInstance()
	}
	if pb.GetMinHostCpuCores() > 0 {
		s.MinHostCPUCores = pb.GetMinHostCpuCores()
	}
	if pb.GetMaxHostCpuCores() > 0 {
		s.MaxHostCPUCores = pb.GetMaxHostCpuCores()
	}
	if pb.GetMinHostMemoryGb() > 0 {
		s.MinHostMemGB = pb.GetMinHostMemoryGb()
	}
	if pb.GetMaxHostMemoryGb() > 0 {
		s.MaxHostMemGB = pb.GetMaxHostMemoryGb()
	}

	if ss := pb.GetStepSizes(); ss != nil {
		if ss.ReplicaStep > 0 {
			s.ReplicaStep = ss.ReplicaStep
		}
		if ss.ServiceCpuRatio > 0 {
			s.ServiceCPURatio = ss.ServiceCpuRatio
		}
		if ss.ServiceMemoryRatio > 0 {
			s.ServiceMemRatio = ss.ServiceMemoryRatio
		}
		if ss.HostCountStep > 0 {
			s.HostCountStep = ss.HostCountStep
		}
		if ss.HostCpuStepCores > 0 {
			s.HostCPUStepCores = ss.HostCpuStepCores
		}
		if ss.HostMemoryStepGb > 0 {
			s.HostMemStepGB = ss.HostMemoryStepGb
		}
	}

	if pb.GetBeamWidth() > 0 {
		s.BeamWidth = pb.GetBeamWidth()
	}
	if pb.GetMaxSearchDepth() > 0 {
		s.MaxSearchDepth = pb.GetMaxSearchDepth()
	}
	if pb.GetMaxNeighborsPerState() > 0 {
		s.MaxNeighborsPerState = pb.GetMaxNeighborsPerState()
	}
	if pb.GetReevaluationsPerCandidate() > 0 {
		s.ReevalPerCandidate = pb.GetReevaluationsPerCandidate()
	}
	if pb.GetInfeasibleBeamWidth() > 0 {
		s.InfeasibleBeamWidth = pb.GetInfeasibleBeamWidth()
	}
	if cw := pb.GetCostWeights(); cw != nil {
		s.CostWeights = cw
	}
	if pw := pb.GetPenaltyWeights(); pw != nil {
		s.PenaltyWeights = pw
	}
	if pb.EnableLocalRefinement != nil {
		s.EnableLocalRefinement = *pb.EnableLocalRefinement
	}
	if pb.DeterministicCandidateSeeds != nil {
		s.DeterministicCandidateSeeds = *pb.DeterministicCandidateSeeds
	}

	s.Proto = pb

	if err := validateBatchSpec(s); err != nil {
		return nil, err
	}
	return s, nil
}

func validateBatchSpec(s *BatchSpec) error {
	if s.MaxP95Ms <= 0 || s.MaxP99Ms <= 0 {
		return fmt.Errorf("max_p95_latency_ms and max_p99_latency_ms must be > 0")
	}
	if s.MaxErrorRate <= 0 || s.MaxErrorRate > 1 {
		return fmt.Errorf("max_error_rate must be in (0,1]")
	}
	if s.MaxQueueDepthSum < 0 || s.MaxTopicBacklogDepthSum < 0 || s.MaxTopicConsumerLagSum < 0 {
		return fmt.Errorf("broker backlog/lag limits cannot be negative")
	}
	if s.MaxQueueOldestMessageAgeMs < 0 || s.MaxTopicOldestMessageAgeMs < 0 {
		return fmt.Errorf("broker oldest message age limits cannot be negative")
	}
	if s.MaxQueueDropCount < 0 || s.MaxTopicDropCount < 0 || s.MaxQueueDlqCount < 0 || s.MaxTopicDlqCount < 0 {
		return fmt.Errorf("broker drop/dlq limits cannot be negative")
	}
	if s.MinLocalityHitRate < 0 || s.MinLocalityHitRate > 1 {
		return fmt.Errorf("min_locality_hit_rate must be in [0,1]")
	}
	if s.MaxCrossZoneRequestFraction < 0 || s.MaxCrossZoneRequestFraction > 1 {
		return fmt.Errorf("max_cross_zone_request_fraction must be in [0,1]")
	}
	if s.MaxTopologyLatencyPenaltyMeanMs < 0 {
		return fmt.Errorf("max_topology_latency_penalty_mean_ms cannot be negative")
	}
	if s.ServiceCPUBandLow >= s.ServiceCPUBandHigh || s.ServiceMemBandLow >= s.ServiceMemBandHigh {
		return fmt.Errorf("invalid service utilization bands")
	}
	if s.HostCPUBandLow >= s.HostCPUBandHigh || s.HostMemBandLow >= s.HostMemBandHigh {
		return fmt.Errorf("invalid host utilization bands")
	}
	if s.MinHosts > s.MaxHosts {
		return fmt.Errorf("min_hosts > max_hosts")
	}
	if s.MinReplicasPerSvc > s.MaxReplicasPerSvc {
		return fmt.Errorf("min_replicas_per_service > max_replicas_per_service")
	}
	if s.MinCPUPerInst >= s.MaxCPUPerInst || s.MinMemPerInst >= s.MaxMemPerInst {
		return fmt.Errorf("invalid per-instance min/max")
	}
	if s.MinHostCPUCores > s.MaxHostCPUCores || s.MinHostMemGB > s.MaxHostMemGB {
		return fmt.Errorf("invalid host min/max")
	}
	if s.BeamWidth < 1 || s.MaxSearchDepth < 1 {
		return fmt.Errorf("beam_width and max_search_depth must be >= 1")
	}
	if s.MaxNeighborsPerState < 1 {
		return fmt.Errorf("max_neighbors_per_state must be >= 1")
	}
	if s.InfeasibleBeamWidth < 1 {
		s.InfeasibleBeamWidth = 1
	}
	if s.BeamWidth > 256 {
		s.BeamWidth = 256
	}
	if s.MaxSearchDepth > 64 {
		s.MaxSearchDepth = 64
	}
	return nil
}

// RefinementSpec returns a copy with smaller step sizes for a local polish pass after beam search.
func (s *BatchSpec) RefinementSpec() *BatchSpec {
	if s == nil {
		return nil
	}
	r := *s
	r.ReplicaStep = s.ReplicaStep / 2
	if r.ReplicaStep < 1 {
		r.ReplicaStep = 1
	}
	r.ServiceCPURatio = s.ServiceCPURatio * 0.5
	if r.ServiceCPURatio < 0.01 {
		r.ServiceCPURatio = 0.01
	}
	r.ServiceMemRatio = s.ServiceMemRatio * 0.5
	if r.ServiceMemRatio < 0.01 {
		r.ServiceMemRatio = 0.01
	}
	if s.HostCPUStepCores > 1 {
		r.HostCPUStepCores = s.HostCPUStepCores / 2
	}
	if s.HostMemStepGB > 1 {
		r.HostMemStepGB = s.HostMemStepGB / 2
	}
	if s.MaxNeighborsPerState > 8 {
		r.MaxNeighborsPerState = s.MaxNeighborsPerState / 2
	}
	if r.MaxNeighborsPerState < 8 {
		r.MaxNeighborsPerState = 8
	}
	r.AllowedActions = make(map[simulationv1.BatchScalingAction]struct{})
	for k := range s.AllowedActions {
		r.AllowedActions[k] = struct{}{}
	}
	r.AllowedActionsOrdered = make([]simulationv1.BatchScalingAction, len(s.AllowedActionsOrdered))
	copy(r.AllowedActionsOrdered, s.AllowedActionsOrdered)
	return &r
}
