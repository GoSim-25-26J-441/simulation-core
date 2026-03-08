package main

import (
	"reflect"
	"testing"

	"github.com/GoSim-25-26J-441/simulation-core/internal/improvement"
)

func TestBuildTopCandidateRunIDs_Deduplication(t *testing.T) {
	// Same RunID repeated in multiple history steps (e.g. same config won 4 times)
	r := &improvement.ExperimentResult{
		BestRunID: "opt-best",
		Runs: []*improvement.RunContext{
			{RunID: "opt-A", Score: 10},
			{RunID: "opt-best", Score: 5},
			{RunID: "opt-best", Score: 5},
			{RunID: "opt-best", Score: 5},
			{RunID: "opt-best", Score: 5},
		},
	}
	// n=5 and len(runs)==5: "return all" branch, unique IDs in first-occurrence order
	got := buildTopCandidateRunIDs(r, 5)
	want := []string{"opt-A", "opt-best"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildTopCandidateRunIDs(r, 5) = %v, want %v", got, want)
	}
	// n=0: return all unique, first-occurrence order gives opt-A then opt-best
	gotAll := buildTopCandidateRunIDs(r, 0)
	wantAll := []string{"opt-A", "opt-best"}
	if !reflect.DeepEqual(gotAll, wantAll) {
		t.Errorf("buildTopCandidateRunIDs(r, 0) = %v, want %v", gotAll, wantAll)
	}
}

func TestBuildTopCandidateRunIDs_TopNByScore(t *testing.T) {
	// n=3, multiple runs with same score (same ID repeated)
	r := &improvement.ExperimentResult{
		BestRunID: "opt-best",
		Runs: []*improvement.RunContext{
			{RunID: "opt-1", Score: 100},
			{RunID: "opt-2", Score: 50},
			{RunID: "opt-2", Score: 50},
			{RunID: "opt-3", Score: 25},
			{RunID: "opt-4", Score: 10},
		},
	}
	got := buildTopCandidateRunIDs(r, 3)
	// Top 3 by score (unique): opt-4 (10), opt-3 (25), opt-2 (50). Best run opt-best not in top 3 so appended
	want := []string{"opt-4", "opt-3", "opt-2", "opt-best"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildTopCandidateRunIDs(r, 3) = %v, want %v", got, want)
	}
}

func TestBuildTopCandidateRunIDs_BestInTopN(t *testing.T) {
	// Best run is already in top 3, should not be duplicated
	r := &improvement.ExperimentResult{
		BestRunID: "opt-best",
		Runs: []*improvement.RunContext{
			{RunID: "opt-1", Score: 100},
			{RunID: "opt-best", Score: 10},
			{RunID: "opt-2", Score: 50},
			{RunID: "opt-3", Score: 25},
		},
	}
	got := buildTopCandidateRunIDs(r, 3)
	want := []string{"opt-best", "opt-3", "opt-2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildTopCandidateRunIDs(r, 3) = %v, want %v", got, want)
	}
}

func TestBuildTopCandidateRunIDs_NoDuplicatesInInput(t *testing.T) {
	// Existing behavior: 6 unique runs, n=5 -> 5 elements; best_run_id in first 5 so not appended again
	r := &improvement.ExperimentResult{
		BestRunID: "opt-cand-best",
		Runs: []*improvement.RunContext{
			{RunID: "opt-cand-best", Score: 5},
			{RunID: "opt-cand-2", Score: 10},
			{RunID: "opt-cand-3", Score: 15},
			{RunID: "opt-cand-4", Score: 20},
			{RunID: "opt-cand-5", Score: 25},
			{RunID: "opt-cand-6", Score: 30},
		},
	}
	got := buildTopCandidateRunIDs(r, 5)
	want := []string{"opt-cand-best", "opt-cand-2", "opt-cand-3", "opt-cand-4", "opt-cand-5"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildTopCandidateRunIDs(r, 5) = %v, want %v", got, want)
	}
}

func TestBuildTopCandidateRunIDs_BestNotInFirstFive(t *testing.T) {
	// 6 unique runs, best is 6th by score; n=5 -> first 5 by score + best_run_id appended
	r := &improvement.ExperimentResult{
		BestRunID: "opt-worst-by-score",
		Runs: []*improvement.RunContext{
			{RunID: "opt-1", Score: 10},
			{RunID: "opt-2", Score: 20},
			{RunID: "opt-3", Score: 30},
			{RunID: "opt-4", Score: 40},
			{RunID: "opt-5", Score: 50},
			{RunID: "opt-worst-by-score", Score: 100},
		},
	}
	got := buildTopCandidateRunIDs(r, 5)
	want := []string{"opt-1", "opt-2", "opt-3", "opt-4", "opt-5", "opt-worst-by-score"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildTopCandidateRunIDs(r, 5) = %v, want %v", got, want)
	}
}

func TestBuildTopCandidateRunIDs_EmptyRuns(t *testing.T) {
	r := &improvement.ExperimentResult{BestRunID: "best", Runs: nil}
	got := buildTopCandidateRunIDs(r, 5)
	if len(got) != 0 {
		t.Errorf("buildTopCandidateRunIDs(nil runs, 5) = %v, want []", got)
	}
	got0 := buildTopCandidateRunIDs(r, 0)
	if len(got0) != 0 {
		t.Errorf("buildTopCandidateRunIDs(nil runs, 0) = %v, want []", got0)
	}
}
