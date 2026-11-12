//go:build linux

package installer

import (
	"context"
	"fmt"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// measure reads blocks with O_DIRECT from four places of the disk. The
// test reads from the start, the first third, the second third and near
// the end, thus a hard disk shows its middle speed.
func measure(ctx context.Context, path string, size int64, d time.Duration) (float64, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|unix.O_DIRECT, 0)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	// O_DIRECT needs a buffer aligned to the block size of the disk.
	raw := make([]byte, SpeedBlock+4096)
	off := 4096 - int(uintptr(unsafe.Pointer(&raw[0]))&4095)
	if off == 4096 {
		off = 0
	}
	buf := raw[off : off+SpeedBlock]

	starts := []int64{0, size / 3, 2 * size / 3, size - 512<<20}
	var total int64
	begin := time.Now()
	deadline := begin.Add(d)
	for i := 0; time.Now().Before(deadline); i++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		start := starts[i%len(starts)]
		if start < 0 {
			start = 0
		}
		pos := start + int64(i/len(starts))*SpeedBlock
		pos -= pos % 4096
		if pos+SpeedBlock > size {
			pos = 0
		}
		n, err := f.ReadAt(buf, pos)
		if err != nil && n == 0 {
			return 0, fmt.Errorf("read %s: %w", path, err)
		}
		total += int64(n)
	}
	sec := time.Since(begin).Seconds()
	if sec <= 0 || total == 0 {
		return 0, fmt.Errorf("read %s: no data", path)
	}
	return float64(total) / sec / 1e6, nil
}
