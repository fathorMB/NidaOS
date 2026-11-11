package databases

import (
	"context"
	"errors"
	"fmt"
)

// ErrNoUpdate: the database has the newest image of its version already.
var ErrNoUpdate = errors.New("the database has the newest minor version already")

// UpdateAvailable gives the newer image of the version of a database, or
// "".
func (c *Controller) UpdateAvailable(d Database) string {
	if img := c.opts.Images[d.Version]; img != "" && img != d.Image {
		return img
	}
	return ""
}

// StartUpdate asks the controller to update the minor version of a
// database. NidaOS makes a backup first (decision D23).
func (c *Controller) StartUpdate(db string) (Database, error) {
	d, ok := c.store.Get(db)
	if !ok {
		return Database{}, ErrNotFound
	}
	if d.State != StateReady || c.Busy(db) {
		return Database{}, ErrNotReady
	}
	target := c.UpdateAvailable(d)
	if target == "" {
		return Database{}, ErrNoUpdate
	}
	d, err := c.store.Change(db, func(x *Database) error {
		x.State, x.UpdateTo, x.Reason = StateUpdating, target, ""
		return nil
	})
	if err == nil {
		c.Kick()
	}
	return d, err
}

// update makes the backup, then changes the image and waits for the
// server. When the server does not start, the old image comes back and the
// database stays ready with the reason.
func (c *Controller) update(ctx context.Context, d Database) error {
	if d.UpdateTo == "" {
		return errors.New("the update has no target image")
	}
	if _, err := c.MakeBackup(ctx, d, KindUpdate); err != nil {
		return fmt.Errorf("the backup before the update failed, thus the update did not start: %w", err)
	}
	next := d
	next.Image = next.UpdateTo
	if err := c.apply(ctx, next); err != nil {
		return err
	}
	if err := c.waitReady(ctx, d.Name); err != nil {
		c.opts.Logger.Warn("the server did not start with the new image: the old image comes back", "database", d.Name, "err", err)
		old := d
		if rerr := c.apply(ctx, old); rerr != nil {
			return fmt.Errorf("%v; the old image did not come back: %w", err, rerr)
		}
		if rerr := c.waitReady(ctx, d.Name); rerr != nil {
			return fmt.Errorf("%v; the server did not start with the old image: %w", err, rerr)
		}
		return fmt.Errorf("the update failed and the old image is back: %w", err)
	}
	_, err := c.store.Change(d.Name, func(x *Database) error {
		x.Image, x.UpdateTo, x.State, x.Reason = next.Image, "", StateReady, ""
		return nil
	})
	return err
}
