package cli

import (
	"fmt"
	"text/tabwriter"

	"sathiraumesh/ampere/internal/devices"
	"sathiraumesh/ampere/internal/devices/cpu"
	"sathiraumesh/ampere/internal/devices/cpu/rapl"

	"github.com/spf13/cobra"
)

// deviceStatus gathers the device report. It is a variable so tests can
// replace it with a fake report instead of reading the real machine.
var deviceStatus = devices.Status

// statusCommand builds "ampere status": check which energy and CPU sources
// are available on this machine.
func statusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check which energy and CPU-time sources are available",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {

			report := deviceStatus()
			out := cmd.OutOrStdout()

			fmt.Fprintln(out, "CPU energy sources:")
			for _, s := range report.CPU.Sources {
				if !cpu.Available(s) {
					fmt.Fprintf(out, " %s: %v\n", s.Name(), s.Err())
					continue
				}
				// Details depend on the concrete implementation.
				switch src := s.(type) {
				case *rapl.Source:
					fmt.Fprintf(out, " %s (%d zones)\n", s.Name(), len(src.Zones))
					w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
					fmt.Fprintln(w, "      ID\tNAME\tPARENT\tMAX (µJ)\tREADABLE")
					for _, z := range src.Zones {
						parent := z.ParentID
						if parent == "" {
							parent = "-"
						}
						fmt.Fprintf(w, "      %s\t%s\t%s\t%d\t%v\n",
							z.ID, z.Name, parent, z.MaxEnergyRangeUJ, z.Readable())
					}
					w.Flush()
				default:
					// A source without a details printer yet: show just the name.
					fmt.Fprintf(out, " %s\n", s.Name())
				}
			}

			if p := report.CPU.Preferred(); p != nil {
				fmt.Fprintln(out, "Using:", p.Name())
			} else {
				fmt.Fprintln(out, "Using: none (no CPU energy source available)")
			}
			return nil
		},
	}
}
