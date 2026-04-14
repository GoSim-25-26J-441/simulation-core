package simd

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/batchspec"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/utils"
	"google.golang.org/protobuf/proto"
)

type RunRecord struct {
	Run                 *simulationv1.Run
	Input               *simulationv1.RunInput
	Metrics             *simulationv1.RunMetrics
	Collector           *metrics.Collector
	OptimizationHistory []*simulationv1.OptimizationStep // Online controller steps; backend can persist to run.metadata.optimization_history
	// FinalConfig is a snapshot of the effective RunConfiguration taken before executor cleanup
	// (placements, replicas, workload). Populated for terminal runs when the simulator still had state.
	FinalConfig *simulationv1.RunConfiguration
}

type RunStore struct {
	mu           sync.RWMutex
	runs         map[string]*RunRecord
	onlineLimits OnlineRunLimits
}

func NewRunStore() *RunStore {
	return &RunStore{
		runs:         make(map[string]*RunRecord),
		onlineLimits: DefaultOnlineRunLimits(),
	}
}

// SetOnlineLimits replaces server-side online run defaults and caps (e.g. from environment).
func (s *RunStore) SetOnlineLimits(l OnlineRunLimits) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onlineLimits = l
}

// OnlineLimits returns a copy of the configured online run limits.
func (s *RunStore) OnlineLimits() OnlineRunLimits {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.onlineLimits
}

// CountRunningOnline returns how many runs are RUNNING with optimization.online set.
func (s *RunStore) CountRunningOnline() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, rec := range s.runs {
		if rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_RUNNING &&
			rec.Input != nil && rec.Input.Optimization != nil && rec.Input.Optimization.Online {
			n++
		}
	}
	return n
}

func nowUnixMs() int64 {
	return time.Now().UTC().UnixMilli()
}

func validateBatchOptimizationInput(input *simulationv1.RunInput) error {
	if input == nil || input.Optimization == nil || input.Optimization.GetBatch() == nil {
		return nil
	}
	if input.Optimization.GetOnline() {
		return fmt.Errorf("batch optimization cannot be used with online=true")
	}
	scenario, err := config.ParseScenarioYAML([]byte(input.GetScenarioYaml()))
	if err != nil {
		return fmt.Errorf("batch optimization: invalid scenario yaml: %w", err)
	}
	if _, err := batchspec.ParseBatchSpec(input.Optimization.GetBatch(), scenario); err != nil {
		return fmt.Errorf("batch optimization: %w", err)
	}
	return nil
}

func (s *RunStore) Create(runID string, input *simulationv1.RunInput) (*RunRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateBatchOptimizationInput(input); err != nil {
		return nil, err
	}

	if runID == "" {
		runID = utils.GenerateRunID()
	}
	// Validate run ID to avoid route parsing ambiguity
	if strings.ContainsAny(runID, ":/") {
		return nil, fmt.Errorf("run ID cannot contain ':' or '/' characters: %s", runID)
	}
	if _, exists := s.runs[runID]; exists {
		return nil, fmt.Errorf("run already exists: %s", runID)
	}

	clonedInput := cloneRunInput(input)
	if err := PrepareOnlineRunInput(clonedInput, s.onlineLimits); err != nil {
		return nil, err
	}
	rec := &RunRecord{
		Run: &simulationv1.Run{
			Id:              runID,
			Status:          simulationv1.RunStatus_RUN_STATUS_PENDING,
			CreatedAtUnixMs: nowUnixMs(),
		},
		Input:   clonedInput,
		Metrics: nil,
	}
	s.runs[runID] = rec
	return cloneRunRecord(rec), nil
}

func (s *RunStore) Get(runID string) (*RunRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.runs[runID]
	if !ok {
		return nil, false
	}
	return cloneRunRecord(rec), true
}

func (s *RunStore) List(limit int) []*RunRecord {
	return s.ListFiltered(limit, 0, simulationv1.RunStatus_RUN_STATUS_UNSPECIFIED)
}

// ListFiltered returns runs with pagination and optional status filter
// limit: maximum number of runs to return (default: 50)
// offset: number of runs to skip (default: 0)
// status: filter by status (RUN_STATUS_UNSPECIFIED means no filter)
func (s *RunStore) ListFiltered(limit, offset int, status simulationv1.RunStatus) []*RunRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	// Collect all matching runs
	allRuns := make([]*RunRecord, 0, len(s.runs))
	for _, rec := range s.runs {
		// Filter by status if specified
		if status != simulationv1.RunStatus_RUN_STATUS_UNSPECIFIED && rec.Run.Status != status {
			continue
		}
		allRuns = append(allRuns, cloneRunRecord(rec))
	}

	// Sort by creation time (newest first)
	sortRunRecords(allRuns)

	// Apply pagination
	start := offset
	if start > len(allRuns) {
		return []*RunRecord{}
	}
	end := start + limit
	if end > len(allRuns) {
		end = len(allRuns)
	}

	if start >= end {
		return []*RunRecord{}
	}

	return allRuns[start:end]
}

// SetStatusRunningWithOnlineConcurrencyGuard atomically enforces MaxConcurrentOnlineRuns
// (when > 0) and transitions PENDING -> RUNNING under a single store lock so concurrent
// Start calls cannot oversubscribe the cap.
func (s *RunStore) SetStatusRunningWithOnlineConcurrencyGuard(runID string) (*RunRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.runs[runID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}

	switch rec.Run.Status {
	case simulationv1.RunStatus_RUN_STATUS_RUNNING:
		return cloneRunRecord(rec), nil
	case simulationv1.RunStatus_RUN_STATUS_COMPLETED,
		simulationv1.RunStatus_RUN_STATUS_FAILED,
		simulationv1.RunStatus_RUN_STATUS_CANCELLED,
		simulationv1.RunStatus_RUN_STATUS_STOPPED:
		return nil, fmt.Errorf("%w: %s", ErrRunTerminal, runID)
	}

	opt := rec.Input.GetOptimization()
	if opt != nil && opt.Online {
		lim := s.onlineLimits
		if lim.MaxConcurrentOnlineRuns > 0 {
			var n int
			for _, r := range s.runs {
				if r == nil || r.Run == nil {
					continue
				}
				if r.Run.Status == simulationv1.RunStatus_RUN_STATUS_RUNNING &&
					r.Input != nil && r.Input.GetOptimization() != nil && r.Input.GetOptimization().GetOnline() {
					n++
				}
			}
			if n >= lim.MaxConcurrentOnlineRuns {
				return nil, fmt.Errorf("%w: maximum concurrent online runs (%d) reached", ErrOnlineRunConcurrencyLimit, lim.MaxConcurrentOnlineRuns)
			}
		}
	}

	rec.Run.Status = simulationv1.RunStatus_RUN_STATUS_RUNNING
	if rec.Run.StartedAtUnixMs == 0 {
		rec.Run.StartedAtUnixMs = nowUnixMs()
	}
	return cloneRunRecord(rec), nil
}

func (s *RunStore) SetStatus(runID string, status simulationv1.RunStatus, errMsg string) (*RunRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.runs[runID]
	if !ok {
		return nil, fmt.Errorf("run not found: %s", runID)
	}

	rec.Run.Status = status
	if errMsg != "" {
		rec.Run.Error = errMsg
	}

	switch status {
	case simulationv1.RunStatus_RUN_STATUS_RUNNING:
		if rec.Run.StartedAtUnixMs == 0 {
			rec.Run.StartedAtUnixMs = nowUnixMs()
		}
	case simulationv1.RunStatus_RUN_STATUS_COMPLETED,
		simulationv1.RunStatus_RUN_STATUS_FAILED,
		simulationv1.RunStatus_RUN_STATUS_CANCELLED,
		simulationv1.RunStatus_RUN_STATUS_STOPPED:
		rec.Run.EndedAtUnixMs = nowUnixMs()
	}

	return cloneRunRecord(rec), nil
}

// SetOnlineCompletionReason sets Run.online_completion_reason (for COMPLETED online runs).
func (s *RunStore) SetOnlineCompletionReason(runID, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.runs[runID]
	if !ok {
		return fmt.Errorf("run not found: %s", runID)
	}
	rec.Run.OnlineCompletionReason = reason
	return nil
}

func (s *RunStore) SetMetrics(runID string, metrics *simulationv1.RunMetrics) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.runs[runID]
	if !ok {
		return fmt.Errorf("run not found: %s", runID)
	}
	rec.Metrics = cloneRunMetrics(metrics)
	return nil
}

// SetFinalConfiguration stores a cloned effective run configuration (e.g. before executor cleanup).
// Pass nil to clear; non-nil replaces any previous snapshot.
func (s *RunStore) SetFinalConfiguration(runID string, cfg *simulationv1.RunConfiguration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.runs[runID]
	if !ok {
		return fmt.Errorf("run not found: %s", runID)
	}
	if cfg == nil {
		rec.FinalConfig = nil
		return nil
	}
	rec.FinalConfig = proto.Clone(cfg).(*simulationv1.RunConfiguration)
	return nil
}

// SetBatchRecommendation stores batch optimization summary fields on the parent run.
// For batch runs, Run.best_score (via SetOptimizationResult) may still reflect efficiency-only for legacy
// compatibility; clients should treat batch_recommendation_feasible, batch_violation_score,
// batch_efficiency_score, and batch_recommendation_summary as the full batch outcome.
func (s *RunStore) SetBatchRecommendation(runID string, feasible bool, violationScore, efficiencyScore float64, summary string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.runs[runID]
	if !ok {
		return fmt.Errorf("run not found: %s", runID)
	}
	rec.Run.BatchRecommendationFeasible = feasible
	rec.Run.BatchViolationScore = violationScore
	rec.Run.BatchEfficiencyScore = efficiencyScore
	rec.Run.BatchRecommendationSummary = summary
	return nil
}

// SetOptimizationResult stores optimization result fields for a run (best_run_id, best_score, iterations, candidate_run_ids).
func (s *RunStore) SetOptimizationResult(runID string, bestRunID string, bestScore float64, iterations int32, candidateRunIDs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.runs[runID]
	if !ok {
		return fmt.Errorf("run not found: %s", runID)
	}
	rec.Run.BestRunId = bestRunID
	rec.Run.BestScore = bestScore
	rec.Run.Iterations = iterations
	if candidateRunIDs != nil {
		rec.Run.CandidateRunIds = make([]string, len(candidateRunIDs))
		copy(rec.Run.CandidateRunIds, candidateRunIDs)
	} else {
		rec.Run.CandidateRunIds = nil
	}
	return nil
}

// SetOptimizationProgress updates in-progress optimization state (iteration, best_score).
// Used for SSE streaming; caller should use SetOptimizationResult for final values.
func (s *RunStore) SetOptimizationProgress(runID string, iteration int32, bestScore float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.runs[runID]
	if !ok {
		return
	}
	rec.Run.Iterations = iteration
	rec.Run.BestScore = bestScore
}

// SetCollector stores a metrics collector reference for a run
func (s *RunStore) SetCollector(runID string, collector *metrics.Collector) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.runs[runID]
	if !ok {
		return fmt.Errorf("run not found: %s", runID)
	}
	rec.Collector = collector
	return nil
}

// GetCollector retrieves the metrics collector for a run
func (s *RunStore) GetCollector(runID string) (*metrics.Collector, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rec, ok := s.runs[runID]
	if !ok || rec.Collector == nil {
		return nil, false
	}
	return rec.Collector, true
}

// AppendOptimizationStep appends an optimization step to the run's history.
// Used by the online controller when it applies configuration changes.
func (s *RunStore) AppendOptimizationStep(runID string, step *simulationv1.OptimizationStep) error {
	if step == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.runs[runID]
	if !ok {
		return fmt.Errorf("run not found: %s", runID)
	}
	rec.OptimizationHistory = append(rec.OptimizationHistory, proto.Clone(step).(*simulationv1.OptimizationStep))
	return nil
}

// OptimizationHistoryCount returns the number of optimization steps for a run (for SSE polling).
func (s *RunStore) OptimizationHistoryCount(runID string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rec, ok := s.runs[runID]
	if !ok {
		return 0
	}
	return len(rec.OptimizationHistory)
}

// sortRunRecords sorts runs by creation time (newest first)
func sortRunRecords(runs []*RunRecord) {
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].Run.CreatedAtUnixMs > runs[j].Run.CreatedAtUnixMs
	})
}

func cloneRunRecord(rec *RunRecord) *RunRecord {
	if rec == nil {
		return nil
	}
	// Note: Collector is not cloned as it's a reference that should be shared
	history := make([]*simulationv1.OptimizationStep, len(rec.OptimizationHistory))
	for i, step := range rec.OptimizationHistory {
		if step != nil {
			history[i] = proto.Clone(step).(*simulationv1.OptimizationStep)
		}
	}
	return &RunRecord{
		Run:                 cloneRun(rec.Run),
		Input:               cloneRunInput(rec.Input),
		Metrics:             cloneRunMetrics(rec.Metrics),
		Collector:           rec.Collector,
		OptimizationHistory: history,
		FinalConfig:         cloneRunConfiguration(rec.FinalConfig),
	}
}

func cloneRunConfiguration(cfg *simulationv1.RunConfiguration) *simulationv1.RunConfiguration {
	if cfg == nil {
		return nil
	}
	return proto.Clone(cfg).(*simulationv1.RunConfiguration)
}

func cloneRun(in *simulationv1.Run) *simulationv1.Run {
	if in == nil {
		return nil
	}
	return proto.Clone(in).(*simulationv1.Run)
}

func cloneRunInput(in *simulationv1.RunInput) *simulationv1.RunInput {
	if in == nil {
		return nil
	}
	return proto.Clone(in).(*simulationv1.RunInput)
}

func cloneRunMetrics(in *simulationv1.RunMetrics) *simulationv1.RunMetrics {
	if in == nil {
		return nil
	}
	return proto.Clone(in).(*simulationv1.RunMetrics)
}
