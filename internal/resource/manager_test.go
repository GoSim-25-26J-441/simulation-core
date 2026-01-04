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

	// Test non-existent service
	_, err = m.SelectInstanceForService("nonexistent")
	if err == nil {
		t.Fatalf("expected error for non-existent service")
	}
}

func TestManagerAllocateMemory(t *testing.T) {
	m := NewManager()
	// Note: Host config doesn't have memory field, so we need to test with a host that has memory
	// For now, we'll test the error case when memory is at capacity
	// The actual memory allocation will be tested when we have hosts with memory configured
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

	// Since host has 0GB memory (default), allocation should fail
	// This tests the capacity check
	err = m.AllocateMemory(instanceID, 100.0) // 100MB
	if err == nil {
		t.Fatalf("expected error when host memory is 0GB")
	}

	// Test direct instance memory allocation (bypassing host check for this test)
	instance, ok := m.GetServiceInstance(instanceID)
	if !ok {
		t.Fatalf("expected instance to exist")
	}
	instance.AllocateMemory(100.0)
	memUtil := instance.MemoryUtilization()
	if memUtil <= 0 {
		t.Fatalf("expected positive memory utilization, got %f", memUtil)
	}

	// Release memory
	instance.ReleaseMemory(100.0)
}

func TestManagerAllocateMemoryErrorCases(t *testing.T) {
	m := NewManager()

	// Test non-existent instance
	err := m.AllocateMemory("nonexistent", 100.0)
	if err == nil {
		t.Fatalf("expected error for non-existent instance")
	}

	// Test memory capacity exceeded (host has 0GB memory by default)
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

	err = m.InitializeFromScenario(scenario)
	if err != nil {
		t.Fatalf("InitializeFromScenario error: %v", err)
	}

	instances := m.GetInstancesForService("svc1")
	instanceID := instances[0].ID()

	// Try to allocate memory - should fail because host has 0GB memory
	err = m.AllocateMemory(instanceID, 100.0) // 100MB
	if err == nil {
		t.Fatalf("expected error when host memory is 0GB")
	}
}

func TestManagerAllocateCPUErrorCases(t *testing.T) {
	m := NewManager()

	// Test non-existent instance
	simTime := time.Now()
	err := m.AllocateCPU("nonexistent", 100.0, simTime)
	if err == nil {
		t.Fatalf("expected error for non-existent instance")
	}
}

func TestManagerReleaseCPUErrorCases(t *testing.T) {
	m := NewManager()
	simTime := time.Now()

	// Release from non-existent instance should not error
	m.ReleaseCPU("nonexistent", 100.0, simTime)
}

func TestManagerReleaseMemoryErrorCases(t *testing.T) {
	m := NewManager()

	// Release from non-existent instance should not error
	m.ReleaseMemory("nonexistent", 100.0)
}

func TestManagerGetHostUtilization(t *testing.T) {
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

	cpuUtil, memUtil, ok := m.GetHostUtilization("host-1")
	if !ok {
		t.Fatalf("expected to get host utilization")
	}
	if cpuUtil < 0 || cpuUtil > 1.0 {
		t.Fatalf("expected CPU utilization between 0 and 1, got %f", cpuUtil)
	}
	if memUtil < 0 || memUtil > 1.0 {
		t.Fatalf("expected memory utilization between 0 and 1, got %f", memUtil)
	}

	// Test non-existent host
	_, _, ok = m.GetHostUtilization("nonexistent")
	if ok {
		t.Fatalf("expected false for non-existent host")
	}
}

func TestManagerGetAllHosts(t *testing.T) {
	m := NewManager()
	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "host-1", Cores: 4},
			{ID: "host-2", Cores: 8},
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

	hosts := m.GetAllHosts()
	if len(hosts) != 2 {
		t.Fatalf("expected 2 hosts, got %d", len(hosts))
	}
}

func TestManagerGetAllInstances(t *testing.T) {
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

	instances := m.GetAllInstances()
	if len(instances) != 3 {
		t.Fatalf("expected 3 instances, got %d", len(instances))
	}
}

func TestManagerGetInstanceUtilization(t *testing.T) {
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

	cpuUtil, memUtil, ok := m.GetInstanceUtilization(instanceID)
	if !ok {
		t.Fatalf("expected to get instance utilization")
	}
	if cpuUtil < 0 || cpuUtil > 1.0 {
		t.Fatalf("expected CPU utilization between 0 and 1, got %f", cpuUtil)
	}
	if memUtil < 0 || memUtil > 1.0 {
		t.Fatalf("expected memory utilization between 0 and 1, got %f", memUtil)
	}

	// Test non-existent instance
	_, _, ok = m.GetInstanceUtilization("nonexistent")
	if ok {
		t.Fatalf("expected false for non-existent instance")
	}
}

func TestServiceInstanceMethods(t *testing.T) {
	instance := NewServiceInstance("inst-1", "svc1", "host-1", 2.0, 1024.0)

	// Test getters
	if instance.ID() != "inst-1" {
		t.Fatalf("expected ID inst-1, got %s", instance.ID())
	}
	if instance.ServiceName() != "svc1" {
		t.Fatalf("expected service name svc1, got %s", instance.ServiceName())
	}
	if instance.HostID() != "host-1" {
		t.Fatalf("expected host ID host-1, got %s", instance.HostID())
	}
	if instance.CPUCores() != 2.0 {
		t.Fatalf("expected 2.0 CPU cores, got %f", instance.CPUCores())
	}
	if instance.MemoryMB() != 1024.0 {
		t.Fatalf("expected 1024.0 MB memory, got %f", instance.MemoryMB())
	}

	// Test CPU utilization with zero cores
	instanceZeroCPU := NewServiceInstance("inst-2", "svc2", "host-1", 0.0, 1024.0)
	util := instanceZeroCPU.CPUUtilization()
	if util != 0.0 {
		t.Fatalf("expected 0.0 CPU utilization for zero cores, got %f", util)
	}

	// Test memory utilization with zero memory
	instanceZeroMem := NewServiceInstance("inst-3", "svc3", "host-1", 2.0, 0.0)
	util = instanceZeroMem.MemoryUtilization()
	if util != 0.0 {
		t.Fatalf("expected 0.0 memory utilization for zero memory, got %f", util)
	}

	// Test CPU allocation and utilization
	simTime := time.Now()
	instance.AllocateCPU(2000.0, simTime) // 2000ms = 2 seconds of CPU time
	util = instance.CPUUtilization()
	// Utilization = (2000ms / 1000) / 2.0 cores = 1.0 (100%)
	if util != 1.0 {
		t.Fatalf("expected 1.0 CPU utilization, got %f", util)
	}

	// Test CPU utilization > 1.0 is clamped
	instance.AllocateCPU(1000.0, simTime) // Additional 1 second
	util = instance.CPUUtilization()
	if util > 1.0 {
		t.Fatalf("expected CPU utilization clamped to 1.0, got %f", util)
	}

	// Test memory allocation
	instance.AllocateMemory(512.0) // 512MB
	util = instance.MemoryUtilization()
	if util != 0.5 {
		t.Fatalf("expected 0.5 memory utilization, got %f", util)
	}

	// Test active requests
	if instance.ActiveRequests() == 0 {
		t.Fatalf("expected active requests > 0")
	}

	// Test capacity check
	hasCapacity := instance.HasCapacity()
	// Should be false since CPU is at 100%
	if hasCapacity {
		t.Fatalf("expected no capacity when CPU at 100%%")
	}

	// Test release CPU
	instance.ReleaseCPU(2000.0, simTime)
	if instance.ActiveRequests() == 0 {
		t.Fatalf("expected active requests > 0 after partial release")
	}

	// Test release more CPU than allocated
	instance.ReleaseCPU(10000.0, simTime)
	if instance.ActiveRequests() < 0 {
		t.Fatalf("active requests should not be negative")
	}

	// Test release memory
	instance.ReleaseMemory(512.0)
	util = instance.MemoryUtilization()
	if util != 0.0 {
		t.Fatalf("expected 0.0 memory utilization after release, got %f", util)
	}

	// Test release more memory than allocated
	instance.ReleaseMemory(1000.0)
	util = instance.MemoryUtilization()
	if util < 0 {
		t.Fatalf("memory utilization should not be negative")
	}
}

func TestServiceInstanceQueueOperations(t *testing.T) {
	instance := NewServiceInstance("inst-1", "svc1", "host-1", 1.0, 512.0)

	// Test empty queue
	queueLen := instance.QueueLength()
	if queueLen != 0 {
		t.Fatalf("expected empty queue, got length %d", queueLen)
	}

	_, ok := instance.DequeueRequest()
	if ok {
		t.Fatalf("expected false when dequeuing from empty queue")
	}

	// Test enqueue
	instance.EnqueueRequest("req-1")
	instance.EnqueueRequest("req-2")
	instance.EnqueueRequest("req-3")

	queueLen = instance.QueueLength()
	if queueLen != 3 {
		t.Fatalf("expected queue length 3, got %d", queueLen)
	}

	// Test dequeue
	reqID, ok := instance.DequeueRequest()
	if !ok {
		t.Fatalf("expected to dequeue request")
	}
	if reqID != "req-1" {
		t.Fatalf("expected req-1, got %s", reqID)
	}

	queueLen = instance.QueueLength()
	if queueLen != 2 {
		t.Fatalf("expected queue length 2, got %d", queueLen)
	}
}

func TestHostMethods(t *testing.T) {
	host := NewHost("host-1", 4, 8)

	// Test getters
	if host.ID() != "host-1" {
		t.Fatalf("expected ID host-1, got %s", host.ID())
	}
	if host.CPUCores() != 4 {
		t.Fatalf("expected 4 CPU cores, got %d", host.CPUCores())
	}
	if host.MemoryGB() != 8 {
		t.Fatalf("expected 8 GB memory, got %d", host.MemoryGB())
	}

	// Test initial utilization
	if host.CPUUtilization() != 0.0 {
		t.Fatalf("expected 0.0 CPU utilization, got %f", host.CPUUtilization())
	}
	if host.MemoryUtilization() != 0.0 {
		t.Fatalf("expected 0.0 memory utilization, got %f", host.MemoryUtilization())
	}

	// Test AddService
	host.AddService("inst-1")
	host.AddService("inst-2")
	instances := host.ServiceInstances()
	if len(instances) != 2 {
		t.Fatalf("expected 2 service instances, got %d", len(instances))
	}

	// Test RemoveService
	host.RemoveService("inst-1")
	instances = host.ServiceInstances()
	if len(instances) != 1 {
		t.Fatalf("expected 1 service instance, got %d", len(instances))
	}
	if instances[0] != "inst-2" {
		t.Fatalf("expected inst-2, got %s", instances[0])
	}

	// Test RemoveService with non-existent instance
	host.RemoveService("nonexistent")
	instances = host.ServiceInstances()
	if len(instances) != 1 {
		t.Fatalf("expected 1 service instance after removing non-existent, got %d", len(instances))
	}

	// Test SetCPUUtilization
	host.SetCPUUtilization(0.5)
	if host.CPUUtilization() != 0.5 {
		t.Fatalf("expected 0.5 CPU utilization, got %f", host.CPUUtilization())
	}

	// Test clamping to 1.0
	host.SetCPUUtilization(1.5)
	if host.CPUUtilization() != 1.0 {
		t.Fatalf("expected CPU utilization clamped to 1.0, got %f", host.CPUUtilization())
	}

	// Test clamping to 0.0
	host.SetCPUUtilization(-0.5)
	if host.CPUUtilization() != 0.0 {
		t.Fatalf("expected CPU utilization clamped to 0.0, got %f", host.CPUUtilization())
	}

	// Test SetMemoryUtilization
	host.SetMemoryUtilization(0.75)
	if host.MemoryUtilization() != 0.75 {
		t.Fatalf("expected 0.75 memory utilization, got %f", host.MemoryUtilization())
	}

	// Test HasCapacity
	host.SetCPUUtilization(0.5)
	host.SetMemoryUtilization(0.5)
	if !host.HasCapacity() {
		t.Fatalf("expected host to have capacity")
	}

	host.SetCPUUtilization(1.0)
	if host.HasCapacity() {
		t.Fatalf("expected host to not have capacity when CPU at 100%%")
	}

	host.SetCPUUtilization(0.5)
	host.SetMemoryUtilization(1.0)
	if host.HasCapacity() {
		t.Fatalf("expected host to not have capacity when memory at 100%%")
	}
}

func TestManagerInitializeFromScenarioNoHosts(t *testing.T) {
	m := NewManager()
	scenario := &config.Scenario{
		Hosts: []config.Host{},
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
	if err == nil {
		t.Fatalf("expected error when no hosts available")
	}
}

func TestManagerEnqueueRequestError(t *testing.T) {
	m := NewManager()
	err := m.EnqueueRequest("nonexistent", "req-1")
	if err == nil {
		t.Fatalf("expected error for non-existent instance")
	}
}
