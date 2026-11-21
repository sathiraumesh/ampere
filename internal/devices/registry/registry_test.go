package registry

import (
	"testing"

	"sathiraumesh/ampere/internal/devices"
)

func TestStatusHasCPU(t *testing.T) {
	r := Status()
	if len(r.For(devices.CPU)) == 0 {
		t.Errorf("no cpu sources in %+v", r.Types())
	}
}
