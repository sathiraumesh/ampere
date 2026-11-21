// Package measure is the "ampere measure" command and its subcommands
// "measure process" and "measure container".
package measure

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"sathiraumesh/ampere/internal/devices"
	"sathiraumesh/ampere/internal/devices/registry"
	"sathiraumesh/ampere/internal/utils"
)

// These are variables so tests can run without waiting (sleep, now) and
// with fake energy sources (deviceStatus).
var (
	sleep        = time.Sleep
	now          = time.Now
	deviceStatus = registry.Status
)

// measureOptions are the flags shared by "measure" and its subcommands
// "measure process" and "measure container" (persistent flags).
type measureOptions struct {
	interval time.Duration
	device   string
	source   string
	output   string
	file     string
	count    int
}

// Command builds "ampere measure": measure a device's energy and power over
// an interval, using its preferred source or the one chosen with --source.
// Its subcommands "process" and "container" also split that energy into
// active, idle and the target's share (see target.go).
func Command() *cobra.Command {
	var o measureOptions

	cmd := &cobra.Command{
		Use:   "measure",
		Short: "Measure a device's energy and power over an interval",
		Long: `Measure a device's energy and average power over an interval.

Use "measure process <pid>" or "measure container <id>" for the share of
one process or container.`,
		Example: "  sudo ampere measure\n" +
			"  sudo ampere measure --interval 5s\n" +
			"  sudo ampere measure --interval 1s --count 20\n" +
			"  sudo ampere measure --device cpu --source rapl\n" +
			"  sudo ampere measure -o json -f samples.jsonl",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.run(cmd, nil)
		},
	}

	f := cmd.PersistentFlags()
	f.DurationVarP(&o.interval, "interval", "i", 1*time.Second,
		"length of each sample (e.g. 500ms, 1s, 5s)")
	f.IntVarP(&o.count, "count", "n", 1,
		"number of samples to take, one per interval, back to back")
	f.StringVarP(&o.device, "device", "d", devices.CPU,
		"device to measure, e.g. cpu")
	f.StringVarP(&o.source, "source", "s", "",
		"energy source of that device, e.g. rapl (default: first available)")
	f.StringVarP(&o.output, "output", "o", utils.OutputTable,
		"output format: table or json")
	f.StringVarP(&o.file, "file", "f", "",
		"append the samples to this file instead of printing them (created if missing)")

	cmd.AddCommand(
		measureProcessCommand(&o),
		measureContainerCommand(&o),
	)
	return cmd
}

// splitHelp explains the split, for the process and container subcommands.
const splitHelp = `For the total and every zone, the energy of each interval is split by
CPU time:
  active energy = energy × active CPU ticks / (active + idle ticks)
  idle energy   = energy × idle CPU ticks   / (active + idle ticks)
  target energy = active energy × target CPU time / active CPU time`

// measureProcessCommand builds "ampere measure process <pid>".
func measureProcessCommand(o *measureOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "process <pid>",
		Short: "Estimate one process's energy: active, idle and its share, per zone",
		Long:  "Estimate one process's energy over an interval.\n\n" + splitHelp,
		Example: "  sudo ampere measure process 1234\n" +
			"  sudo ampere measure process 1234 -i 1s -n 20 -o json -f pid.jsonl",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, err := strconv.Atoi(args[0])
			if err != nil || pid <= 0 {
				return fmt.Errorf("pid must be a positive number, got %q", args[0])
			}
			return o.run(cmd, &target{pid: pid})
		},
	}
}

// measureContainerCommand builds "ampere measure container <id>".
func measureContainerCommand(o *measureOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "container <id>",
		Short: "Estimate one container's energy: active, idle and its share, per zone",
		Long: "Estimate one container's energy over an interval: all its processes,\n" +
			"from its cgroup's CPU time.\n\n" +
			"<id> is a Docker, Podman or containerd (Kubernetes CRI, nerdctl)\n" +
			"container ID, full or short (as docker ps shows). Needs cgroup v2.\n\n" +
			splitHelp,
		Example: "  sudo ampere measure container 44f86868889b\n" +
			"  sudo ampere measure container 44f86868889b -i 1s -n 20 -o json -f ctr.jsonl",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkSampling(o.interval, o.count, o.output); err != nil {
				return err // before the cgroup search, so bad flags fail fast
			}
			c, err := findContainer(args[0])
			if err != nil {
				return err
			}
			return o.run(cmd, &target{container: &c})
		},
	}
}

// run measures with the options. With a target (process or container) it
// writes target samples, otherwise device samples.
func (o *measureOptions) run(cmd *cobra.Command, t *target) error {
	if err := checkSampling(o.interval, o.count, o.output); err != nil {
		return err
	}
	src, err := deviceStatus().Pick(o.device, o.source)
	if err != nil {
		return err
	}

	// Zones are included whenever the source has them (it implements
	// ZoneReader); otherwise zr stays nil and only the total is shown.
	zr, _ := src.(devices.ZoneReader)

	// write prints one sample (or appends it to --file); header is true for
	// the first one, so a table's rows line up under it.
	write := func(header bool, sample func(w io.Writer, header bool) error) error {
		if o.file == "" {
			return sample(cmd.OutOrStdout(), header)
		}
		return utils.AppendTo(o.file, sample)
	}

	// Each interval starts at the previous interval's end reading, so
	// back-to-back samples cover the whole run with no gaps.
	prev, err := takeSnapshot(t, src, zr)
	if err != nil {
		return err
	}
	for i := 0; i < o.count; i++ {
		sleep(o.interval)
		cur, err := takeSnapshot(t, src, zr)
		if err != nil {
			return err
		}
		// sample writes this interval's result: the device sample, or the
		// target's sample.
		var sample func(w io.Writer, header bool) error
		if t == nil {
			s, err := makeSample(src, zr != nil, prev.energy, cur.energy)
			if err != nil {
				return fmt.Errorf("%s: %w", src.Name(), err)
			}
			sample = func(w io.Writer, header bool) error {
				return utils.WriteSample(w, o.output, s, header)
			}
		} else {
			s, err := makeTargetSample(src, zr != nil, t, prev, cur)
			if err != nil {
				return err
			}
			sample = func(w io.Writer, header bool) error {
				return utils.WriteTargetSample(w, o.output, s, header)
			}
		}
		if err := write(i == 0, sample); err != nil {
			return err
		}
		prev = cur
	}
	return nil
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
