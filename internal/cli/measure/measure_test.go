package measure

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"sathiraumesh/ampere/internal/devices"
	"sathiraumesh/ampere/internal/devices/cpu/rapl"
	"sathiraumesh/ampere/internal/host/attribution/energy"
)

// counterSource is a source that returns the given readings in order.
// Its device is "cpu" unless dev is set.
type counterSource struct {
	readings []uint64
	readErr  error
	calls    int
	dev      string
}

func (c *counterSource) Name() string { return "fake" }
func (c *counterSource) Device() string {
	if c.dev == "" {
		return devices.CPU
	}
	return c.dev
}
func (c *counterSource) Err() error { return nil }
func (c *counterSource) Read() (uint64, error) {
	if c.readErr != nil {
		return 0, c.readErr
	}
	v := c.readings[c.calls]
	c.calls++
	return v, nil
}

// fakeClock replaces sleep/now: sleeping advances the clock instantly.
func fakeClock(t *testing.T) *[]time.Duration {
	t.Helper()
	oldSleep, oldNow := sleep, now
	t.Cleanup(func() { sleep, now = oldSleep, oldNow })

	clock := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	var slept []time.Duration
	now = func() time.Time { return clock }
	sleep = func(d time.Duration) {
		slept = append(slept, d)
		clock = clock.Add(d)
	}
	return &slept
}

// measureSample runs "ampere measure <args> -o json" and decodes the sample.
func measureSample(t *testing.T, args ...string) energy.Sample {
	t.Helper()
	out := run(t, append(append([]string{"measure"}, args...), "-o", "json")...)
	var s energy.Sample
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	return s
}

func TestMeasurePower(t *testing.T) {
	slept := fakeClock(t)
	src := &counterSource{readings: []uint64{1_000_000, 13_000_000}} // +12 J
	withSources(t, src)

	got := measureSample(t)

	want := energy.Sample{
		Device:    "cpu",
		Source:    "fake",
		Start:     time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC),
		End:       time.Date(2026, 11, 20, 10, 0, 1, 0, time.UTC),
		IntervalS: 1,
		EnergyJ:   12,
		PowerW:    12,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sample = %+v\nwant     %+v", got, want)
	}
	if len(*slept) != 1 || (*slept)[0] != time.Second {
		t.Errorf("slept %v, want [1s]", *slept)
	}
	if src.calls != 2 {
		t.Errorf("Read called %d times, want 2", src.calls)
	}
}

func TestMeasureInterval(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 50_000_000}}) // 50 J

	got := measureSample(t, "--interval", "5s")

	if got.IntervalS != 5 || got.EnergyJ != 50 || got.PowerW != 10 {
		t.Errorf("sample = %+v, want 5 s, 50 J, 10 W", got)
	}
}

func TestMeasureUsesPreferredSource(t *testing.T) {
	fakeClock(t)
	withSources(t,
		fakeSource{name: "rapl", err: errors.New("not here")}, // unavailable
		&counterSource{readings: []uint64{0, 1_000_000}},      // used
	)

	if got := measureSample(t); got.Source != "fake" {
		t.Errorf("source = %q, want fake", got.Source)
	}
}

func TestMeasureTable(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{1_000_000, 13_000_000}})

	out := run(t, "measure") // table is the default

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("want header + 1 row, got %d lines:\n%s", len(lines), out)
	}
	header := strings.Join(strings.Fields(lines[0]), " ")
	if header != "DEVICE SOURCE INTERVAL (s) ENERGY (J) POWER (W)" {
		t.Errorf("header = %q", header)
	}
	if row := strings.Fields(lines[1]); strings.Join(row, " ") != "cpu fake 1.000000 12.000000 12.00" {
		t.Errorf("row = %q", lines[1])
	}
}

func TestMeasureJSONIsOneLine(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1_000_000}})

	out := run(t, "measure", "-o", "json")

	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "}\n") {
		t.Errorf("want one JSON object per line, got:\n%s", out)
	}
	for _, key := range []string{`"device"`, `"source"`, `"start"`, `"end"`,
		`"interval_s"`, `"energy_j"`, `"power_w"`} {
		if !strings.Contains(out, key) {
			t.Errorf("JSON missing %s: %s", key, out)
		}
	}
}

func TestMeasureBadOutput(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1}})

	_, err := runErr("measure", "-o", "xml")
	if err == nil || !strings.Contains(err.Error(), "--output") {
		t.Errorf("err = %v, want --output error", err)
	}
}

func TestMeasureNoSource(t *testing.T) {
	fakeClock(t)
	withSources(t, fakeSource{name: "rapl", err: errors.New("not here")})

	_, err := runErr("measure")
	if err == nil || !strings.Contains(err.Error(), "no cpu energy source") {
		t.Errorf("err = %v, want no-source error", err)
	}
}

func TestMeasurePermissionError(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readErr: errors.New("permission denied")})

	_, err := runErr("measure")
	if err == nil || !strings.Contains(err.Error(), "permission denied") ||
		!strings.Contains(err.Error(), "sudo") {
		t.Errorf("err = %v, want permission error with sudo hint", err)
	}
}

func TestMeasureWraparound(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{65_000_000_000, 100}})

	_, err := runErr("measure")
	if err == nil || !strings.Contains(err.Error(), "wrapped") {
		t.Errorf("err = %v, want wraparound error", err)
	}
}

func TestMeasureBadInterval(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 0}})

	for _, iv := range []string{"0s", "-1s"} {
		if _, err := runErr("measure", "--interval", iv); err == nil {
			t.Errorf("measure --interval %s: want error", iv)
		}
	}
}

func TestMeasureChooseSource(t *testing.T) {
	fakeClock(t)
	chosen := &counterSource{readings: []uint64{0, 2_000_000}}
	withSources(t,
		&counterSource{readings: []uint64{0, 9_000_000}}, // preferred, not chosen
		namedSource{chosen, "hwmon"},
	)

	got := measureSample(t, "--source", "hwmon")

	if got.Source != "hwmon" || got.EnergyJ != 2 {
		t.Errorf("sample = %+v, want hwmon, 2 J", got)
	}
}

func TestMeasureChooseUnknownSource(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1}})

	_, err := runErr("measure", "-s", "gpu")
	if err == nil || !strings.Contains(err.Error(), "unknown cpu source") {
		t.Errorf("err = %v, want unknown source", err)
	}
}

func TestMeasureChooseUnavailableSource(t *testing.T) {
	fakeClock(t)
	withSources(t, fakeSource{name: "rapl", err: errors.New("no zones")})

	_, err := runErr("measure", "--source", "rapl")
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Errorf("err = %v, want not available", err)
	}
}

// namedSource gives a counterSource a different name.
type namedSource struct {
	*counterSource
	name string
}

func (n namedSource) Name() string { return n.name }

func TestMeasureOtherDevice(t *testing.T) {
	fakeClock(t)
	withSources(t,
		&counterSource{readings: []uint64{0, 9_000_000}},
		&counterSource{readings: []uint64{0, 3_000_000}, dev: "gpu"},
	)

	got := measureSample(t, "--device", "gpu")

	if got.Device != "gpu" || got.EnergyJ != 3 {
		t.Errorf("sample = %+v, want gpu, 3 J", got)
	}
}

func TestMeasureUnknownDevice(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1}})

	_, err := runErr("measure", "-d", "gpu")
	if err == nil || !strings.Contains(err.Error(), `unknown device "gpu" (known: cpu)`) {
		t.Errorf("err = %v, want unknown device", err)
	}
}

func TestMeasureToFileJSON(t *testing.T) {
	fakeClock(t)
	path := filepath.Join(t.TempDir(), "samples.jsonl")

	for i := 0; i < 2; i++ {
		withSources(t, &counterSource{readings: []uint64{0, 1_000_000}})
		if out := run(t, "measure", "-o", "json", "-f", path); out != "" {
			t.Errorf("with --file nothing should be printed, got %q", out)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 appended JSON lines, got %d:\n%s", len(lines), data)
	}
	for _, l := range lines {
		var s energy.Sample
		if err := json.Unmarshal([]byte(l), &s); err != nil || s.EnergyJ != 1 {
			t.Errorf("line %q: %+v, %v", l, s, err)
		}
	}
}

func TestMeasureToFileTableHeaderOnce(t *testing.T) {
	fakeClock(t)
	path := filepath.Join(t.TempDir(), "samples.txt")

	for i := 0; i < 3; i++ {
		withSources(t, &counterSource{readings: []uint64{0, 1_000_000}})
		run(t, "measure", "-f", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "DEVICE"); n != 1 {
		t.Errorf("header written %d times, want 1:\n%s", n, data)
	}
	if n := strings.Count(string(data), "cpu"); n != 3 {
		t.Errorf("want 3 rows, got %d:\n%s", n, data)
	}
}

func TestMeasureToFileBadPath(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1}})

	_, err := runErr("measure", "-f", filepath.Join(t.TempDir(), "no-such-dir", "x.jsonl"))
	if err == nil {
		t.Error("want an error for a file in a missing folder")
	}
}

// zoneSource is a counterSource that also has zones (like RAPL).
type zoneSource struct {
	*counterSource
	zoneReadings [][]rapl.ZoneReading
	zoneCalls    int
}

func (z *zoneSource) ReadZones() ([]rapl.ZoneReading, error) {
	r := z.zoneReadings[z.zoneCalls]
	z.zoneCalls++
	return r, nil
}

// singlePackageZones is a single-socket source with zones over one second:
// package-0 uses 12 J, its core subzone 5 J.
func singlePackageZones() *zoneSource {
	return &zoneSource{
		counterSource: &counterSource{readings: []uint64{0, 12_000_000}}, // 12 J
		zoneReadings: [][]rapl.ZoneReading{
			{
				{ID: "intel-rapl:0", Name: "package-0", EnergyUJ: 0},
				{ID: "intel-rapl:0:0", Name: "core", ParentID: "intel-rapl:0", EnergyUJ: 0},
			},
			{
				{ID: "intel-rapl:0", Name: "package-0", EnergyUJ: 12_000_000},
				{ID: "intel-rapl:0:0", Name: "core", ParentID: "intel-rapl:0", EnergyUJ: 5_000_000},
			},
		},
	}
}

func TestMeasureZonesByDefaultJSON(t *testing.T) {
	fakeClock(t)
	withSources(t, singlePackageZones())

	got := measureSample(t)

	want := []rapl.ZoneSample{
		{ID: "intel-rapl:0", Name: "package-0", EnergyJ: 12, PowerW: 12},
		{ID: "intel-rapl:0:0", Name: "core", Parent: "intel-rapl:0", EnergyJ: 5, PowerW: 5},
	}
	if !reflect.DeepEqual(got.Zones, want) {
		t.Errorf("zones = %+v\nwant    %+v", got.Zones, want)
	}
}

func TestMeasureZonesInOneTable(t *testing.T) {
	fakeClock(t)
	withSources(t, singlePackageZones())

	out := run(t, "measure")

	want := [][]string{
		{"DEVICE", "SOURCE", "INTERVAL", "(s)", "ZONE", "NAME", "PARENT", "ENERGY", "(J)", "POWER", "(W)"},
		{"cpu", "fake", "1.000000", "total", "-", "-", "12.000000", "12.00"},
		{"cpu", "fake", "1.000000", "intel-rapl:0", "package-0", "-", "12.000000", "12.00"},
		{"cpu", "fake", "1.000000", "intel-rapl:0:0", "core", "intel-rapl:0", "5.000000", "5.00"},
	}
	if got := tableRows(out); !reflect.DeepEqual(got, want) {
		t.Errorf("table:\n%s\ngot  %q\nwant %q", out, got, want)
	}
}

func TestMeasureNoZonesForPlainSource(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1_000_000}})

	if got := measureSample(t); got.Zones != nil {
		t.Errorf("source without zones: Zones = %+v, want nil", got.Zones)
	}
	withSources(t, &counterSource{readings: []uint64{0, 1_000_000}}) // fresh readings
	out := run(t, "measure", "-o", "json")
	if strings.Contains(out, "zones") {
		t.Errorf(`JSON should omit "zones" when there are none: %s`, out)
	}
}

func TestMeasureCountJSON(t *testing.T) {
	slept := fakeClock(t)
	// 4 readings → 3 back-to-back samples: +1 J, +2 J, +3 J.
	src := &counterSource{readings: []uint64{0, 1_000_000, 3_000_000, 6_000_000}}
	withSources(t, src)

	out := run(t, "measure", "-n", "3", "-o", "json")

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 JSON lines, got %d:\n%s", len(lines), out)
	}
	start := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	for i, l := range lines {
		var s energy.Sample
		if err := json.Unmarshal([]byte(l), &s); err != nil {
			t.Fatalf("line %d not JSON: %v", i, err)
		}
		if want := float64(i + 1); s.EnergyJ != want {
			t.Errorf("sample %d: energy = %g J, want %g", i, s.EnergyJ, want)
		}
		// No gaps: each sample starts where the previous one ended.
		wantStart := start.Add(time.Duration(i) * time.Second)
		if !s.Start.Equal(wantStart) || !s.End.Equal(wantStart.Add(time.Second)) {
			t.Errorf("sample %d: %v → %v, want %v → +1s", i, s.Start, s.End, wantStart)
		}
	}
	if len(*slept) != 3 || src.calls != 4 {
		t.Errorf("slept %d times, read %d times; want 3 sleeps, 4 reads", len(*slept), src.calls)
	}
}

func TestMeasureCountTableHeaderOnce(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1_000_000, 2_000_000, 3_000_000}})

	out := run(t, "measure", "--count", "3")

	if n := strings.Count(out, "DEVICE"); n != 1 {
		t.Errorf("header written %d times, want 1:\n%s", n, out)
	}
	if n := strings.Count(out, "cpu"); n != 3 {
		t.Errorf("want 3 rows, got %d:\n%s", n, out)
	}
}

func TestMeasureCountToFile(t *testing.T) {
	fakeClock(t)
	path := filepath.Join(t.TempDir(), "samples.jsonl")
	withSources(t, &counterSource{readings: []uint64{0, 1, 2, 3, 4, 5}})

	run(t, "measure", "-n", "5", "-o", "json", "-f", path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "\n"); n != 5 {
		t.Errorf("want 5 lines in file, got %d:\n%s", n, data)
	}
}

func TestMeasureCountZones(t *testing.T) {
	fakeClock(t)
	src := singlePackageZones()
	src.readings = append(src.readings, 20_000_000)
	src.zoneReadings = append(src.zoneReadings, []rapl.ZoneReading{
		{ID: "intel-rapl:0", Name: "package-0", EnergyUJ: 20_000_000},
		{ID: "intel-rapl:0:0", Name: "core", ParentID: "intel-rapl:0", EnergyUJ: 9_000_000},
	})
	withSources(t, src)

	out := run(t, "measure", "-n", "2", "-o", "json")

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got:\n%s", out)
	}
	var s energy.Sample
	if err := json.Unmarshal([]byte(lines[1]), &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Zones) != 2 || s.Zones[0].EnergyJ != 8 || s.Zones[1].EnergyJ != 4 {
		t.Errorf("second sample zones = %+v, want package 8 J, core 4 J", s.Zones)
	}
}

func TestMeasureBadCount(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1}})

	for _, n := range []string{"0", "-1"} {
		_, err := runErr("measure", "--count", n)
		if err == nil || !strings.Contains(err.Error(), "--count") {
			t.Errorf("measure --count %s: err = %v, want --count error", n, err)
		}
	}
}
