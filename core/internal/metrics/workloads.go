package metrics

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Workload kinds.
const (
	// KindPod is a pod of k3s: an app, a database or a runner.
	KindPod = "pod"
	// KindService is a systemd service of the host, for example k3s.
	KindService = "service"
)

// workloadRaw is one read of the cgroup counters of one workload.
type workloadRaw struct {
	kind, namespace, name, id string
	cpuMicros                 uint64
	memoryBytes               uint64
}

// podDir matches the cgroup directory of a pod for the two cgroup drivers
// of the kubelet:
//
//	cgroupfs: kubepods/burstable/pod<uid>
//	systemd:  kubepods-burstable-pod<uid with _>.slice
var podDir = regexp.MustCompile(`pod([0-9a-f]{8}[-_][0-9a-f]{4}[-_][0-9a-f]{4}[-_][0-9a-f]{4}[-_][0-9a-f]{12})(\.slice)?$`)

// maxCgroupDepth limits the search for the pod directories.
const maxCgroupDepth = 5

// workloads reads the counters of the pods and of the systemd services.
// A cgroup that disappears during the read is not an error.
func (r reader) workloads() map[string]workloadRaw {
	out := map[string]workloadRaw{}
	root := r.path("/sys/fs/cgroup")
	if _, err := os.Stat(root); err != nil {
		return out
	}
	names := r.podNames()

	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		depth := strings.Count(filepath.ToSlash(rel), "/") + 1
		if rel == "." {
			depth = 0
		}
		if depth > maxCgroupDepth {
			return fs.SkipDir
		}
		if m := podDir.FindStringSubmatch(d.Name()); m != nil {
			uid := strings.ReplaceAll(m[1], "_", "-")
			w := workloadRaw{kind: KindPod, id: uid, name: uid}
			if n, ok := names[uid]; ok {
				w.namespace, w.name = n[0], n[1]
			}
			if readCgroup(p, &w) {
				out[KindPod+"/"+uid] = w
			}
			// The containers of the pod are in the pod total.
			return fs.SkipDir
		}
		if filepath.ToSlash(filepath.Dir(rel)) == "system.slice" && strings.HasSuffix(d.Name(), ".service") {
			name := strings.TrimSuffix(d.Name(), ".service")
			w := workloadRaw{kind: KindService, id: d.Name(), name: name}
			if readCgroup(p, &w) {
				out[KindService+"/"+name] = w
			}
			return fs.SkipDir
		}
		return nil
	})
	return out
}

// readCgroup reads cpu.stat and memory.current of a cgroup (version 2).
func readCgroup(dir string, w *workloadRaw) bool {
	b, err := os.ReadFile(filepath.Join(dir, "cpu.stat"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "usage_usec "); ok {
			w.cpuMicros, _ = strconv.ParseUint(strings.TrimSpace(v), 10, 64)
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "memory.current")); err == nil {
		w.memoryBytes, _ = strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	}
	return true
}

// podNames reads the namespace and the name of each pod from the log
// directories of the kubelet: <namespace>_<name>_<uid>. Thus the service
// needs no access to the Kubernetes API for the names.
func (r reader) podNames() map[string][2]string {
	out := map[string][2]string{}
	entries, err := os.ReadDir(r.path(r.podLogDir))
	if err != nil {
		return out
	}
	for _, e := range entries {
		parts := strings.Split(e.Name(), "_")
		if len(parts) != 3 {
			continue
		}
		out[parts[2]] = [2]string{parts[0], parts[1]}
	}
	return out
}
