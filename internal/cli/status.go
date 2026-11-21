package cli

import (
	"fmt"

	"sathiraumesh/ampere/internal/devices/registry"
	"sathiraumesh/ampere/internal/output"

	"github.com/spf13/cobra"
)

// deviceStatus gathers every energy source of every device. It is a
// variable so tests can replace it with a fake report.
var deviceStatus = registry.Status

// statusCommand builds "ampere status": check which energy sources are
// available for every device on this machine.
func statusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check which energy sources are available for each device",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			report := deviceStatus()

			types := report.Types()
			if len(types) == 0 {
				fmt.Fprintln(out, "No energy sources found.")
				return nil
			}
			for i, device := range types {
				if i > 0 {
					fmt.Fprintln(out)
				}
				if err := output.WriteDevice(out, device, report); err != nil {
					return err
				}
			}
			return nil
		},
	}
}
