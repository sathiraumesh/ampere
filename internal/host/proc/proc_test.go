package proc

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeProc writes files under a temp procfs root, e.g. {"stat": "...",
// "1234/stat": "..."}, and returns the root.
func fakeProc(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// A real /proc/stat from a 2-CPU machine (trimmed).
const procStat = `cpu  4705 356 584 3699176 23 0 98 7 11 2
cpu0 1393 280 290 1849663 12 0 53 3 5 1
cpu1 3312 76 294 1849513 11 0 45 4 6 1
intr 1462898 21 9 0 0 0 0 3 0 1 0
ctxt 2876418
btime 1763632800
processes 26423
`

func TestReadCPU(t *testing.T) {
	root := fakeProc(t, map[string]string{"stat": procStat})

	got, err := readCPUAt(root)
	if err != nil {
		t.Fatal(err)
	}
	want := CPUTimes{
		User: 4705, Nice: 356, System: 584, Idle: 3699176, IOWait: 23,
		IRQ: 0, SoftIRQ: 98, Steal: 7, Guest: 11, GuestNice: 2,
	}
	if got != want {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if a := got.ActiveTicks(); a != 4705+356+584+0+98 {
		t.Errorf("ActiveTicks = %d: must exclude idle, iowait, steal and not re-add guest", a)
	}
	if i := got.IdleTicks(); i != 3699176+23 {
		t.Errorf("IdleTicks = %d, want idle + iowait", i)
	}
	if tot := got.Total(); tot != got.ActiveTicks()+got.IdleTicks()+7 {
		t.Errorf("Total = %d", tot)
	}
}

func TestReadCPUOldKernel(t *testing.T) {
	// Only user, nice, system, idle: the rest must stay 0.
	root := fakeProc(t, map[string]string{"stat": "cpu  10 20 30 40\n"})

	got, err := readCPUAt(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != (CPUTimes{User: 10, Nice: 20, System: 30, Idle: 40}) {
		t.Errorf("got %+v", got)
	}
}

func TestReadCPUErrors(t *testing.T) {
	for name, body := range map[string]string{
		"no cpu line": "cpu0 1 2 3 4\nintr 5\n",
		"too short":   "cpu  1 2 3\n",
		"not number":  "cpu  1 x 3 4\n",
	} {
		root := fakeProc(t, map[string]string{"stat": body})
		if _, err := readCPUAt(root); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if _, err := readCPUAt(t.TempDir()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file: err = %v, want not exist", err)
	}
}

// statLine builds a /proc/<pid>/stat line with the given comm, utime,
// stime and starttime; other fields are filler.
func statLine(pid, comm, utime, stime, start string) string {
	// fields 3..13: state ppid pgrp session tty tpgid flags minflt cminflt majflt cmajflt
	f := []string{"S", "1", "1234", "1234", "0", "-1", "4194560", "100", "0", "2", "0"}
	f = append(f, utime, stime, "5", "6") // 14 utime, 15 stime, 16 cutime, 17 cstime
	f = append(f, "20", "0", "1", "0")    // 18 priority, 19 nice, 20 threads, 21 itrealvalue
	f = append(f, start, "1000", "200")   // 22 starttime, 23 vsize, 24 rss
	return pid + " (" + comm + ") " + strings.Join(f, " ") + "\n"
}

func TestReadProcess(t *testing.T) {
	root := fakeProc(t, map[string]string{"1234/stat": statLine("1234", "firefox", "300", "45", "98765")})

	got, err := readProcessAt(root, 1234)
	if err != nil {
		t.Fatal(err)
	}
	want := ProcessTimes{PID: 1234, Comm: "firefox", UTime: 300, STime: 45, StartTime: 98765}
	if got != want {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if got.CPU() != 345 {
		t.Errorf("CPU = %d, want 345 (children's cutime/cstime not included)", got.CPU())
	}
}

func TestReadProcessTrickyComm(t *testing.T) {
	// A comm may contain spaces and ")": fields start after the LAST ")".
	for _, comm := range []string{"my prog", "a) b (c", "x)"} {
		root := fakeProc(t, map[string]string{"7/stat": statLine("7", comm, "11", "22", "33")})

		got, err := readProcessAt(root, 7)
		if err != nil {
			t.Fatalf("comm %q: %v", comm, err)
		}
		if got.Comm != comm || got.UTime != 11 || got.STime != 22 || got.StartTime != 33 {
			t.Errorf("comm %q: got %+v", comm, got)
		}
	}
}

func TestReadProcessGone(t *testing.T) {
	_, err := readProcessAt(t.TempDir(), 999)
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want not exist (process exited)", err)
	}
}

func TestParseProcessStatErrors(t *testing.T) {
	for name, line := range map[string]string{
		"no parens":   "1234 firefox S 1 2 3",
		"bad pid":     "abc (x) S 1",
		"too short":   "1 (x) S 1 2 3",
		"bad utime":   statLine("1", "x", "u", "1", "1"),
		"bad stime":   statLine("1", "x", "1", "s", "1"),
		"bad start":   statLine("1", "x", "1", "1", "z"),
		"only parens": "()",
	} {
		if _, err := parseProcessStat(line); err == nil {
			t.Errorf("%s: want error for %q", name, line)
		}
	}
}

// TestReadRealProc checks parsing against this machine's /proc, if any.
func TestReadRealProc(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc here")
	}
	cpu, err := ReadCPU()
	if err != nil {
		t.Fatal(err)
	}
	if cpu.Total() == 0 {
		t.Errorf("total CPU time is 0: %+v", cpu)
	}
	self, err := ReadProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if self.PID != os.Getpid() || self.Comm == "" {
		t.Errorf("self = %+v", self)
	}
}

// auxv encodes (key, value) pairs the way the kernel does on this machine.
func auxv(pairs ...uint64) []byte {
	const word = strconv.IntSize / 8
	var b []byte
	for _, v := range pairs {
		if word == 8 {
			b = binary.NativeEndian.AppendUint64(b, v)
		} else {
			b = binary.NativeEndian.AppendUint32(b, uint32(v))
		}
	}
	return b
}

func TestReadClockTicks(t *testing.T) {
	dir := t.TempDir()
	for name, tc := range map[string]struct {
		data []byte
		want int64
	}{
		"found":          {auxv(6, 4096, atClkTck, 250, 0, 0), 250},
		"stops at null":  {auxv(6, 4096, 0, 0, atClkTck, 250), defaultClockTicks},
		"missing":        {auxv(6, 4096, 0, 0), defaultClockTicks},
		"zero value":     {auxv(atClkTck, 0, 0, 0), defaultClockTicks},
		"truncated pair": {auxv(atClkTck, 250)[:strconv.IntSize/8+1], defaultClockTicks},
		"empty":          {nil, defaultClockTicks},
	} {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "_"))
		if err := os.WriteFile(path, tc.data, 0o644); err != nil {
			t.Fatal(err)
		}
		if got := readClockTicks(path); got != tc.want {
			t.Errorf("%s: got %d, want %d", name, got, tc.want)
		}
	}
	if got := readClockTicks(filepath.Join(dir, "nope")); got != defaultClockTicks {
		t.Errorf("missing file: got %d, want %d", got, defaultClockTicks)
	}
}

func TestClockTicksRealSystem(t *testing.T) {
	if _, err := os.Stat("/proc/self/auxv"); err != nil {
		t.Skip("no /proc here")
	}
	// The real value, read from this machine; 100 on all mainstream Linux.
	if ClockTicks <= 0 {
		t.Errorf("ClockTicks = %d", ClockTicks)
	}
	t.Logf("ClockTicks = %d", ClockTicks)
}

func TestUsage(t *testing.T) {
	before := CPUTimes{User: 100, Nice: 10, System: 50, Idle: 1000, IOWait: 20,
		IRQ: 1, SoftIRQ: 4, Steal: 5, Guest: 30}
	after := CPUTimes{User: 160, Nice: 10, System: 70, Idle: 1100, IOWait: 30,
		IRQ: 2, SoftIRQ: 9, Steal: 10, Guest: 90}

	u := after.Usage(before)

	// active: 60 user + 0 nice + 20 system + 1 irq + 5 softirq; guest not re-added
	want := CPUUsage{Active: 86, Idle: 110, Steal: 5}
	if u != want {
		t.Fatalf("usage = %+v, want %+v", u, want)
	}
	if u.Total() != 201 {
		t.Errorf("Total = %d, want 201", u.Total())
	}
	if f := u.ActiveFraction(); f != 86.0/201 {
		t.Errorf("ActiveFraction = %g, want %g", f, 86.0/201)
	}
	if f := u.IdleFraction(); f != 110.0/201 {
		t.Errorf("IdleFraction = %g, want %g", f, 110.0/201)
	}
}

func TestUsageCounterStepsBack(t *testing.T) {
	// iowait can decrease slightly between readings: count it as 0,
	// do not wrap the uint64.
	before := CPUTimes{User: 10, Idle: 100, IOWait: 50}
	after := CPUTimes{User: 20, Idle: 150, IOWait: 48}

	if u := after.Usage(before); u != (CPUUsage{Active: 10, Idle: 50}) {
		t.Errorf("usage = %+v, want active 10, idle 50", u)
	}
}

func TestUsageNoTime(t *testing.T) {
	c := CPUTimes{User: 5, Idle: 5}
	u := c.Usage(c)
	if u.Total() != 0 || u.ActiveFraction() != 0 || u.IdleFraction() != 0 {
		t.Errorf("no time passed: %+v, active %g, idle %g", u, u.ActiveFraction(), u.IdleFraction())
	}
}

func TestSeconds(t *testing.T) {
	if got, want := Seconds(uint64(ClockTicks)*3/2), 1.5; got != want {
		t.Errorf("Seconds(1.5 s of ticks) = %g, want %g", got, want)
	}
}

// TestUsageRealSystem checks two real readings make sense.
func TestUsageRealSystem(t *testing.T) {
	if _, err := os.Stat("/proc/stat"); err != nil {
		t.Skip("no /proc here")
	}
	before, err := ReadCPU()
	if err != nil {
		t.Fatal(err)
	}
	// Spin a little so some time passes.
	for end := time.Now().Add(50 * time.Millisecond); time.Now().Before(end); {
	}
	after, err := ReadCPU()
	if err != nil {
		t.Fatal(err)
	}
	u := after.Usage(before)
	if a, i := u.ActiveFraction(), u.IdleFraction(); a < 0 || a > 1 || i < 0 || i > 1 {
		t.Errorf("fractions out of range: active %g, idle %g", a, i)
	}
	t.Logf("50 ms: %+v → active %.0f%%, idle %.0f%%", u, 100*u.ActiveFraction(), 100*u.IdleFraction())
}

func TestProcessUsage(t *testing.T) {
	before := ProcessTimes{PID: 7, UTime: 100, STime: 20, StartTime: 500}
	after := ProcessTimes{PID: 7, UTime: 130, STime: 25, StartTime: 500}

	got, err := after.Usage(before)
	if err != nil || got != 35 {
		t.Errorf("Usage = %d, %v; want 35 ticks", got, err)
	}

	reused := ProcessTimes{PID: 7, UTime: 1, StartTime: 900}
	if _, err := reused.Usage(before); err == nil || !strings.Contains(err.Error(), "reused") {
		t.Errorf("PID reused: err = %v, want reused error", err)
	}
}
