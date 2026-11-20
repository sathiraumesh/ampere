package utils

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile creates a temp file with content and returns its path.
func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "value")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadUint(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    uint64
	}{
		{"sysfs style with newline", "65532610987\n", 65532610987},
		{"no newline", "42", 42},
		{"surrounding spaces", "  7 \n", 7},
		{"zero", "0\n", 0},
		{"max uint64", "18446744073709551615\n", 18446744073709551615},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ReadUint(writeFile(t, tt.content))
			if err != nil {
				t.Fatalf("ReadUint: unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ReadUint = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestReadUintErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"empty", ""},
		{"negative", "-1\n"},
		{"text", "package-0\n"},
		{"two numbers", "1 2\n"},
		{"overflow", "18446744073709551616\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ReadUint(writeFile(t, tt.content)); err == nil {
				t.Errorf("ReadUint(%q): expected an error, got nil", tt.content)
			}
		})
	}
}

func TestReadUintMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := ReadUint(path); !os.IsNotExist(err) {
		t.Errorf("ReadUint(missing) err = %v, want a not-exist error", err)
	}
}
