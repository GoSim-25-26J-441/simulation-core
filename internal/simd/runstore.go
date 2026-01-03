package simd

import (
	"fmt"
	"sync"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/utils"
)

type RunRecord struct {
	Run     *simulationv1.Run
	Input   *simulationv1.RunInput
	Metrics *simulationv1.RunMetrics
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
	if _, exists := s.runs[runID]; exists {
		return nil, fmt.Errorf("run already exists: %s", runID)
	}

	rec := &RunRecord{
		Run: &simulationv1.Run{
			Id:              runID,
			Status:          simulationv1.RunStatus_RUN_STATUS_PENDING,
			CreatedAtUnixMs: nowUnixMs(),
		},
		Input:   input,
		Metrics: nil,
	}
	s.runs[runID] = rec
	return rec, nil
}

func (s *RunStore) Get(runID string) (*RunRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.runs[runID]
	return rec, ok
}

func (s *RunStore) List(limit int) []*RunRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 50
	}
	out := make([]*RunRecord, 0, minInt(limit, len(s.runs)))
	for _, rec := range s.runs {
		out = append(out, rec)
		if len(out) >= limit {
			break
		}
	}
	return out
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

	return rec, nil
}

func (s *RunStore) SetMetrics(runID string, metrics *simulationv1.RunMetrics) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.runs[runID]
	if !ok {
		return fmt.Errorf("run not found: %s", runID)
	}
	rec.Metrics = metrics
	return nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
