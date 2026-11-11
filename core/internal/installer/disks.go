// Package installer is the NidaOS installer (milestone M-07, decisions
// D12, D32, D33 and D34). It reads the disks, measures their speed, makes
// the disk plan, and installs the NidaOS image with bootc. A web page in a
// kiosk browser shows the six steps of the mockup design/installer.
package installer

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// MinDiskBytes is the smallest disk that the plan uses.
const MinDiskBytes = 8 << 30

// Disk is one disk of the computer.
type Disk struct {
	// Name is the kernel name, for example "sda" or "nvme0n1".
	Name  string `json:"name"`
	Path  string `json:"path"`
	Model string `json:"model"`
	// Kind is nvme, ssd or hdd.
	Kind      string `json:"kind"`
	Transport string `json:"transport"`
	SizeBytes int64  `json:"size_bytes"`
	// Now tells what the disk has now, for example "ext4, 1 partition".
	Now string `json:"now"`
	// Usable is false for a disk that the plan cannot use. Reason tells why.
	Usable bool   `json:"usable"`
	Reason string `json:"reason,omitempty"`
	// SpeedMBs is the measured read speed. 0: not measured yet.
	SpeedMBs float64 `json:"speed_mbs"`
}

// lsblk -J -b -o NAME,PATH,SIZE,ROTA,RM,RO,TYPE,TRAN,MODEL,FSTYPE,LABEL,MOUNTPOINT
type lsblkOut struct {
	Blockdevices []lsblkDev `json:"blockdevices"`
}

type lsblkDev struct {
	Name       string     `json:"name"`
	Path       string     `json:"path"`
	Size       flexInt    `json:"size"`
	Rota       flexBool   `json:"rota"`
	Rm         flexBool   `json:"rm"`
	Ro         flexBool   `json:"ro"`
	Type       string     `json:"type"`
	Tran       string     `json:"tran"`
	Model      string     `json:"model"`
	Fstype     string     `json:"fstype"`
	Label      string     `json:"label"`
	Mountpoint string     `json:"mountpoint"`
	Children   []lsblkDev `json:"children"`
}

// flexInt and flexBool accept the two forms of lsblk: a JSON number or
// bool (new versions), and a string (old versions).
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "null" || s == "" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	*f = flexInt(n)
	return err
}

type flexBool bool

func (f *flexBool) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	*f = flexBool(s == "true" || s == "1")
	return nil
}

// LsblkArgs are the arguments of the lsblk command for ParseDisks.
var LsblkArgs = []string{"-J", "-b", "-o", "NAME,PATH,SIZE,ROTA,RM,RO,TYPE,TRAN,MODEL,FSTYPE,LABEL,MOUNTPOINT"}

// Labels of the installation media and of the seed volume.
const (
	MediaLabel = "NidaOS-Installer"
	SeedLabel  = "NIDASEED"
)

// ParseDisks reads the output of lsblk. It marks the disks that the plan
// cannot use: the installation media, the seed, removable, read-only, USB
// and small disks.
func ParseDisks(b []byte) ([]Disk, error) {
	var out lsblkOut
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("the output of lsblk is not correct: %w", err)
	}
	var disks []Disk
	for _, d := range out.Blockdevices {
		if d.Type != "disk" {
			continue
		}
		if strings.HasPrefix(d.Name, "zram") || strings.HasPrefix(d.Name, "loop") || strings.HasPrefix(d.Name, "ram") {
			continue
		}
		disk := Disk{
			Name:      d.Name,
			Path:      d.Path,
			Model:     strings.TrimSpace(d.Model),
			Transport: d.Tran,
			SizeBytes: int64(d.Size),
			Now:       describe(d),
			Usable:    true,
		}
		if disk.Path == "" {
			disk.Path = "/dev/" + d.Name
		}
		if disk.Model == "" {
			disk.Model = "Disk " + d.Name
		}
		switch {
		case d.Tran == "nvme" || strings.HasPrefix(d.Name, "nvme"):
			disk.Kind = "nvme"
		case !bool(d.Rota):
			disk.Kind = "ssd"
		default:
			disk.Kind = "hdd"
		}
		labels := allLabels(d)
		switch {
		case labels[MediaLabel]:
			disk.Usable, disk.Reason = false, "the installation media"
		case labels[SeedLabel]:
			disk.Usable, disk.Reason = false, "the seed volume NIDASEED"
		case bool(d.Rm):
			disk.Usable, disk.Reason = false, "a removable disk"
		case bool(d.Ro):
			disk.Usable, disk.Reason = false, "a read-only disk"
		case d.Tran == "usb":
			disk.Usable, disk.Reason = false, "a USB disk"
		case disk.SizeBytes < MinDiskBytes:
			disk.Usable, disk.Reason = false, "smaller than 8 GB"
		}
		disks = append(disks, disk)
	}
	sort.Slice(disks, func(i, j int) bool { return disks[i].Name < disks[j].Name })
	return disks, nil
}

func allLabels(d lsblkDev) map[string]bool {
	m := map[string]bool{}
	var walk func(x lsblkDev)
	walk = func(x lsblkDev) {
		if x.Label != "" {
			m[x.Label] = true
		}
		for _, c := range x.Children {
			walk(c)
		}
	}
	walk(d)
	return m
}

// describe tells what a disk has now, for the disk step and the review.
func describe(d lsblkDev) string {
	parts := 0
	types := map[string]int{}
	var order []string
	for _, c := range d.Children {
		if c.Type != "part" {
			continue
		}
		parts++
		t := c.Fstype
		if t == "" {
			continue
		}
		if types[t] == 0 {
			order = append(order, t)
		}
		types[t]++
	}
	if parts == 0 {
		if d.Fstype != "" {
			return d.Fstype + ", no partitions"
		}
		return "empty"
	}
	n := "1 partition"
	if parts > 1 {
		n = fmt.Sprintf("%d partitions", parts)
	}
	if len(order) == 0 {
		return n
	}
	return strings.Join(order, ", ") + ", " + n
}

// Runner runs a command and gives its standard output.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ReadDisks runs lsblk and gives the disks.
func ReadDisks(ctx context.Context, r Runner) ([]Disk, error) {
	out, err := r.Run(ctx, "lsblk", LsblkArgs...)
	if err != nil {
		return nil, err
	}
	return ParseDisks(out)
}
