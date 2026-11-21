package measure

import (
	"errors"
	"fmt"
	"io/fs"

	"sathiraumesh/ampere/internal/devices"
	"sathiraumesh/ampere/internal/host/attribution"
	"sathiraumesh/ampere/internal/host/cgroup"
	"sathiraumesh/ampere/internal/host/proc"
)

// These read /proc and /sys/fs/cgroup; they are variables so tests can use
// fake data.
var (
	readHostCPU        = proc.ReadCPU
	readProcess        = proc.ReadProcess
	findContainer      = cgroup.FindContainer
	readContainerUsage = cgroup.ReadUsage
)

// target is what "measure process" or "measure container" measures.
// Exactly one of pid and container is set.
type target struct {
	pid       int
	container *cgroup.Container
}

// snapshot is everything "measure" reads at one moment: the energy
// counters and, with a target, the host's CPU time and the target's.
type snapshot struct {
	energy     reading
	cpu        proc.CPUTimes     // only with a target
	process    proc.ProcessTimes // only for a process
	ctrUsageUS uint64            // only for a container: cgroup usage_usec
}

// takeSnapshot reads the target's CPU time (if t is not nil), the host's
// CPU time and the energy counters, as close together as possible. With
// no target, /proc and /sys/fs/cgroup are not read at all.
func takeSnapshot(t *target, src devices.Source, zr devices.ZoneReader) (snapshot, error) {
	var s snapshot
	if t != nil {
		if err := t.read(&s); err != nil {
			return snapshot{}, err
		}
		c, err := readHostCPU()
		if err != nil {
			return snapshot{}, fmt.Errorf("host CPU time: %w", err)
		}
		s.cpu = c
	}
	e, err := takeReading(src, zr)
	if err != nil {
		return snapshot{}, fmt.Errorf("%s: %w (energy is root-only: try sudo)", src.Name(), err)
	}
	s.energy = e
	return s, nil
}

// read stores the target's current CPU counter in s.
func (t *target) read(s *snapshot) error {
	if t.container != nil {
		u, err := readContainerUsage(t.container.Path)
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%s container %s stopped", t.container.Runtime, t.container.ID[:12])
		}
		if err != nil {
			return fmt.Errorf("%s container %s: %w", t.container.Runtime, t.container.ID[:12], err)
		}
		s.ctrUsageUS = u
		return nil
	}
	p, err := readProcess(t.pid)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("process %d not found (exited?)", t.pid)
	}
	if err != nil {
		return fmt.Errorf("process %d: %w", t.pid, err)
	}
	s.process = p
	return nil
}

// makeTargetSample turns two snapshots into the target's energy sample.
func makeTargetSample(src devices.Source, withZones bool, t *target, a, b snapshot) (attribution.Sample, error) {
	energy, err := makeSample(src, withZones, a.energy, b.energy)
	if err != nil {
		return attribution.Sample{}, fmt.Errorf("%s: %w", src.Name(), err)
	}

	var who attribution.Target
	var cpuSeconds float64
	if t.container != nil {
		who = attribution.Target{Container: t.container.ID, Runtime: t.container.Runtime}
		if b.ctrUsageUS > a.ctrUsageUS {
			cpuSeconds = float64(b.ctrUsageUS-a.ctrUsageUS) / 1e6 // µs → s
		}
	} else {
		ticks, err := b.process.Usage(a.process)
		if err != nil {
			return attribution.Sample{}, err
		}
		who = attribution.Target{PID: b.process.PID, Comm: b.process.Comm}
		cpuSeconds = proc.Seconds(ticks)
	}
	return attribution.NewSample(energy, who, b.cpu.Usage(a.cpu), cpuSeconds)
}
