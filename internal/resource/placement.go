package resource

import (
	"sort"
	"time"
)

// InstancePlacement is an immutable snapshot of one service instance placement and observed stats.
// Suitable for APIs and UI graphs; order instances by (HostID, InstanceID) via GetInstancePlacements.
type InstancePlacement struct {
	InstanceID        string
	ServiceID         string
	HostID            string
	HostZone          string
	HostLabels        map[string]string
	Lifecycle         string // ACTIVE or DRAINING
	CPUCores          float64
	MemoryMB          float64
	CPUUtilization    float64
	MemoryUtilization float64
	ActiveRequests    int32
	QueueLength       int32
}

func safeInt32FromInt(v int) int32 {
	if v > int(^uint32(0)>>1) {
		return int32(^uint32(0) >> 1)
	}
	if v < -int(^uint32(0)>>1)-1 {
		return -int32(^uint32(0)>>1) - 1
	}
	return int32(v)
}

// GetInstancePlacements returns a stable-ordered snapshot of all instances (host_id, then instance_id).
// CPU utilization is evaluated at simTime; zero simTime uses wall-clock now (avoid in DES paths).
func (m *Manager) GetInstancePlacements(simTime time.Time) []InstancePlacement {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if simTime.IsZero() {
		simTime = time.Now()
	}
	type row struct {
		host, id string
		inst     *ServiceInstance
	}
	rows := make([]row, 0, len(m.instances))
	for id, inst := range m.instances {
		rows = append(rows, row{host: inst.HostID(), id: id, inst: inst})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].host != rows[j].host {
			return rows[i].host < rows[j].host
		}
		return rows[i].id < rows[j].id
	})
	out := make([]InstancePlacement, 0, len(rows))
	for _, r := range rows {
		inst := r.inst
		lc := "ACTIVE"
		if inst.Lifecycle() == InstanceDraining {
			lc = "DRAINING"
		}
		out = append(out, InstancePlacement{
			InstanceID: r.id,
			ServiceID:  inst.ServiceName(),
			HostID:     inst.HostID(),
			HostZone: func() string {
				if h, ok := m.hosts[inst.HostID()]; ok && h != nil {
					return h.Zone()
				}
				return ""
			}(),
			HostLabels: func() map[string]string {
				if h, ok := m.hosts[inst.HostID()]; ok && h != nil {
					return h.Labels()
				}
				return nil
			}(),
			Lifecycle:         lc,
			CPUCores:          inst.CPUCores(),
			MemoryMB:          inst.MemoryMB(),
			CPUUtilization:    inst.CPUUtilizationAt(simTime),
			MemoryUtilization: inst.MemoryUtilization(),
			ActiveRequests:    safeInt32FromInt(inst.ActiveRequests()),
			QueueLength:       safeInt32FromInt(inst.QueueLength()),
		})
	}
	return out
}
