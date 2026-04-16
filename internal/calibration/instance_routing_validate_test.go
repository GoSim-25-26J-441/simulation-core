package calibration

import (
	"testing"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestValidateScenario_InstanceRoutingValidated(t *testing.T) {
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 4}},
		Services: []config.Service{{
			ID: "api", Replicas: 2, Model: "cpu",
			Endpoints: []config.Endpoint{{
				Path: "/x", MeanCPUMs: 1, CPUSigmaMs: 0,
				NetLatencyMs: config.LatencySpec{Mean: 0.5, Sigma: 0},
			}},
		}},
		Workload: []config.WorkloadPattern{{
			From: "client", To: "api:/x",
			Arrival: config.ArrivalSpec{Type: "constant", RateRPS: 4},
		}},
	}
	obs := &ObservedMetrics{
		Global: GlobalObservation{
			IngressThroughputRPS: F64(4),
		},
		InstanceRouting: []InstanceRoutingObservation{
			{
				ServiceID:    "api",
				EndpointPath: "/x",
				InstanceID:   "api-instance-0",
				RequestShare: F64(0.5),
			},
		},
	}
	rep, err := ValidateScenario(sc, obs, 1000, &ValidateOptions{Seeds: []int64{1}})
	if err != nil {
		t.Fatal(err)
	}
	foundCheck := false
	for _, ch := range rep.Checks {
		if ch.Name == "instance_routing_share:api:/x:api-instance-0" {
			foundCheck = true
			break
		}
	}
	if !foundCheck {
		t.Fatalf("expected routing skew check, got checks=%+v", rep.Checks)
	}
}

func TestValidateScenario_InstanceRoutingWarningWhenMissingPrediction(t *testing.T) {
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 4}},
		Services: []config.Service{{
			ID: "api", Replicas: 2, Model: "cpu",
			Endpoints: []config.Endpoint{{
				Path: "/x", MeanCPUMs: 1, CPUSigmaMs: 0,
				NetLatencyMs: config.LatencySpec{Mean: 0.5, Sigma: 0},
			}},
		}},
		Workload: []config.WorkloadPattern{},
	}
	obs := &ObservedMetrics{
		InstanceRouting: []InstanceRoutingObservation{
			{ServiceID: "api", EndpointPath: "/x", InstanceID: "api-instance-0", RequestShare: F64(0.5)},
		},
	}
	rep, err := ValidateScenario(sc, obs, 1000, &ValidateOptions{Seeds: []int64{1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Warnings) == 0 {
		t.Fatalf("expected warning when routing observations cannot be validated, got %+v", rep)
	}
}
