//go:build linux

package metrics

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

// TestRealHost reads the real counters of the Linux computer that runs
// the test two times, for example on the GitHub runner.
func TestRealHost(t *testing.T) {
	s := NewSampler("/", "", time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.Sample()
	time.Sleep(300 * time.Millisecond)
	if !s.Sample() {
		t.Fatal("the second read did not publish a snapshot")
	}
	snap, _ := s.Hub.Last()
	if len(snap.Host.CPU.Threads) < 1 || snap.Host.Memory.TotalBytes == 0 {
		t.Errorf("host = %+v", snap.Host)
	}
	t.Logf("cpu %.1f%%, memory %.1f%%, %d disks, %d interfaces, %d workloads",
		snap.Host.CPU.UsagePercent, snap.Host.Memory.UsedPercent,
		len(snap.Host.Disks), len(snap.Host.Network), len(snap.Workloads))
}
