package cpu

import (
	"testing"

	"sathiraumesh/ampere/internal/devices"
	"sathiraumesh/ampere/internal/devices/cpu/rapl"
)

// Compile-time check: if a method is missing or has the wrong signature,
// the build fails here.
var _ devices.Source = (*rapl.Source)(nil)

func TestStatusListsRAPL(t *testing.T) {
	sources := Status()
	if len(sources) == 0 || sources[0].Name() != "rapl" {
		t.Fatalf("first source should be rapl, got %+v", sources)
	}
	for _, s := range sources {
		if s.Device() != devices.CPU {
			t.Errorf("%s: Device() = %q, want %q", s.Name(), s.Device(), devices.CPU)
		}
	}
}
