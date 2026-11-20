// Package cpu reports CPU energy, independent of where it comes from.
// Today the only source is RAPL; later it could be e.g. the AMD energy
// driver (hwmon), perf events, or a model-based estimate.
package cpu

import "sathiraumesh/ampere/internal/devices/cpu/rapl"

// Source is any CPU energy implementation. Each one lives in its own
// package (rapl, later hwmon, ...) and keeps its own details there.
type Source interface {
	// Name identifies the implementation, e.g. "rapl".
	Name() string

	// Err is nil if the source is available, otherwise the reason it is not.
	Err() error

	// Read returns the current CPU energy counter in µJ (raw, no state).
	// Energy over an interval = Read() after − Read() before; the counter
	// can wrap to 0, so the caller must handle after < before.
	Read() (energyUJ uint64, err error)
}

// Available reports whether s was found on this machine.
func Available(s Source) bool { return s.Err() == nil }

// Report lists every CPU energy source ampere knows, in priority order,
// available or not.
type Report struct {
	Sources []Source
}

// Preferred returns the first available source (the one to measure with),
// or nil if none is available. Sources must not be summed: on AMD, RAPL
// and hwmon read the same hardware counter.
func (r Report) Preferred() Source {
	for _, s := range r.Sources {
		if Available(s) {
			return s
		}
	}
	return nil
}

// Status checks every CPU energy source. It only looks; it does not print.
func Status() Report {
	return Report{
		Sources: []Source{
			rapl.NewSource(),
			//TODO: hwmon.NewSource(),
		},
	}
}
