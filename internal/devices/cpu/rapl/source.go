package rapl

import (
	"fmt"
	"path/filepath"
	"strings"

	"sathiraumesh/ampere/internal/devices"
	"sathiraumesh/ampere/internal/utils"
)

// Source is the RAPL implementation of cpu.Source..
type Source struct {
	Zones []Zone // RAPL zones and subzones found (empty if err != nil)
	err   error
}

// NewSource checks RAPL on this machine and returns it as a source.
func NewSource() *Source {
	zones, err := Status()
	return &Source{Zones: zones, err: err}
}

// Device returns "cpu": RAPL measures the CPU package.
func (s *Source) Device() string { return devices.CPU }

// Name identifies this implementation.
func (s *Source) Name() string { return "rapl" }

// Err is nil if RAPL is available, otherwise the reason it is not.
func (s *Source) Err() error { return s.err }

// Read returns the current CPU energy counter in µJ: the raw energy_uj of
// all package-N zones (one per socket), summed. It keeps no state.
//
// Not summed:
//   - subzones (core, uncore, dram under a package): already inside the package
//   - psys: covers package + more, would double count
//
// The counter wraps to 0 at MaxEnergyRangeUJ, so the caller must handle a
// second reading that is smaller than the first.
func (s *Source) Read() (uint64, error) {
	if s.err != nil {
		return 0, s.err
	}

	var sum uint64
	found := false
	for _, z := range s.Zones {
		if z.IsSubzone() || !strings.HasPrefix(z.Name, "package-") {
			continue
		}
		found = true

		cur, err := utils.ReadUint(filepath.Join(z.Path, "energy_uj"))
		if err != nil {
			return 0, fmt.Errorf("%s: %w", z.ID, err) // e.g. permission denied
		}
		sum += cur
	}

	if !found {
		return 0, fmt.Errorf("rapl: no package-N zone found")
	}
	return sum, nil
}

// ReadZones returns the current raw counter of every zone and subzone
// (package, core, dram, psys, ...). It implements ZoneReader.
func (s *Source) ReadZones() ([]ZoneReading, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make([]ZoneReading, 0, len(s.Zones))
	for _, z := range s.Zones {
		cur, err := utils.ReadUint(filepath.Join(z.Path, "energy_uj"))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", z.ID, err)
		}
		out = append(out, ZoneReading{
			ID:       z.ID,
			Name:     z.Name,
			ParentID: z.ParentID,
			EnergyUJ: cur,
			MaxUJ:    z.MaxEnergyRangeUJ,
		})
	}
	return out, nil
}

// ListZones describes every zone and subzone found, without reading their
// energy. It implements ZoneLister.
func (s *Source) ListZones() []ZoneInfo {
	out := make([]ZoneInfo, 0, len(s.Zones))
	for _, z := range s.Zones {
		out = append(out, ZoneInfo{
			ID:       z.ID,
			Name:     z.Name,
			ParentID: z.ParentID,
			MaxUJ:    z.MaxEnergyRangeUJ,
			Readable: z.Readable(),
		})
	}
	return out
}
