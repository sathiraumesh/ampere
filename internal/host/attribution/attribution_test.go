package attribution

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"sathiraumesh/ampere/internal/devices/cpu/rapl"
	"sathiraumesh/ampere/internal/host/attribution/energy"
	"sathiraumesh/ampere/internal/host/proc"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func sample() energy.Sample {
	return energy.Sample{
		Device:    "cpu",
		Source:    "rapl",
		Start:     time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC),
		End:       time.Date(2026, 11, 20, 10, 0, 2, 0, time.UTC),
		IntervalS: 2,
		EnergyJ:   20,
		PowerW:    10,
		Zones: []rapl.ZoneSample{
			{ID: "intel-rapl:0", Name: "package-0", EnergyJ: 20},
			{ID: "intel-rapl:0:0", Name: "core", Parent: "intel-rapl:0", EnergyJ: 8},
		},
	}
}

func TestNewSample(t *testing.T) {
	// 2 s on 2 CPUs at 100 Hz = 400 ticks: 100 active, 300 idle (+ 10 steal,
	// which is left out). The process used 25 of the 100 active ticks.
	cpu := proc.CPUUsage{Active: 100, Idle: 300, Steal: 10}
	p := Target{PID: 42, Comm: "worker"}

	got, err := NewSample(sample(), p, cpu, proc.Seconds(25))
	if err != nil {
		t.Fatal(err)
	}

	if got.PID != 42 || got.Comm != "worker" || got.Device != "cpu" || got.Source != "rapl" || got.IntervalS != 2 {
		t.Errorf("header fields = %+v", got)
	}
	if !near(got.TargetCPU, 0.25) {
		t.Errorf("target_cpu %g, want 0.25", got.TargetCPU)
	}

	// total 20 J: 5 J active, 15 J idle, process 1.25 J = 0.625 W over 2 s
	checkSplit(t, "total", got.EnergySplit, EnergySplit{EnergyJ: 20, TargetJ: 1.25, TargetW: 0.625})

	if len(got.Zones) != 2 {
		t.Fatalf("zones = %+v", got.Zones)
	}
	pkg, core := got.Zones[0], got.Zones[1]
	if pkg.ID != "intel-rapl:0" || pkg.Name != "package-0" || pkg.Parent != "" {
		t.Errorf("package zone = %+v", pkg)
	}
	if core.ID != "intel-rapl:0:0" || core.Name != "core" || core.Parent != "intel-rapl:0" {
		t.Errorf("core zone = %+v", core)
	}
	checkSplit(t, "package-0", pkg.EnergySplit, EnergySplit{EnergyJ: 20, TargetJ: 1.25, TargetW: 0.625})
	checkSplit(t, "core", core.EnergySplit, EnergySplit{EnergyJ: 8, TargetJ: 0.5, TargetW: 0.25})
}

func checkSplit(t *testing.T, name string, got, want EnergySplit) {
	t.Helper()
	if !near(got.EnergyJ, want.EnergyJ) || !near(got.TargetJ, want.TargetJ) ||
		!near(got.TargetW, want.TargetW) {
		t.Errorf("%s: got  %+v\nwant %+v", name, got, want)
	}
}

func TestNewProcessSampleNoZones(t *testing.T) {
	s := sample()
	s.Zones = nil

	got, err := NewSample(s, Target{PID: 1}, proc.CPUUsage{Active: 1, Idle: 1}, proc.Seconds(1))
	if err != nil {
		t.Fatal(err)
	}
	if got.Zones != nil {
		t.Errorf("zones = %+v, want nil", got.Zones)
	}
}

func TestNewProcessSampleAllIdle(t *testing.T) {
	// No active time: all the energy is idle, the process gets nothing.
	got, err := NewSample(sample(), Target{PID: 1}, proc.CPUUsage{Idle: 400}, 0)
	if err != nil {
		t.Fatal(err)
	}
	checkSplit(t, "total", got.EnergySplit, EnergySplit{EnergyJ: 20})
}

func TestNewProcessSampleNoCPUTime(t *testing.T) {
	_, err := NewSample(sample(), Target{PID: 1}, proc.CPUUsage{Steal: 5}, 0)
	if !errors.Is(err, ErrNoCPUTime) {
		t.Errorf("err = %v, want ErrNoCPUTime", err)
	}
}

func TestShare(t *testing.T) {
	for _, tc := range []struct {
		active  uint64
		seconds float64
		want    float64
	}{
		{100, proc.Seconds(25), 0.25},
		{100, proc.Seconds(100), 1},
		{100, proc.Seconds(103), 1}, // read a moment apart: capped
		{0, 0, 0},
		{0, 5, 0},
		{100, 0, 0},
		// a container's µs: 0.25 s of 1 s active (100 ticks at 100 Hz)
		{uint64(proc.ClockTicks), 0.25, 0.25},
	} {
		if got := Share(proc.CPUUsage{Active: tc.active}, tc.seconds); !near(got, tc.want) {
			t.Errorf("active %d ticks, target %g s: share %g, want %g", tc.active, tc.seconds, got, tc.want)
		}
	}
}

func TestNewSampleContainer(t *testing.T) {
	c := Target{Container: strings.Repeat("a", 64), Runtime: "docker"}
	if !c.IsContainer() || (Target{PID: 1}).IsContainer() {
		t.Error("IsContainer wrong")
	}
	// 2 s at ClockTicks, half active; the container used 0.5 s of the 1 s active.
	ticks := uint64(proc.ClockTicks)
	got, err := NewSample(sample(), c, proc.CPUUsage{Active: ticks, Idle: ticks}, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if got.Target != c || !near(got.TargetCPU, 0.5) {
		t.Errorf("target %+v, share %g", got.Target, got.TargetCPU)
	}
	checkSplit(t, "total", got.EnergySplit, EnergySplit{EnergyJ: 20, TargetJ: 5, TargetW: 2.5})
}

// TestNewProcessSampleServerZones uses a 2-socket server layout: every
// zone the source reports (dram, uncore, psys, ...) is split, in order,
// with its ID, name and parent kept; nothing is specific to one machine.
func TestNewProcessSampleServerZones(t *testing.T) {
	s := sample()
	s.Zones = []rapl.ZoneSample{
		{ID: "intel-rapl:0", Name: "package-0", EnergyJ: 40},
		{ID: "intel-rapl:0:0", Name: "dram", Parent: "intel-rapl:0", EnergyJ: 8},
		{ID: "intel-rapl:1", Name: "package-1", EnergyJ: 36},
		{ID: "intel-rapl:1:0", Name: "dram", Parent: "intel-rapl:1", EnergyJ: 6},
		{ID: "intel-rapl:1:1", Name: "uncore", Parent: "intel-rapl:1", EnergyJ: 2},
		{ID: "intel-rapl:2", Name: "psys", EnergyJ: 90},
	}
	cpu := proc.CPUUsage{Active: 300, Idle: 100} // 75% active
	got, err := NewSample(s, Target{PID: 1}, cpu, proc.Seconds(30))
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Zones) != len(s.Zones) {
		t.Fatalf("got %d zones, want %d", len(got.Zones), len(s.Zones))
	}
	for i, in := range s.Zones {
		z := got.Zones[i]
		if z.ID != in.ID || z.Name != in.Name || z.Parent != in.Parent {
			t.Errorf("zone %d = %s/%s/%s, want %s/%s/%s", i, z.ID, z.Name, z.Parent, in.ID, in.Name, in.Parent)
		}
		// 75% active; the process used 30/300 = 10% of the active time.
		want := EnergySplit{
			EnergyJ: in.EnergyJ,
			TargetJ: in.EnergyJ * 0.075,
			TargetW: in.EnergyJ * 0.075 / 2,
		}
		checkSplit(t, in.ID+" "+in.Name, z.EnergySplit, want)
	}
}
