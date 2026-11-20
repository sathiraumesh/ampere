// Package proc reads CPU time from the Linux /proc filesystem: active and
// idle time of the whole host (/proc/stat) and CPU time of one process (/proc/<pid>/stat).
//
// Together with a CPU energy reading this gives a process's share:
//
//	process energy = CPU energy × process CPU time / active CPU time
//
// All times are in clock ticks (USER_HZ). Only the ratio of two tick counts
// is needed for the share, so the unit cancels out; use ClockTicks to turn
// ticks into seconds for display.
package proc

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Root is where Linux mounts procfs.
const Root = "/proc"

// ClockTicks is USER_HZ, the unit of all times in /proc, in ticks per
// second. It is read from the system at startup (see readClockTicks).
var ClockTicks = readClockTicks(filepath.Join(Root, "self", "auxv"))

const (
	// defaultClockTicks is USER_HZ on every mainstream Linux architecture,
	// used if the real value cannot be read.
	defaultClockTicks = 100

	// atClkTck is AT_CLKTCK, the auxv entry holding USER_HZ.
	atClkTck = 17
)

// CPUTimes is the "cpu" line of /proc/stat: time all CPUs together have
// spent in each state since boot, in clock ticks.
type CPUTimes struct {
	User    uint64 // user mode (includes Guest)
	Nice    uint64 // user mode, low priority (includes GuestNice)
	System  uint64 // kernel mode
	Idle    uint64 // idle
	IOWait  uint64 // idle while waiting for I/O
	IRQ     uint64 // servicing hardware interrupts
	SoftIRQ uint64 // servicing softirqs
	Steal   uint64 // taken by the hypervisor for other VMs
	// Guest and GuestNice are already counted in User and Nice;
	// they are kept for completeness but never added again.
	Guest     uint64
	GuestNice uint64
}

// ProcessTimes is the CPU time of one process (all its threads), from
// /proc/<pid>/stat, in clock ticks.
type ProcessTimes struct {
	PID  int
	Comm string // command name, e.g. "firefox" (max 15 chars)
	// UTime and STime are user and kernel time of the process itself.
	// Time of finished children (cutime/cstime) is not included.
	UTime uint64
	STime uint64
	// StartTime is when the process started, in ticks after boot. If it
	// differs between two readings, the PID was reused by a new process.
	StartTime uint64
}

// CPUUsage is how the CPUs spent an interval between two CPUTimes
// readings, in clock ticks summed over all CPUs (2 CPUs fully active for
// 1 s at 100 ticks/s = 200 active ticks).
type CPUUsage struct {
	Active uint64 // doing work: user, nice, system, irq, softirq
	Idle   uint64 // idle or waiting for I/O
	Steal  uint64 // taken by the hypervisor for other VMs
}

// ActiveTicks is the time the CPUs spent doing work: user, nice, system,
// irq and softirq. Steal is not active time: the CPU ran someone else's VM.
func (c CPUTimes) ActiveTicks() uint64 {
	return c.User + c.Nice + c.System + c.IRQ + c.SoftIRQ
}

// IdleTicks is the time the CPUs had nothing to do: idle + iowait.
func (c CPUTimes) IdleTicks() uint64 { return c.Idle + c.IOWait }

// Total is all time: active + idle + steal (Guest is not added: it is
// inside User).
func (c CPUTimes) Total() uint64 {
	return c.ActiveTicks() + c.IdleTicks() + c.Steal
}

// Usage returns how the CPUs spent the interval from before to c.
// Each counter is subtracted on its own with elapsedTicks.
func (c CPUTimes) Usage(before CPUTimes) CPUUsage {
	return CPUUsage{
		Active: elapsedTicks(before.User, c.User) +
			elapsedTicks(before.Nice, c.Nice) +
			elapsedTicks(before.System, c.System) +
			elapsedTicks(before.IRQ, c.IRQ) +
			elapsedTicks(before.SoftIRQ, c.SoftIRQ),
		Idle: elapsedTicks(before.Idle, c.Idle) +
			elapsedTicks(before.IOWait, c.IOWait),
		Steal: elapsedTicks(before.Steal, c.Steal),
	}
}

// elapsedTicks is how much a counter grew from start to end.
//
// If the counter went down it returns 0 instead of a negative number: a
// uint64 cannot be negative, so end-start would wrap to a huge value. The
// kernel's iowait counter is known to step backwards slightly.
func elapsedTicks(start, end uint64) uint64 {
	if end < start {
		return 0
	}
	return end - start
}

// Total is all ticks in the interval: active + idle + steal.
func (u CPUUsage) Total() uint64 { return u.Active + u.Idle + u.Steal }

// ActiveFraction is the share of the interval the CPUs were doing work,
// from 0 to 1. It is 0 if no time passed.
func (u CPUUsage) ActiveFraction() float64 { return u.fraction(u.Active) }

// IdleFraction is the share of the interval the CPUs were idle, 0 to 1.
func (u CPUUsage) IdleFraction() float64 { return u.fraction(u.Idle) }

func (u CPUUsage) fraction(n uint64) float64 {
	t := u.Total()
	if t == 0 {
		return 0
	}
	return float64(n) / float64(t)
}

// Seconds turns a tick count into seconds of CPU time.
func Seconds(ticks uint64) float64 { return float64(ticks) / float64(ClockTicks) }

// CPU is the process's total CPU time: user + kernel.
func (p ProcessTimes) CPU() uint64 { return p.UTime + p.STime }

// Usage returns the CPU ticks the process used from before to p.
// It fails if the two readings are of different processes: the PID was
// freed and reused in between (their StartTime differs).
func (p ProcessTimes) Usage(before ProcessTimes) (uint64, error) {
	if p.PID != before.PID || p.StartTime != before.StartTime {
		return 0, fmt.Errorf("process %d exited and its PID was reused", before.PID)
	}
	return elapsedTicks(before.CPU(), p.CPU()), nil
}

// ReadCPU reads the host's CPU times from /proc/stat.
func ReadCPU() (CPUTimes, error) { return readCPUAt(Root) }

// ReadProcess reads the CPU time of process pid from /proc/<pid>/stat.
// It does not need root.
func ReadProcess(pid int) (ProcessTimes, error) { return readProcessAt(Root, pid) }

// readCPUAt does the work of ReadCPU for any procfs root, so tests can use
// a fake tree.
func readCPUAt(root string) (CPUTimes, error) {
	path := filepath.Join(root, "stat")
	f, err := os.Open(path)
	if err != nil {
		return CPUTimes{}, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		// The first line is "cpu" (all CPUs); "cpu0", "cpu1", ... follow.
		if len(fields) == 0 || fields[0] != "cpu" {
			continue
		}
		// Older kernels have fewer columns; missing ones stay 0.
		// At least user, nice, system, idle have always been there.
		if len(fields) < 5 {
			return CPUTimes{}, fmt.Errorf("%s: cpu line has %d columns, want at least 4", path, len(fields)-1)
		}
		var v [10]uint64
		for i := 0; i < len(v) && i+1 < len(fields); i++ {
			if v[i], err = strconv.ParseUint(fields[i+1], 10, 64); err != nil {
				return CPUTimes{}, fmt.Errorf("%s: cpu column %d: %w", path, i+1, err)
			}
		}
		return CPUTimes{
			User: v[0], Nice: v[1], System: v[2], Idle: v[3], IOWait: v[4],
			IRQ: v[5], SoftIRQ: v[6], Steal: v[7], Guest: v[8], GuestNice: v[9],
		}, nil
	}
	if err := sc.Err(); err != nil {
		return CPUTimes{}, err
	}
	return CPUTimes{}, fmt.Errorf("%s: no cpu line", path)
}

// readProcessAt does the work of ReadProcess for any procfs root.
func readProcessAt(root string, pid int) (ProcessTimes, error) {
	path := filepath.Join(root, strconv.Itoa(pid), "stat")
	raw, err := os.ReadFile(path)
	if err != nil {
		return ProcessTimes{}, err // os.ErrNotExist: the process has exited
	}
	p, err := parseProcessStat(string(raw))
	if err != nil {
		return ProcessTimes{}, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

// parseProcessStat parses one /proc/<pid>/stat line:
//
//	1234 (my prog) S 1 1234 ... utime stime cutime cstime ... starttime ...
//
// The command name is in parentheses and may itself contain spaces or ")",
// so the fields are split after the LAST ")". Field numbers below are the
// 1-based ones from man 5 proc.
func parseProcessStat(line string) (ProcessTimes, error) {
	open := strings.IndexByte(line, '(')
	closing := strings.LastIndexByte(line, ')')
	if open < 0 || closing < open {
		return ProcessTimes{}, fmt.Errorf("no (comm) in stat line")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line[:open]))
	if err != nil {
		return ProcessTimes{}, fmt.Errorf("pid: %w", err)
	}

	// rest[0] is field 3 (state), so field n is rest[n-3].
	rest := strings.Fields(line[closing+1:])
	const (
		utimeField     = 14
		stimeField     = 15
		starttimeField = 22
	)
	if len(rest) < starttimeField-2 {
		return ProcessTimes{}, fmt.Errorf("stat line has %d fields, want at least %d", len(rest)+2, starttimeField)
	}
	field := func(n int) (uint64, error) {
		v, err := strconv.ParseUint(rest[n-3], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("field %d: %w", n, err)
		}
		return v, nil
	}

	p := ProcessTimes{PID: pid, Comm: line[open+1 : closing]}
	if p.UTime, err = field(utimeField); err != nil {
		return ProcessTimes{}, err
	}
	if p.STime, err = field(stimeField); err != nil {
		return ProcessTimes{}, err
	}
	if p.StartTime, err = field(starttimeField); err != nil {
		return ProcessTimes{}, err
	}
	return p, nil
}

// readClockTicks returns USER_HZ, the same value as C's
// sysconf(_SC_CLK_TCK), without cgo.
//
// The kernel gives every process an "auxiliary vector" at startup: a list
// of (key, value) pairs, each a native-endian machine word, ended by key 0.
// USER_HZ is the value of key AT_CLKTCK. /proc/self/auxv is that list.
// If it cannot be read or has no AT_CLKTCK, the default (100) is returned.
func readClockTicks(auxvPath string) int64 {
	raw, err := os.ReadFile(auxvPath)
	if err != nil {
		return defaultClockTicks
	}
	const word = strconv.IntSize / 8 // 8 bytes on 64-bit, 4 on 32-bit
	get := func(b []byte) uint64 {
		if word == 8 {
			return binary.NativeEndian.Uint64(b)
		}
		return uint64(binary.NativeEndian.Uint32(b))
	}
	for i := 0; i+2*word <= len(raw); i += 2 * word {
		key, val := get(raw[i:]), get(raw[i+word:])
		if key == 0 { // AT_NULL: end of the list
			break
		}
		if key == atClkTck && val > 0 {
			return int64(val)
		}
	}
	return defaultClockTicks
}
