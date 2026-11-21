// Package measure is the "ampere measure" command and its subcommands
// "measure process" and "measure container".
package measure

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"sathiraumesh/ampere/internal/devices"
	"sathiraumesh/ampere/internal/devices/cpu/rapl"
	"sathiraumesh/ampere/internal/devices/registry"
	"sathiraumesh/ampere/internal/host/attribution/energy"
	"sathiraumesh/ampere/internal/output"
)

// These are variables so tests can run without waiting (sleep, now) and
// with fake energy sources (deviceStatus).
var (
	sleep        = time.Sleep
	now          = time.Now
	deviceStatus = registry.Status
)

// measureOptions are the flags shared by "measure" and its subcommands
// "measure process" and "measure container" (persistent flags).
type measureOptions struct {
	interval time.Duration
	device   string
	source   string
	output   string
	file     string
	count    int
}

// Command builds "ampere measure": measure a device's energy and power over
// an interval, using its preferred source or the one chosen with --source.
// Its subcommands "process" and "container" give one target's share of
// that energy (shared code in helpers.go).
func Command() *cobra.Command {
	var opts measureOptions

	cmd := &cobra.Command{
		Use:   "measure",
		Short: "Measure a device's energy and power over an interval",
		Long: `Measure a device's energy and average power over an interval.

Use "measure process <pid>" or "measure container <id>" for the share of
one process or container.`,
		Example: "  sudo ampere measure\n" +
			"  sudo ampere measure --interval 5s\n" +
			"  sudo ampere measure --interval 1s --count 20\n" +
			"  sudo ampere measure --device cpu --source rapl\n" +
			"  sudo ampere measure -o json -f samples.jsonl",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run(cmd, nil)
		},
	}

	f := cmd.PersistentFlags()
	f.DurationVarP(&opts.interval, "interval", "i", 1*time.Second,
		"length of each sample (e.g. 500ms, 1s, 5s)")
	f.IntVarP(&opts.count, "count", "n", 1,
		"number of samples to take, one per interval, back to back")
	f.StringVarP(&opts.device, "device", "d", devices.CPU,
		"device to measure, e.g. cpu")
	f.StringVarP(&opts.source, "source", "s", "",
		"energy source of that device, e.g. rapl (default: first available)")
	f.StringVarP(&opts.output, "output", "o", output.Table,
		"output format: table or json")
	f.StringVarP(&opts.file, "file", "f", "",
		"append the samples to this file instead of printing them (created if missing)")

	cmd.AddCommand(
		measureProcessCommand(&opts),
		measureContainerCommand(&opts),
	)
	return cmd
}

// run measures with the options. With a target (process or container) it
// writes target samples, otherwise device samples.
func (opts *measureOptions) run(cmd *cobra.Command, t *target) error {
	if err := checkSampling(opts.interval, opts.count, opts.output); err != nil {
		return err
	}
	src, err := deviceStatus().Pick(opts.device, opts.source)
	if err != nil {
		return err
	}

	// Zones are included whenever the source has them (it implements
	// ZoneReader); otherwise zr stays nil and only the total is shown.
	zr, _ := src.(rapl.ZoneReader)

	// write prints one sample (or appends it to --file); header is true for
	// the first one, so a table's rows line up under it.
	write := func(header bool, sample func(w io.Writer, header bool) error) error {
		if opts.file == "" {
			return sample(cmd.OutOrStdout(), header)
		}
		return output.AppendTo(opts.file, sample)
	}

	// Each interval starts at the previous interval's end reading, so
	// back-to-back samples cover the whole run with no gaps.
	prev, err := takeSnapshot(t, src, zr)
	if err != nil {
		return err
	}
	for i := 0; i < opts.count; i++ {
		sleep(opts.interval)
		cur, err := takeSnapshot(t, src, zr)
		if err != nil {
			return err
		}
		// sample writes this interval's result: the device sample, or the
		// target's sample.
		var sample func(w io.Writer, header bool) error
		if t == nil {
			s, err := makeSample(src, zr != nil, prev.energy, cur.energy)
			if err != nil {
				return fmt.Errorf("%s: %w", src.Name(), err)
			}
			sample = func(w io.Writer, header bool) error {
				return output.WriteSample(w, opts.output, s, header)
			}
		} else {
			s, err := makeTargetSample(src, zr != nil, t, prev, cur)
			if err != nil {
				return err
			}
			sample = func(w io.Writer, header bool) error {
				return output.WriteTargetSample(w, opts.output, s, header)
			}
		}
		if err := write(i == 0, sample); err != nil {
			return err
		}
		prev = cur
	}
	return nil
}

// checkSampling validates the sampling flags.
func checkSampling(interval time.Duration, count int, format string) error {
	if interval <= 0 {
		return fmt.Errorf("--interval must be > 0, got %s", interval)
	}
	if count < 1 {
		return fmt.Errorf("--count must be >= 1, got %d", count)
	}
	if format != output.Table && format != output.JSON {
		return fmt.Errorf("--output must be %q or %q, got %q", output.Table, output.JSON, format)
	}
	return nil
}

// reading is the total counter and zone counters at one moment.
type reading struct {
	energyUJ uint64
	at       time.Time
	zones    []rapl.ZoneReading
}

// takeReading reads the total counter and, if zr is not nil, all zones.
func takeReading(src devices.Source, zr rapl.ZoneReader) (reading, error) {
	e, t, err := readAt(src.Read)
	if err != nil {
		return reading{}, err
	}
	z, err := readZones(zr)
	if err != nil {
		return reading{}, err
	}
	return reading{energyUJ: e, at: t, zones: z}, nil
}

// makeSample turns two readings into energy and power over the interval
// between them. withZones adds the per-zone breakdown.
func makeSample(src devices.Source, withZones bool, a, b reading) (energy.Sample, error) {
	// The raw counter wraps to 0; a smaller second reading means it
	// wrapped during the interval and the difference is not usable.
	if b.energyUJ < a.energyUJ {
		return energy.Sample{}, fmt.Errorf("energy counter wrapped during the interval, try again")
	}

	dt := b.at.Sub(a.at).Seconds()
	joules := float64(b.energyUJ-a.energyUJ) / 1e6 // µJ → J

	s := energy.Sample{
		Device:    src.Device(),
		Source:    src.Name(),
		Start:     a.at.UTC(),
		End:       b.at.UTC(),
		IntervalS: dt,
		EnergyJ:   joules,
		PowerW:    joules / dt,
	}
	if withZones {
		var err error
		if s.Zones, err = rapl.ZoneSamples(a.zones, b.zones, dt); err != nil {
			return energy.Sample{}, err
		}
	}
	return s, nil
}

// readZones reads all zones, or nothing if zr is nil (--zones not set).
func readZones(zr rapl.ZoneReader) ([]rapl.ZoneReading, error) {
	if zr == nil {
		return nil, nil
	}
	return zr.ReadZones()
}

// readAt reads the energy counter and records when it was read.
func readAt(read func() (uint64, error)) (uint64, time.Time, error) {
	t := now()
	e, err := read()
	return e, t, err
}
