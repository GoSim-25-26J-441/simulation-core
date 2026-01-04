package resource

import (
	"sync"
	"time"
)

// ServiceInstance represents a service instance with resource tracking
type ServiceInstance struct {
	mu sync.RWMutex

	id          string
	serviceName string
	hostID      string

	// Resource allocation
	cpuCores float64 // Allocated CPU cores
	memoryMB float64 // Allocated memory in MB

	// Current usage with time-window tracking
	cpuUsageWindow   time.Duration // Time window for CPU utilization calculation (default 1 second)
	cpuUsageInWindow float64       // CPU time consumed in the current time window (ms)
	windowStartTime  time.Time     // Start of the current measurement window

	activeMemoryMB float64 // Memory currently in use (MB)
	activeRequests int     // Number of active requests

	// Queue
	requestQueue []string // Queue of request IDs waiting to be processed

	// Timestamps
	lastUpdate time.Time
}

// NewServiceInstance creates a new service instance
func NewServiceInstance(id, serviceName, hostID string, cpuCores, memoryMB float64) *ServiceInstance {
	now := time.Now()
	return &ServiceInstance{
		id:               id,
		serviceName:      serviceName,
		hostID:           hostID,
		cpuCores:         cpuCores,
		memoryMB:         memoryMB,
		cpuUsageWindow:   1 * time.Second, // Default 1-second window for utilization calculation
		cpuUsageInWindow: 0,
		windowStartTime:  now,
		requestQueue:     make([]string, 0),
		lastUpdate:       now,
	}
}

// ID returns the instance ID
func (s *ServiceInstance) ID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.id
}

// ServiceName returns the service name
func (s *ServiceInstance) ServiceName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.serviceName
}

// HostID returns the host ID
func (s *ServiceInstance) HostID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hostID
}

// CPUCores returns the allocated CPU cores
func (s *ServiceInstance) CPUCores() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cpuCores
}

// MemoryMB returns the allocated memory in MB
func (s *ServiceInstance) MemoryMB() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.memoryMB
}

// CPUUtilization returns CPU utilization (0.0 to 1.0) based on a sliding time window
func (s *ServiceInstance) CPUUtilization() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cpuUtilizationAt(time.Now())
}

// cpuUtilizationAt calculates CPU utilization at a specific time.
// This is an internal method that assumes the caller holds the read lock (s.mu.RLock).
// For public use, call CPUUtilization() instead.
func (s *ServiceInstance) cpuUtilizationAt(currentTime time.Time) float64 {
	if s.cpuCores == 0 {
		return 0.0
	}

	// Check if we need to move to a new window
	windowEnd := s.windowStartTime.Add(s.cpuUsageWindow)
	if currentTime.After(windowEnd) || currentTime.Equal(windowEnd) {
		// We've moved past the current window, so CPU usage decays to 0
		// This implements the time-based decay that the reviewer requested
		return 0.0
	}

	// Calculate utilization based on CPU time consumed in this window
	// Utilization = (CPU time consumed in window / window duration) / available cores
	windowDurationMs := float64(s.cpuUsageWindow.Milliseconds())
	if windowDurationMs == 0 {
		return 0.0
	}

	utilization := (s.cpuUsageInWindow / windowDurationMs) / s.cpuCores
	if utilization > 1.0 {
		return 1.0
	}
	if utilization < 0.0 {
		return 0.0
	}
	return utilization
}

// MemoryUtilization returns memory utilization (0.0 to 1.0)
func (s *ServiceInstance) MemoryUtilization() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.memoryMB == 0 {
		return 0.0
	}
	return s.activeMemoryMB / s.memoryMB
}

// ActiveRequests returns the number of active requests
func (s *ServiceInstance) ActiveRequests() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeRequests
}

// AllocateCPU allocates CPU time for a request
func (s *ServiceInstance) AllocateCPU(cpuTimeMs float64, simTime time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if we need to start a new window
	windowEnd := s.windowStartTime.Add(s.cpuUsageWindow)
	if simTime.After(windowEnd) || simTime.Equal(windowEnd) {
		// Start a new window
		s.windowStartTime = simTime
		s.cpuUsageInWindow = 0
	}

	// Add CPU time to the current window
	s.cpuUsageInWindow += cpuTimeMs

	s.activeRequests++
	s.lastUpdate = simTime
}

// ReleaseCPU releases CPU time for a request
func (s *ServiceInstance) ReleaseCPU(cpuTimeMs float64, simTime time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Note: We don't subtract from cpuUsageInWindow because the CPU time was
	// already consumed during processing. The window automatically resets
	// when we move past the window end time, implementing time-based decay.

	if s.activeRequests > 0 {
		s.activeRequests--
	}
	s.lastUpdate = simTime
}

// AllocateMemory allocates memory
func (s *ServiceInstance) AllocateMemory(memoryMB float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeMemoryMB += memoryMB
}

// ReleaseMemory releases memory
func (s *ServiceInstance) ReleaseMemory(memoryMB float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeMemoryMB >= memoryMB {
		s.activeMemoryMB -= memoryMB
	}
}

// EnqueueRequest adds a request to the queue
func (s *ServiceInstance) EnqueueRequest(requestID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requestQueue = append(s.requestQueue, requestID)
}

// DequeueRequest removes and returns the next request from the queue
func (s *ServiceInstance) DequeueRequest() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requestQueue) == 0 {
		return "", false
	}
	requestID := s.requestQueue[0]
	// Clear the reference to the dequeued element to avoid retaining it in the backing array.
	s.requestQueue[0] = ""
	if len(s.requestQueue) == 1 {
		// When the queue becomes empty, release the backing array.
		s.requestQueue = nil
	} else {
		s.requestQueue = s.requestQueue[1:]
	}
	return requestID, true
}

// QueueLength returns the current queue length
func (s *ServiceInstance) QueueLength() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.requestQueue)
}

// HasCapacity checks if the instance has capacity for a new request
func (s *ServiceInstance) HasCapacity() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// If no CPU cores are allocated, this instance cannot process requests
	if s.cpuCores <= 0 {
		return false
	}

	// Check if CPU utilization is below 100%
	// Use the internal method to avoid re-acquiring the lock
	utilization := s.cpuUtilizationAt(time.Now())
	return utilization < 1.0
}

// ActiveMemoryMB returns the active memory usage in MB (for internal use)
func (s *ServiceInstance) ActiveMemoryMB() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeMemoryMB
}
