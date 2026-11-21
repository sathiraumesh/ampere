package utils

import "strings"

// CutAffix returns s without prefix and suffix, and whether s had both.
// If it does not, it returns "", false. The suffix may be "" (only the
// prefix is needed then).
//
//	CutAffix("docker-44f8.scope", "docker-", ".scope") → "44f8", true
func CutAffix(s, prefix, suffix string) (string, bool) {
	rest, ok := strings.CutPrefix(s, prefix)
	if !ok {
		return "", false
	}
	if rest, ok = strings.CutSuffix(rest, suffix); !ok {
		return "", false
	}
	return rest, true
}

// IsHex reports whether s is made only of lowercase hex digits (0-9, a-f),
// as in container IDs. It is true for "".
func IsHex(s string) bool {
	for _, c := range s {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}

// ContainerIDLen is the length of a full container ID (64 hex characters)
// for Docker, Podman and containerd (Kubernetes CRI and nerdctl).
const ContainerIDLen = 64

// IsContainerID reports whether s is a full container ID: exactly
// ContainerIDLen lowercase hex digits.
func IsContainerID(s string) bool { return len(s) == ContainerIDLen && IsHex(s) }

// CutContainerID returns what is between prefix and suffix in name, if it
// is a full container ID; otherwise "", false.
//
//	CutContainerID("docker-<64 hex>.scope", "docker-", ".scope") → "<64 hex>", true
func CutContainerID(name, prefix, suffix string) (string, bool) {
	id, ok := CutAffix(name, prefix, suffix)
	if !ok || !IsContainerID(id) {
		return "", false
	}
	return id, true
}
