package top

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"sathiraumesh/ampere/internal/devices"
	"sathiraumesh/ampere/internal/devices/cpu/rapl"
	"sathiraumesh/ampere/internal/host/attribution"
	"sathiraumesh/ampere/internal/host/cgroup"
	"sathiraumesh/ampere/internal/host/proc"
	"sathiraumesh/ampere/internal/tui"
)

// These read the system; they are variables so tests can use fake data.
var (
	now                = time.Now
	readHostCPU        = proc.ReadCPU
	readAllProcesses   = proc.ReadAllProcesses
	listContainers     = cgroup.ListContainers
	readContainerUsage = cgroup.ReadUsage
	containerName      = cgroup.Name
)

// errWrapped means the energy counter wrapped to 0 during the interval, so
// that interval has no usable energy and is skipped.
var errWrapped = errors.New("energy counter wrapped during the interval")

// procKey identifies a process across readings: a PID can be reused by a
// new process, which then has a different start time.
type procKey struct {
	pid   int
	start uint64
}

// reading is everything read at one moment.
type reading struct {
	at       time.Time
	energyUJ uint64
	zones    []rapl.ZoneReading
	cpu      proc.CPUTimes
	procs    map[procKey]proc.ProcessTimes
	ctrs     map[string]ctrReading // by container ID
}

type ctrReading struct {
	c       cgroup.Container
	usageUS uint64
}

// sampler takes a reading every interval and turns two readings into a
// Frame. It keeps each process's and container's energy since start.
type sampler struct {
	src        devices.Source
	zr         rapl.ZoneReader // nil if the source has no zones
	prev       *reading
	machineJ   float64
	procTotals map[procKey]float64
	ctrTotals  map[string]float64
	ctrNames   map[string]string // by container ID: looked up once
}

func newSampler(src devices.Source) *sampler {
	zr, _ := src.(rapl.ZoneReader)
	return &sampler{
		src:        src,
		zr:         zr,
		procTotals: map[procKey]float64{},
		ctrTotals:  map[string]float64{},
		ctrNames:   map[string]string{},
	}
}

// Step takes a reading; it implements tui.Sampler. The first call only
// primes the sampler and returns ok false; every later call returns the
// frame for the interval since the previous call.
func (s *sampler) Step() (f tui.Frame, ok bool, err error) {
	cur, err := s.read()
	if err != nil {
		return tui.Frame{}, false, err
	}
	prev := s.prev
	s.prev = cur
	if prev == nil {
		return tui.Frame{}, false, nil
	}
	f, err = s.frame(prev, cur)
	if err != nil {
		return tui.Frame{}, false, err
	}
	return f, true, nil
}

// read takes one reading: processes and containers first, then host CPU,
// then energy, as close together as possible.
func (s *sampler) read() (*reading, error) {
	r := &reading{procs: map[procKey]proc.ProcessTimes{}, ctrs: map[string]ctrReading{}}

	ps, err := readAllProcesses()
	if err != nil {
		return nil, fmt.Errorf("processes: %w", err)
	}
	for _, p := range ps {
		r.procs[procKey{p.PID, p.StartTime}] = p
	}

	// Containers are optional: without cgroup v2 the containers tab is empty.
	if cs, err := listContainers(); err == nil {
		for _, c := range cs {
			if u, err := readContainerUsage(c.Path); err == nil {
				r.ctrs[c.ID] = ctrReading{c: c, usageUS: u}
			}
		}
	}

	if r.cpu, err = readHostCPU(); err != nil {
		return nil, fmt.Errorf("host CPU time: %w", err)
	}

	r.at = now()
	if r.energyUJ, err = s.src.Read(); err != nil {
		return nil, fmt.Errorf("%s: %w", s.src.Name(), err)
	}
	if s.zr != nil {
		if r.zones, err = s.zr.ReadZones(); err != nil {
			return nil, fmt.Errorf("%s: %w", s.src.Name(), err)
		}
	}
	return r, nil
}

// frame turns two readings into a tui.Frame and adds this interval's energy to
// the running totals.
func (s *sampler) frame(a, b *reading) (tui.Frame, error) {
	if b.energyUJ < a.energyUJ {
		return tui.Frame{}, errWrapped
	}
	dt := b.at.Sub(a.at).Seconds()
	if dt <= 0 {
		return tui.Frame{}, fmt.Errorf("no time passed between readings")
	}
	energyJ := float64(b.energyUJ-a.energyUJ) / 1e6
	cpu := b.cpu.Usage(a.cpu)
	ran := cpu.Active + cpu.Idle
	if ran == 0 {
		return tui.Frame{}, attribution.ErrNoCPUTime
	}
	activeShare := float64(cpu.Active) / float64(ran)
	activeJ := energyJ * activeShare

	s.machineJ += energyJ
	f := tui.Frame{
		At:          b.at,
		IntervalS:   dt,
		Device:      s.src.Device(),
		Source:      s.src.Name(),
		PowerW:      energyJ / dt,
		ActiveShare: activeShare,
		TotalJ:      s.machineJ,
	}

	if s.zr != nil {
		zs, err := rapl.ZoneSamples(a.zones, b.zones, dt)
		if err != nil {
			return tui.Frame{}, err
		}
		for _, z := range zs {
			f.Zones = append(f.Zones, tui.ZoneRow{ID: z.ID, Name: z.Name, Parent: z.Parent, W: z.PowerW})
		}
	}

	// share gives a target its part of the active energy, from its CPU seconds.
	share := func(cpuSeconds float64) (cpuShare, w, j float64) {
		cpuShare = attribution.Share(cpu, cpuSeconds)
		j = activeJ * cpuShare
		return cpuShare, j / dt, j
	}

	for key, p := range b.procs {
		before, ok := a.procs[key]
		if !ok {
			continue // started during the interval: shown from the next one
		}
		ticks, err := p.Usage(before)
		if err != nil {
			continue
		}
		c, w, j := share(proc.Seconds(ticks))
		s.procTotals[key] += j
		f.Processes = append(f.Processes, tui.Row{
			PID: p.PID, Comm: p.Comm, CPU: c, W: w, TotalJ: s.procTotals[key],
		})
	}
	for id, cur := range b.ctrs {
		before, ok := a.ctrs[id]
		if !ok || cur.usageUS < before.usageUS {
			continue // new, or restarted (its counter started again)
		}
		c, w, j := share(float64(cur.usageUS-before.usageUS) / 1e6)
		s.ctrTotals[id] += j
		f.Containers = append(f.Containers, tui.Row{
			Container: id, Name: s.name(cur.c), Runtime: cur.c.Runtime,
			CPU: c, W: w, TotalJ: s.ctrTotals[id],
		})
	}

	// Forget totals of processes and containers that are gone.
	for key := range s.procTotals {
		if _, ok := b.procs[key]; !ok {
			delete(s.procTotals, key)
		}
	}
	for id := range s.ctrTotals {
		if _, ok := b.ctrs[id]; !ok {
			delete(s.ctrTotals, id)
			delete(s.ctrNames, id)
		}
	}

	sortRows(f.Processes)
	sortRows(f.Containers)
	return f, nil
}

// name returns the container's name, looking it up only the first time.
func (s *sampler) name(c cgroup.Container) string {
	n, ok := s.ctrNames[c.ID]
	if !ok {
		n = containerName(c)
		s.ctrNames[c.ID] = n
	}
	return n
}

// sortRows sorts by power, highest first; ties by total energy, then by
// PID or container ID so the order is stable.
func sortRows(rows []tui.Row) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.W != b.W {
			return a.W > b.W
		}
		if a.TotalJ != b.TotalJ {
			return a.TotalJ > b.TotalJ
		}
		if a.PID != b.PID {
			return a.PID < b.PID
		}
		return a.Container < b.Container
	})
}
