// Package devices defines energy sources and groups them by device
// (CPU now; later e.g. GPU, DRAM, BMC/Redfish). Each device type lives in its
// own package (cpu, later gpu, ...) and returns its sources; this package
// does not import them, so they can all import it.
package devices

import (
	"errors"
	"fmt"
	"strings"
)

// Report is every energy source on this machine, for all devices, in
// priority order within each device. A source's Device() says which device
// it belongs to.
type Report struct {
	Sources []Source
}

// Types returns the device types present, in the order they first appear,
// e.g. ["cpu", "gpu"].
func (r Report) Types() []string {
	var types []string
	seen := map[string]bool{}
	for _, s := range r.Sources {
		if d := s.Device(); !seen[d] {
			seen[d] = true
			types = append(types, d)
		}
	}
	return types
}

// For returns the sources of one device, in priority order.
func (r Report) For(device string) []Source {
	var out []Source
	for _, s := range r.Sources {
		if s.Device() == device {
			out = append(out, s)
		}
	}
	return out
}

// Preferred returns the first available source of the device (the one to
// measure with), or nil if none is available. Sources of one device must
// not be summed: e.g. on AMD, RAPL and hwmon read the same hardware counter.
func (r Report) Preferred(device string) Source {
	for _, s := range r.For(device) {
		if Available(s) {
			return s
		}
	}
	return nil
}

// Find returns the source of the device with the given name, e.g. "rapl".
// It fails if the device or source is unknown, or the source is not
// available here.
func (r Report) Find(device, name string) (Source, error) {
	sources, err := r.forKnown(device)
	if err != nil {
		return nil, err
	}
	var known []string
	for _, s := range sources {
		if s.Name() == name {
			if !Available(s) {
				return nil, fmt.Errorf("%s source %q is not available: %w", device, name, s.Err())
			}
			return s, nil
		}
		known = append(known, s.Name())
	}
	return nil, fmt.Errorf("unknown %s source %q (known: %s)", device, name, strings.Join(known, ", "))
}

// Pick returns the named source of the device, or its preferred source if
// name is "".
func (r Report) Pick(device, name string) (Source, error) {
	if name != "" {
		return r.Find(device, name)
	}
	if _, err := r.forKnown(device); err != nil {
		return nil, err
	}
	if s := r.Preferred(device); s != nil {
		return s, nil
	}
	return nil, fmt.Errorf("no %s energy source available (see: ampere status)", device)
}

// forKnown is For, but fails if the device has no sources at all.
func (r Report) forKnown(device string) ([]Source, error) {
	if len(r.Sources) == 0 {
		return nil, errors.New("no energy sources known")
	}
	sources := r.For(device)
	if len(sources) == 0 {
		return nil, fmt.Errorf("unknown device %q (known: %s)", device, strings.Join(r.Types(), ", "))
	}
	return sources, nil
}
