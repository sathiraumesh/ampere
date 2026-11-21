package utils

import (
	"fmt"
	"io"
	"strings"
)

// column is one table column: its title and the width its values are
// padded to (at least the title's width).
type column struct {
	title string
	width int
}

// col makes a column at least width wide.
func col(title string, width int) column {
	return column{title: title, width: max(width, len(title))}
}

// table writes rows with fixed column widths, so rows written at different
// times (one sample at a time, or appended to a file by later runs) still
// line up under one header. text/tabwriter cannot do that: it sizes the
// columns again for every batch of rows.
type table struct {
	out  io.Writer
	cols []column
	err  error // the first write error
}

func newTable(out io.Writer, cols ...column) *table {
	return &table{out: out, cols: cols}
}

func (t *table) add(cols ...column) { t.cols = append(t.cols, cols...) }

// header writes the column titles.
func (t *table) header() error {
	titles := make([]string, len(t.cols))
	for i, c := range t.cols {
		titles[i] = c.title
	}
	return t.row(titles...)
}

// row writes one row: each cell padded to its column's width, two spaces
// between columns, no trailing spaces.
func (t *table) row(cells ...string) error {
	if t.err != nil {
		return t.err
	}
	var b strings.Builder
	for i, cell := range cells {
		if i > 0 {
			b.WriteString("  ")
		}
		if i < len(cells)-1 && i < len(t.cols) {
			fmt.Fprintf(&b, "%-*s", t.cols[i].width, cell)
		} else {
			b.WriteString(cell)
		}
	}
	_, t.err = fmt.Fprintln(t.out, strings.TrimRight(b.String(), " "))
	return t.err
}
