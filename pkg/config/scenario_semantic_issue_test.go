package config

import (
	"errors"
	"strings"
	"testing"
)

func TestSemanticIssueFromValidateError_WorkloadMissingEndpoint(t *testing.T) {
	yaml := `
hosts:
  - id: host-1
    cores: 2
services:
  - id: checkout
    replicas: 1
    model: cpu
    endpoints:
      - path: /read
        mean_cpu_ms: 10
        cpu_sigma_ms: 2
        downstream: []
        net_latency_ms: {mean: 1, sigma: 0.5}
workload:
  - from: client
    to: checkout:/write
    arrival: {type: poisson, rate_rps: 10}
`
	s, err := UnmarshalScenarioYAMLString(strings.TrimSpace(yaml))
	if err != nil {
		t.Fatal(err)
	}
	vErr := ValidateScenario(s)
	if vErr == nil {
		t.Fatal("expected validation error")
	}
	code, path, msg := SemanticIssueFromValidateError(vErr)
	if code != "UNKNOWN_WORKLOAD_ENDPOINT" {
		t.Fatalf("code=%q want UNKNOWN_WORKLOAD_ENDPOINT", code)
	}
	if path != "workload[0].to" {
		t.Fatalf("path=%q", path)
	}
	if !strings.Contains(msg, "checkout") || !strings.Contains(msg, "/write") {
		t.Fatalf("message=%q", msg)
	}
}

func TestSemanticIssueFromValidateError_DownstreamMissingService(t *testing.T) {
	yaml := `
hosts:
  - id: host-1
    cores: 4
services:
  - id: a
    replicas: 1
    model: cpu
    endpoints:
      - path: /x
        mean_cpu_ms: 1
        cpu_sigma_ms: 0
        downstream:
          - to: "nosuch:/y"
            mode: sync
        net_latency_ms: {mean: 1, sigma: 0}
  - id: b
    replicas: 1
    model: cpu
    endpoints:
      - path: /y
        mean_cpu_ms: 1
        cpu_sigma_ms: 0
        downstream: []
        net_latency_ms: {mean: 1, sigma: 0}
workload:
  - from: c
    to: "a:/x"
    arrival: {type: poisson, rate_rps: 1}
`
	s, err := UnmarshalScenarioYAMLString(strings.TrimSpace(yaml))
	if err != nil {
		t.Fatal(err)
	}
	vErr := ValidateScenario(s)
	if vErr == nil {
		t.Fatal("expected error")
	}
	code, _, _ := SemanticIssueFromValidateError(vErr)
	if code != "UNKNOWN_DOWNSTREAM_SERVICE" {
		t.Fatalf("code=%q err=%v", code, vErr)
	}
}

func TestSemanticIssueFromValidateError_DownstreamMissingEndpoint(t *testing.T) {
	yaml := `
hosts:
  - id: host-1
    cores: 4
services:
  - id: a
    replicas: 1
    model: cpu
    endpoints:
      - path: /x
        mean_cpu_ms: 1
        cpu_sigma_ms: 0
        downstream:
          - to: "b:/missing"
            mode: sync
        net_latency_ms: {mean: 1, sigma: 0}
  - id: b
    replicas: 1
    model: cpu
    endpoints:
      - path: /ok
        mean_cpu_ms: 1
        cpu_sigma_ms: 0
        downstream: []
        net_latency_ms: {mean: 1, sigma: 0}
workload:
  - from: c
    to: "a:/x"
    arrival: {type: poisson, rate_rps: 1}
`
	s, err := UnmarshalScenarioYAMLString(strings.TrimSpace(yaml))
	if err != nil {
		t.Fatal(err)
	}
	vErr := ValidateScenario(s)
	if vErr == nil {
		t.Fatal("expected error")
	}
	code, _, _ := SemanticIssueFromValidateError(vErr)
	if code != "UNKNOWN_DOWNSTREAM_ENDPOINT" {
		t.Fatalf("code=%q err=%v", code, vErr)
	}
}

func TestSemanticIssueFromValidateError_TableCoverage(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		code    string
		path    string
		msgPart string
	}{
		{
			name:    "nil error",
			err:     nil,
			code:    "",
			path:    "",
			msgPart: "",
		},
		{
			name:    "workload missing service",
			err:     errors.New("workload 2: target service billing does not exist"),
			code:    "UNKNOWN_WORKLOAD_SERVICE",
			path:    "workload[2].to",
			msgPart: "unknown service billing",
		},
		{
			name:    "invalid quoted workload target",
			err:     errors.New(`workload 3: invalid to "oops": bad format`),
			code:    "INVALID_SCENARIO_SCHEMA",
			path:    "workload[3].to",
			msgPart: `invalid to "oops"`,
		},
		{
			name:    "queue consumer missing endpoint",
			err:     errors.New("service queue-svc: queue consumer_target endpoint worker:/missing does not exist"),
			code:    "UNKNOWN_QUEUE_CONSUMER_TARGET",
			path:    `services["queue-svc"].behavior.queue.consumer_target`,
			msgPart: "missing endpoint",
		},
		{
			name:    "queue dlq missing service",
			err:     errors.New("service queue-svc: queue dlq service dead-letter does not exist"),
			code:    "UNKNOWN_QUEUE_CONSUMER_TARGET",
			path:    `services["queue-svc"].behavior.queue.dlq_target`,
			msgPart: "unknown service dead-letter",
		},
		{
			name:    "topic consumer required",
			err:     errors.New("service topic-svc: behavior.topic.subscribers[1].consumer_target is required"),
			code:    "INVALID_SCENARIO_SCHEMA",
			path:    `services["topic-svc"].behavior.topic.subscribers[1].consumer_target`,
			msgPart: "consumer_target is required",
		},
		{
			name:    "queue consumer target schema error",
			err:     errors.New("service queue-svc: behavior.queue.consumer_target: invalid target"),
			code:    "INVALID_SCENARIO_SCHEMA",
			path:    `services["queue-svc"].behavior.queue.consumer_target`,
			msgPart: "invalid target",
		},
		{
			name:    "duplicate host id fallback",
			err:     errors.New("duplicate host id: host-1"),
			code:    "INVALID_SCENARIO_SCHEMA",
			path:    "hosts",
			msgPart: "duplicate host id",
		},
		{
			name:    "generic fallback",
			err:     errors.New("some unknown validation issue"),
			code:    "INVALID_SCENARIO_SCHEMA",
			path:    "",
			msgPart: "unknown validation issue",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, path, msg := SemanticIssueFromValidateError(tc.err)
			if code != tc.code || path != tc.path {
				t.Fatalf("got (code=%q path=%q) want (code=%q path=%q)", code, path, tc.code, tc.path)
			}
			if tc.msgPart != "" && !strings.Contains(msg, tc.msgPart) {
				t.Fatalf("message %q does not include %q", msg, tc.msgPart)
			}
		})
	}
}
