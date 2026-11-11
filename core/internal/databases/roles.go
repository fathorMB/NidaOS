package databases

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The roles of a database. All the SQL goes through SQLRunner as the
// superuser of the controller, on the local socket. The passwords are
// hexadecimal text that this file makes, thus the SQL can quote them.

// ErrSessionsRemain: the disconnect is not complete, because sessions of
// the link are still open.
var ErrSessionsRemain = errors.New("sessions of the link are still open")

// QuoteIdent quotes an SQL identifier.
func QuoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// QuoteLiteral quotes an SQL text.
func QuoteLiteral(s string) string { return `'` + strings.ReplaceAll(s, `'`, `''`) + `'` }

// OwnerRole gives the role that owns the database. It is not a superuser.
func OwnerRole(db string) string { return dbIdent(db) + "_owner" }

// ReadOnlyRole gives the role that can only read.
func ReadOnlyRole(db string) string { return dbIdent(db) + "_ro" }

// LinkRole gives the login role of the link of an app.
func LinkRole(db, app string) string { return "l_" + dbIdent(app) }

// dbIdent gives the PostgreSQL form of a name: '-' becomes '_'.
func dbIdent(name string) string { return strings.ReplaceAll(name, "-", "_") }

// DatabaseName gives the name of the database in the server.
func DatabaseName(db string) string { return dbIdent(db) }

// NewPassword makes 32 hexadecimal characters (128 bits).
func NewPassword() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// SetupRoles makes the roles and the database. It is idempotent: the
// creation and the restore use it.
//   - <name>_owner: login, not a superuser, the owner of the database;
//   - <name>_ro: no login, a member of pg_read_all_data.
func SetupRoles(ctx context.Context, r SQLRunner, db, ownerPassword string) error {
	owner, ro := OwnerRole(db), ReadOnlyRole(db)
	err := r.Exec(ctx, db, "postgres", fmt.Sprintf(`
DO $$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = %[1]s) THEN
    CREATE ROLE %[2]s LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD %[3]s;
  ELSE
    ALTER ROLE %[2]s PASSWORD %[3]s;
  END IF;
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = %[4]s) THEN
    CREATE ROLE %[5]s NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
  END IF;
END $$;
GRANT pg_read_all_data TO %[5]s;`, QuoteLiteral(owner), QuoteIdent(owner), QuoteLiteral(ownerPassword), QuoteLiteral(ro), QuoteIdent(ro)))
	if err != nil {
		return fmt.Errorf("the roles of the database: %w", err)
	}
	name := DatabaseName(db)
	rows, err := r.Query(ctx, db, "postgres", fmt.Sprintf(`SELECT 1 FROM pg_database WHERE datname = %s`, QuoteLiteral(name)))
	if err != nil {
		return fmt.Errorf("the list of the databases: %w", err)
	}
	if len(rows) == 0 {
		if err := r.Exec(ctx, db, "postgres", fmt.Sprintf(`CREATE DATABASE %s OWNER %s`, QuoteIdent(name), QuoteIdent(owner))); err != nil {
			return fmt.Errorf("the creation of the database: %w", err)
		}
	}
	if err := r.Exec(ctx, db, "postgres", fmt.Sprintf(`REVOKE ALL ON DATABASE %[1]s FROM PUBLIC; GRANT CONNECT ON DATABASE %[1]s TO %[2]s;`, QuoteIdent(name), QuoteIdent(ro))); err != nil {
		return fmt.Errorf("the connect privileges: %w", err)
	}
	return ReapplyReadPrivileges(ctx, r, db)
}

// ReapplyReadPrivileges gives the read privileges to <name>_ro again. It
// is idempotent, also after a restore.
func ReapplyReadPrivileges(ctx context.Context, r SQLRunner, db string) error {
	owner, ro := QuoteIdent(OwnerRole(db)), QuoteIdent(ReadOnlyRole(db))
	return r.Exec(ctx, db, DatabaseName(db), fmt.Sprintf(`
ALTER SCHEMA public OWNER TO %[1]s;
GRANT USAGE ON SCHEMA public TO %[2]s;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO %[2]s;
GRANT SELECT ON ALL SEQUENCES IN SCHEMA public TO %[2]s;
ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA public GRANT SELECT ON TABLES TO %[2]s;
ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA public GRANT SELECT ON SEQUENCES TO %[2]s;`, owner, ro))
}

// CreateLinkRole makes the login role of a link. It is not a superuser and
// cannot make roles or databases.
//   - rw: a member of <name>_owner, with "SET role" to the owner. Thus each
//     object that the app makes belongs to the owner, and the role of the
//     link owns nothing.
//   - ro: a member of <name>_ro.
//
// When the role exists (a link that did not complete), it gets the new
// password.
func CreateLinkRole(ctx context.Context, r SQLRunner, db, app, mode, password string) error {
	role := LinkRole(db, app)
	var grant, set string
	switch mode {
	case ModeRW:
		grant = fmt.Sprintf(`GRANT %s TO %s;`, QuoteIdent(OwnerRole(db)), QuoteIdent(role))
		set = fmt.Sprintf(`ALTER ROLE %s SET role = %s;`, QuoteIdent(role), QuoteLiteral(OwnerRole(db)))
	case ModeRO:
		grant = fmt.Sprintf(`GRANT %s TO %s;`, QuoteIdent(ReadOnlyRole(db)), QuoteIdent(role))
	default:
		return fmt.Errorf("unknown mode of the link: %q", mode)
	}
	return r.Exec(ctx, db, "postgres", fmt.Sprintf(`
DO $$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = %[1]s) THEN
    CREATE ROLE %[2]s LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD %[3]s;
  ELSE
    ALTER ROLE %[2]s LOGIN PASSWORD %[3]s;
  END IF;
END $$;
%[4]s %[5]s`, QuoteLiteral(role), QuoteIdent(role), QuoteLiteral(password), grant, set))
}

// TerminateSessions closes the sessions of a role and gives the number of
// the sessions that stay.
func TerminateSessions(ctx context.Context, r SQLRunner, db, role string) (int, error) {
	if err := r.Exec(ctx, db, "postgres", fmt.Sprintf(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = %s AND pid <> pg_backend_pid()`, QuoteLiteral(role))); err != nil {
		return 0, err
	}
	rows, err := r.Query(ctx, db, "postgres", fmt.Sprintf(`SELECT count(*) FROM pg_stat_activity WHERE usename = %s`, QuoteLiteral(role)))
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	var n int
	_, _ = fmt.Sscan(rows[0], &n)
	return n, nil
}

// DropLinkRole removes the role of a link in this order:
//  1. ALTER ROLE ... NOLOGIN: no new login;
//  2. pg_terminate_backend for each session, again until no session stays
//     (NOLOGIN and DROP ROLE do not close the open sessions);
//  3. DROP OWNED BY and DROP ROLE.
//
// When sessions stay after the timeout, it gives ErrSessionsRemain and the
// role stays.
func DropLinkRole(ctx context.Context, r SQLRunner, db, app string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	role := LinkRole(db, app)
	rows, err := r.Query(ctx, db, "postgres", fmt.Sprintf(`SELECT 1 FROM pg_roles WHERE rolname = %s`, QuoteLiteral(role)))
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil // removed already
	}
	if err := r.Exec(ctx, db, "postgres", fmt.Sprintf(`ALTER ROLE %s NOLOGIN`, QuoteIdent(role))); err != nil {
		return fmt.Errorf("NOLOGIN of the role of the link: %w", err)
	}
	deadline := time.Now().Add(timeout)
	for {
		n, err := TerminateSessions(ctx, r, db, role)
		if err != nil {
			return fmt.Errorf("the close of the sessions of the link: %w", err)
		}
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: %d", ErrSessionsRemain, n)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	if err := r.Exec(ctx, db, DatabaseName(db), fmt.Sprintf(`DROP OWNED BY %s`, QuoteIdent(role))); err != nil {
		return fmt.Errorf("DROP OWNED of the role of the link: %w", err)
	}
	return r.Exec(ctx, db, "postgres", fmt.Sprintf(`DROP ROLE %s`, QuoteIdent(role)))
}

// Stats are the live values of a database (T-08).
type Stats struct {
	SizeBytes   int64 `json:"size_bytes"`
	Connections int   `json:"connections"`
	// Transactions is the total of the commits and the rollbacks since the
	// start of the server. The metrics give the rate.
	Transactions int64 `json:"transactions"`
}

// ReadStats reads the size, the connections and the transactions of the
// database of the owner.
func ReadStats(ctx context.Context, r SQLRunner, db string) (Stats, error) {
	name := QuoteLiteral(DatabaseName(db))
	rows, err := r.Query(ctx, db, "postgres", fmt.Sprintf(`SELECT pg_database_size(%[1]s) || ' ' ||
  (SELECT count(*) FROM pg_stat_activity WHERE datname = %[1]s AND usename <> %[2]s) || ' ' ||
  (SELECT xact_commit + xact_rollback FROM pg_stat_database WHERE datname = %[1]s)`, name, QuoteLiteral(AdminRole)))
	if err != nil {
		return Stats{}, err
	}
	if len(rows) == 0 {
		return Stats{}, errors.New("the server gave no statistics")
	}
	var s Stats
	if _, err := fmt.Sscan(rows[0], &s.SizeBytes, &s.Connections, &s.Transactions); err != nil {
		return Stats{}, fmt.Errorf("the statistics %q: %w", rows[0], err)
	}
	return s, nil
}
