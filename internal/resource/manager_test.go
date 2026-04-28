package resource

import (
	"errors"
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
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

func TestManagerInitializeFromScenarioHostMemory(t *testing.T) {
	// Scenario with explicit memory_gb: host gets that value
	m := NewManager()
	scenarioWithMem := &config.Scenario{
		Hosts: []config.Host{
			{ID: "host-1", Cores: 4, MemoryGB: 8},
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
	if err := m.InitializeFromScenario(scenarioWithMem); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	host, ok := m.GetHost("host-1")
	if !ok {
		t.Fatalf("expected host-1 to exist")
	}
	if host.MemoryGB() != 8 {
		t.Fatalf("expected host-1 MemoryGB 8, got %d", host.MemoryGB())
	}

	// Scenario with MemoryGB 0 (omit): host gets default 16 GB
	m2 := NewManager()
	scenarioDefault := &config.Scenario{
		Hosts: []config.Host{
			{ID: "host-1", Cores: 2},
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
	if err := m2.InitializeFromScenario(scenarioDefault); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	host2, ok := m2.GetHost("host-1")
	if !ok {
		t.Fatalf("expected host-1 to exist")
	}
	if host2.MemoryGB() != 16 {
		t.Fatalf("expected host-1 default MemoryGB 16, got %d", host2.MemoryGB())
	}
}

func TestManagerScaleService(t *testing.T) {
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
	if err := m.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}

	// Scale up: 2 -> 4
	if err := m.ScaleService("svc1", 4); err != nil {
		t.Fatalf("ScaleService(svc1, 4): %v", err)
	}
	if n := m.ActiveReplicas("svc1"); n != 4 {
		t.Fatalf("expected 4 active replicas after scale up, got %d", n)
	}
	instances := m.GetInstancesForService("svc1")
	if len(instances) != 4 {
		t.Fatalf("expected 4 instances for svc1, got %d", len(instances))
	}

	// Scale down: 4 -> 2 active; surplus instances drain then are removed.
	if err := m.ScaleService("svc1", 2); err != nil {
		t.Fatalf("ScaleService(svc1, 2): %v", err)
	}
	if n := m.ActiveReplicas("svc1"); n != 2 {
		t.Fatalf("expected 2 active replicas after scale down, got %d", n)
	}
	instances = m.GetInstancesForService("svc1")
	if len(instances) != 4 {
		t.Fatalf("expected 4 physical instances while draining, got %d", len(instances))
	}
	draining := 0
	for _, inst := range instances {
		if inst.Lifecycle() == InstanceDraining {
			draining++
		}
	}
	if draining != 2 {
		t.Fatalf("expected 2 draining instances, got %d", draining)
	}
	m.ProcessDrainingInstances(time.Unix(0, 0))
	instances = m.GetInstancesForService("svc1")
	if len(instances) != 2 {
		t.Fatalf("expected 2 instances after drain removal, got %d", len(instances))
	}

	// Invalid: replicas < 1
	if err := m.ScaleService("svc1", 0); err == nil {
		t.Fatal("expected error for replicas 0")
	}
	// Unknown service
	if err := m.ScaleService("nonexistent", 3); err == nil {
		t.Fatal("expected error for unknown service")
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

func TestManagerUtilityAndBrokerAccessors(t *testing.T) {
	m := NewManager()
	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "host-1", Cores: 4, MemoryGB: 8},
			{ID: "host-2", Cores: 4, MemoryGB: 8},
		},
		Services: []config.Service{
			{
				ID:       "queue-broker",
				Kind:     "queue",
				Replicas: 1,
				Model:    "cpu",
				Endpoints: []config.Endpoint{
					{Path: "/q", MeanCPUMs: 1, CPUSigmaMs: 0},
				},
				Behavior: &config.ServiceBehavior{
					Queue: &config.QueueBehavior{
						ConsumerTarget:      "worker:/w",
						ConsumerConcurrency: 1,
						Capacity:            8,
					},
				},
			},
			{
				ID:       "topic-broker",
				Kind:     "topic",
				Replicas: 1,
				Model:    "cpu",
				Endpoints: []config.Endpoint{
					{Path: "/t", MeanCPUMs: 1, CPUSigmaMs: 0},
				},
				Behavior: &config.ServiceBehavior{
					Topic: &config.TopicBehavior{
						Subscribers: []config.TopicSubscriber{
							{Name: "sub-a", ConsumerGroup: "g1", ConsumerTarget: "worker:/w"},
						},
					},
				},
			},
			{
				ID:       "worker",
				Replicas: 1,
				Model:    "cpu",
				Endpoints: []config.Endpoint{
					{Path: "/w", MeanCPUMs: 1, CPUSigmaMs: 0},
				},
			},
		},
	}
	if err := m.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}

	m.SetScaleDownDrainTimeout(3 * time.Second)
	now := time.Now()
	m.NoteSimTime(now)
	if got := m.LastSimTime(); got.IsZero() || got.Before(now) {
		t.Fatalf("LastSimTime not updated: %v", got)
	}

	ids := m.HostIDs()
	if len(ids) != 2 {
		t.Fatalf("expected 2 host IDs, got %d", len(ids))
	}

	svcIDs := m.ListServiceIDs()
	if len(svcIDs) == 0 {
		t.Fatal("expected non-empty service IDs")
	}

	qShard := m.GetBrokerQueue("queue-broker", "/q", &config.QueueBehavior{
		ConsumerTarget:      "worker:/w",
		ConsumerConcurrency: 1,
		Capacity:            8,
	})
	if qShard == nil {
		t.Fatal("expected non-nil queue shard")
	}

	sub := &config.TopicSubscriber{Name: "sub-a", ConsumerGroup: "g1", ConsumerTarget: "worker:/w"}
	tShard := m.GetBrokerTopicSubscriberShard("topic-broker", "/t", "g1", &config.TopicBehavior{}, sub)
	if tShard == nil {
		t.Fatal("expected non-nil topic shard")
	}
	tPartShard := m.GetBrokerTopicSubscriberPartitionShard("topic-broker", "/t", 1, "g1", &config.TopicBehavior{}, sub)
	if tPartShard == nil {
		t.Fatal("expected non-nil topic partition shard")
	}

	if m.BrokerQueues() == nil {
		t.Fatal("expected broker queues registry")
	}
	if qs := m.QueueBrokerHealthSnapshots(now); len(qs) == 0 {
		t.Fatal("expected queue snapshots to be non-empty")
	}
	if ts := m.TopicBrokerHealthSnapshots(now); len(ts) == 0 {
		t.Fatal("expected topic snapshots to be non-empty")
	}
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

func TestManagerSelectInstanceForServiceRoundRobin(t *testing.T) {
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

	// Get all instances for verification
	allInstances := m.GetInstancesForService("svc1")
	if len(allInstances) != 3 {
		t.Fatalf("expected 3 instances, got %d", len(allInstances))
	}

	// Call SelectInstanceForService multiple times and verify round-robin behavior
	selectedInstances := make([]string, 0)
	for i := 0; i < 9; i++ { // 3 full rounds
		instance, err := m.SelectInstanceForService("svc1")
		if err != nil {
			t.Fatalf("SelectInstanceForService error on iteration %d: %v", i, err)
		}
		selectedInstances = append(selectedInstances, instance.ID())
	}

	// Verify that instances are selected in a round-robin fashion
	// After 3 iterations, we should see the same pattern repeated
	if selectedInstances[0] != selectedInstances[3] || selectedInstances[0] != selectedInstances[6] {
		t.Fatalf("expected same instance at positions 0, 3, 6, got %s, %s, %s",
			selectedInstances[0], selectedInstances[3], selectedInstances[6])
	}
	if selectedInstances[1] != selectedInstances[4] || selectedInstances[1] != selectedInstances[7] {
		t.Fatalf("expected same instance at positions 1, 4, 7, got %s, %s, %s",
			selectedInstances[1], selectedInstances[4], selectedInstances[7])
	}
	if selectedInstances[2] != selectedInstances[5] || selectedInstances[2] != selectedInstances[8] {
		t.Fatalf("expected same instance at positions 2, 5, 8, got %s, %s, %s",
			selectedInstances[2], selectedInstances[5], selectedInstances[8])
	}

	// Verify that all three instances are used
	uniqueInstances := make(map[string]bool)
	for i := 0; i < 3; i++ {
		uniqueInstances[selectedInstances[i]] = true
	}
	if len(uniqueInstances) != 3 {
		t.Fatalf("expected 3 unique instances in first round, got %d", len(uniqueInstances))
	}

	// Verify that the instances are different in the first 3 selections
	if selectedInstances[0] == selectedInstances[1] || selectedInstances[1] == selectedInstances[2] || selectedInstances[0] == selectedInstances[2] {
		t.Fatalf("expected different instances in first 3 selections, got %s, %s, %s",
			selectedInstances[0], selectedInstances[1], selectedInstances[2])
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

	// Since host has 0GB memory (default), this means unlimited capacity
	// Memory allocation should succeed
	err = m.AllocateMemory(instanceID, 100.0) // 100MB
	if err != nil {
		t.Fatalf("expected no error when host has unlimited memory (0GB), got: %v", err)
	}

	// Test direct instance memory allocation
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

	// Host has 0GB memory (unlimited), so allocation should succeed
	err = m.AllocateMemory(instanceID, 100.0) // 100MB
	if err != nil {
		t.Fatalf("expected no error when host has unlimited memory (0GB), got: %v", err)
	}
}

func TestManagerAllocateMemoryHostCapacitySentinel(t *testing.T) {
	m := NewManager()
	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "host-1", Cores: 4, MemoryGB: 1},
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
	if err := m.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	instanceID := m.GetInstancesForService("svc1")[0].ID()
	if err := m.AllocateMemory(instanceID, 900.0); err != nil {
		t.Fatalf("first AllocateMemory: %v", err)
	}
	err := m.AllocateMemory(instanceID, 200.0)
	if err == nil {
		t.Fatalf("expected host capacity error")
	}
	if !errors.Is(err, ErrHostMemoryCapacity) {
		t.Fatalf("want ErrHostMemoryCapacity, got %v", err)
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

func TestHostResourceAggregationFromInstances(t *testing.T) {
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
				CPUCores: 2.0,
				MemoryMB: 512.0,
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

	// Initially host utilization should be 0 (or very low)
	cpuUtil0, memUtil0, ok := m.GetHostUtilization("host-1")
	if !ok {
		t.Fatalf("expected to get host utilization")
	}
	if cpuUtil0 < 0 || cpuUtil0 > 1.0 || memUtil0 < 0 || memUtil0 > 1.0 {
		t.Fatalf("expected utilizations in [0, 1], got cpu=%f mem=%f", cpuUtil0, memUtil0)
	}

	// Allocate CPU - host CPU utilization should increase
	simTime := time.Now()
	err = m.AllocateCPU(instanceID, 500.0, simTime) // 500ms in 1s window = 0.5 utilization for 2 cores = 1 core used
	if err != nil {
		t.Fatalf("AllocateCPU error: %v", err)
	}

	cpuUtil1, _, ok := m.GetHostUtilization("host-1")
	if !ok {
		t.Fatalf("expected to get host utilization after CPU alloc")
	}
	if cpuUtil1 <= cpuUtil0 {
		t.Fatalf("expected host CPU utilization to increase after AllocateCPU, was %f, now %f", cpuUtil0, cpuUtil1)
	}

	// Allocate memory - host memory utilization should increase
	err = m.AllocateMemory(instanceID, 256.0) // 256 MB
	if err != nil {
		t.Fatalf("AllocateMemory error: %v", err)
	}

	_, memUtil1, ok := m.GetHostUtilization("host-1")
	if !ok {
		t.Fatalf("expected to get host utilization after memory alloc")
	}
	if memUtil1 <= memUtil0 {
		t.Fatalf("expected host memory utilization to increase after AllocateMemory, was %f, now %f", memUtil0, memUtil1)
	}

	// Release and verify host utilization decreases
	m.ReleaseCPU(instanceID, 500.0, simTime)
	m.ReleaseMemory(instanceID, 256.0)
}

func TestHostUpdateCPUUtilizationDirect(t *testing.T) {
	host := NewHost("host-1", 4, 16)
	inst1 := NewServiceInstance("inst-1", "svc1", "host-1", 2.0, 256.0)
	inst2 := NewServiceInstance("inst-2", "svc1", "host-1", 2.0, 256.0)

	// No allocations - should be 0
	host.UpdateCPUUtilization([]InstanceUtilizationSource{inst1, inst2})
	if host.CPUUtilization() != 0 {
		t.Errorf("expected 0 CPU utilization with no allocations, got %f", host.CPUUtilization())
	}

	// Allocate CPU on both instances (500ms each in 1s window)
	now := time.Now()
	inst1.AllocateCPU(500.0, now) // 0.25 util each (500/1000/2), 2 instances * 2 cores * 0.25 = 1 core used
	inst2.AllocateCPU(500.0, now)
	host.UpdateCPUUtilization([]InstanceUtilizationSource{inst1, inst2})
	util := host.CPUUtilization()
	if util <= 0 || util > 1.0 {
		t.Errorf("expected positive CPU utilization, got %f", util)
	}
}

func TestHostUpdateMemoryUtilizationDirect(t *testing.T) {
	host := NewHost("host-1", 4, 16) // 16 GB
	inst := NewServiceInstance("inst-1", "svc1", "host-1", 2.0, 512.0)

	// No allocations - should be 0
	host.UpdateMemoryUtilization([]InstanceUtilizationSource{inst})
	if host.MemoryUtilization() != 0 {
		t.Errorf("expected 0 memory utilization with no allocations, got %f", host.MemoryUtilization())
	}

	// Allocate 8 GB (8192 MB) - should be 0.5 utilization
	inst.AllocateMemory(8192.0)
	host.UpdateMemoryUtilization([]InstanceUtilizationSource{inst})
	util := host.MemoryUtilization()
	if util < 0.49 || util > 0.51 {
		t.Errorf("expected ~0.5 memory utilization (8GB/16GB), got %f", util)
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
	instance.AllocateCPU(1000.0, simTime) // 1000ms = 1 second of CPU time
	util = instance.CPUUtilization()
	// Utilization = (1000ms / 1000ms window) / 2.0 cores = 0.5 (50%)
	expected := 0.5
	if util < expected-0.01 || util > expected+0.01 {
		t.Fatalf("expected ~%.2f CPU utilization, got %f", expected, util)
	}

	// Test CPU utilization clamping at 1.0
	instance.AllocateCPU(3000.0, simTime) // Additional 3 seconds = 4 total
	util = instance.CPUUtilization()
	// Utilization = (4000ms / 1000ms window) / 2.0 cores = 2.0, clamped to 1.0
	if util != 1.0 {
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

	// HasCapacity reflects FIFO CPU scheduler backlog (cpuNextFree), not sliding-window utilization.
	if !instance.HasCapacityAt(simTime) {
		t.Fatal("expected no backlog before reserve")
	}
	_, cpuTail := instance.ReserveCPUWork(simTime, 2000.0) // 1000ms wall with 2 cores
	if instance.HasCapacityAt(simTime) {
		t.Fatal("expected scheduler backlog when next free is after simTime")
	}
	if !instance.HasCapacityAt(cpuTail) {
		t.Fatal("expected capacity at tail of reserved interval")
	}

	// Test release CPU - note that with time-window tracking, releasing
	// doesn't reduce cpuUsageInWindow because the CPU was already consumed
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

	// Test setters for CPU and memory
	instance.SetCPUCores(4.0)
	if instance.CPUCores() != 4.0 {
		t.Fatalf("expected CPU cores updated to 4.0, got %f", instance.CPUCores())
	}
	instance.SetMemoryMB(2048.0)
	if instance.MemoryMB() != 2048.0 {
		t.Fatalf("expected memory updated to 2048.0, got %f", instance.MemoryMB())
	}
}

func TestManagerUpdateServiceResources(t *testing.T) {
	m := NewManager()
	scenario := &config.Scenario{
		Hosts:    []config.Host{{ID: "host-1", Cores: 8}},
		Services: []config.Service{{ID: "svc1", Replicas: 2, Model: "cpu"}},
	}
	if err := m.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario error: %v", err)
	}

	instances := m.GetInstancesForService("svc1")
	if len(instances) != 2 {
		t.Fatalf("expected 2 instances for svc1, got %d", len(instances))
	}
	if instances[0].CPUCores() != DefaultInstanceCPUCores || instances[0].MemoryMB() != DefaultInstanceMemoryMB {
		t.Fatalf("expected default resources before update, got cpu=%f mem=%f", instances[0].CPUCores(), instances[0].MemoryMB())
	}

	// Happy path: increase resources within host capacity.
	if err := m.UpdateServiceResources("svc1", 4.0, 2048.0); err != nil {
		t.Fatalf("UpdateServiceResources error: %v", err)
	}

	instances = m.GetInstancesForService("svc1")
	for _, inst := range instances {
		if inst.CPUCores() != 4.0 {
			t.Fatalf("expected CPU cores updated to 4.0, got %f", inst.CPUCores())
		}
		if inst.MemoryMB() != 2048.0 {
			t.Fatalf("expected memory updated to 2048.0, got %f", inst.MemoryMB())
		}
	}

	// Capacity guard: attempt to overcommit CPU should fail and leave resources unchanged.
	if err := m.UpdateServiceResources("svc1", 16.0, 2048.0); err == nil {
		t.Fatalf("expected error when exceeding host CPU capacity, got nil")
	}
	instances = m.GetInstancesForService("svc1")
	for _, inst := range instances {
		if inst.CPUCores() != 4.0 {
			t.Fatalf("expected CPU cores to remain at 4.0 after failed update, got %f", inst.CPUCores())
		}
		if inst.MemoryMB() != 2048.0 {
			t.Fatalf("expected memory to remain at 2048.0 after failed update, got %f", inst.MemoryMB())
		}
	}

}

func TestManagerUpdateServiceResourcesMemoryDownsizeRejected(t *testing.T) {
	m := NewManager()
	scenario := &config.Scenario{
		Hosts:    []config.Host{{ID: "host-1", Cores: 8}},
		Services: []config.Service{{ID: "svc1", Replicas: 1, Model: "cpu"}},
	}
	if err := m.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	inst := m.GetInstancesForService("svc1")[0]
	inst.AllocateMemory(100)
	if err := m.UpdateServiceResourcesWithHeadroom("svc1", 0, 50, 16); err == nil {
		t.Fatal("expected error when new memory limit is below active usage plus headroom")
	}
}

func TestManagerWorkReservationHelpersAndHostMemoryUtilization(t *testing.T) {
	m := NewManager()
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 4, MemoryGB: 2}},
		Services: []config.Service{
			{ID: "svc1", Replicas: 1, Model: "cpu"},
		},
	}
	if err := m.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	instances := m.GetInstancesForService("svc1")
	if len(instances) != 1 {
		t.Fatalf("expected one instance, got %d", len(instances))
	}
	inst := instances[0]
	now := time.Unix(1000, 0)

	if _, _, err := m.ReserveCPUWork("missing", now, 20); err == nil {
		t.Fatal("expected ReserveCPUWork to fail for unknown instance")
	}
	cpuStart, cpuEnd, err := m.ReserveCPUWork(inst.ID(), now, 20)
	if err != nil {
		t.Fatalf("ReserveCPUWork error: %v", err)
	}
	if !cpuEnd.After(cpuStart) {
		t.Fatalf("expected cpuEnd after cpuStart, got start=%v end=%v", cpuStart, cpuEnd)
	}

	if _, _, _, _, err := m.ReserveDBWork("missing", cpuEnd, 25, 2); err == nil {
		t.Fatal("expected ReserveDBWork to fail for unknown instance")
	}
	ioStart, ioEnd, slotIdx, waitMs, err := m.ReserveDBWork(inst.ID(), cpuEnd, 25, 2)
	if err != nil {
		t.Fatalf("ReserveDBWork error: %v", err)
	}
	if !ioEnd.After(ioStart) {
		t.Fatalf("expected ioEnd after ioStart, got start=%v end=%v", ioStart, ioEnd)
	}
	if slotIdx < 0 {
		t.Fatalf("expected non-negative slot index, got %d", slotIdx)
	}
	if waitMs < 0 {
		t.Fatalf("expected non-negative waitMs, got %f", waitMs)
	}

	m.ReleaseDBConnection("missing") // no-op path
	m.ReleaseDBConnection(inst.ID())
	m.RollbackCPUTailReservation("missing", cpuStart, cpuEnd) // no-op path
	m.RollbackCPUTailReservation(inst.ID(), cpuStart, cpuEnd)

	if err := m.AllocateMemory(inst.ID(), 1024); err != nil {
		t.Fatalf("AllocateMemory error: %v", err)
	}
	util := m.MaxHostMemoryUtilization()
	if util <= 0 {
		t.Fatalf("expected positive host memory utilization, got %f", util)
	}
}

func TestProcessDrainingInstancesTimeoutEvictsBusyInstance(t *testing.T) {
	m := NewManager()
	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "host-1", Cores: 4},
			{ID: "host-2", Cores: 4},
		},
		Services: []config.Service{
			{ID: "svc1", Replicas: 2, Model: "cpu"},
		},
	}
	if err := m.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	t0 := time.Unix(1000, 0)
	if err := m.ScaleServiceWithOptions("svc1", 1, ScaleServiceOptions{SimTime: t0, DrainTimeout: time.Second}); err != nil {
		t.Fatalf("ScaleServiceWithOptions: %v", err)
	}
	var draining *ServiceInstance
	for _, inst := range m.GetInstancesForService("svc1") {
		if inst.Lifecycle() == InstanceDraining {
			draining = inst
			break
		}
	}
	if draining == nil {
		t.Fatal("expected one draining instance")
	}
	draining.AllocateMemory(64)
	m.ProcessDrainingInstances(t0.Add(2 * time.Second))
	if n := len(m.GetInstancesForService("svc1")); n != 1 {
		t.Fatalf("expected 1 instance after forced timeout eviction, got %d", n)
	}
}

func TestDrainingInstanceNotSelectedForTraffic(t *testing.T) {
	m := NewManager()
	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "host-1", Cores: 4},
			{ID: "host-2", Cores: 4},
		},
		Services: []config.Service{
			{ID: "svc1", Replicas: 2, Model: "cpu"},
		},
	}
	if err := m.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	if err := m.ScaleServiceWithOptions("svc1", 1, ScaleServiceOptions{SimTime: time.Unix(1, 0), DrainTimeout: time.Hour}); err != nil {
		t.Fatalf("ScaleServiceWithOptions: %v", err)
	}
	seen := map[string]int{}
	for i := 0; i < 20; i++ {
		inst, err := m.SelectInstanceForService("svc1")
		if err != nil {
			t.Fatalf("SelectInstanceForService: %v", err)
		}
		seen[inst.ID()]++
		if inst.Lifecycle() != InstanceActive {
			t.Fatalf("selected instance should be active, got %v", inst.Lifecycle())
		}
	}
	if len(seen) != 1 {
		t.Fatalf("expected only one routable instance, got ids %+v", seen)
	}
}

func TestManagerHostScalingHelpers(t *testing.T) {
	m := NewManager()
	scenario := &config.Scenario{
		Hosts:    []config.Host{{ID: "host-1", Cores: 4}},
		Services: []config.Service{{ID: "svc1", Replicas: 1, Model: "cpu"}},
	}
	if err := m.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario error: %v", err)
	}

	if got := m.HostCount(); got != 1 {
		t.Fatalf("expected HostCount 1, got %d", got)
	}

	// With a single idle host, MaxHostCPUUtilization should be 0.
	if maxCPU := m.MaxHostCPUUtilization(); maxCPU != 0.0 {
		t.Fatalf("expected MaxHostCPUUtilization 0.0, got %f", maxCPU)
	}

	// Scale out hosts to 3 total.
	if err := m.ScaleOutHosts(3); err != nil {
		t.Fatalf("ScaleOutHosts error: %v", err)
	}
	if got := m.HostCount(); got != 3 {
		t.Fatalf("expected HostCount 3 after scale-out, got %d", got)
	}

	// Increase host capacity for all hosts. We primarily verify that this does
	// not panic and that the host count remains unchanged.
	m.IncreaseHostCapacity(2, 4)
	if got := m.HostCount(); got != 3 {
		t.Fatalf("expected HostCount to remain 3 after capacity increase, got %d", got)
	}
}

func TestManagerScaleInHosts(t *testing.T) {
	// Scale out to 3 hosts (all empty except host-1 which has 1 instance), then scale in to 2.
	m := NewManager()
	scenario := &config.Scenario{
		Hosts:    []config.Host{{ID: "host-1", Cores: 4}},
		Services: []config.Service{{ID: "svc1", Replicas: 1, Model: "cpu"}},
	}
	if err := m.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	if err := m.ScaleOutHosts(3); err != nil {
		t.Fatalf("ScaleOutHosts: %v", err)
	}
	if m.HostCount() != 3 {
		t.Fatalf("expected 3 hosts after scale-out, got %d", m.HostCount())
	}
	// Scale in to 2: should remove one empty host (host-auto-2 or host-auto-3).
	if err := m.ScaleInHosts(2); err != nil {
		t.Fatalf("ScaleInHosts(2): %v", err)
	}
	if m.HostCount() != 2 {
		t.Fatalf("expected 2 hosts after scale-in, got %d", m.HostCount())
	}
	// Scale in to 1: should remove the other empty auto host; host-1 must remain (has instance).
	if err := m.ScaleInHosts(1); err != nil {
		t.Fatalf("ScaleInHosts(1): %v", err)
	}
	if m.HostCount() != 1 {
		t.Fatalf("expected 1 host after scale-in to 1, got %d", m.HostCount())
	}
	// Scale in when no empty host: 1 host with 1 instance, target 0 -> effectively target 1, no-op.
	if err := m.ScaleInHosts(0); err != nil {
		t.Fatalf("ScaleInHosts(0) should clamp to 1: %v", err)
	}
	if m.HostCount() != 1 {
		t.Fatalf("expected still 1 host, got %d", m.HostCount())
	}
	// Two hosts both with instances: scale in to 1 should fail (no empty host).
	m2 := NewManager()
	scenario2 := &config.Scenario{
		Hosts: []config.Host{
			{ID: "host-1", Cores: 4},
			{ID: "host-2", Cores: 4},
		},
		Services: []config.Service{{ID: "svc1", Replicas: 2, Model: "cpu"}},
	}
	if err := m2.InitializeFromScenario(scenario2); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	if err := m2.ScaleInHosts(1); err == nil {
		t.Fatalf("expected error when scaling in with no empty host")
	}
	if m2.HostCount() != 2 {
		t.Fatalf("expected host count unchanged, got %d", m2.HostCount())
	}
}

func TestManagerDecreaseHostCapacity(t *testing.T) {
	m := NewManager()
	scenario := &config.Scenario{
		Hosts:    []config.Host{{ID: "host-1", Cores: 4}},
		Services: []config.Service{{ID: "svc1", Replicas: 1, Model: "cpu"}},
	}
	if err := m.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	m.IncreaseHostCapacity(2, 0)
	h, _ := m.GetHost("host-1")
	if h.CPUCores() != 6 {
		t.Fatalf("expected 6 cores after +2, got %d", h.CPUCores())
	}
	if err := m.DecreaseHostCapacity(-2, 0); err != nil {
		t.Fatalf("DecreaseHostCapacity(-2, 0): %v", err)
	}
	if h.CPUCores() != 4 {
		t.Fatalf("expected 4 cores after -2, got %d", h.CPUCores())
	}
	// Decrease below allocation: 1 instance with 1 core default; try to set host to 1 core then decrease.
	// Actually host has 4 cores, instance has 1. So decrease to 2 is ok. Decrease to 0 would clamp to 1.
	// Try decreasing by 3 so we go to 1 core - should succeed (allocated 1).
	if err := m.DecreaseHostCapacity(-3, 0); err != nil {
		t.Fatalf("DecreaseHostCapacity(-3, 0): %v", err)
	}
	if h.CPUCores() != 1 {
		t.Fatalf("expected 1 core after -3, got %d", h.CPUCores())
	}
	// Further decrease would go below 1; allocated is 1, so new capacity 0 -> clamp 1. So -1 is no-op on capacity.
	m.DecreaseHostCapacity(-1, 0)
	if h.CPUCores() != 1 {
		t.Fatalf("expected still 1 core, got %d", h.CPUCores())
	}
	// Increase again then try to decrease below allocation: scale instance to 2 cores, then try to decrease host to 1.
	m.IncreaseHostCapacity(2, 0)
	if err := m.UpdateServiceResources("svc1", 2.0, 0); err != nil {
		t.Fatalf("UpdateServiceResources: %v", err)
	}
	// Host now 3 cores, allocated 2. Decrease by 2 -> 1 core, but allocated 2 > 1 -> error.
	if err := m.DecreaseHostCapacity(-2, 0); err == nil {
		t.Fatalf("expected error when decreasing capacity below allocation")
	}
	h, _ = m.GetHost("host-1")
	if h.CPUCores() != 3 {
		t.Fatalf("expected host cores unchanged at 3, got %d", h.CPUCores())
	}
}

func TestServiceInstanceCPUTimeWindowDecay(t *testing.T) {
	instance := NewServiceInstance("inst-1", "svc1", "host-1", 2.0, 1024.0)

	// Test 1: CPU utilization within the same time window
	simTime := time.Now()
	instance.AllocateCPU(1000.0, simTime) // 1000ms CPU time
	util := instance.CPUUtilization()
	// Utilization = (1000ms / 1000ms window) / 2.0 cores = 0.5
	if util < 0.49 || util > 0.51 {
		t.Fatalf("expected ~0.5 CPU utilization within window, got %f", util)
	}

	// Test 2: CPU utilization decays when time moves past the window
	// Move simulation time forward by more than 1 second (the window duration)
	futureTime := simTime.Add(2 * time.Second)
	// Allocate CPU at the future time, which will start a new window
	instance.AllocateCPU(0.001, futureTime) // Allocate minimal CPU to trigger window update
	instance.ReleaseCPU(0.001, futureTime)  // Release it immediately
	decayedUtil := instance.CPUUtilization()
	// Utilization should be near 0 since the previous window has expired
	// and we only allocated a negligible amount in the new window
	if decayedUtil > 0.001 {
		t.Fatalf("expected near-0 CPU utilization after window expires, got %f", decayedUtil)
	}

	// Test 3: New CPU allocation in a new time window
	instance.AllocateCPU(500.0, futureTime) // 500ms CPU time in new window
	util = instance.CPUUtilization()
	// Utilization = (500ms / 1000ms window) / 2.0 cores = 0.25
	expected := 0.25
	if util < expected-0.01 || util > expected+0.01 {
		t.Fatalf("expected ~%.2f CPU utilization in new window, got %f", expected, util)
	}

	// Test 4: Multiple allocations within the same window accumulate
	instance2 := NewServiceInstance("inst-2", "svc2", "host-1", 4.0, 1024.0)
	t1 := time.Now()
	instance2.AllocateCPU(1000.0, t1)
	instance2.AllocateCPU(1000.0, t1.Add(100*time.Millisecond)) // Still in same window
	util = instance2.CPUUtilization()
	// Utilization = (2000ms / 1000ms window) / 4.0 cores = 0.5
	expected = 0.5
	if util < expected-0.01 || util > expected+0.01 {
		t.Fatalf("expected ~%.2f CPU utilization with multiple allocations, got %f", expected, util)
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

	// Test SetCPUCores clamps to >= 1.
	host.SetCPUCores(0)
	if host.CPUCores() != 1 {
		t.Fatalf("expected CPU cores clamped to 1, got %d", host.CPUCores())
	}
	host.SetCPUCores(16)
	if host.CPUCores() != 16 {
		t.Fatalf("expected CPU cores updated to 16, got %d", host.CPUCores())
	}

	// Test SetMemoryGB clamps to >= 1.
	host.SetMemoryGB(0)
	if host.MemoryGB() != 1 {
		t.Fatalf("expected memory GB clamped to 1, got %d", host.MemoryGB())
	}
	host.SetMemoryGB(32)
	if host.MemoryGB() != 32 {
		t.Fatalf("expected memory GB updated to 32, got %d", host.MemoryGB())
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

func TestSelectInstanceForRequest_DefaultRoundRobinCompatibility(t *testing.T) {
	m := NewManager()
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8}},
		Services: []config.Service{{
			ID: "svc", Replicas: 3, Model: "cpu",
			Endpoints: []config.Endpoint{{Path: "/a", MeanCPUMs: 1, CPUSigmaMs: 0}},
		}},
	}
	if err := m.InitializeFromScenario(sc); err != nil {
		t.Fatal(err)
	}
	var got []string
	for i := 0; i < 6; i++ {
		req := &models.Request{ServiceName: "svc", Endpoint: "/a", Metadata: map[string]interface{}{}}
		inst, strategy, err := m.SelectInstanceForRequest("svc", req, time.Unix(0, 0))
		if err != nil {
			t.Fatal(err)
		}
		if strategy != RoutingRoundRobin {
			t.Fatalf("expected round_robin, got %s", strategy)
		}
		got = append(got, inst.ID())
	}
	if got[0] != got[3] || got[1] != got[4] || got[2] != got[5] {
		t.Fatalf("round robin sequence changed: %v", got)
	}
}

func TestSelectInstanceForRequest_LeastQueue(t *testing.T) {
	m := NewManager()
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8}},
		Services: []config.Service{{
			ID: "svc", Replicas: 2, Model: "cpu",
			Routing:   &config.RoutingPolicy{Strategy: RoutingLeastQueue},
			Endpoints: []config.Endpoint{{Path: "/a", MeanCPUMs: 1, CPUSigmaMs: 0}},
		}},
	}
	if err := m.InitializeFromScenario(sc); err != nil {
		t.Fatal(err)
	}
	insts := m.GetInstancesForService("svc")
	busy, idle := insts[0], insts[1]
	_ = m.EnqueueRequest(busy.ID(), "q1")
	_ = m.EnqueueRequest(busy.ID(), "q2")
	req := &models.Request{ServiceName: "svc", Endpoint: "/a", Metadata: map[string]interface{}{}}
	chosen, strategy, err := m.SelectInstanceForRequest("svc", req, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if strategy != RoutingLeastQueue {
		t.Fatalf("strategy=%s", strategy)
	}
	if chosen.ID() != idle.ID() {
		t.Fatalf("expected least queued instance %s, got %s", idle.ID(), chosen.ID())
	}
}

func TestSelectInstanceForRequest_LeastConnections(t *testing.T) {
	m := NewManager()
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8}},
		Services: []config.Service{{
			ID: "svc", Replicas: 2, Model: "cpu",
			Routing:   &config.RoutingPolicy{Strategy: RoutingLeastConnections},
			Endpoints: []config.Endpoint{{Path: "/a", MeanCPUMs: 1, CPUSigmaMs: 0}},
		}},
	}
	if err := m.InitializeFromScenario(sc); err != nil {
		t.Fatal(err)
	}
	insts := m.GetInstancesForService("svc")
	busy, idle := insts[0], insts[1]
	busy.AllocateCPU(5, time.Now())
	req := &models.Request{ServiceName: "svc", Endpoint: "/a", Metadata: map[string]interface{}{}}
	chosen, _, err := m.SelectInstanceForRequest("svc", req, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if chosen.ID() != idle.ID() {
		t.Fatalf("expected least active %s, got %s", idle.ID(), chosen.ID())
	}
}

func TestSelectInstanceForRequest_Sticky(t *testing.T) {
	m := NewManager()
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8}},
		Services: []config.Service{{
			ID: "svc", Replicas: 3, Model: "cpu",
			Routing:   &config.RoutingPolicy{Strategy: RoutingSticky, StickyKeyFrom: "session_id"},
			Endpoints: []config.Endpoint{{Path: "/a", MeanCPUMs: 1, CPUSigmaMs: 0}},
		}},
	}
	if err := m.InitializeFromScenario(sc); err != nil {
		t.Fatal(err)
	}
	req1 := &models.Request{ServiceName: "svc", Endpoint: "/a", Metadata: map[string]interface{}{"session_id": "abc"}}
	a, _, err := m.SelectInstanceForRequest("svc", req1, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		req := &models.Request{ServiceName: "svc", Endpoint: "/a", Metadata: map[string]interface{}{"session_id": "abc"}}
		b, _, err := m.SelectInstanceForRequest("svc", req, time.Unix(0, 0))
		if err != nil {
			t.Fatal(err)
		}
		if b.ID() != a.ID() {
			t.Fatalf("sticky routing mismatch: first=%s now=%s", a.ID(), b.ID())
		}
	}
}

func TestSelectInstanceForRequest_DrainingAndScaleChanges(t *testing.T) {
	m := NewManager()
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8}, {ID: "h2", Cores: 8}},
		Services: []config.Service{{
			ID: "svc", Replicas: 2, Model: "cpu",
			Endpoints: []config.Endpoint{{Path: "/a", MeanCPUMs: 1, CPUSigmaMs: 0}},
		}},
	}
	if err := m.InitializeFromScenario(sc); err != nil {
		t.Fatal(err)
	}
	if err := m.ScaleServiceWithOptions("svc", 1, ScaleServiceOptions{SimTime: time.Unix(100, 0), DrainTimeout: time.Hour}); err != nil {
		t.Fatal(err)
	}
	req := &models.Request{ServiceName: "svc", Endpoint: "/a", Metadata: map[string]interface{}{}}
	chosen, _, err := m.SelectInstanceForRequest("svc", req, time.Unix(101, 0))
	if err != nil {
		t.Fatal(err)
	}
	if chosen.Lifecycle() != InstanceActive {
		t.Fatalf("selected draining instance: %s", chosen.ID())
	}
	if err := m.ScaleService("svc", 3); err != nil {
		t.Fatal(err)
	}
	seen := map[string]struct{}{}
	for i := 0; i < 12; i++ {
		r := &models.Request{ServiceName: "svc", Endpoint: "/a", Metadata: map[string]interface{}{}}
		inst, _, err := m.SelectInstanceForRequest("svc", r, time.Unix(102, 0))
		if err != nil {
			t.Fatal(err)
		}
		seen[inst.ID()] = struct{}{}
	}
	if len(seen) < 3 {
		t.Fatalf("expected new replica to become routable, seen=%v", seen)
	}
}

func TestSelectInstanceForRequest_RandomDeterministicWithSeed(t *testing.T) {
	buildSeq := func(seed int64) ([]string, error) {
		m := NewManager()
		m.SetRoutingSeed(seed)
		sc := &config.Scenario{
			Hosts: []config.Host{{ID: "h1", Cores: 8}},
			Services: []config.Service{{
				ID: "svc", Replicas: 3, Model: "cpu",
				Routing:   &config.RoutingPolicy{Strategy: RoutingRandom},
				Endpoints: []config.Endpoint{{Path: "/a", MeanCPUMs: 1, CPUSigmaMs: 0}},
			}},
		}
		if err := m.InitializeFromScenario(sc); err != nil {
			return nil, err
		}
		out := make([]string, 0, 10)
		for i := 0; i < 10; i++ {
			req := &models.Request{ServiceName: "svc", Endpoint: "/a", Metadata: map[string]interface{}{}}
			inst, _, err := m.SelectInstanceForRequest("svc", req, time.Unix(0, 0))
			if err != nil {
				return nil, err
			}
			out = append(out, inst.ID())
		}
		return out, nil
	}
	a, err := buildSeq(99)
	if err != nil {
		t.Fatal(err)
	}
	b, err := buildSeq(99)
	if err != nil {
		t.Fatal(err)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("random routing not deterministic for fixed seed at idx %d: %v vs %v", i, a, b)
		}
	}
}

func TestSelectInstanceForRequest_WeightedRoundRobinZeroWeight(t *testing.T) {
	m := NewManager()
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8}},
		Services: []config.Service{{
			ID: "svc", Replicas: 2, Model: "cpu",
			Routing: &config.RoutingPolicy{
				Strategy: RoutingWeightedRR,
				Weights: map[string]float64{
					"svc-instance-0": 1.0,
					"svc-instance-1": 0.0,
				},
			},
			Endpoints: []config.Endpoint{{Path: "/a", MeanCPUMs: 1, CPUSigmaMs: 0}},
		}},
	}
	if err := m.InitializeFromScenario(sc); err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for i := 0; i < 20; i++ {
		req := &models.Request{ServiceName: "svc", Endpoint: "/a", Metadata: map[string]interface{}{}}
		inst, strategy, err := m.SelectInstanceForRequest("svc", req, time.Unix(0, 0))
		if err != nil {
			t.Fatal(err)
		}
		if strategy != RoutingWeightedRR {
			t.Fatalf("expected weighted strategy, got %s", strategy)
		}
		seen[inst.ID()]++
	}
	if seen["svc-instance-1"] != 0 {
		t.Fatalf("expected zero-weight instance to receive no traffic, seen=%v", seen)
	}
}

func TestSelectInstanceForRequest_WeightedRoundRobinFractionalProportions(t *testing.T) {
	m := NewManager()
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8}},
		Services: []config.Service{{
			ID: "svc", Replicas: 2, Model: "cpu",
			Routing: &config.RoutingPolicy{
				Strategy: RoutingWeightedRR,
				Weights: map[string]float64{
					"svc-instance-0": 0.1,
					"svc-instance-1": 0.9,
				},
			},
			Endpoints: []config.Endpoint{{Path: "/a", MeanCPUMs: 1, CPUSigmaMs: 0}},
		}},
	}
	if err := m.InitializeFromScenario(sc); err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for i := 0; i < 1000; i++ {
		req := &models.Request{ServiceName: "svc", Endpoint: "/a", Metadata: map[string]interface{}{}}
		inst, _, err := m.SelectInstanceForRequest("svc", req, time.Unix(0, 0))
		if err != nil {
			t.Fatal(err)
		}
		seen[inst.ID()]++
	}
	if seen["svc-instance-0"] < 80 || seen["svc-instance-0"] > 120 {
		t.Fatalf("expected ~10%% selection on instance-0, seen=%v", seen)
	}
	if seen["svc-instance-1"] < 880 || seen["svc-instance-1"] > 920 {
		t.Fatalf("expected ~90%% selection on instance-1, seen=%v", seen)
	}
}

func TestSelectInstanceForRequest_WeightedRoundRobinAllZeroFallsBack(t *testing.T) {
	m := NewManager()
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8}},
		Services: []config.Service{{
			ID: "svc", Replicas: 2, Model: "cpu",
			Routing: &config.RoutingPolicy{
				Strategy: RoutingWeightedRR,
				Weights: map[string]float64{
					"svc-instance-0": 0.0,
					"svc-instance-1": 0.0,
				},
			},
			Endpoints: []config.Endpoint{{Path: "/a", MeanCPUMs: 1, CPUSigmaMs: 0}},
		}},
	}
	if err := m.InitializeFromScenario(sc); err != nil {
		t.Fatal(err)
	}
	var seq []string
	for i := 0; i < 4; i++ {
		req := &models.Request{ServiceName: "svc", Endpoint: "/a", Metadata: map[string]interface{}{}}
		inst, strategy, err := m.SelectInstanceForRequest("svc", req, time.Unix(0, 0))
		if err != nil {
			t.Fatal(err)
		}
		if strategy != RoutingRoundRobin {
			t.Fatalf("expected fallback round_robin strategy, got %s", strategy)
		}
		seq = append(seq, inst.ID())
	}
	if seq[0] != seq[2] || seq[1] != seq[3] {
		t.Fatalf("expected RR fallback sequence, got %v", seq)
	}
}

func TestSelectInstanceForRequest_WeightedRoundRobinDeterministicSequence(t *testing.T) {
	buildSeq := func() ([]string, error) {
		m := NewManager()
		sc := &config.Scenario{
			Hosts: []config.Host{{ID: "h1", Cores: 8}},
			Services: []config.Service{{
				ID: "svc", Replicas: 3, Model: "cpu",
				Routing: &config.RoutingPolicy{
					Strategy: RoutingWeightedRR,
					Weights: map[string]float64{
						"svc-instance-0": 0.2,
						"svc-instance-1": 0.3,
						"svc-instance-2": 0.5,
					},
				},
				Endpoints: []config.Endpoint{{Path: "/a", MeanCPUMs: 1, CPUSigmaMs: 0}},
			}},
		}
		if err := m.InitializeFromScenario(sc); err != nil {
			return nil, err
		}
		out := make([]string, 0, 40)
		for i := 0; i < 40; i++ {
			req := &models.Request{ServiceName: "svc", Endpoint: "/a", Metadata: map[string]interface{}{}}
			inst, _, err := m.SelectInstanceForRequest("svc", req, time.Unix(0, 0))
			if err != nil {
				return nil, err
			}
			out = append(out, inst.ID())
		}
		return out, nil
	}
	a, err := buildSeq()
	if err != nil {
		t.Fatal(err)
	}
	b, err := buildSeq()
	if err != nil {
		t.Fatal(err)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("weighted sequence should be deterministic; mismatch at %d: %v vs %v", i, a, b)
		}
	}
}

func TestSelectInstanceForRequest_WeightedRoundRobinScaleOutScaleDown(t *testing.T) {
	m := NewManager()
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8}, {ID: "h2", Cores: 8}},
		Services: []config.Service{{
			ID: "svc", Replicas: 2, Model: "cpu",
			Routing: &config.RoutingPolicy{
				Strategy: RoutingWeightedRR,
				Weights: map[string]float64{
					"svc-instance-0": 0.9,
					"svc-instance-1": 0.1,
				},
			},
			Endpoints: []config.Endpoint{{Path: "/a", MeanCPUMs: 1, CPUSigmaMs: 0}},
		}},
	}
	if err := m.InitializeFromScenario(sc); err != nil {
		t.Fatal(err)
	}
	if err := m.ScaleService("svc", 3); err != nil {
		t.Fatal(err)
	}
	seenAfterScaleOut := map[string]struct{}{}
	for i := 0; i < 2500; i++ {
		req := &models.Request{ServiceName: "svc", Endpoint: "/a", Metadata: map[string]interface{}{}}
		inst, _, err := m.SelectInstanceForRequest("svc", req, time.Unix(0, 0))
		if err != nil {
			t.Fatal(err)
		}
		seenAfterScaleOut[inst.ID()] = struct{}{}
	}
	if len(seenAfterScaleOut) < 3 {
		t.Fatalf("expected scale-out instance to join weighted routing pool, seen=%v", seenAfterScaleOut)
	}
	if err := m.ScaleServiceWithOptions("svc", 1, ScaleServiceOptions{SimTime: time.Unix(10, 0), DrainTimeout: time.Hour}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		req := &models.Request{ServiceName: "svc", Endpoint: "/a", Metadata: map[string]interface{}{}}
		inst, _, err := m.SelectInstanceForRequest("svc", req, time.Unix(11, 0))
		if err != nil {
			t.Fatal(err)
		}
		if inst.Lifecycle() != InstanceActive {
			t.Fatalf("draining instance should not receive new weighted traffic, got %s", inst.ID())
		}
	}
}

func TestSelectInstanceForRequest_UnknownStrategyErrors(t *testing.T) {
	m := NewManager()
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8}},
		Services: []config.Service{{
			ID: "svc", Replicas: 1, Model: "cpu",
			Routing:   &config.RoutingPolicy{Strategy: "unknown"},
			Endpoints: []config.Endpoint{{Path: "/a", MeanCPUMs: 1, CPUSigmaMs: 0}},
		}},
	}
	if err := m.InitializeFromScenario(sc); err != nil {
		t.Fatal(err)
	}
	req := &models.Request{ServiceName: "svc", Endpoint: "/a", Metadata: map[string]interface{}{}}
	_, _, err := m.SelectInstanceForRequest("svc", req, time.Unix(0, 0))
	if err == nil {
		t.Fatalf("expected error for unsupported routing strategy")
	}
}

func TestSelectInstanceForRequest_LocalityPreferencePrefersMatchingZone(t *testing.T) {
	m := NewManager()
	sc := &config.Scenario{
		Hosts: []config.Host{
			{ID: "h1", Cores: 8, Zone: "zone-a"},
			{ID: "h2", Cores: 8, Zone: "zone-b"},
		},
		Services: []config.Service{{
			ID: "svc", Replicas: 2, Model: "cpu",
			Routing: &config.RoutingPolicy{
				Strategy:         RoutingRoundRobin,
				LocalityZoneFrom: "client_zone",
			},
			Endpoints: []config.Endpoint{{Path: "/a", MeanCPUMs: 1, CPUSigmaMs: 0}},
		}},
	}
	if err := m.InitializeFromScenario(sc); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		req := &models.Request{ServiceName: "svc", Endpoint: "/a", Metadata: map[string]interface{}{"client_zone": "zone-b"}}
		inst, _, err := m.SelectInstanceForRequest("svc", req, time.Unix(0, 0))
		if err != nil {
			t.Fatal(err)
		}
		host, ok := m.GetHost(inst.HostID())
		if !ok {
			t.Fatalf("host missing for %s", inst.ID())
		}
		if host.Zone() != "zone-b" {
			t.Fatalf("expected zone-b preference, got host=%s zone=%s", host.ID(), host.Zone())
		}
	}
}

func TestSelectInstanceForRequest_LocalityPreferenceFallsBackWhenNoZoneMatch(t *testing.T) {
	m := NewManager()
	sc := &config.Scenario{
		Hosts: []config.Host{
			{ID: "h1", Cores: 8, Zone: "zone-a"},
			{ID: "h2", Cores: 8, Zone: "zone-b"},
		},
		Services: []config.Service{{
			ID: "svc", Replicas: 2, Model: "cpu",
			Routing: &config.RoutingPolicy{
				Strategy:         RoutingRoundRobin,
				LocalityZoneFrom: "client_zone",
			},
			Endpoints: []config.Endpoint{{Path: "/a", MeanCPUMs: 1, CPUSigmaMs: 0}},
		}},
	}
	if err := m.InitializeFromScenario(sc); err != nil {
		t.Fatal(err)
	}
	var seq []string
	for i := 0; i < 4; i++ {
		req := &models.Request{ServiceName: "svc", Endpoint: "/a", Metadata: map[string]interface{}{"client_zone": "zone-x"}}
		inst, strategy, err := m.SelectInstanceForRequest("svc", req, time.Unix(0, 0))
		if err != nil {
			t.Fatal(err)
		}
		if strategy != RoutingRoundRobin {
			t.Fatalf("expected round_robin strategy, got %s", strategy)
		}
		seq = append(seq, inst.ID())
	}
	if seq[0] != seq[2] || seq[1] != seq[3] {
		t.Fatalf("expected RR fallback sequence under non-matching locality, got %v", seq)
	}
}

func TestSelectInstanceForRequest_LocalityPreferenceEndpointOverride(t *testing.T) {
	m := NewManager()
	sc := &config.Scenario{
		Hosts: []config.Host{
			{ID: "h1", Cores: 8, Zone: "zone-a"},
			{ID: "h2", Cores: 8, Zone: "zone-b"},
		},
		Services: []config.Service{{
			ID: "svc", Replicas: 2, Model: "cpu",
			Routing: &config.RoutingPolicy{
				Strategy:         RoutingRoundRobin,
				LocalityZoneFrom: "service_zone",
			},
			Endpoints: []config.Endpoint{{
				Path: "/a", MeanCPUMs: 1, CPUSigmaMs: 0,
				Routing: &config.RoutingPolicy{
					Strategy:         RoutingRoundRobin,
					LocalityZoneFrom: "endpoint_zone",
				},
			}},
		}},
	}
	if err := m.InitializeFromScenario(sc); err != nil {
		t.Fatal(err)
	}
	req := &models.Request{
		ServiceName: "svc",
		Endpoint:    "/a",
		Metadata: map[string]interface{}{
			"service_zone":  "zone-a",
			"endpoint_zone": "zone-b",
		},
	}
	inst, _, err := m.SelectInstanceForRequest("svc", req, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	host, ok := m.GetHost(inst.HostID())
	if !ok {
		t.Fatalf("host missing for %s", inst.ID())
	}
	if host.Zone() != "zone-b" {
		t.Fatalf("expected endpoint locality override to zone-b, got zone=%s host=%s", host.Zone(), host.ID())
	}
}
