package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// Build info, set at build time by the Makefile with
// -ldflags "-X sathiraumesh/ampere/internal/cli.Version=...".
// A plain "go build" keeps the defaults.
var (
	Version   = "dev"     // e.g. "v0.1.0" (from git tags)
	Commit    = "unknown" // short git commit hash
	BuildDate = "unknown" // UTC, RFC 3339
)

// versionString is what "ampere --version" prints, e.g.
// "ampere v0.1.0 (commit a1b2c3d, built 2026-11-20T10:00:00Z, go1.26.2 linux/amd64)".
func versionString() string {
	return fmt.Sprintf("ampere %s (commit %s, built %s, %s %s/%s)",
		Version, Commit, BuildDate, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

func RootCommand() *cobra.Command {

	root := &cobra.Command{
		Use:   "ampere",
		Short: "Estimate the energy used by processes (and later containers)",
		Long: `ampere combines RAPL energy counters (/sys/class/powercap) with CPU
time from /proc to estimate how much energy each process uses.`,
		SilenceUsage: true,    // don't print usage on runtime errors, only on bad args
		Version:      Version, // enables "ampere --version" / "-v"
	}

	// Print the full build info for --version instead of just "ampere version dev".
	root.SetVersionTemplate(versionString() + "\n")
	root.Flags().BoolP("version", "v", false, "print version and build info")

	root.AddCommand(
		statusCommand(),
		measureCommand(),
	)

	return root
}
