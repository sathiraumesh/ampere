package measure

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
)

// measureProcessCommand builds "ampere measure process <pid>": one
// process's share of the energy, from its CPU time in /proc/<pid>/stat.
func measureProcessCommand(opts *measureOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "process <pid>",
		Short: "Estimate one process's energy (its share of the total and each zone)",
		Long:  "Estimate one process's energy over an interval.\n\n" + splitHelp,
		Example: "  sudo ampere measure process 1234\n" +
			"  sudo ampere measure process 1234 -i 1s -n 20 -o json -f pid.jsonl",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, err := strconv.Atoi(args[0])
			if err != nil || pid <= 0 {
				return fmt.Errorf("pid must be a positive number, got %q", args[0])
			}
			return opts.run(cmd, &target{pid: pid})
		},
	}
}
