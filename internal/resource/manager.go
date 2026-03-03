package resource

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

const (
	// DefaultInstanceCPUCores is the default number of CPU cores allocated to a service instance
	DefaultInstanceCPUCores = 1.0
	// DefaultInstanceMemoryMB is the default amount of memory (in MB) allocated to a service instance
	DefaultInstanceMemoryMB = 512.0
)

// Manager tracks resource usage across hosts and service instances
type Manager struct {
	mu                   sync.RWMutex
	hosts                map[string]*Host
	instances            map[string]*ServiceInstance
	hostToInstances      map[string][]string           // host ID -> instance IDs
	roundRobinIdx        map[string]int                // service name -> last selected instance index
	sortedServiceInstMap map[string][]*ServiceInstance // service name -> sorted instances (cached); scale-down only shrinks this
	nextInstanceID       int                           // global counter for new instance IDs when scaling up
}

// NewManager creates a new resource manager
func NewManager() *Manager {
	return &Manager{
		hosts:                make(map[string]*Host),
		instances:            make(map[string]*ServiceInstance),
		hostToInstances:      make(map[string][]string),
		roundRobinIdx:        make(map[string]int),
		sortedServiceInstMap: make(map[string][]*ServiceInstance),
	}
}

// InitializeFromScenario initializes the resource manager from a scenario configuration
func (m *Manager) InitializeFromScenario(scenario *config.Scenario) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Initialize hosts
	for _, hostConfig := range scenario.Hosts {
		host := NewHost(hostConfig.ID, hostConfig.Cores, 16*1024) // Memory not specified in config, default to 16GB (in MB units)
		m.hosts[hostConfig.ID] = host
	}

	// Initialize service instances
	instanceID := 0
	for _, serviceConfig := range scenario.Services {
		// Distribute replicas across available hosts
		hostIDs := make([]string, 0, len(m.hosts))
		for hostID := range m.hosts {
			hostIDs = append(hostIDs, hostID)
		}
		if len(hostIDs) == 0 {
			return fmt.Errorf("no hosts available for service %s", serviceConfig.ID)
		}

		for replica := 0; replica < serviceConfig.Replicas; replica++ {
			// Round-robin assignment of replicas to hosts
			hostID := hostIDs[replica%len(hostIDs)]
			instanceIDStr := fmt.Sprintf("%s-instance-%d", serviceConfig.ID, instanceID)
			instanceID++

			// Use configured values if provided, otherwise use defaults
			cpuCores := serviceConfig.CPUCores
			if cpuCores == 0 {
				cpuCores = DefaultInstanceCPUCores
			}
			memoryMB := serviceConfig.MemoryMB
			if memoryMB == 0 {
				memoryMB = DefaultInstanceMemoryMB
			}

			instance := NewServiceInstance(instanceIDStr, serviceConfig.ID, hostID, cpuCores, memoryMB)
			m.instances[instanceIDStr] = instance
			m.hosts[hostID].AddService(instanceIDStr)
			m.hostToInstances[hostID] = append(m.hostToInstances[hostID], instanceIDStr)
		}
	}
	m.nextInstanceID = instanceID

	// Build the sorted instance cache for each service
	m.rebuildSortedInstanceCache()

	return nil
}

// GetServiceInstance returns a service instance by ID
func (m *Manager) GetServiceInstance(instanceID string) (*ServiceInstance, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	instance, ok := m.instances[instanceID]
	return instance, ok
}

// GetHost returns a host by ID
func (m *Manager) GetHost(hostID string) (*Host, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	host, ok := m.hosts[hostID]
	return host, ok
}

// GetInstancesForService returns all instances for a given service name
func (m *Manager) GetInstancesForService(serviceName string) []*ServiceInstance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.getInstancesForServiceLocked(serviceName)
}

// getInstancesForServiceLocked returns instances for a service (assumes lock is held)
func (m *Manager) getInstancesForServiceLocked(serviceName string) []*ServiceInstance {
	instances := make([]*ServiceInstance, 0)
	for _, instance := range m.instances {
		if instance.ServiceName() == serviceName {
			instances = append(instances, instance)
		}
	}
	return instances
}

// SelectInstanceForService selects an instance for a service using round-robin selection
func (m *Manager) SelectInstanceForService(serviceName string) (*ServiceInstance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Use cached sorted instances if available
	instances, ok := m.sortedServiceInstMap[serviceName]
	if !ok || len(instances) == 0 {
		return nil, fmt.Errorf("no instances available for service %s", serviceName)
	}

	// Get the current index for this service (defaults to 0)
	idx := m.roundRobinIdx[serviceName]

	// Select the instance at the current index
	selectedInstance := instances[idx]

	// Update the index for next time (wrap around to 0 if we reach the end)
	m.roundRobinIdx[serviceName] = (idx + 1) % len(instances)

	return selectedInstance, nil
}

// rebuildSortedInstanceCache rebuilds the cache of sorted instances per service
// Assumes lock is already held by caller
func (m *Manager) rebuildSortedInstanceCache() {
	// Group instances by service
	serviceInstances := make(map[string][]*ServiceInstance)
	for _, instance := range m.instances {
		serviceName := instance.ServiceName()
		serviceInstances[serviceName] = append(serviceInstances[serviceName], instance)
	}

	// Sort and cache each service's instances
	for serviceName, instances := range serviceInstances {
		sort.Slice(instances, func(i, j int) bool {
			return instances[i].ID() < instances[j].ID()
		})
		m.sortedServiceInstMap[serviceName] = instances
	}
}

// ScaleService changes the number of replicas for a service at runtime.
// Scale-up: adds new instances (round-robin across hosts) using same CPU/memory as existing instances.
// Scale-down: reduces the set of instances that receive new traffic (soft scale-down; existing instances remain for in-flight requests).
func (m *Manager) ScaleService(serviceID string, newReplicas int) error {
	if newReplicas < 1 {
		return fmt.Errorf("replicas must be at least 1, got %d", newReplicas)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	instances := m.getInstancesForServiceLocked(serviceID)
	if len(instances) == 0 {
		return fmt.Errorf("service not found: %s", serviceID)
	}

	sort.Slice(instances, func(i, j int) bool {
		return instances[i].ID() < instances[j].ID()
	})
	currentN := len(instances)

	if newReplicas > currentN {
		// Scale up: add new instances
		hostIDs := make([]string, 0, len(m.hosts))
		for hostID := range m.hosts {
			hostIDs = append(hostIDs, hostID)
		}
		if len(hostIDs) == 0 {
			return fmt.Errorf("no hosts available")
		}
		first := instances[0]
		cpuCores := first.CPUCores()
		memoryMB := first.MemoryMB()

		for i := 0; i < newReplicas-currentN; i++ {
			hostID := hostIDs[(currentN+i)%len(hostIDs)]
			instanceIDStr := fmt.Sprintf("%s-instance-%d", serviceID, m.nextInstanceID)
			m.nextInstanceID++

			instance := NewServiceInstance(instanceIDStr, serviceID, hostID, cpuCores, memoryMB)
			m.instances[instanceIDStr] = instance
			m.hosts[hostID].AddService(instanceIDStr)
			m.hostToInstances[hostID] = append(m.hostToInstances[hostID], instanceIDStr)
		}
		// Rebuild sorted cache for this service so new instances are included
		instances = m.getInstancesForServiceLocked(serviceID)
		sort.Slice(instances, func(i, j int) bool {
			return instances[i].ID() < instances[j].ID()
		})
		m.sortedServiceInstMap[serviceID] = instances
	} else if newReplicas < currentN {
		// Soft scale down: only the first newReplicas instances receive new traffic
		m.sortedServiceInstMap[serviceID] = instances[:newReplicas]
		if m.roundRobinIdx[serviceID] >= newReplicas {
			m.roundRobinIdx[serviceID] = 0
		}
	}
	// else newReplicas == currentN: no-op

	return nil
}

// ActiveReplicas returns the number of replicas currently in rotation for the service (for GET config).
func (m *Manager) ActiveReplicas(serviceID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	instances, ok := m.sortedServiceInstMap[serviceID]
	if !ok {
		return 0
	}
	return len(instances)
}

// UpdateServiceResources updates per-instance CPU cores and memory (MB) for all
// instances of a given service. Passing 0 for a field leaves it unchanged.
func (m *Manager) UpdateServiceResources(serviceID string, cpuCores, memoryMB float64) error {
	if cpuCores < 0 || memoryMB < 0 {
		return fmt.Errorf("cpu_cores and memory_mb must be non-negative")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	instances := m.getInstancesForServiceLocked(serviceID)
	if len(instances) == 0 {
		return fmt.Errorf("service not found: %s", serviceID)
	}

	for _, inst := range instances {
		if cpuCores > 0 {
			inst.SetCPUCores(cpuCores)
		}
		if memoryMB > 0 {
			inst.SetMemoryMB(memoryMB)
		}
	}

	// Host utilization will be recomputed on next allocation/release; no need
	// to force an update here for correctness of the simulator.
	return nil
}

// ListServiceIDs returns all service IDs that have at least one instance (for GET config).
func (m *Manager) ListServiceIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.sortedServiceInstMap))
	for id := range m.sortedServiceInstMap {
		ids = append(ids, id)
	}
	return ids
}

// AllocateCPU allocates CPU resources for a request
func (m *Manager) AllocateCPU(instanceID string, cpuTimeMs float64, simTime time.Time) error {
	// Collect references while holding Manager lock
	m.mu.Lock()
	instance, ok := m.instances[instanceID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("instance not found: %s", instanceID)
	}

	// Get host ID while we have the instance reference
	hostID := instance.HostID()
	host, ok := m.hosts[hostID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("host not found: %s", hostID)
	}

	// Get all instances on this host for utilization calculation
	instances := m.collectInstancesForHost(hostID)
	m.mu.Unlock()

	// Now perform operations without holding Manager lock
	// Allocate CPU on instance
	instance.AllocateCPU(cpuTimeMs, simTime)

	// Update host utilization (aggregate from all instances on this host)
	m.updateHostCPUUtilizationWithData(host, instances)

	return nil
}

// ReleaseCPU releases CPU resources for a request
func (m *Manager) ReleaseCPU(instanceID string, cpuTimeMs float64, simTime time.Time) {
	// Collect references while holding Manager lock
	m.mu.Lock()
	instance, ok := m.instances[instanceID]
	if !ok {
		m.mu.Unlock()
		return
	}

	hostID := instance.HostID()
	host, ok := m.hosts[hostID]
	if !ok {
		m.mu.Unlock()
		return
	}

	// Get all instances on this host for utilization calculation
	instances := m.collectInstancesForHost(hostID)
	m.mu.Unlock()

	// Release CPU and update utilization without holding Manager lock
	instance.ReleaseCPU(cpuTimeMs, simTime)
	m.updateHostCPUUtilizationWithData(host, instances)
}

// AllocateMemory allocates memory resources
func (m *Manager) AllocateMemory(instanceID string, memoryMB float64) error {
	// Collect references while holding Manager lock
	m.mu.Lock()
	instance, ok := m.instances[instanceID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("instance not found: %s", instanceID)
	}

	hostID := instance.HostID()
	host, ok := m.hosts[hostID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("host not found: %s", hostID)
	}

	// Check host memory capacity (skip check if host has unlimited memory, i.e., 0 GB configured)
	// Note: This check is best-effort. We read host memory utilization without holding the Manager
	// lock for the entire allocation operation to avoid lock hierarchy issues. This means another
	// goroutine could allocate memory between our check and allocation, potentially causing
	// over-allocation. This is an acceptable trade-off for better concurrency.
	hostMemoryGB := host.MemoryGB()
	if hostMemoryGB > 0 {
		hostMemUtil := host.MemoryUtilization()
		if hostMemUtil+(memoryMB/1024.0)/float64(hostMemoryGB) > 1.0 {
			m.mu.Unlock()
			return fmt.Errorf("host memory at capacity")
		}
	}

	// Get all instances on this host for utilization calculation
	instances := m.collectInstancesForHost(hostID)
	m.mu.Unlock()

	// Allocate memory and update utilization without holding Manager lock
	instance.AllocateMemory(memoryMB)
	m.updateHostMemoryUtilizationWithData(host, instances)

	return nil
}

// ReleaseMemory releases memory resources
func (m *Manager) ReleaseMemory(instanceID string, memoryMB float64) {
	// Collect references while holding Manager lock
	m.mu.Lock()
	instance, ok := m.instances[instanceID]
	if !ok {
		m.mu.Unlock()
		return
	}

	hostID := instance.HostID()
	host, ok := m.hosts[hostID]
	if !ok {
		m.mu.Unlock()
		return
	}

	// Get all instances on this host for utilization calculation
	instances := m.collectInstancesForHost(hostID)
	m.mu.Unlock()

	// Release memory and update utilization without holding Manager lock
	instance.ReleaseMemory(memoryMB)
	m.updateHostMemoryUtilizationWithData(host, instances)
}

// EnqueueRequest adds a request to the instance queue
func (m *Manager) EnqueueRequest(instanceID string, requestID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	instance, ok := m.instances[instanceID]
	if !ok {
		return fmt.Errorf("instance not found: %s", instanceID)
	}

	instance.EnqueueRequest(requestID)
	return nil
}

// DequeueRequest removes a request from the instance queue
func (m *Manager) DequeueRequest(instanceID string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	instance, ok := m.instances[instanceID]
	if !ok {
		return "", false
	}

	return instance.DequeueRequest()
}

// GetQueueLength returns the queue length for an instance
func (m *Manager) GetQueueLength(instanceID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	instance, ok := m.instances[instanceID]
	if !ok {
		return 0
	}

	return instance.QueueLength()
}

// GetHostUtilization returns CPU and memory utilization for a host
func (m *Manager) GetHostUtilization(hostID string) (cpuUtil, memUtil float64, ok bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	host, ok := m.hosts[hostID]
	if !ok {
		return 0, 0, false
	}

	return host.CPUUtilization(), host.MemoryUtilization(), true
}

// GetInstanceUtilization returns CPU and memory utilization for an instance
func (m *Manager) GetInstanceUtilization(instanceID string) (cpuUtil, memUtil float64, ok bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	instance, ok := m.instances[instanceID]
	if !ok {
		return 0, 0, false
	}

	return instance.CPUUtilization(), instance.MemoryUtilization(), true
}

// GetAllHosts returns all hosts
func (m *Manager) GetAllHosts() []*Host {
	m.mu.RLock()
	defer m.mu.RUnlock()

	hosts := make([]*Host, 0, len(m.hosts))
	for _, host := range m.hosts {
		hosts = append(hosts, host)
	}
	return hosts
}

// GetAllInstances returns all service instances
func (m *Manager) GetAllInstances() []*ServiceInstance {
	m.mu.RLock()
	defer m.mu.RUnlock()

	instances := make([]*ServiceInstance, 0, len(m.instances))
	for _, instance := range m.instances {
		instances = append(instances, instance)
	}
	return instances
}

// collectInstancesForHost gathers all ServiceInstance pointers for a given host.
// This helper method assumes the Manager lock is already held by the caller.
func (m *Manager) collectInstancesForHost(hostID string) []*ServiceInstance {
	instanceIDs := m.hostToInstances[hostID]
	instances := make([]*ServiceInstance, 0, len(instanceIDs))
	for _, id := range instanceIDs {
		if inst, ok := m.instances[id]; ok {
			instances = append(instances, inst)
		}
	}
	return instances
}

// updateHostCPUUtilizationWithData updates host CPU utilization by delegating to Host aggregation
func (m *Manager) updateHostCPUUtilizationWithData(host *Host, instances []*ServiceInstance) {
	sources := make([]InstanceUtilizationSource, len(instances))
	for i, inst := range instances {
		sources[i] = inst
	}
	host.UpdateCPUUtilization(sources)
}

// updateHostMemoryUtilizationWithData updates host memory utilization by delegating to Host aggregation
func (m *Manager) updateHostMemoryUtilizationWithData(host *Host, instances []*ServiceInstance) {
	sources := make([]InstanceUtilizationSource, len(instances))
	for i, inst := range instances {
		sources[i] = inst
	}
	host.UpdateMemoryUtilization(sources)
}
