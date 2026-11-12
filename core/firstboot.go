package main

// nida-core firstboot applies the choices of the installer at the first
// start of the installed system (milestone M-07, decision D34). The
// installer writes /var/lib/nidaos/firstboot.json (mode 0600). The unit
// nida-firstboot.service runs this command before nida-core, SSH and the
// Remote Desktop service. The command deletes the file at the end: it has
// the owner password.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/auth"
	"github.com/fathorMB/NidaOS/core/internal/config"
	"github.com/fathorMB/NidaOS/core/internal/installer"
)

// FirstbootFile is the file of the installer.
const FirstbootFile = "/var/lib/nidaos/firstboot.json"

// firstbootRunner runs the system commands. The tests give a fake.
type firstbootRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

func execCombined(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func runFirstboot(stdout, stderr io.Writer) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(stderr, "nida-core:", err)
		return 1
	}
	return firstboot(FirstbootFile, cfg.AuthFile, execCombined, lookupHome, stdout, stderr)
}

// lookupHome gives the home folder, the user id and the group id.
func lookupHome(user string) (string, int, int, error) {
	out, err := exec.Command("getent", "passwd", user).Output()
	if err != nil {
		return "", 0, 0, fmt.Errorf("getent passwd %s: %w", user, err)
	}
	f := strings.Split(strings.TrimSpace(string(out)), ":")
	if len(f) < 6 {
		return "", 0, 0, fmt.Errorf("getent passwd %s: %q", user, out)
	}
	uid, _ := strconv.Atoi(f[2])
	gid, _ := strconv.Atoi(f[3])
	return f[5], uid, gid, nil
}

func firstboot(file, authFile string, run firstbootRunner, home func(string) (string, int, int, error), stdout, stderr io.Writer) int {
	b, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(stdout, "nida-core firstboot: no file of the installer, nothing to do")
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, "nida-core firstboot:", err)
		return 1
	}
	// The file has the password: delete it at the end, also after an error.
	defer shred(file)
	var fb installer.Firstboot
	if err := json.Unmarshal(b, &fb); err != nil {
		fmt.Fprintln(stderr, "nida-core firstboot: the file is not correct:", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	failures := 0
	step := func(what string, f func() error) {
		if err := f(); err != nil {
			failures++
			fmt.Fprintf(stderr, "nida-core firstboot: %s: %v\n", what, err)
			return
		}
		fmt.Fprintf(stdout, "nida-core firstboot: %s\n", what)
	}
	cmd := func(name string, args ...string) error {
		out, err := run(ctx, name, args...)
		if err != nil {
			return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return nil
	}

	if fb.Hostname != "" {
		step("the computer name is "+fb.Hostname, func() error { return cmd("hostnamectl", "set-hostname", fb.Hostname) })
	}
	user := fb.User
	if user == "" {
		user = "nida"
	}
	groups := strings.Join(fb.Groups, ",")
	step("the owner "+user+" is in the groups "+groups, func() error {
		if _, err := run(ctx, "id", "-u", user); err == nil {
			return cmd("usermod", "-a", "-G", groups, user)
		}
		// The owner "nida" gets the group "nida" of sysusers.d as its own
		// group. An other name gets a new group with its name.
		primary := []string{"-U"}
		if _, err := run(ctx, "getent", "group", user); err == nil {
			primary = []string{"-g", user}
		}
		args := append([]string{"-m"}, primary...)
		args = append(args, "-G", groups, "-s", "/bin/bash", "-c", "NidaOS owner", user)
		return cmd("useradd", args...)
	})
	if fb.SSHKey != "" {
		step("the SSH key of "+user+" is set", func() error { return writeKey(user, fb.SSHKey, home) })
	}
	if fb.Password != "" {
		step("the owner password of nida-core is set", func() error {
			store, err := auth.Open(authFile)
			if err != nil {
				return err
			}
			return store.SetPassword(fb.Password)
		})
		step("the Remote Desktop password of "+user+" is set", func() error {
			return cmd("grdctl", "--system", "rdp", "set-credentials", user, fb.Password)
		})
	}
	if fb.Keyboard != "" {
		step("the keyboard is "+fb.Keyboard, func() error { return cmd("localectl", "set-x11-keymap", fb.Keyboard) })
	}
	if fb.Timezone != "" {
		step("the time zone is "+fb.Timezone, func() error { return cmd("timedatectl", "set-timezone", fb.Timezone) })
	}
	if fb.Network.Mode == "static" {
		step("the fixed address is "+fb.Network.Address, func() error {
			args := []string{"connection", "add", "type", "ethernet", "con-name", "nidaos", "ifname", fb.Network.Interface,
				"ipv4.method", "manual", "ipv4.addresses", fb.Network.Address, "ipv4.gateway", fb.Network.Gateway, "connection.autoconnect", "yes"}
			if len(fb.Network.DNS) > 0 {
				args = append(args, "ipv4.dns", strings.Join(fb.Network.DNS, " "))
			}
			if fb.Network.Interface == "" {
				args[6] = "*"
			}
			if err := cmd("nmcli", args...); err != nil {
				return err
			}
			return cmd("nmcli", "connection", "up", "nidaos")
		})
	}
	if failures > 0 {
		fmt.Fprintf(stderr, "nida-core firstboot: %d steps failed. Set them by hand.\n", failures)
		return 1
	}
	fmt.Fprintln(stdout, "nida-core firstboot: the choices of the installer are applied")
	return 0
}

func writeKey(user, key string, home func(string) (string, int, int, error)) error {
	dir, uid, gid, err := home(user)
	if err != nil {
		return err
	}
	ssh := filepath.Join(dir, ".ssh")
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		return err
	}
	path := filepath.Join(ssh, "authorized_keys")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(key)+"\n"), 0o600); err != nil {
		return err
	}
	if uid < 0 {
		return nil // a system without user ids (the tests on Windows)
	}
	for _, p := range []string{ssh, path} {
		if err := os.Chown(p, uid, gid); err != nil {
			return err
		}
	}
	return nil
}

// shred writes zeros on the file, then deletes it.
func shred(path string) {
	if st, err := os.Stat(path); err == nil {
		_ = os.WriteFile(path, make([]byte, st.Size()), 0o600)
	}
	_ = os.Remove(path)
}
