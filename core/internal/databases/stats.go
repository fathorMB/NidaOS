package databases

import (
	"context"
	"sort"
	"time"
)

// LiveStats is the live state of a database for the metrics.
type LiveStats struct {
	Name                  string
	State                 string
	Stats                 Stats
	TransactionsPerSecond float64
	Observed              time.Time
}

type statsEntry struct {
	stats    Stats
	rate     float64
	observed time.Time
}

// collectStats reads the statistics of each ready database. The rate of
// the transactions is the difference to the last read.
func (c *Controller) collectStats(ctx context.Context) {
	seen := map[string]bool{}
	for _, d := range c.store.List() {
		seen[d.Name] = true
		if d.State != StateReady {
			continue
		}
		s, err := ReadStats(ctx, c.sql, d.Name)
		if err != nil {
			c.opts.Logger.Debug("the controller cannot read the statistics of a database", "database", d.Name, "err", err)
			continue
		}
		now := c.opts.Now()
		c.mu.Lock()
		e := statsEntry{stats: s, observed: now}
		if prev, ok := c.stats[d.Name]; ok {
			if secs := now.Sub(prev.observed).Seconds(); secs > 0 && s.Transactions >= prev.stats.Transactions {
				e.rate = float64(s.Transactions-prev.stats.Transactions) / secs
			}
		}
		c.stats[d.Name] = e
		c.mu.Unlock()
	}
	c.mu.Lock()
	for n := range c.stats {
		if !seen[n] {
			delete(c.stats, n)
		}
	}
	c.mu.Unlock()
}

// Live gives the live state of all the databases, sorted by name. A
// database without a read yet has zero values.
func (c *Controller) Live() []LiveStats {
	list := c.store.List()
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]LiveStats, 0, len(list))
	for _, d := range list {
		e := c.stats[d.Name]
		out = append(out, LiveStats{Name: d.Name, State: d.State, Stats: e.stats, TransactionsPerSecond: e.rate, Observed: e.observed})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
