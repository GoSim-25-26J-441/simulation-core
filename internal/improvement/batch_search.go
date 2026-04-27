package improvement

import (
	"context"
	"errors"
	"fmt"
	"sort"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/batchspec"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// BatchEvalFunc runs one logical evaluation: returns aggregated metrics and how many simulations were executed.
type BatchEvalFunc func(*config.Scenario) (*simulationv1.RunMetrics, int, error)

// BatchSearchResult is the outcome of a batch beam search.
type BatchSearchResult struct {
	BestScenario          *config.Scenario
	BestRunID             string
	BestScore             BatchScore
	Feasible              bool
	Summary               string
	Evaluations           int
	RefinementEvaluations int
	CandidateRunIDs       []string
	AllFeasibleEmpty      bool
	// EffectiveMaxEvaluations is the maxEvaluations argument passed to RunBatchBeamSearch (0 = no cap).
	EffectiveMaxEvaluations int
	// BudgetExhausted is true if the search stopped early because maxEvaluations would be exceeded.
	BudgetExhausted bool
}

type beamState struct {
	scenario *config.Scenario
	metrics  *simulationv1.RunMetrics
	score    BatchScore
	hash     uint64
	depth    int
}

// RunBatchBeamSearch runs constrained beam search over scaling actions.
func RunBatchBeamSearch(
	ctx context.Context,
	spec *batchspec.BatchSpec,
	baseline *config.Scenario,
	maxEvaluations int,
	cand *CandidateStore,
	eval BatchEvalFunc,
) (*BatchSearchResult, error) {
	if spec == nil || baseline == nil {
		return nil, fmt.Errorf("spec and baseline required")
	}
	if cand == nil {
		cand = NewCandidateStore()
	}
	visited := make(map[uint64]struct{})
	evalCount := 0
	budgetExhausted := false

	var globalBest beamState

	reev := int(spec.ReevalPerCandidate)
	if reev < 1 {
		reev = 1
	}

	tryEval := func(sc *config.Scenario) (beamState, bool, error) {
		if sc == nil {
			return beamState{}, false, fmt.Errorf("nil scenario")
		}
		h := batchspec.ConfigHash(sc)
		if _, ok := visited[h]; ok {
			return beamState{}, false, nil
		}
		if ctx.Err() != nil {
			return beamState{}, false, ctx.Err()
		}
		if maxEvaluations > 0 && evalCount+reev > maxEvaluations {
			return beamState{}, false, ErrBatchBudgetExhausted
		}
		m, cost, err := eval(sc)
		if err != nil {
			return beamState{}, false, err
		}
		evalCount += cost
		visited[h] = struct{}{}
		score := ComputeBatchScore(spec, baseline, sc, m)
		cand.RecordBatchScore(h, score)
		st := beamState{scenario: sc, metrics: m, score: score, hash: h}
		if globalBest.scenario == nil || CompareBatchScores(st.score, globalBest.score, st.hash, globalBest.hash) {
			globalBest = st
		}
		return st, true, nil
	}

	baseSt, ok, err := tryEval(cloneScenario(baseline))
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("baseline duplicate or empty")
	}
	baseSt.depth = 0

	frontier := []beamState{baseSt}

	depthLimit := int(spec.MaxSearchDepth)
	if depthLimit < 1 {
		depthLimit = 1
	}

	for depth := 0; depth < depthLimit; depth++ {
		var nextLayer []beamState
		for i := range frontier {
			st := &frontier[i]
			neighbors := GenerateBatchNeighbors(spec, baseline, st.scenario, st.metrics)
			for _, nsc := range neighbors {
				ns, evaluated, err := tryEval(nsc)
				if err != nil {
					if errors.Is(err, ErrBatchBudgetExhausted) {
						budgetExhausted = true
						goto finish
					}
					return nil, err
				}
				if !evaluated {
					continue
				}
				ns.depth = depth + 1
				nextLayer = append(nextLayer, ns)
			}
		}
		if len(nextLayer) == 0 {
			break
		}

		var feas, infeas []beamState
		for i := range nextLayer {
			s := &nextLayer[i]
			if s.score.Feasible {
				feas = append(feas, *s)
			} else {
				infeas = append(infeas, *s)
			}
		}
		sort.Slice(feas, func(i, j int) bool {
			return CompareBatchScores(feas[i].score, feas[j].score, feas[i].hash, feas[j].hash)
		})
		sort.Slice(infeas, func(i, j int) bool {
			return CompareBatchScores(infeas[i].score, infeas[j].score, infeas[i].hash, infeas[j].hash)
		})
		bw := int(spec.BeamWidth)
		if bw < 1 {
			bw = 8
		}
		ibw := int(spec.InfeasibleBeamWidth)
		if ibw < 1 {
			ibw = 4
		}
		var nf []beamState
		if len(feas) > bw {
			nf = append(nf, feas[:bw]...)
		} else {
			nf = append(nf, feas...)
		}
		if len(infeas) > ibw {
			nf = append(nf, infeas[:ibw]...)
		} else {
			nf = append(nf, infeas...)
		}
		frontier = nf
	}
finish:

	beamEvalCount := evalCount
	if spec.EnableLocalRefinement && globalBest.scenario != nil {
		refSpec := spec.RefinementSpec()
		if refSpec != nil {
			neighbors := GenerateBatchNeighbors(refSpec, baseline, globalBest.scenario, globalBest.metrics)
			for _, nsc := range neighbors {
				_, _, err := tryEval(nsc)
				if err != nil {
					if errors.Is(err, ErrBatchBudgetExhausted) {
						budgetExhausted = true
						break
					}
					return nil, err
				}
			}
		}
	}
	refineSimRuns := evalCount - beamEvalCount

	best := globalBest
	out := &BatchSearchResult{
		BestScenario:            cloneScenario(best.scenario),
		BestScore:               best.score,
		Feasible:                best.score.Feasible,
		Evaluations:             evalCount,
		RefinementEvaluations:   refineSimRuns,
		CandidateRunIDs:         cand.SortedBatchCandidateRunIDs(),
		EffectiveMaxEvaluations: maxEvaluations,
		BudgetExhausted:         budgetExhausted,
	}
	if h := batchspec.ConfigHash(out.BestScenario); h != 0 {
		out.BestRunID, _ = cand.Lookup(h)
	}
	if out.Feasible {
		out.Summary = fmt.Sprintf("feasible candidate violation=%.6g efficiency=%.6g evals=%d refine_sims=%d budget_cap=%d budget_exhausted=%v",
			best.score.ViolationScore, best.score.EfficiencyScore, evalCount, refineSimRuns, maxEvaluations, budgetExhausted)
	} else {
		out.Summary = fmt.Sprintf("no feasible candidate in search; least-violating violation=%.6g efficiency=%.6g evals=%d refine_sims=%d budget_cap=%d budget_exhausted=%v",
			best.score.ViolationScore, best.score.EfficiencyScore, evalCount, refineSimRuns, maxEvaluations, budgetExhausted)
		out.AllFeasibleEmpty = true
	}
	return out, nil
}
