package databases

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The kinds of a backup.
const (
	KindDaily  = "daily"
	KindManual = "manual"
	// KindUpdate is the backup before a version update (T-07).
	KindUpdate = "update"
)

// Backup is one backup file of a database.
type Backup struct {
	Database string    `json:"database"`
	Name     string    `json:"name"`
	Kind     string    `json:"kind"`
	Created  time.Time `json:"created"`
	// Version is the major version of the server that made the backup.
	Version   int   `json:"version"`
	SizeBytes int64 `json:"size_bytes"`
}

// ErrBackupNotFound: the backup does not exist.
var ErrBackupNotFound = errors.New("the backup does not exist")

const backupTimeLayout = "20060102T150405Z"

// backupName gives the file name of a backup: "<time>-<kind>-pg<version>.dump".
func backupName(t time.Time, kind string, version int) string {
	return fmt.Sprintf("%s-%s-pg%d.dump", t.UTC().Format(backupTimeLayout), kind, version)
}

// parseBackupName reads a file name that backupName made.
func parseBackupName(name string) (time.Time, string, int, bool) {
	base, ok := strings.CutSuffix(name, ".dump")
	if !ok {
		return time.Time{}, "", 0, false
	}
	parts := strings.Split(base, "-")
	if len(parts) != 3 || !strings.HasPrefix(parts[2], "pg") {
		return time.Time{}, "", 0, false
	}
	t, err := time.Parse(backupTimeLayout, parts[0])
	if err != nil {
		return time.Time{}, "", 0, false
	}
	var v int
	if _, err := fmt.Sscanf(parts[2], "pg%d", &v); err != nil {
		return time.Time{}, "", 0, false
	}
	switch parts[1] {
	case KindDaily, KindManual, KindUpdate:
		return t, parts[1], v, true
	}
	return time.Time{}, "", 0, false
}

func (c *Controller) backupDir(db string) string { return filepath.Join(c.opts.BackupDir, db) }

// Backups gives the backups of a database, the newest first. The backups
// of a removed database stay until the owner deletes them.
func (c *Controller) Backups(db string) ([]Backup, error) {
	if c.opts.BackupDir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(c.backupDir(db))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Backup
	for _, e := range entries {
		t, kind, v, ok := parseBackupName(e.Name())
		if !ok || e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Backup{Database: db, Name: e.Name(), Kind: kind, Created: t, Version: v, SizeBytes: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

// BackupPath gives the file of a backup.
func (c *Controller) BackupPath(db, name string) (string, error) {
	if _, _, _, ok := parseBackupName(name); !ok || strings.ContainsAny(name, `/\`) {
		return "", ErrBackupNotFound
	}
	p := filepath.Join(c.backupDir(db), name)
	if _, err := os.Stat(p); err != nil {
		return "", ErrBackupNotFound
	}
	return p, nil
}

// DeleteBackup deletes one backup file.
func (c *Controller) DeleteBackup(db, name string) error {
	p, err := c.BackupPath(db, name)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

// MakeBackup runs pg_dump in the pod and writes the backup file. The file
// has the mode 0600, and a backup that stops in the middle leaves no file.
func (c *Controller) MakeBackup(ctx context.Context, d Database, kind string) (Backup, error) {
	if c.opts.BackupDir == "" {
		return Backup{}, errors.New("the service has no backup folder")
	}
	dir := c.backupDir(d.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Backup{}, err
	}
	now := c.opts.Now()
	name := backupName(now, kind, d.Version)
	tmp, err := os.CreateTemp(dir, ".partial-*")
	if err != nil {
		return Backup{}, err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return Backup{}, err
	}
	cmd := []string{"pg_dump", "-Fc", "-h", "/var/run/postgresql", "-U", AdminRole, "-d", DatabaseName(d.Name)}
	if err := c.pod.Exec(ctx, Namespace(d.Name), PodName, WorkloadName, cmd, nil, tmp); err != nil {
		tmp.Close()
		return Backup{}, fmt.Errorf("pg_dump: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return Backup{}, err
	}
	info, err := tmp.Stat()
	if err != nil {
		tmp.Close()
		return Backup{}, err
	}
	if err := tmp.Close(); err != nil {
		return Backup{}, err
	}
	if info.Size() == 0 {
		return Backup{}, errors.New("pg_dump wrote nothing")
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		return Backup{}, err
	}
	c.prune(d.Name)
	c.opts.Logger.Info("a backup is ready", "database", d.Name, "kind", kind, "file", name, "bytes", info.Size())
	return Backup{Database: d.Name, Name: name, Kind: kind, Created: now.UTC().Truncate(time.Second), Version: d.Version, SizeBytes: info.Size()}, nil
}

// prune keeps the newest backups of each kind: KeepDaily daily backups,
// and KeepManual backups on demand and before an update.
func (c *Controller) prune(db string) {
	list, err := c.Backups(db)
	if err != nil {
		return
	}
	daily, manual := 0, 0
	for _, b := range list {
		keep := true
		switch b.Kind {
		case KindDaily:
			daily++
			keep = daily <= c.opts.KeepDaily
		default:
			manual++
			keep = manual <= c.opts.KeepManual
		}
		if !keep {
			if err := os.Remove(filepath.Join(c.backupDir(db), b.Name)); err == nil {
				c.opts.Logger.Info("an old backup is deleted", "database", db, "file", b.Name)
			}
		}
	}
}

// StartBackup starts a backup on demand in the background. The result is
// a new file in the list of the backups.
func (c *Controller) StartBackup(ctx context.Context, db string) error {
	d, ok := c.store.Get(db)
	if !ok {
		return ErrNotFound
	}
	if d.State != StateReady {
		return ErrNotReady
	}
	if !c.start(ctx, &c.wg, db, func(ctx context.Context) {
		if _, err := c.MakeBackup(ctx, d, KindManual); err != nil {
			c.opts.Logger.Warn("the backup on demand failed", "database", db, "err", err)
			c.setLastError(db, "the last backup failed: "+err.Error())
		}
	}) {
		return ErrBusy
	}
	return nil
}

// setLastError writes the reason of a failed background operation, but
// keeps the state.
func (c *Controller) setLastError(db, reason string) {
	_, _ = c.store.Change(db, func(x *Database) error {
		x.Reason = trim(reason)
		return nil
	})
}

// dailyBackups makes the daily backup of each ready database after
// BackupHour (local time), one time each day.
func (c *Controller) dailyBackups(ctx context.Context) {
	if c.opts.BackupDir == "" {
		return
	}
	now := c.opts.Now().Local()
	if now.Hour() < c.opts.BackupHour {
		return
	}
	today := now.Format("2006-01-02")
	for _, d := range c.store.List() {
		if d.State != StateReady {
			continue
		}
		list, _ := c.Backups(d.Name)
		done := false
		for _, b := range list {
			if b.Kind == KindDaily && b.Created.Local().Format("2006-01-02") == today {
				done = true
				break
			}
		}
		if done {
			continue
		}
		c.start(ctx, &c.wg, d.Name, func(ctx context.Context) {
			if _, err := c.MakeBackup(ctx, d, KindDaily); err != nil {
				c.opts.Logger.Warn("the daily backup failed", "database", d.Name, "err", err)
				c.setLastError(d.Name, "the last daily backup failed: "+err.Error())
			}
		})
	}
}
