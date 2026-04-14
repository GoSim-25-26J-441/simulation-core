package simd

import (
	"testing"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/utils"
)

func TestResolveServiceExecutionProfileDbLatencyCapsCPU(t *testing.T) {
	svc := &config.Service{ID: "db", Kind: "database", Model: "db_latency"}
	ep := &config.Endpoint{
		Path:       "/q",
		MeanCPUMs:  2,
		CPUSigmaMs: 0,
		NetLatencyMs: config.LatencySpec{Mean: 20, Sigma: 0},
	}
	rng := utils.NewRandSource(42)
	p := resolveServiceExecutionProfile(svc, ep, nil, rng)
	if p.CPUTimeMs > 12 {
		t.Fatalf("expected CPU dominated by IO for db_latency, got cpu=%.2f", p.CPUTimeMs)
	}
	if p.QueueMeanWorkMs < ep.NetLatencyMs.Mean {
		t.Fatalf("queue mean should track IO, got %.2f", p.QueueMeanWorkMs)
	}
}

func TestResolveServiceExecutionProfileCacheLowerCPU(t *testing.T) {
	svc := &config.Service{ID: "c", Kind: "cache", Model: "cpu"}
	ep := &config.Endpoint{
		Path:         "/g",
		MeanCPUMs:    10,
		CPUSigmaMs:   0,
		NetLatencyMs: config.LatencySpec{Mean: 5, Sigma: 0},
	}
	rng := utils.NewRandSource(1)
	p := resolveServiceExecutionProfile(svc, ep, nil, rng)
	if p.CPUTimeMs > ep.MeanCPUMs {
		t.Fatalf("cache should not increase CPU, got %.2f", p.CPUTimeMs)
	}
}
