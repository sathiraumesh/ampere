package top

import (
	"errors"
	"math"
	"testing"
	"time"

	"sathiraumesh/ampere/internal/devices"
	"sathiraumesh/ampere/internal/devices/cpu/rapl"
	"sathiraumesh/ampere/internal/host/cgroup"
	"sathiraumesh/ampere/internal/host/proc"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// fakeSource returns energy readings (µJ) and zone readings in order.
type fakeSource struct {
	energy []uint64
	zones  [][]rapl.ZoneReading
	err    error
	calls  int
}

func (f *fakeSource) Name() string   { return "fake" }
func (f *fakeSource) Device() string { return devices.CPU }
func (f *fakeSource) Err() error     { return nil }
func (f *fakeSource) Read() (uint64, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.energy[f.calls], nil
}
func (f *fakeSource) ReadZones() ([]rapl.ZoneReading, error) {
	z := f.zones[f.calls]
	f.calls++ // ReadZones is read after Read: one step per call
	return z, nil
}

// world is the fake system: one entry per reading.
type world struct {
	cpus  []proc.CPUTimes
	procs [][]proc.ProcessTimes
	ctrs  []map[string]uint64 // container ID → usage_usec
}

// fakeWorld replaces the system readers; every reading advances 1 s.
func fakeWorld(t *testing.T, w world) {
	t.Helper()
	oldNow, oldCPU, oldProcs, oldList, oldUsage := now, readHostCPU, readAllProcesses, listContainers, readContainerUsage
	t.Cleanup(func() {
		now, readHostCPU, readAllProcesses, listContainers, readContainerUsage = oldNow, oldCPU, oldProcs, oldList, oldUsage
	})

	step := -1
	clock := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	readAllProcesses = func() ([]proc.ProcessTimes, error) {
		step++
		return w.procs[step], nil
	}
	listContainers = func() ([]cgroup.Container, error) {
		var cs []cgroup.Container
		for id := range w.ctrs[step] {
			cs = append(cs, cgroup.Container{ID: id, Runtime: cgroup.Docker, Path: "/cg/" + id})
		}
		return cs, nil
	}
	readContainerUsage = func(path string) (uint64, error) {
		return w.ctrs[step][path[len("/cg/"):]], nil
	}
	readHostCPU = func() (proc.CPUTimes, error) { return w.cpus[step], nil }
	now = func() time.Time { return clock.Add(time.Duration(step) * time.Second) }
}

func zones(pkg, core uint64) []rapl.ZoneReading {
	return []rapl.ZoneReading{
		{ID: "intel-rapl:0", Name: "package-0", EnergyUJ: pkg},
		{ID: "intel-rapl:0:0", Name: "core", ParentID: "intel-rapl:0", EnergyUJ: core},
	}
}

func p(pid int, comm string, ticks, start uint64) proc.ProcessTimes {
	return proc.ProcessTimes{PID: pid, Comm: comm, UTime: ticks, StartTime: start}
}

func ticks(s float64) uint64 { return uint64(s * float64(proc.ClockTicks)) }

func TestSamplerFrame(t *testing.T) {
	// 1 s: 20 J; CPUs 25% active (1 s active, 3 s idle over 4 CPUs).
	// worker used 0.5 s (50% of active), shell 0.25 s; the container 0.25 s.
	src := &fakeSource{
		energy: []uint64{0, 20_000_000},
		zones:  [][]rapl.ZoneReading{zones(0, 0), zones(20_000_000, 8_000_000)},
	}
	fakeWorld(t, world{
		cpus: []proc.CPUTimes{{}, {User: ticks(1), Idle: ticks(3)}},
		procs: [][]proc.ProcessTimes{
			{p(10, "worker", 0, 5), p(11, "shell", 0, 6)},
			{p(10, "worker", ticks(0.5), 5), p(11, "shell", ticks(0.25), 6), p(12, "new", 9, 7)},
		},
		ctrs: []map[string]uint64{{"abc": 1_000_000}, {"abc": 1_250_000}},
	})
	s := newSampler(src)

	if _, ok, err := s.Step(); ok || err != nil {
		t.Fatalf("first step primes: ok %v, err %v", ok, err)
	}
	f, ok, err := s.Step()
	if !ok || err != nil {
		t.Fatalf("second step: ok %v, err %v", ok, err)
	}

	if !near(f.PowerW, 20) || !near(f.ActiveShare, 0.25) || !near(f.TotalJ, 20) || f.IntervalS != 1 {
		t.Errorf("machine: %.3f W, active %.3f, total %.3f J, %g s", f.PowerW, f.ActiveShare, f.TotalJ, f.IntervalS)
	}
	if len(f.Zones) != 2 || f.Zones[0].Name != "package-0" || !near(f.Zones[0].W, 20) ||
		f.Zones[1].Parent != "intel-rapl:0" || !near(f.Zones[1].W, 8) {
		t.Errorf("zones = %+v", f.Zones)
	}

	// active energy 5 J: worker 50% → 2.5 W, shell 25% → 1.25 W.
	// The process that started during the interval is not shown yet.
	if len(f.Processes) != 2 {
		t.Fatalf("processes = %+v", f.Processes)
	}
	w, sh := f.Processes[0], f.Processes[1]
	if w.Comm != "worker" || !near(w.CPU, 0.5) || !near(w.W, 2.5) || !near(w.TotalJ, 2.5) {
		t.Errorf("worker = %+v", w)
	}
	if sh.Comm != "shell" || !near(sh.W, 1.25) {
		t.Errorf("shell = %+v (want second, sorted by power)", sh)
	}
	if len(f.Containers) != 1 || f.Containers[0].Container != "abc" || f.Containers[0].Runtime != "docker" ||
		!near(f.Containers[0].W, 1.25) {
		t.Errorf("containers = %+v", f.Containers)
	}
}

func TestSamplerTotalsAndReuse(t *testing.T) {
	src := &fakeSource{
		energy: []uint64{0, 10_000_000, 20_000_000, 30_000_000},
		zones:  [][]rapl.ZoneReading{nil, nil, nil, nil},
	}
	all := proc.CPUTimes{User: ticks(1)} // all time active
	fakeWorld(t, world{
		cpus: []proc.CPUTimes{{}, all, {User: ticks(2)}, {User: ticks(3)}},
		procs: [][]proc.ProcessTimes{
			{p(10, "a", 0, 5)},
			{p(10, "a", ticks(1), 5)},  // all of the CPU: 10 J
			{p(10, "a", ticks(2), 5)},  // again: total 20 J
			{p(10, "b", ticks(1), 99)}, // PID 10 reused by a new process
		},
		ctrs: []map[string]uint64{{}, {}, {}, {}},
	})
	s := newSampler(src)
	s.Step()

	f1, _, _ := s.Step()
	f2, _, _ := s.Step()
	if !near(f1.Processes[0].TotalJ, 10) || !near(f2.Processes[0].TotalJ, 20) || !near(f2.TotalJ, 20) {
		t.Errorf("totals: %v then %v (machine %v)", f1.Processes, f2.Processes, f2.TotalJ)
	}
	f3, _, _ := s.Step()
	if len(f3.Processes) != 0 {
		t.Errorf("a reused PID is a new process, shown from the next interval: %+v", f3.Processes)
	}
	if len(s.procTotals) != 0 {
		t.Errorf("totals of the exited process should be forgotten: %v", s.procTotals)
	}
}

func TestSamplerWrapAndErrors(t *testing.T) {
	src := &fakeSource{energy: []uint64{500, 100}, zones: [][]rapl.ZoneReading{nil, nil}}
	fakeWorld(t, world{
		cpus:  []proc.CPUTimes{{}, {User: 1, Idle: 1}},
		procs: [][]proc.ProcessTimes{nil, nil},
		ctrs:  []map[string]uint64{{}, {}},
	})
	s := newSampler(src)
	s.Step()
	if _, ok, err := s.Step(); ok || !errors.Is(err, errWrapped) {
		t.Errorf("wrap: ok %v, err %v", ok, err)
	}

	denied := &fakeSource{err: errors.New("permission denied")}
	fakeWorld(t, world{cpus: []proc.CPUTimes{{}}, procs: [][]proc.ProcessTimes{nil}, ctrs: []map[string]uint64{{}}})
	if _, _, err := newSampler(denied).Step(); err == nil {
		t.Error("energy read error should be returned")
	}
}

func TestSamplerContainerRestart(t *testing.T) {
	src := &fakeSource{energy: []uint64{0, 1_000_000}, zones: [][]rapl.ZoneReading{nil, nil}}
	fakeWorld(t, world{
		cpus:  []proc.CPUTimes{{}, {User: ticks(1)}},
		procs: [][]proc.ProcessTimes{nil, nil},
		ctrs:  []map[string]uint64{{"abc": 5_000_000}, {"abc": 100}}, // counter started again
	})
	s := newSampler(src)
	s.Step()
	f, _, _ := s.Step()
	if len(f.Containers) != 0 {
		t.Errorf("a restarted container is skipped for that interval: %+v", f.Containers)
	}
}

func TestSamplerContainerNamesCached(t *testing.T) {
	src := &fakeSource{energy: []uint64{0, 1_000_000, 2_000_000}, zones: [][]rapl.ZoneReading{nil, nil, nil}}
	fakeWorld(t, world{
		cpus:  []proc.CPUTimes{{}, {User: ticks(1)}, {User: ticks(2)}},
		procs: [][]proc.ProcessTimes{nil, nil, nil},
		ctrs:  []map[string]uint64{{"abc": 0}, {"abc": 100}, {"abc": 200}},
	})
	old := containerName
	t.Cleanup(func() { containerName = old })
	lookups := 0
	containerName = func(c cgroup.Container) string { lookups++; return "web" }

	s := newSampler(src)
	s.Step()
	f1, _, _ := s.Step()
	f2, _, _ := s.Step()
	if f1.Containers[0].Name != "web" || f2.Containers[0].Name != "web" {
		t.Errorf("names: %q, %q", f1.Containers[0].Name, f2.Containers[0].Name)
	}
	if lookups != 1 {
		t.Errorf("name looked up %d times, want once", lookups)
	}
}
