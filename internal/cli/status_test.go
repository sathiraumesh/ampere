package cli

import (
	"errors"
	"strings"
	"testing"

	"sathiraumesh/ampere/internal/devices"
	"sathiraumesh/ampere/internal/devices/cpu/rapl"
)

// fakeSource is a source that is not RAPL, to test the generic output.
// Its device is "cpu" unless dev is set.
type fakeSource struct {
	name string
	err  error
	dev  string
}

func (f fakeSource) Name() string { return f.name }
func (f fakeSource) Device() string {
	if f.dev == "" {
		return devices.CPU
	}
	return f.dev
}
func (f fakeSource) Err() error            { return f.err }
func (f fakeSource) Read() (uint64, error) { return 0, f.err }

// withSources makes the commands see the given sources for one test.
func withSources(t *testing.T, sources ...devices.Source) {
	t.Helper()
	old := deviceStatus
	t.Cleanup(func() { deviceStatus = old })
	deviceStatus = func() devices.Report { return devices.Report{Sources: sources} }
}

// assertContains fails for every want that is not in got.
func assertContains(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("output missing %q\n--- output ---\n%s", w, got)
		}
	}
}

// serverRAPL is a two-socket layout with every RAPL zone type: each
// package-N with its subzones (core, uncore, dram), and the top-level psys.
// Packages and psys are readable; the subzones are not, so both cases show.
func serverRAPL() *rapl.Source {
	denied := errors.New("permission denied")
	return &rapl.Source{Zones: []rapl.Zone{
		{ID: "intel-rapl:0", Name: "package-0", MaxEnergyRangeUJ: 262143328850},
		{ID: "intel-rapl:0:0", Name: "core", ParentID: "intel-rapl:0", MaxEnergyRangeUJ: 262143328850, EnergyErr: denied},
		{ID: "intel-rapl:0:1", Name: "uncore", ParentID: "intel-rapl:0", MaxEnergyRangeUJ: 262143328850, EnergyErr: denied},
		{ID: "intel-rapl:0:2", Name: "dram", ParentID: "intel-rapl:0", MaxEnergyRangeUJ: 65712999613, EnergyErr: denied},
		{ID: "intel-rapl:1", Name: "package-1", MaxEnergyRangeUJ: 262143328850},
		{ID: "intel-rapl:1:0", Name: "core", ParentID: "intel-rapl:1", MaxEnergyRangeUJ: 262143328850, EnergyErr: denied},
		{ID: "intel-rapl:1:1", Name: "dram", ParentID: "intel-rapl:1", MaxEnergyRangeUJ: 65712999613, EnergyErr: denied},
		{ID: "intel-rapl:2", Name: "psys", MaxEnergyRangeUJ: 262143328850},
	}}
}

func TestStatusRAPL(t *testing.T) {
	withSources(t, serverRAPL())

	out := run(t, "status")

	assertContains(t, out,
		"CPU energy sources:",
		" rapl (8 zones)",
		"ID", "NAME", "PARENT", "MAX (µJ)", "READABLE", // table header
		"Using: rapl",
	)

	// Every zone has a row with its name, parent ("-" if top-level), max
	// and whether it is readable.
	want := map[string][]string{
		"intel-rapl:0":   {"package-0", "-", "262143328850", "true"},
		"intel-rapl:0:0": {"core", "intel-rapl:0", "262143328850", "false"},
		"intel-rapl:0:1": {"uncore", "intel-rapl:0", "262143328850", "false"},
		"intel-rapl:0:2": {"dram", "intel-rapl:0", "65712999613", "false"},
		"intel-rapl:1":   {"package-1", "-", "262143328850", "true"},
		"intel-rapl:1:0": {"core", "intel-rapl:1", "262143328850", "false"},
		"intel-rapl:1:1": {"dram", "intel-rapl:1", "65712999613", "false"},
		"intel-rapl:2":   {"psys", "-", "262143328850", "true"},
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 5 {
			continue
		}
		w, ok := want[fields[0]]
		if !ok {
			continue
		}
		seen[fields[0]] = true
		if strings.Join(fields[1:], " ") != strings.Join(w, " ") {
			t.Errorf("row %s = %q, want %q", fields[0], fields[1:], w)
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("no row for zone %s:\n%s", id, out)
		}
	}
}

func TestStatusUnavailable(t *testing.T) {
	withSources(t, fakeSource{name: "rapl", err: errors.New("no intel-rapl zones")})

	out := run(t, "status")

	assertContains(t, out,
		"rapl: no intel-rapl zones",
		"Using: none (no cpu energy source available)",
	)
}

func TestStatusUnknownSourceUsesDefault(t *testing.T) {
	// A source with no details printer still shows up (default case).
	withSources(t, fakeSource{name: "hwmon"})

	out := run(t, "status")

	assertContains(t, out, " hwmon\n", "Using: hwmon")
}

func TestStatusPrefersFirstAvailable(t *testing.T) {
	withSources(t,
		fakeSource{name: "rapl", err: errors.New("not here")},
		fakeSource{name: "hwmon"},
		fakeSource{name: "model"},
	)

	out := run(t, "status")

	assertContains(t, out, "rapl: not here", " hwmon\n", " model\n", "Using: hwmon")
}

func TestStatusNoSources(t *testing.T) {
	withSources(t) // empty list

	out := run(t, "status")

	assertContains(t, out, "No energy sources found.")
}

func TestStatusAllDevices(t *testing.T) {
	withSources(t,
		serverRAPL(),
		fakeSource{name: "nvml", dev: "gpu"},
		fakeSource{name: "rapl-dram", dev: "dram", err: errors.New("no zone")},
	)

	out := run(t, "status")

	assertContains(t, out,
		"CPU energy sources:", " rapl (8 zones)", "Using: rapl",
		"GPU energy sources:", " nvml\n", "Using: nvml",
		"DRAM energy sources:", "rapl-dram: no zone",
		"Using: none (no dram energy source available)",
	)
	if strings.Index(out, "CPU") > strings.Index(out, "GPU") {
		t.Errorf("devices should print in order cpu, gpu, dram:\n%s", out)
	}
}
