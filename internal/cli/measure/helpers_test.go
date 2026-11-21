package measure

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"sathiraumesh/ampere/internal/devices"
)

// withSources makes the command see only the given sources.
func withSources(t *testing.T, sources ...devices.Source) {
	t.Helper()
	old := deviceStatus
	t.Cleanup(func() { deviceStatus = old })
	deviceStatus = func() devices.Report { return devices.Report{Sources: sources} }
}

// newRoot is a stand-in "ampere" root with only "measure" under it, so
// tests run the command as users type it: "measure process 7 -o json".
func newRoot() *cobra.Command {
	root := &cobra.Command{Use: "ampere", SilenceUsage: true}
	root.AddCommand(Command())
	return root
}

// run executes the command and returns its output, failing on error.
func run(t *testing.T, args ...string) string {
	t.Helper()
	out, err := runErr(args...)
	if err != nil {
		t.Fatalf("ampere %v: %v\n%s", args, err, out)
	}
	return out
}

// runErr executes the command and returns its output and error.
func runErr(args ...string) (string, error) {
	var out bytes.Buffer
	cmd := newRoot()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// fakeSource is a source that is unavailable when err is set.
type fakeSource struct {
	name string
	err  error
}

func (f fakeSource) Name() string          { return f.name }
func (f fakeSource) Device() string        { return devices.CPU }
func (f fakeSource) Err() error            { return f.err }
func (f fakeSource) Read() (uint64, error) { return 0, f.err }

// assertContains fails for every want that is not in got.
func assertContains(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("output missing %q\n--- output ---\n%s", w, got)
		}
	}
}
