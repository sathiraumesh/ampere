package utils

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"sathiraumesh/ampere/internal/devices"
	"sathiraumesh/ampere/internal/host/attribution"
)

// Output formats for commands with an --output flag.
const (
	OutputTable = "table"
	OutputJSON  = "json"
)

// WriteSample writes s in the given format. header controls whether the
// table header row is written (ignored for JSON).
func WriteSample(out io.Writer, format string, s devices.Sample, header bool) error {
	if format == OutputJSON {
		return WriteJSON(out, s)
	}
	return WriteTable(out, s, header)
}

// AppendTo opens the file at path for appending (creating it if needed)
// and calls write, with header true only if the file is new or empty, so
// repeated runs build one table with one header line.
func AppendTo(path string, write func(w io.Writer, header bool) error) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	if err := write(f, info.Size() == 0); err != nil {
		f.Close()
		return err
	}
	return f.Close() // Close can fail too (e.g. disk full), so report it
}

// WriteTable prints the sample as table rows, with the header line first
// if header is true. Without zones it is one row. With zones there is a
// "total" row and one row per zone, all under the same header, so many
// samples build one table. Parent zones include their children: zone rows
// must not be summed.
func WriteTable(out io.Writer, s devices.Sample, header bool) error {
	t := newTable(out, col("DEVICE", 6), col("SOURCE", 6), col("INTERVAL (s)", 12))
	if len(s.Zones) > 0 {
		t.add(zoneColumns...)
	}
	t.add(col("ENERGY (J)", 11), col("POWER (W)", 9))
	if header {
		t.header()
	}

	head := []string{s.Device, s.Source, fmt.Sprintf("%.6f", s.IntervalS)}
	if len(s.Zones) == 0 {
		return t.row(append(head, fmt.Sprintf("%.6f", s.EnergyJ), fmt.Sprintf("%.2f", s.PowerW))...)
	}
	t.row(append(head, totalZone, "-", "-", fmt.Sprintf("%.6f", s.EnergyJ), fmt.Sprintf("%.2f", s.PowerW))...)
	for _, z := range s.Zones {
		t.row(append(head, z.ID, z.Name, dash(z.Parent), fmt.Sprintf("%.6f", z.EnergyJ), fmt.Sprintf("%.2f", z.PowerW))...)
	}
	return t.err
}

// WriteJSON prints v (a sample) as one JSON object on one line, so many
// samples can be appended to a file and read back line by line (JSON Lines).
// Zones, if any, are in the "zones" array.
func WriteJSON(out io.Writer, v any) error {
	return json.NewEncoder(out).Encode(v)
}

// WriteTargetSample writes a --pid or --container sample in the given
// format. header controls whether the table header row is written
// (ignored for JSON).
func WriteTargetSample(out io.Writer, format string, s attribution.Sample, header bool) error {
	if format == OutputJSON {
		return WriteJSON(out, s)
	}
	return WriteTargetTable(out, s, header)
}

// WriteTargetTable prints the target's share as table rows, like
// WriteTable: one row, or with zones a "total" row and one row per zone,
// all under one header line (written only if header is true). Columns
// about the target are labelled PID for a process and CTR for a container.
func WriteTargetTable(out io.Writer, s attribution.Sample, header bool) error {
	label := "PID"
	t := newTable(out, col("PID", 7), col("COMM", 15))
	ids := []string{fmt.Sprint(s.PID), s.Comm}
	if s.IsContainer() {
		label = "CTR"
		t = newTable(out, col("CONTAINER", 12), col("RUNTIME", 10))
		ids = []string{shortID(s.Container), s.Runtime}
	}
	t.add(col("INTERVAL (s)", 12), col(label+" CPU", 7))
	if len(s.Zones) > 0 {
		t.add(zoneColumns...)
	}
	t.add(col("ENERGY (J)", 11), col(label+" (J)", 9), col(label+" (W)", 7))
	if header {
		t.header()
	}

	head := append(ids, fmt.Sprintf("%.6f", s.IntervalS), fmt.Sprintf("%.1f%%", 100*s.TargetCPU))
	values := func(e attribution.EnergySplit) []string {
		return []string{fmt.Sprintf("%.6f", e.EnergyJ), fmt.Sprintf("%.6f", e.TargetJ), fmt.Sprintf("%.4f", e.TargetW)}
	}
	if len(s.Zones) == 0 {
		return t.row(append(head, values(s.EnergySplit)...)...)
	}
	t.row(append(append(head, totalZone, "-", "-"), values(s.EnergySplit)...)...)
	for _, z := range s.Zones {
		t.row(append(append(head, z.ID, z.Name, dash(z.Parent)), values(z.EnergySplit)...)...)
	}
	return t.err
}

// totalZone is the ZONE of the row with the device's total.
const totalZone = "total"

// zoneColumns are the columns of a zone row. The widths fit RAPL IDs such
// as "intel-rapl:0:0"; longer values still work but push the row right.
var zoneColumns = []column{col("ZONE", 14), col("NAME", 10), col("PARENT", 14)}

// dash returns s, or "-" if s is empty.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// shortID is the first 12 characters of a container ID, as docker ps shows.
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
