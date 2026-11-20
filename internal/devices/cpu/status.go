// Package cpu lists the CPU energy sources: today only RAPL; later e.g. the
// AMD energy driver (hwmon), perf events, or a model-based estimate.
package cpu

import (
	"sathiraumesh/ampere/internal/devices"
	"sathiraumesh/ampere/internal/devices/cpu/rapl"
)

// Status checks every CPU energy source, highest priority first.
// Each source reports devices.CPU as its Device(). It only looks; it does
// not print.
func Status() []devices.Source {
	return []devices.Source{
		rapl.NewSource(),
		//TODO: hwmon.NewSource(),
	}
}
