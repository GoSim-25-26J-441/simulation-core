package simd

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/utils"
	"google.golang.org/protobuf/proto"
)

type RunRecord struct {
	Run       *simulationv1.Run
	Input     *simulationv1.RunInput
	Metrics   *simulationv1.RunMetrics
	Collector *metrics.Collector
}

type RunStore struct {
	mu   sync.RWMutex
	runs map[string]*RunRecord
}

func NewRunStore() *RunStore {
	return &RunStore{
		runs: make(map[string]*RunRecord),
	}
}

func nowUnixMs() int64 {
	return time.Now().UTC().UnixMilli()
}

func (s *RunStore) Create(runID string, input *simulationv1.RunInput) (*RunRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

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

	rec := &RunRecord{
		Run: &simulationv1.Run{
			Id:              runID,
			Status:          simulationv1.RunStatus_RUN_STATUS_PENDING,
			CreatedAtUnixMs: nowUnixMs(),
		},
		Input:   cloneRunInput(input),
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
		simulationv1.RunStatus_RUN_STATUS_CANCELLED:
		rec.Run.EndedAtUnixMs = nowUnixMs()
	}

	return cloneRunRecord(rec), nil
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
	return &RunRecord{
		Run:       cloneRun(rec.Run),
		Input:     cloneRunInput(rec.Input),
		Metrics:   cloneRunMetrics(rec.Metrics),
		Collector: rec.Collector,
	}
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
