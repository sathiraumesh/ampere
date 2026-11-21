// Package registry lists the energy sources of every device on this
// machine. It is the one place where device types are wired in: add e.g.
// gpu.Status() here later.
//
// It is its own package because the device packages (cpu, later gpu, ...)
// import devices, so devices cannot import them.
package registry

import (
	"sathiraumesh/ampere/internal/devices"
	"sathiraumesh/ampere/internal/devices/cpu"
)

// Status checks every energy source of every device, in priority order
// within each device.
func Status() devices.Report {
	var sources []devices.Source
	sources = append(sources, cpu.Status()...)
	//TODO: sources = append(sources, gpu.Status()...)
	return devices.Report{Sources: sources}
}
