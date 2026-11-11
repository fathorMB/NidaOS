package installer

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Role is the job of a disk in the plan (decision D7).
type Role string

const (
	RoleSystem      Role = "system"
	RolePerformance Role = "performance"
	RoleStandard    Role = "standard"
	RoleNone        Role = "none"
)

// Plan gives a role to each disk. Reasons tell why the installer proposes
// the role.
type Plan struct {
	Roles   map[string]Role   `json:"roles"`
	Reasons map[string]string `json:"reasons"`
	// Notes are warnings for the operator, for example "no performance tier".
	Notes []string `json:"notes,omitempty"`
}

// class orders the kinds: an NVMe disk is faster than an other
// solid-state disk, and a solid-state disk is faster than a hard disk.
func class(d Disk) int {
	switch d.Kind {
	case "nvme":
		return 0
	case "ssd":
		return 1
	}
	return 2
}

// SpeedMargin is the difference of speed that the plan accepts as "the
// same speed": the measurement of a disk changes from one run to the next.
const SpeedMargin = 1.25

// faster tells if a is a better system or performance disk than b.
func faster(a, b Disk) bool {
	if ca, cb := class(a), class(b); ca != cb {
		return ca < cb
	}
	if a.SpeedMBs > 0 && b.SpeedMBs > 0 {
		if a.SpeedMBs > b.SpeedMBs*SpeedMargin {
			return true
		}
		if b.SpeedMBs > a.SpeedMBs*SpeedMargin {
			return false
		}
	}
	// The same speed: the smaller disk gets the fast role, thus the larger
	// disk stays for the data.
	if a.SizeBytes != b.SizeBytes {
		return a.SizeBytes < b.SizeBytes
	}
	return a.Name < b.Name
}

// Propose makes the plan of the installer:
//  1. The fastest disk gets the system.
//  2. The next solid-state disk is the performance tier.
//  3. The largest hard disk is the standard tier. Without a hard disk, the
//     largest disk that is not used is the standard tier.
//  4. The other disks are not used.
func Propose(disks []Disk) Plan {
	p := Plan{Roles: map[string]Role{}, Reasons: map[string]string{}}
	var free []Disk
	for _, d := range disks {
		if d.Usable {
			free = append(free, d)
		}
	}
	if len(free) == 0 {
		p.Notes = append(p.Notes, "The computer has no disk that the installer can use.")
		return p
	}
	sort.Slice(free, func(i, j int) bool { return faster(free[i], free[j]) })

	sys := free[0]
	p.Roles[sys.Name] = RoleSystem
	p.Reasons[sys.Name] = "Proposed: the fastest disk."
	if len(free) > 1 && class(free[1]) == class(sys) && !clearlyFaster(sys, free[1]) {
		p.Reasons[sys.Name] = "Proposed: the fastest disk. It has the speed of an other disk, and it is the smaller one."
	}
	rest := free[1:]

	for i, d := range rest {
		if d.Kind != "hdd" {
			p.Roles[d.Name] = RolePerformance
			p.Reasons[d.Name] = fmt.Sprintf("Proposed: the next %s.", kindText(d))
			rest = append(rest[:i:i], rest[i+1:]...)
			break
		}
	}

	if len(rest) > 0 {
		best := -1
		for i, d := range rest {
			if d.Kind == "hdd" && (best < 0 || d.SizeBytes > rest[best].SizeBytes) {
				best = i
			}
		}
		why := "Proposed: the largest hard disk."
		if best < 0 {
			for i, d := range rest {
				if best < 0 || d.SizeBytes > rest[best].SizeBytes {
					best = i
				}
			}
			why = "Proposed: the largest disk that is not used."
		}
		d := rest[best]
		p.Roles[d.Name] = RoleStandard
		p.Reasons[d.Name] = why
		rest = append(rest[:best:best], rest[best+1:]...)
	}
	for _, d := range rest {
		p.Roles[d.Name] = RoleNone
		p.Reasons[d.Name] = "Not used: each role has a disk. The installer does not change this disk."
	}

	if !p.has(RolePerformance) {
		p.Notes = append(p.Notes, "There is no performance tier. The databases and the apps use the standard tier.")
	}
	if !p.has(RoleStandard) && !p.has(RolePerformance) {
		p.Notes = append(p.Notes, "There is no storage tier. Add a disk later for the apps and the databases.")
	} else if !p.has(RoleStandard) {
		p.Notes = append(p.Notes, "There is no standard tier. The backups of the databases need it: add a disk later.")
	}
	return p
}

func clearlyFaster(a, b Disk) bool {
	return a.SpeedMBs > 0 && b.SpeedMBs > 0 && a.SpeedMBs > b.SpeedMBs*SpeedMargin
}

func kindText(d Disk) string {
	switch d.Kind {
	case "nvme":
		return "solid-state disk (NVMe)"
	case "ssd":
		return "solid-state disk"
	}
	return "hard disk"
}

func (p Plan) has(r Role) bool {
	for _, x := range p.Roles {
		if x == r {
			return true
		}
	}
	return false
}

// DiskOf gives the disk with the role, or "".
func (p Plan) DiskOf(r Role) string {
	var names []string
	for n, x := range p.Roles {
		if x == r {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

// Errors of a plan.
var (
	ErrNoSystem   = errors.New("one disk must have the System role")
	ErrSameRole   = errors.New("two disks have the same role: give each role to one disk only")
	ErrBadDisk    = errors.New("the plan names a disk that the installer cannot use")
	ErrUnknownRole = errors.New("the plan has an unknown role")
)

// Check examines a plan of the operator against the disks.
func Check(roles map[string]Role, disks []Disk) error {
	usable := map[string]bool{}
	for _, d := range disks {
		usable[d.Name] = d.Usable
	}
	seen := map[Role]bool{}
	for name, r := range roles {
		switch r {
		case RoleNone:
			continue
		case RoleSystem, RolePerformance, RoleStandard:
		default:
			return fmt.Errorf("%w: %q", ErrUnknownRole, r)
		}
		if !usable[name] {
			return fmt.Errorf("%w: %s", ErrBadDisk, name)
		}
		if seen[r] {
			return ErrSameRole
		}
		seen[r] = true
	}
	if !seen[RoleSystem] {
		return ErrNoSystem
	}
	return nil
}

// PlanText is the disk plan file of the installed system
// (/var/lib/nidaos/install-plan.txt). nida-core reads it (system/disks.go).
func PlanText(roles map[string]Role, disks []Disk) string {
	var unused []string
	var b strings.Builder
	get := func(r Role) string {
		for _, d := range disks {
			if roles[d.Name] == r {
				return d.Name
			}
		}
		return ""
	}
	for _, d := range disks {
		if d.Usable && (roles[d.Name] == RoleNone || roles[d.Name] == "") {
			unused = append(unused, d.Name)
		}
	}
	fmt.Fprintf(&b, "os=%s\nperformance=%s\nstandard=%s\nunused=%s\n", get(RoleSystem), get(RolePerformance), get(RoleStandard), strings.Join(unused, " "))
	b.WriteString("# name kind size_bytes speed_mbs\n")
	for _, d := range disks {
		fmt.Fprintf(&b, "# %s %s %d %.0f\n", d.Name, d.Kind, d.SizeBytes, d.SpeedMBs)
	}
	return b.String()
}
