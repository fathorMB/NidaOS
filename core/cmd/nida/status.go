package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"sort"
	"strings"

	"github.com/fathorMB/NidaOS/core/internal/client"
	"github.com/fathorMB/NidaOS/core/internal/metrics"
	"github.com/fathorMB/NidaOS/core/internal/system"
)

// topWorkloads is the number of workloads in the status.
const topWorkloads = 8

func (a *app) status(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return fmt.Errorf("%w: status has no arguments", errUsage)
	}

	var st system.State
	if err := a.c.Get(ctx, "/api/v1/state", nil, &st); err != nil {
		return err
	}
	// The metrics are not ready in the first seconds after the start.
	var snap *metrics.Snapshot
	var s metrics.Snapshot
	err = a.c.Get(ctx, "/api/v1/metrics", nil, &s)
	var apiErr *client.APIError
	switch {
	case err == nil:
		snap = &s
	case errors.As(err, &apiErr) && apiErr.Code == "metrics_unavailable":
	default:
		return err
	}

	if a.json {
		return a.writeJSON(struct {
			State   system.State      `json:"state"`
			Metrics *metrics.Snapshot `json:"metrics"`
		}{st, snap})
	}
	a.printStatus(st, snap)
	return nil
}

func (a *app) printStatus(st system.State, snap *metrics.Snapshot) {
	sys := st.System
	osName := sys.OS.PrettyName
	if osName == "" {
		osName = strings.TrimSpace(sys.OS.Name + " " + sys.OS.Version)
	}
	if sys.OS.ImageVersion != "" {
		osName += " · image " + sys.OS.ImageVersion
	}
	fmt.Fprintf(a.out, "%s  %s · kernel %s · up %s\n\n", sys.Hostname, osName, sys.Kernel, uptime(sys.UptimeSeconds))

	w := a.table()
	cpu := fmt.Sprintf("%s, %d threads", sys.CPU.Model, sys.CPU.Threads)
	mem := fmt.Sprintf("%s of %s", size(sys.Memory.TotalBytes-sys.Memory.AvailableBytes), size(sys.Memory.TotalBytes))
	if snap != nil {
		h := snap.Host
		cpu = fmt.Sprintf("%.1f%%\t%s", h.CPU.UsagePercent, cpu)
		if h.CPU.TemperatureC != nil {
			cpu += fmt.Sprintf(", %.0f °C", *h.CPU.TemperatureC)
		}
		mem = fmt.Sprintf("%.1f%%\t%s of %s", h.Memory.UsedPercent, size(h.Memory.UsedBytes), size(h.Memory.TotalBytes))
	} else {
		cpu = "-\t" + cpu
		mem = "-\t" + mem
	}
	fmt.Fprintf(w, "Processor\t%s\n", cpu)
	fmt.Fprintf(w, "Memory\t%s\n", mem)
	fmt.Fprintf(w, "Load\t%.2f %.2f %.2f\n", sys.Load.One, sys.Load.Five, sys.Load.Fifteen)
	w.Flush()

	fmt.Fprintln(a.out, "\nDisks")
	w = a.table()
	io := map[string]metrics.DiskIO{}
	if snap != nil {
		for _, d := range snap.Host.Disks {
			io[d.Name] = d
		}
	}
	for _, d := range st.Disks {
		role := d.Role
		if role == "" {
			role = "-"
		}
		line := fmt.Sprintf("  %s\t%s\t%s\t%s\t%s", d.Name, strings.ToUpper(d.Kind), size(d.SizeBytes), role, d.Model)
		if x, ok := io[d.Name]; ok {
			line += fmt.Sprintf("\tread %s, write %s", rate(x.ReadBytesPerSecond), rate(x.WriteBytesPerSecond))
		}
		fmt.Fprintln(w, line)
	}
	w.Flush()

	fmt.Fprintln(a.out, "\nStorage tiers")
	w = a.table()
	for _, t := range st.Tiers {
		state := strings.ReplaceAll(t.State, "_", " ")
		where := t.Disk
		if t.Device != "" {
			where = t.Device
		}
		if where == "" {
			where = "-"
		}
		usage := ""
		if t.Usage != nil && t.Usage.TotalBytes > 0 {
			usage = fmt.Sprintf("%s of %s used, %s free", size(t.Usage.UsedBytes), size(t.Usage.TotalBytes), size(t.Usage.FreeBytes))
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", t.Name, state, where, usage)
	}
	w.Flush()

	if snap == nil {
		fmt.Fprintln(a.out, "\nThe metrics are not ready yet. Try again in a few seconds.")
		return
	}
	fmt.Fprintln(a.out, "\nNetwork")
	w = a.table()
	for _, n := range snap.Host.Network {
		fmt.Fprintf(w, "  %s\tin %s\tout %s\n", n.Name, rate(n.ReceiveBytesPerSecond), rate(n.TransmitBytesPerSecond))
	}
	w.Flush()

	ws := append([]metrics.Workload(nil), snap.Workloads...)
	sort.SliceStable(ws, func(i, j int) bool {
		if ws[i].CPUCores != ws[j].CPUCores {
			return ws[i].CPUCores > ws[j].CPUCores
		}
		return ws[i].MemoryBytes > ws[j].MemoryBytes
	})
	fmt.Fprintf(a.out, "\nWorkloads (%d, the %d with the most processor usage)\n", len(ws), min(len(ws), topWorkloads))
	w = a.table()
	for _, wl := range ws[:min(len(ws), topWorkloads)] {
		name := wl.Name
		if wl.Namespace != "" {
			name = wl.Namespace + "/" + wl.Name
		}
		fmt.Fprintf(w, "  %s\t%s\t%.2f cores\t%s\n", wl.Kind, name, wl.CPUCores, size(wl.MemoryBytes))
	}
	w.Flush()
	fmt.Fprintf(a.out, "\nMetrics at %s\n", clock(snap.Time))
}
