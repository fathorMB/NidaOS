package metrics

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

const podUID = "0f3c2a8e-1b2d-4c5e-8f9a-123456789abc"

// host writes the counter files of a host under a temporary root. Each
// call of set changes the counters, as the kernel does.
type host struct {
	t    *testing.T
	root string
}

func newHost(t *testing.T) *host {
	h := &host{t: t, root: t.TempDir()}
	h.write("/proc/meminfo", "MemTotal: 1000 kB\nMemAvailable: 250 kB\n")
	h.write("/proc/loadavg", "1.00 0.50 0.25 1/100 42\n")
	h.write("/sys/block/sda/size", "100\n")
	h.write("/sys/class/hwmon/hwmon0/name", "coretemp\n")
	h.write("/sys/class/hwmon/hwmon0/temp1_input", "47000\n")
	h.write("/sys/class/hwmon/hwmon1/name", "acpitz\n")
	h.write("/var/log/pods/apps_photos-7d9f_"+podUID+"/.keep", "")
	return h
}

func (h *host) write(p, content string) {
	h.t.Helper()
	full := filepath.Join(h.root, filepath.FromSlash(p))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// set writes the counters. cpu is "total idle" for the total line; the two
// threads get the same values divided by two.
func (h *host) set(total, idle, sectorsRead, sectorsWritten, ioMs, rx, tx, podMicros uint64) {
	h.write("/proc/stat", fmtStat(total, idle))
	h.write("/proc/diskstats",
		"   8       0 sda 10 0 "+u(sectorsRead)+" 0 10 0 "+u(sectorsWritten)+" 0 0 "+u(ioMs)+" 0\n"+
			"   8       1 sda1 10 0 "+u(sectorsRead)+" 0 10 0 "+u(sectorsWritten)+" 0 0 "+u(ioMs)+" 0\n"+
			"   7       0 loop0 1 0 1 0 1 0 1 0 0 1 0\n")
	h.write("/proc/net/dev",
		"Inter-|   Receive                            |  Transmit\n"+
			" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets\n"+
			"    lo: 999 1 0 0 0 0 0 0 999 1 0 0 0 0 0 0\n"+
			"  eno1: "+u(rx)+" 1 0 0 0 0 0 0 "+u(tx)+" 1 0 0 0 0 0 0\n"+
			"vethab12: 5 1 0 0 0 0 0 0 5 1 0 0 0 0 0 0\n")
	pod := "/sys/fs/cgroup/kubepods/burstable/pod" + podUID
	h.write(pod+"/cpu.stat", "usage_usec "+u(podMicros)+"\nuser_usec 1\n")
	h.write(pod+"/memory.current", "52428800\n")
	// A container inside the pod: the sampler must not count it again.
	h.write(pod+"/abc123/cpu.stat", "usage_usec 1\n")
	h.write("/sys/fs/cgroup/system.slice/k3s.service/cpu.stat", "usage_usec "+u(podMicros*2)+"\n")
	h.write("/sys/fs/cgroup/system.slice/k3s.service/memory.current", "1000\n")
}

func fmtStat(total, idle uint64) string {
	// user=total-idle, idle=idle, the other fields 0.
	line := func(name string, t, i uint64) string {
		return name + " " + u(t-i) + " 0 0 " + u(i) + " 0 0 0 0 0 0\n"
	}
	return line("cpu", total, idle) + line("cpu0", total/2, idle/2) + line("cpu1", total/2, idle/2) +
		"intr 1 2 3\nctxt 100\n"
}

func u(v uint64) string { return strconv.FormatUint(v, 10) }

func newTestSampler(h *host, clock *time.Time) *Sampler {
	s := NewSampler(h.root, "", time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.Now = func() time.Time { return *clock }
	return s
}

func TestSampler(t *testing.T) {
	h := newHost(t)
	clock := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	s := newTestSampler(h, &clock)

	h.set(1000, 800, 0, 0, 0, 1000, 500, 1_000_000)
	if s.Sample() {
		t.Fatal("the first read must not publish a snapshot")
	}
	if _, ok := s.Hub.Last(); ok {
		t.Fatal("the hub must be empty after the first read")
	}

	// Two seconds later: 1000 more processor ticks, 250 of them idle.
	clock = clock.Add(2 * time.Second)
	h.set(2000, 1050, 4096, 2048, 500, 5000, 2500, 2_000_000)
	if !s.Sample() {
		t.Fatal("the second read must publish a snapshot")
	}
	snap, _ := s.Hub.Last()

	if snap.Seq != 1 || snap.IntervalSeconds != 2 || !snap.Time.Equal(clock) {
		t.Errorf("seq, interval, time = %d, %v, %v", snap.Seq, snap.IntervalSeconds, snap.Time)
	}
	cpu := snap.Host.CPU
	if cpu.UsagePercent != 75 || len(cpu.Threads) != 2 || cpu.Threads[0] != 75 {
		t.Errorf("cpu = %+v", cpu)
	}
	if cpu.TemperatureC == nil || *cpu.TemperatureC != 47 {
		t.Errorf("temperature = %v", cpu.TemperatureC)
	}
	mem := snap.Host.Memory
	if mem.TotalBytes != 1000*1024 || mem.UsedBytes != 750*1024 || mem.UsedPercent != 75 {
		t.Errorf("memory = %+v", mem)
	}
	if snap.Host.Load != [3]float64{1, 0.5, 0.25} {
		t.Errorf("load = %v", snap.Host.Load)
	}

	// Only sda: sda1 is a partition and loop0 is virtual.
	if len(snap.Host.Disks) != 1 {
		t.Fatalf("disks = %+v", snap.Host.Disks)
	}
	d := snap.Host.Disks[0]
	if d.Name != "sda" || d.ReadBytesPerSecond != 4096*512/2 || d.WriteBytesPerSecond != 2048*512/2 || d.BusyPercent != 25 {
		t.Errorf("disk = %+v", d)
	}

	// Only eno1: lo and veth are not in the result.
	if len(snap.Host.Network) != 1 {
		t.Fatalf("network = %+v", snap.Host.Network)
	}
	n := snap.Host.Network[0]
	if n.Name != "eno1" || n.ReceiveBytesPerSecond != 2000 || n.TransmitBytesPerSecond != 1000 {
		t.Errorf("network = %+v", n)
	}

	if len(snap.Workloads) != 2 {
		t.Fatalf("workloads = %+v", snap.Workloads)
	}
	pod, svc := snap.Workloads[0], snap.Workloads[1]
	if pod.Kind != KindPod || pod.Namespace != "apps" || pod.Name != "photos-7d9f" || pod.ID != podUID ||
		pod.CPUCores != 0.5 || pod.MemoryBytes != 52428800 {
		t.Errorf("pod = %+v", pod)
	}
	if svc.Kind != KindService || svc.Name != "k3s" || svc.CPUCores != 1 || svc.MemoryBytes != 1000 {
		t.Errorf("service = %+v", svc)
	}
}

func TestSystemdPodDirectory(t *testing.T) {
	h := newHost(t)
	h.set(1000, 800, 0, 0, 0, 0, 0, 0)
	dir := "/sys/fs/cgroup/kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod" +
		"0f3c2a8e_1b2d_4c5e_8f9a_123456789abc.slice"
	h.write(dir+"/cpu.stat", "usage_usec 5\n")
	// Remove the cgroupfs directory of set, to keep only the systemd form.
	if err := os.RemoveAll(filepath.Join(h.root, "sys", "fs", "cgroup", "kubepods")); err != nil {
		t.Fatal(err)
	}
	w := reader{root: h.root, podLogDir: DefaultPodLogDir}.workloads()
	got, ok := w[KindPod+"/"+podUID]
	if !ok || got.name != "photos-7d9f" || got.cpuMicros != 5 {
		t.Errorf("workloads = %+v", w)
	}
}

func TestCounterGoesBack(t *testing.T) {
	h := newHost(t)
	clock := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	s := newTestSampler(h, &clock)
	h.set(1000, 800, 100, 100, 100, 100, 100, 100)
	s.Sample()
	clock = clock.Add(time.Second)
	h.set(1100, 850, 10, 10, 10, 10, 10, 10)
	s.Sample()
	snap, _ := s.Hub.Last()
	if d := snap.Host.Disks[0]; d.ReadBytesPerSecond != 0 || d.BusyPercent != 0 {
		t.Errorf("disk = %+v", d)
	}
	if snap.Workloads[0].CPUCores != 0 {
		t.Errorf("pod = %+v", snap.Workloads[0])
	}
}

func TestReadError(t *testing.T) {
	s := NewSampler(t.TempDir(), "", time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for i := 0; i < 2; i++ {
		if s.Sample() {
			t.Errorf("read %d: a sampler without /proc must not publish", i+1)
		}
	}
}
