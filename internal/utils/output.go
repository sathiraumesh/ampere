package utils

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"sathiraumesh/ampere/internal/devices"
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

// AppendSample appends s to the file at path, creating it if needed.
// For tables without zones, the header is only written when the file is new
// or empty, so repeated runs build one table. With zones, each sample is
// written as its own block (total table + zones table).
func AppendSample(path, format string, s devices.Sample) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	if err := WriteSample(f, format, s, info.Size() == 0); err != nil {
		f.Close()
		return err
	}
	return f.Close() // Close can fail too (e.g. disk full), so report it
}

// WriteTable prints the total as a table (header row only if header is
// true). If the sample has zones, they follow in a separate table, always
// with its own header, after a blank line.
func WriteTable(out io.Writer, s devices.Sample, header bool) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if header || len(s.Zones) > 0 {
		fmt.Fprintln(w, "DEVICE\tSOURCE\tINTERVAL (s)\tENERGY (J)\tPOWER (W)")
	}
	fmt.Fprintf(w, "%s\t%s\t%.6f\t%.6f\t%.2f\n",
		s.Device, s.Source, s.IntervalS, s.EnergyJ, s.PowerW)
	if err := w.Flush(); err != nil {
		return err
	}
	if len(s.Zones) == 0 {
		return nil
	}

	// Zones: parents already include their children, so rows must not be summed.
	fmt.Fprintln(out)
	w = tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ZONE\tNAME\tPARENT\tENERGY (J)\tPOWER (W)")
	for _, z := range s.Zones {
		parent := z.Parent
		if parent == "" {
			parent = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%.6f\t%.2f\n", z.ID, z.Name, parent, z.EnergyJ, z.PowerW)
	}
	return w.Flush()
}

// WriteJSON prints the sample as one JSON object on one line, so many
// samples can be appended to a file and read back line by line (JSON Lines).
// Zones, if any, are in the "zones" array.
func WriteJSON(out io.Writer, s devices.Sample) error {
	return json.NewEncoder(out).Encode(s)
}
