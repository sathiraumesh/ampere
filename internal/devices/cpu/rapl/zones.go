// Package rapl reads Intel/AMD RAPL energy zones from the Linux powercap
// framework (/sys/class/powercap).
package rapl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sathiraumesh/ampere/internal/utils"
)

const (
	// Powercap is where Linux exposes RAPL zones.
	Powercap = "/sys/class/powercap"

	// ZonePrefix is the folder prefix of every zone and subzone,
	// e.g. "intel-rapl:0" or "intel-rapl:0:0". The control type
	// "intel-rapl" (no colon) is not a zone and is skipped.
	ZonePrefix = "intel-rapl:"
)

// Zone is one RAPL zone or subzone as found on disk.
type Zone struct {
	ID   string // folder name, e.g. "intel-rapl:0:0"
	Path string // e.g. "/sys/class/powercap/intel-rapl:0:0"
	Name string // from the "name" file: "package-0", "core", "dram", ...

	// EnergyErr is nil if energy_uj could be read, otherwise the error
	// (usually "permission denied" when not running as root).
	EnergyErr error

	// MaxEnergyRangeUJ is the value (µJ) where energy_uj wraps back to 0,
	// from "max_energy_range_uj". Readable without root. 0 if unknown.
	MaxEnergyRangeUJ uint64

	// ParentID is the zone this one is nested inside, e.g. "intel-rapl:0"
	// for the core subzone "intel-rapl:0:0". Empty for top-level zones.
	// A subzone's energy is ALREADY included in its parent's energy.
	ParentID string
}

// IsSubzone reports whether this zone is nested inside another zone.
func (z Zone) IsSubzone() bool { return z.ParentID != "" }

// Readable reports whether this zone's energy_uj can be read.
func (z Zone) Readable() bool { return z.EnergyErr == nil }

// Status lists all RAPL zones and subzones and checks whether their
// energy counter is readable. It only looks; it does not print anything.
func Status() ([]Zone, error) {
	return statusAt(Powercap)
}

// statusAt does the work of Status for any powercap directory, so tests can
// point it at a fake tree instead of /sys/class/powercap.
func statusAt(powercap string) ([]Zone, error) {
	entries, err := os.ReadDir(powercap)
	if err != nil {
		return nil, err
	}

	var zones []Zone
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ZonePrefix) {
			continue
		}

		path := filepath.Join(powercap, e.Name())
		name, _ := os.ReadFile(filepath.Join(path, "name"))
		_, energyErr := os.ReadFile(filepath.Join(path, "energy_uj"))
		maxUJ, _ := utils.ReadUint(filepath.Join(path, "max_energy_range_uj"))

		zones = append(zones, Zone{
			ID:               e.Name(),
			Path:             path,
			Name:             strings.TrimSpace(string(name)),
			EnergyErr:        energyErr,
			MaxEnergyRangeUJ: maxUJ,
			ParentID:         parentID(e.Name()),
		})
	}

	if len(zones) == 0 {
		return nil, fmt.Errorf("no %s* zones in %s (VM, or driver not loaded?)",
			ZonePrefix, powercap)
	}
	return zones, nil
}

// parentID drops the last ":<n>" from a zone ID:
//
//	"intel-rapl:0:0" → "intel-rapl:0"
//	"intel-rapl:0"   → ""   (top-level; "intel-rapl" is the control type)
func parentID(id string) string {
	parent := id[:strings.LastIndex(id, ":")]
	if !strings.HasPrefix(parent, ZonePrefix) {
		return ""
	}
	return parent
}
