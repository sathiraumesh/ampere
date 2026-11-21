package utils

import (
	"strings"
	"testing"
)

func TestCutAffix(t *testing.T) {
	for _, tc := range []struct {
		s, prefix, suffix, want string
		ok                      bool
	}{
		{"docker-44f8.scope", "docker-", ".scope", "44f8", true},
		{"libpod-ab", "libpod-", "", "ab", true},
		{"docker-.scope", "docker-", ".scope", "", true},
		{"podman-44f8.scope", "docker-", ".scope", "", false},
		{"docker-44f8.service", "docker-", ".scope", "", false},
		{"docker-", "docker-", ".scope", "", false},
	} {
		got, ok := CutAffix(tc.s, tc.prefix, tc.suffix)
		if got != tc.want || ok != tc.ok {
			t.Errorf("CutAffix(%q, %q, %q) = %q, %v; want %q, %v", tc.s, tc.prefix, tc.suffix, got, ok, tc.want, tc.ok)
		}
	}
}

func TestIsHex(t *testing.T) {
	for s, want := range map[string]bool{
		"":                 true,
		"0123456789abcdef": true,
		"44f8":             true,
		"44F8":             false, // uppercase is not accepted
		"44g8":             false,
		"44-8":             false,
	} {
		if got := IsHex(s); got != want {
			t.Errorf("IsHex(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestIsContainerID(t *testing.T) {
	full := strings.Repeat("ab", 32) // 64 hex
	for s, want := range map[string]bool{
		full:                    true,
		full[:63]:               false, // too short
		full + "a":              false, // too long
		strings.ToUpper(full):   false, // uppercase
		strings.Repeat("g", 64): false, // not hex
		"":                      false,
	} {
		if got := IsContainerID(s); got != want {
			t.Errorf("IsContainerID(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestCutContainerID(t *testing.T) {
	full := strings.Repeat("ab", 32)
	for _, tc := range []struct {
		name, prefix, suffix, want string
		ok                         bool
	}{
		{"docker-" + full + ".scope", "docker-", ".scope", full, true},
		{"libpod-" + full, "libpod-", "", full, true},
		{"docker-44f8.scope", "docker-", ".scope", "", false},                // short ID
		{"libpod-conmon-" + full + ".scope", "libpod-", ".scope", "", false}, // conmon
		{"docker-" + full + ".service", "docker-", ".scope", "", false},      // wrong suffix
	} {
		got, ok := CutContainerID(tc.name, tc.prefix, tc.suffix)
		if got != tc.want || ok != tc.ok {
			t.Errorf("CutContainerID(%q) = %q, %v; want %q, %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}
