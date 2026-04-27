package improvement

import (
	"context"
	"fmt"
	"strconv"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/batchspec"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/logger"
)

// RunBatchExperiment executes beam-search batch optimization.
// maxEvaluations caps total simulation runs (0 = unlimited). The simd executor applies a
// default cap when the client omits optimization.max_evaluations for batch runs.
func (o *Orchestrator) RunBatchExperiment(ctx context.Context, initial *config.Scenario, durationMs int64, pb *simulationv1.BatchOptimizationConfig, maxEvaluations int) (*ExperimentResult, error) {
	if initial == nil {
		return nil, fmt.Errorf("initial configuration is required")
	}
	spec, err := batchspec.ParseBatchSpec(pb, initial)
	if err != nil {
		return nil, err
	}

	cand := NewCandidateStore()
	o.batchCandMu.Lock()
	o.batchCandidateStore = cand
	o.batchCandMu.Unlock()
	defer func() {
		o.batchCandMu.Lock()
		o.batchCandidateStore = nil
		o.batchCandMu.Unlock()
	}()

	start := time.Now()
	eval := func(sc *config.Scenario) (*simulationv1.RunMetrics, int, error) {
		n := int(spec.ReevalPerCandidate)
		if n < 1 {
			n = 1
		}
		runs := make([]*simulationv1.RunMetrics, 0, n)
		for i := 0; i < n; i++ {
			var seed int64
			if spec.DeterministicCandidateSeeds {
				h := batchspec.ConfigHash(sc)
				seedBase, err := strconv.ParseInt(strconv.FormatUint(h%maxInt64Uint64, 10), 10, 64)
				if err != nil {
					return nil, 0, err
				}
				seed = seedBase ^ int64(i+1)
			}
			m, err := o.evaluateConfigurationMetrics(ctx, sc, durationMs, cand, seed)
			if err != nil {
				return nil, 0, err
			}
			runs = append(runs, m)
		}
		return AggregateRunMetrics(runs), n, nil
	}

	batchRes, err := RunBatchBeamSearch(ctx, spec, initial, maxEvaluations, cand, eval)
	if err != nil {
		return nil, err
	}
	logger.Info("batch beam search finished",
		"max_evaluations", maxEvaluations,
		"evaluations", batchRes.Evaluations,
		"budget_exhausted", batchRes.BudgetExhausted,
		"feasible", batchRes.Feasible,
		"violation_score", batchRes.BestScore.ViolationScore,
		"efficiency_score", batchRes.BestScore.EfficiencyScore,
	)

	result := &ExperimentResult{
		BestConfig: batchRes.BestScenario,
		// BestScore is legacy scalar compatibility: for batch runs this is efficiency only.
		// Feasibility and violation live in Batch (and Run batch_* fields after SetBatchRecommendation).
		BestScore:            batchRes.BestScore.EfficiencyScore,
		BestRunID:            batchRes.BestRunID,
		Iterations:           batchRes.Evaluations,
		TotalRuns:            cand.Len(),
		CompletedRuns:        cand.Len(),
		Duration:             time.Since(start),
		Converged:            true,
		ConvergenceReason:    "batch_beam",
		Batch:                batchRes,
		BatchCandidateRunIDs: cand.SortedBatchCandidateRunIDs(),
	}

	o.mu.RLock()
	for _, rid := range result.BatchCandidateRunIDs {
		if runCtx, ok := o.activeRuns[rid]; ok {
			result.Runs = append(result.Runs, runCtx)
		}
	}
	o.mu.RUnlock()

	if result.BestRunID == "" && batchRes.BestScenario != nil {
		if rid, ok := cand.Lookup(batchspec.ConfigHash(batchRes.BestScenario)); ok {
			result.BestRunID = rid
		}
	}

	return result, nil
}

const maxInt64Uint64 = ^uint64(0) >> 1
