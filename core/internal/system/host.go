package system

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// System is the state of the host.
type System struct {
	Time          time.Time `json:"time"`
	Hostname      string    `json:"hostname"`
	OS            OS        `json:"os"`
	Kernel        string    `json:"kernel"`
	UptimeSeconds float64   `json:"uptime_seconds"`
	CPU           CPU       `json:"cpu"`
	Memory        Memory    `json:"memory"`
	Load          Load      `json:"load"`
}

// OS is the operating system, from /etc/os-release.
type OS struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	PrettyName string `json:"pretty_name"`
	// ImageVersion is the version of the NidaOS image, if the image gives it.
	ImageVersion string `json:"image_version,omitempty"`
}

// CPU is the processor of the host.
type CPU struct {
	Model   string `json:"model"`
	Threads int    `json:"threads"`
}

// Memory is the main memory of the host.
type Memory struct {
	TotalBytes     uint64 `json:"total_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
}

// Load is the load average of the host.
type Load struct {
	One     float64 `json:"one"`
	Five    float64 `json:"five"`
	Fifteen float64 `json:"fifteen"`
}

func (c *Collector) path(p string) string {
	return filepath.Join(c.Root, filepath.FromSlash(p))
}

func (c *Collector) read(p string) ([]byte, error) {
	return os.ReadFile(c.path(p))
}

func (c *Collector) readTrim(p string) (string, error) {
	b, err := c.read(p)
	return strings.TrimSpace(string(b)), err
}

// System reads the state of the host.
func (c *Collector) System() (System, error) {
	s := System{Time: c.Now().UTC()}
	var err error

	if s.Hostname, err = c.readTrim("/proc/sys/kernel/hostname"); err != nil {
		if s.Hostname, err = c.readTrim("/etc/hostname"); err != nil {
			return System{}, fmt.Errorf("hostname: %w", err)
		}
	}
	if s.Kernel, err = c.readTrim("/proc/sys/kernel/osrelease"); err != nil {
		return System{}, fmt.Errorf("kernel: %w", err)
	}
	if s.OS, err = c.osRelease(); err != nil {
		return System{}, err
	}
	if s.UptimeSeconds, err = c.uptime(); err != nil {
		return System{}, err
	}
	if s.CPU, err = c.cpu(); err != nil {
		return System{}, err
	}
	if s.Memory, err = c.memory(); err != nil {
		return System{}, err
	}
	if s.Load, err = c.load(); err != nil {
		return System{}, err
	}
	return s, nil
}

func (c *Collector) osRelease() (OS, error) {
	b, err := c.read("/etc/os-release")
	if err != nil {
		if b, err = c.read("/usr/lib/os-release"); err != nil {
			return OS{}, fmt.Errorf("os-release: %w", err)
		}
	}
	kv := parseKeyValues(b)
	return OS{
		Name:         kv["NAME"],
		Version:      kv["VERSION_ID"],
		PrettyName:   kv["PRETTY_NAME"],
		ImageVersion: firstOf(kv, "IMAGE_VERSION", "OSTREE_VERSION"),
	}, nil
}

func (c *Collector) uptime() (float64, error) {
	s, err := c.readTrim("/proc/uptime")
	if err != nil {
		return 0, fmt.Errorf("uptime: %w", err)
	}
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0, fmt.Errorf("uptime: the file is empty")
	}
	return strconv.ParseFloat(f[0], 64)
}

func (c *Collector) cpu() (CPU, error) {
	b, err := c.read("/proc/cpuinfo")
	if err != nil {
		return CPU{}, fmt.Errorf("cpuinfo: %w", err)
	}
	var cpu CPU
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "processor":
			cpu.Threads++
		case "model name":
			if cpu.Model == "" {
				cpu.Model = strings.TrimSpace(v)
			}
		}
	}
	return cpu, nil
}

func (c *Collector) memory() (Memory, error) {
	b, err := c.read("/proc/meminfo")
	if err != nil {
		return Memory{}, fmt.Errorf("meminfo: %w", err)
	}
	var m Memory
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		n, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			continue
		}
		// The values in /proc/meminfo are in KiB.
		switch f[0] {
		case "MemTotal:":
			m.TotalBytes = n * 1024
		case "MemAvailable:":
			m.AvailableBytes = n * 1024
		}
	}
	return m, nil
}

func (c *Collector) load() (Load, error) {
	s, err := c.readTrim("/proc/loadavg")
	if err != nil {
		return Load{}, fmt.Errorf("loadavg: %w", err)
	}
	f := strings.Fields(s)
	if len(f) < 3 {
		return Load{}, fmt.Errorf("loadavg: the format is not correct: %q", s)
	}
	var l Load
	var vals [3]float64
	for i := range vals {
		if vals[i], err = strconv.ParseFloat(f[i], 64); err != nil {
			return Load{}, fmt.Errorf("loadavg: %w", err)
		}
	}
	l.One, l.Five, l.Fifteen = vals[0], vals[1], vals[2]
	return l, nil
}

// parseKeyValues reads lines KEY=value. It removes the quotes of the values.
func parseKeyValues(b []byte) map[string]string {
	kv := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		kv[strings.TrimSpace(k)] = v
	}
	return kv
}

func firstOf(kv map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := kv[k]; v != "" {
			return v
		}
	}
	return ""
}
