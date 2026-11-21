package measure

import "github.com/spf13/cobra"

// measureContainerCommand builds "ampere measure container <id>": one
// container's share of the energy, from its cgroup's CPU time (all its
// processes).
func measureContainerCommand(opts *measureOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "container <id>",
		Short: "Estimate one container's energy (its share of the total and each zone)",
		Long: "Estimate one container's energy over an interval: all its processes,\n" +
			"from its cgroup's CPU time.\n\n" +
			"<id> is a Docker, Podman or containerd (Kubernetes CRI, nerdctl)\n" +
			"container ID, full or short (as docker ps shows). Needs cgroup v2.\n\n" +
			splitHelp,
		Example: "  sudo ampere measure container 44f86868889b\n" +
			"  sudo ampere measure container 44f86868889b -i 1s -n 20 -o json -f ctr.jsonl",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkSampling(opts.interval, opts.count, opts.output); err != nil {
				return err // before the cgroup search, so bad flags fail fast
			}
			c, err := findContainer(args[0])
			if err != nil {
				return err
			}
			return opts.run(cmd, &target{container: &c})
		},
	}
}
