package rapl

import (
	"os"
	"path/filepath"
	"testing"
)

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
	root := fakePowercap(t, serverZones...)

	zones, err := statusAt(root)
	if err != nil {
		t.Fatalf("statusAt: %v", err)
	}

	// intel-rapl (control type) and dtpm must be skipped.
	if len(zones) != len(serverZones) {
		t.Fatalf("got %d zones, want %d: %+v", len(zones), len(serverZones), zones)
	}

	want := map[string]struct {
		name, parent string
		max          uint64
	}{
		"intel-rapl:0":   {"package-0", "", 262143328850},
		"intel-rapl:0:0": {"core", "intel-rapl:0", 262143328850},
		"intel-rapl:0:1": {"uncore", "intel-rapl:0", 262143328850},
		"intel-rapl:0:2": {"dram", "intel-rapl:0", 65712999613},
		"intel-rapl:1":   {"package-1", "", 262143328850},
		"intel-rapl:1:0": {"core", "intel-rapl:1", 262143328850},
		"intel-rapl:1:1": {"dram", "intel-rapl:1", 65712999613},
		"intel-rapl:2":   {"psys", "", 262143328850},
	}
	for _, z := range zones {
		w, ok := want[z.ID]
		if !ok {
			t.Errorf("unexpected zone %s", z.ID)
			continue
		}
		delete(want, z.ID)
		if z.Name != w.name || z.ParentID != w.parent || z.MaxEnergyRangeUJ != w.max {
			t.Errorf("%s = %s/%q/%d, want %s/%q/%d", z.ID, z.Name, z.ParentID, z.MaxEnergyRangeUJ, w.name, w.parent, w.max)
		}
		if z.IsSubzone() != (w.parent != "") {
			t.Errorf("%s: IsSubzone = %v", z.ID, z.IsSubzone())
		}
		if z.Path != filepath.Join(root, z.ID) {
			t.Errorf("%s: Path = %q", z.ID, z.Path)
		}
		if !z.Readable() {
			t.Errorf("%s should be readable: %v", z.ID, z.EnergyErr)
		}
	}
	for id := range want {
		t.Errorf("zone %s not found", id)
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
