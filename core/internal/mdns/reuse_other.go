//go:build !linux

package mdns

import "syscall"

// reuse does nothing on other systems: NidaOS runs on Linux.
func reuse(_, _ string, _ syscall.RawConn) error { return nil }
