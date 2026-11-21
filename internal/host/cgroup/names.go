package cgroup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Where the runtimes keep their container metadata. They are variables so
// tests can point them at fake folders.
var (
	// DockerRoot is Docker's data root ("data-root" in daemon.json).
	DockerRoot = "/var/lib/docker"
	// PodmanStorage is rootful Podman's storage folder.
	PodmanStorage = "/var/lib/containers/storage"
)

// Name returns the container's name as its runtime knows it (e.g. "web"),
// or "" if it cannot be found. It reads the runtime's own files, which
// usually needs root:
//
//	Docker   <DockerRoot>/containers/<id>/config.v2.json            "Name": "/web"
//	Podman   <PodmanStorage>/overlay-containers/containers.json     "names": ["web"]
//
// Rootless Podman (storage in the user's home), Docker with another data
// root, and containerd are not looked up: their containers get "".
func Name(c Container) string {
	switch c.Runtime {
	case Docker:
		return dockerName(c.ID)
	case Podman:
		return podmanName(c.ID)
	}
	return ""
}

func dockerName(id string) string {
	raw, err := os.ReadFile(filepath.Join(DockerRoot, "containers", id, "config.v2.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		Name string `json:"Name"`
	}
	if json.Unmarshal(raw, &cfg) != nil {
		return ""
	}
	return strings.TrimPrefix(cfg.Name, "/") // Docker stores "/web"
}

func podmanName(id string) string {
	raw, err := os.ReadFile(filepath.Join(PodmanStorage, "overlay-containers", "containers.json"))
	if err != nil {
		return ""
	}
	var all []struct {
		ID    string   `json:"id"`
		Names []string `json:"names"`
	}
	if json.Unmarshal(raw, &all) != nil {
		return ""
	}
	for _, c := range all {
		if c.ID == id && len(c.Names) > 0 {
			return c.Names[0]
		}
	}
	return ""
}
