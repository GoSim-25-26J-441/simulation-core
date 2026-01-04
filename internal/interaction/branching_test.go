package interaction

import (
	"math/rand"
	"testing"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestDefaultBranchingStrategy(t *testing.T) {
	strategy := &DefaultBranchingStrategy{}
	rng := rand.New(rand.NewSource(42))

	calls := []ResolvedCall{
		{
			ServiceID: "svc1",
			Path:      "/api",
			Call: config.DownstreamCall{
				CallCountMean: 1.0,
			},
		},
		{
			ServiceID: "svc2",
			Path:      "/test",
			Call: config.DownstreamCall{
				CallCountMean: 2.0,
			},
		},
	}

	selected := strategy.SelectCalls(calls, rng)
	if len(selected) == 0 {
		t.Fatalf("expected at least one selected call")
	}

	// With CallCountMean 1.0 and 2.0, we should get calls
	if len(selected) < 1 {
		t.Fatalf("expected at least 1 selected call, got %d", len(selected))
	}
}

func TestDefaultBranchingStrategyWithFractionalMean(t *testing.T) {
	strategy := &DefaultBranchingStrategy{}
	rng := rand.New(rand.NewSource(42))

	calls := []ResolvedCall{
		{
			ServiceID: "svc1",
			Path:      "/api",
			Call: config.DownstreamCall{
				CallCountMean: 0.5, // 50% chance
			},
		},
	}

	selected := strategy.SelectCalls(calls, rng)
	// With 0.5 mean, we might get 0 or 1 calls depending on random
	if len(selected) > 1 {
		t.Fatalf("expected at most 1 selected call, got %d", len(selected))
	}
}

func TestProbabilisticBranchingStrategy(t *testing.T) {
	probabilities := map[string]float64{
		"svc1:/api": 0.8,
		"svc2:/test": 0.3,
	}

	strategy := NewProbabilisticBranchingStrategy(probabilities)
	rng := rand.New(rand.NewSource(42))

	calls := []ResolvedCall{
		{
			ServiceID: "svc1",
			Path:      "/api",
			Call: config.DownstreamCall{
				CallCountMean: 1.0,
			},
		},
		{
			ServiceID: "svc2",
			Path:      "/test",
			Call: config.DownstreamCall{
				CallCountMean: 1.0,
			},
		},
	}

	selected := strategy.SelectCalls(calls, rng)
	// Should select based on probabilities
	if len(selected) > len(calls) {
		t.Fatalf("expected at most %d selected calls, got %d", len(calls), len(selected))
	}
}

