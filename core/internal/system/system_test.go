package system

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// The fixture testdata/host is a copy of the files of a NidaOS host with
// the disks of the target computer: sda (SSD, os), sdb (SSD, performance,
// mounted) and sdc (HDD, standard, not mounted). loop0 is a virtual device.

var fixedTime = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func newFixture(t *testing.T) *Collector {
	t.Helper()
	c := NewCollector(filepath.Join("testdata", "host"), "")
	c.Now = func() time.Time { return fixedTime }
	c.Statfs = func(p string) (Usage, error) {
		return Usage{TotalBytes: 1000, UsedBytes: 250, FreeBytes: 750}, nil
	}
	return c
}

func TestSystem(t *testing.T) {
	s, err := newFixture(t).System()
	if err != nil {
		t.Fatalf("System: %v", err)
	}
	if s.Hostname != "nida" || s.Kernel != "6.17.7-300.fc45.x86_64" {
		t.Errorf("hostname, kernel = %q, %q", s.Hostname, s.Kernel)
	}
	if s.OS.Name != "Fedora Linux" || s.OS.Version != "45" || s.OS.ImageVersion != "v1" {
		t.Errorf("os = %+v", s.OS)
	}
	if s.UptimeSeconds != 3725.42 {
		t.Errorf("uptime = %v", s.UptimeSeconds)
	}
	if s.CPU.Threads != 8 || s.CPU.Model != "Intel(R) Core(TM) i7-4790K CPU @ 4.00GHz" {
		t.Errorf("cpu = %+v", s.CPU)
	}
	if s.Memory.TotalBytes != 32768000*1024 || s.Memory.AvailableBytes != 20480000*1024 {
		t.Errorf("memory = %+v", s.Memory)
	}
	if s.Load != (Load{One: 0.52, Five: 0.40, Fifteen: 0.31}) {
		t.Errorf("load = %+v", s.Load)
	}
	if !s.Time.Equal(fixedTime) {
		t.Errorf("time = %v", s.Time)
	}
}

func TestDisks(t *testing.T) {
	disks, err := newFixture(t).Disks()
	if err != nil {
		t.Fatalf("Disks: %v", err)
	}
	if len(disks) != 3 {
		t.Fatalf("got %d disks, want 3 (loop0 is virtual): %+v", len(disks), disks)
	}
	want := []struct {
		name, kind, role, model string
		size                    uint64
		parts                   int
	}{
		{"sda", KindSSD, "os", "Samsung SSD 850", 234441648 * 512, 3},
		{"sdb", KindSSD, TierPerformance, "Crucial MX500", 488397168 * 512, 1},
		{"sdc", KindHDD, TierStandard, "WDC WD20EZRZ", 3907029168 * 512, 1},
	}
	for i, w := range want {
		d := disks[i]
		if d.Name != w.name || d.Kind != w.kind || d.Role != w.role || d.Model != w.model ||
			d.SizeBytes != w.size || len(d.Partitions) != w.parts {
			t.Errorf("disk %d = %+v, want %+v", i, d, w)
		}
	}

	// The first mount of sda3 is /sysroot: the later bind mounts do not
	// replace it.
	sda3 := disks[0].Partitions[2]
	if sda3.Name != "sda3" || sda3.MountPoint != "/sysroot" || sda3.FSType != "xfs" {
		t.Errorf("sda3 = %+v", sda3)
	}
	if p := disks[2].Partitions[0]; p.MountPoint != "" {
		t.Errorf("sdc1 must not be mounted: %+v", p)
	}
}

func TestTiers(t *testing.T) {
	tiers, err := newFixture(t).Tiers()
	if err != nil {
		t.Fatalf("Tiers: %v", err)
	}
	if len(tiers) != 2 {
		t.Fatalf("got %d tiers", len(tiers))
	}
	perf, std := tiers[0], tiers[1]
	if perf.Name != TierPerformance || perf.StorageClass != "nida-performance" ||
		perf.Path != "/var/nida/performance" || perf.Disk != "sdb" || perf.Device != "sdb1" ||
		perf.State != TierReady || perf.Usage == nil || perf.Usage.FreeBytes != 750 {
		t.Errorf("performance = %+v", perf)
	}
	if std.Name != TierStandard || std.Disk != "sdc" || std.State != TierNotMounted || std.Usage != nil {
		t.Errorf("standard = %+v", std)
	}
}

func TestTiersWithoutPlan(t *testing.T) {
	c := newFixture(t)
	c.PlanFile = "/var/lib/nidaos/no-such-plan.txt"
	tiers, err := c.Tiers()
	if err != nil {
		t.Fatalf("Tiers: %v", err)
	}
	// The performance tier is mounted, thus it is ready without a plan.
	if tiers[0].State != TierReady || tiers[1].State != TierNoDisk {
		t.Errorf("tiers = %+v", tiers)
	}
	disks, err := c.Disks()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range disks {
		if d.Role != "" {
			t.Errorf("disk %s has the role %q without a plan", d.Name, d.Role)
		}
	}
}

func TestTiersWithoutStatfs(t *testing.T) {
	c := newFixture(t)
	c.Statfs = func(string) (Usage, error) { return Usage{}, ErrNoStatfs }
	tiers, err := c.Tiers()
	if err != nil {
		t.Fatalf("Tiers: %v", err)
	}
	if tiers[0].State != TierReady || tiers[0].Usage != nil {
		t.Errorf("performance = %+v", tiers[0])
	}

	c.Statfs = func(string) (Usage, error) { return Usage{}, errors.New("disk error") }
	if _, err := c.Tiers(); err == nil {
		t.Error("a statfs error must give an error")
	}
}

func TestState(t *testing.T) {
	s, err := newFixture(t).State()
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if s.System.Hostname != "nida" || len(s.Disks) != 3 || len(s.Tiers) != 2 || !s.Time.Equal(fixedTime) {
		t.Errorf("state = %+v", s)
	}
}

func TestMissingRoot(t *testing.T) {
	c := NewCollector(filepath.Join("testdata", "no-such-root"), "")
	if _, err := c.System(); err == nil {
		t.Error("System must give an error without /proc")
	}
	if _, err := c.Disks(); err == nil {
		t.Error("Disks must give an error without /sys/block")
	}
}

func TestUnescapeMount(t *testing.T) {
	if got := unescapeMount(`/var/nida/my\040disk`); got != "/var/nida/my disk" {
		t.Errorf("got %q", got)
	}
}
