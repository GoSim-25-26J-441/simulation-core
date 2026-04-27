package config

// Scenario represents a complete simulation scenario and is the primary
// configuration format for simulation runs (see pkg/config/doc.go).
type Scenario struct {
	Metadata         *ScenarioMetadata `yaml:"metadata,omitempty"`
	SimulationLimits *SimulationLimits `yaml:"simulation_limits,omitempty"`
	// Network holds optional multi-zone latency overlays for downstream service calls.
	Network  *NetworkConfig    `yaml:"network,omitempty"`
	Hosts    []Host            `yaml:"hosts"`
	Services []Service         `yaml:"services"`
	Workload []WorkloadPattern `yaml:"workload"`
	Policies *Policies         `yaml:"policies,omitempty"`
}

// NetworkConfig models optional topology-aware overlays on downstream hop network latency.
// Omitted or all-zero fields preserve legacy behavior.
type NetworkConfig struct {
	// SymmetricCrossZoneLatency: when true, a missing directed edge zone-a -> zone-b uses zone-b -> zone-a if listed.
	// Default false: cross_zone_latency_ms is directed only; define reverse edges explicitly unless symmetric is enabled.
	SymmetricCrossZoneLatency bool `yaml:"symmetric_cross_zone_latency,omitempty"`

	SameHostLatencyMs LatencySpec `yaml:"same_host_latency_ms,omitempty"`
	SameZoneLatencyMs LatencySpec `yaml:"same_zone_latency_ms,omitempty"`

	DefaultCrossZoneLatencyMs LatencySpec                       `yaml:"default_cross_zone_latency_ms,omitempty"`
	CrossZoneLatencyMs        map[string]map[string]LatencySpec `yaml:"cross_zone_latency_ms,omitempty"`

	// ExternalLatencyMs default for downstream hops to services with kind: external (override per service via external_network_latency_ms).
	ExternalLatencyMs LatencySpec `yaml:"external_latency_ms,omitempty"`
}

// SimulationLimits caps downstream trace expansion (async cycles, deep call chains).
// Zero values mean unlimited (backward compatible).
type SimulationLimits struct {
	MaxTraceDepth int `yaml:"max_trace_depth,omitempty"`
	MaxAsyncHops  int `yaml:"max_async_hops,omitempty"`
}

// ScenarioMetadata holds optional scenario versioning (e.g. schema 0.2.0 extensions).
type ScenarioMetadata struct {
	SchemaVersion string `yaml:"schema_version,omitempty"`
}

// Host represents a physical host
type Host struct {
	ID       string            `yaml:"id"`
	Cores    int               `yaml:"cores"`
	MemoryGB int               `yaml:"memory_gb,omitempty"` // Optional; 0 means use simulator default (16 GB)
	Zone     string            `yaml:"zone,omitempty"`
	Labels   map[string]string `yaml:"labels,omitempty"`
}

// Service represents a microservice
type Service struct {
	ID       string  `yaml:"id"`
	Kind     string  `yaml:"kind,omitempty"` // api_gateway, service, database, queue, cache, ...
	Role     string  `yaml:"role,omitempty"` // ingress, internal, datastore, external
	Replicas int     `yaml:"replicas"`
	Model    string  `yaml:"model"` // cpu, mixed, db_latency
	CPUCores float64 `yaml:"cpu_cores,omitempty"`
	MemoryMB float64 `yaml:"memory_mb,omitempty"`
	// ExternalNetworkLatencyMs (optional) overrides scenario.network.external_latency_ms for this service when kind is external.
	ExternalNetworkLatencyMs *LatencySpec     `yaml:"external_network_latency_ms,omitempty"`
	Scaling                  *ScalingPolicy   `yaml:"scaling,omitempty"`
	Behavior                 *ServiceBehavior `yaml:"behavior,omitempty"`
	Placement                *PlacementPolicy `yaml:"placement,omitempty"`
	Routing                  *RoutingPolicy   `yaml:"routing,omitempty"`
	Endpoints                []Endpoint       `yaml:"endpoints"`
}

// PlacementPolicy defines optional topology-aware placement preferences/constraints.
// Empty fields preserve legacy behavior.
type PlacementPolicy struct {
	// RequiredZones constrains placement to these zones when set.
	RequiredZones []string `yaml:"required_zones,omitempty"`
	// PreferredZones biases placement toward these zones when feasible.
	PreferredZones []string `yaml:"preferred_zones,omitempty"`
	// AffinityZones constrains placement to these zones when set.
	AffinityZones []string `yaml:"affinity_zones,omitempty"`
	// AntiAffinityZones excludes placement from these zones when set.
	AntiAffinityZones []string `yaml:"anti_affinity_zones,omitempty"`
	// RequiredHostLabels constrains placement to hosts matching all key/value labels.
	RequiredHostLabels map[string]string `yaml:"required_host_labels,omitempty"`
	// PreferredHostLabels biases placement toward hosts matching these labels.
	PreferredHostLabels map[string]string `yaml:"preferred_host_labels,omitempty"`
	// AntiAffinityServices avoids co-locating this service on hosts already running these services.
	AntiAffinityServices []string `yaml:"anti_affinity_services,omitempty"`
	// SpreadAcrossZones prefers balancing replicas across zones where possible.
	SpreadAcrossZones bool `yaml:"spread_across_zones,omitempty"`
	// MaxReplicasPerHost caps same-service replicas per host when > 0.
	MaxReplicasPerHost int `yaml:"max_replicas_per_host,omitempty"`
}

// ServiceBehavior holds optional failure, saturation, pool, cache, and queue/broker semantics (all backward compatible).
type ServiceBehavior struct {
	FailureRate             float64        `yaml:"failure_rate,omitempty"`
	SaturationLatencyFactor float64        `yaml:"saturation_latency_factor,omitempty"`
	MaxConnections          int            `yaml:"max_connections,omitempty"`
	Cache                   *CacheBehavior `yaml:"cache,omitempty"`
	Queue                   *QueueBehavior `yaml:"queue,omitempty"`
	Topic                   *TopicBehavior `yaml:"topic,omitempty"` // kind: topic — pub/sub fan-out per subscriber group
}

// QueueBehavior configures broker semantics for services with kind: queue.
type QueueBehavior struct {
	Capacity               int         `yaml:"capacity,omitempty"`                 // max queued messages; 0 = unlimited
	ConsumerConcurrency    int         `yaml:"consumer_concurrency,omitempty"`     // parallel consumers; 0 defaults to 1
	MinConsumerConcurrency int         `yaml:"min_consumer_concurrency,omitempty"` // optional optimizer/runtime lower bound (>=1 when set)
	MaxConsumerConcurrency int         `yaml:"max_consumer_concurrency,omitempty"` // optional optimizer/runtime upper bound (>= min when set)
	ConsumerTarget         string      `yaml:"consumer_target,omitempty"`          // required: "serviceID:path" for dequeue processing
	DeliveryLatencyMs      LatencySpec `yaml:"delivery_latency_ms,omitempty"`      // publish / broker ack latency
	AckTimeoutMs           float64     `yaml:"ack_timeout_ms,omitempty"`           // consumer must finish within this from dequeue
	MaxRedeliveries        int         `yaml:"max_redeliveries,omitempty"`         // after this, message goes to DLQ
	DLQTarget              string      `yaml:"dlq,omitempty"`                      // optional "serviceID:path" for dead-letter handling
	DropPolicy             string      `yaml:"drop_policy,omitempty"`              // block, reject, drop_oldest, drop_newest
	AsyncFireAndForget     bool        `yaml:"async_fire_and_forget,omitempty"`    // if true, producer hop finalizes before broker ack (no publish wait)
}

// TopicBehavior configures pub/sub broker semantics for services with kind: topic.
// Each subscriber defines an independent consumer group (backlog, concurrency, redelivery, DLQ).
type TopicBehavior struct {
	// Partitions splits the topic into independent per-partition FIFO logs (runtime DES), each with a monotonic offset.
	Partitions int `yaml:"partitions,omitempty"`
	// RetentionMs is max queued age in simulation time before messages are dropped (retention_expired); enforced via DES retention events.
	RetentionMs        int64             `yaml:"retention_ms,omitempty"`
	Capacity           int               `yaml:"capacity,omitempty"`            // max backlog per subscriber group; -1 = unlimited
	DeliveryLatencyMs  LatencySpec       `yaml:"delivery_latency_ms,omitempty"` // publish / leader ack latency
	PublishAck         string            `yaml:"publish_ack,omitempty"`         // e.g. leader_ack (label/metadata)
	AsyncFireAndForget bool              `yaml:"async_fire_and_forget,omitempty"`
	Subscribers        []TopicSubscriber `yaml:"subscribers,omitempty"`
}

// TopicSubscriber is one consumer group subscription on a topic service.
type TopicSubscriber struct {
	Name                   string  `yaml:"name,omitempty"`                     // display name for metrics
	ConsumerTarget         string  `yaml:"consumer_target,omitempty"`          // required: serviceID:path
	ConsumerGroup          string  `yaml:"consumer_group,omitempty"`           // required: unique key among subscribers
	ConsumerConcurrency    int     `yaml:"consumer_concurrency,omitempty"`     // parallel consumers; 0 defaults to 1
	MinConsumerConcurrency int     `yaml:"min_consumer_concurrency,omitempty"` // optional optimizer/runtime lower bound (>=1 when set)
	MaxConsumerConcurrency int     `yaml:"max_consumer_concurrency,omitempty"` // optional optimizer/runtime upper bound (>= min when set)
	AckTimeoutMs           float64 `yaml:"ack_timeout_ms,omitempty"`
	MaxRedeliveries        int     `yaml:"max_redeliveries,omitempty"`
	DLQ                    string  `yaml:"dlq,omitempty"` // optional serviceID:path
	DropPolicy             string  `yaml:"drop_policy,omitempty"`
}

// CacheBehavior configures hit/miss latency sampling for cache-style services.
type CacheBehavior struct {
	HitRate       float64     `yaml:"hit_rate,omitempty"`
	HitLatencyMs  LatencySpec `yaml:"hit_latency_ms,omitempty"`
	MissLatencyMs LatencySpec `yaml:"miss_latency_ms,omitempty"`
}

// ScalingPolicy declares which scaling dimensions are allowed for batch/online optimizers.
type ScalingPolicy struct {
	Horizontal     bool `yaml:"horizontal,omitempty"`
	VerticalCPU    bool `yaml:"vertical_cpu,omitempty"`
	VerticalMemory bool `yaml:"vertical_memory,omitempty"`
}

// Endpoint represents a service endpoint
type Endpoint struct {
	Path            string           `yaml:"path"`
	MeanCPUMs       float64          `yaml:"mean_cpu_ms"`
	CPUSigmaMs      float64          `yaml:"cpu_sigma_ms"`
	DefaultMemoryMB float64          `yaml:"default_memory_mb,omitempty"` // Default memory usage in MB (optional, defaults to 10.0)
	FailureRate     float64          `yaml:"failure_rate,omitempty"`
	TimeoutMs       float64          `yaml:"timeout_ms,omitempty"` // Local processing deadline from StartTime (optional)
	IOMs            LatencySpec      `yaml:"io_ms,omitempty"`
	ConnectionPool  int              `yaml:"connection_pool,omitempty"`
	Routing         *RoutingPolicy   `yaml:"routing,omitempty"`
	Downstream      []DownstreamCall `yaml:"downstream"`
	NetLatencyMs    LatencySpec      `yaml:"net_latency_ms"`
}

// RoutingPolicy configures request-to-instance routing/load-balancing behavior.
// Defaults preserve legacy behavior: strategy=round_robin.
type RoutingPolicy struct {
	// Strategy selects the balancing method:
	// round_robin (default), random, least_connections, least_queue, least_cpu, weighted_round_robin, sticky.
	Strategy string `yaml:"strategy,omitempty"`
	// StickyKeyFrom uses request.Metadata[sticky_key_from] as hash input for sticky routing.
	StickyKeyFrom string `yaml:"sticky_key_from,omitempty"`
	// LocalityZoneFrom uses request.Metadata[locality_zone_from] as preferred host zone for routing.
	// When present, routing first considers same-zone instances; if none exist, it falls back to all instances.
	LocalityZoneFrom string `yaml:"locality_zone_from,omitempty"`
	// Weights applies to weighted_round_robin; map key is instance ID (e.g. "api-instance-0"), value >= 0.
	Weights map[string]float64 `yaml:"weights,omitempty"`
}

// DownstreamCall represents a call to a downstream service
type DownstreamCall struct {
	To                    string      `yaml:"to"`
	Mode                  string      `yaml:"mode,omitempty"` // sync (default), async, event
	Kind                  string      `yaml:"kind,omitempty"` // rest, grpc, db, queue, external
	Probability           float64     `yaml:"probability,omitempty"`
	CallCountMean         float64     `yaml:"call_count_mean,omitempty"`
	CallLatencyMs         LatencySpec `yaml:"call_latency_ms,omitempty"`
	TimeoutMs             float64     `yaml:"timeout_ms,omitempty"`
	FailureRate           float64     `yaml:"failure_rate,omitempty"`
	Retryable             *bool       `yaml:"retryable,omitempty"` // default true when omitted
	DownstreamFractionCPU float64     `yaml:"downstream_fraction_cpu,omitempty"`
	// PartitionKey (downstream.kind: topic) static key for deterministic partition assignment; overrides PartitionKeyFrom when both set.
	PartitionKey string `yaml:"partition_key,omitempty"`
	// PartitionKeyFrom names a key in the parent request Metadata whose string value is used as the partition key (e.g. tenant_id).
	PartitionKeyFrom string `yaml:"partition_key_from,omitempty"`
}

// LatencySpec represents latency with mean and standard deviation
type LatencySpec struct {
	Mean  float64 `yaml:"mean"`
	Sigma float64 `yaml:"sigma"`
}

// WorkloadPattern represents a workload entry point
type WorkloadPattern struct {
	From         string `yaml:"from"`
	SourceKind   string `yaml:"source_kind,omitempty"`   // e.g. client
	TrafficClass string `yaml:"traffic_class,omitempty"` // ingress, background, replay
	// Metadata is copied into request metadata for arrivals generated from this workload pattern.
	Metadata map[string]string `yaml:"metadata,omitempty"`
	To       string            `yaml:"to"`
	Arrival  ArrivalSpec       `yaml:"arrival"`
}

// ArrivalSpec represents arrival process specification
type ArrivalSpec struct {
	Type                 string  `yaml:"type"`                             // poisson, uniform, normal, bursty, constant
	RateRPS              float64 `yaml:"rate_rps"`                         // Mean/constant rate in requests per second
	StdDevRPS            float64 `yaml:"stddev_rps,omitempty"`             // Standard deviation for normal distribution
	BurstRateRPS         float64 `yaml:"burst_rate_rps,omitempty"`         // Rate during bursts (for bursty type)
	BurstDurationSeconds float64 `yaml:"burst_duration_seconds,omitempty"` // Duration of burst periods
	QuietDurationSeconds float64 `yaml:"quiet_duration_seconds,omitempty"` // Duration of quiet periods between bursts
}
