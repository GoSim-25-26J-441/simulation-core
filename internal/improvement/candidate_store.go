package improvement

import (
	"sort"
	"sync"
)

// BatchCandidateRecord holds batch ranking metadata for one evaluated configuration.
type BatchCandidateRecord struct {
	ConfigHash      uint64
	RunID           string
	Feasible        bool
	ViolationScore  float64
	EfficiencyScore float64
}

// CandidateStore maps configuration hashes to evaluation run IDs for the duration of an experiment.
type CandidateStore struct {
	mu          sync.RWMutex
	m           map[uint64]string
	batchScores map[uint64]BatchScore
}

// NewCandidateStore creates an empty registry.
func NewCandidateStore() *CandidateStore {
	return &CandidateStore{m: make(map[uint64]string), batchScores: make(map[uint64]BatchScore)}
}

// Register records that hash h maps to runID. If already present, the first run ID wins.
func (c *CandidateStore) Register(h uint64, runID string) {
	if h == 0 || runID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.m[h]; !ok {
		c.m[h] = runID
	}
}

// RecordBatchScore stores the latest batch score for a configuration hash (after evaluation).
func (c *CandidateStore) RecordBatchScore(h uint64, bs BatchScore) {
	if h == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.batchScores == nil {
		c.batchScores = make(map[uint64]BatchScore)
	}
	c.batchScores[h] = bs
}

// Lookup returns the run ID for a configuration hash, if registered.
func (c *CandidateStore) Lookup(h uint64) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	id, ok := c.m[h]
	return id, ok
}

// Len returns the number of registered candidates.
func (c *CandidateStore) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.m)
}

// AllRunIDs returns registered run IDs in arbitrary order (legacy).
func (c *CandidateStore) AllRunIDs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.m))
	seen := make(map[string]struct{})
	for _, id := range c.m {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// SortedBatchCandidateRunIDs returns run IDs ordered by CompareBatchScores (feasible first, then violation, efficiency, hash).
func (c *CandidateStore) SortedBatchCandidateRunIDs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	type row struct {
		h  uint64
		id string
		bs BatchScore
	}
	var rows []row
	for h, id := range c.m {
		if id == "" {
			continue
		}
		bs, ok := c.batchScores[h]
		if !ok {
			continue
		}
		rows = append(rows, row{h: h, id: id, bs: bs})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return CompareBatchScores(rows[i].bs, rows[j].bs, rows[i].h, rows[j].h)
	})
	out := make([]string, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, r.id)
	}
	return out
}

// BatchCandidateRecords returns metadata for all scored candidates, sorted like SortedBatchCandidateRunIDs.
func (c *CandidateStore) BatchCandidateRecords() []BatchCandidateRecord {
	c.mu.RLock()
	defer c.mu.RUnlock()
	type row struct {
		h  uint64
		id string
		bs BatchScore
	}
	var rows []row
	for h, id := range c.m {
		if id == "" {
			continue
		}
		bs, ok := c.batchScores[h]
		if !ok {
			continue
		}
		rows = append(rows, row{h: h, id: id, bs: bs})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return CompareBatchScores(rows[i].bs, rows[j].bs, rows[i].h, rows[j].h)
	})
	out := make([]BatchCandidateRecord, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, BatchCandidateRecord{
			ConfigHash:      r.h,
			RunID:           r.id,
			Feasible:        r.bs.Feasible,
			ViolationScore:  r.bs.ViolationScore,
			EfficiencyScore: r.bs.EfficiencyScore,
		})
	}
	return out
}
