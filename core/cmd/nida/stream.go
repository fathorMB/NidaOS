package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/client"
)

// maxRetry is the maximum wait before a stream connects again.
const maxRetry = 30 * time.Second

// follow keeps a stream open. When the stream stops, it tells the
// operator, with the time of the last data (decision D8), and connects
// again. It gives the last event id to the service, thus no log line is
// lost when the service still has it. It stops for an error of access.
func (a *app) follow(ctx context.Context, path string, q url.Values, fn func(client.Event) error) error {
	lastID := ""
	var lastData time.Time
	wait := a.retry
	for {
		err := a.c.Subscribe(ctx, path, q, lastID, func(ev client.Event) error {
			if ev.ID != "" {
				lastID = ev.ID
			}
			lastData = a.now()
			wait = a.retry
			return fn(ev)
		})
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var apiErr *client.APIError
		if errors.As(err, &apiErr) && apiErr.Status < 500 {
			return err
		}
		last := "no data"
		if !lastData.IsZero() {
			last = "last data at " + clock(lastData)
		}
		fmt.Fprintf(a.errOut, "nida: the stream stopped at %s (%s): %v. Connect again in %s.\n",
			clock(a.now()), last, err, wait.Round(time.Second))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		wait = min(wait*2, maxRetry)
	}
}
