package databases

import (
	"context"
	"fmt"
	"os"
)

// StartRestore adds a new database that the controller makes from a
// backup. The backup can come from a removed database. The version of the
// new database must be the same as the version of the backup or newer:
// thus a restore is also the major update (decision D23).
func (c *Controller) StartRestore(d Database, from Restore) (Database, error) {
	if _, err := c.BackupPath(from.Database, from.Backup); err != nil {
		return Database{}, err
	}
	_, _, backupVersion, _ := parseBackupName(from.Backup)
	if d.Version < backupVersion {
		return Database{}, fieldErr("version", "the backup comes from PostgreSQL %d: the new database must have the version %d or newer", backupVersion, backupVersion)
	}
	d.State, d.Restore = StateRestoring, &from
	created, err := c.store.Create(d)
	if err != nil {
		return Database{}, err
	}
	c.Kick()
	return created, nil
}

// restore makes the database and its roles, then runs pg_restore with the
// backup. The objects of the backup get the new owner role, and the read
// privileges apply again. The links of the old database do not come back:
// the owner connects the apps again.
func (c *Controller) restore(ctx context.Context, d Database) error {
	if d.Restore == nil {
		return fmt.Errorf("the database %s has no backup to restore", d.Name)
	}
	path, err := c.BackupPath(d.Restore.Database, d.Restore.Backup)
	if err != nil {
		return err
	}
	if err := c.apply(ctx, d); err != nil {
		return err
	}
	if err := c.setupRoles(ctx, d); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	cmd := []string{"pg_restore", "--no-owner", "--no-acl", "--exit-on-error", "--role=" + OwnerRole(d.Name),
		"-h", "/var/run/postgresql", "-U", AdminRole, "-d", DatabaseName(d.Name)}
	if err := c.pod.Exec(ctx, Namespace(d.Name), PodName, WorkloadName, cmd, f, discard{}); err != nil {
		return fmt.Errorf("pg_restore: %w", err)
	}
	if err := ReapplyReadPrivileges(ctx, c.sql, d.Name); err != nil {
		return err
	}
	_, err = c.store.Change(d.Name, func(x *Database) error {
		x.State, x.Reason, x.Restore = StateReady, "", nil
		return nil
	})
	return err
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
