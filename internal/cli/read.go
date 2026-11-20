package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"sathiraumesh/ampere/internal/devices"
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
	)

	cmd := &cobra.Command{
		Use:   "read",
		Short: "Read a device's energy and average power over an interval",
		Example: "  sudo ampere read\n" +
			"  sudo ampere read --interval 5s\n" +
			"  sudo ampere read --device cpu --source rapl",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval <= 0 {
				return fmt.Errorf("--interval must be > 0, got %s", interval)
			}

			src, err := deviceStatus().Pick(device, source)
			if err != nil {
				return err
			}

			// Take both readings the same way, so any read delay cancels out.
			e1, t1, err := readAt(src.Read)
			if err != nil {
				return fmt.Errorf("%s: %w (energy is root-only: try sudo)", src.Name(), err)
			}
			sleep(interval)
			e2, t2, err := readAt(src.Read)
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

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "device:    %s\n", src.Device())
			fmt.Fprintf(out, "source:    %s\n", src.Name())
			fmt.Fprintf(out, "interval:  %.6f s\n", dt)
			fmt.Fprintf(out, "energy:    %.6f J\n", joules)
			fmt.Fprintf(out, "power:     %.2f W\n", joules/dt)
			return nil
		},
	}

	cmd.Flags().DurationVarP(&interval, "interval", "i", 1*time.Second,
		"time between the two readings (e.g. 500ms, 1s, 5s)")
	cmd.Flags().StringVarP(&device, "device", "d", devices.CPU,
		"device to measure, e.g. cpu")
	cmd.Flags().StringVarP(&source, "source", "s", "",
		"energy source of that device, e.g. rapl (default: first available)")

	return cmd
}

// readAt reads the energy counter and records when it was read.
func readAt(read func() (uint64, error)) (uint64, time.Time, error) {
	t := now()
	e, err := read()
	return e, t, err
}
