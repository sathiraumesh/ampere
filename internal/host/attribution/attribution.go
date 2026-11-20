// Package attribution turns a measured energy sample into active, idle and
// per-process energy, using CPU time from host/proc.
//
// Over one interval, for the total and for every zone:
//
//	active energy  = energy × active ticks / (active + idle ticks)
//	idle energy    = energy × idle ticks   / (active + idle ticks)
//	process energy = active energy × process ticks / active ticks
//
// Active + idle is always the whole energy. Steal ticks (time the
// hypervisor gave to other VMs) are left out: this machine's CPUs did not
// run then, so they used no energy for it.
package attribution

import (
	"errors"
	"time"

	"sathiraumesh/ampere/internal/devices"
	"sathiraumesh/ampere/internal/host/proc"
)

// ErrNoCPUTime means no CPU ticks passed in the interval, so energy cannot
// be split. The interval is shorter than one clock tick (10 ms at 100 Hz).
var ErrNoCPUTime = errors.New("no CPU time passed in the interval: use a longer --interval")

// EnergySplit is one energy amount split into active and idle parts, and
// the process's share of the active part. Parent zones include their
// children, so splits of nested zones must not be summed.
type EnergySplit struct {
	EnergyJ  float64 `json:"energy_j"`  // measured energy of the interval
	ActiveJ  float64 `json:"active_j"`  // spent while the CPUs were active
	IdleJ    float64 `json:"idle_j"`    // spent while the CPUs were idle
	ProcessJ float64 `json:"process_j"` // the process's share of ActiveJ
	ProcessW float64 `json:"process_w"` // ProcessJ / interval
}

// ZoneSplit is the split of one zone. Zones are whatever the energy source
// reports on this machine, not a fixed list: e.g. package-0, package-1,
// core, uncore, dram or psys for RAPL. A zone is split with the same CPU
// active/idle ticks as the total, so for non-CPU zones such as dram the
// split is an estimate by CPU time, not a measurement of that component.
type ZoneSplit struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Parent string `json:"parent,omitempty"`
	EnergySplit
}

// ProcessSample is one process's energy over an interval: the device's
// total and every zone, each split into active, idle and process energy.
type ProcessSample struct {
	PID       int       `json:"pid"`
	Comm      string    `json:"comm"`
	Device    string    `json:"device"`
	Source    string    `json:"source"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	IntervalS float64   `json:"interval_s"`

	// CPUActive is the share of CPU time that was active (steal left out),
	// 0 to 1. ProcessCPU is the process's share of that active time, 0 to 1.
	CPUActive  float64 `json:"cpu_active"`
	ProcessCPU float64 `json:"process_cpu"`

	EnergySplit             // total energy of the device
	Zones       []ZoneSplit `json:"zones,omitempty"`
}

// NewProcessSample splits the energy sample s (total and zones) using the
// host's CPU usage and the process's CPU ticks over the same interval.
func NewProcessSample(s devices.Sample, p proc.ProcessTimes, cpu proc.CPUUsage, processTicks uint64) (ProcessSample, error) {
	ran := cpu.Active + cpu.Idle
	if ran == 0 {
		return ProcessSample{}, ErrNoCPUTime
	}
	activeShare := float64(cpu.Active) / float64(ran)
	processShare := ProcessShare(cpu, processTicks)

	split := func(energyJ float64) EnergySplit {
		activeJ := energyJ * activeShare
		processJ := activeJ * processShare
		out := EnergySplit{
			EnergyJ:  energyJ,
			ActiveJ:  activeJ,
			IdleJ:    energyJ - activeJ,
			ProcessJ: processJ,
		}
		if s.IntervalS > 0 {
			out.ProcessW = processJ / s.IntervalS
		}
		return out
	}

	out := ProcessSample{
		PID:         p.PID,
		Comm:        p.Comm,
		Device:      s.Device,
		Source:      s.Source,
		Start:       s.Start,
		End:         s.End,
		IntervalS:   s.IntervalS,
		CPUActive:   activeShare,
		ProcessCPU:  processShare,
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

// ProcessShare is the process's share of the active CPU time, 0 to 1.
//
// It is capped at 1: the process and host counters are read a moment
// apart, so a process using all CPUs can show slightly more ticks than the
// host did.
func ProcessShare(cpu proc.CPUUsage, processTicks uint64) float64 {
	if cpu.Active == 0 {
		return 0
	}
	if processTicks >= cpu.Active {
		return 1
	}
	return float64(processTicks) / float64(cpu.Active)
}
