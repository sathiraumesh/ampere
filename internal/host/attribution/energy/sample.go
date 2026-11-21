// Package energy holds one measurement of a device's energy over an
// interval (Sample), with its RAPL zones if the source has them.
package energy

import (
	"time"

	"sathiraumesh/ampere/internal/devices/cpu/rapl"
)

// Sample is one measurement of a device's energy over an interval.
// The JSON field names include units so the output is self-describing.
type Sample struct {
	Device    string    `json:"device"`
	Source    string    `json:"source"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	IntervalS float64   `json:"interval_s"`
	EnergyJ   float64   `json:"energy_j"`
	PowerW    float64   `json:"power_w"`

	// Zones is the per-zone breakdown, for sources that have zones.
	// Parent zones already include their children: do not sum them.
	Zones []rapl.ZoneSample `json:"zones,omitempty"`
}
