package measure

import (
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"sathiraumesh/ampere/internal/devices/cpu/rapl"
	"sathiraumesh/ampere/internal/host/attribution"
	"sathiraumesh/ampere/internal/host/cgroup"
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

// pidSample runs "ampere measure process <pid> <args> -o json" and decodes the sample.
func pidSample(t *testing.T, pid string, args ...string) attribution.Sample {
	t.Helper()
	out := run(t, append(append([]string{"measure", "process", pid}, args...), "-o", "json")...)
	var s attribution.Sample
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	return s
}

func close(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestMeasureProcessSplitsEnergy(t *testing.T) {
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
	if !close(got.TargetCPU, 0.25) {
		t.Errorf("target_cpu %g, want 0.25", got.TargetCPU)
	}
	// 20 J, 5 J of it active: process 1.25 J over 1 s
	if !close(got.EnergyJ, 20) ||
		!close(got.TargetJ, 1.25) || !close(got.TargetW, 1.25) {
		t.Errorf("split = %+v, want 20 / 5 / 15 / 1.25 J / 1.25 W", got.EnergySplit)
	}
}

func TestMeasureProcessZones(t *testing.T) {
	fakeClock(t)
	withSources(t, singlePackageZones()) // package 12 J, core 5 J
	fakeProcReads(t,
		[]proc.CPUTimes{{User: 0, Idle: 0}, {User: 50, Idle: 50}}, // half active
		[]proc.ProcessTimes{worker(0), worker(10)},                // 1/5 of active
	)

	got := pidSample(t, "7")

	if len(got.Zones) != 2 {
		t.Fatalf("zones = %+v", got.Zones)
	}
	pkg, core := got.Zones[0], got.Zones[1]
	if pkg.Name != "package-0" || !close(pkg.EnergyJ, 12) || !close(pkg.TargetJ, 1.2) {
		t.Errorf("package-0 = %+v, want 12 J, 1.2 process", pkg)
	}
	if core.Name != "core" || core.Parent != "intel-rapl:0" ||
		!close(core.EnergyJ, 5) || !close(core.TargetJ, 0.5) {
		t.Errorf("core = %+v, want 5 J, 0.5 process", core)
	}
}

func TestMeasureProcessTable(t *testing.T) {
	fakeClock(t)
	withSources(t, singlePackageZones())
	fakeProcReads(t,
		[]proc.CPUTimes{{}, {User: 50, Idle: 50}},
		[]proc.ProcessTimes{worker(0), worker(10)},
	)

	out := run(t, "measure", "process", "7")

	want := [][]string{
		{"PID", "COMM", "INTERVAL", "(s)", "PID", "CPU", "ZONE", "NAME", "PARENT", "ENERGY", "(J)", "PID", "(J)", "PID", "(W)"},
		{"7", "worker", "1.000000", "20.0%", "total", "-", "-", "12.000000", "1.200000", "1.2000"},
		{"7", "worker", "1.000000", "20.0%", "intel-rapl:0", "package-0", "-", "12.000000", "1.200000", "1.2000"},
		{"7", "worker", "1.000000", "20.0%", "intel-rapl:0:0", "core", "intel-rapl:0", "5.000000", "0.500000", "0.5000"},
	}
	if got := tableRows(out); !reflect.DeepEqual(got, want) {
		t.Errorf("table:\n%s\ngot  %q\nwant %q", out, got, want)
	}
	assertNoMachineColumns(t, out)
}

func TestMeasureProcessCount(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 10_000_000, 30_000_000}})
	fakeProcReads(t,
		[]proc.CPUTimes{{}, {User: 100, Idle: 100}, {User: 300, Idle: 100}},
		[]proc.ProcessTimes{worker(0), worker(50), worker(250)},
	)

	out := run(t, "measure", "process", "9", "-n", "2", "-o", "json")

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 JSON lines:\n%s", out)
	}
	var second attribution.Sample
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatal(err)
	}
	// second interval: 20 J, all active, process used all of it
	if !close(second.EnergyJ, 20) || !close(second.TargetJ, 20) {
		t.Errorf("second sample = %+v", second.EnergySplit)
	}
}

func TestMeasureProcessToFile(t *testing.T) {
	fakeClock(t)
	path := filepath.Join(t.TempDir(), "pid.txt")
	withSources(t, &counterSource{readings: []uint64{0, 1_000_000, 2_000_000}})
	fakeProcReads(t,
		[]proc.CPUTimes{{}, {User: 1, Idle: 1}, {User: 2, Idle: 2}},
		[]proc.ProcessTimes{worker(0), worker(1), worker(2)},
	)

	if out := run(t, "measure", "process", "5", "-n", "2", "-f", path); out != "" {
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

func TestMeasureProcessBadArgs(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1}})

	for _, args := range [][]string{
		{"measure", "process"},
		{"measure", "process", "1", "2"},
		{"measure", "process", "abc"},
		{"measure", "process", "0"},
		{"measure", "process", "-5"},
		{"measure", "process", "1", "-n", "0"},
		{"measure", "process", "1", "-i", "0s"},
		{"measure", "process", "1", "-o", "xml"},
	} {
		if _, err := runErr(args...); err == nil {
			t.Errorf("ampere %v: want error", args)
		}
	}
}

func TestMeasureProcessProcessGone(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1}})
	old := readProcess
	t.Cleanup(func() { readProcess = old })
	readProcess = func(int) (proc.ProcessTimes, error) { return proc.ProcessTimes{}, os.ErrNotExist }

	_, err := runErr("measure", "process", "4242")
	if err == nil || !strings.Contains(err.Error(), "process 4242 not found") {
		t.Errorf("err = %v, want not found", err)
	}
}

func TestMeasureProcessReused(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1_000_000}})
	fakeProcReads(t,
		[]proc.CPUTimes{{}, {User: 1, Idle: 1}},
		[]proc.ProcessTimes{worker(0), {Comm: "other", StartTime: 999}},
	)

	_, err := runErr("measure", "process", "3")
	if err == nil || !strings.Contains(err.Error(), "reused") {
		t.Errorf("err = %v, want PID reused", err)
	}
}

func TestMeasureProcessNoCPUTime(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1_000_000}})
	fakeProcReads(t,
		[]proc.CPUTimes{{User: 5}, {User: 5}},
		[]proc.ProcessTimes{worker(0), worker(0)},
	)

	_, err := runErr("measure", "process", "3")
	if !errors.Is(err, attribution.ErrNoCPUTime) {
		t.Errorf("err = %v, want ErrNoCPUTime", err)
	}
}

func TestMeasureProcessEnergyPermission(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readErr: errors.New("permission denied")})
	fakeProcReads(t, []proc.CPUTimes{{}}, []proc.ProcessTimes{worker(0)})

	_, err := runErr("measure", "process", "3")
	if err == nil || !strings.Contains(err.Error(), "sudo") {
		t.Errorf("err = %v, want sudo hint", err)
	}
}

func TestMeasureDeviceSkipsProc(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1_000_000}})
	old1, old2 := readHostCPU, readProcess
	t.Cleanup(func() { readHostCPU, readProcess = old1, old2 })
	readHostCPU = func() (proc.CPUTimes, error) {
		t.Fatal("read /proc/stat for a device measurement")
		return proc.CPUTimes{}, nil
	}
	readProcess = func(int) (proc.ProcessTimes, error) {
		t.Fatal("read /proc/<pid> for a device measurement")
		return proc.ProcessTimes{}, nil
	}

	if out := run(t, "measure"); strings.Contains(out, "PID") {
		t.Errorf("plain measure: no process columns:\n%s", out)
	}
}

// fakeContainer replaces findContainer and readContainerUsage: id is found
// as a docker container whose usage_usec readings are returned in order.
func fakeContainer(t *testing.T, usages ...uint64) cgroup.Container {
	t.Helper()
	oldFind, oldRead := findContainer, readContainerUsage
	t.Cleanup(func() { findContainer, readContainerUsage = oldFind, oldRead })

	c := cgroup.Container{
		ID:      "44f86868889bf4ebf9db3fe8866e7f992a5625c69cccc827c728b81c10f10565",
		Runtime: cgroup.Docker,
		Path:    "/fake/system.slice/docker-44f8.scope",
	}
	findContainer = func(id string) (cgroup.Container, error) {
		if !strings.HasPrefix(c.ID, id) {
			return cgroup.Container{}, errors.New("no running container " + id)
		}
		return c, nil
	}
	i := 0
	readContainerUsage = func(path string) (uint64, error) {
		if path != c.Path {
			t.Errorf("read usage of %s, want %s", path, c.Path)
		}
		u := usages[i]
		i++
		return u, nil
	}
	return c
}

// hostTicks is a host CPU reading with the given active and idle seconds.
func hostTicks(activeS, idleS float64) proc.CPUTimes {
	hz := float64(proc.ClockTicks)
	return proc.CPUTimes{User: uint64(activeS * hz), Idle: uint64(idleS * hz)}
}

func TestMeasureContainer(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 20_000_000}}) // 20 J
	c := fakeContainer(t, 5_000_000, 5_500_000)                       // +0.5 s CPU
	// 2 s active, 6 s idle: 25% active; the container used 0.5 of the 2 s.
	fakeProcReads(t, []proc.CPUTimes{hostTicks(10, 30), hostTicks(12, 36)}, nil)

	out := run(t, "measure", "container", "44f868", "-o", "json")

	var got attribution.Sample
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got.Container != c.ID || got.Runtime != "docker" || got.PID != 0 {
		t.Errorf("target = %+v", got.Target)
	}
	if !close(got.TargetCPU, 0.25) {
		t.Errorf("target_cpu %g, want 0.25", got.TargetCPU)
	}
	if !close(got.EnergyJ, 20) || !close(got.TargetJ, 1.25) {
		t.Errorf("split = %+v, want 20 J, 1.25 target", got.EnergySplit)
	}
	for _, key := range []string{`"container"`, `"runtime"`, `"target_j"`} {
		if !strings.Contains(out, key) {
			t.Errorf("JSON missing %s: %s", key, out)
		}
	}
	if strings.Contains(out, `"pid"`) || strings.Contains(out, `"comm"`) {
		t.Errorf("container JSON should not have pid/comm: %s", out)
	}
}

func TestMeasureContainerTable(t *testing.T) {
	fakeClock(t)
	withSources(t, singlePackageZones())
	fakeContainer(t, 0, 500_000)
	fakeProcReads(t, []proc.CPUTimes{hostTicks(0, 0), hostTicks(1, 1)}, nil)

	out := run(t, "measure", "container", "44f86868889b")

	rows := tableRows(out)
	if len(rows) != 4 {
		t.Fatalf("want header + total + 2 zones:\n%s", out)
	}
	if h := strings.Join(rows[0], " "); h != "CONTAINER RUNTIME INTERVAL (s) CTR CPU ZONE NAME PARENT ENERGY (J) CTR (J) CTR (W)" {
		t.Errorf("header = %q", h)
	}
	for _, r := range rows[1:] {
		if r[0] != "44f86868889b" || r[1] != "docker" || r[3] != "50.0%" {
			t.Errorf("row %q: want short ID, docker, 50.0%%", r)
		}
	}
	if rows[1][4] != "total" || rows[2][5] != "package-0" || rows[3][5] != "core" {
		t.Errorf("rows = %q", rows)
	}
	assertNoMachineColumns(t, out)
	if strings.Contains(out, "44f86868889bf") {
		t.Errorf("table should show the 12-character short ID:\n%s", out)
	}
}

func TestMeasureContainerNotFound(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1}})
	fakeContainer(t)

	_, err := runErr("measure", "container", "ffff")
	if err == nil || !strings.Contains(err.Error(), "no running container") {
		t.Errorf("err = %v, want not found", err)
	}
}

func TestMeasureContainerStopped(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1_000_000}})
	fakeContainer(t, 0)
	readContainerUsage = func(string) (uint64, error) { return 0, fs.ErrNotExist }
	fakeProcReads(t, []proc.CPUTimes{{}}, nil)

	_, err := runErr("measure", "container", "44f8")
	if err == nil || !strings.Contains(err.Error(), "docker container 44f86868889b stopped") {
		t.Errorf("err = %v, want stopped", err)
	}
}

func TestMeasureContainerBadArgs(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1}})
	fakeContainer(t)
	findContainer = func(string) (cgroup.Container, error) {
		t.Error("bad flags must fail before searching the cgroup tree")
		return cgroup.Container{}, nil
	}

	for _, args := range [][]string{
		{"measure", "container"},
		{"measure", "container", "a", "b"},
		{"measure", "container", "44f8", "-n", "0"},
		{"measure", "container", "44f8", "-o", "xml"},
	} {
		if _, err := runErr(args...); err == nil {
			t.Errorf("ampere %v: want error", args)
		}
	}
}

func TestMeasureOldFlagsGone(t *testing.T) {
	for _, flag := range []string{"--pid", "-p", "--container", "-c"} {
		if _, err := runErr("measure", flag, "1"); err == nil {
			t.Errorf("measure %s should be an unknown flag", flag)
		}
	}
}

func TestMeasureFlagsBeforeSubcommand(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1_000_000}})
	fakeProcReads(t, []proc.CPUTimes{{}, {User: 1, Idle: 1}}, []proc.ProcessTimes{worker(0), worker(1)})

	out := run(t, "measure", "-o", "json", "process", "8")
	if !strings.Contains(out, `"pid":8`) {
		t.Errorf("persistent flags before the subcommand should work: %s", out)
	}
}

// assertNoMachineColumns checks that machine-wide active/idle values are
// not shown as if they were the target's.
func assertNoMachineColumns(t *testing.T, out string) {
	t.Helper()
	for _, col := range []string{"CPU ACTIVE", "ACTIVE (J)", "IDLE (J)", "active_j", "idle_j", "cpu_active"} {
		if strings.Contains(out, col) {
			t.Errorf("target output must not have %q:\n%s", col, out)
		}
	}
}

func TestMeasureTargetJSONHasNoMachineFields(t *testing.T) {
	fakeClock(t)
	withSources(t, singlePackageZones())
	fakeProcReads(t, []proc.CPUTimes{{}, {User: 50, Idle: 50}}, []proc.ProcessTimes{worker(0), worker(10)})

	assertNoMachineColumns(t, run(t, "measure", "process", "7", "-o", "json"))
}

// twoSecondZones is singlePackageZones with a third reading, for -n 2:
// second 1 uses 12 J, second 2 uses 8 J (package-0 = total each time).
func twoSecondZones() *zoneSource {
	z := singlePackageZones()
	z.readings = append(z.readings, 20_000_000)
	z.zoneReadings = append(z.zoneReadings, []rapl.ZoneReading{
		{ID: "intel-rapl:0", Name: "package-0", EnergyUJ: 20_000_000},
		{ID: "intel-rapl:0:0", Name: "core", ParentID: "intel-rapl:0", EnergyUJ: 7_000_000},
	})
	return z
}

// tableRows returns the table's rows (header first) as their fields.
func tableRows(out string) [][]string {
	var rows [][]string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		rows = append(rows, strings.Fields(l))
	}
	return rows
}

func TestMeasureSamplesShareOneHeader(t *testing.T) {
	fakeClock(t)
	withSources(t, twoSecondZones())
	fakeContainer(t, 0, 100_000, 300_000)
	fakeProcReads(t, []proc.CPUTimes{hostTicks(0, 0), hostTicks(1, 1), hostTicks(2, 2)}, nil)

	out := run(t, "measure", "container", "44f8", "-n", "2")

	if n := strings.Count(out, "CONTAINER"); n != 1 {
		t.Errorf("header written %d times, want 1:\n%s", n, out)
	}
	if strings.Contains(out, "\n\n") {
		t.Errorf("one table: no blank lines expected:\n%s", out)
	}
	rows := tableRows(out)
	// header + 2 samples × (total + package-0 + core)
	if len(rows) != 7 {
		t.Fatalf("want 7 lines, got %d:\n%s", len(rows), out)
	}
	// Each sample's package-0 row has the same energy as its own total row.
	for i, want := range []string{"12.000000", "8.000000"} {
		total, pkg, core := rows[1+3*i], rows[2+3*i], rows[3+3*i]
		if total[4] != "total" || pkg[4] != "intel-rapl:0" || pkg[5] != "package-0" || core[5] != "core" {
			t.Errorf("sample %d rows: %v / %v / %v", i+1, total, pkg, core)
		}
		if total[7] != want || pkg[7] != want {
			t.Errorf("sample %d: total %s, package-0 %s; want both %s", i+1, total[7], pkg[7], want)
		}
	}
}

func TestMeasureColumnsLineUp(t *testing.T) {
	fakeClock(t)
	z := twoSecondZones()
	z.readings[2] = 112_000_000 // second sample: 100 J, a wider number
	z.zoneReadings[2][0].EnergyUJ = 112_000_000
	withSources(t, z)

	out := run(t, "measure", "-n", "2")

	lines := strings.Split(strings.TrimSpace(out), "\n")
	col := strings.Index(lines[0], "ENERGY (J)")
	for _, l := range lines[1:] {
		if len(l) <= col || l[col-1] != ' ' || l[col] == ' ' {
			t.Errorf("value not under ENERGY (J) (col %d):\n%s", col, out)
			break
		}
	}
}

func TestMeasureFileOneHeaderAcrossRuns(t *testing.T) {
	fakeClock(t)
	path := filepath.Join(t.TempDir(), "zones.txt")
	for i := 0; i < 2; i++ {
		withSources(t, singlePackageZones())
		run(t, "measure", "-f", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "DEVICE"); n != 1 {
		t.Errorf("header written %d times, want 1:\n%s", n, data)
	}
	if n := len(tableRows(string(data))); n != 1+2*3 {
		t.Errorf("want header + 2 runs × 3 rows, got %d lines:\n%s", n, data)
	}
}
