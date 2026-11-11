package mdns

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// reuse lets other mDNS programs (for example avahi) listen on port 5353
// at the same time. Each program gets each query.
func reuse(_, _ string, c syscall.RawConn) error {
	var serr error
	err := c.Control(func(fd uintptr) {
		if serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); serr != nil {
			return
		}
		serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
	})
	if err != nil {
		return err
	}
	return serr
}
