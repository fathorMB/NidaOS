package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fathorMB/NidaOS/core/internal/auth"
	"github.com/fathorMB/NidaOS/core/internal/installer"
)

func TestFirstboot(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "firstboot.json")
	authFile := filepath.Join(dir, "auth.json")
	home := filepath.Join(dir, "home", "owner")
	fb := installer.Firstboot{
		Hostname: "home", User: "owner", SSHKey: "ssh-ed25519 AAAA operator@pc", Password: "a long owner password",
		Keyboard: "it", Timezone: "Europe/Rome", Groups: []string{"wheel", "nida"},
		Network: installer.Network{Mode: "static", Interface: "enp1s0", Address: "192.168.1.50/24", Gateway: "192.168.1.1", DNS: []string{"1.1.1.1"}},
	}
	b, _ := json.Marshal(fb)
	if err := os.WriteFile(file, b, 0o600); err != nil {
		t.Fatal(err)
	}
	var calls []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "id" || name == "getent" {
			return nil, errors.New("no such user")
		}
		return nil, nil
	}
	homeOf := func(string) (string, int, int, error) { return home, os.Getuid(), os.Getgid(), nil }
	var out, errOut bytes.Buffer
	code := firstboot(file, authFile, run, homeOf, &out, &errOut)
	if code != 0 {
		t.Fatalf("code %d: %s", code, errOut.String())
	}
	all := strings.Join(calls, "\n")
	for _, want := range []string{
		"hostnamectl set-hostname home",
		"useradd -m -U -G wheel,nida -s /bin/bash -c NidaOS owner owner",
		"grdctl --system rdp set-credentials owner a long owner password",
		"localectl set-x11-keymap it",
		"timedatectl set-timezone Europe/Rome",
		"nmcli connection add type ethernet con-name nidaos ifname enp1s0 ipv4.method manual ipv4.addresses 192.168.1.50/24",
		"nmcli connection up nidaos",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no command %q in\n%s", want, all)
		}
	}
	if k, _ := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys")); string(k) != "ssh-ed25519 AAAA operator@pc\n" {
		t.Errorf("key = %q", k)
	}
	store, err := auth.Open(authFile)
	if err != nil || !store.OwnerSet() {
		t.Fatalf("owner password: %v", err)
	}
	if _, _, err := store.SignIn("a long owner password"); err != nil {
		t.Errorf("sign-in with the password of the installer: %v", err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("the file with the password stays")
	}
	if strings.Contains(out.String()+errOut.String(), "a long owner password") {
		t.Error("the output has the password")
	}
	// A second start does nothing.
	if code := firstboot(file, authFile, run, homeOf, &out, &errOut); code != 0 {
		t.Errorf("second start: %d", code)
	}
}
