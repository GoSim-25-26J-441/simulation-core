package improvement

import (
	"testing"

	"github.com/GoSim-25-26J-441/simulation-core/internal/batchspec"
)

func TestCandidateStoreFirstRegisterWinsPerHash(t *testing.T) {
	c := NewCandidateStore()
	h1 := uint64(0xabc123)
	c.Register(h1, "run-a")
	c.Register(h1, "run-b")
	if id, ok := c.Lookup(h1); !ok || id != "run-a" {
		t.Fatalf("expected first run ID for hash, got %q ok=%v", id, ok)
	}
}

func TestCandidateStoreDistinctHashesDoNotMerge(t *testing.T) {
	c := NewCandidateStore()
	h1 := batchspec.ConfigHash(fullScenarioIdentity())
	alt := fullScenarioIdentity()
	alt.Services[0].Endpoints[0].Downstream[0].TimeoutMs = 999
	h2 := batchspec.ConfigHash(alt)
	if h1 == h2 {
		t.Fatal("expected different hashes for different scenarios")
	}
	c.Register(h1, "run-1")
	c.Register(h2, "run-2")
	if id1, _ := c.Lookup(h1); id1 != "run-1" {
		t.Fatalf("expected run-1 for h1, got %q", id1)
	}
	if id2, _ := c.Lookup(h2); id2 != "run-2" {
		t.Fatalf("expected run-2 for h2, got %q", id2)
	}
}
