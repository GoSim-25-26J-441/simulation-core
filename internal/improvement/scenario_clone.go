package improvement

import "github.com/GoSim-25-26J-441/simulation-core/pkg/config"

func cloneRoutingPolicy(rp *config.RoutingPolicy) *config.RoutingPolicy {
	if rp == nil {
		return nil
	}
	out := &config.RoutingPolicy{
		Strategy:         rp.Strategy,
		StickyKeyFrom:    rp.StickyKeyFrom,
		LocalityZoneFrom: rp.LocalityZoneFrom,
	}
	if len(rp.Weights) > 0 {
		out.Weights = make(map[string]float64, len(rp.Weights))
		for k, v := range rp.Weights {
			out.Weights[k] = v
		}
	}
	return out
}

func clonePlacementPolicy(pp *config.PlacementPolicy) *config.PlacementPolicy {
	if pp == nil {
		return nil
	}
	out := &config.PlacementPolicy{
		RequiredZones:        append([]string(nil), pp.RequiredZones...),
		PreferredZones:       append([]string(nil), pp.PreferredZones...),
		AffinityZones:        append([]string(nil), pp.AffinityZones...),
		AntiAffinityZones:    append([]string(nil), pp.AntiAffinityZones...),
		AntiAffinityServices: append([]string(nil), pp.AntiAffinityServices...),
		SpreadAcrossZones:    pp.SpreadAcrossZones,
		MaxReplicasPerHost:   pp.MaxReplicasPerHost,
	}
	if len(pp.RequiredHostLabels) > 0 {
		out.RequiredHostLabels = make(map[string]string, len(pp.RequiredHostLabels))
		for k, v := range pp.RequiredHostLabels {
			out.RequiredHostLabels[k] = v
		}
	}
	if len(pp.PreferredHostLabels) > 0 {
		out.PreferredHostLabels = make(map[string]string, len(pp.PreferredHostLabels))
		for k, v := range pp.PreferredHostLabels {
			out.PreferredHostLabels[k] = v
		}
	}
	return out
}

// cloneScenario returns a deep copy of the scenario so batch/optimizer neighbors
// preserve v2 metadata, service kind/role/scaling, downstream call semantics, workload
// source/traffic fields, limits, and policies.
func cloneScenario(scenario *config.Scenario) *config.Scenario {
	if scenario == nil {
		return nil
	}
	out := &config.Scenario{
		Hosts:    make([]config.Host, len(scenario.Hosts)),
		Services: make([]config.Service, len(scenario.Services)),
		Workload: make([]config.WorkloadPattern, len(scenario.Workload)),
	}
	if scenario.Metadata != nil {
		out.Metadata = &config.ScenarioMetadata{SchemaVersion: scenario.Metadata.SchemaVersion}
	}
	if scenario.SimulationLimits != nil {
		out.SimulationLimits = &config.SimulationLimits{
			MaxTraceDepth: scenario.SimulationLimits.MaxTraceDepth,
			MaxAsyncHops:  scenario.SimulationLimits.MaxAsyncHops,
		}
	}
	if scenario.Network != nil {
		n := scenario.Network
		out.Network = &config.NetworkConfig{
			SymmetricCrossZoneLatency: n.SymmetricCrossZoneLatency,
			SameHostLatencyMs:         n.SameHostLatencyMs,
			SameZoneLatencyMs:         n.SameZoneLatencyMs,
			DefaultCrossZoneLatencyMs: n.DefaultCrossZoneLatencyMs,
			ExternalLatencyMs:         n.ExternalLatencyMs,
		}
		if len(n.CrossZoneLatencyMs) > 0 {
			out.Network.CrossZoneLatencyMs = make(map[string]map[string]config.LatencySpec, len(n.CrossZoneLatencyMs))
			for fk, inner := range n.CrossZoneLatencyMs {
				out.Network.CrossZoneLatencyMs[fk] = make(map[string]config.LatencySpec, len(inner))
				for tk, ls := range inner {
					out.Network.CrossZoneLatencyMs[fk][tk] = ls
				}
			}
		}
	}
	for i := range scenario.Hosts {
		out.Hosts[i] = scenario.Hosts[i]
		if len(scenario.Hosts[i].Labels) > 0 {
			out.Hosts[i].Labels = make(map[string]string, len(scenario.Hosts[i].Labels))
			for k, v := range scenario.Hosts[i].Labels {
				out.Hosts[i].Labels[k] = v
			}
		}
	}

	for i, svc := range scenario.Services {
		ns := config.Service{
			ID:        svc.ID,
			Kind:      svc.Kind,
			Role:      svc.Role,
			Replicas:  svc.Replicas,
			Model:     svc.Model,
			CPUCores:  svc.CPUCores,
			MemoryMB:  svc.MemoryMB,
			Placement: clonePlacementPolicy(svc.Placement),
			Routing:   cloneRoutingPolicy(svc.Routing),
			Endpoints: make([]config.Endpoint, len(svc.Endpoints)),
		}
		if svc.ExternalNetworkLatencyMs != nil {
			ls := *svc.ExternalNetworkLatencyMs
			ns.ExternalNetworkLatencyMs = &ls
		}
		if svc.Scaling != nil {
			ns.Scaling = &config.ScalingPolicy{
				Horizontal:     svc.Scaling.Horizontal,
				VerticalCPU:    svc.Scaling.VerticalCPU,
				VerticalMemory: svc.Scaling.VerticalMemory,
			}
		}
		if svc.Behavior != nil {
			b := svc.Behavior
			ns.Behavior = &config.ServiceBehavior{
				FailureRate:             b.FailureRate,
				SaturationLatencyFactor: b.SaturationLatencyFactor,
				MaxConnections:          b.MaxConnections,
			}
			if b.Cache != nil {
				ns.Behavior.Cache = &config.CacheBehavior{
					HitRate:       b.Cache.HitRate,
					HitLatencyMs:  b.Cache.HitLatencyMs,
					MissLatencyMs: b.Cache.MissLatencyMs,
				}
			}
			if b.Queue != nil {
				q := b.Queue
				ns.Behavior.Queue = &config.QueueBehavior{
					Capacity:               q.Capacity,
					ConsumerConcurrency:    q.ConsumerConcurrency,
					MinConsumerConcurrency: q.MinConsumerConcurrency,
					MaxConsumerConcurrency: q.MaxConsumerConcurrency,
					ConsumerTarget:         q.ConsumerTarget,
					DeliveryLatencyMs:      q.DeliveryLatencyMs,
					AckTimeoutMs:           q.AckTimeoutMs,
					MaxRedeliveries:        q.MaxRedeliveries,
					DLQTarget:              q.DLQTarget,
					DropPolicy:             q.DropPolicy,
					AsyncFireAndForget:     q.AsyncFireAndForget,
				}
			}
			if b.Topic != nil {
				t := b.Topic
				nt := &config.TopicBehavior{
					Partitions:         t.Partitions,
					RetentionMs:        t.RetentionMs,
					Capacity:           t.Capacity,
					DeliveryLatencyMs:  t.DeliveryLatencyMs,
					PublishAck:         t.PublishAck,
					AsyncFireAndForget: t.AsyncFireAndForget,
					Subscribers:        make([]config.TopicSubscriber, len(t.Subscribers)),
				}
				copy(nt.Subscribers, t.Subscribers)
				ns.Behavior.Topic = nt
			}
		}
		for j, ep := range svc.Endpoints {
			ne := config.Endpoint{
				Path:            ep.Path,
				MeanCPUMs:       ep.MeanCPUMs,
				CPUSigmaMs:      ep.CPUSigmaMs,
				DefaultMemoryMB: ep.DefaultMemoryMB,
				FailureRate:     ep.FailureRate,
				TimeoutMs:       ep.TimeoutMs,
				IOMs:            ep.IOMs,
				ConnectionPool:  ep.ConnectionPool,
				Routing:         cloneRoutingPolicy(ep.Routing),
				NetLatencyMs:    ep.NetLatencyMs,
				Downstream:      make([]config.DownstreamCall, len(ep.Downstream)),
			}
			for k, ds := range ep.Downstream {
				dc := config.DownstreamCall{
					To:                    ds.To,
					Mode:                  ds.Mode,
					Kind:                  ds.Kind,
					Probability:           ds.Probability,
					CallCountMean:         ds.CallCountMean,
					CallLatencyMs:         ds.CallLatencyMs,
					TimeoutMs:             ds.TimeoutMs,
					FailureRate:           ds.FailureRate,
					DownstreamFractionCPU: ds.DownstreamFractionCPU,
					PartitionKey:          ds.PartitionKey,
					PartitionKeyFrom:      ds.PartitionKeyFrom,
				}
				if ds.Retryable != nil {
					v := *ds.Retryable
					dc.Retryable = &v
				}
				ne.Downstream[k] = dc
			}
			ns.Endpoints[j] = ne
		}
		out.Services[i] = ns
	}

	for i, wl := range scenario.Workload {
		var wlMetadata map[string]string
		if len(wl.Metadata) > 0 {
			wlMetadata = make(map[string]string, len(wl.Metadata))
			for k, v := range wl.Metadata {
				wlMetadata[k] = v
			}
		}
		out.Workload[i] = config.WorkloadPattern{
			From:         wl.From,
			SourceKind:   wl.SourceKind,
			TrafficClass: wl.TrafficClass,
			Metadata:     wlMetadata,
			To:           wl.To,
			Arrival:      wl.Arrival,
		}
	}

	if scenario.Policies != nil {
		out.Policies = &config.Policies{}
		if scenario.Policies.Autoscaling != nil {
			out.Policies.Autoscaling = &config.AutoscalingPolicy{
				Enabled:       scenario.Policies.Autoscaling.Enabled,
				TargetCPUUtil: scenario.Policies.Autoscaling.TargetCPUUtil,
				ScaleStep:     scenario.Policies.Autoscaling.ScaleStep,
			}
		}
		if scenario.Policies.Retries != nil {
			out.Policies.Retries = &config.RetryPolicy{
				Enabled:    scenario.Policies.Retries.Enabled,
				MaxRetries: scenario.Policies.Retries.MaxRetries,
				Backoff:    scenario.Policies.Retries.Backoff,
				BaseMs:     scenario.Policies.Retries.BaseMs,
			}
		}
	}

	return out
}

// CloneScenario returns a deep copy of the scenario for safe mutation (optimizer, calibration).
func CloneScenario(scenario *config.Scenario) *config.Scenario {
	return cloneScenario(scenario)
}
