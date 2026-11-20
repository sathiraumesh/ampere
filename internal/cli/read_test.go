package cli

import (
	"errors"
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

	out := run(t, "read")

	assertContains(t, out,
		"device:    cpu",
		"source:    fake",
		"interval:  1.000000 s",
		"energy:    12.000000 J",
		"power:     12.00 W",
	)
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

	out := run(t, "read", "--interval", "5s")

	assertContains(t, out, "interval:  5.000000 s", "energy:    50.000000 J", "power:     10.00 W")
}

func TestReadUsesPreferredSource(t *testing.T) {
	fakeClock(t)
	withSources(t,
		fakeSource{name: "rapl", err: errors.New("not here")}, // unavailable
		&counterSource{readings: []uint64{0, 1_000_000}},      // used
	)

	out := run(t, "read")

	assertContains(t, out, "source:    fake")
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

	out := run(t, "read", "--source", "hwmon")

	assertContains(t, out, "source:    hwmon", "energy:    2.000000 J")
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

	out := run(t, "read", "--device", "gpu")

	assertContains(t, out, "device:    gpu", "energy:    3.000000 J")
}

func TestReadUnknownDevice(t *testing.T) {
	fakeClock(t)
	withSources(t, &counterSource{readings: []uint64{0, 1}})

	_, err := runErr("read", "-d", "gpu")
	if err == nil || !strings.Contains(err.Error(), `unknown device "gpu" (known: cpu)`) {
		t.Errorf("err = %v, want unknown device", err)
	}
}
