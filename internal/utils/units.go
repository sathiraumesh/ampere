// Package utils holds small helpers shared by the ampere packages.
package utils

import (
	"os"
	"strconv"
	"strings"
)

// ReadUint reads a file containing one unsigned number, e.g. a sysfs file
// such as energy_uj or max_energy_range_uj ("65532610987\n").
func ReadUint(path string) (uint64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
}
