package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"sathiraumesh/ampere/internal/devices"
	"sathiraumesh/ampere/internal/devices/cpu"
	"sathiraumesh/ampere/internal/devices/cpu/rapl"

	"github.com/spf13/cobra"
)

// deviceStatus gathers every energy source of every device. This list is
// the one place where device types are wired in; add e.g. gpu.Status()
// here later. It is a variable so tests can replace it with a fake report.
var deviceStatus = func() devices.Report {
	var sources []devices.Source
	sources = append(sources, cpu.Status()...)
	//TODO: sources = append(sources, gpu.Status()...)
	return devices.Report{Sources: sources}
}

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
				printDevice(out, device, report)
			}
			return nil
		},
	}
}

// printDevice prints one device: its sources and which one will be used.
func printDevice(out io.Writer, device string, report devices.Report) {
	fmt.Fprintf(out, "%s energy sources:\n", strings.ToUpper(device))
	for _, s := range report.For(device) {
		if !devices.Available(s) {
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

	if p := report.Preferred(device); p != nil {
		fmt.Fprintln(out, "Using:", p.Name())
	} else {
		fmt.Fprintf(out, "Using: none (no %s energy source available)\n", device)
	}
}
