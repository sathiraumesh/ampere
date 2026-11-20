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

// readCommand builds "ampere read": measure a device's energy and power over
// an interval, using its preferred source or the one chosen with --source.
func readCommand() *cobra.Command {
	var (
		interval time.Duration
		device   string
		source   string
		output   string
		file     string
	)

	cmd := &cobra.Command{
		Use:   "read",
		Short: "Read a device's energy and average power over an interval",
		Example: "  sudo ampere read\n" +
			"  sudo ampere read --interval 5s\n" +
			"  sudo ampere read --device cpu --source rapl\n" +
			"  sudo ampere read -o json\n" +
			"  sudo ampere read -o json -f samples.jsonl",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval <= 0 {
				return fmt.Errorf("--interval must be > 0, got %s", interval)
			}
			if output != utils.OutputTable && output != utils.OutputJSON {
				return fmt.Errorf("--output must be %q or %q, got %q", utils.OutputTable, utils.OutputJSON, output)
			}

			src, err := deviceStatus().Pick(device, source)
			if err != nil {
				return err
			}

			// Zones are included whenever the source has them (it implements
			// ZoneReader); otherwise zr stays nil and only the total is shown.
			zr, _ := src.(devices.ZoneReader)

			// Take both readings the same way, so any read delay cancels out.
			e1, t1, err := readAt(src.Read)
			if err != nil {
				return fmt.Errorf("%s: %w (energy is root-only: try sudo)", src.Name(), err)
			}
			z1, err := readZones(zr)
			if err != nil {
				return fmt.Errorf("%s: %w", src.Name(), err)
			}
			sleep(interval)
			e2, t2, err := readAt(src.Read)
			if err != nil {
				return fmt.Errorf("%s: %w", src.Name(), err)
			}
			z2, err := readZones(zr)
			if err != nil {
				return fmt.Errorf("%s: %w", src.Name(), err)
			}

			// The raw counter wraps to 0; a smaller second reading means it
			// wrapped during the interval and the difference is not usable.
			if e2 < e1 {
				return fmt.Errorf("%s: energy counter wrapped during the interval, try again", src.Name())
			}

			dt := t2.Sub(t1).Seconds()
			joules := float64(e2-e1) / 1e6 // µJ → J

			s := devices.Sample{
				Device:    src.Device(),
				Source:    src.Name(),
				Start:     t1.UTC(),
				End:       t2.UTC(),
				IntervalS: dt,
				EnergyJ:   joules,
				PowerW:    joules / dt,
			}
			if zr != nil {
				if s.Zones, err = devices.ZoneSamples(z1, z2, dt); err != nil {
					return fmt.Errorf("%s: %w", src.Name(), err)
				}
			}
			if file == "" {
				return utils.WriteSample(cmd.OutOrStdout(), output, s, true)
			}
			return utils.AppendSample(file, output, s)
		},
	}

	cmd.Flags().DurationVarP(&interval, "interval", "i", 1*time.Second,
		"time between the two readings (e.g. 500ms, 1s, 5s)")
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
