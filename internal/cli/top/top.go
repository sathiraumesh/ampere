// Package top is the "ampere top" command: it reads the machine's energy,
// every process and every container (sampler.go) and shows them live with
// the tui package.
package top

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"sathiraumesh/ampere/internal/devices"
	"sathiraumesh/ampere/internal/devices/registry"
	"sathiraumesh/ampere/internal/tui"
)

// deviceStatus gathers every energy source of every device. It is a
// variable so tests can replace it with a fake report.
var deviceStatus = registry.Status

// Command builds "ampere top".
func Command() *cobra.Command {
	var (
		interval time.Duration
		device   string
		source   string
	)
	cmd := &cobra.Command{
		Use:   "top",
		Short: "Live view of power per process and container, like top",
		Long: `Show the machine's power and every process and container, sorted by power,
updated every interval.

Each process and container gets its share of the active energy, by CPU time
(the same split as "measure process" and "measure container"). ENERGY is the
total since ampere top started.

Keys: p processes, c containers, tab switch, ↑/↓ or j/k scroll, q quit.`,
		Example: "  sudo ampere top\n" +
			"  sudo ampere top -i 2s",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval < 100*time.Millisecond {
				return fmt.Errorf("--interval must be at least 100ms, got %s", interval)
			}
			src, err := deviceStatus().Pick(device, source)
			if err != nil {
				return err
			}

			// Take the first reading before the screen opens, so errors
			// such as "permission denied" are printed normally.
			s := newSampler(src)
			if _, _, err := s.Step(); err != nil {
				return fmt.Errorf("%w (energy is root-only: try sudo)", err)
			}

			return tui.Run(s, interval, cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
	cmd.Flags().DurationVarP(&interval, "interval", "i", 1*time.Second,
		"how often to update (e.g. 500ms, 1s, 2s)")
	cmd.Flags().StringVarP(&device, "device", "d", devices.CPU,
		"device to measure, e.g. cpu")
	cmd.Flags().StringVarP(&source, "source", "s", "",
		"energy source of that device, e.g. rapl (default: first available)")
	return cmd
}
