package main

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

// writeJSON writes v as indented JSON.
func (a *app) writeJSON(v any) error {
	enc := json.NewEncoder(a.out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// table writes aligned columns.
func (a *app) table() *tabwriter.Writer {
	return tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
}

// size writes a number of bytes with a binary unit: "1.5 GiB".
func size(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit && exp < 5; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// rate writes bytes for each second: "1.5 MB/s". The rates use the
// decimal units, as the network tools do.
func rate(bps float64) string {
	switch {
	case bps < 1000:
		return fmt.Sprintf("%.0f B/s", bps)
	case bps < 1e6:
		return fmt.Sprintf("%.1f kB/s", bps/1e3)
	case bps < 1e9:
		return fmt.Sprintf("%.1f MB/s", bps/1e6)
	}
	return fmt.Sprintf("%.2f GB/s", bps/1e9)
}

// uptime writes a duration in seconds as "3d 4h", "2h 5m" or "45s".
func uptime(secs float64) string {
	d := time.Duration(secs * float64(time.Second))
	days := int(d.Hours()) / 24
	h := int(d.Hours()) % 24
	m := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, h)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%ds", int(math.Round(secs)))
}

// clock writes a time in the local zone.
func clock(t time.Time) string {
	return t.Local().Format("15:04:05")
}

// stamp writes a date and a time in the local zone.
func stamp(t time.Time) string {
	return t.Local().Format("2006-01-02 15:04:05")
}

// levelTag writes a level with a fixed width.
func levelTag(level string) string {
	switch level {
	case "error":
		return "ERROR"
	case "warning":
		return "WARN "
	case "debug":
		return "DEBUG"
	}
	return "INFO "
}

// oneLine removes the line breaks of a message.
func oneLine(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r", ""), "\n", " ⏎ ")
}

// sortedKeys gives the keys of m in order.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
