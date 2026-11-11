package databases

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

// AppLinker is the part of the app hosting that the links use. main gives
// an adapter of the app store and the app controller.
type AppLinker interface {
	// AppExists tells if the app exists.
	AppExists(app string) bool
	// UsedNames gives the names of the variables and the secrets of the
	// app, with their source.
	UsedNames(app string) map[string]string
	// SetLinked gives the connection secrets of a source to the app.
	SetLinked(app, source string, values map[string]string) error
	// RemoveLinked removes the secrets of a source from the app.
	RemoveLinked(app, source string) error
	// Namespace gives the namespace of the app.
	Namespace(app string) string
	// Restart starts the copies of the app again, thus they get the new
	// secrets. An app without a version gives no error.
	Restart(ctx context.Context, app string) error
}

// Errors of the links.
var (
	ErrNotReady    = errors.New("the database is not ready")
	ErrAppNotFound = errors.New("the app does not exist")
	ErrLinked      = errors.New("the app is connected to this database already")
	ErrNotLinked   = errors.New("the app is not connected to this database")
	// ErrNameInUse: the app has a variable or a secret with a name of the
	// connection, or an other database gives these names.
	ErrNameInUse = errors.New("the app uses a name of the connection already")
	// ErrNoApps: the service has no app hosting.
	ErrNoApps = errors.New("the service has no app hosting")
)

// ConnectionNames are the secret variables that a connected app gets.
var ConnectionNames = []string{"DATABASE_URL", "PGHOST", "PGPORT", "PGDATABASE", "PGUSER", "PGPASSWORD", "PGSSLMODE"}

// linkSource is the source name of the secrets of a database in an app.
func linkSource(db string) string { return "database " + db }

func linkSecretKey(app string) string { return secretLinkPrefix + app }

// Connection gives the secret variables of a link. The apps use TLS
// without a check of the name (decision D24).
func Connection(db, app, password string) map[string]string {
	user, name, host := LinkRole(db, app), DatabaseName(db), ServiceHost(db)
	port := strconv.Itoa(PostgresPort)
	u := url.URL{Scheme: "postgresql", User: url.UserPassword(user, password), Host: host + ":" + port, Path: "/" + name, RawQuery: "sslmode=require"}
	return map[string]string{
		"DATABASE_URL": u.String(),
		"PGHOST":       host,
		"PGPORT":       port,
		"PGDATABASE":   name,
		"PGUSER":       user,
		"PGPASSWORD":   password,
		"PGSSLMODE":    "require",
	}
}

// SetApps gives the controller the app hosting for the links.
func (c *Controller) SetApps(a AppLinker) { c.apps = a }

// Link connects an app to a database. The app gets its own login role and
// the connection secrets, and its copies start again.
func (c *Controller) Link(ctx context.Context, db, app, mode string) (Database, error) {
	if c.apps == nil {
		return Database{}, ErrNoApps
	}
	if mode == "" {
		mode = ModeRW
	}
	if mode != ModeRW && mode != ModeRO {
		return Database{}, fieldErr("mode", "use rw or ro")
	}
	d, ok := c.store.Get(db)
	switch {
	case !ok:
		return Database{}, ErrNotFound
	case d.State != StateReady || c.Busy(db):
		return Database{}, ErrNotReady
	case !c.apps.AppExists(app):
		return Database{}, ErrAppNotFound
	}
	if _, linked := d.Link(app); linked {
		return Database{}, ErrLinked
	}
	used := c.apps.UsedNames(app)
	for _, n := range ConnectionNames {
		if src, ok := used[n]; ok {
			return Database{}, fmt.Errorf("%w: %s (%s)", ErrNameInUse, n, src)
		}
	}
	password, err := NewPassword()
	if err != nil {
		return Database{}, err
	}
	// The password goes to the store first: a link that stops in the middle
	// can start again with the same password.
	if err := c.store.SetSecret(db, linkSecretKey(app), password); err != nil {
		return Database{}, err
	}
	if err := CreateLinkRole(ctx, c.sql, db, app, mode, password); err != nil {
		return Database{}, fmt.Errorf("the role of the link: %w", err)
	}
	if err := c.apps.SetLinked(app, linkSource(db), Connection(db, app, password)); err != nil {
		_ = DropLinkRole(ctx, c.sql, db, app, 0)
		return Database{}, err
	}
	d, err = c.store.Change(db, func(x *Database) error {
		x.Links = append(x.Links, Link{App: app, Mode: mode, Role: LinkRole(db, app), Created: time.Now().UTC()})
		return nil
	})
	if err != nil {
		return Database{}, err
	}
	if err := c.labelApp(ctx, db, app, true); err != nil {
		c.opts.Logger.Warn("the controller cannot mark the app namespace: the next sync tries again", "app", app, "err", err)
	}
	if err := c.apps.Restart(ctx, app); err != nil {
		c.opts.Logger.Warn("the app did not start again after the link: it gets the connection at the next deploy", "app", app, "err", err)
	}
	c.opts.Logger.Info("an app is connected to a database", "database", db, "app", app, "mode", mode)
	return d, nil
}

// Unlink disconnects an app. The order is important: first no new login
// and no open session, then the secrets go away from the app.
func (c *Controller) Unlink(ctx context.Context, db, app string) (Database, error) {
	d, ok := c.store.Get(db)
	if !ok {
		return Database{}, ErrNotFound
	}
	if _, linked := d.Link(app); !linked {
		return Database{}, ErrNotLinked
	}
	if d.State == StateReady {
		if err := DropLinkRole(ctx, c.sql, db, app, 30*time.Second); err != nil {
			return Database{}, fmt.Errorf("the role of the link: %w", err)
		}
	}
	return c.forgetLink(ctx, db, app)
}

// forgetLink removes the secrets, the label and the record of a link.
func (c *Controller) forgetLink(ctx context.Context, db, app string) (Database, error) {
	if c.apps != nil {
		if err := c.apps.RemoveLinked(app, linkSource(db)); err != nil {
			return Database{}, err
		}
		if err := c.labelApp(ctx, db, app, false); err != nil {
			c.opts.Logger.Warn("the controller cannot remove the mark of the app namespace", "app", app, "err", err)
		}
	}
	d, err := c.store.Change(db, func(x *Database) error {
		kept := x.Links[:0]
		for _, l := range x.Links {
			if l.App != app {
				kept = append(kept, l)
			}
		}
		x.Links = kept
		return nil
	})
	if err != nil {
		return Database{}, err
	}
	_ = c.store.DeleteSecret(db, linkSecretKey(app))
	if c.apps != nil && c.apps.AppExists(app) {
		if err := c.apps.Restart(ctx, app); err != nil {
			c.opts.Logger.Warn("the app did not start again after the disconnect", "app", app, "err", err)
		}
	}
	c.opts.Logger.Info("an app is disconnected from a database", "database", db, "app", app)
	return d, nil
}

// labelApp adds or removes the link label on the namespace of an app. An
// app without a namespace (no deploy yet) gets the label at a sync.
func (c *Controller) labelApp(ctx context.Context, db, app string, on bool) error {
	nss := c.kube.CoreV1().Namespaces()
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		ns, err := nss.Get(ctx, c.apps.Namespace(app), metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		key := LinkLabel(db)
		if on == (ns.Labels[key] == "true") {
			return nil
		}
		if on {
			if ns.Labels == nil {
				ns.Labels = map[string]string{}
			}
			ns.Labels[key] = "true"
		} else {
			delete(ns.Labels, key)
		}
		_, err = nss.Update(ctx, ns, metav1.UpdateOptions{})
		return err
	})
}

// syncLinks adds the labels of the links, and removes the links of the
// removed apps.
func (c *Controller) syncLinks(ctx context.Context, d Database) {
	if c.apps == nil {
		return
	}
	for _, l := range d.Links {
		if !c.apps.AppExists(l.App) {
			if err := DropLinkRole(ctx, c.sql, d.Name, l.App, 30*time.Second); err == nil {
				_, _ = c.forgetLink(ctx, d.Name, l.App)
			}
			continue
		}
		if err := c.labelApp(ctx, d.Name, l.App, true); err != nil {
			c.opts.Logger.Warn("the controller cannot mark the app namespace", "app", l.App, "err", err)
		}
	}
}

// RemoveDatabase disconnects all the apps, deletes the namespace with the
// data, and removes the database from the store.
func (c *Controller) RemoveDatabase(ctx context.Context, db string) error {
	d, ok := c.store.Get(db)
	if !ok {
		return ErrNotFound
	}
	if c.Busy(db) {
		return ErrBusy
	}
	for _, l := range d.Links {
		// The namespace goes away with the server: the roles go too.
		if _, err := c.forgetLink(ctx, db, l.App); err != nil {
			return err
		}
	}
	if err := c.Remove(ctx, db); err != nil {
		return err
	}
	return c.store.Remove(db)
}
