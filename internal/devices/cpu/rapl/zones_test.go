package rapl

import (
	"os"
	"path/filepath"
	"testing"
)

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

// laptop is the layout of the dev laptop (AMD): one package with a core subzone.
var laptop = []fakeZone{
	{"intel-rapl:0", "package-0", "1000", "65532610987"},
	{"intel-rapl:0:0", "core", "400", "65532610987"},
}

func TestParentID(t *testing.T) {
	tests := []struct{ id, want string }{
		{"intel-rapl:0", ""},
		{"intel-rapl:1", ""},
		{"intel-rapl:0:0", "intel-rapl:0"},
		{"intel-rapl:1:2", "intel-rapl:1"},
		{"intel-rapl:0:0:3", "intel-rapl:0:0"}, // deeper nesting
	}
	for _, tt := range tests {
		if got := parentID(tt.id); got != tt.want {
			t.Errorf("parentID(%q) = %q, want %q", tt.id, got, tt.want)
		}
	}
}

func TestStatusAt(t *testing.T) {
	root := fakePowercap(t, laptop...)

	zones, err := statusAt(root)
	if err != nil {
		t.Fatalf("statusAt: %v", err)
	}

	// intel-rapl (control type) and dtpm must be skipped.
	if len(zones) != 2 {
		t.Fatalf("got %d zones, want 2: %+v", len(zones), zones)
	}

	byID := map[string]Zone{}
	for _, z := range zones {
		byID[z.ID] = z
	}

	pkg, core := byID["intel-rapl:0"], byID["intel-rapl:0:0"]

	if pkg.Name != "package-0" || core.Name != "core" {
		t.Errorf("names = %q, %q; want package-0, core", pkg.Name, core.Name)
	}
	if pkg.ParentID != "" || pkg.IsSubzone() {
		t.Errorf("package ParentID = %q, want top-level", pkg.ParentID)
	}
	if core.ParentID != "intel-rapl:0" || !core.IsSubzone() {
		t.Errorf("core ParentID = %q, want intel-rapl:0", core.ParentID)
	}
	if pkg.MaxEnergyRangeUJ != 65532610987 {
		t.Errorf("MaxEnergyRangeUJ = %d, want 65532610987", pkg.MaxEnergyRangeUJ)
	}
	if pkg.Path != filepath.Join(root, "intel-rapl:0") {
		t.Errorf("Path = %q", pkg.Path)
	}
	if !pkg.Readable() || !core.Readable() {
		t.Errorf("zones should be readable: %v, %v", pkg.EnergyErr, core.EnergyErr)
	}
}

func TestStatusAtUnreadableEnergy(t *testing.T) {
	// No energy_uj file: like "permission denied", the zone is still listed.
	root := fakePowercap(t, fakeZone{"intel-rapl:0", "package-0", "", "100"})

	zones, err := statusAt(root)
	if err != nil {
		t.Fatalf("statusAt: %v", err)
	}
	if len(zones) != 1 || zones[0].Readable() {
		t.Errorf("want 1 unreadable zone, got %+v", zones)
	}
}

func TestStatusAtNoZones(t *testing.T) {
	root := fakePowercap(t) // only intel-rapl and dtpm, no zones
	if _, err := statusAt(root); err == nil {
		t.Error("want an error when no zones exist (e.g. inside a VM)")
	}
}

func TestStatusAtMissingDir(t *testing.T) {
	if _, err := statusAt(filepath.Join(t.TempDir(), "nope")); !os.IsNotExist(err) {
		t.Errorf("err = %v, want not-exist", err)
	}
}

// writeEnergy overwrites energy_uj of a zone folder.
func writeEnergy(t *testing.T, zonePath, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(zonePath, "energy_uj"), []byte(value+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
