package cpu

import (
	"testing"

	"sathiraumesh/ampere/internal/devices/cpu/rapl"
)

// Compile-time check: *rapl.Source must implement Source.
// If a method is missing or has the wrong signature, the build fails here.
var _ Source = (*rapl.Source)(nil)

func TestStatusListsRAPL(t *testing.T) {
	r := Status()
	if len(r.Sources) == 0 || r.Sources[0].Name() != "rapl" {
		t.Fatalf("first source should be rapl, got %+v", r.Sources)
	}
}
