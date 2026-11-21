// Package cgroup finds Docker, Podman and containerd containers in the cgroup v2 tree
// (/sys/fs/cgroup) and reads their CPU time.
//
// Every container runs in its own cgroup. Its cpu.stat "usage_usec" is the
// CPU time of all the container's processes, including ones that already
// exited, so it needs no PID tracking.
package cgroup

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"sathiraumesh/ampere/internal/utils"
)

// Root is where Linux mounts the cgroup v2 tree.
const Root = "/sys/fs/cgroup"

// Container runtimes.
const (
	Docker     = "docker"
	Podman     = "podman"
	Containerd = "containerd"
)

// Container is one running container and its cgroup folder.
type Container struct {
	ID      string // full 64-character ID
	Runtime string // Docker, Podman or Containerd
	Path    string // cgroup folder, e.g. /sys/fs/cgroup/system.slice/docker-<id>.scope
}

// FindContainer finds the running Docker, Podman or containerd container
// whose ID is id or starts with id (a short ID such as "44f86868889b", as
// docker ps, podman ps, crictl ps and nerdctl ps show). It fails if none or
// more than one match.
func FindContainer(id string) (Container, error) { return findContainerAt(Root, id) }

// findContainerAt does the work of FindContainer for any cgroup root, so
// tests can use a fake tree.
func findContainerAt(root, id string) (Container, error) {
	id = strings.ToLower(id)
	if id == "" || len(id) > utils.ContainerIDLen || !utils.IsHex(id) {
		return Container{}, fmt.Errorf("container ID must be 1 to %d hex characters, got %q", utils.ContainerIDLen, id)
	}
	all, err := listContainersAt(root)
	if err != nil {
		return Container{}, err
	}
	var found []Container
	for _, c := range all {
		if strings.HasPrefix(c.ID, id) {
			found = append(found, c)
		}
	}

	switch len(found) {
	case 0:
		return Container{}, fmt.Errorf("no running docker, podman or containerd container with ID %q", id)
	case 1:
		return found[0], nil
	default:
		var ids []string
		for _, c := range found {
			ids = append(ids, c.Runtime+" "+c.ID[:12])
		}
		sort.Strings(ids)
		return Container{}, fmt.Errorf("container ID %q matches %d containers (%s): use a longer ID",
			id, len(found), strings.Join(ids, ", "))
	}
}

// ListContainers returns every running Docker, Podman and containerd
// container, in the order their cgroup folders are found.
func ListContainers() ([]Container, error) { return listContainersAt(Root) }

// listContainersAt does the work of ListContainers for any cgroup root, so
// tests can use a fake tree.
func listContainersAt(root string) ([]Container, error) {
	if _, err := os.Stat(filepath.Join(root, "cgroup.controllers")); err != nil {
		return nil, fmt.Errorf("%s is not a cgroup v2 tree (cgroup v1 is not supported): %w", root, err)
	}

	var found []Container
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A cgroup can disappear while we walk (a container stopped);
			// skip it instead of failing the whole search.
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
				return nil
			}
			return err
		}
		if !d.IsDir() || path == root {
			return nil
		}
		runtime, cid, ok := containerDir(filepath.Base(filepath.Dir(path)), d.Name())
		if !ok {
			return nil
		}
		found = append(found, Container{ID: cid, Runtime: runtime, Path: path})
		// A container's own sub-cgroups (e.g. systemd inside podman) are
		// already counted in its usage: do not look inside.
		return filepath.SkipDir
	})
	if err != nil {
		return nil, err
	}
	return found, nil
}

// containerDir reports whether the cgroup folder name (inside a folder
// named parent) is a Docker, Podman or containerd container, and its ID.
//
//	docker-<id>.scope              Docker, systemd cgroup driver (rootful or rootless)
//	docker/<id>                    Docker, cgroupfs driver
//	libpod-<id>.scope              Podman, systemd cgroup driver (rootful or rootless)
//	libpod_parent/libpod-<id>      Podman, cgroupfs driver
//	cri-containerd-<id>.scope      containerd under Kubernetes (CRI), systemd driver
//	nerdctl-<id>.scope             containerd via nerdctl, systemd driver
//
// Podman's monitor, libpod-conmon-<id>.scope, is not the container: its
// name has "conmon-" where the ID should be, so it never matches.
func containerDir(parent, name string) (runtime, id string, ok bool) {
	if id, ok := utils.CutContainerID(name, "docker-", ".scope"); ok {
		return Docker, id, true
	}
	if parent == "docker" && utils.IsContainerID(name) {
		return Docker, name, true
	}
	if id, ok := utils.CutContainerID(name, "libpod-", ".scope"); ok {
		return Podman, id, true
	}
	if id, ok := utils.CutContainerID(name, "cri-containerd-", ".scope"); ok {
		return Containerd, id, true
	}
	if id, ok := utils.CutContainerID(name, "nerdctl-", ".scope"); ok {
		return Containerd, id, true
	}
	if parent == "libpod_parent" {
		if id, ok := utils.CutContainerID(name, "libpod-", ""); ok {
			return Podman, id, true
		}
	}
	return "", "", false
}

// ReadUsage returns the CPU time used by all processes in the cgroup at
// path since it was created, in µs ("usage_usec" in cpu.stat). It does
// not need root. If the container has stopped, the error is
// fs.ErrNotExist.
func ReadUsage(path string) (uint64, error) {
	f, err := os.Open(filepath.Join(path, "cpu.stat"))
	if err != nil {
		return 0, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, value, ok := strings.Cut(sc.Text(), " ")
		if !ok || key != "usage_usec" {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%s/cpu.stat: usage_usec: %w", path, err)
		}
		return n, nil
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("%s/cpu.stat: no usage_usec", path)
}
