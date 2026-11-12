// Package settings makes a backup of the settings of NidaOS and restores
// it (milestone M-11, task T-06). The settings are the files of nida-core
// in /var/lib/nidaos: the owner password and the tokens, the apps, the
// databases, the GitHub App, the agents and the certificate authority. The
// data of the apps and the databases are not in it: the database backups
// keep the data.
package settings

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Files are the files and the folders of the settings, below the state
// folder (/var/lib/nidaos).
var Files = []string{"auth.json", "apps.json", "databases.json", "runners.json", "agents.json", "ca"}

// Manifest is the first file of a backup.
const Manifest = "nidaos-settings.txt"

// MaxSize is the maximum size of a backup.
const MaxSize = 64 << 20

// Backup writes a gzip tar of the settings of dir to w. A missing file is
// not an error: a new NidaOS has no runners file.
func Backup(w io.Writer, dir, version string, now time.Time) ([]string, error) {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	var names []string
	header := func(name string, size int64, mode int64) *tar.Header {
		return &tar.Header{Name: name, Size: size, Mode: mode, ModTime: now, Typeflag: tar.TypeReg, Format: tar.FormatPAX}
	}
	man := []byte(fmt.Sprintf("NidaOS settings\nversion=%s\ntime=%s\n", version, now.UTC().Format(time.RFC3339)))
	if err := tw.WriteHeader(header(Manifest, int64(len(man)), 0o600)); err != nil {
		return nil, err
	}
	if _, err := tw.Write(man); err != nil {
		return nil, err
	}
	for _, f := range Files {
		root := filepath.Join(dir, f)
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if d.IsDir() || !d.Type().IsRegular() {
				return nil
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			name := filepath.ToSlash(rel)
			if err := tw.WriteHeader(header(name, int64(len(b)), 0o600)); err != nil {
				return err
			}
			if _, err := tw.Write(b); err != nil {
				return err
			}
			names = append(names, name)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return names, gz.Close()
}

// allowed tells if a name of a backup is a file of the settings.
func allowed(name string) bool {
	clean := path.Clean(name)
	if clean != name || strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "..") || clean == "." {
		return false
	}
	if clean == Manifest {
		return true
	}
	for _, f := range Files {
		if clean == f || strings.HasPrefix(clean, f+"/") {
			return true
		}
	}
	return false
}

// ErrNotSettings: the file is not a backup of the settings of NidaOS.
var ErrNotSettings = errors.New("the file is not a backup of the NidaOS settings")

// Restore writes the files of a backup into dir (mode 0600, folders
// 0700). It refuses a file with a name out of the settings.
func Restore(r io.Reader, dir string) ([]string, error) {
	gz, err := gzip.NewReader(io.LimitReader(r, MaxSize))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotSettings, err)
	}
	tr := tar.NewReader(gz)
	var names []string
	sawManifest := false
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return names, fmt.Errorf("%w: %v", ErrNotSettings, err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		if !allowed(h.Name) {
			return names, fmt.Errorf("%w: the name %q is not a file of the settings", ErrNotSettings, h.Name)
		}
		if h.Name == Manifest {
			sawManifest = true
			continue
		}
		if !sawManifest {
			return names, fmt.Errorf("%w: no %s at the start", ErrNotSettings, Manifest)
		}
		dst := filepath.Join(dir, filepath.FromSlash(h.Name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return names, err
		}
		tmp := dst + ".restore"
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return names, err
		}
		_, err = io.Copy(f, io.LimitReader(tr, MaxSize))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return names, err
		}
		if err := os.Rename(tmp, dst); err != nil {
			return names, err
		}
		names = append(names, h.Name)
	}
	if !sawManifest {
		return names, fmt.Errorf("%w: no %s", ErrNotSettings, Manifest)
	}
	return names, nil
}

// Keep writes a backup into folder each day and keeps the last n. It is
// the automatic copy on the standard tier.
func Keep(folder, dir, version string, now time.Time, n int) (string, error) {
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return "", err
	}
	name := filepath.Join(folder, "nidaos-settings-"+now.UTC().Format("20060102T150405Z")+".tar.gz")
	f, err := os.OpenFile(name+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	_, err = Backup(f, dir, version, now)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(name + ".tmp")
		return "", err
	}
	if err := os.Rename(name+".tmp", name); err != nil {
		return "", err
	}
	old, _ := filepath.Glob(filepath.Join(folder, "nidaos-settings-*.tar.gz"))
	sort.Strings(old)
	for len(old) > n {
		_ = os.Remove(old[0])
		old = old[1:]
	}
	return name, nil
}
