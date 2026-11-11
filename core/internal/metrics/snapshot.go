package metrics

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// Snapshot is the result of one interval. The clients receive one Snapshot
// at each interval. Time is the end of the interval: an interface shows it
// as the time of the last data (decision D8).
type Snapshot struct {
	Seq             uint64     `json:"seq"`
	Time            time.Time  `json:"time"`
	IntervalSeconds float64    `json:"interval_seconds"`
	Host            Host       `json:"host"`
	Workloads       []Workload `json:"workloads"`
	// Databases are the managed databases (milestone M-04). It is not there
	// without databases.
	Databases []Database `json:"databases,omitempty"`
}

// Database is the live state of one managed database. The service reads
// it from the server each 10 seconds, thus it can be older than Time.
type Database struct {
	Name        string `json:"name"`
	State       string `json:"state"`
	SizeBytes   int64  `json:"size_bytes"`
	Connections int    `json:"connections"`
	// TransactionsPerSecond is the rate of the commits and the rollbacks.
	TransactionsPerSecond float64   `json:"transactions_per_second"`
	Observed              time.Time `json:"observed"`
}

// Host is the metrics of the computer.
type Host struct {
	CPU     HostCPU    `json:"cpu"`
	Memory  HostMemory `json:"memory"`
	Load    [3]float64 `json:"load"`
	Disks   []DiskIO   `json:"disks"`
	Network []NetIO    `json:"network"`
}

// HostCPU is the processor usage. The percentages are from 0 to 100.
type HostCPU struct {
	UsagePercent float64 `json:"usage_percent"`
	// Threads is the usage of each hardware thread, in the order cpu0, cpu1...
	Threads []float64 `json:"threads"`
	// TemperatureC is the package temperature. It is empty when the
	// computer has no sensor.
	TemperatureC *float64 `json:"temperature_c,omitempty"`
}

// HostMemory is the main memory.
type HostMemory struct {
	TotalBytes     uint64  `json:"total_bytes"`
	UsedBytes      uint64  `json:"used_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	UsedPercent    float64 `json:"used_percent"`
}

// DiskIO is the input and output of one disk.
type DiskIO struct {
	Name                string  `json:"name"`
	ReadBytesPerSecond  float64 `json:"read_bytes_per_second"`
	WriteBytesPerSecond float64 `json:"write_bytes_per_second"`
	// BusyPercent is the part of the interval with I/O in progress.
	BusyPercent float64 `json:"busy_percent"`
}

// NetIO is the traffic of one network interface.
type NetIO struct {
	Name                   string  `json:"name"`
	ReceiveBytesPerSecond  float64 `json:"receive_bytes_per_second"`
	TransmitBytesPerSecond float64 `json:"transmit_bytes_per_second"`
}

// Workload is the metrics of one pod or one systemd service.
type Workload struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	ID        string `json:"id"`
	// CPUCores is the processor usage in cores: 1.0 is one full thread.
	CPUCores    float64 `json:"cpu_cores"`
	MemoryBytes uint64  `json:"memory_bytes"`
}

// diff calculates the snapshot of the interval from prev to cur.
func diff(prev, cur raw) Snapshot {
	secs := cur.time.Sub(prev.time).Seconds()
	if secs <= 0 {
		secs = 1e-9
	}
	s := Snapshot{
		Time:            cur.time.UTC(),
		IntervalSeconds: round(secs, 3),
		Workloads:       []Workload{},
	}

	// Processor.
	s.Host.CPU.UsagePercent = cpuPercent(prev.cpu["cpu"], cur.cpu["cpu"])
	threads := make([]string, 0, len(cur.cpu))
	for k := range cur.cpu {
		if k != "cpu" {
			threads = append(threads, k)
		}
	}
	sort.Slice(threads, func(i, j int) bool { return threadIndex(threads[i]) < threadIndex(threads[j]) })
	s.Host.CPU.Threads = make([]float64, 0, len(threads))
	for _, k := range threads {
		s.Host.CPU.Threads = append(s.Host.CPU.Threads, cpuPercent(prev.cpu[k], cur.cpu[k]))
	}
	s.Host.CPU.TemperatureC = cur.tempC

	// Memory and load.
	m := &s.Host.Memory
	m.TotalBytes, m.AvailableBytes = cur.memTotal, cur.memAvail
	if cur.memTotal >= cur.memAvail {
		m.UsedBytes = cur.memTotal - cur.memAvail
	}
	if cur.memTotal > 0 {
		m.UsedPercent = round(100*float64(m.UsedBytes)/float64(cur.memTotal), 1)
	}
	s.Host.Load = cur.load

	// Disks.
	s.Host.Disks = []DiskIO{}
	for _, name := range sortedKeys(cur.disks) {
		c, p := cur.disks[name], prev.disks[name]
		s.Host.Disks = append(s.Host.Disks, DiskIO{
			Name:                name,
			ReadBytesPerSecond:  rate(p.readBytes, c.readBytes, secs),
			WriteBytesPerSecond: rate(p.writeBytes, c.writeBytes, secs),
			BusyPercent:         clamp(round(100*float64(delta(p.ioMillis, c.ioMillis))/(secs*1000), 1)),
		})
	}

	// Network.
	s.Host.Network = []NetIO{}
	for _, name := range sortedKeys(cur.nets) {
		c, p := cur.nets[name], prev.nets[name]
		s.Host.Network = append(s.Host.Network, NetIO{
			Name:                   name,
			ReceiveBytesPerSecond:  rate(p.rxBytes, c.rxBytes, secs),
			TransmitBytesPerSecond: rate(p.txBytes, c.txBytes, secs),
		})
	}

	// Workloads. A workload that is new in this interval has no previous
	// counter: its processor usage is 0 for this interval.
	for _, key := range sortedKeys(cur.workloads) {
		c := cur.workloads[key]
		w := Workload{Kind: c.kind, Namespace: c.namespace, Name: c.name, ID: c.id, MemoryBytes: c.memoryBytes}
		if p, ok := prev.workloads[key]; ok {
			w.CPUCores = round(float64(delta(p.cpuMicros, c.cpuMicros))/1e6/secs, 3)
		}
		s.Workloads = append(s.Workloads, w)
	}
	return s
}

func cpuPercent(p, c cpuTimes) float64 {
	total := delta(p.total, c.total)
	if total == 0 {
		return 0
	}
	idle := min(delta(p.idle, c.idle), total)
	return clamp(round(100*float64(total-idle)/float64(total), 1))
}

// delta gives c - p. A counter that goes back (a new device with the same
// name, or a counter that wraps) gives 0.
func delta(p, c uint64) uint64 {
	if c < p {
		return 0
	}
	return c - p
}

func rate(p, c uint64, secs float64) float64 {
	return round(float64(delta(p, c))/secs, 0)
}

func round(v float64, places int) float64 {
	f, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'f', places, 64), 64)
	return f
}

func clamp(v float64) float64 {
	return min(max(v, 0), 100)
}

func threadIndex(name string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(name, "cpu"))
	if err != nil {
		return 1 << 30
	}
	return n
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
