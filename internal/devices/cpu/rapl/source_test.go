package rapl

import (
	"errors"
	"strings"
	"testing"
)

// Helpers fakeZone and fakePowercap are in zones_test.go (same package).

func TestSourceName(t *testing.T) {
	if got := (&Source{}).Name(); got != "rapl" {
		t.Errorf("Name() = %q, want rapl", got)
	}
}

func TestReadSumsPackagesOnly(t *testing.T) {
	// 2-socket server with core subzones and a psys zone.
	root := fakePowercap(t,
		fakeZone{"intel-rapl:0", "package-0", "1000", "100000"},
		fakeZone{"intel-rapl:0:0", "core", "400", "100000"}, // inside package-0
		fakeZone{"intel-rapl:1", "package-1", "2000", "100000"},
		fakeZone{"intel-rapl:1:0", "core", "500", "100000"}, // inside package-1
		fakeZone{"intel-rapl:2", "psys", "9999", "100000"},  // covers everything
	)
	zones, err := statusAt(root)
	if err != nil {
		t.Fatal(err)
	}

	got, err := (&Source{Zones: zones}).Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != 3000 { // package-0 + package-1 only
		t.Errorf("Read() = %d, want 3000 (packages only)", got)
	}
}

func TestReadUnavailable(t *testing.T) {
	want := errors.New("no rapl")
	if _, err := (&Source{err: want}).Read(); err != want {
		t.Errorf("Read() err = %v, want %v", err, want)
	}
}

func TestReadNoPackage(t *testing.T) {
	zones := []Zone{{ID: "intel-rapl:0", Name: "psys"}}
	if _, err := (&Source{Zones: zones}).Read(); err == nil {
		t.Error("want an error when there is no package-N zone")
	}
}

func TestReadEnergyError(t *testing.T) {
	root := fakePowercap(t, fakeZone{"intel-rapl:0", "package-0", "", "100"})
	zones, err := statusAt(root)
	if err != nil {
		t.Fatal(err)
	}

	_, err = (&Source{Zones: zones}).Read()
	if err == nil || !strings.Contains(err.Error(), "intel-rapl:0") {
		t.Errorf("err = %v, want an error naming the zone", err)
	}
}

func TestSourceErr(t *testing.T) {
	if err := (&Source{}).Err(); err != nil {
		t.Errorf("Err() = %v, want nil for an available source", err)
	}
	want := errors.New("no rapl")
	if err := (&Source{err: want}).Err(); err != want {
		t.Errorf("Err() = %v, want %v", err, want)
	}
}

func TestNewSource(t *testing.T) {
	// Runs against the real machine: RAPL may or may not exist here,
	// but the result must be consistent either way.
	s := NewSource()
	if s == nil {
		t.Fatal("NewSource() = nil")
	}
	if s.Err() == nil && len(s.Zones) == 0 {
		t.Error("available source must have zones")
	}
	if s.Err() != nil && len(s.Zones) != 0 {
		t.Error("unavailable source must have no zones")
	}
}

func TestReadPicksUpNewValues(t *testing.T) {
	// Read keeps no state: each call returns the current counter.
	root := fakePowercap(t, fakeZone{"intel-rapl:0", "package-0", "1000", "100000"})
	zones, err := statusAt(root)
	if err != nil {
		t.Fatal(err)
	}
	s := &Source{Zones: zones}

	if got, _ := s.Read(); got != 1000 {
		t.Fatalf("first Read() = %d, want 1000", got)
	}
	writeEnergy(t, zones[0].Path, "1500")
	if got, _ := s.Read(); got != 1500 {
		t.Errorf("second Read() = %d, want 1500", got)
	}
}
