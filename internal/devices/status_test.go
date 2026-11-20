package devices

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// stubSource is a minimal Source.
type stubSource struct {
	device, name string
	err          error
}

func (s stubSource) Name() string          { return s.name }
func (s stubSource) Device() string        { return s.device }
func (s stubSource) Err() error            { return s.err }
func (s stubSource) Read() (uint64, error) { return 0, s.err }

var report = Report{Sources: []Source{
	stubSource{"cpu", "rapl", errors.New("permission denied")},
	stubSource{"gpu", "nvml", nil},
	stubSource{"cpu", "hwmon", nil},
	stubSource{"cpu", "model", nil},
	stubSource{"dram", "rapl-dram", errors.New("no zone")},
}}

func names(ss []Source) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s.Name())
	}
	return out
}

func TestTypes(t *testing.T) {
	want := []string{"cpu", "gpu", "dram"} // first-appearance order
	if got := report.Types(); !reflect.DeepEqual(got, want) {
		t.Errorf("Types() = %v, want %v", got, want)
	}
	if got := (Report{}).Types(); len(got) != 0 {
		t.Errorf("empty Types() = %v", got)
	}
}

func TestFor(t *testing.T) {
	want := []string{"rapl", "hwmon", "model"} // priority order kept
	if got := names(report.For("cpu")); !reflect.DeepEqual(got, want) {
		t.Errorf("For(cpu) = %v, want %v", got, want)
	}
	if got := report.For("fpga"); len(got) != 0 {
		t.Errorf("For(fpga) = %v, want none", got)
	}
}

func TestPreferred(t *testing.T) {
	if got := report.Preferred("cpu"); got == nil || got.Name() != "hwmon" {
		t.Errorf("Preferred(cpu) = %v, want hwmon (first available)", got)
	}
	if got := report.Preferred("dram"); got != nil {
		t.Errorf("Preferred(dram) = %v, want nil (none available)", got)
	}
}

func TestFind(t *testing.T) {
	if s, err := report.Find("cpu", "model"); err != nil || s.Name() != "model" {
		t.Errorf("Find(cpu, model) = %v, %v", s, err)
	}

	tests := []struct {
		device, name string
		wants        []string
	}{
		{"cpu", "rapl", []string{"not available", "permission denied"}},
		{"cpu", "nvml", []string{"unknown cpu source", "rapl, hwmon, model"}},
		{"fpga", "x", []string{`unknown device "fpga"`, "cpu, gpu, dram"}},
	}
	for _, tt := range tests {
		_, err := report.Find(tt.device, tt.name)
		for _, w := range tt.wants {
			if err == nil || !strings.Contains(err.Error(), w) {
				t.Errorf("Find(%s, %s) err = %v, want %q", tt.device, tt.name, err, w)
			}
		}
	}
}

func TestPick(t *testing.T) {
	if s, _ := report.Pick("cpu", ""); s == nil || s.Name() != "hwmon" {
		t.Errorf(`Pick(cpu, "") = %v, want preferred hwmon`, s)
	}
	if s, _ := report.Pick("gpu", "nvml"); s == nil || s.Name() != "nvml" {
		t.Errorf("Pick(gpu, nvml) = %v", s)
	}

	_, err := report.Pick("dram", "")
	if err == nil || !strings.Contains(err.Error(), "no dram energy source available") {
		t.Errorf("Pick(dram) err = %v", err)
	}
	_, err = report.Pick("fpga", "")
	if err == nil || !strings.Contains(err.Error(), `unknown device "fpga"`) {
		t.Errorf("Pick(fpga) err = %v", err)
	}
	if _, err := (Report{}).Pick("cpu", ""); err == nil {
		t.Error("empty report: want error")
	}
}
