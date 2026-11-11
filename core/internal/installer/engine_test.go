package installer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// fakeRun records the commands. bootc makes a deployment.
type fakeRun struct {
	mu     sync.Mutex
	calls  []string
	target string
	fail   string
}

func (f *fakeRun) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := name + " " + strings.Join(args, " ")
	f.mu.Lock()
	f.calls = append(f.calls, line)
	f.mu.Unlock()
	if f.fail != "" && strings.HasPrefix(line, f.fail) {
		return []byte("simulated output"), errors.New("exit status 1")
	}
	switch name {
	case "bootc":
		dep := filepath.Join(f.target, "ostree", "deploy", "default")
		for _, d := range []string{filepath.Join(dep, "deploy", "abc123.0", "etc"), filepath.Join(dep, "var"), filepath.Join(f.target, "boot", "efi", "EFI")} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return nil, err
			}
		}
		_ = os.WriteFile(filepath.Join(dep, "deploy", "abc123.0", "etc", "fstab"), []byte("UUID=r / xfs defaults 0 0\n"), 0o644)
	case "blkid":
		return []byte("uuid-" + filepath.Base(args[len(args)-1]) + "\n"), nil
	}
	return nil, nil
}

func (f *fakeRun) has(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func testAnswers() Answers {
	a := Answers{
		Roles:    map[string]Role{"sdc": RoleSystem, "sdb": RolePerformance, "sda": RoleStandard, "nvme0n1": RoleNone},
		Hostname: "nidaos", User: "nida", SSHKey: "seed", Password: "a long owner password",
		Keyboard: "it", Timezone: "Europe/Rome", Confirm: "nidaos",
	}
	a.Normalize()
	return a
}

func testDisks() []Disk {
	return []Disk{disk("sda", "hdd", 64, 0), disk("sdb", "ssd", 32, 0), disk("sdc", "ssd", 24, 0), disk("nvme0n1", "nvme", 100, 0)}
}

func TestInstall(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	f := &fakeRun{target: target}
	var steps, lines []string
	e := &Engine{Run: f, Target: target, TmpMount: filepath.Join(t.TempDir(), "tmp"), PayloadRef: "ghcr.io/fathormb/nidaos:proof", UEFI: true,
		Log: func(s string) { lines = append(lines, s) }, Step: func(i int, s string) { steps = append(steps, s) },
		Lines: func() []string { return lines }}
	key := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIK operator@pc"
	if err := e.Install(context.Background(), testAnswers(), testDisks(), key); err != nil {
		t.Fatalf("install: %v\n%s", err, strings.Join(lines, "\n"))
	}
	if strings.Join(steps, ",") != "run,done,run,done,run,done,run,done,run,done" {
		t.Errorf("steps = %v", steps)
	}
	for _, want := range []string{
		"wipefs -a /dev/sdc", "sgdisk -n 1:0:+600M -t 1:ef00 -c 1:EFI -n 2:0:+1G", "sgdisk -n 1:0:0 -t 1:8300 -c 1:nida-perf /dev/sdb",
		"mkfs.xfs -f -q -L root /dev/sdc3", "mkfs.xfs -f -q -L nida-std /dev/sda1", "mount /dev/sdc3 " + target,
		"bootc install to-filesystem --source-imgref containers-storage:ghcr.io/fathormb/nidaos:proof", "umount -R " + target,
	} {
		if !f.has(want) {
			t.Errorf("no command %q in\n%s", want, strings.Join(f.calls, "\n"))
		}
	}
	if f.has("wipefs -a /dev/nvme0n1") {
		t.Error("the installer erased a disk that the plan does not use")
	}
	if !f.has("mount --bind " + e.TmpMount + " /var/tmp") || !f.has("umount /var/tmp") {
		t.Error("/var/tmp is not on a tier during the copy")
	}

	dep := filepath.Join(target, "ostree", "deploy", "default")
	fstab, _ := os.ReadFile(filepath.Join(dep, "deploy", "abc123.0", "etc", "fstab"))
	if !strings.Contains(string(fstab), "UUID=uuid-sdb1 /var/nida/performance xfs defaults,nofail") || !strings.Contains(string(fstab), "UUID=uuid-sda1 /var/nida/standard") {
		t.Errorf("fstab:\n%s", fstab)
	}
	host, _ := os.ReadFile(filepath.Join(dep, "deploy", "abc123.0", "etc", "hostname"))
	if string(host) != "nidaos\n" {
		t.Errorf("hostname = %q", host)
	}
	plan, _ := os.ReadFile(filepath.Join(dep, "var", "lib", "nidaos", "install-plan.txt"))
	if !strings.HasPrefix(string(plan), "os=sdc\nperformance=sdb\nstandard=sda\nunused=nvme0n1\n") {
		t.Errorf("plan:\n%s", plan)
	}
	fbPath := filepath.Join(dep, "var", "lib", "nidaos", "firstboot.json")
	b, err := os.ReadFile(fbPath)
	if err != nil {
		t.Fatal(err)
	}
	var fb Firstboot
	if err := json.Unmarshal(b, &fb); err != nil || fb.SSHKey != key || fb.Password != "a long owner password" || fb.User != "nida" || strings.Join(fb.Groups, ",") != "wheel,nida" {
		t.Errorf("firstboot = %+v %v", fb, err)
	}
	if st, err := os.Stat(fbPath); err == nil && runtime.GOOS == "linux" && st.Mode().Perm() != 0o600 {
		t.Errorf("firstboot mode = %v", st.Mode())
	}
	if _, err := os.Stat(filepath.Join(dep, "var", "lib", "nidaos", "install.log")); err != nil {
		t.Errorf("install.log: %v", err)
	}
}

func TestInstallStopsAtTheFirstError(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	f := &fakeRun{target: target, fail: "bootc"}
	var steps []string
	e := &Engine{Run: f, Target: target, TmpMount: "/run/x", PayloadRef: "x", UEFI: false, Step: func(i int, s string) { steps = append(steps, s) }}
	err := e.Install(context.Background(), testAnswers(), testDisks(), "")
	if err == nil || !strings.Contains(err.Error(), "Copy the system image") {
		t.Fatalf("err = %v", err)
	}
	if strings.Join(steps, ",") != "run,done,run,failed" {
		t.Errorf("steps = %v", steps)
	}
	if !f.has("sgdisk -n 1:0:+1M -t 1:ef02 -c 1:BIOS") {
		t.Error("a BIOS computer gets no BIOS boot partition")
	}
	if !f.has("umount -R " + target) {
		t.Error("no clean-up after the error")
	}
}

func TestPartName(t *testing.T) {
	if PartName("/dev/sda", 3) != "/dev/sda3" || PartName("/dev/nvme0n1", 1) != "/dev/nvme0n1p1" || PartName("/dev/mmcblk0", 2) != "/dev/mmcblk0p2" {
		t.Error("PartName")
	}
}

func TestAnswers(t *testing.T) {
	disks := testDisks()
	const seedKey = "ssh-ed25519 AAAA x"
	a := testAnswers()
	if err := a.Validate(disks, seedKey); err != nil {
		t.Fatalf("good answers: %v", err)
	}
	bad := func(field string, change func(*Answers), seed string) {
		t.Helper()
		x := testAnswers()
		change(&x)
		var fe *FieldError
		if err := x.Validate(disks, seed); !errors.As(err, &fe) || fe.Field != field {
			t.Errorf("%s: %v", field, err)
		}
	}
	bad("password", func(a *Answers) { a.Password = "short" }, seedKey)
	bad("ssh_key", func(a *Answers) {}, "")
	bad("ssh_key", func(a *Answers) { a.SSHKey = "not a key" }, "")
	bad("hostname", func(a *Answers) { a.Hostname, a.Confirm = "-bad", "-bad" }, seedKey)
	bad("user", func(a *Answers) { a.User = "root" }, seedKey)
	bad("confirm", func(a *Answers) { a.Confirm = "other" }, seedKey)
	bad("roles", func(a *Answers) { a.Roles = map[string]Role{"sda": RoleStandard} }, seedKey)
	bad("network.gateway", func(a *Answers) {
		a.Network = Network{Mode: "static", Address: "192.168.1.50/24", Gateway: "10.0.0.1"}
	}, seedKey)
	x := testAnswers()
	x.Network = Network{Mode: "static", Address: "192.168.1.50/24", Gateway: "192.168.1.1", DNS: []string{"1.1.1.1"}}
	if err := x.Validate(disks, seedKey); err != nil {
		t.Errorf("static network: %v", err)
	}
	if r := x.Redacted(); r.Password == x.Password {
		t.Error("Redacted keeps the password")
	}
}

func TestSeed(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "authorized_keys"), []byte("# comment\nssh-ed25519 AAAAC3Nza operator@windows-pc\n"), 0o644)
	s, err := ReadSeedDir(dir)
	if err != nil || !s.HasKey || s.KeyComment != "operator@windows-pc" || s.Auto != nil {
		t.Fatalf("seed = %+v %v", s, err)
	}
	os.WriteFile(filepath.Join(dir, "install.json"), []byte(`{"plan":"proposed","password":"a long password 1"}`), 0o644)
	if _, err := ReadSeedDir(dir); err == nil {
		t.Error("an automatic installation without erase: yes")
	}
	os.WriteFile(filepath.Join(dir, "install.json"), []byte(`{"plan":"proposed","password":"a long password 1","erase":"yes"}`), 0o644)
	s, err = ReadSeedDir(dir)
	if err != nil || s.Auto == nil || s.Auto.After != "poweroff" || s.Auto.Password != "a long password 1" {
		t.Fatalf("auto = %+v %v", s.Auto, err)
	}
}
