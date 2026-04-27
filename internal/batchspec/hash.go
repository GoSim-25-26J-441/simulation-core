package batchspec

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"

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
		if err := binary.Write(h, binary.LittleEndian, byte(0)); err != nil {
			panic(err)
		}
	}
	writeF := func(x float64) {
		if err := binary.Write(h, binary.LittleEndian, x); err != nil {
			panic(err)
		}
	}
	writeI := func(x int) {
		if err := binary.Write(h, binary.LittleEndian, int64(x)); err != nil {
			panic(err)
		}
	}
	writeI64 := func(x int64) {
		if err := binary.Write(h, binary.LittleEndian, x); err != nil {
			panic(err)
		}
	}
	writeB := func(x bool) {
		if err := binary.Write(h, binary.LittleEndian, x); err != nil {
			panic(err)
		}
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

	// --- network (optional zone latency overlays) ---
	if s.Network == nil {
		writeStr("net_nil")
	} else {
		writeStr("net")
		writeB(s.Network.SymmetricCrossZoneLatency)
		writeF(s.Network.SameHostLatencyMs.Mean)
		writeF(s.Network.SameHostLatencyMs.Sigma)
		writeF(s.Network.SameZoneLatencyMs.Mean)
		writeF(s.Network.SameZoneLatencyMs.Sigma)
		writeF(s.Network.DefaultCrossZoneLatencyMs.Mean)
		writeF(s.Network.DefaultCrossZoneLatencyMs.Sigma)
		writeF(s.Network.ExternalLatencyMs.Mean)
		writeF(s.Network.ExternalLatencyMs.Sigma)
		var fromKeys []string
		for k := range s.Network.CrossZoneLatencyMs {
			fromKeys = append(fromKeys, k)
		}
		sort.Strings(fromKeys)
		for _, from := range fromKeys {
			writeStr(from)
			m := s.Network.CrossZoneLatencyMs[from]
			var toKeys []string
			for k := range m {
				toKeys = append(toKeys, k)
			}
			sort.Strings(toKeys)
			for _, to := range toKeys {
				writeStr(to)
				ls := m[to]
				writeF(ls.Mean)
				writeF(ls.Sigma)
			}
		}
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
		writeStr(hh.Zone)
		if len(hh.Labels) == 0 {
			writeI(0)
		} else {
			keys := make([]string, 0, len(hh.Labels))
			for k := range hh.Labels {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			writeI(len(keys))
			for _, k := range keys {
				writeStr(k)
				writeStr(hh.Labels[k])
			}
		}
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
		if sv.ExternalNetworkLatencyMs == nil {
			writeStr("extnetlat_nil")
		} else {
			writeStr("extnetlat")
			writeF(sv.ExternalNetworkLatencyMs.Mean)
			writeF(sv.ExternalNetworkLatencyMs.Sigma)
		}
		if sv.Scaling == nil {
			writeStr("scaling_nil")
		} else {
			writeStr("scaling")
			writeB(sv.Scaling.Horizontal)
			writeB(sv.Scaling.VerticalCPU)
			writeB(sv.Scaling.VerticalMemory)
		}
		if sv.Placement == nil {
			writeStr("placement_nil")
		} else {
			writeStr("placement")
			p := sv.Placement
			requiredZones := append([]string(nil), p.RequiredZones...)
			sort.Strings(requiredZones)
			writeI(len(requiredZones))
			for _, z := range requiredZones {
				writeStr(strings.TrimSpace(z))
			}
			preferredZones := append([]string(nil), p.PreferredZones...)
			sort.Strings(preferredZones)
			writeI(len(preferredZones))
			for _, z := range preferredZones {
				writeStr(strings.TrimSpace(z))
			}
			affinityZones := append([]string(nil), p.AffinityZones...)
			sort.Strings(affinityZones)
			writeI(len(affinityZones))
			for _, z := range affinityZones {
				writeStr(strings.TrimSpace(z))
			}
			antiAffinityZones := append([]string(nil), p.AntiAffinityZones...)
			sort.Strings(antiAffinityZones)
			writeI(len(antiAffinityZones))
			for _, z := range antiAffinityZones {
				writeStr(strings.TrimSpace(z))
			}
			antiServices := append([]string(nil), p.AntiAffinityServices...)
			sort.Strings(antiServices)
			writeI(len(antiServices))
			for _, svc := range antiServices {
				writeStr(strings.TrimSpace(svc))
			}
			writeB(p.SpreadAcrossZones)
			writeI(p.MaxReplicasPerHost)
			if len(p.RequiredHostLabels) == 0 {
				writeI(0)
			} else {
				keys := make([]string, 0, len(p.RequiredHostLabels))
				for k := range p.RequiredHostLabels {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				writeI(len(keys))
				for _, k := range keys {
					writeStr(k)
					writeStr(p.RequiredHostLabels[k])
				}
			}
			if len(p.PreferredHostLabels) == 0 {
				writeI(0)
			} else {
				keys := make([]string, 0, len(p.PreferredHostLabels))
				for k := range p.PreferredHostLabels {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				writeI(len(keys))
				for _, k := range keys {
					writeStr(k)
					writeStr(p.PreferredHostLabels[k])
				}
			}
		}
		if sv.Routing == nil {
			writeStr("routing_nil")
		} else {
			writeStr("routing")
			writeStr(strings.ToLower(strings.TrimSpace(sv.Routing.Strategy)))
			writeStr(sv.Routing.StickyKeyFrom)
			writeStr(sv.Routing.LocalityZoneFrom)
			if len(sv.Routing.Weights) == 0 {
				writeI(0)
			} else {
				keys := make([]string, 0, len(sv.Routing.Weights))
				for k := range sv.Routing.Weights {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				writeI(len(keys))
				for _, k := range keys {
					writeStr(k)
					writeF(sv.Routing.Weights[k])
				}
			}
		}
		if sv.Behavior == nil {
			writeStr("beh_nil")
		} else {
			writeStr("beh")
			b := sv.Behavior
			writeF(b.FailureRate)
			writeF(b.SaturationLatencyFactor)
			writeI(b.MaxConnections)
			if b.Cache == nil {
				writeStr("cache_nil")
			} else {
				writeStr("cache")
				writeF(b.Cache.HitRate)
				writeF(b.Cache.HitLatencyMs.Mean)
				writeF(b.Cache.HitLatencyMs.Sigma)
				writeF(b.Cache.MissLatencyMs.Mean)
				writeF(b.Cache.MissLatencyMs.Sigma)
			}
			if b.Queue == nil {
				writeStr("queue_nil")
			} else {
				writeStr("queue")
				q := b.Queue
				writeI(q.Capacity)
				writeI(q.ConsumerConcurrency)
				writeI(q.MinConsumerConcurrency)
				writeI(q.MaxConsumerConcurrency)
				writeStr(q.ConsumerTarget)
				writeF(q.DeliveryLatencyMs.Mean)
				writeF(q.DeliveryLatencyMs.Sigma)
				writeF(q.AckTimeoutMs)
				writeI(q.MaxRedeliveries)
				writeStr(q.DLQTarget)
				writeStr(q.DropPolicy)
				writeB(q.AsyncFireAndForget)
			}
			if b.Topic == nil {
				writeStr("topic_nil")
			} else {
				writeStr("topic")
				t := b.Topic
				writeI(t.Partitions)
				writeI64(t.RetentionMs)
				writeI(t.Capacity)
				writeF(t.DeliveryLatencyMs.Mean)
				writeF(t.DeliveryLatencyMs.Sigma)
				writeStr(t.PublishAck)
				writeB(t.AsyncFireAndForget)
				subs := config.CanonicalTopicSubscribersForHash(t)
				writeI(len(subs))
				for _, sub := range subs {
					writeStr(sub.Name)
					writeStr(sub.ConsumerTarget)
					writeStr(sub.ConsumerGroup)
					writeI(sub.ConsumerConcurrency)
					writeI(sub.MinConsumerConcurrency)
					writeI(sub.MaxConsumerConcurrency)
					writeF(sub.AckTimeoutMs)
					writeI(sub.MaxRedeliveries)
					writeStr(sub.DLQ)
					writeStr(sub.DropPolicy)
				}
			}
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
			writeF(ep.FailureRate)
			writeF(ep.TimeoutMs)
			writeF(ep.IOMs.Mean)
			writeF(ep.IOMs.Sigma)
			writeI(ep.ConnectionPool)
			if ep.Routing == nil {
				writeStr("ep_routing_nil")
			} else {
				writeStr("ep_routing")
				writeStr(strings.ToLower(strings.TrimSpace(ep.Routing.Strategy)))
				writeStr(ep.Routing.StickyKeyFrom)
				writeStr(ep.Routing.LocalityZoneFrom)
				if len(ep.Routing.Weights) == 0 {
					writeI(0)
				} else {
					keys := make([]string, 0, len(ep.Routing.Weights))
					for k := range ep.Routing.Weights {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					writeI(len(keys))
					for _, k := range keys {
						writeStr(k)
						writeF(ep.Routing.Weights[k])
					}
				}
			}
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
				if a.PartitionKey != b.PartitionKey {
					return a.PartitionKey < b.PartitionKey
				}
				if a.PartitionKeyFrom != b.PartitionKeyFrom {
					return a.PartitionKeyFrom < b.PartitionKeyFrom
				}
				if a.FailureRate != b.FailureRate {
					return a.FailureRate < b.FailureRate
				}
				aR, bR := false, false
				if a.Retryable != nil {
					aR = *a.Retryable
				}
				if b.Retryable != nil {
					bR = *b.Retryable
				}
				if aR != bR {
					return !aR && bR
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
				writeF(d.FailureRate)
				if d.Retryable == nil {
					writeStr("dr_nil")
				} else {
					writeB(*d.Retryable)
				}
				writeF(d.DownstreamFractionCPU)
				writeStr(d.PartitionKey)
				writeStr(d.PartitionKeyFrom)
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
		if len(a.Metadata) != len(b.Metadata) {
			return len(a.Metadata) < len(b.Metadata)
		}
		if len(a.Metadata) > 0 {
			ak := make([]string, 0, len(a.Metadata))
			bk := make([]string, 0, len(b.Metadata))
			for k := range a.Metadata {
				ak = append(ak, k)
			}
			for k := range b.Metadata {
				bk = append(bk, k)
			}
			sort.Strings(ak)
			sort.Strings(bk)
			for i := range ak {
				if ak[i] != bk[i] {
					return ak[i] < bk[i]
				}
				if a.Metadata[ak[i]] != b.Metadata[bk[i]] {
					return a.Metadata[ak[i]] < b.Metadata[bk[i]]
				}
			}
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
		if len(w.Metadata) == 0 {
			writeI(0)
		} else {
			keys := make([]string, 0, len(w.Metadata))
			for k := range w.Metadata {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			writeI(len(keys))
			for _, k := range keys {
				writeStr(k)
				writeStr(w.Metadata[k])
			}
		}
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
