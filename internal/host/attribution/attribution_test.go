package attribution

import (
	"errors"
	"math"
	"testing"
	"time"

	"sathiraumesh/ampere/internal/devices"
	"sathiraumesh/ampere/internal/host/proc"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func sample() devices.Sample {
	return devices.Sample{
		Device:    "cpu",
		Source:    "rapl",
		Start:     time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC),
		End:       time.Date(2026, 11, 20, 10, 0, 2, 0, time.UTC),
		IntervalS: 2,
		EnergyJ:   20,
		PowerW:    10,
		Zones: []devices.ZoneSample{
			{ID: "intel-rapl:0", Name: "package-0", EnergyJ: 20},
			{ID: "intel-rapl:0:0", Name: "core", Parent: "intel-rapl:0", EnergyJ: 8},
		},
	}
}

func TestNewProcessSample(t *testing.T) {
	// 2 s on 2 CPUs at 100 Hz = 400 ticks: 100 active, 300 idle (+ 10 steal,
	// which is left out). The process used 25 of the 100 active ticks.
	cpu := proc.CPUUsage{Active: 100, Idle: 300, Steal: 10}
	p := proc.ProcessTimes{PID: 42, Comm: "worker"}

	got, err := NewProcessSample(sample(), p, cpu, 25)
	if err != nil {
		t.Fatal(err)
	}

	if got.PID != 42 || got.Comm != "worker" || got.Device != "cpu" || got.Source != "rapl" || got.IntervalS != 2 {
		t.Errorf("header fields = %+v", got)
	}
	if !near(got.CPUActive, 0.25) || !near(got.ProcessCPU, 0.25) {
		t.Errorf("cpu_active %g, process_cpu %g; want 0.25, 0.25", got.CPUActive, got.ProcessCPU)
	}

	// total 20 J: 5 J active, 15 J idle, process 1.25 J = 0.625 W over 2 s
	checkSplit(t, "total", got.EnergySplit, EnergySplit{EnergyJ: 20, ActiveJ: 5, IdleJ: 15, ProcessJ: 1.25, ProcessW: 0.625})

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
	checkSplit(t, "package-0", pkg.EnergySplit, EnergySplit{EnergyJ: 20, ActiveJ: 5, IdleJ: 15, ProcessJ: 1.25, ProcessW: 0.625})
	checkSplit(t, "core", core.EnergySplit, EnergySplit{EnergyJ: 8, ActiveJ: 2, IdleJ: 6, ProcessJ: 0.5, ProcessW: 0.25})
}

func checkSplit(t *testing.T, name string, got, want EnergySplit) {
	t.Helper()
	if !near(got.EnergyJ, want.EnergyJ) || !near(got.ActiveJ, want.ActiveJ) ||
		!near(got.IdleJ, want.IdleJ) || !near(got.ProcessJ, want.ProcessJ) ||
		!near(got.ProcessW, want.ProcessW) {
		t.Errorf("%s: got  %+v\nwant %+v", name, got, want)
	}
	if !near(got.ActiveJ+got.IdleJ, got.EnergyJ) {
		t.Errorf("%s: active + idle = %g, want energy %g", name, got.ActiveJ+got.IdleJ, got.EnergyJ)
	}
}

func TestNewProcessSampleNoZones(t *testing.T) {
	s := sample()
	s.Zones = nil

	got, err := NewProcessSample(s, proc.ProcessTimes{PID: 1}, proc.CPUUsage{Active: 1, Idle: 1}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Zones != nil {
		t.Errorf("zones = %+v, want nil", got.Zones)
	}
}

func TestNewProcessSampleAllIdle(t *testing.T) {
	// No active time: everything is idle energy, the process gets nothing.
	got, err := NewProcessSample(sample(), proc.ProcessTimes{PID: 1}, proc.CPUUsage{Idle: 400}, 0)
	if err != nil {
		t.Fatal(err)
	}
	checkSplit(t, "total", got.EnergySplit, EnergySplit{EnergyJ: 20, IdleJ: 20})
}

func TestNewProcessSampleNoCPUTime(t *testing.T) {
	_, err := NewProcessSample(sample(), proc.ProcessTimes{PID: 1}, proc.CPUUsage{Steal: 5}, 0)
	if !errors.Is(err, ErrNoCPUTime) {
		t.Errorf("err = %v, want ErrNoCPUTime", err)
	}
}

func TestProcessShare(t *testing.T) {
	for _, tc := range []struct {
		active, process uint64
		want            float64
	}{
		{100, 25, 0.25},
		{100, 100, 1},
		{100, 103, 1}, // read a moment apart: capped
		{0, 0, 0},
		{0, 5, 0},
	} {
		if got := ProcessShare(proc.CPUUsage{Active: tc.active}, tc.process); !near(got, tc.want) {
			t.Errorf("active %d, process %d: share %g, want %g", tc.active, tc.process, got, tc.want)
		}
	}
}

// TestNewProcessSampleServerZones uses a 2-socket server layout: every
// zone the source reports (dram, uncore, psys, ...) is split, in order,
// with its ID, name and parent kept; nothing is specific to one machine.
func TestNewProcessSampleServerZones(t *testing.T) {
	s := sample()
	s.Zones = []devices.ZoneSample{
		{ID: "intel-rapl:0", Name: "package-0", EnergyJ: 40},
		{ID: "intel-rapl:0:0", Name: "dram", Parent: "intel-rapl:0", EnergyJ: 8},
		{ID: "intel-rapl:1", Name: "package-1", EnergyJ: 36},
		{ID: "intel-rapl:1:0", Name: "dram", Parent: "intel-rapl:1", EnergyJ: 6},
		{ID: "intel-rapl:1:1", Name: "uncore", Parent: "intel-rapl:1", EnergyJ: 2},
		{ID: "intel-rapl:2", Name: "psys", EnergyJ: 90},
	}
	cpu := proc.CPUUsage{Active: 300, Idle: 100} // 75% active
	got, err := NewProcessSample(s, proc.ProcessTimes{PID: 1}, cpu, 30)
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
			EnergyJ:  in.EnergyJ,
			ActiveJ:  in.EnergyJ * 0.75,
			IdleJ:    in.EnergyJ * 0.25,
			ProcessJ: in.EnergyJ * 0.075,
			ProcessW: in.EnergyJ * 0.075 / 2,
		}
		checkSplit(t, in.ID+" "+in.Name, z.EnergySplit, want)
	}
}
