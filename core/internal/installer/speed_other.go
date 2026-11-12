//go:build !linux

package installer

import (
	"context"
	"time"
)

func measure(context.Context, string, int64, time.Duration) (float64, error) {
	return 0, ErrNoSpeedTest
}
