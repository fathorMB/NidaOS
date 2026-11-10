// Package metrics measures the computer and each workload at a fixed
// interval. One Sampler reads the counters of the kernel, calculates the
// rates from the previous read, and sends one Snapshot to all the clients
// (decision D8).
//
// The package reads only files under Root: /proc, /sys, the cgroups of
// /sys/fs/cgroup and the pod log directories of the kubelet.
package metrics

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/system"
)

// raw is one read of the counters. The counters only increase. A Snapshot
// is the difference of two raw reads.
type raw struct {
	time      time.Time
	cpu       map[string]cpuTimes // "cpu" is the total, "cpu0"... are the threads
	memTotal  uint64
	memAvail  uint64
	load      [3]float64
	tempC     *float64
	disks     map[string]diskCounters
	nets      map[string]netCounters
	workloads map[string]workloadRaw
}

type cpuTimes struct {
	total, idle uint64
}

type diskCounters struct {
	readBytes, writeBytes, ioMillis uint64
}

type netCounters struct {
	rxBytes, txBytes uint64
}

// reader reads the counter files under root.
type reader struct {
	root      string
	podLogDir string
}

func (r reader) path(p string) string {
	return filepath.Join(r.root, filepath.FromSlash(p))
}

func (r reader) read(p string) ([]byte, error) {
	return os.ReadFile(r.path(p))
}

// readRaw reads all the counters. The host counters are necessary. The
// temperature and the workloads are optional.
func (r reader) readRaw(now time.Time) (raw, error) {
	out := raw{time: now}
	var err error
	if out.cpu, err = r.cpuTimes(); err != nil {
		return raw{}, err
	}
	if out.memTotal, out.memAvail, err = r.memory(); err != nil {
		return raw{}, err
	}
	if out.load, err = r.load(); err != nil {
		return raw{}, err
	}
	if out.disks, err = r.diskCounters(); err != nil {
		return raw{}, err
	}
	if out.nets, err = r.netCounters(); err != nil {
		return raw{}, err
	}
	out.tempC = r.cpuTemperature()
	out.workloads = r.workloads()
	return out, nil
}

// cpuTimes reads /proc/stat. The unit of the values is not important,
// because the snapshot uses only the ratio of two differences.
func (r reader) cpuTimes() (map[string]cpuTimes, error) {
	b, err := r.read("/proc/stat")
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}
	out := map[string]cpuTimes{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 || !strings.HasPrefix(f[0], "cpu") {
			continue
		}
		var v [8]uint64
		for i := 0; i < len(v) && i+1 < len(f); i++ {
			v[i], _ = strconv.ParseUint(f[i+1], 10, 64)
		}
		// user nice system idle iowait irq softirq steal. The guest times
		// are also in user and nice, thus do not add them.
		var total uint64
		for _, x := range v {
			total += x
		}
		out[f[0]] = cpuTimes{total: total, idle: v[3] + v[4]}
	}
	if _, ok := out["cpu"]; !ok {
		return nil, fmt.Errorf("stat: there is no cpu line")
	}
	return out, nil
}

func (r reader) memory() (total, avail uint64, err error) {
	b, err := r.read("/proc/meminfo")
	if err != nil {
		return 0, 0, fmt.Errorf("meminfo: %w", err)
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		n, _ := strconv.ParseUint(f[1], 10, 64)
		switch f[0] {
		case "MemTotal:":
			total = n * 1024
		case "MemAvailable:":
			avail = n * 1024
		}
	}
	return total, avail, nil
}

func (r reader) load() ([3]float64, error) {
	var l [3]float64
	b, err := r.read("/proc/loadavg")
	if err != nil {
		return l, fmt.Errorf("loadavg: %w", err)
	}
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return l, fmt.Errorf("loadavg: the format is not correct")
	}
	for i := range l {
		if l[i], err = strconv.ParseFloat(f[i], 64); err != nil {
			return l, fmt.Errorf("loadavg: %w", err)
		}
	}
	return l, nil
}

// diskCounters reads /proc/diskstats for the physical disks only.
func (r reader) diskCounters() (map[string]diskCounters, error) {
	b, err := r.read("/proc/diskstats")
	if err != nil {
		return nil, fmt.Errorf("diskstats: %w", err)
	}
	out := map[string]diskCounters{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 13 {
			continue
		}
		name := f[2]
		if system.IsVirtualBlock(name) {
			continue
		}
		// Only whole disks have a directory in /sys/block.
		if _, err := os.Stat(r.path("/sys/block/" + name)); err != nil {
			continue
		}
		num := func(i int) uint64 { n, _ := strconv.ParseUint(f[i], 10, 64); return n }
		// Fields: 5 sectors read, 9 sectors written, 12 milliseconds of I/O.
		// A sector of diskstats is always 512 bytes.
		out[name] = diskCounters{readBytes: num(5) * 512, writeBytes: num(9) * 512, ioMillis: num(12)}
	}
	return out, nil
}

// netCounters reads /proc/net/dev. It ignores the loopback and the virtual
// ends of the pod networks (veth).
func (r reader) netCounters() (map[string]netCounters, error) {
	b, err := r.read("/proc/net/dev")
	if err != nil {
		return nil, fmt.Errorf("net/dev: %w", err)
	}
	out := map[string]netCounters{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "lo" || strings.HasPrefix(name, "veth") {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		rx, _ := strconv.ParseUint(f[0], 10, 64)
		tx, _ := strconv.ParseUint(f[8], 10, 64)
		out[name] = netCounters{rxBytes: rx, txBytes: tx}
	}
	return out, nil
}

// cpuTemperature reads the package temperature of the Intel driver
// coretemp. It gives nil when the computer has no such sensor, for
// example in a virtual machine.
func (r reader) cpuTemperature() *float64 {
	dirs, _ := filepath.Glob(r.path("/sys/class/hwmon/hwmon*"))
	for _, d := range dirs {
		name, err := os.ReadFile(filepath.Join(d, "name"))
		if err != nil || strings.TrimSpace(string(name)) != "coretemp" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(d, "temp1_input"))
		if err != nil {
			continue
		}
		milli, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
		if err != nil {
			continue
		}
		c := milli / 1000
		return &c
	}
	return nil
}
