package installer

import (
	"context"
	"errors"
	"time"
)

// SpeedTime is the time of the read test of one disk.
const SpeedTime = 3 * time.Second

// SpeedBlock is the size of one read of the test.
const SpeedBlock = 4 << 20

// ErrNoSpeedTest: this system cannot measure a disk (not Linux).
var ErrNoSpeedTest = errors.New("this system cannot measure the speed of a disk")

// Measurer gives the read speed of a disk in MB/s.
type Measurer func(ctx context.Context, path string, size int64) (float64, error)

// MeasureDisk reads the disk without the page cache during SpeedTime and
// gives the speed in MB/s. It only reads: it does not change the disk.
func MeasureDisk(ctx context.Context, path string, size int64) (float64, error) {
	return measure(ctx, path, size, SpeedTime)
}
