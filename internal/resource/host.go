package resource

import (
	"sync"
)

// Host represents a physical or virtual host with capacity constraints
type Host struct {
	mu sync.RWMutex

	id       string
	cpuCores int
	memoryGB int

	// Current utilization (aggregated from all service instances)
	cpuUtilization    float64 // 0.0 to 1.0
	memoryUtilization float64 // 0.0 to 1.0

	// Service instances running on this host
	serviceInstances []string
}

// NewHost creates a new host
func NewHost(id string, cpuCores, memoryGB int) *Host {
	return &Host{
		id:               id,
		cpuCores:         cpuCores,
		memoryGB:         memoryGB,
		serviceInstances: make([]string, 0),
	}
}

// ID returns the host ID
func (h *Host) ID() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.id
}

// CPUCores returns the number of CPU cores
func (h *Host) CPUCores() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cpuCores
}

// MemoryGB returns the memory in GB
func (h *Host) MemoryGB() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.memoryGB
}

// CPUUtilization returns CPU utilization (0.0 to 1.0)
func (h *Host) CPUUtilization() float64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cpuUtilization
}

// MemoryUtilization returns memory utilization (0.0 to 1.0)
func (h *Host) MemoryUtilization() float64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.memoryUtilization
}

// ServiceInstances returns the list of service instance IDs on this host
func (h *Host) ServiceInstances() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	instances := make([]string, len(h.serviceInstances))
	copy(instances, h.serviceInstances)
	return instances
}

// AddService adds a service instance to this host
func (h *Host) AddService(instanceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.serviceInstances = append(h.serviceInstances, instanceID)
}

// RemoveService removes a service instance from this host
func (h *Host) RemoveService(instanceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, id := range h.serviceInstances {
		if id == instanceID {
			h.serviceInstances = append(h.serviceInstances[:i], h.serviceInstances[i+1:]...)
			break
		}
	}
}

// InstanceUtilizationSource provides CPU and memory utilization data for aggregation.
// ServiceInstance implements this interface.
type InstanceUtilizationSource interface {
	CPUUtilization() float64
	CPUCores() float64
	ActiveMemoryMB() float64
}

// UpdateCPUUtilization recalculates CPU utilization by aggregating from service instances.
// It sums (instance CPU utilization × allocated cores) across all instances on this host,
// then divides by host CPU cores to get host-level utilization (0.0 to 1.0).
func (h *Host) UpdateCPUUtilization(instances []InstanceUtilizationSource) {
	if len(instances) == 0 {
		h.SetCPUUtilization(0)
		return
	}
	totalCPUUsed := 0.0
	for _, inst := range instances {
		totalCPUUsed += inst.CPUUtilization() * inst.CPUCores()
	}
	h.mu.RLock()
	cores := h.cpuCores
	h.mu.RUnlock()
	if cores > 0 {
		util := totalCPUUsed / float64(cores)
		if util > 1.0 {
			util = 1.0
		}
		if util < 0.0 {
			util = 0.0
		}
		h.SetCPUUtilization(util)
	}
}

// UpdateMemoryUtilization recalculates memory utilization by aggregating from service instances.
// It sums active memory (MB) across all instances, converts to GB, and divides by host
// memory capacity to get host-level utilization (0.0 to 1.0).
func (h *Host) UpdateMemoryUtilization(instances []InstanceUtilizationSource) {
	if len(instances) == 0 {
		h.SetMemoryUtilization(0)
		return
	}
	totalMemoryUsedMB := 0.0
	for _, inst := range instances {
		totalMemoryUsedMB += inst.ActiveMemoryMB()
	}
	h.mu.RLock()
	memoryGB := h.memoryGB
	h.mu.RUnlock()
	if memoryGB > 0 {
		hostUtil := (totalMemoryUsedMB / 1024.0) / float64(memoryGB)
		if hostUtil > 1.0 {
			hostUtil = 1.0
		}
		if hostUtil < 0.0 {
			hostUtil = 0.0
		}
		h.SetMemoryUtilization(hostUtil)
	}
}

// SetCPUUtilization sets the CPU utilization (called by Manager)
func (h *Host) SetCPUUtilization(util float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if util < 0.0 {
		util = 0.0
	}
	if util > 1.0 {
		util = 1.0
	}
	h.cpuUtilization = util
}

// SetMemoryUtilization sets the memory utilization (called by Manager)
func (h *Host) SetMemoryUtilization(util float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if util < 0.0 {
		util = 0.0
	}
	if util > 1.0 {
		util = 1.0
	}
	h.memoryUtilization = util
}

// HasCapacity checks if the host has capacity for additional resources
func (h *Host) HasCapacity() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cpuUtilization < 1.0 && h.memoryUtilization < 1.0
}
