package batchspec

import (
	"testing"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestParseBatchSpecDefaults(t *testing.T) {
	base := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 4, MemoryGB: 16}},
		Services: []config.Service{
			{ID: "svc1", Replicas: 2, CPUCores: 1, MemoryMB: 512, Model: "cpu"},
		},
	}
	pb := &simulationv1.BatchOptimizationConfig{}
	spec, err := ParseBatchSpec(pb, base)
	if err != nil {
		t.Fatalf("ParseBatchSpec: %v", err)
	}
	if !spec.FreezeWorkload || !spec.FreezePolicies {
		t.Fatalf("expected default freeze_workload and freeze_policies true")
	}
	if spec.BeamWidth != 8 || spec.MaxSearchDepth != 8 {
		t.Fatalf("unexpected beam defaults: %d %d", spec.BeamWidth, spec.MaxSearchDepth)
	}
	if len(spec.AllowedActions) < 8 {
		t.Fatalf("expected default allowed actions populated")
	}
	if len(spec.AllowedActionsOrdered) != len(spec.AllowedActions) {
		t.Fatalf("ordered actions should match map size")
	}
	if !spec.EnableLocalRefinement || !spec.DeterministicCandidateSeeds {
		t.Fatalf("expected default enable_local_refinement and deterministic_candidate_seeds true when unset in proto")
	}
}

func TestOptionalBoolsExplicitFalseOverridesDefaults(t *testing.T) {
	base := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 4, MemoryGB: 16}},
		Services: []config.Service{
			{ID: "svc1", Replicas: 2, CPUCores: 1, MemoryMB: 512, Model: "cpu"},
		},
	}
	f := false
	pb := &simulationv1.BatchOptimizationConfig{
		EnableLocalRefinement:       &f,
		DeterministicCandidateSeeds: &f,
	}
	spec, err := ParseBatchSpec(pb, base)
	if err != nil {
		t.Fatalf("ParseBatchSpec: %v", err)
	}
	if spec.EnableLocalRefinement || spec.DeterministicCandidateSeeds {
		t.Fatalf("expected explicit false to override defaults")
	}
}

func TestParseBatchSpecBrokerGuardrails(t *testing.T) {
	base := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 4, MemoryGB: 16}},
		Services: []config.Service{
			{ID: "svc1", Replicas: 2, CPUCores: 1, MemoryMB: 512, Model: "cpu"},
		},
	}
	pb := &simulationv1.BatchOptimizationConfig{
		MaxQueueDepthSum:           100,
		MaxTopicBacklogDepthSum:    200,
		MaxTopicConsumerLagSum:     300,
		MaxQueueOldestMessageAgeMs: 400,
		MaxTopicOldestMessageAgeMs: 500,
		MaxQueueDropCount:          5,
		MaxTopicDropCount:          6,
		MaxQueueDlqCount:           7,
		MaxTopicDlqCount:           8,
	}
	spec, err := ParseBatchSpec(pb, base)
	if err != nil {
		t.Fatalf("ParseBatchSpec: %v", err)
	}
	if spec.MaxQueueDepthSum != 100 || spec.MaxTopicBacklogDepthSum != 200 || spec.MaxTopicConsumerLagSum != 300 {
		t.Fatalf("unexpected backlog guardrails: %+v", spec)
	}
	if spec.MaxQueueOldestMessageAgeMs != 400 || spec.MaxTopicOldestMessageAgeMs != 500 {
		t.Fatalf("unexpected oldest-age guardrails: %+v", spec)
	}
	if spec.MaxQueueDropCount != 5 || spec.MaxTopicDropCount != 6 || spec.MaxQueueDlqCount != 7 || spec.MaxTopicDlqCount != 8 {
		t.Fatalf("unexpected drop/dlq guardrails: %+v", spec)
	}
}
