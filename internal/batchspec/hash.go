package batchspec

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"sort"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// ConfigHash returns a stable FNV-1a hash of the scenario topology and allocation
// (hosts, services, workload, policies). Used for candidate deduplication and tie-breaking.
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
		writeI(sv.Replicas)
		writeF(sv.CPUCores)
		writeF(sv.MemoryMB)
		writeStr(sv.Model)
	}

	for _, w := range s.Workload {
		writeStr("wl")
		writeStr(w.From)
		writeStr(w.To)
		writeStr(w.Arrival.Type)
		writeF(w.Arrival.RateRPS)
	}

	if s.Policies != nil {
		writeStr("pol")
		if s.Policies.Autoscaling != nil {
			writeStr("as")
			_ = binary.Write(h, binary.LittleEndian, s.Policies.Autoscaling.Enabled)
			writeF(s.Policies.Autoscaling.TargetCPUUtil)
			writeI(s.Policies.Autoscaling.ScaleStep)
		}
		if s.Policies.Retries != nil {
			writeStr("ret")
			_ = binary.Write(h, binary.LittleEndian, s.Policies.Retries.Enabled)
			writeI(s.Policies.Retries.MaxRetries)
		}
	}

	return h.Sum64()
}

// ConfigHashHex is a hex string for logging and API payloads.
func ConfigHashHex(s *config.Scenario) string {
	return fmt.Sprintf("%016x", ConfigHash(s))
}
