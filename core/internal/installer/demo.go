package installer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The demonstration mode shows the installer with the disks of the mockup
// and a simulated installation. It changes no disk. The browser tests and
// the development on a computer without Linux use it.

// DemoDisks are the disks of the computer of the mockup.
func DemoDisks() []Disk {
	return []Disk{
		{Name: "sda", Path: "/dev/sda", Model: "Seagate ST1000DM003", Kind: "hdd", Transport: "sata", SizeBytes: 1000204886016, Now: "empty", Usable: true},
		{Name: "sdb", Path: "/dev/sdb", Model: "Kingston SA400S37", Kind: "ssd", Transport: "sata", SizeBytes: 120034123776, Now: "ext4, vfat, 3 partitions", Usable: true},
		{Name: "sdc", Path: "/dev/sdc", Model: "SanDisk SDSSDHP128G", Kind: "ssd", Transport: "sata", SizeBytes: 128035676160, Now: "ext4, 1 partition", Usable: true},
		{Name: "sdd", Path: "/dev/sdd", Model: "SanDisk Ultra USB", Kind: "ssd", Transport: "usb", SizeBytes: 32015679488, Now: "iso9660, 2 partitions", Usable: false, Reason: "the installation media"},
	}
}

var demoSpeeds = map[string]float64{"/dev/sda": 162, "/dev/sdb": 486, "/dev/sdc": 441}

// DemoMeasure gives the speeds of the mockup after a short wait.
func DemoMeasure(ctx context.Context, path string, _ int64) (float64, error) {
	select {
	case <-time.After(600 * time.Millisecond):
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	return demoSpeeds[path], nil
}

// DemoFacts are the facts of the computer of the mockup.
func DemoFacts() Facts {
	return Facts{Computer: "MSI B85M-G43", CPU: "Intel Core i7-4790K", Threads: 8, MemoryB: 32 << 30, Firmware: "UEFI",
		Network: []NIC{{Name: "enp3s0", Driver: "r8169", SpeedMbit: 1000, Carrier: true, Addresses: []string{"192.168.1.50/24"}}}}
}

// DemoRunner simulates the commands. bootc makes a deployment in the
// target folder. Delay is the time of each command.
type DemoRunner struct {
	Target string
	Delay  time.Duration
	Line   func(string)
}

func (r DemoRunner) Streaming() bool { return false }

func (r DemoRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	d := r.Delay
	if name == "bootc" {
		d *= 8
	}
	select {
	case <-time.After(d):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	switch name {
	case "bootc":
		dep := filepath.Join(r.Target, "ostree", "deploy", "default")
		for _, p := range []string{filepath.Join(dep, "deploy", "demo.0", "etc"), filepath.Join(dep, "var"), filepath.Join(r.Target, "boot", "efi", "EFI")} {
			if err := os.MkdirAll(p, 0o755); err != nil {
				return nil, err
			}
		}
		return []byte("Installing the NidaOS image (demonstration)\nInstallation complete!\n"), nil
	case "blkid":
		return []byte("demo-" + filepath.Base(args[len(args)-1]) + "\n"), nil
	case "umount":
		if len(args) == 2 && args[0] == "-R" && strings.HasPrefix(args[1], os.TempDir()) {
			_ = os.RemoveAll(args[1])
		}
	}
	return nil, nil
}
