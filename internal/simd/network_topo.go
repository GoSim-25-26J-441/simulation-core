package simd

import (
	"math"
	"strings"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

// findCrossZoneLatencySpec returns latency mean/sigma for a directed caller_zone -> callee_zone hop.
// Same zones and missing/empty zones yield !ok. When the pair is not listed, optional symmetric reverse
// and then default_cross_zone_latency_ms apply when configured.
func findCrossZoneLatencySpec(net *config.NetworkConfig, callerZone, calleeZone string) (config.LatencySpec, bool) {
	if net == nil {
		return config.LatencySpec{}, false
	}
	callerZone = strings.TrimSpace(callerZone)
	calleeZone = strings.TrimSpace(calleeZone)
	if callerZone == "" || calleeZone == "" {
		return config.LatencySpec{}, false
	}
	if strings.EqualFold(callerZone, calleeZone) {
		return config.LatencySpec{}, false
	}
	if s, ok := lookupDirectedCrossZone(net.CrossZoneLatencyMs, callerZone, calleeZone); ok {
		return s, true
	}
	if net.SymmetricCrossZoneLatency {
		if s, ok := lookupDirectedCrossZone(net.CrossZoneLatencyMs, calleeZone, callerZone); ok {
			return s, true
		}
	}
	d := net.DefaultCrossZoneLatencyMs
	if d.Mean != 0 || d.Sigma != 0 {
		return d, true
	}
	return config.LatencySpec{}, false
}

func lookupDirectedCrossZone(m map[string]map[string]config.LatencySpec, callerZone, calleeZone string) (config.LatencySpec, bool) {
	if len(m) == 0 {
		return config.LatencySpec{}, false
	}
	if inner, ok := m[callerZone]; ok && inner != nil {
		if s, ok2 := inner[calleeZone]; ok2 {
			return s, true
		}
	}
	for fk, inner := range m {
		if !strings.EqualFold(strings.TrimSpace(fk), callerZone) {
			continue
		}
		if inner == nil {
			continue
		}
		for tk, s := range inner {
			if strings.EqualFold(strings.TrimSpace(tk), calleeZone) {
				return s, true
			}
		}
	}
	return config.LatencySpec{}, false
}

func sampleCrossZoneNetworkPenaltyMs(state *scenarioState, spec config.LatencySpec) float64 {
	if state == nil || state.rng == nil {
		return 0
	}
	v := state.rng.NormFloat64(spec.Mean, spec.Sigma)
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	if v < 0 {
		return 0
	}
	return v
}

// downstreamCallerHostZone resolves the upstream caller's zone for downstream request metadata (same order as topology metrics).
func downstreamCallerHostZone(state *scenarioState, req *models.Request) string {
	if state == nil || req == nil || req.Metadata == nil || state.rm == nil {
		return ""
	}
	callerZone := ""
	if callerID, ok := req.Metadata["caller_instance_id"].(string); ok && callerID != "" {
		if callerInst, ok := state.rm.GetServiceInstance(callerID); ok && callerInst != nil {
			if callerHost, ok := state.rm.GetHost(callerInst.HostID()); ok && callerHost != nil {
				callerZone = strings.TrimSpace(callerHost.Zone())
			}
		}
	}
	if callerZone == "" {
		if fallback, ok := req.Metadata["caller_host_zone"].(string); ok {
			callerZone = strings.TrimSpace(fallback)
		}
	}
	return callerZone
}

// downstreamCallerHostID resolves the upstream caller's host for same-host / same-zone classing.
// Prefer live lookup via caller_instance_id; if the instance is gone, use stable caller_host_id from snapshots.
func downstreamCallerHostID(state *scenarioState, req *models.Request) string {
	if state == nil || req == nil || req.Metadata == nil {
		return ""
	}
	if state.rm != nil {
		if callerID, ok := req.Metadata["caller_instance_id"].(string); ok && callerID != "" {
			if callerInst, ok := state.rm.GetServiceInstance(callerID); ok && callerInst != nil {
				return strings.TrimSpace(callerInst.HostID())
			}
		}
	}
	if v, ok := req.Metadata["caller_host_id"].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// applyTopologyNetworkPenaltyMs evaluates scenario.network for downstream hops: external overlays,
// then same-host / same-zone / cross-zone when placement topology is known. Returns applied penalty in ms.
func applyTopologyNetworkPenaltyMs(state *scenarioState, serviceID, endpointPath string, request *models.Request, instanceID string, simTime time.Time) float64 {
	if state == nil || request == nil || request.ParentID == "" || state.scenario == nil || state.scenario.Network == nil {
		return 0
	}
	net := state.scenario.Network
	svc, ok := state.services[serviceID]
	if !ok || svc == nil {
		return 0
	}
	kindNorm := strings.ToLower(strings.TrimSpace(svc.Kind))
	lbl := labelsForRequestMetrics(request, serviceID, endpointPath)

	if kindNorm == "external" {
		spec := net.ExternalLatencyMs
		if svc.ExternalNetworkLatencyMs != nil {
			spec = *svc.ExternalNetworkLatencyMs
		}
		if spec.Mean == 0 && spec.Sigma == 0 {
			return 0
		}
		pen := sampleCrossZoneNetworkPenaltyMs(state, spec)
		if pen <= 0 {
			return 0
		}
		if state.collector != nil {
			metrics.RecordExternalLatencyPenalty(state.collector, pen, simTime, lbl)
			metrics.RecordTopologyLatencyPenalty(state.collector, pen, simTime, lbl)
		}
		return pen
	}

	callerZ := downstreamCallerHostZone(state, request)
	calleeZ := calleeZoneForInstance(state, instanceID)
	callerHost := downstreamCallerHostID(state, request)
	calleeHost := calleeHostForInstance(state, instanceID)

	if callerZ == "" || calleeZ == "" {
		return 0
	}

	// Precedence: same host → same zone (different hosts) → cross zone.
	if callerHost != "" && calleeHost != "" && callerHost == calleeHost {
		spec := net.SameHostLatencyMs
		if spec.Mean == 0 && spec.Sigma == 0 {
			return 0
		}
		pen := sampleCrossZoneNetworkPenaltyMs(state, spec)
		if pen <= 0 {
			return 0
		}
		if state.collector != nil {
			lblZ := copyMetricLabelsWithZones(lbl, callerZ, calleeZ)
			metrics.RecordTopologyLatencyPenalty(state.collector, pen, simTime, lblZ)
		}
		return pen
	}

	if strings.EqualFold(callerZ, calleeZ) {
		if callerHost == "" || calleeHost == "" || callerHost == calleeHost {
			return 0
		}
		spec := net.SameZoneLatencyMs
		if spec.Mean == 0 && spec.Sigma == 0 {
			return 0
		}
		pen := sampleCrossZoneNetworkPenaltyMs(state, spec)
		if pen <= 0 {
			return 0
		}
		if state.collector != nil {
			lblZ := copyMetricLabelsWithZones(lbl, callerZ, calleeZ)
			metrics.RecordSameZoneLatencyPenalty(state.collector, pen, simTime, lblZ)
			metrics.RecordTopologyLatencyPenalty(state.collector, pen, simTime, lblZ)
		}
		return pen
	}

	spec, ok := findCrossZoneLatencySpec(net, callerZ, calleeZ)
	if !ok {
		return 0
	}
	pen := sampleCrossZoneNetworkPenaltyMs(state, spec)
	if pen <= 0 {
		return 0
	}
	if state.collector != nil {
		lblZ := copyMetricLabelsWithZones(lbl, callerZ, calleeZ)
		metrics.RecordCrossZoneLatencyPenalty(state.collector, pen, simTime, lblZ)
		metrics.RecordTopologyLatencyPenalty(state.collector, pen, simTime, lblZ)
	}
	return pen
}

func copyMetricLabelsWithZones(base map[string]string, callerZ, calleeZ string) map[string]string {
	out := make(map[string]string, len(base)+2)
	for k, v := range base {
		out[k] = v
	}
	out["caller_zone"] = callerZ
	out["callee_zone"] = calleeZ
	return out
}

func calleeZoneForInstance(state *scenarioState, instanceID string) string {
	if state == nil || state.rm == nil || instanceID == "" {
		return ""
	}
	if inst, ok := state.rm.GetServiceInstance(instanceID); ok && inst != nil {
		if h, ok := state.rm.GetHost(inst.HostID()); ok && h != nil {
			return strings.TrimSpace(h.Zone())
		}
	}
	return ""
}

func calleeHostForInstance(state *scenarioState, instanceID string) string {
	if state == nil || state.rm == nil || instanceID == "" {
		return ""
	}
	if inst, ok := state.rm.GetServiceInstance(instanceID); ok && inst != nil {
		return strings.TrimSpace(inst.HostID())
	}
	return ""
}
