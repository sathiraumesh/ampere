package rapl

import (
	"reflect"
	"strings"
	"testing"
)

func TestZoneSamples(t *testing.T) {
	before := []ZoneReading{
		{ID: "intel-rapl:0", Name: "package-0", EnergyUJ: 1_000_000, MaxUJ: 100_000_000},
		{ID: "intel-rapl:0:0", Name: "core", ParentID: "intel-rapl:0", EnergyUJ: 99_000_000, MaxUJ: 100_000_000},
	}
	after := []ZoneReading{
		{ID: "intel-rapl:0", Name: "package-0", EnergyUJ: 11_000_000, MaxUJ: 100_000_000},
		{ID: "intel-rapl:0:0", Name: "core", ParentID: "intel-rapl:0", EnergyUJ: 3_000_000, MaxUJ: 100_000_000}, // wrapped
	}

	got, err := ZoneSamples(before, after, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []ZoneSample{
		{ID: "intel-rapl:0", Name: "package-0", EnergyJ: 10, PowerW: 5},
		{ID: "intel-rapl:0:0", Name: "core", Parent: "intel-rapl:0", EnergyJ: 4, PowerW: 2}, // (100-99)+3
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestZoneSamplesErrors(t *testing.T) {
	z := func(id string, e, max uint64) ZoneReading { return ZoneReading{ID: id, EnergyUJ: e, MaxUJ: max} }

	tests := []struct {
		name          string
		before, after []ZoneReading
		dt            float64
		want          string
	}{
		{"wrap with unknown max", []ZoneReading{z("a", 10, 0)}, []ZoneReading{z("a", 5, 0)}, 1, "max is unknown"},
		{"new zone", nil, []ZoneReading{z("a", 5, 0)}, 1, "appeared"},
		{"zero interval", nil, nil, 0, "interval"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ZoneSamples(tt.before, tt.after, tt.dt)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}
