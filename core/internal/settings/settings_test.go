package settings

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackupAndRestore(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "auth.json"), []byte(`{"owner":1}`), 0o600)
	os.WriteFile(filepath.Join(src, "apps.json"), []byte(`{"apps":[]}`), 0o600)
	os.MkdirAll(filepath.Join(src, "ca"), 0o700)
	os.WriteFile(filepath.Join(src, "ca", "ca.key"), []byte("KEY"), 0o600)
	os.WriteFile(filepath.Join(src, "install-plan.txt"), []byte("os=sda"), 0o644)
	var buf bytes.Buffer
	names, err := Backup(&buf, src, "45.1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "auth.json,apps.json,ca/ca.key" {
		t.Errorf("names = %v", names)
	}
	dst := t.TempDir()
	got, err := Restore(bytes.NewReader(buf.Bytes()), dst)
	if err != nil || len(got) != 3 {
		t.Fatalf("restore: %v %v", got, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "ca", "ca.key")); string(b) != "KEY" {
		t.Errorf("ca.key = %q", b)
	}
	if _, err := os.Stat(filepath.Join(dst, "install-plan.txt")); err == nil {
		t.Error("the plan of the disks is not a setting")
	}
}

func TestRestoreRefusesOtherFiles(t *testing.T) {
	for _, name := range []string{"../etc/shadow", "/etc/passwd", "other.json", "ca/../../x"} {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		tw.WriteHeader(&tar.Header{Name: Manifest, Size: 1, Mode: 0o600, Typeflag: tar.TypeReg})
		tw.Write([]byte("x"))
		tw.WriteHeader(&tar.Header{Name: name, Size: 1, Mode: 0o600, Typeflag: tar.TypeReg})
		tw.Write([]byte("x"))
		tw.Close()
		gz.Close()
		if _, err := Restore(&buf, t.TempDir()); !errors.Is(err, ErrNotSettings) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := Restore(strings.NewReader("not a tar"), t.TempDir()); !errors.Is(err, ErrNotSettings) {
		t.Errorf("not a tar: %v", err)
	}
}

func TestKeep(t *testing.T) {
	src, folder := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(src, "auth.json"), []byte(`{}`), 0o600)
	start := time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC)
	for i := 0; i < 9; i++ {
		if _, err := Keep(folder, src, "45.1", start.Add(time.Duration(i)*24*time.Hour), 7); err != nil {
			t.Fatal(err)
		}
	}
	files, _ := filepath.Glob(filepath.Join(folder, "*.tar.gz"))
	if len(files) != 7 || !strings.Contains(files[0], "20261103") {
		t.Errorf("files = %v", files)
	}
}
