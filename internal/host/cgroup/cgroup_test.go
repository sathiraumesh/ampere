package cgroup

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Full IDs used in the fake trees.
var (
	dockerID  = "44f86868889bf4ebf9db3fe8866e7f992a5625c69cccc827c728b81c10f10565"
	docker2ID = "74484aaf214738ac72a80efddc871fb0a11f0e66760986ce1b2e429035771e42"
	podmanID  = "9c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d"
	ctrdID    = "e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6"
	sharedA   = "abcd" + strings.Repeat("1", 60)
	sharedB   = "abcd" + strings.Repeat("2", 60)
)

// fakeCgroup builds a cgroup v2 root with the given folders (each gets a
// cpu.stat with usage_usec 1000) and returns the root.
func fakeCgroup(t *testing.T, dirs ...string) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "cgroup.controllers"), "cpu io memory\n")
	for _, d := range dirs {
		write(t, filepath.Join(root, d, "cpu.stat"), "usage_usec 1000\nuser_usec 600\nsystem_usec 400\n")
	}
	return root
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindContainerLayouts(t *testing.T) {
	uid := "user.slice/user-1000.slice/user@1000.service"
	for _, tc := range []struct {
		name, dir, id, runtime string
	}{
		{"docker systemd", "system.slice/docker-" + dockerID + ".scope", dockerID, Docker},
		{"docker cgroupfs", "docker/" + dockerID, dockerID, Docker},
		{"docker rootless", uid + "/docker.service/docker-" + dockerID + ".scope", dockerID, Docker},
		{"podman rootful", "machine.slice/libpod-" + podmanID + ".scope", podmanID, Podman},
		{"podman rootless", uid + "/user.slice/libpod-" + podmanID + ".scope", podmanID, Podman},
		{"podman cgroupfs", "libpod_parent/libpod-" + podmanID, podmanID, Podman},
		{"containerd kubernetes", "kubepods.slice/kubepods-burstable.slice/" +
			"kubepods-burstable-pod1a2b3c4d_5e6f_7a8b_9c0d_1e2f3a4b5c6d.slice/" +
			"cri-containerd-" + ctrdID + ".scope", ctrdID, Containerd},
		{"containerd nerdctl", "system.slice/nerdctl-" + ctrdID + ".scope", ctrdID, Containerd},
	} {
		root := fakeCgroup(t, tc.dir, "system.slice/cron.service", "init.scope")

		got, err := findContainerAt(root, tc.id[:12]) // short ID, as docker ps shows
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		want := Container{ID: tc.id, Runtime: tc.runtime, Path: filepath.Join(root, tc.dir)}
		if got != want {
			t.Errorf("%s: got  %+v\nwant %+v", tc.name, got, want)
		}
	}
}

func TestFindContainerFullAndUpperCaseID(t *testing.T) {
	root := fakeCgroup(t, "system.slice/docker-"+dockerID+".scope")
	for _, id := range []string{dockerID, strings.ToUpper(dockerID[:8])} {
		if got, err := findContainerAt(root, id); err != nil || got.ID != dockerID {
			t.Errorf("id %q: got %+v, %v", id, got, err)
		}
	}
}

func TestFindContainerPicksTheRightOne(t *testing.T) {
	root := fakeCgroup(t,
		"system.slice/docker-"+dockerID+".scope",
		"system.slice/docker-"+docker2ID+".scope",
		"machine.slice/libpod-"+podmanID+".scope",
		"kubepods.slice/cri-containerd-"+ctrdID+".scope",
	)
	for id, want := range map[string]string{"44f8": dockerID, "7448": docker2ID, "9c1d": podmanID, "e5f6": ctrdID} {
		if got, err := findContainerAt(root, id); err != nil || got.ID != want {
			t.Errorf("id %s: got %+v, %v; want %s", id, got, err, want)
		}
	}
}

func TestFindContainerSkipsConmon(t *testing.T) {
	// Podman's monitor process has its own scope next to the container.
	root := fakeCgroup(t,
		"machine.slice/libpod-conmon-"+podmanID+".scope",
		"machine.slice/libpod-"+podmanID+".scope",
	)
	got, err := findContainerAt(root, podmanID[:12])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got.Path, "/libpod-"+podmanID+".scope") {
		t.Errorf("path = %s, want the container scope, not conmon", got.Path)
	}
}

func TestFindContainerIgnoresNested(t *testing.T) {
	// systemd inside a podman container makes sub-cgroups; only the
	// container itself is a match (its usage already includes them).
	root := fakeCgroup(t,
		"machine.slice/libpod-"+podmanID+".scope",
		"machine.slice/libpod-"+podmanID+".scope/container/docker-"+dockerID+".scope",
	)
	if _, err := findContainerAt(root, dockerID[:12]); err == nil {
		t.Error("a cgroup nested inside a container should not be found")
	}
}

func TestFindContainerAmbiguous(t *testing.T) {
	root := fakeCgroup(t,
		"system.slice/docker-"+sharedA+".scope",
		"machine.slice/libpod-"+sharedB+".scope",
	)
	_, err := findContainerAt(root, "abcd")
	if err == nil || !strings.Contains(err.Error(), "matches 2 containers") ||
		!strings.Contains(err.Error(), "docker abcd11111111") || !strings.Contains(err.Error(), "podman abcd22222222") {
		t.Errorf("err = %v, want ambiguous error listing both", err)
	}
	if got, err := findContainerAt(root, "abcd2"); err != nil || got.ID != sharedB {
		t.Errorf("longer ID: got %+v, %v", got, err)
	}
}

func TestFindContainerErrors(t *testing.T) {
	root := fakeCgroup(t,
		"system.slice/docker-"+dockerID+".scope",
		"system.slice/docker-short.scope",            // not a full ID
		"system.slice/docker-"+dockerID+"0.scope",    // too long
		"something/"+dockerID,                        // bare ID outside docker/
		"system.slice/libpod-"+podmanID+".service",   // wrong suffix
		"user.slice/docker-"+dockerID+".scope.extra", // wrong suffix
		"system.slice/containerd.service",            // the daemon, not a container
		"kubepods.slice/crio-"+ctrdID+".scope",       // CRI-O: not supported
	)
	for _, id := range []string{"", "xyz", "44f8-", strings.Repeat("a", 65)} {
		if _, err := findContainerAt(root, id); err == nil || !strings.Contains(err.Error(), "hex") {
			t.Errorf("id %q: err = %v, want bad ID error", id, err)
		}
	}
	if _, err := findContainerAt(root, "ffff"); err == nil || !strings.Contains(err.Error(), "no running") {
		t.Errorf("unknown ID: err = %v", err)
	}
	if _, err := findContainerAt(root, podmanID[:8]); err == nil {
		t.Error("a .service is not a podman container")
	}
	if _, err := findContainerAt(root, ctrdID[:8]); err == nil {
		t.Error("crio-<id>.scope is not a containerd container")
	}
}

func TestFindContainerCgroupV1(t *testing.T) {
	root := t.TempDir() // no cgroup.controllers
	if _, err := findContainerAt(root, "44f8"); err == nil || !strings.Contains(err.Error(), "cgroup v2") {
		t.Errorf("err = %v, want cgroup v2 error", err)
	}
}

func TestReadUsage(t *testing.T) {
	root := fakeCgroup(t, "c")
	got, err := ReadUsage(filepath.Join(root, "c"))
	if err != nil || got != 1000 {
		t.Errorf("ReadUsage = %d, %v; want 1000", got, err)
	}
}

func TestReadUsageErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadUsage(filepath.Join(dir, "gone")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stopped container: err = %v, want not exist", err)
	}
	write(t, filepath.Join(dir, "a", "cpu.stat"), "user_usec 5\n")
	if _, err := ReadUsage(filepath.Join(dir, "a")); err == nil {
		t.Error("no usage_usec: want error")
	}
	write(t, filepath.Join(dir, "b", "cpu.stat"), "usage_usec x\n")
	if _, err := ReadUsage(filepath.Join(dir, "b")); err == nil {
		t.Error("bad number: want error")
	}
}

// TestFindRealDocker checks against this machine's running Docker
// containers, if any.
func TestFindRealDocker(t *testing.T) {
	matches, _ := filepath.Glob(filepath.Join(Root, "system.slice", "docker-*.scope"))
	if len(matches) == 0 {
		t.Skip("no docker containers running here")
	}
	id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(matches[0]), "docker-"), ".scope")

	c, err := FindContainer(id[:12])
	if err != nil {
		t.Fatal(err)
	}
	usage, err := ReadUsage(c.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s %s: %d µs CPU", c.Runtime, c.ID[:12], usage)
}
