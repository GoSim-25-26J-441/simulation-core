package config

import (
	"fmt"
	"os"
	"strings"
)

// LoadConfig loads and parses a Config file (legacy cluster/graph format).
// For simulation runs, use Scenario and ParseScenarioYAML instead.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}
	cfg, err := ParseConfigYAML(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", path, err)
	}
	return cfg, nil
}

// LoadScenario loads and parses a scenario file
func LoadScenario(path string) (*Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read scenario file %s: %w", path, err)
	}
	scenario, err := ParseScenarioYAML(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse scenario file %s: %w", path, err)
	}
	return scenario, nil
}

// validateConfig performs validation on the configuration
func validateConfig(cfg *Config) error {
	// Validate log level
	validLogLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLogLevels[cfg.LogLevel] {
		return fmt.Errorf("invalid log_level: %s (must be debug, info, warn, or error)", cfg.LogLevel)
	}

	// Validate clusters
	if len(cfg.Clusters) == 0 {
		return fmt.Errorf("at least one cluster must be defined")
	}
	clusterNames := make(map[string]bool)
	for _, cluster := range cfg.Clusters {
		if cluster.Name == "" {
			return fmt.Errorf("cluster name cannot be empty")
		}
		if clusterNames[cluster.Name] {
			return fmt.Errorf("duplicate cluster name: %s", cluster.Name)
		}
		clusterNames[cluster.Name] = true
		if cluster.NetworkRTTMs < 0 {
			return fmt.Errorf("cluster %s: network_rtt_ms cannot be negative", cluster.Name)
		}
		if cluster.Capacity.CPUCores <= 0 {
			return fmt.Errorf("cluster %s: cpu_cores must be positive", cluster.Name)
		}
		if cluster.Capacity.MemGB <= 0 {
			return fmt.Errorf("cluster %s: mem_gb must be positive", cluster.Name)
		}
	}

	// Validate service graph if present
	if cfg.ServiceGraph != nil {
		if err := validateServiceGraph(cfg.ServiceGraph, clusterNames); err != nil {
			return fmt.Errorf("service_graph validation failed: %w", err)
		}
	}

	// Validate workload if present
	if cfg.Workload != nil {
		if err := validateWorkload(cfg.Workload); err != nil {
			return fmt.Errorf("workload validation failed: %w", err)
		}
	}

	// Validate policies if present
	if cfg.Policies != nil {
		if err := validatePolicies(cfg.Policies); err != nil {
			return fmt.Errorf("policies validation failed: %w", err)
		}
	}

	// Validate optimization if present
	if cfg.Optimization != nil {
		if err := validateOptimization(cfg.Optimization); err != nil {
			return fmt.Errorf("optimization validation failed: %w", err)
		}
	}

	return nil
}

// validateServiceGraph validates the service graph
func validateServiceGraph(sg *ServiceGraph, clusterNames map[string]bool) error {
	if len(sg.Nodes) == 0 {
		return fmt.Errorf("service graph must have at least one node")
	}

	serviceNames := make(map[string]bool)
	for _, node := range sg.Nodes {
		if node.Name == "" {
			return fmt.Errorf("service node name cannot be empty")
		}
		if serviceNames[node.Name] {
			return fmt.Errorf("duplicate service name: %s", node.Name)
		}
		serviceNames[node.Name] = true

		if node.Cluster != "" && !clusterNames[node.Cluster] {
			return fmt.Errorf("service %s references unknown cluster: %s", node.Name, node.Cluster)
		}
		if node.CPUCostMs < 0 {
			return fmt.Errorf("service %s: cpu_cost_ms cannot be negative", node.Name)
		}
		if node.Concurrency < 0 {
			return fmt.Errorf("service %s: concurrency cannot be negative", node.Name)
		}
	}

	// Validate edges
	for i, edge := range sg.Edges {
		if !serviceNames[edge.From] {
			return fmt.Errorf("edge %d: 'from' service %s does not exist", i, edge.From)
		}
		if !serviceNames[edge.To] {
			return fmt.Errorf("edge %d: 'to' service %s does not exist", i, edge.To)
		}
		if edge.Mode != "sync" && edge.Mode != "async" {
			return fmt.Errorf("edge %d: mode must be 'sync' or 'async', got %s", i, edge.Mode)
		}
		if edge.P < 0 || edge.P > 1 {
			return fmt.Errorf("edge %d: probability p must be between 0 and 1, got %f", i, edge.P)
		}
	}

	return nil
}

// validateWorkload validates the workload configuration
func validateWorkload(w *Workload) error {
	norm, err := NormalizeArrivalType(w.Arrival)
	if err != nil {
		return fmt.Errorf("invalid arrival: %w", err)
	}
	w.Arrival = norm

	if w.RateRPS <= 0 {
		return fmt.Errorf("rate_rps must be positive, got %d", w.RateRPS)
	}

	if _, err := w.GetDuration(); err != nil {
		return fmt.Errorf("invalid duration %s: %w", w.Duration, err)
	}

	if _, err := w.GetWarmup(); err != nil {
		return fmt.Errorf("invalid warmup %s: %w", w.Warmup, err)
	}

	return nil
}

// validatePolicies validates the policies configuration
func validatePolicies(p *Policies) error {
	if p.Autoscaling != nil {
		if p.Autoscaling.TargetCPUUtil <= 0 || p.Autoscaling.TargetCPUUtil > 1 {
			return fmt.Errorf("autoscaling target_cpu_util must be between 0 and 1, got %f", p.Autoscaling.TargetCPUUtil)
		}
		if p.Autoscaling.ScaleStep <= 0 {
			return fmt.Errorf("autoscaling scale_step must be positive, got %d", p.Autoscaling.ScaleStep)
		}
	}

	if p.Retries != nil {
		if p.Retries.MaxRetries < 0 {
			return fmt.Errorf("retries max_retries cannot be negative, got %d", p.Retries.MaxRetries)
		}
		validBackoffs := map[string]bool{
			"exponential": true,
			"linear":      true,
			"constant":    true,
		}
		if !validBackoffs[p.Retries.Backoff] {
			return fmt.Errorf("invalid backoff type: %s (must be exponential, linear, or constant)", p.Retries.Backoff)
		}
		if p.Retries.BaseMs < 0 {
			return fmt.Errorf("retries base_ms cannot be negative, got %d", p.Retries.BaseMs)
		}
	}

	return nil
}

// validateOptimization validates the optimization configuration
func validateOptimization(o *Optimization) error {
	if o.Objective == "" {
		return fmt.Errorf("optimization objective cannot be empty")
	}
	if o.MaxIterations <= 0 {
		return fmt.Errorf("max_iterations must be positive, got %d", o.MaxIterations)
	}
	return nil
}

// validateScenario validates the scenario configuration
func validateScenario(s *Scenario) error {
	if s.SimulationLimits != nil {
		if s.SimulationLimits.MaxTraceDepth < 0 {
			return fmt.Errorf("simulation_limits.max_trace_depth cannot be negative")
		}
		if s.SimulationLimits.MaxAsyncHops < 0 {
			return fmt.Errorf("simulation_limits.max_async_hops cannot be negative")
		}
	}

	// Validate hosts
	if len(s.Hosts) == 0 {
		return fmt.Errorf("at least one host must be defined")
	}
	hostIDs := make(map[string]bool)
	for _, host := range s.Hosts {
		if host.ID == "" {
			return fmt.Errorf("host id cannot be empty")
		}
		if hostIDs[host.ID] {
			return fmt.Errorf("duplicate host id: %s", host.ID)
		}
		hostIDs[host.ID] = true
		if host.Cores <= 0 {
			return fmt.Errorf("host %s: cores must be positive", host.ID)
		}
		if host.MemoryGB < 0 {
			return fmt.Errorf("host %s: memory_gb cannot be negative", host.ID)
		}
	}

	// Validate services
	if len(s.Services) == 0 {
		return fmt.Errorf("at least one service must be defined")
	}
	serviceIDs := make(map[string]bool)

	// First pass: collect service IDs and validate basic properties
	for _, svc := range s.Services {
		if svc.ID == "" {
			return fmt.Errorf("service id cannot be empty")
		}
		if serviceIDs[svc.ID] {
			return fmt.Errorf("duplicate service id: %s", svc.ID)
		}
		serviceIDs[svc.ID] = true

		if svc.Replicas <= 0 {
			return fmt.Errorf("service %s: replicas must be positive", svc.ID)
		}

		kindNorm := strings.ToLower(strings.TrimSpace(svc.Kind))
		validKinds := map[string]bool{
			"": true, "api_gateway": true, "service": true, "database": true, "cache": true, "external": true, "queue": true,
		}
		if !validKinds[kindNorm] {
			return fmt.Errorf("service %s: unknown or unsupported kind %q", svc.ID, svc.Kind)
		}

		validModels := map[string]bool{
			"cpu":        true,
			"mixed":      true,
			"db_latency": true,
		}
		if !validModels[svc.Model] {
			return fmt.Errorf("service %s: invalid model %s (must be cpu, mixed, or db_latency)", svc.ID, svc.Model)
		}

		if len(svc.Endpoints) == 0 {
			return fmt.Errorf("service %s: at least one endpoint must be defined", svc.ID)
		}

		if svc.Behavior != nil {
			b := svc.Behavior
			if b.FailureRate < 0 || b.FailureRate > 1 {
				return fmt.Errorf("service %s: behavior.failure_rate must be in [0,1], got %v", svc.ID, b.FailureRate)
			}
			if b.SaturationLatencyFactor < 0 {
				return fmt.Errorf("service %s: behavior.saturation_latency_factor cannot be negative", svc.ID)
			}
			if b.MaxConnections < 0 {
				return fmt.Errorf("service %s: behavior.max_connections cannot be negative", svc.ID)
			}
			if b.Cache != nil {
				c := b.Cache
				if c.HitRate < 0 || c.HitRate > 1 {
					return fmt.Errorf("service %s: behavior.cache.hit_rate must be in [0,1], got %v", svc.ID, c.HitRate)
				}
				if c.HitLatencyMs.Mean < 0 || c.HitLatencyMs.Sigma < 0 {
					return fmt.Errorf("service %s: behavior.cache.hit_latency_ms mean/sigma cannot be negative", svc.ID)
				}
				if c.MissLatencyMs.Mean < 0 || c.MissLatencyMs.Sigma < 0 {
					return fmt.Errorf("service %s: behavior.cache.miss_latency_ms mean/sigma cannot be negative", svc.ID)
				}
			}
		}

		for _, ep := range svc.Endpoints {
			if ep.Path == "" {
				return fmt.Errorf("service %s: endpoint path cannot be empty", svc.ID)
			}
			if ep.MeanCPUMs < 0 {
				return fmt.Errorf("service %s, endpoint %s: mean_cpu_ms cannot be negative", svc.ID, ep.Path)
			}
			if ep.CPUSigmaMs < 0 {
				return fmt.Errorf("service %s, endpoint %s: cpu_sigma_ms cannot be negative", svc.ID, ep.Path)
			}
			if ep.NetLatencyMs.Mean < 0 {
				return fmt.Errorf("service %s, endpoint %s: net_latency_ms.mean cannot be negative", svc.ID, ep.Path)
			}
			if ep.NetLatencyMs.Sigma < 0 {
				return fmt.Errorf("service %s, endpoint %s: net_latency_ms.sigma cannot be negative", svc.ID, ep.Path)
			}
			if ep.FailureRate < 0 || ep.FailureRate > 1 {
				return fmt.Errorf("service %s, endpoint %s: failure_rate must be in [0,1], got %v", svc.ID, ep.Path, ep.FailureRate)
			}
			if ep.TimeoutMs < 0 {
				return fmt.Errorf("service %s, endpoint %s: timeout_ms cannot be negative", svc.ID, ep.Path)
			}
			if ep.IOMs.Mean < 0 || ep.IOMs.Sigma < 0 {
				return fmt.Errorf("service %s, endpoint %s: io_ms mean/sigma cannot be negative", svc.ID, ep.Path)
			}
			if ep.ConnectionPool < 0 {
				return fmt.Errorf("service %s, endpoint %s: connection_pool cannot be negative", svc.ID, ep.Path)
			}
		}
	}

	endpointRef := make(map[string]bool)
	serviceKindByID := make(map[string]string)
	for _, svc := range s.Services {
		serviceKindByID[svc.ID] = strings.ToLower(strings.TrimSpace(svc.Kind))
		for _, ep := range svc.Endpoints {
			endpointRef[svc.ID+":"+ep.Path] = true
		}
	}

	for _, svc := range s.Services {
		if strings.ToLower(strings.TrimSpace(svc.Kind)) != "queue" {
			continue
		}
		var q *QueueBehavior
		if svc.Behavior != nil {
			q = svc.Behavior.Queue
		}
		if err := ValidateQueueBehavior(svc.ID, q, endpointRef, serviceIDs); err != nil {
			return err
		}
	}

	validDownstreamModes := map[string]bool{
		"": true, "sync": true, "async": true, "event": true,
	}
	validDownstreamKinds := map[string]bool{
		"": true, "rest": true, "grpc": true, "db": true, "queue": true, "external": true,
	}

	// Second pass: validate downstream calls now that all service IDs are known
	for _, svc := range s.Services {
		for _, ep := range svc.Endpoints {
			for _, ds := range ep.Downstream {
				if ds.To == "" {
					return fmt.Errorf("service %s, endpoint %s: downstream 'to' cannot be empty", svc.ID, ep.Path)
				}
				tgtSvc, tgtPath, err := parseDownstreamTargetForValidation(ds.To)
				if err != nil {
					return fmt.Errorf("service %s, endpoint %s: downstream to %q: %w", svc.ID, ep.Path, ds.To, err)
				}
				if !serviceIDs[tgtSvc] {
					return fmt.Errorf("service %s, endpoint %s: downstream service %s does not exist", svc.ID, ep.Path, tgtSvc)
				}
				if !endpointRef[tgtSvc+":"+tgtPath] {
					return fmt.Errorf("service %s, endpoint %s: downstream endpoint %s:%s does not exist", svc.ID, ep.Path, tgtSvc, tgtPath)
				}
				mode := strings.ToLower(strings.TrimSpace(ds.Mode))
				if !validDownstreamModes[mode] {
					return fmt.Errorf("service %s, endpoint %s: invalid downstream mode %q", svc.ID, ep.Path, ds.Mode)
				}
				kind := strings.ToLower(strings.TrimSpace(ds.Kind))
				if !validDownstreamKinds[kind] {
					return fmt.Errorf("service %s, endpoint %s: invalid downstream kind %q", svc.ID, ep.Path, ds.Kind)
				}
				if ds.Probability < 0 || ds.Probability > 1 {
					return fmt.Errorf("service %s, endpoint %s: downstream probability must be in [0,1], got %v", svc.ID, ep.Path, ds.Probability)
				}
				if ds.TimeoutMs < 0 {
					return fmt.Errorf("service %s, endpoint %s: downstream timeout_ms cannot be negative", svc.ID, ep.Path)
				}
				if ds.FailureRate < 0 || ds.FailureRate > 1 {
					return fmt.Errorf("service %s, endpoint %s: downstream failure_rate must be in [0,1], got %v", svc.ID, ep.Path, ds.FailureRate)
				}
				if ds.DownstreamFractionCPU < 0 || ds.DownstreamFractionCPU > 1 {
					return fmt.Errorf("service %s, endpoint %s: downstream_fraction_cpu must be in [0,1], got %v", svc.ID, ep.Path, ds.DownstreamFractionCPU)
				}
				tgtKind := serviceKindByID[tgtSvc]
				if kind == "queue" && tgtKind != "queue" {
					return fmt.Errorf("service %s, endpoint %s: downstream kind queue requires target service %s to have kind queue", svc.ID, ep.Path, tgtSvc)
				}
				if tgtKind == "queue" && kind != "" && kind != "queue" {
					return fmt.Errorf("service %s, endpoint %s: downstream to queue service %s must use kind queue (or omit kind)", svc.ID, ep.Path, tgtSvc)
				}
			}
		}
	}

	// Validate workload
	if len(s.Workload) == 0 {
		return fmt.Errorf("at least one workload pattern must be defined")
	}
	for i := range s.Workload {
		wl := &s.Workload[i]
		if wl.From == "" {
			return fmt.Errorf("workload %d: 'from' cannot be empty", i)
		}
		if wl.To == "" {
			return fmt.Errorf("workload %d: 'to' cannot be empty", i)
		}
		if wl.Arrival.Type == "" {
			return fmt.Errorf("workload %d: arrival type cannot be empty", i)
		}
		norm, err := NormalizeArrivalType(wl.Arrival.Type)
		if err != nil {
			return fmt.Errorf("workload %d: %w", i, err)
		}
		wl.Arrival.Type = norm
		if wl.Arrival.RateRPS <= 0 {
			return fmt.Errorf("workload %d: arrival rate_rps must be positive", i)
		}
		wlSvc, wlPath, err := parseDownstreamTargetForValidation(wl.To)
		if err != nil {
			return fmt.Errorf("workload %d: invalid to %q: %w", i, wl.To, err)
		}
		if !serviceIDs[wlSvc] {
			return fmt.Errorf("workload %d: target service %s does not exist", i, wlSvc)
		}
		if !endpointRef[wlSvc+":"+wlPath] {
			return fmt.Errorf("workload %d: target endpoint %s:%s does not exist", i, wlSvc, wlPath)
		}
	}

	return nil
}

func parseDownstreamTargetForValidation(target string) (serviceID, path string, err error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", "", fmt.Errorf("target cannot be empty")
	}
	if strings.Contains(target, ":") {
		parts := strings.SplitN(target, ":", 2)
		if len(parts) != 2 {
			return "", "", fmt.Errorf("invalid target format")
		}
		serviceID = strings.TrimSpace(parts[0])
		path = strings.TrimSpace(parts[1])
		if serviceID == "" || path == "" {
			return "", "", fmt.Errorf("service and path must be non-empty")
		}
		return serviceID, path, nil
	}
	return target, "/", nil
}
