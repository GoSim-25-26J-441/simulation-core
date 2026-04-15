package improvement

import "github.com/GoSim-25-26J-441/simulation-core/pkg/config"

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
	copy(out.Hosts, scenario.Hosts)

	for i, svc := range scenario.Services {
		ns := config.Service{
			ID:        svc.ID,
			Kind:      svc.Kind,
			Role:      svc.Role,
			Replicas:  svc.Replicas,
			Model:     svc.Model,
			CPUCores:  svc.CPUCores,
			MemoryMB:  svc.MemoryMB,
			Endpoints: make([]config.Endpoint, len(svc.Endpoints)),
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
		out.Workload[i] = config.WorkloadPattern{
			From:         wl.From,
			SourceKind:   wl.SourceKind,
			TrafficClass: wl.TrafficClass,
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
