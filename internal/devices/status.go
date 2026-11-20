// Package devices collects the status of every energy source ampere can use
// (CPU now; later e.g. DRAM, GPU, BMC/Redfish).
package devices

import "sathiraumesh/ampere/internal/devices/cpu"

// Report is the status of all devices.
type Report struct {
	CPU cpu.Report
	// later: GPU gpu.Report, Platform redfish.Report, ...
}

// Status checks every device. It only looks; it does not print.
func Status() Report {
	return Report{
		CPU: cpu.Status(),
	}
}
