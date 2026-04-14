package config

// Scenario represents a complete simulation scenario and is the primary
// configuration format for simulation runs (see pkg/config/doc.go).
type Scenario struct {
	Metadata         *ScenarioMetadata  `yaml:"metadata,omitempty"`
	SimulationLimits *SimulationLimits  `yaml:"simulation_limits,omitempty"`
	Hosts            []Host             `yaml:"hosts"`
	Services         []Service          `yaml:"services"`
	Workload         []WorkloadPattern  `yaml:"workload"`
	Policies         *Policies          `yaml:"policies,omitempty"`
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
	ID       string `yaml:"id"`
	Cores    int    `yaml:"cores"`
	MemoryGB int    `yaml:"memory_gb,omitempty"` // Optional; 0 means use simulator default (16 GB)
}

// Service represents a microservice
type Service struct {
	ID        string          `yaml:"id"`
	Kind      string          `yaml:"kind,omitempty"`   // api_gateway, service, database, queue, cache, ...
	Role      string          `yaml:"role,omitempty"`   // ingress, internal, datastore, external
	Replicas  int             `yaml:"replicas"`
	Model     string          `yaml:"model"` // cpu, mixed, db_latency
	CPUCores  float64         `yaml:"cpu_cores,omitempty"`
	MemoryMB  float64         `yaml:"memory_mb,omitempty"`
	Scaling   *ScalingPolicy  `yaml:"scaling,omitempty"`
	Behavior  *ServiceBehavior `yaml:"behavior,omitempty"`
	Endpoints []Endpoint      `yaml:"endpoints"`
}

// ServiceBehavior holds optional failure, saturation, pool, and cache semantics (all backward compatible).
type ServiceBehavior struct {
	FailureRate               float64         `yaml:"failure_rate,omitempty"`
	SaturationLatencyFactor   float64         `yaml:"saturation_latency_factor,omitempty"`
	MaxConnections            int             `yaml:"max_connections,omitempty"`
	Cache                     *CacheBehavior  `yaml:"cache,omitempty"`
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
	Downstream      []DownstreamCall `yaml:"downstream"`
	NetLatencyMs    LatencySpec      `yaml:"net_latency_ms"`
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
}

// LatencySpec represents latency with mean and standard deviation
type LatencySpec struct {
	Mean  float64 `yaml:"mean"`
	Sigma float64 `yaml:"sigma"`
}

// WorkloadPattern represents a workload entry point
type WorkloadPattern struct {
	From         string      `yaml:"from"`
	SourceKind   string      `yaml:"source_kind,omitempty"`   // e.g. client
	TrafficClass string      `yaml:"traffic_class,omitempty"`   // ingress, background, replay
	To           string      `yaml:"to"`
	Arrival      ArrivalSpec `yaml:"arrival"`
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
