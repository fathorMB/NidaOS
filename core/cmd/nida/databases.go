package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/databases"
)

const dbUsage = `use one of:
  db list
  db show NAME
  db add NAME [--version 17|18] [--tier performance|standard] [--size 10G] [--memory 512M] [--cpu MILLIS]
              [--restore DATABASE/BACKUP] [--wait]
  db link NAME APP [--ro]
  db unlink NAME APP
  db lan NAME on|off
  db connection NAME
  db backup NAME [--wait]
  db backups NAME
  db backup-rm NAME BACKUP
  db update NAME [--wait]
  db remove NAME --yes`

// dbView is a database of the API.
type dbView struct {
	databases.Database
	UpdateAvailable string `json:"update_available"`
	Address         string `json:"address"`
	Live            *struct {
		SizeBytes             int64     `json:"size_bytes"`
		Connections           int       `json:"connections"`
		TransactionsPerSecond float64   `json:"transactions_per_second"`
		Observed              time.Time `json:"observed"`
	} `json:"live"`
}

func dbPath(name string, parts ...string) string {
	p := "/api/v1/databases/" + url.PathEscape(name)
	for _, s := range parts {
		p += "/" + url.PathEscape(s)
	}
	return p
}

func (a *app) databases(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: %s", errUsage, dbUsage)
	}
	cmd, rest := args[0], args[1:]
	one := func(use string) (string, error) {
		if len(rest) != 1 {
			return "", fmt.Errorf("%w: use %s", errUsage, use)
		}
		return rest[0], nil
	}
	switch cmd {
	case "list", "ls":
		return a.dbList(ctx)
	case "show":
		name, err := one("db show NAME")
		if err != nil {
			return err
		}
		return a.dbShow(ctx, name)
	case "add":
		return a.dbAdd(ctx, rest)
	case "link":
		fs := flag.NewFlagSet("db link", flag.ContinueOnError)
		ro := fs.Bool("ro", false, "")
		r, err := parseFlags(fs, rest)
		if err != nil {
			return err
		}
		if len(r) != 2 {
			return fmt.Errorf("%w: use db link NAME APP [--ro]", errUsage)
		}
		mode := databases.ModeRW
		if *ro {
			mode = databases.ModeRO
		}
		var v dbView
		if err := a.c.Do(ctx, http.MethodPost, dbPath(r[0], "links"), map[string]string{"app": r[1], "mode": mode}, &v); err != nil {
			return err
		}
		if a.json {
			return a.writeJSON(v)
		}
		fmt.Fprintf(a.out, "The app %s is connected to %s (%s). Its copies start again with DATABASE_URL and the PG* variables.\n", r[1], r[0], mode)
		return nil
	case "unlink":
		if len(rest) != 2 {
			return fmt.Errorf("%w: use db unlink NAME APP", errUsage)
		}
		if err := a.c.Do(ctx, http.MethodDelete, dbPath(rest[0], "links", rest[1]), nil, nil); err != nil {
			return err
		}
		a.say("The app %s is disconnected from %s. Its copies start again without the connection.\n", rest[1], rest[0])
		return nil
	case "lan":
		if len(rest) != 2 || (rest[1] != "on" && rest[1] != "off") {
			return fmt.Errorf("%w: use db lan NAME on|off", errUsage)
		}
		var v dbView
		if err := a.c.Do(ctx, http.MethodPut, dbPath(rest[0], "lan"), map[string]bool{"on": rest[1] == "on"}, &v); err != nil {
			return err
		}
		if a.json {
			return a.writeJSON(v)
		}
		if v.LAN {
			fmt.Fprintf(a.out, "The devices of the home network can connect to %s at %s, with TLS. See: nida db connection %s\n", v.Name, v.Address, v.Name)
		} else {
			fmt.Fprintf(a.out, "The access of %s from the home network is off.\n", v.Name)
		}
		return nil
	case "connection":
		name, err := one("db connection NAME")
		if err != nil {
			return err
		}
		var c map[string]any
		if err := a.c.Get(ctx, dbPath(name, "connection"), nil, &c); err != nil {
			return err
		}
		if a.json {
			return a.writeJSON(c)
		}
		w := a.table()
		for _, k := range []string{"host", "port", "database", "user", "password", "sslmode", "url"} {
			if v, ok := c[k]; ok {
				fmt.Fprintf(w, "%s\t%v\n", k, v)
			}
		}
		if err := w.Flush(); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "\n%v\n", c["note"])
		return nil
	case "backup":
		return a.dbBackup(ctx, rest)
	case "backups":
		name, err := one("db backups NAME")
		if err != nil {
			return err
		}
		return a.dbBackups(ctx, name)
	case "backup-rm":
		if len(rest) != 2 {
			return fmt.Errorf("%w: use db backup-rm NAME BACKUP", errUsage)
		}
		if err := a.c.Do(ctx, http.MethodDelete, dbPath(rest[0], "backups", rest[1]), nil, nil); err != nil {
			return err
		}
		a.say("The backup %s is deleted.\n", rest[1])
		return nil
	case "update":
		fs := flag.NewFlagSet("db update", flag.ContinueOnError)
		wait := fs.Bool("wait", false, "")
		r, err := parseFlags(fs, rest)
		if err != nil {
			return err
		}
		if len(r) != 1 {
			return fmt.Errorf("%w: use db update NAME [--wait]", errUsage)
		}
		var v dbView
		if err := a.c.Do(ctx, http.MethodPost, dbPath(r[0], "update"), nil, &v); err != nil {
			return err
		}
		a.say("The update of %s to %s started. NidaOS makes a backup first.\n", r[0], v.UpdateTo)
		if !*wait {
			return nil
		}
		target := v.UpdateTo
		v, err = a.dbWait(ctx, r[0])
		if err != nil {
			return err
		}
		if v.Image != target {
			return fmt.Errorf("the update failed: %s", v.Reason)
		}
		return a.dbDone(v, fmt.Sprintf("The database %s runs %s.", v.Name, v.Image))
	case "remove", "rm":
		fs := flag.NewFlagSet("db remove", flag.ContinueOnError)
		yes := fs.Bool("yes", false, "")
		r, err := parseFlags(fs, rest)
		if err != nil {
			return err
		}
		if len(r) != 1 {
			return fmt.Errorf("%w: use db remove NAME --yes", errUsage)
		}
		if !*yes {
			return fmt.Errorf("%w: the remove deletes the database and its data, and disconnects its apps. The backups stay. Add --yes to confirm", errUsage)
		}
		if err := a.c.Do(ctx, http.MethodDelete, dbPath(r[0]), nil, nil); err != nil {
			return err
		}
		a.say("The database %s is removed. Its backups stay: see nida db backups %s\n", r[0], r[0])
		return nil
	}
	return fmt.Errorf("%w: unknown db command %q\n%s", errUsage, cmd, dbUsage)
}

func (a *app) dbDone(v dbView, text string) error {
	if a.json {
		return a.writeJSON(v)
	}
	fmt.Fprintln(a.out, text)
	return nil
}

func (a *app) dbList(ctx context.Context) error {
	var body struct {
		Databases []dbView `json:"databases"`
		Versions  []int    `json:"versions"`
	}
	if err := a.c.Get(ctx, "/api/v1/databases", nil, &body); err != nil {
		return err
	}
	if a.json {
		return a.writeJSON(body)
	}
	if len(body.Databases) == 0 {
		fmt.Fprintln(a.out, "There are no databases. Add one with: nida db add NAME --wait")
		return nil
	}
	w := a.table()
	fmt.Fprintln(w, "NAME\tSTATE\tVERSION\tTIER\tSIZE\tCONNECTIONS\tAPPS\tHOME NETWORK")
	for _, d := range body.Databases {
		size, conns := "-", "-"
		if d.Live != nil {
			size, conns = memText(d.Live.SizeBytes), strconv.Itoa(d.Live.Connections)
		}
		var appsList []string
		for _, l := range d.Links {
			appsList = append(appsList, l.App)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", d.Name, d.State, imageVersion(d.Image), d.Tier, size, conns,
			dash(strings.Join(appsList, ",")), dash(d.Address))
	}
	return w.Flush()
}

// imageVersion gives the version in an image reference, for example
// "18.1" of "postgres:18.1-alpine".
func imageVersion(img string) string {
	_, tag, ok := strings.Cut(img[strings.LastIndex(img, "/")+1:], ":")
	if !ok {
		return img
	}
	return strings.TrimSuffix(tag, "-alpine")
}

func (a *app) dbShow(ctx context.Context, name string) error {
	var v dbView
	if err := a.c.Get(ctx, dbPath(name), nil, &v); err != nil {
		return err
	}
	if a.json {
		return a.writeJSON(v)
	}
	w := a.table()
	fmt.Fprintf(w, "Database\t%s\n", v.Name)
	state := v.State
	if v.Reason != "" {
		state += " (" + v.Reason + ")"
	}
	fmt.Fprintf(w, "State\t%s\n", state)
	ver := "PostgreSQL " + imageVersion(v.Image)
	if v.UpdateAvailable != "" {
		ver += " (update available: " + imageVersion(v.UpdateAvailable) + ", nida db update " + v.Name + ")"
	}
	fmt.Fprintf(w, "Version\t%s\n", ver)
	fmt.Fprintf(w, "Tier\t%s, %s declared\n", v.Tier, memText(v.SizeBytes))
	fmt.Fprintf(w, "Limits\tprocessor %s, memory %s\n", cpuText(v.CPUMillis), memText(v.MemoryBytes))
	if v.Live != nil {
		fmt.Fprintf(w, "Now\t%s used, %d connections, %.1f transactions/s (%s)\n", memText(v.Live.SizeBytes), v.Live.Connections, v.Live.TransactionsPerSecond, stamp(v.Live.Observed))
	}
	home := "off"
	if v.LAN {
		home = v.Address + " (TLS only)"
	}
	fmt.Fprintf(w, "Home network\t%s\n", home)
	for _, l := range v.Links {
		fmt.Fprintf(w, "App\t%s (%s, role %s)\n", l.App, l.Mode, l.Role)
	}
	return w.Flush()
}

func (a *app) dbAdd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("db add", flag.ContinueOnError)
	version := fs.Int("version", 0, "")
	tier := fs.String("tier", "", "")
	size := fs.String("size", "", "")
	memory := fs.String("memory", "", "")
	cpu := fs.Int("cpu", 0, "")
	restore := fs.String("restore", "", "")
	wait := fs.Bool("wait", false, "")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("%w: use db add NAME [options]", errUsage)
	}
	body := map[string]any{"name": rest[0], "version": *version, "tier": *tier, "cpu_millis": *cpu}
	if *size != "" {
		n, err := parseSizeArg(*size)
		if err != nil {
			return err
		}
		body["size_bytes"] = n
	}
	if *memory != "" {
		n, err := parseSizeArg(*memory)
		if err != nil {
			return err
		}
		body["memory_bytes"] = n
	}
	if *restore != "" {
		db, backup, ok := strings.Cut(*restore, "/")
		if !ok {
			return fmt.Errorf("%w: use --restore DATABASE/BACKUP, for example shop-db/20261111T030000Z-daily-pg18.dump", errUsage)
		}
		body["restore_from"] = map[string]string{"database": db, "backup": backup}
	}
	var v dbView
	if err := a.c.Do(ctx, http.MethodPost, "/api/v1/databases", body, &v); err != nil {
		return err
	}
	a.say("NidaOS makes the database %s (PostgreSQL %s, %s tier).\n", v.Name, imageVersion(v.Image), v.Tier)
	if !*wait {
		if a.json {
			return a.writeJSON(v)
		}
		return nil
	}
	if v, err = a.dbWait(ctx, v.Name); err != nil {
		return err
	}
	if v.State != databases.StateReady {
		return fmt.Errorf("the database did not start: %s", v.Reason)
	}
	return a.dbDone(v, fmt.Sprintf("The database %s is ready. Connect an app with: nida db link %s APP", v.Name, v.Name))
}

// dbWait waits until the database is not busy.
func (a *app) dbWait(ctx context.Context, name string) (dbView, error) {
	for {
		var v dbView
		if err := a.c.Get(ctx, dbPath(name), nil, &v); err != nil {
			return dbView{}, err
		}
		if !v.Busy() {
			return v, nil
		}
		if err := a.sleep(ctx, 2*time.Second); err != nil {
			return dbView{}, err
		}
	}
}

func (a *app) listBackups(ctx context.Context, name string) ([]databases.Backup, error) {
	var body struct {
		Backups []databases.Backup `json:"backups"`
	}
	err := a.c.Get(ctx, dbPath(name, "backups"), nil, &body)
	return body.Backups, err
}

func (a *app) dbBackups(ctx context.Context, name string) error {
	list, err := a.listBackups(ctx, name)
	if err != nil {
		return err
	}
	if a.json {
		return a.writeJSON(map[string]any{"backups": list})
	}
	if len(list) == 0 {
		fmt.Fprintf(a.out, "There are no backups of %s. Make one with: nida db backup %s\n", name, name)
		return nil
	}
	w := a.table()
	fmt.Fprintln(w, "BACKUP\tKIND\tVERSION\tCREATED\tSIZE")
	for _, b := range list {
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", b.Name, b.Kind, b.Version, stamp(b.Created), memText(b.SizeBytes))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "\nRestore one into a new database: nida db add NEW-NAME --restore %s/BACKUP --wait\n", name)
	return nil
}

var errBackupFailed = errors.New("the backup failed")

func (a *app) dbBackup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("db backup", flag.ContinueOnError)
	wait := fs.Bool("wait", false, "")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("%w: use db backup NAME [--wait]", errUsage)
	}
	name := rest[0]
	before, err := a.listBackups(ctx, name)
	if err != nil {
		return err
	}
	if err := a.c.Do(ctx, http.MethodPost, dbPath(name, "backups"), nil, nil); err != nil {
		return err
	}
	a.say("The backup of %s started.\n", name)
	if !*wait {
		return nil
	}
	var newest time.Time
	if len(before) > 0 {
		newest = before[0].Created
	}
	for range 900 {
		list, err := a.listBackups(ctx, name)
		if err != nil {
			return err
		}
		if len(list) > 0 && list[0].Created.After(newest) && list[0].Kind == databases.KindManual {
			if a.json {
				return a.writeJSON(list[0])
			}
			fmt.Fprintf(a.out, "The backup %s is ready (%s).\n", list[0].Name, memText(list[0].SizeBytes))
			return nil
		}
		var v dbView
		if err := a.c.Get(ctx, dbPath(name), nil, &v); err == nil && strings.Contains(v.Reason, "backup failed") {
			return fmt.Errorf("%w: %s", errBackupFailed, v.Reason)
		}
		if err := a.sleep(ctx, 2*time.Second); err != nil {
			return err
		}
	}
	return errors.New("the backup did not end in 30 minutes")
}
