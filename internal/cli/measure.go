package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"sathiraumesh/ampere/internal/devices"
	"sathiraumesh/ampere/internal/utils"
)

// sleep and now are variables so tests can run without waiting.
var (
	sleep = time.Sleep
	now   = time.Now
)

// measureCommand builds "ampere measure": measure a device's energy and power over
// an interval, using its preferred source or the one chosen with --source.
// With --pid it also splits that energy into active, idle and the process's
// share (see measure_pid.go).
func measureCommand() *cobra.Command {
	var (
		interval time.Duration
		device   string
		source   string
		output   string
		file     string
		count    int
		pid      int
	)

	cmd := &cobra.Command{
		Use:   "measure",
		Short: "Measure a device's energy and power, or one process's share with --pid",
		Long: `Measure a device's energy and average power over an interval.

With --pid, the energy of the total and of every zone is split by CPU time:
  active energy  = energy × active CPU ticks / (active + idle ticks)
  idle energy    = energy × idle CPU ticks   / (active + idle ticks)
  process energy = active energy × process CPU ticks / active CPU ticks`,
		Example: "  sudo ampere measure\n" +
			"  sudo ampere measure --interval 5s\n" +
			"  sudo ampere measure --interval 1s --count 20\n" +
			"  sudo ampere measure --device cpu --source rapl\n" +
			"  sudo ampere measure -o json -f samples.jsonl\n" +
			"  sudo ampere measure --pid 1234\n" +
			"  sudo ampere measure --pid 1234 -i 1s -n 20 -o json -f pid.jsonl",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkSampling(interval, count, output); err != nil {
				return err
			}
			if pid < 0 || (cmd.Flags().Changed("pid") && pid == 0) {
				return fmt.Errorf("--pid must be a positive number, got %d", pid)
			}

			src, err := deviceStatus().Pick(device, source)
			if err != nil {
				return err
			}

			// Zones are included whenever the source has them (it implements
			// ZoneReader); otherwise zr stays nil and only the total is shown.
			zr, _ := src.(devices.ZoneReader)

			// write prints one sample (or appends it to --file); header is
			// true for the first one, so a table's rows line up under it.
			write := func(header bool, sample func(w io.Writer, header bool) error) error {
				if file == "" {
					return sample(cmd.OutOrStdout(), header)
				}
				return utils.AppendTo(file, sample)
			}

			// Each interval starts at the previous interval's end reading, so
			// back-to-back samples cover the whole run with no gaps.
			prev, err := takeSnapshot(pid, src, zr)
			if err != nil {
				return err
			}
			for i := 0; i < count; i++ {
				sleep(interval)
				cur, err := takeSnapshot(pid, src, zr)
				if err != nil {
					return err
				}
				// sample writes this interval's result: the device sample, or
				// with --pid the process sample.
				var sample func(w io.Writer, header bool) error
				if pid == 0 {
					s, err := makeSample(src, zr != nil, prev.energy, cur.energy)
					if err != nil {
						return fmt.Errorf("%s: %w", src.Name(), err)
					}
					sample = func(w io.Writer, header bool) error {
						return utils.WriteSample(w, output, s, header)
					}
				} else {
					s, err := makePidSample(src, zr != nil, prev, cur)
					if err != nil {
						return err
					}
					sample = func(w io.Writer, header bool) error {
						return utils.WriteProcessSample(w, output, s, header)
					}
				}
				if err := write(i == 0, sample); err != nil {
					return err
				}
				prev = cur
			}
			return nil
		},
	}

	cmd.Flags().DurationVarP(&interval, "interval", "i", 1*time.Second,
		"length of each sample (e.g. 500ms, 1s, 5s)")
	cmd.Flags().IntVarP(&count, "count", "n", 1,
		"number of samples to take, one per interval, back to back")
	cmd.Flags().StringVarP(&device, "device", "d", devices.CPU,
		"device to measure, e.g. cpu")
	cmd.Flags().StringVarP(&source, "source", "s", "",
		"energy source of that device, e.g. rapl (default: first available)")
	cmd.Flags().StringVarP(&output, "output", "o", utils.OutputTable,
		"output format: table or json")
	cmd.Flags().StringVarP(&file, "file", "f", "",
		"append the samples to this file instead of printing them (created if missing)")
	cmd.Flags().IntVarP(&pid, "pid", "p", 0,
		"also estimate this process's energy: active, idle and its share, per zone")

	return cmd
}

// checkSampling validates the sampling flags.
func checkSampling(interval time.Duration, count int, output string) error {
	if interval <= 0 {
		return fmt.Errorf("--interval must be > 0, got %s", interval)
	}
	if count < 1 {
		return fmt.Errorf("--count must be >= 1, got %d", count)
	}
	if output != utils.OutputTable && output != utils.OutputJSON {
		return fmt.Errorf("--output must be %q or %q, got %q", utils.OutputTable, utils.OutputJSON, output)
	}
	return nil
}

// reading is the total counter and zone counters at one moment.
type reading struct {
	energyUJ uint64
	at       time.Time
	zones    []devices.ZoneReading
}

// takeReading reads the total counter and, if zr is not nil, all zones.
func takeReading(src devices.Source, zr devices.ZoneReader) (reading, error) {
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
func makeSample(src devices.Source, withZones bool, a, b reading) (devices.Sample, error) {
	// The raw counter wraps to 0; a smaller second reading means it
	// wrapped during the interval and the difference is not usable.
	if b.energyUJ < a.energyUJ {
		return devices.Sample{}, fmt.Errorf("energy counter wrapped during the interval, try again")
	}

	dt := b.at.Sub(a.at).Seconds()
	joules := float64(b.energyUJ-a.energyUJ) / 1e6 // µJ → J

	s := devices.Sample{
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
		if s.Zones, err = devices.ZoneSamples(a.zones, b.zones, dt); err != nil {
			return devices.Sample{}, err
		}
	}
	return s, nil
}

// readZones reads all zones, or nothing if zr is nil (--zones not set).
func readZones(zr devices.ZoneReader) ([]devices.ZoneReading, error) {
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
