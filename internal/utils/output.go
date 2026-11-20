package utils

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

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
// repeated runs build one table. Samples with zones are written as their
// own blocks (total table + zones table), each with a header.
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

// WriteJSON prints v (a sample) as one JSON object on one line, so many
// samples can be appended to a file and read back line by line (JSON Lines).
// Zones, if any, are in the "zones" array.
func WriteJSON(out io.Writer, v any) error {
	return json.NewEncoder(out).Encode(v)
}

// WriteProcessSample writes a process sample in the given format. header
// controls whether the table header row is written (ignored for JSON).
func WriteProcessSample(out io.Writer, format string, s attribution.ProcessSample, header bool) error {
	if format == OutputJSON {
		return WriteJSON(out, s)
	}
	return WriteProcessTable(out, s, header)
}

// WriteProcessTable prints the process's total split as one row (header
// only if header is true). If there are zones, they follow in a separate
// table with its own header, after a blank line.
func WriteProcessTable(out io.Writer, s attribution.ProcessSample, header bool) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if header || len(s.Zones) > 0 {
		fmt.Fprintln(w, "PID\tCOMM\tINTERVAL (s)\tCPU ACTIVE\tPID CPU\tENERGY (J)\tACTIVE (J)\tIDLE (J)\tPID (J)\tPID (W)")
	}
	fmt.Fprintf(w, "%d\t%s\t%.6f\t%.1f%%\t%.1f%%\t%.6f\t%.6f\t%.6f\t%.6f\t%.2f\n",
		s.PID, s.Comm, s.IntervalS, 100*s.CPUActive, 100*s.ProcessCPU,
		s.EnergyJ, s.ActiveJ, s.IdleJ, s.ProcessJ, s.ProcessW)
	if err := w.Flush(); err != nil {
		return err
	}
	if len(s.Zones) == 0 {
		return nil
	}

	// Zones: parents already include their children, so rows must not be summed.
	fmt.Fprintln(out)
	w = tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ZONE\tNAME\tPARENT\tENERGY (J)\tACTIVE (J)\tIDLE (J)\tPID (J)\tPID (W)")
	for _, z := range s.Zones {
		parent := z.Parent
		if parent == "" {
			parent = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%.6f\t%.6f\t%.6f\t%.6f\t%.2f\n",
			z.ID, z.Name, parent, z.EnergyJ, z.ActiveJ, z.IdleJ, z.ProcessJ, z.ProcessW)
	}
	return w.Flush()
}
