package resource

import (
	"fmt"
	"sync"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// Manager tracks resource usage across hosts and service instances
type Manager struct {
	mu        sync.RWMutex
	hosts     map[string]*Host
	instances map[string]*ServiceInstance
}

// NewManager creates a new resource manager
func NewManager() *Manager {
	return &Manager{
		hosts:     make(map[string]*Host),
		instances: make(map[string]*ServiceInstance),
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

			instance := NewServiceInstance(instanceIDStr, serviceConfig.ID, hostID, 1.0, 512.0) // Default: 1 CPU core, 512MB memory
			m.instances[instanceIDStr] = instance
			m.hosts[hostID].AddService(instanceIDStr)
		}
	}

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

	instances := make([]*ServiceInstance, 0)
	for _, instance := range m.instances {
		if instance.ServiceName() == serviceName {
			instances = append(instances, instance)
		}
	}
	return instances
}

// SelectInstanceForService selects an instance for a service (round-robin or least loaded)
func (m *Manager) SelectInstanceForService(serviceName string) (*ServiceInstance, error) {
	instances := m.GetInstancesForService(serviceName)
	if len(instances) == 0 {
		return nil, fmt.Errorf("no instances available for service %s", serviceName)
	}

	// Simple round-robin selection (can be enhanced with least-loaded selection)
	// For now, use the first instance (in a real implementation, we'd track last selected)
	return instances[0], nil
}

// AllocateCPU allocates CPU resources for a request
func (m *Manager) AllocateCPU(instanceID string, cpuTimeMs float64, simTime time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	instance, ok := m.instances[instanceID]
	if !ok {
		return fmt.Errorf("instance not found: %s", instanceID)
	}

	host, ok := m.hosts[instance.HostID()]
	if !ok {
		return fmt.Errorf("host not found: %s", instance.HostID())
	}

	// Check host capacity
	// We'll check at the instance level, not host level for now
	// Host capacity checking can be added later if needed

	// Allocate CPU on instance
	instance.AllocateCPU(cpuTimeMs, simTime)

	// Update host utilization (aggregate from all instances on this host)
	m.updateHostCPUUtilization(host.ID())

	return nil
}

// ReleaseCPU releases CPU resources for a request
func (m *Manager) ReleaseCPU(instanceID string, cpuTimeMs float64, simTime time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	instance, ok := m.instances[instanceID]
	if !ok {
		return
	}

	instance.ReleaseCPU(cpuTimeMs, simTime)

	host, ok := m.hosts[instance.HostID()]
	if ok {
		m.updateHostCPUUtilization(host.ID())
	}
}

// AllocateMemory allocates memory resources
func (m *Manager) AllocateMemory(instanceID string, memoryMB float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	instance, ok := m.instances[instanceID]
	if !ok {
		return fmt.Errorf("instance not found: %s", instanceID)
	}

	host, ok := m.hosts[instance.HostID()]
	if !ok {
		return fmt.Errorf("host not found: %s", instance.HostID())
	}

	// Check host memory capacity (skip check if host has unlimited memory, i.e., 0 GB configured)
	if host.MemoryGB() > 0 && host.MemoryUtilization()+(memoryMB/1024.0)/float64(host.MemoryGB()) > 1.0 {
		return fmt.Errorf("host memory at capacity")
	}

	instance.AllocateMemory(memoryMB)
	m.updateHostMemoryUtilization(host.ID())

	return nil
}

// ReleaseMemory releases memory resources
func (m *Manager) ReleaseMemory(instanceID string, memoryMB float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	instance, ok := m.instances[instanceID]
	if !ok {
		return
	}

	instance.ReleaseMemory(memoryMB)

	host, ok := m.hosts[instance.HostID()]
	if ok {
		m.updateHostMemoryUtilization(host.ID())
	}
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

// updateHostCPUUtilization recalculates host CPU utilization from all instances
func (m *Manager) updateHostCPUUtilization(hostID string) {
	host, ok := m.hosts[hostID]
	if !ok {
		return
	}

	totalCPUUsed := 0.0

	for _, instance := range m.instances {
		if instance.HostID() != hostID {
			continue
		}
		// Sum up CPU utilization from all instances
		// CPU utilization is measured as cores used per second
		// We need to aggregate the active CPU time
		// For simplicity, we'll use the instance's CPU utilization * its allocated cores
		instanceUtil := instance.CPUUtilization()
		instanceCores := instance.CPUCores()
		totalCPUUsed += instanceUtil * instanceCores
	}

	// Host utilization = total CPU used / host CPU cores
	hostUtil := totalCPUUsed / float64(host.CPUCores())
	if hostUtil > 1.0 {
		hostUtil = 1.0
	}
	host.SetCPUUtilization(hostUtil)
}

// updateHostMemoryUtilization recalculates host memory utilization from all instances
func (m *Manager) updateHostMemoryUtilization(hostID string) {
	host, ok := m.hosts[hostID]
	if !ok {
		return
	}

	totalMemoryUsedMB := 0.0

	for _, instance := range m.instances {
		if instance.HostID() != hostID {
			continue
		}
		// Sum up memory usage from all instances
		totalMemoryUsedMB += instance.ActiveMemoryMB()
	}

	// Host utilization = total memory used / host memory
	if host.MemoryGB() > 0 {
		hostUtil := (totalMemoryUsedMB / 1024.0) / float64(host.MemoryGB())
		if hostUtil > 1.0 {
			hostUtil = 1.0
		}
		host.SetMemoryUtilization(hostUtil)
	}
}
