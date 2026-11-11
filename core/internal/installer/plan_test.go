package installer

import (
	"errors"
	"strings"
	"testing"
)

const GiB = 1 << 30

func disk(name, kind string, gib int64, speed float64) Disk {
	return Disk{Name: name, Path: "/dev/" + name, Kind: kind, SizeBytes: gib * GiB, SpeedMBs: speed, Usable: true}
}

func TestParseDisks(t *testing.T) {
	out := `{"blockdevices":[
	 {"name":"sda","path":"/dev/sda","size":68719476736,"rota":true,"rm":false,"ro":false,"type":"disk","tran":"sata","model":"QEMU HARDDISK","children":[{"name":"sda1","size":100,"type":"part","fstype":"ext4"},{"name":"sda2","size":100,"type":"part","fstype":"ext4"},{"name":"sda3","size":100,"type":"part","fstype":"swap"}]},
	 {"name":"sdb","size":"34359738368","rota":"0","rm":"0","ro":"0","type":"disk","tran":"sata","model":"QEMU SSD"},
	 {"name":"sr0","size":4000000000,"rota":false,"rm":true,"ro":true,"type":"rom","tran":"ata","label":"NidaOS-Installer"},
	 {"name":"sdc","size":30000000000,"rota":false,"rm":true,"ro":false,"type":"disk","tran":"usb","model":"USB key","children":[{"name":"sdc1","type":"part","label":"NidaOS-Installer","fstype":"iso9660"}]},
	 {"name":"nvme0n1","size":1000204886016,"rota":false,"rm":false,"ro":false,"type":"disk","tran":"nvme","model":"Samsung SSD 980"},
	 {"name":"sdd","size":4294967296,"rota":false,"rm":false,"ro":false,"type":"disk","tran":"sata","model":"small"},
	 {"name":"zram0","size":8000000000,"type":"disk"}
	]}`
	disks, err := ParseDisks([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Disk{}
	for _, d := range disks {
		by[d.Name] = d
	}
	if len(disks) != 5 {
		t.Fatalf("disks = %+v", disks)
	}
	if d := by["sda"]; d.Kind != "hdd" || !d.Usable || d.Now != "ext4, swap, 3 partitions" {
		t.Errorf("sda = %+v", d)
	}
	if d := by["sdb"]; d.Kind != "ssd" || d.SizeBytes != 32*GiB || d.Now != "empty" || d.Path != "/dev/sdb" {
		t.Errorf("sdb = %+v", d)
	}
	if d := by["nvme0n1"]; d.Kind != "nvme" || !d.Usable {
		t.Errorf("nvme = %+v", d)
	}
	if d := by["sdc"]; d.Usable || d.Reason != "the installation media" {
		t.Errorf("usb media = %+v", d)
	}
	if d := by["sdd"]; d.Usable || !strings.Contains(d.Reason, "8 GB") {
		t.Errorf("small = %+v", d)
	}
}

func roles(p Plan) string {
	var b []string
	for _, r := range []Role{RoleSystem, RolePerformance, RoleStandard} {
		b = append(b, string(r)+"="+p.DiskOf(r))
	}
	return strings.Join(b, " ")
}

func TestProposeSets(t *testing.T) {
	cases := []struct {
		name  string
		disks []Disk
		want  string
		notes int
	}{
		// The disks of the VM test: the hard disk is first on the bus.
		{"vm test", []Disk{disk("sda", "hdd", 64, 900), disk("sdb", "ssd", 32, 880), disk("sdc", "ssd", 24, 910)},
			"system=sdc performance=sdb standard=sda", 0},
		// The computer of the operator (mockup): 2 SSD and 1 HDD.
		{"operator", []Disk{disk("sda", "hdd", 931, 162), disk("sdb", "ssd", 112, 486), disk("sdc", "ssd", 119, 441)},
			"system=sdb performance=sdc standard=sda", 0},
		// A clearly faster SSD gets the system, also when it is larger.
		{"faster ssd", []Disk{disk("sda", "ssd", 500, 1500), disk("sdb", "ssd", 120, 450)},
			"system=sda performance=sdb standard=", 1},
		// NVMe before SATA, whatever the size.
		{"nvme", []Disk{disk("sda", "ssd", 120, 500), disk("nvme0n1", "nvme", 1000, 3000), disk("sdb", "hdd", 2000, 180), disk("sdc", "hdd", 4000, 170)},
			"system=nvme0n1 performance=sda standard=sdc", 0},
		{"one disk", []Disk{disk("sda", "ssd", 240, 500)}, "system=sda performance= standard=", 2},
		// Two hard disks: the faster gets the system? No: the smaller one
		// with the same speed, and the larger one is the standard tier.
		{"two hdd", []Disk{disk("sda", "hdd", 1000, 160), disk("sdb", "hdd", 2000, 170)},
			"system=sda performance= standard=sdb", 1},
		{"two ssd and no hdd", []Disk{disk("sda", "ssd", 120, 500), disk("sdb", "ssd", 250, 520), disk("sdc", "ssd", 500, 510)},
			"system=sda performance=sdb standard=sdc", 0},
	}
	for _, c := range cases {
		p := Propose(c.disks)
		if got := roles(p); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
		if len(p.Notes) != c.notes {
			t.Errorf("%s: notes %v", c.name, p.Notes)
		}
		for _, d := range c.disks {
			if p.Reasons[d.Name] == "" {
				t.Errorf("%s: no reason for %s", c.name, d.Name)
			}
		}
		if err := Check(p.Roles, c.disks); err != nil {
			t.Errorf("%s: the proposed plan fails the check: %v", c.name, err)
		}
	}
	if p := Propose(nil); len(p.Roles) != 0 || len(p.Notes) != 1 {
		t.Errorf("no disk: %+v", p)
	}
}

func TestCheck(t *testing.T) {
	disks := []Disk{disk("sda", "ssd", 100, 0), disk("sdb", "hdd", 100, 0), {Name: "sdc", Usable: false}}
	if err := Check(map[string]Role{"sda": RolePerformance}, disks); !errors.Is(err, ErrNoSystem) {
		t.Errorf("no system: %v", err)
	}
	if err := Check(map[string]Role{"sda": RoleSystem, "sdb": RoleSystem}, disks); !errors.Is(err, ErrSameRole) {
		t.Errorf("same role: %v", err)
	}
	if err := Check(map[string]Role{"sda": RoleSystem, "sdc": RoleStandard}, disks); !errors.Is(err, ErrBadDisk) {
		t.Errorf("bad disk: %v", err)
	}
	if err := Check(map[string]Role{"sda": RoleSystem, "sdb": "boot"}, disks); !errors.Is(err, ErrUnknownRole) {
		t.Errorf("unknown role: %v", err)
	}
	if err := Check(map[string]Role{"sdb": RoleSystem, "sda": RoleNone}, disks); err != nil {
		t.Errorf("good plan: %v", err)
	}
}

func TestPlanText(t *testing.T) {
	disks := []Disk{disk("sda", "hdd", 64, 900), disk("sdb", "ssd", 32, 880), disk("sdc", "ssd", 24, 910), disk("sdd", "hdd", 10, 100)}
	txt := PlanText(map[string]Role{"sdc": RoleSystem, "sdb": RolePerformance, "sda": RoleStandard, "sdd": RoleNone}, disks)
	if !strings.HasPrefix(txt, "os=sdc\nperformance=sdb\nstandard=sda\nunused=sdd\n") {
		t.Errorf("plan text:\n%s", txt)
	}
}
