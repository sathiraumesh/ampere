package cli

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

// readSample runs "ampere read <args> -o json" and decodes the sample.
func readSample(t *testing.T, args ...string) devices.Sample {
	t.Helper()
	out := run(t, append(append([]string{"read"}, args...), "-o", "json")...)
	var s devices.Sample
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	return s
}

// runErr executes the root command and returns its output and error.
func runErr(args ...string) (string, error) {
	var out strings.Builder
	cmd := RootCommand()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestReadPower(t *testing.T) {
	slept := fakeClock(t)
	src := &counterSource{readings: []uint64{1_000_000, 13_000_000}} // +12 J
	withSources(t, src)

	got := readSample(t)

	want := devices.Sample{
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

func TestReadInterval(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 50_000_000}}) // 50 J

	got := readSample(t, "--interval", "5s")

	if got.IntervalS != 5 || got.EnergyJ != 50 || got.PowerW != 10 {
		t.Errorf("sample = %+v, want 5 s, 50 J, 10 W", got)
	}
}

func TestReadUsesPreferredSource(t *testing.T) {
	fakeClock(t)
	withSources(t,
		fakeSource{name: "rapl", err: errors.New("not here")}, // unavailable
		&counterSource{readings: []uint64{0, 1_000_000}},      // used
	)

	if got := readSample(t); got.Source != "fake" {
		t.Errorf("source = %q, want fake", got.Source)
	}
}

func TestReadTable(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{1_000_000, 13_000_000}})

	out := run(t, "read") // table is the default

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

func TestReadJSONIsOneLine(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1_000_000}})

	out := run(t, "read", "-o", "json")

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

func TestReadBadOutput(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1}})

	_, err := runErr("read", "-o", "xml")
	if err == nil || !strings.Contains(err.Error(), "--output") {
		t.Errorf("err = %v, want --output error", err)
	}
}

func TestReadNoSource(t *testing.T) {
	fakeClock(t)
	withSources(t, fakeSource{name: "rapl", err: errors.New("not here")})

	_, err := runErr("read")
	if err == nil || !strings.Contains(err.Error(), "no cpu energy source") {
		t.Errorf("err = %v, want no-source error", err)
	}
}

func TestReadPermissionError(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readErr: errors.New("permission denied")})

	_, err := runErr("read")
	if err == nil || !strings.Contains(err.Error(), "permission denied") ||
		!strings.Contains(err.Error(), "sudo") {
		t.Errorf("err = %v, want permission error with sudo hint", err)
	}
}

func TestReadWraparound(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{65_000_000_000, 100}})

	_, err := runErr("read")
	if err == nil || !strings.Contains(err.Error(), "wrapped") {
		t.Errorf("err = %v, want wraparound error", err)
	}
}

func TestReadBadInterval(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 0}})

	for _, iv := range []string{"0s", "-1s"} {
		if _, err := runErr("read", "--interval", iv); err == nil {
			t.Errorf("read --interval %s: want error", iv)
		}
	}
}

func TestReadChooseSource(t *testing.T) {
	fakeClock(t)
	chosen := &counterSource{readings: []uint64{0, 2_000_000}}
	withSources(t,
		&counterSource{readings: []uint64{0, 9_000_000}}, // preferred, not chosen
		namedSource{chosen, "hwmon"},
	)

	got := readSample(t, "--source", "hwmon")

	if got.Source != "hwmon" || got.EnergyJ != 2 {
		t.Errorf("sample = %+v, want hwmon, 2 J", got)
	}
}

func TestReadChooseUnknownSource(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1}})

	_, err := runErr("read", "-s", "gpu")
	if err == nil || !strings.Contains(err.Error(), "unknown cpu source") {
		t.Errorf("err = %v, want unknown source", err)
	}
}

func TestReadChooseUnavailableSource(t *testing.T) {
	fakeClock(t)
	withSources(t, fakeSource{name: "rapl", err: errors.New("no zones")})

	_, err := runErr("read", "--source", "rapl")
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

func TestReadOtherDevice(t *testing.T) {
	fakeClock(t)
	withSources(t,
		&counterSource{readings: []uint64{0, 9_000_000}},
		&counterSource{readings: []uint64{0, 3_000_000}, dev: "gpu"},
	)

	got := readSample(t, "--device", "gpu")

	if got.Device != "gpu" || got.EnergyJ != 3 {
		t.Errorf("sample = %+v, want gpu, 3 J", got)
	}
}

func TestReadUnknownDevice(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1}})

	_, err := runErr("read", "-d", "gpu")
	if err == nil || !strings.Contains(err.Error(), `unknown device "gpu" (known: cpu)`) {
		t.Errorf("err = %v, want unknown device", err)
	}
}

func TestReadToFileJSON(t *testing.T) {
	fakeClock(t)
	path := filepath.Join(t.TempDir(), "samples.jsonl")

	for i := 0; i < 2; i++ {
		withSources(t, &counterSource{readings: []uint64{0, 1_000_000}})
		if out := run(t, "read", "-o", "json", "-f", path); out != "" {
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
		var s devices.Sample
		if err := json.Unmarshal([]byte(l), &s); err != nil || s.EnergyJ != 1 {
			t.Errorf("line %q: %+v, %v", l, s, err)
		}
	}
}

func TestReadToFileTableHeaderOnce(t *testing.T) {
	fakeClock(t)
	path := filepath.Join(t.TempDir(), "samples.txt")

	for i := 0; i < 3; i++ {
		withSources(t, &counterSource{readings: []uint64{0, 1_000_000}})
		run(t, "read", "-f", path)
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

func TestReadToFileBadPath(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1}})

	_, err := runErr("read", "-f", filepath.Join(t.TempDir(), "no-such-dir", "x.jsonl"))
	if err == nil {
		t.Error("want an error for a file in a missing folder")
	}
}

// zoneSource is a counterSource that also has zones (like RAPL).
type zoneSource struct {
	*counterSource
	zoneReadings [][]devices.ZoneReading
	zoneCalls    int
}

func (z *zoneSource) ReadZones() ([]devices.ZoneReading, error) {
	r := z.zoneReadings[z.zoneCalls]
	z.zoneCalls++
	return r, nil
}

func laptopZones() *zoneSource {
	return &zoneSource{
		counterSource: &counterSource{readings: []uint64{0, 12_000_000}}, // 12 J
		zoneReadings: [][]devices.ZoneReading{
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

func TestReadZonesByDefaultJSON(t *testing.T) {
	fakeClock(t)
	withSources(t, laptopZones())

	got := readSample(t)

	want := []devices.ZoneSample{
		{ID: "intel-rapl:0", Name: "package-0", EnergyJ: 12, PowerW: 12},
		{ID: "intel-rapl:0:0", Name: "core", Parent: "intel-rapl:0", EnergyJ: 5, PowerW: 5},
	}
	if !reflect.DeepEqual(got.Zones, want) {
		t.Errorf("zones = %+v\nwant    %+v", got.Zones, want)
	}
}

func TestReadZonesSeparateTable(t *testing.T) {
	fakeClock(t)
	withSources(t, laptopZones())

	out := run(t, "read")

	parts := strings.Split(strings.TrimSpace(out), "\n\n")
	if len(parts) != 2 {
		t.Fatalf("want total table and zones table separated by a blank line:\n%s", out)
	}
	assertContains(t, parts[0], "DEVICE", "cpu", "12.000000")
	assertContains(t, parts[1], "ZONE", "NAME", "PARENT",
		"intel-rapl:0 ", "package-0", "intel-rapl:0:0", "core", "5.000000")
	if strings.Contains(parts[0], "core") {
		t.Errorf("zones must not be in the total table:\n%s", parts[0])
	}
}

func TestReadNoZonesForPlainSource(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1_000_000}})

	if got := readSample(t); got.Zones != nil {
		t.Errorf("source without zones: Zones = %+v, want nil", got.Zones)
	}
	withSources(t, &counterSource{readings: []uint64{0, 1_000_000}}) // fresh readings
	out := run(t, "read", "-o", "json")
	if strings.Contains(out, "zones") {
		t.Errorf(`JSON should omit "zones" when there are none: %s`, out)
	}
}
