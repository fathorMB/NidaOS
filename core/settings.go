package main

// The backup of the settings of NidaOS (milestone M-11, task T-06).
//
//	nida-core settings-backup FILE   write a backup of /var/lib/nidaos
//	nida-core settings-restore FILE  restore a backup (stop nida-core first)

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/config"
	"github.com/fathorMB/NidaOS/core/internal/settings"
	"github.com/fathorMB/NidaOS/core/internal/version"
)

func runSettings(cmd string, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintf(stderr, "use: nida-core %s FILE\n", cmd)
		return 2
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(stderr, "nida-core:", err)
		return 1
	}
	dir := filepath.Dir(cfg.AuthFile)
	switch cmd {
	case "settings-backup":
		f, err := os.OpenFile(args[0], os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintln(stderr, "nida-core:", err)
			return 1
		}
		names, err := settings.Backup(f, dir, version.Version, time.Now())
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			fmt.Fprintln(stderr, "nida-core: the backup failed:", err)
			return 1
		}
		fmt.Fprintf(stdout, "The backup has %d files of %s: %s\n", len(names), dir, args[0])
	case "settings-restore":
		f, err := os.Open(args[0])
		if err != nil {
			fmt.Fprintln(stderr, "nida-core:", err)
			return 1
		}
		defer f.Close()
		names, err := settings.Restore(f, dir)
		if err != nil {
			fmt.Fprintln(stderr, "nida-core: the restore failed:", err)
			return 1
		}
		fmt.Fprintf(stdout, "%d files restored into %s. Start nida-core again.\n", len(names), dir)
	}
	return 0
}

// RestoreFile is the backup of the settings that the installer copies
// from the seed volume. nida-core firstboot restores it (M-11).
const RestoreFile = "/var/lib/nidaos/settings-restore.tar.gz"

// keepSettings writes a backup of the settings each day on the standard
// tier and keeps the last 7.
func keepSettings(ctx context.Context, cfg config.Config, logf func(string, ...any)) {
	if cfg.BackupDir == "" {
		return
	}
	folder := filepath.Join(cfg.BackupDir, "nidaos-settings")
	dir := filepath.Dir(cfg.AuthFile)
	t := time.NewTimer(30 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if name, err := settings.Keep(folder, dir, version.Version, time.Now(), 7); err != nil {
			logf("the daily backup of the settings failed", "err", err)
		} else {
			logf("the daily backup of the settings is complete", "file", name)
		}
		t.Reset(24 * time.Hour)
	}
}
