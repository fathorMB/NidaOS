package databases

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRestoreIntoANewDatabase(t *testing.T) {
	r := newRig(t)
	r.c.opts.BackupDir = t.TempDir()
	r.pod.output = "PGDMP dump of shop-db"
	old := r.create(t, "shop-db")
	old.Version = 17 // the backup says pg17
	b, err := r.c.MakeBackup(context.Background(), old, KindManual)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := r.c.StartRestore(Database{Name: "old", Version: 16, Image: "x", Tier: TierStandard}, Restore{Database: "shop-db", Backup: b.Name}); !IsField(err, "version") {
		t.Errorf("restore into an older version: %v", err)
	}
	if _, err := r.c.StartRestore(Database{Name: "x", Version: 18, Image: "x", Tier: TierStandard}, Restore{Database: "shop-db", Backup: "none.dump"}); !errors.Is(err, ErrBackupNotFound) {
		t.Errorf("restore of an unknown backup: %v", err)
	}
	d, err := r.c.StartRestore(Database{Name: "shop-copy", Version: 18, Image: testImages()[18], Tier: TierStandard}, Restore{Database: "shop-db", Backup: b.Name})
	if err != nil || d.State != StateRestoring {
		t.Fatalf("start: %+v %v", d, err)
	}
	go r.markReady(t, "shop-copy")
	r.c.operate(context.Background(), d)
	got, _ := r.store.Get("shop-copy")
	if got.State != StateReady || got.Restore != nil {
		t.Fatalf("state %s %q", got.State, got.Reason)
	}
	last := len(r.pod.cmds) - 1
	cmd := strings.Join(r.pod.cmds[last], " ")
	if !strings.HasPrefix(cmd, "pg_restore --no-owner --no-acl --exit-on-error --role=shop_copy_owner") || !strings.HasSuffix(cmd, "-d shop_copy") {
		t.Errorf("command %q", cmd)
	}
	if r.pod.stdins[last] != "PGDMP dump of shop-db" {
		t.Errorf("stdin %q", r.pod.stdins[last])
	}
	if !strings.Contains(r.sql.all(), `GRANT SELECT ON ALL TABLES IN SCHEMA public TO "shop_copy_ro"`) {
		t.Error("the read privileges did not apply again")
	}
}

func TestFailedRestore(t *testing.T) {
	r := newRig(t)
	r.c.opts.BackupDir = t.TempDir()
	r.pod.output = "PGDMP"
	old := r.create(t, "shop-db")
	b, _ := r.c.MakeBackup(context.Background(), old, KindManual)
	r.pod.fail = "pg_restore"
	d, _ := r.c.StartRestore(Database{Name: "copy", Version: 18, Image: testImages()[18], Tier: TierStandard}, Restore{Database: "shop-db", Backup: b.Name})
	go r.markReady(t, "copy")
	r.c.operate(context.Background(), d)
	got, _ := r.store.Get("copy")
	if got.State != StateFailed || !strings.Contains(got.Reason, "pg_restore") {
		t.Errorf("state %s %q", got.State, got.Reason)
	}
}

// IsField tells if err is a FieldError for field.
func IsField(err error, field string) bool {
	var fe *FieldError
	return errors.As(err, &fe) && fe.Field == field
}
