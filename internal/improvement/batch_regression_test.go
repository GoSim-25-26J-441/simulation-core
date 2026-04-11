package improvement

import (
	"context"
	"reflect"
	"testing"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/batchspec"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestSortedBatchCandidateRunIDs_OrderMatchesCompareBatchScores(t *testing.T) {
	c := NewCandidateStore()
	c.Register(3, "r3")
	c.Register(2, "r2")
	c.Register(1, "r1")
	c.RecordBatchScore(1, BatchScore{Feasible: true, ViolationScore: 0, EfficiencyScore: 10})
	c.RecordBatchScore(2, BatchScore{Feasible: true, ViolationScore: 0, EfficiencyScore: 5})
	c.RecordBatchScore(3, BatchScore{Feasible: false, ViolationScore: 0, EfficiencyScore: 1})
	got := c.SortedBatchCandidateRunIDs()
	want := []string{"r2", "r1", "r3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestRunBatchBeamSearch_RepeatedRunsSameCandidateOrder(t *testing.T) {
	base := testBatchScenario()
	pb := &simulationv1.BatchOptimizationConfig{
		BeamWidth:            1,
		MaxSearchDepth:       1,
		MaxNeighborsPerState: 4,
	}
	spec, err := batchspec.ParseBatchSpec(pb, base)
	if err != nil {
		t.Fatal(err)
	}
	eval := func(*config.Scenario) (*simulationv1.RunMetrics, int, error) {
		return feasibleBatchMetrics(), 1, nil
	}
	run := func() []string {
		res, err := RunBatchBeamSearch(context.Background(), spec, base, 0, NewCandidateStore(), eval)
		if err != nil {
			t.Fatal(err)
		}
		return res.CandidateRunIDs
	}
	a := run()
	b := run()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("candidate order differed between runs: %v vs %v", a, b)
	}
}
