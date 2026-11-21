// Package attribution gives a target its share of a measured energy
// sample. The target is one process (--pid) or one container
// (--container); its CPU time comes from host/proc or host/cgroup, and the
// host's active and idle time from host/proc.
//
// Over one interval, for the total and for every zone:
//
//	active energy = energy × active ticks / (active + idle ticks)
//	target energy = active energy × target CPU time / active CPU time
//
// The target only gets a share of the active energy; the idle energy is the
// machine's, not any target's. Active and idle energy are machine-wide, so
// they are used here but not part of a target's sample. Steal ticks (time
// the hypervisor gave to other VMs) are left out: this machine's CPUs did
// not run then, so they used no energy for it.
package attribution

import (
	"errors"
	"time"

	"sathiraumesh/ampere/internal/host/attribution/energy"
	"sathiraumesh/ampere/internal/host/proc"
)

// ErrNoCPUTime means no CPU ticks passed in the interval, so energy cannot
// be split. The interval is shorter than one clock tick (10 ms at 100 Hz).
var ErrNoCPUTime = errors.New("no CPU time passed in the interval: use a longer --interval")

// Target is what energy is attributed to: a process (PID and Comm set) or
// a container (Container and Runtime set).
type Target struct {
	PID       int    `json:"pid,omitempty"`
	Comm      string `json:"comm,omitempty"`      // command name of the process
	Container string `json:"container,omitempty"` // full container ID
	Runtime   string `json:"runtime,omitempty"`   // "docker" or "podman"
}

// IsContainer reports whether the target is a container.
func (t Target) IsContainer() bool { return t.Container != "" }

// EnergySplit is one measured energy amount (the machine's, for the total
// or a zone) and the target's share of it. Parent zones include their
// children, so splits of nested zones must not be summed.
type EnergySplit struct {
	EnergyJ float64 `json:"energy_j"` // measured energy of the interval (machine)
	TargetJ float64 `json:"target_j"` // the target's share of its active part
	TargetW float64 `json:"target_w"` // TargetJ / interval
}

// ZoneSplit is the target's share of one zone. Zones are whatever the energy source
// reports on this machine, not a fixed list: e.g. package-0, package-1,
// core, uncore, dram or psys for RAPL. A zone is split with the same CPU
// time as the total, so for non-CPU zones such as dram the target's share
// is an estimate by CPU time, not a measurement of that component.
type ZoneSplit struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Parent string `json:"parent,omitempty"`
	EnergySplit
}

// Sample is one target's energy over an interval: its share of the device's
// total and of every zone.
type Sample struct {
	Target
	Device    string    `json:"device"`
	Source    string    `json:"source"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	IntervalS float64   `json:"interval_s"`

	// TargetCPU is the target's share of the machine's active CPU time,
	// 0 to 1.
	TargetCPU float64 `json:"target_cpu"`

	EnergySplit             // total energy of the device
	Zones       []ZoneSplit `json:"zones,omitempty"`
}

// NewSample splits the energy sample s (total and zones) using the host's
// CPU usage and the target's CPU time in seconds over the same interval.
func NewSample(s energy.Sample, t Target, cpu proc.CPUUsage, targetCPUSeconds float64) (Sample, error) {
	ran := cpu.Active + cpu.Idle
	if ran == 0 {
		return Sample{}, ErrNoCPUTime
	}
	activeShare := float64(cpu.Active) / float64(ran)
	targetShare := Share(cpu, targetCPUSeconds)

	split := func(energyJ float64) EnergySplit {
		activeJ := energyJ * activeShare // machine-wide: not reported
		targetJ := activeJ * targetShare
		out := EnergySplit{EnergyJ: energyJ, TargetJ: targetJ}
		if s.IntervalS > 0 {
			out.TargetW = targetJ / s.IntervalS
		}
		return out
	}

	out := Sample{
		Target:      t,
		Device:      s.Device,
		Source:      s.Source,
		Start:       s.Start,
		End:         s.End,
		IntervalS:   s.IntervalS,
		TargetCPU:   targetShare,
		EnergySplit: split(s.EnergyJ),
	}
	for _, z := range s.Zones {
		out.Zones = append(out.Zones, ZoneSplit{
			ID:          z.ID,
			Name:        z.Name,
			Parent:      z.Parent,
			EnergySplit: split(z.EnergyJ),
		})
	}
	return out, nil
}

// Share is the target's share of the host's active CPU time, 0 to 1.
// targetCPUSeconds is compared with the active ticks turned into seconds,
// so ticks (processes) and µs (containers) can both be used.
//
// It is capped at 1: the target and host counters are read a moment apart
// and have different resolutions (10 ms ticks vs µs), so a target using all
// CPUs can show slightly more time than the host did.
func Share(cpu proc.CPUUsage, targetCPUSeconds float64) float64 {
	active := proc.Seconds(cpu.Active)
	if active <= 0 || targetCPUSeconds <= 0 {
		return 0
	}
	if targetCPUSeconds >= active {
		return 1
	}
	return targetCPUSeconds / active
}
