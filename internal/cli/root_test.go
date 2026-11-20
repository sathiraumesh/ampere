package cli

import (
	"bytes"
	"strings"
	"testing"
)

// run executes the root command with args and returns what it printed.
func run(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	cmd := RootCommand()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("ampere %v: %v\n%s", args, err, out.String())
	}
	return out.String()
}

func TestVersionFlag(t *testing.T) {
	want := versionString()

	for _, flag := range []string{"--version", "-v"} {
		t.Run(flag, func(t *testing.T) {
			got := strings.TrimSpace(run(t, flag))
			if got != want {
				t.Errorf("ampere %s = %q, want %q", flag, got, want)
			}
		})
	}
}
