//go:build linux

package system

import "testing"

// TestRealHost reads the real files of the Linux computer that runs the
// test, for example the GitHub runner. It makes sure that the collector
// accepts real files, not only the fixture.
func TestRealHost(t *testing.T) {
	c := NewCollector("/", "/no-plan-on-this-computer")
	s, err := c.State()
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if s.System.Hostname == "" || s.System.Kernel == "" || s.System.CPU.Threads < 1 || s.System.Memory.TotalBytes == 0 {
		t.Errorf("system = %+v", s.System)
	}
	if len(s.Tiers) != 2 {
		t.Errorf("tiers = %+v", s.Tiers)
	}
	t.Logf("host %s, %d threads, %d disks", s.System.Hostname, s.System.CPU.Threads, len(s.Disks))
}
