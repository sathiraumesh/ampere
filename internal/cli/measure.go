package cli

import (
	"fmt"
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
func measureCommand() *cobra.Command {
	var (
		interval time.Duration
		device   string
		source   string
		output   string
		file     string
		count    int
	)

	cmd := &cobra.Command{
		Use:   "measure",
		Short: "Measure a device's energy and average power over an interval",
		Example: "  sudo ampere measure\n" +
			"  sudo ampere measure --interval 5s\n" +
			"  sudo ampere measure --interval 1s --count 20\n" +
			"  sudo ampere measure --device cpu --source rapl\n" +
			"  sudo ampere measure -o json\n" +
			"  sudo ampere measure -o json -f samples.jsonl",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval <= 0 {
				return fmt.Errorf("--interval must be > 0, got %s", interval)
			}
			if output != utils.OutputTable && output != utils.OutputJSON {
				return fmt.Errorf("--output must be %q or %q, got %q", utils.OutputTable, utils.OutputJSON, output)
			}

			if count < 1 {
				return fmt.Errorf("--count must be >= 1, got %d", count)
			}

			src, err := deviceStatus().Pick(device, source)
			if err != nil {
				return err
			}

			// Zones are included whenever the source has them (it implements
			// ZoneReader); otherwise zr stays nil and only the total is shown.
			zr, _ := src.(devices.ZoneReader)

			// Each interval starts at the previous interval's end reading, so
			// back-to-back samples cover the whole run with no gaps.
			prev, err := takeReading(src, zr)
			if err != nil {
				return fmt.Errorf("%s: %w (energy is root-only: try sudo)", src.Name(), err)
			}
			for i := 0; i < count; i++ {
				sleep(interval)
				cur, err := takeReading(src, zr)
				if err != nil {
					return fmt.Errorf("%s: %w", src.Name(), err)
				}
				s, err := makeSample(src, zr != nil, prev, cur)
				if err != nil {
					return fmt.Errorf("%s: %w", src.Name(), err)
				}
				if file == "" {
					// Table header only on the first sample, so rows line up.
					err = utils.WriteSample(cmd.OutOrStdout(), output, s, i == 0)
				} else {
					err = utils.AppendSample(file, output, s)
				}
				if err != nil {
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
		"append the sample to this file instead of printing it (created if missing)")

	return cmd
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
