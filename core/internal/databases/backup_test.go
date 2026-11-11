package databases

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBackupFiles(t *testing.T) {
	r := newRig(t)
	r.c.opts.BackupDir = t.TempDir()
	r.c.opts.KeepManual = 2
	d := r.create(t, "shop-db")
	r.pod.output = "PGDMP fake dump"
	ctx := context.Background()
	clock := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	r.c.opts.Now = func() time.Time { clock = clock.Add(time.Minute); return clock }

	for range 3 {
		if _, err := r.c.MakeBackup(ctx, d, KindManual); err != nil {
			t.Fatal(err)
		}
	}
	b, err := r.c.MakeBackup(ctx, d, KindDaily)
	if err != nil {
		t.Fatal(err)
	}
	if b.Kind != KindDaily || b.Version != 18 || b.SizeBytes != int64(len("PGDMP fake dump")) {
		t.Errorf("backup %+v", b)
	}
	list, _ := r.c.Backups("shop-db")
	kinds := ""
	for _, x := range list {
		kinds += x.Kind[:1]
	}
	// The newest first; only 2 manual backups stay.
	if kinds != "dmm" {
		t.Errorf("kinds %q of %+v", kinds, list)
	}
	cmd := strings.Join(r.pod.cmds[len(r.pod.cmds)-1], " ")
	if cmd != "pg_dump -Fc -h /var/run/postgresql -U nida_admin -d shop_db" {
		t.Errorf("command %q", cmd)
	}
	p, err := r.c.BackupPath("shop-db", b.Name)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
			t.Errorf("mode %v", fi.Mode().Perm())
		}
	}
	if _, err := r.c.BackupPath("shop-db", "../../etc/passwd"); err == nil {
		t.Error("BackupPath accepted a path out of the folder")
	}
	if err := r.c.DeleteBackup("shop-db", b.Name); err != nil {
		t.Fatal(err)
	}
	if list, _ = r.c.Backups("shop-db"); len(list) != 2 {
		t.Errorf("after delete: %+v", list)
	}
}

func TestFailedBackupLeavesNoFile(t *testing.T) {
	r := newRig(t)
	r.c.opts.BackupDir = t.TempDir()
	d := r.create(t, "shop-db")
	r.pod.fail = "pg_dump"
	if _, err := r.c.MakeBackup(context.Background(), d, KindManual); err == nil {
		t.Fatal("no error")
	}
	entries, _ := os.ReadDir(filepath.Join(r.c.opts.BackupDir, "shop-db"))
	if len(entries) != 0 {
		t.Errorf("files after a failed backup: %v", entries)
	}
}

func TestDailyBackupOnceADay(t *testing.T) {
	r := newRig(t)
	r.c.opts.BackupDir = t.TempDir()
	r.pod.output = "PGDMP"
	r.create(t, "shop-db")
	day := time.Date(2026, 11, 12, 4, 0, 0, 0, time.Local)
	r.c.opts.Now = func() time.Time { return day }
	ctx := context.Background()
	r.c.dailyBackups(ctx)
	r.c.wg.Wait()
	r.c.dailyBackups(ctx)
	r.c.wg.Wait()
	list, _ := r.c.Backups("shop-db")
	if len(list) != 1 || list[0].Kind != KindDaily {
		t.Errorf("backups %+v", list)
	}
	// Before the backup hour: nothing.
	r.c.opts.Now = func() time.Time { return day.Add(22 * time.Hour) } // 02:00 of the next day
	r.c.dailyBackups(ctx)
	r.c.wg.Wait()
	if list, _ = r.c.Backups("shop-db"); len(list) != 1 {
		t.Errorf("a backup before 03:00: %+v", list)
	}
}

func TestBackupNames(t *testing.T) {
	n := backupName(time.Date(2026, 11, 11, 3, 0, 5, 0, time.UTC), KindUpdate, 17)
	tm, kind, v, ok := parseBackupName(n)
	if !ok || kind != KindUpdate || v != 17 || tm.Hour() != 3 {
		t.Errorf("%s: %v %s %d %v", n, tm, kind, v, ok)
	}
	for _, bad := range []string{"x.dump", "20261111T030005Z-other-pg17.dump", ".partial-1", "a-b-c"} {
		if _, _, _, ok := parseBackupName(bad); ok {
			t.Errorf("%q is a backup name", bad)
		}
	}
}
