package cli

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sathiraumesh/ampere/internal/host/attribution"
	"sathiraumesh/ampere/internal/host/proc"
)

// fakeProcReads replaces readHostCPU and readProcess with readings returned
// in order, one per call.
func fakeProcReads(t *testing.T, cpus []proc.CPUTimes, procs []proc.ProcessTimes) {
	t.Helper()
	oldCPU, oldProc := readHostCPU, readProcess
	t.Cleanup(func() { readHostCPU, readProcess = oldCPU, oldProc })

	ci, pi := 0, 0
	readHostCPU = func() (proc.CPUTimes, error) {
		c := cpus[ci]
		ci++
		return c, nil
	}
	readProcess = func(pid int) (proc.ProcessTimes, error) {
		p := procs[pi]
		pi++
		p.PID = pid
		return p, nil
	}
}

// worker is a process that used ut ticks so far.
func worker(ut uint64) proc.ProcessTimes {
	return proc.ProcessTimes{Comm: "worker", UTime: ut, StartTime: 500}
}

// pidSample runs "ampere measure --pid <pid> <args> -o json" and decodes the sample.
func pidSample(t *testing.T, pid string, args ...string) attribution.ProcessSample {
	t.Helper()
	out := run(t, append(append([]string{"measure", "--pid", pid}, args...), "-o", "json")...)
	var s attribution.ProcessSample
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	return s
}

func close(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestMeasurePidSplitsEnergy(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 20_000_000}}) // 20 J
	// 100 active + 300 idle ticks; the process used 25 of the active ones.
	fakeProcReads(t,
		[]proc.CPUTimes{{User: 1000, Idle: 5000}, {User: 1100, Idle: 5300}},
		[]proc.ProcessTimes{worker(40), worker(65)},
	)

	got := pidSample(t, "1234")

	if got.PID != 1234 || got.Comm != "worker" || got.Device != "cpu" || got.Source != "fake" {
		t.Errorf("header = %+v", got)
	}
	if !close(got.CPUActive, 0.25) || !close(got.ProcessCPU, 0.25) {
		t.Errorf("cpu_active %g, process_cpu %g; want 0.25, 0.25", got.CPUActive, got.ProcessCPU)
	}
	// 20 J: 5 active, 15 idle, process 1.25 J over 1 s
	if !close(got.EnergyJ, 20) || !close(got.ActiveJ, 5) || !close(got.IdleJ, 15) ||
		!close(got.ProcessJ, 1.25) || !close(got.ProcessW, 1.25) {
		t.Errorf("split = %+v, want 20 / 5 / 15 / 1.25 J / 1.25 W", got.EnergySplit)
	}
}

func TestMeasurePidZones(t *testing.T) {
	fakeClock(t)
	withSources(t, laptopZones()) // package 12 J, core 5 J
	fakeProcReads(t,
		[]proc.CPUTimes{{User: 0, Idle: 0}, {User: 50, Idle: 50}}, // half active
		[]proc.ProcessTimes{worker(0), worker(10)},                // 1/5 of active
	)

	got := pidSample(t, "7")

	if len(got.Zones) != 2 {
		t.Fatalf("zones = %+v", got.Zones)
	}
	pkg, core := got.Zones[0], got.Zones[1]
	if pkg.Name != "package-0" || !close(pkg.ActiveJ, 6) || !close(pkg.IdleJ, 6) || !close(pkg.ProcessJ, 1.2) {
		t.Errorf("package-0 = %+v, want 6 active, 6 idle, 1.2 process", pkg)
	}
	if core.Name != "core" || core.Parent != "intel-rapl:0" ||
		!close(core.ActiveJ, 2.5) || !close(core.IdleJ, 2.5) || !close(core.ProcessJ, 0.5) {
		t.Errorf("core = %+v, want 2.5 active, 2.5 idle, 0.5 process", core)
	}
}

func TestMeasurePidTable(t *testing.T) {
	fakeClock(t)
	withSources(t, laptopZones())
	fakeProcReads(t,
		[]proc.CPUTimes{{}, {User: 50, Idle: 50}},
		[]proc.ProcessTimes{worker(0), worker(10)},
	)

	out := run(t, "measure", "--pid", "7")

	parts := strings.Split(strings.TrimSpace(out), "\n\n")
	if len(parts) != 2 {
		t.Fatalf("want total table and zones table:\n%s", out)
	}
	assertContains(t, parts[0], "PID", "COMM", "CPU ACTIVE", "ACTIVE (J)", "IDLE (J)", "PID (J)",
		"7", "worker", "50.0%", "20.0%", "12.000000", "6.000000", "1.200000")
	assertContains(t, parts[1], "ZONE", "ACTIVE (J)", "IDLE (J)", "PID (J)",
		"package-0", "core", "2.500000", "0.500000")
}

func TestMeasurePidCount(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 10_000_000, 30_000_000}})
	fakeProcReads(t,
		[]proc.CPUTimes{{}, {User: 100, Idle: 100}, {User: 300, Idle: 100}},
		[]proc.ProcessTimes{worker(0), worker(50), worker(250)},
	)

	out := run(t, "measure", "--pid", "9", "-n", "2", "-o", "json")

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 JSON lines:\n%s", out)
	}
	var second attribution.ProcessSample
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatal(err)
	}
	// second interval: 20 J, all active, process used all of it
	if !close(second.EnergyJ, 20) || !close(second.ActiveJ, 20) || !close(second.ProcessJ, 20) {
		t.Errorf("second sample = %+v", second.EnergySplit)
	}
}

func TestMeasurePidToFile(t *testing.T) {
	fakeClock(t)
	path := filepath.Join(t.TempDir(), "pid.txt")
	withSources(t, &counterSource{readings: []uint64{0, 1_000_000, 2_000_000}})
	fakeProcReads(t,
		[]proc.CPUTimes{{}, {User: 1, Idle: 1}, {User: 2, Idle: 2}},
		[]proc.ProcessTimes{worker(0), worker(1), worker(2)},
	)

	if out := run(t, "measure", "--pid", "5", "-n", "2", "-f", path); out != "" {
		t.Errorf("with --file nothing should be printed, got %q", out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "COMM"); n != 1 {
		t.Errorf("header written %d times, want 1:\n%s", n, data)
	}
	if n := strings.Count(string(data), "worker"); n != 2 {
		t.Errorf("want 2 rows:\n%s", data)
	}
}

func TestMeasurePidBadArgs(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1}})

	for _, args := range [][]string{
		{"measure", "--pid"},
		{"measure", "--pid", "1", "2"},
		{"measure", "--pid", "abc"},
		{"measure", "--pid", "0"},
		{"measure", "--pid", "-5"},
		{"measure", "--pid", "1", "-n", "0"},
		{"measure", "--pid", "1", "-i", "0s"},
		{"measure", "--pid", "1", "-o", "xml"},
	} {
		if _, err := runErr(args...); err == nil {
			t.Errorf("ampere %v: want error", args)
		}
	}
}

func TestMeasurePidProcessGone(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1}})
	old := readProcess
	t.Cleanup(func() { readProcess = old })
	readProcess = func(int) (proc.ProcessTimes, error) { return proc.ProcessTimes{}, os.ErrNotExist }

	_, err := runErr("measure", "--pid", "4242")
	if err == nil || !strings.Contains(err.Error(), "process 4242 not found") {
		t.Errorf("err = %v, want not found", err)
	}
}

func TestMeasurePidReused(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1_000_000}})
	fakeProcReads(t,
		[]proc.CPUTimes{{}, {User: 1, Idle: 1}},
		[]proc.ProcessTimes{worker(0), {Comm: "other", StartTime: 999}},
	)

	_, err := runErr("measure", "--pid", "3")
	if err == nil || !strings.Contains(err.Error(), "reused") {
		t.Errorf("err = %v, want PID reused", err)
	}
}

func TestMeasurePidNoCPUTime(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1_000_000}})
	fakeProcReads(t,
		[]proc.CPUTimes{{User: 5}, {User: 5}},
		[]proc.ProcessTimes{worker(0), worker(0)},
	)

	_, err := runErr("measure", "--pid", "3")
	if !errors.Is(err, attribution.ErrNoCPUTime) {
		t.Errorf("err = %v, want ErrNoCPUTime", err)
	}
}

func TestMeasurePidEnergyPermission(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readErr: errors.New("permission denied")})
	fakeProcReads(t, []proc.CPUTimes{{}}, []proc.ProcessTimes{worker(0)})

	_, err := runErr("measure", "--pid", "3")
	if err == nil || !strings.Contains(err.Error(), "sudo") {
		t.Errorf("err = %v, want sudo hint", err)
	}
}

func TestMeasureWithoutPidSkipsProc(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1_000_000}})
	old1, old2 := readHostCPU, readProcess
	t.Cleanup(func() { readHostCPU, readProcess = old1, old2 })
	readHostCPU = func() (proc.CPUTimes, error) { t.Fatal("read /proc/stat without --pid"); return proc.CPUTimes{}, nil }
	readProcess = func(int) (proc.ProcessTimes, error) {
		t.Fatal("read /proc/<pid> without --pid")
		return proc.ProcessTimes{}, nil
	}

	if out := run(t, "measure"); strings.Contains(out, "PID") {
		t.Errorf("without --pid, no process columns:\n%s", out)
	}
}

func TestMeasurePidShortFlag(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1_000_000}})
	fakeProcReads(t, []proc.CPUTimes{{}, {User: 1, Idle: 1}}, []proc.ProcessTimes{worker(0), worker(1)})

	out := run(t, "measure", "-p", "8", "-o", "json")
	if !strings.Contains(out, `"pid":8`) {
		t.Errorf("-p should work like --pid: %s", out)
	}
}
