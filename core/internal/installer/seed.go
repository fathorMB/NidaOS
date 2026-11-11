package installer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Seed is the content of the seed volume NIDASEED: the SSH key of the
// operator, and optionally the answers of an automatic installation.
type Seed struct {
	Found bool `json:"found"`
	// Key is the first public key of authorized_keys.
	Key string `json:"-"`
	// KeyComment is the end of the key line, for example operator@pc.
	KeyComment string `json:"key_comment,omitempty"`
	HasKey     bool   `json:"has_key"`
	// Auto is the automatic installation of install.json.
	Auto *Auto `json:"-"`
}

// Auto is install.json on the seed volume (task T-07). It is for the
// automatic tests: the installer installs without questions.
type Auto struct {
	Answers
	// Plan "proposed" uses the plan of the installer. Else Roles is the plan.
	Plan string `json:"plan"`
	// After is "poweroff" (default), "reboot" or "stay".
	After string `json:"after"`
	// PlanOnly: the installer makes the plan and does not install. The VM
	// test reads the plan through the API and stops the machine.
	PlanOnly bool `json:"plan_only"`
	// Erase must be "yes": an automatic installation erases disks.
	Erase string `json:"erase"`
}

// ReadSeedDir reads a mounted seed volume.
func ReadSeedDir(dir string) (Seed, error) {
	s := Seed{Found: true}
	if b, err := os.ReadFile(filepath.Join(dir, "authorized_keys")); err == nil {
		s.Key = firstKey(b)
		s.HasKey = s.Key != ""
		if f := strings.Fields(s.Key); len(f) > 2 {
			s.KeyComment = strings.Join(f[2:], " ")
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, "install.json"))
	if err == nil {
		var a Auto
		dec := json.NewDecoder(bytes.NewReader(b))
		if err := dec.Decode(&a); err != nil {
			return s, fmt.Errorf("install.json on the seed volume is not correct: %w", err)
		}
		if !a.PlanOnly && a.Erase != "yes" {
			return s, fmt.Errorf(`install.json on the seed volume must have "erase": "yes"`)
		}
		if a.After == "" {
			a.After = "poweroff"
		}
		s.Auto = &a
	}
	return s, nil
}

func firstKey(b []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if keyRE.MatchString(l) {
			return l
		}
	}
	return ""
}

// FindSeed mounts the volume NIDASEED read-only, reads it and unmounts it.
func FindSeed(ctx context.Context, r Runner) (Seed, error) {
	out, err := r.Run(ctx, "blkid", "-L", SeedLabel)
	dev := strings.TrimSpace(string(out))
	if err != nil || dev == "" {
		return Seed{}, nil
	}
	dir, err := os.MkdirTemp("", "nida-seed-")
	if err != nil {
		return Seed{}, err
	}
	defer os.Remove(dir)
	if _, err := r.Run(ctx, "mount", "-o", "ro", dev, dir); err != nil {
		return Seed{}, fmt.Errorf("mount the seed volume: %w", err)
	}
	defer r.Run(context.Background(), "umount", dir) //nolint:errcheck
	return ReadSeedDir(dir)
}
