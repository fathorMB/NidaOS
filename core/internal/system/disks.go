package system

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Disk kinds.
const (
	KindNVMe = "nvme"
	KindSSD  = "ssd"
	KindHDD  = "hdd"
)

// Disk is one physical disk of the computer.
type Disk struct {
	Name      string `json:"name"`
	Model     string `json:"model"`
	SizeBytes uint64 `json:"size_bytes"`
	// Kind is nvme, ssd or hdd.
	Kind string `json:"kind"`
	// Transport is nvme, sata, usb, virtio or other.
	Transport string `json:"transport"`
	Removable bool   `json:"removable"`
	ReadOnly  bool   `json:"read_only"`
	// Role comes from the disk plan: os, performance, standard or unused.
	// It is empty when the plan does not name the disk.
	Role       string      `json:"role"`
	Partitions []Partition `json:"partitions"`
}

// Partition is one partition of a disk.
type Partition struct {
	Name       string `json:"name"`
	SizeBytes  uint64 `json:"size_bytes"`
	MountPoint string `json:"mount_point,omitempty"`
	FSType     string `json:"fs_type,omitempty"`
}

// mount is one line of /proc/self/mounts.
type mount struct {
	device, point, fsType string
}

// Disks reads the disks from /sys/block. It ignores the virtual devices.
func (c *Collector) Disks() ([]Disk, error) {
	entries, err := os.ReadDir(c.path("/sys/block"))
	if err != nil {
		return nil, fmt.Errorf("disks: %w", err)
	}
	plan, err := c.plan()
	if err != nil {
		return nil, err
	}
	mounts := c.mounts()

	disks := []Disk{}
	for _, e := range entries {
		name := e.Name()
		if IsVirtualBlock(name) {
			continue
		}
		d, err := c.disk(name, mounts)
		if err != nil {
			return nil, err
		}
		d.Role = plan.roleOf(name)
		disks = append(disks, d)
	}
	sort.Slice(disks, func(i, j int) bool { return disks[i].Name < disks[j].Name })
	return disks, nil
}

// IsVirtualBlock tells if a device of /sys/block is not a physical disk,
// for example a loop device or a device-mapper device.
func IsVirtualBlock(name string) bool {
	for _, p := range []string{"loop", "ram", "zram", "dm-", "md", "sr", "nbd"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func (c *Collector) disk(name string, mounts map[string]mount) (Disk, error) {
	base := "/sys/block/" + name
	d := Disk{Name: name, Partitions: []Partition{}}

	var err error
	if d.SizeBytes, err = c.sectors(base + "/size"); err != nil {
		return Disk{}, fmt.Errorf("disk %s: %w", name, err)
	}
	d.Model, _ = c.readTrim(base + "/device/model")
	d.Removable = c.flag(base + "/removable")
	d.ReadOnly = c.flag(base + "/ro")
	d.Transport = c.transport(name)
	switch {
	case strings.HasPrefix(name, "nvme"):
		d.Kind = KindNVMe
	case c.flag(base + "/queue/rotational"):
		d.Kind = KindHDD
	default:
		d.Kind = KindSSD
	}

	entries, err := os.ReadDir(c.path(base))
	if err != nil {
		return Disk{}, fmt.Errorf("disk %s: %w", name, err)
	}
	for _, e := range entries {
		// A partition is a directory with a "partition" file.
		if _, err := os.Stat(c.path(base + "/" + e.Name() + "/partition")); err != nil {
			continue
		}
		p := Partition{Name: e.Name()}
		if p.SizeBytes, err = c.sectors(base + "/" + e.Name() + "/size"); err != nil {
			return Disk{}, fmt.Errorf("partition %s: %w", e.Name(), err)
		}
		if m, ok := mounts[e.Name()]; ok {
			p.MountPoint, p.FSType = m.point, m.fsType
		}
		d.Partitions = append(d.Partitions, p)
	}
	sort.Slice(d.Partitions, func(i, j int) bool { return d.Partitions[i].Name < d.Partitions[j].Name })
	return d, nil
}

// sectors reads a size file of /sys. The unit is always 512 bytes.
func (c *Collector) sectors(p string) (uint64, error) {
	s, err := c.readTrim(p)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", p, err)
	}
	return n * 512, nil
}

func (c *Collector) flag(p string) bool {
	s, err := c.readTrim(p)
	return err == nil && s == "1"
}

// transport finds the bus of the disk from the link in /sys/block.
func (c *Collector) transport(name string) string {
	switch {
	case strings.HasPrefix(name, "nvme"):
		return "nvme"
	case strings.HasPrefix(name, "vd"):
		return "virtio"
	}
	link, err := os.Readlink(c.path("/sys/block/" + name))
	if err != nil {
		return "other"
	}
	switch {
	case strings.Contains(link, "/usb"):
		return "usb"
	case strings.Contains(link, "/ata"):
		return "sata"
	}
	return "other"
}

// mounts reads /proc/self/mounts. The key is the device name without
// "/dev/", for example "sda2". A missing file gives an empty result.
func (c *Collector) mounts() map[string]mount {
	out := map[string]mount{}
	b, err := c.read("/proc/self/mounts")
	if err != nil {
		return out
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 || !strings.HasPrefix(f[0], "/dev/") {
			continue
		}
		dev := path.Base(f[0])
		point := unescapeMount(f[1])
		// Keep the first mount point of a device: bootc adds bind mounts
		// of the same device later in the file.
		if _, ok := out[dev]; !ok {
			out[dev] = mount{device: dev, point: point, fsType: f[2]}
		}
		// Keep also each mount point, for the tiers.
		out["@"+point] = mount{device: dev, point: point, fsType: f[2]}
	}
	return out
}

// unescapeMount decodes the octal escapes of /proc/self/mounts, for
// example "\040" for a space.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// diskPlan is the content of the plan file of the installer.
type diskPlan struct {
	os, performance, standard string
	unused                    []string
	found                     bool
}

func (p diskPlan) roleOf(name string) string {
	switch name {
	case "":
		return ""
	case p.os:
		return "os"
	case p.performance:
		return TierPerformance
	case p.standard:
		return TierStandard
	}
	if slices.Contains(p.unused, name) {
		return "unused"
	}
	return ""
}

// plan reads the plan file. A missing file is not an error: the computer
// can be a development computer.
func (c *Collector) plan() (diskPlan, error) {
	b, err := c.read(c.PlanFile)
	if errors.Is(err, fs.ErrNotExist) {
		return diskPlan{}, nil
	}
	if err != nil {
		return diskPlan{}, fmt.Errorf("disk plan: %w", err)
	}
	kv := parseKeyValues(b)
	return diskPlan{
		os:          kv["os"],
		performance: kv[TierPerformance],
		standard:    kv[TierStandard],
		unused:      strings.Fields(kv["unused"]),
		found:       true,
	}, nil
}
