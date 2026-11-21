package rapl

import (
	"os"
	"path/filepath"
	"testing"
)

// Test helpers shared by the rapl tests.

// fakeZone describes one zone folder in a fake powercap tree.
// An empty energy means the energy_uj file is not created.
type fakeZone struct {
	id, name, energy, max string
}

// fakePowercap builds a fake /sys/class/powercap in a temp dir:
//
//	intel-rapl/        control type (no files, must be skipped)
//	dtpm/              unrelated powercap entry (must be skipped)
//	<zone.id>/name, energy_uj, max_energy_range_uj
func fakePowercap(t *testing.T, zones ...fakeZone) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"intel-rapl", "dtpm"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, z := range zones {
		dir := filepath.Join(root, z.id)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		write := func(file, content string) {
			if err := os.WriteFile(filepath.Join(dir, file), []byte(content+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		write("name", z.name)
		write("max_energy_range_uj", z.max)
		if z.energy != "" {
			write("energy_uj", z.energy)
		}
	}
	return root
}

// serverZones is a two-socket layout with every RAPL zone type: each
// package-N with its subzones (core, uncore, dram), and the top-level psys.
// Fields: id, name, energy_uj, max_energy_range_uj.
var serverZones = []fakeZone{
	{"intel-rapl:0", "package-0", "1000", "262143328850"},
	{"intel-rapl:0:0", "core", "400", "262143328850"},
	{"intel-rapl:0:1", "uncore", "100", "262143328850"},
	{"intel-rapl:0:2", "dram", "300", "65712999613"},
	{"intel-rapl:1", "package-1", "2000", "262143328850"},
	{"intel-rapl:1:0", "core", "800", "262143328850"},
	{"intel-rapl:1:1", "dram", "500", "65712999613"},
	{"intel-rapl:2", "psys", "9000", "262143328850"},
}

// writeEnergy overwrites energy_uj of a zone folder.
func writeEnergy(t *testing.T, zonePath, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(zonePath, "energy_uj"), []byte(value+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
