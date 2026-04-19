package config

import "testing"

func TestValidateScenarioBehaviorAndDownstreamFields(t *testing.T) {
	valid := &Scenario{
		Hosts: []Host{{ID: "h1", Cores: 4}},
		Services: []Service{
			{
				ID:       "svc1",
				Replicas: 1,
				Model:    "cpu",
				Behavior: &ServiceBehavior{
					FailureRate:             0.1,
					SaturationLatencyFactor: 0.5,
					MaxConnections:          4,
					Cache: &CacheBehavior{
						HitRate:       0.5,
						HitLatencyMs:  LatencySpec{Mean: 1, Sigma: 0},
						MissLatencyMs: LatencySpec{Mean: 2, Sigma: 0},
					},
				},
				Endpoints: []Endpoint{
					{
						Path:           "/a",
						MeanCPUMs:      1,
						CPUSigmaMs:     0,
						FailureRate:    0.05,
						TimeoutMs:      100,
						IOMs:           LatencySpec{Mean: 3, Sigma: 1},
						ConnectionPool: 2,
						NetLatencyMs:   LatencySpec{Mean: 1, Sigma: 0},
						Downstream: []DownstreamCall{
							{
								To:          "svc2:/b",
								FailureRate: 0.2,
							},
						},
					},
				},
			},
			{
				ID:       "svc2",
				Replicas: 1,
				Model:    "cpu",
				Endpoints: []Endpoint{
					{Path: "/b", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: LatencySpec{Mean: 1, Sigma: 0}},
				},
			},
		},
		Workload: []WorkloadPattern{{From: "c", To: "svc1:/a", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 1}}},
	}
	if err := ValidateScenario(valid); err != nil {
		t.Fatalf("valid scenario: %v", err)
	}

	invalid := *valid
	invalid.Services = append([]Service(nil), valid.Services...)
	invalid.Services[0].Behavior = &ServiceBehavior{FailureRate: 2}
	if err := ValidateScenario(&invalid); err == nil {
		t.Fatal("expected error for failure_rate > 1")
	}
}
