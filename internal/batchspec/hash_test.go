package batchspec

import (
	"testing"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestConfigHashIncludesHosts(t *testing.T) {
	s1 := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 2, MemoryGB: 8}},
		Services: []config.Service{
			{ID: "s", Replicas: 1, CPUCores: 1, MemoryMB: 512, Model: "cpu"},
		},
	}
	s2 := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 4, MemoryGB: 8}},
		Services: []config.Service{
			{ID: "s", Replicas: 1, CPUCores: 1, MemoryMB: 512, Model: "cpu"},
		},
	}
	if ConfigHash(s1) == ConfigHash(s2) {
		t.Fatal("expected different hashes when host CPU differs")
	}
}
