package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"strconv"

	"github.com/fathorMB/NidaOS/core/internal/client"
	"github.com/fathorMB/NidaOS/core/internal/logs"
)

func (a *app) logs(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	followFlag := fs.Bool("follow", false, "")
	fs.BoolVar(followFlag, "f", false, "")
	source := fs.String("source", "", "")
	level := fs.String("level", "", "")
	text := fs.String("q", "", "")
	since := fs.String("since", "", "")
	until := fs.String("until", "", "")
	n := fs.Int("n", 50, "")
	fs.IntVar(n, "tail", 50, "")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return fmt.Errorf("%w: logs has no arguments; use --source, --level or -q", errUsage)
	}
	if *n < 0 {
		return fmt.Errorf("%w: -n must be 0 or more", errUsage)
	}
	if *followFlag && (*since != "" || *until != "") {
		return fmt.Errorf("%w: -f does not accept --since or --until", errUsage)
	}

	q := url.Values{}
	for k, v := range map[string]string{"source": *source, "level": *level, "q": *text} {
		if v != "" {
			q.Set(k, v)
		}
	}

	switch {
	case *followFlag:
		q.Set("tail", strconv.Itoa(min(*n, 5000)))
		return a.follow(ctx, "/api/v1/logs/stream", q, func(ev client.Event) error {
			switch ev.Name {
			case "log":
				var l logs.Line
				if err := json.Unmarshal(ev.Data, &l); err != nil {
					return err
				}
				return a.printLine(l)
			case "dropped":
				var d struct {
					Count uint64 `json:"count"`
				}
				_ = json.Unmarshal(ev.Data, &d)
				fmt.Fprintf(a.errOut, "nida: %d lines were lost: the output was too slow.\n", d.Count)
			}
			return nil
		})

	case *since != "" || *until != "":
		// A search in the store, the newest first. Print the oldest first.
		if *since != "" {
			q.Set("since", *since)
		}
		if *until != "" {
			q.Set("until", *until)
		}
		q.Set("limit", strconv.Itoa(max(1, min(*n, 1000))))
		var res logs.Result
		if err := a.c.Get(ctx, "/api/v1/logs/search", q, &res); err != nil {
			return err
		}
		if a.json {
			return a.writeJSON(res)
		}
		for i := len(res.Lines) - 1; i >= 0; i-- {
			if err := a.printLine(res.Lines[i]); err != nil {
				return err
			}
		}
		if res.NextCursor != "" {
			fmt.Fprintf(a.errOut, "nida: there are more lines. Use -n with a larger number, or a shorter --since.\n")
		}
		return nil

	default:
		q.Set("tail", strconv.Itoa(min(*n, 5000)))
		var body struct {
			Lines []logs.Line `json:"lines"`
		}
		if err := a.c.Get(ctx, "/api/v1/logs", q, &body); err != nil {
			return err
		}
		if a.json {
			return a.writeJSON(body)
		}
		for _, l := range body.Lines {
			if err := a.printLine(l); err != nil {
				return err
			}
		}
		return nil
	}
}

// printLine writes one log line: time, level, source, message. With
// --json, it writes the JSON of the line.
func (a *app) printLine(l logs.Line) error {
	if a.json {
		b, err := json.Marshal(l)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(a.out, "%s\n", b)
		return err
	}
	_, err := fmt.Fprintf(a.out, "%s  %s  %s  %s\n", stamp(l.Time), levelTag(l.Level), l.Source.ID, oneLine(l.Message))
	return err
}
