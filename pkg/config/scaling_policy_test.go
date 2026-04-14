package config

import (
	"testing"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

func TestServiceAllowsBatchScalingActionDatabaseDefault(t *testing.T) {
	db := &Service{Kind: "database", ID: "db"}
	if ServiceAllowsBatchScalingAction(db, simulationv1.BatchScalingAction_SERVICE_SCALE_OUT) {
		t.Fatal("expected scale-out blocked for database without policy")
	}
	svc := &Service{ID: "x"}
	if !ServiceAllowsBatchScalingAction(svc, simulationv1.BatchScalingAction_SERVICE_SCALE_OUT) {
		t.Fatal("expected scale-out allowed for non-database without policy")
	}
}

func TestServiceAllowsBatchScalingActionExplicitPolicy(t *testing.T) {
	db := &Service{
		Kind: "database",
		ID:   "db",
		Scaling: &ScalingPolicy{
			Horizontal:     true,
			VerticalCPU:    false,
			VerticalMemory: true,
		},
	}
	if !ServiceAllowsBatchScalingAction(db, simulationv1.BatchScalingAction_SERVICE_SCALE_OUT) {
		t.Fatal("horizontal allowed")
	}
	if ServiceAllowsBatchScalingAction(db, simulationv1.BatchScalingAction_SERVICE_SCALE_UP_CPU) {
		t.Fatal("vertical CPU disallowed")
	}
}

func TestServiceAllowsVerticalFlags(t *testing.T) {
	svc := &Service{
		ID: "s",
		Scaling: &ScalingPolicy{
			Horizontal:     true,
			VerticalCPU:    false,
			VerticalMemory: true,
		},
	}
	if !ServiceAllowsHorizontalScaling(svc) {
		t.Fatal("horizontal")
	}
	if ServiceAllowsVerticalCPU(svc) {
		t.Fatal("vertical cpu blocked")
	}
	if !ServiceAllowsVerticalMemory(svc) {
		t.Fatal("vertical memory allowed")
	}
}
