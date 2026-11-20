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

// laptopRAPL looks like the dev laptop: package-0 with a core subzone.
func laptopRAPL() *rapl.Source {
	return &rapl.Source{Zones: []rapl.Zone{
		{ID: "intel-rapl:0", Name: "package-0", MaxEnergyRangeUJ: 65532610987},
		{ID: "intel-rapl:0:0", Name: "core", ParentID: "intel-rapl:0",
			MaxEnergyRangeUJ: 65532610987, EnergyErr: errors.New("permission denied")},
	}}
}

func TestStatusRAPL(t *testing.T) {
	withSources(t, laptopRAPL())

	out := run(t, "status")

	assertContains(t, out,
		"CPU energy sources:",
		" rapl (2 zones)",
		"ID", "NAME", "PARENT", "MAX (µJ)", "READABLE", // table header
		"intel-rapl:0 ", "package-0",
		"intel-rapl:0:0", "core",
		"65532610987",
		"Using: rapl",
	)

	// Top-level zone shows "-" as parent and is readable;
	// the core subzone shows its parent and is not readable.
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 5 {
			continue
		}
		switch fields[0] {
		case "intel-rapl:0":
			if fields[2] != "-" || fields[4] != "true" {
				t.Errorf("package row = %q, want parent - and readable true", line)
			}
		case "intel-rapl:0:0":
			if fields[2] != "intel-rapl:0" || fields[4] != "false" {
				t.Errorf("core row = %q, want parent intel-rapl:0 and readable false", line)
			}
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

func TestStatusRejectsArgs(t *testing.T) {
	withSources(t, laptopRAPL())

	cmd := RootCommand()
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	cmd.SetArgs([]string{"status", "extra"})
	if err := cmd.Execute(); err == nil {
		t.Error(`"ampere status extra" should fail: status takes no arguments`)
	}
}

func TestStatusAllDevices(t *testing.T) {
	withSources(t,
		laptopRAPL(),
		fakeSource{name: "nvml", dev: "gpu"},
		fakeSource{name: "rapl-dram", dev: "dram", err: errors.New("no zone")},
	)

	out := run(t, "status")

	assertContains(t, out,
		"CPU energy sources:", " rapl (2 zones)", "Using: rapl",
		"GPU energy sources:", " nvml\n", "Using: nvml",
		"DRAM energy sources:", "rapl-dram: no zone",
		"Using: none (no dram energy source available)",
	)
	if strings.Index(out, "CPU") > strings.Index(out, "GPU") {
		t.Errorf("devices should print in order cpu, gpu, dram:\n%s", out)
	}
}
