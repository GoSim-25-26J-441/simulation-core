package batchspec

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"sort"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// ConfigHash returns a stable FNV-1a hash of the full scenario semantics used for runtime,
// optimizer constraints, and metrics. Ordering is canonicalized where noted below so that
// logically equivalent multisets (e.g. services or workloads listed in different orders) hash
// the same; see IMPLEMENTATION_NOTE.md.
func ConfigHash(s *config.Scenario) uint64 {
	if s == nil {
		return 0
	}
	h := fnv.New64a()
	writeStr := func(x string) {
		_, _ = h.Write([]byte(x))
		_ = binary.Write(h, binary.LittleEndian, byte(0))
	}
	writeF := func(x float64) {
		_ = binary.Write(h, binary.LittleEndian, x)
	}
	writeI := func(x int) {
		_ = binary.Write(h, binary.LittleEndian, int64(x))
	}
	writeB := func(x bool) {
		_ = binary.Write(h, binary.LittleEndian, x)
	}

	// --- metadata ---
	if s.Metadata == nil {
		writeStr("meta_nil")
	} else {
		writeStr("meta")
		writeStr(s.Metadata.SchemaVersion)
	}

	// --- simulation_limits ---
	if s.SimulationLimits == nil {
		writeStr("sl_nil")
	} else {
		writeStr("sl")
		writeI(s.SimulationLimits.MaxTraceDepth)
		writeI(s.SimulationLimits.MaxAsyncHops)
	}

	// --- hosts (canonical: by host ID) ---
	hostIDs := make([]string, len(s.Hosts))
	for i := range s.Hosts {
		hostIDs[i] = s.Hosts[i].ID
	}
	sort.Strings(hostIDs)
	for _, id := range hostIDs {
		var hi int
		for i := range s.Hosts {
			if s.Hosts[i].ID == id {
				hi = i
				break
			}
		}
		hh := s.Hosts[hi]
		writeStr("host")
		writeStr(hh.ID)
		writeI(hh.Cores)
		writeI(hh.MemoryGB)
	}

	// --- services (canonical: by service ID) ---
	svcIDs := make([]string, len(s.Services))
	for i := range s.Services {
		svcIDs[i] = s.Services[i].ID
	}
	sort.Strings(svcIDs)
	for _, id := range svcIDs {
		var si int
		for i := range s.Services {
			if s.Services[i].ID == id {
				si = i
				break
			}
		}
		sv := s.Services[si]
		writeStr("svc")
		writeStr(sv.ID)
		writeStr(sv.Kind)
		writeStr(sv.Role)
		writeI(sv.Replicas)
		writeStr(sv.Model)
		writeF(sv.CPUCores)
		writeF(sv.MemoryMB)
		if sv.Scaling == nil {
			writeStr("scaling_nil")
		} else {
			writeStr("scaling")
			writeB(sv.Scaling.Horizontal)
			writeB(sv.Scaling.VerticalCPU)
			writeB(sv.Scaling.VerticalMemory)
		}

		// endpoints (canonical: by path, then declaration order for duplicate paths)
		epIdx := make([]int, len(sv.Endpoints))
		for i := range epIdx {
			epIdx[i] = i
		}
		sort.SliceStable(epIdx, func(i, j int) bool {
			pi, pj := sv.Endpoints[epIdx[i]].Path, sv.Endpoints[epIdx[j]].Path
			if pi != pj {
				return pi < pj
			}
			return epIdx[i] < epIdx[j]
		})
		for _, ei := range epIdx {
			ep := sv.Endpoints[ei]
			writeStr("ep")
			writeStr(ep.Path)
			writeF(ep.MeanCPUMs)
			writeF(ep.CPUSigmaMs)
			writeF(ep.DefaultMemoryMB)
			writeF(ep.NetLatencyMs.Mean)
			writeF(ep.NetLatencyMs.Sigma)

			dsIdx := make([]int, len(ep.Downstream))
			for i := range dsIdx {
				dsIdx[i] = i
			}
			sort.SliceStable(dsIdx, func(i, j int) bool {
				a, b := ep.Downstream[dsIdx[i]], ep.Downstream[dsIdx[j]]
				if a.To != b.To {
					return a.To < b.To
				}
				if a.Mode != b.Mode {
					return a.Mode < b.Mode
				}
				if a.Kind != b.Kind {
					return a.Kind < b.Kind
				}
				if a.Probability != b.Probability {
					return a.Probability < b.Probability
				}
				if a.CallCountMean != b.CallCountMean {
					return a.CallCountMean < b.CallCountMean
				}
				if a.CallLatencyMs.Mean != b.CallLatencyMs.Mean {
					return a.CallLatencyMs.Mean < b.CallLatencyMs.Mean
				}
				if a.CallLatencyMs.Sigma != b.CallLatencyMs.Sigma {
					return a.CallLatencyMs.Sigma < b.CallLatencyMs.Sigma
				}
				if a.TimeoutMs != b.TimeoutMs {
					return a.TimeoutMs < b.TimeoutMs
				}
				if a.DownstreamFractionCPU != b.DownstreamFractionCPU {
					return a.DownstreamFractionCPU < b.DownstreamFractionCPU
				}
				return dsIdx[i] < dsIdx[j]
			})
			writeI(len(dsIdx))
			for _, di := range dsIdx {
				d := ep.Downstream[di]
				writeStr("ds")
				writeStr(d.To)
				writeStr(d.Mode)
				writeStr(d.Kind)
				writeF(d.Probability)
				writeF(d.CallCountMean)
				writeF(d.CallLatencyMs.Mean)
				writeF(d.CallLatencyMs.Sigma)
				writeF(d.TimeoutMs)
				writeF(d.DownstreamFractionCPU)
			}
		}
	}

	// --- workload (canonical: full tuple, then declaration order for duplicates) ---
	wlIdx := make([]int, len(s.Workload))
	for i := range wlIdx {
		wlIdx[i] = i
	}
	sort.SliceStable(wlIdx, func(i, j int) bool {
		a, b := s.Workload[wlIdx[i]], s.Workload[wlIdx[j]]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		if a.TrafficClass != b.TrafficClass {
			return a.TrafficClass < b.TrafficClass
		}
		if a.SourceKind != b.SourceKind {
			return a.SourceKind < b.SourceKind
		}
		if a.Arrival.Type != b.Arrival.Type {
			return a.Arrival.Type < b.Arrival.Type
		}
		if a.Arrival.RateRPS != b.Arrival.RateRPS {
			return a.Arrival.RateRPS < b.Arrival.RateRPS
		}
		if a.Arrival.StdDevRPS != b.Arrival.StdDevRPS {
			return a.Arrival.StdDevRPS < b.Arrival.StdDevRPS
		}
		if a.Arrival.BurstRateRPS != b.Arrival.BurstRateRPS {
			return a.Arrival.BurstRateRPS < b.Arrival.BurstRateRPS
		}
		if a.Arrival.BurstDurationSeconds != b.Arrival.BurstDurationSeconds {
			return a.Arrival.BurstDurationSeconds < b.Arrival.BurstDurationSeconds
		}
		if a.Arrival.QuietDurationSeconds != b.Arrival.QuietDurationSeconds {
			return a.Arrival.QuietDurationSeconds < b.Arrival.QuietDurationSeconds
		}
		return wlIdx[i] < wlIdx[j]
	})
	for _, wi := range wlIdx {
		w := s.Workload[wi]
		writeStr("wl")
		writeStr(w.From)
		writeStr(w.SourceKind)
		writeStr(w.TrafficClass)
		writeStr(w.To)
		writeStr(w.Arrival.Type)
		writeF(w.Arrival.RateRPS)
		writeF(w.Arrival.StdDevRPS)
		writeF(w.Arrival.BurstRateRPS)
		writeF(w.Arrival.BurstDurationSeconds)
		writeF(w.Arrival.QuietDurationSeconds)
	}

	// --- policies ---
	if s.Policies == nil {
		writeStr("pol_nil")
	} else {
		writeStr("pol")
		if s.Policies.Autoscaling == nil {
			writeStr("as_nil")
		} else {
			a := s.Policies.Autoscaling
			writeStr("as")
			writeB(a.Enabled)
			writeF(a.TargetCPUUtil)
			writeI(a.ScaleStep)
		}
		if s.Policies.Retries == nil {
			writeStr("ret_nil")
		} else {
			r := s.Policies.Retries
			writeStr("ret")
			writeB(r.Enabled)
			writeI(r.MaxRetries)
			writeStr(r.Backoff)
			writeI(r.BaseMs)
		}
	}

	return binary.LittleEndian.Uint64(h.Sum(nil))
}

// ConfigHashHex is a hex string for logging and API payloads.
func ConfigHashHex(s *config.Scenario) string {
	return fmt.Sprintf("%016x", ConfigHash(s))
}

// ScenarioSemanticsEqual reports whether two scenarios are identical for optimizer identity
// (same ConfigHash). Nil matches only nil.
func ScenarioSemanticsEqual(a, b *config.Scenario) bool {
	if a == nil || b == nil {
		return a == b
	}
	return ConfigHash(a) == ConfigHash(b)
}
