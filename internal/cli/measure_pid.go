package cli

import (
	"errors"
	"fmt"
	"os"

	"sathiraumesh/ampere/internal/devices"
	"sathiraumesh/ampere/internal/host/attribution"
	"sathiraumesh/ampere/internal/host/proc"
)

// readHostCPU and readProcess are variables so tests can use fake /proc data.
var (
	readHostCPU = proc.ReadCPU
	readProcess = proc.ReadProcess
)

// snapshot is everything "measure" reads at one moment: the energy
// counters and, with --pid, the host's and the process's CPU time.
type snapshot struct {
	energy  reading
	cpu     proc.CPUTimes     // only with --pid
	process proc.ProcessTimes // only with --pid
}

// takeSnapshot reads the energy counters and, if pid > 0, the process's
// and host's CPU time, as close together as possible. pid 0 means no
// process: /proc is not read at all.
func takeSnapshot(pid int, src devices.Source, zr devices.ZoneReader) (snapshot, error) {
	var s snapshot
	if pid > 0 {
		p, err := readProcess(pid)
		if errors.Is(err, os.ErrNotExist) {
			return snapshot{}, fmt.Errorf("process %d not found (exited?)", pid)
		}
		if err != nil {
			return snapshot{}, fmt.Errorf("process %d: %w", pid, err)
		}
		c, err := readHostCPU()
		if err != nil {
			return snapshot{}, fmt.Errorf("host CPU time: %w", err)
		}
		s.process, s.cpu = p, c
	}
	e, err := takeReading(src, zr)
	if err != nil {
		return snapshot{}, fmt.Errorf("%s: %w (energy is root-only: try sudo)", src.Name(), err)
	}
	s.energy = e
	return s, nil
}

// makePidSample turns two snapshots into the process's energy sample.
func makePidSample(src devices.Source, withZones bool, a, b snapshot) (attribution.ProcessSample, error) {
	energy, err := makeSample(src, withZones, a.energy, b.energy)
	if err != nil {
		return attribution.ProcessSample{}, fmt.Errorf("%s: %w", src.Name(), err)
	}
	ticks, err := b.process.Usage(a.process)
	if err != nil {
		return attribution.ProcessSample{}, err
	}
	return attribution.NewProcessSample(energy, b.process, b.cpu.Usage(a.cpu), ticks)
}
