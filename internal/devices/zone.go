package devices

import "fmt"

// ZoneReader is an optional extra for a Source whose energy is split into
// zones, e.g. RAPL package-0, core, dram. Sources without zones simply do
// not implement it; check with: zr, ok := src.(ZoneReader).
type ZoneReader interface {
	// ReadZones returns the current raw counter of every zone.
	ReadZones() ([]ZoneReading, error)
}

// ZoneReading is one raw zone counter at one moment.
type ZoneReading struct {
	ID       string // e.g. "intel-rapl:0:0"
	Name     string // e.g. "core"
	ParentID string // "" for top-level zones; a child's energy is inside its parent
	EnergyUJ uint64 // raw counter in µJ
	MaxUJ    uint64 // where the counter wraps back to 0; 0 if unknown
}

// ZoneSample is one zone's energy and power over an interval.
type ZoneSample struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Parent  string  `json:"parent,omitempty"`
	EnergyJ float64 `json:"energy_j"`
	PowerW  float64 `json:"power_w"`
}

// ZoneSamples turns two sets of zone readings, dt seconds apart, into energy
// and power per zone. Zones are matched by ID and keep the order of after.
// A counter that wrapped is corrected using MaxUJ; if MaxUJ is unknown the
// wrap cannot be corrected and an error is returned.
func ZoneSamples(before, after []ZoneReading, dt float64) ([]ZoneSample, error) {
	if dt <= 0 {
		return nil, fmt.Errorf("interval must be > 0, got %g s", dt)
	}
	prev := make(map[string]ZoneReading, len(before))
	for _, z := range before {
		prev[z.ID] = z
	}

	out := make([]ZoneSample, 0, len(after))
	for _, z := range after {
		b, ok := prev[z.ID]
		if !ok {
			return nil, fmt.Errorf("zone %s appeared during the interval", z.ID)
		}

		var deltaUJ uint64
		switch {
		case z.EnergyUJ >= b.EnergyUJ:
			deltaUJ = z.EnergyUJ - b.EnergyUJ
		case z.MaxUJ > 0:
			deltaUJ = (z.MaxUJ - b.EnergyUJ) + z.EnergyUJ // wrapped once
		default:
			return nil, fmt.Errorf("zone %s wrapped and its max is unknown", z.ID)
		}

		j := float64(deltaUJ) / 1e6
		out = append(out, ZoneSample{
			ID:      z.ID,
			Name:    z.Name,
			Parent:  z.ParentID,
			EnergyJ: j,
			PowerW:  j / dt,
		})
	}
	return out, nil
}
