package resource

import (
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestNewManager(t *testing.T) {
	m := NewManager()
	if m == nil {
		t.Fatalf("expected non-nil manager")
	}
}

func TestManagerInitializeFromScenario(t *testing.T) {
	m := NewManager()
	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "host-1", Cores: 4},
			{ID: "host-2", Cores: 8},
		},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 2,
				Model:    "cpu",
				Endpoints: []config.Endpoint{
					{Path: "/test", MeanCPUMs: 10, CPUSigmaMs: 2},
				},
			},
		},
	}

	err := m.InitializeFromScenario(scenario)
	if err != nil {
		t.Fatalf("InitializeFromScenario error: %v", err)
	}

	// Check hosts were created
	host1, ok := m.GetHost("host-1")
	if !ok {
		t.Fatalf("expected host-1 to exist")
	}
	if host1.CPUCores() != 4 {
		t.Fatalf("expected host-1 to have 4 cores, got %d", host1.CPUCores())
	}

	// Check service instances were created
	instances := m.GetInstancesForService("svc1")
	if len(instances) != 2 {
		t.Fatalf("expected 2 instances for svc1, got %d", len(instances))
	}
}

func TestManagerAllocateCPU(t *testing.T) {
	m := NewManager()
	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "host-1", Cores: 4},
		},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 1,
				Model:    "cpu",
				Endpoints: []config.Endpoint{
					{Path: "/test", MeanCPUMs: 10, CPUSigmaMs: 2},
				},
			},
		},
	}

	err := m.InitializeFromScenario(scenario)
	if err != nil {
		t.Fatalf("InitializeFromScenario error: %v", err)
	}

	instances := m.GetInstancesForService("svc1")
	if len(instances) == 0 {
		t.Fatalf("expected at least one instance")
	}
	instanceID := instances[0].ID()

	// Allocate CPU
	simTime := time.Now()
	err = m.AllocateCPU(instanceID, 100.0, simTime) // 100ms CPU time
	if err != nil {
		t.Fatalf("AllocateCPU error: %v", err)
	}

	// Check utilization
	cpuUtil, _, ok := m.GetInstanceUtilization(instanceID)
	if !ok {
		t.Fatalf("expected to get instance utilization")
	}
	if cpuUtil <= 0 {
		t.Fatalf("expected positive CPU utilization, got %f", cpuUtil)
	}

	// Release CPU
	m.ReleaseCPU(instanceID, 100.0, simTime)
}

func TestManagerQueueOperations(t *testing.T) {
	m := NewManager()
	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "host-1", Cores: 4},
		},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 1,
				Model:    "cpu",
				Endpoints: []config.Endpoint{
					{Path: "/test", MeanCPUMs: 10, CPUSigmaMs: 2},
				},
			},
		},
	}

	err := m.InitializeFromScenario(scenario)
	if err != nil {
		t.Fatalf("InitializeFromScenario error: %v", err)
	}

	instances := m.GetInstancesForService("svc1")
	instanceID := instances[0].ID()

	// Enqueue requests
	err = m.EnqueueRequest(instanceID, "req-1")
	if err != nil {
		t.Fatalf("EnqueueRequest error: %v", err)
	}

	err = m.EnqueueRequest(instanceID, "req-2")
	if err != nil {
		t.Fatalf("EnqueueRequest error: %v", err)
	}

	// Check queue length
	queueLen := m.GetQueueLength(instanceID)
	if queueLen != 2 {
		t.Fatalf("expected queue length 2, got %d", queueLen)
	}

	// Dequeue
	reqID, ok := m.DequeueRequest(instanceID)
	if !ok {
		t.Fatalf("expected to dequeue request")
	}
	if reqID != "req-1" {
		t.Fatalf("expected req-1, got %s", reqID)
	}

	queueLen = m.GetQueueLength(instanceID)
	if queueLen != 1 {
		t.Fatalf("expected queue length 1, got %d", queueLen)
	}
}

func TestManagerSelectInstanceForService(t *testing.T) {
	m := NewManager()
	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "host-1", Cores: 4},
		},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 3,
				Model:    "cpu",
				Endpoints: []config.Endpoint{
					{Path: "/test", MeanCPUMs: 10, CPUSigmaMs: 2},
				},
			},
		},
	}

	err := m.InitializeFromScenario(scenario)
	if err != nil {
		t.Fatalf("InitializeFromScenario error: %v", err)
	}

	instance, err := m.SelectInstanceForService("svc1")
	if err != nil {
		t.Fatalf("SelectInstanceForService error: %v", err)
	}
	if instance == nil {
		t.Fatalf("expected non-nil instance")
	}
	if instance.ServiceName() != "svc1" {
		t.Fatalf("expected service name svc1, got %s", instance.ServiceName())
	}
}
