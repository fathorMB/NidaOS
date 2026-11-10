package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"

	"github.com/fathorMB/NidaOS/core/internal/client"
	"github.com/fathorMB/NidaOS/core/internal/metrics"
)

func (a *app) metrics(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("metrics", flag.ContinueOnError)
	watch := fs.Bool("watch", false, "")
	fs.BoolVar(watch, "w", false, "")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return fmt.Errorf("%w: metrics has no arguments", errUsage)
	}

	if !*watch {
		var s metrics.Snapshot
		if err := a.c.Get(ctx, "/api/v1/metrics", nil, &s); err != nil {
			return err
		}
		if a.json {
			return a.writeJSON(s)
		}
		a.printSnapshotHeader()
		a.printSnapshot(s)
		return nil
	}

	if !a.json {
		a.printSnapshotHeader()
	}
	return a.follow(ctx, "/api/v1/metrics/stream", nil, func(ev client.Event) error {
		if ev.Name != "metrics" {
			return nil
		}
		if a.json {
			// One JSON object on each line, for the programs.
			_, err := fmt.Fprintf(a.out, "%s\n", ev.Data)
			return err
		}
		var s metrics.Snapshot
		if err := json.Unmarshal(ev.Data, &s); err != nil {
			return err
		}
		a.printSnapshot(s)
		return nil
	})
}

func (a *app) printSnapshotHeader() {
	fmt.Fprintf(a.out, "%-8s  %6s  %6s  %-21s  %-21s  %s\n", "TIME", "CPU", "MEMORY", "DISKS (read/write)", "NETWORK (in/out)", "LOAD")
}

// printSnapshot writes one line with the totals of a snapshot.
func (a *app) printSnapshot(s metrics.Snapshot) {
	var dr, dw, ni, no float64
	for _, d := range s.Host.Disks {
		dr += d.ReadBytesPerSecond
		dw += d.WriteBytesPerSecond
	}
	for _, n := range s.Host.Network {
		ni += n.ReceiveBytesPerSecond
		no += n.TransmitBytesPerSecond
	}
	fmt.Fprintf(a.out, "%-8s  %5.1f%%  %5.1f%%  %-21s  %-21s  %.2f\n",
		clock(s.Time), s.Host.CPU.UsagePercent, s.Host.Memory.UsedPercent,
		rate(dr)+" / "+rate(dw), rate(ni)+" / "+rate(no), s.Host.Load[0])
}
