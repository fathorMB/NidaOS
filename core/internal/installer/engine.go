package installer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The steps of the installation, as on the install step of the mockup.
var StepNames = []string{
	"Erase the disks and make the partitions",
	"Copy the system image and install the boot loader",
	"Prepare the storage tiers",
	"Make the owner and set the access",
	"Examine the installation",
}

// Labels of the file systems that the installer makes.
const (
	LabelEFI         = "EFI"
	LabelBoot        = "boot"
	LabelRoot        = "root"
	LabelPerformance = "nida-perf"
	LabelStandard    = "nida-std"
)

// Engine installs NidaOS on the disks of a plan (decision D33).
type Engine struct {
	Run Runner
	// Target is the mount point of the new root file system.
	Target string
	// TmpMount is the mount point of a tier for the temporary files of the
	// image copy. The memory of the installer is too small for them.
	TmpMount string
	// PayloadRef is the NidaOS image in the container storage of the media.
	PayloadRef string
	// UEFI: the computer starts with UEFI firmware. Else BIOS.
	UEFI bool
	// Log gets each line of the progress and of the commands.
	Log func(string)
	// Step tells the start (state "run") and the end ("done" or "failed")
	// of each step.
	Step func(i int, state string)
	// Lines gives the lines of the log for install.log. Nil: no file.
	Lines func() []string
}

func (e *Engine) logf(format string, args ...any) {
	if e.Log != nil {
		e.Log(fmt.Sprintf(format, args...))
	}
}

// run runs a command and writes it and its output in the log.
func (e *Engine) run(ctx context.Context, name string, args ...string) (string, error) {
	e.logf("$ %s %s", name, strings.Join(args, " "))
	out, err := e.Run.Run(ctx, name, args...)
	if s, ok := e.Run.(streamer); !ok || !s.Streaming() {
		for l := range strings.SplitSeq(strings.TrimRight(string(out), "\n"), "\n") {
			if l != "" {
				e.logf("  %s", l)
			}
		}
	}
	if err != nil {
		e.logf("  error: %v", err)
		return string(out), fmt.Errorf("%s: %w", name, err)
	}
	return string(out), nil
}

// PartName gives the partition n of a disk: /dev/sda1 or /dev/nvme0n1p1.
func PartName(dev string, n int) string {
	if last := dev[len(dev)-1]; last >= '0' && last <= '9' {
		return fmt.Sprintf("%sp%d", dev, n)
	}
	return fmt.Sprintf("%s%d", dev, n)
}

// layout is the result of the first step.
type layout struct {
	sysDisk, esp, boot, root string
	perf, std                string
}

// Install runs the five steps. It stops at the first error. The caller
// writes the files of the plan and the first boot (WriteFiles is part of
// step 4).
func (e *Engine) Install(ctx context.Context, a Answers, disks []Disk, seedKey string) error {
	byName := map[string]Disk{}
	for _, d := range disks {
		byName[d.Name] = d
	}
	var l layout
	steps := []func(context.Context) error{
		func(ctx context.Context) error { return e.partitions(ctx, a.Roles, byName, &l) },
		func(ctx context.Context) error { return e.copyImage(ctx, &l) },
		func(ctx context.Context) error { return e.tiers(ctx, &l) },
		func(ctx context.Context) error { return e.access(a, disks, seedKey) },
		func(ctx context.Context) error { return e.finish(ctx) },
	}
	for i, f := range steps {
		if e.Step != nil {
			e.Step(i, "run")
		}
		start := time.Now()
		e.logf("== %s", StepNames[i])
		if err := f(ctx); err != nil {
			e.logf("== failed after %s: %v", time.Since(start).Round(time.Second), err)
			if e.Step != nil {
				e.Step(i, "failed")
			}
			e.cleanup()
			return fmt.Errorf("%s: %w", StepNames[i], err)
		}
		if e.Step != nil {
			e.Step(i, "done")
		}
	}
	return nil
}

func (e *Engine) partitions(ctx context.Context, roles map[string]Role, disks map[string]Disk, l *layout) error {
	order := []Role{RoleSystem, RolePerformance, RoleStandard}
	for _, role := range order {
		for name, r := range roles {
			if r != role {
				continue
			}
			d, ok := disks[name]
			if !ok || !d.Usable {
				return fmt.Errorf("%w: %s", ErrBadDisk, name)
			}
			dev := d.Path
			e.logf("%s: %s (%s)", role, dev, d.Model)
			if _, err := e.run(ctx, "wipefs", "-a", dev); err != nil {
				return err
			}
			if _, err := e.run(ctx, "sgdisk", "--zap-all", dev); err != nil {
				return err
			}
			switch role {
			case RoleSystem:
				first := []string{"-n", "1:0:+600M", "-t", "1:ef00", "-c", "1:EFI"}
				if !e.UEFI {
					first = []string{"-n", "1:0:+1M", "-t", "1:ef02", "-c", "1:BIOS"}
				}
				args := append(first, "-n", "2:0:+1G", "-t", "2:8300", "-c", "2:boot", "-n", "3:0:0", "-t", "3:8304", "-c", "3:root", dev)
				if _, err := e.run(ctx, "sgdisk", args...); err != nil {
					return err
				}
				l.sysDisk, l.esp, l.boot, l.root = dev, PartName(dev, 1), PartName(dev, 2), PartName(dev, 3)
			case RolePerformance, RoleStandard:
				label := LabelPerformance
				if role == RoleStandard {
					label = LabelStandard
				}
				if _, err := e.run(ctx, "sgdisk", "-n", "1:0:0", "-t", "1:8300", "-c", "1:"+label, dev); err != nil {
					return err
				}
				if role == RolePerformance {
					l.perf = PartName(dev, 1)
				} else {
					l.std = PartName(dev, 1)
				}
			}
			if _, err := e.run(ctx, "partprobe", dev); err != nil {
				return err
			}
		}
	}
	if l.root == "" {
		return ErrNoSystem
	}
	if _, err := e.run(ctx, "udevadm", "settle", "--timeout=30"); err != nil {
		return err
	}
	if e.UEFI {
		if _, err := e.run(ctx, "mkfs.vfat", "-F", "32", "-n", LabelEFI, l.esp); err != nil {
			return err
		}
	}
	if _, err := e.run(ctx, "mkfs.ext4", "-F", "-q", "-L", LabelBoot, l.boot); err != nil {
		return err
	}
	if _, err := e.run(ctx, "mkfs.xfs", "-f", "-q", "-L", LabelRoot, l.root); err != nil {
		return err
	}
	for part, label := range map[string]string{l.perf: LabelPerformance, l.std: LabelStandard} {
		if part == "" {
			continue
		}
		if _, err := e.run(ctx, "mkfs.xfs", "-f", "-q", "-L", label, part); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) copyImage(ctx context.Context, l *layout) error {
	if e.PayloadRef == "" {
		return errors.New("the installation media has no reference of the NidaOS image")
	}
	mounts := [][2]string{{l.root, e.Target}, {l.boot, filepath.Join(e.Target, "boot")}}
	if e.UEFI {
		mounts = append(mounts, [2]string{l.esp, filepath.Join(e.Target, "boot", "efi")})
	}
	for _, m := range mounts {
		if _, err := e.run(ctx, "mkdir", "-p", m[1]); err != nil {
			return err
		}
		if _, err := e.run(ctx, "mount", m[0], m[1]); err != nil {
			return err
		}
	}
	// The image copy writes large temporary files in /var/tmp. The memory
	// of the installer is too small for them (proof of M-01), thus /var/tmp
	// goes on a storage tier for the time of the copy.
	tier := firstOf(l.std, l.perf)
	if tier != "" {
		for _, c := range [][]string{{"mkdir", "-p", e.TmpMount}, {"mount", tier, e.TmpMount}, {"mount", "--bind", e.TmpMount, "/var/tmp"}} {
			if _, err := e.run(ctx, c[0], c[1:]...); err != nil {
				return err
			}
		}
	}
	// bootc of the installation media installs the NidaOS image from the
	// container storage of the media, as in the proof of M-01. The
	// installed system gets its updates from the same reference.
	_, err := e.run(ctx, "bootc", "install", "to-filesystem",
		"--source-imgref", "containers-storage:"+e.PayloadRef,
		"--target-imgref", e.PayloadRef,
		"--skip-fetch-check",
		"--disable-selinux",
		"--karg", "console=tty0",
		e.Target)
	if tier != "" {
		// The tier must be empty for the installed system.
		e.run(context.Background(), "umount", "/var/tmp") //nolint:errcheck
		emptyDir(e.TmpMount)
		e.run(context.Background(), "umount", e.TmpMount) //nolint:errcheck
	}
	return err
}

func firstOf(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// emptyDir deletes the content of a mounted tier. It refuses a path that
// is not below /run or the temporary folder.
func emptyDir(dir string) {
	clean := filepath.Clean(dir)
	if !filepath.IsAbs(clean) || clean == "/" || (!strings.HasPrefix(clean, "/run/") && !strings.HasPrefix(clean, os.TempDir())) {
		return
	}
	entries, err := os.ReadDir(clean)
	if err != nil {
		return
	}
	for _, x := range entries {
		_ = os.RemoveAll(filepath.Join(clean, x.Name()))
	}
}

// deployment finds the /etc and the /var of the installed system.
func (e *Engine) deployment() (etc, varDir string, err error) {
	etcs, _ := filepath.Glob(filepath.Join(e.Target, "ostree", "deploy", "*", "deploy", "*", "etc"))
	if len(etcs) != 1 {
		return "", "", fmt.Errorf("the installer found %d deployments in %s, not one", len(etcs), e.Target)
	}
	stateroot := filepath.Dir(filepath.Dir(filepath.Dir(etcs[0])))
	return etcs[0], filepath.Join(stateroot, "var"), nil
}

func (e *Engine) tiers(ctx context.Context, l *layout) error {
	etc, varDir, err := e.deployment()
	if err != nil {
		return err
	}
	var lines []string
	for _, t := range []struct{ part, dir string }{{l.perf, "performance"}, {l.std, "standard"}} {
		if t.part == "" {
			continue
		}
		out, err := e.run(ctx, "blkid", "-s", "UUID", "-o", "value", t.part)
		if err != nil {
			return err
		}
		uuid := strings.TrimSpace(out)
		if uuid == "" {
			return fmt.Errorf("blkid gives no UUID for %s", t.part)
		}
		// nofail: the system starts also without the tier. nida-core shows
		// the tier as "not mounted".
		lines = append(lines, fmt.Sprintf("UUID=%s /var/nida/%s xfs defaults,nofail,x-systemd.device-timeout=30s 0 0", uuid, t.dir))
		if err := os.MkdirAll(filepath.Join(varDir, "nida", t.dir), 0o755); err != nil {
			return err
		}
	}
	if len(lines) == 0 {
		e.logf("the plan has no storage tier")
		return nil
	}
	f, err := os.OpenFile(filepath.Join(etc, "fstab"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "# The storage tiers of NidaOS (decision D7). The installer wrote these lines.\n%s\n", strings.Join(lines, "\n"))
	if err == nil {
		e.logf("fstab: %s", strings.Join(lines, " | "))
	}
	return err
}

func (e *Engine) access(a Answers, disks []Disk, seedKey string) error {
	etc, varDir, err := e.deployment()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(etc, "hostname"), []byte(a.Hostname+"\n"), 0o644); err != nil {
		return err
	}
	dir := filepath.Join(varDir, "lib", "nidaos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "install-plan.txt"), []byte(PlanText(a.Roles, disks)), 0o644); err != nil {
		return err
	}
	fb, err := MarshalFirstboot(FirstbootOf(a, seedKey))
	if err != nil {
		return err
	}
	// The file has the owner password until the first start. Only root
	// can read it, and nida-core firstboot deletes it.
	if err := os.WriteFile(filepath.Join(dir, "firstboot.json"), fb, 0o600); err != nil {
		return err
	}
	e.logf("computer name %s, owner %s, first-boot file written", a.Hostname, a.User)
	return nil
}

func (e *Engine) finish(ctx context.Context) error {
	var check string
	if e.UEFI {
		check = filepath.Join(e.Target, "boot", "efi", "EFI")
	} else {
		check = filepath.Join(e.Target, "boot", "grub2")
	}
	if _, err := os.Stat(check); err != nil {
		return fmt.Errorf("the boot loader is not installed: %w", err)
	}
	if _, varDir, err := e.deployment(); err == nil {
		e.logf("the installation is complete")
		e.saveLog(filepath.Join(varDir, "lib", "nidaos", "install.log"))
	}
	if _, err := e.run(ctx, "sync"); err != nil {
		return err
	}
	_, err := e.run(ctx, "umount", "-R", e.Target)
	return err
}

func (e *Engine) saveLog(path string) {
	if e.Lines == nil {
		return
	}
	_ = os.WriteFile(path, []byte(strings.Join(e.Lines(), "\n")+"\n"), 0o600)
}

// cleanup unmounts the target after a failure, thus the operator can try
// again.
func (e *Engine) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e.Run.Run(ctx, "umount", "-R", e.Target) //nolint:errcheck
	e.Run.Run(ctx, "umount", e.TmpMount)     //nolint:errcheck
}
