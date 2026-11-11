package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

// Answers are the choices of the operator in the six steps. In the
// automatic mode, they come from install.json on the seed volume.
type Answers struct {
	Roles    map[string]Role `json:"roles"`
	Hostname string          `json:"hostname"`
	User     string          `json:"user"`
	// SSHKey is one public key line. "seed": the key of the seed volume.
	SSHKey string `json:"ssh_key"`
	// Password is the one owner password (decision D34): the web console
	// and Remote Desktop.
	Password string  `json:"password"`
	Keyboard string  `json:"keyboard"`
	Timezone string  `json:"timezone"`
	Network  Network `json:"network"`
	// Confirm must be the computer name (the review step).
	Confirm string `json:"confirm"`
}

// Network is the address of the installed computer.
type Network struct {
	// Mode is "dhcp" or "static".
	Mode      string `json:"mode"`
	Interface string `json:"interface,omitempty"`
	// Address is the address with its prefix, for example 192.168.1.50/24.
	Address string   `json:"address,omitempty"`
	Gateway string   `json:"gateway,omitempty"`
	DNS     []string `json:"dns,omitempty"`
}

// MinPassword is the minimum length of the owner password (decision D16).
const MinPassword = 12

var (
	hostRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	userRE = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	keyRE  = regexp.MustCompile(`^(ssh-(ed25519|rsa|dss)|ecdsa-sha2-nistp(256|384|521)|sk-(ssh-ed25519|ecdsa-sha2-nistp256)@openssh\.com) [A-Za-z0-9+/=]+( .*)?$`)
	zoneRE = regexp.MustCompile(`^(UTC|[A-Z][A-Za-z_+-]*(/[A-Za-z0-9_+-]+){1,2})$`)
)

// Keyboards are the keyboard layouts of the welcome step.
var Keyboards = map[string]string{
	"us": "English (US)", "gb": "English (UK)", "it": "Italian", "de": "German",
	"fr": "French", "es": "Spanish", "ch": "Swiss", "pt": "Portuguese",
}

// reserved names that the owner cannot have.
var reservedUsers = map[string]bool{"root": true, "bin": true, "daemon": true, "nobody": true,
	"gnome-remote-desktop": true, "sshd": true, "kiosk": true}

// FieldError tells which answer is not correct.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Message }

// ErrNotConfirmed: the confirmation is not the computer name.
var ErrNotConfirmed = errors.New("the confirmation is not the computer name")

// Normalize sets the defaults.
func (a *Answers) Normalize() {
	a.Hostname = strings.ToLower(strings.TrimSpace(a.Hostname))
	a.User = strings.TrimSpace(a.User)
	a.SSHKey = strings.TrimSpace(a.SSHKey)
	a.Confirm = strings.ToLower(strings.TrimSpace(a.Confirm))
	if a.Hostname == "" {
		a.Hostname = "nidaos"
	}
	if a.User == "" {
		a.User = "nida"
	}
	if a.Keyboard == "" {
		a.Keyboard = "us"
	}
	if a.Timezone == "" {
		a.Timezone = "UTC"
	}
	if a.Network.Mode == "" {
		a.Network.Mode = "dhcp"
	}
}

// Validate examines the answers. seedKey is the key of the seed volume, or "".
func (a *Answers) Validate(disks []Disk, seedKey string) error {
	if err := Check(a.Roles, disks); err != nil {
		return &FieldError{"roles", err.Error()}
	}
	if !hostRE.MatchString(a.Hostname) {
		return &FieldError{"hostname", "Use 1 to 63 lowercase letters, digits and hyphens. Do not start or end with a hyphen."}
	}
	if !userRE.MatchString(a.User) || reservedUsers[a.User] {
		return &FieldError{"user", "Use lowercase letters, digits, - and _. Start with a letter. Do not use a name of the system."}
	}
	key := a.SSHKey
	if key == "seed" {
		key = seedKey
		if key == "" {
			return &FieldError{"ssh_key", "The seed volume NIDASEED has no key. Paste a public key."}
		}
	}
	if key != "" && !keyRE.MatchString(key) {
		return &FieldError{"ssh_key", "This is not an OpenSSH public key. It starts with ssh-ed25519, ssh-rsa or ecdsa-."}
	}
	if len([]rune(a.Password)) < MinPassword {
		return &FieldError{"password", fmt.Sprintf("Use %d characters or more.", MinPassword)}
	}
	if strings.ContainsAny(a.Password, "\n\r") {
		return &FieldError{"password", "The password cannot have a line break."}
	}
	if _, ok := Keyboards[a.Keyboard]; !ok {
		return &FieldError{"keyboard", "Select a keyboard of the list."}
	}
	if !zoneRE.MatchString(a.Timezone) {
		return &FieldError{"timezone", "Select a time zone of the list."}
	}
	switch a.Network.Mode {
	case "dhcp":
	case "static":
		p, err := netip.ParsePrefix(a.Network.Address)
		if err != nil || !p.Addr().Is4() || p.Bits() < 8 || p.Bits() > 30 {
			return &FieldError{"network.address", "Write an IPv4 address with its prefix, for example 192.168.1.50/24."}
		}
		g, err := netip.ParseAddr(a.Network.Gateway)
		if err != nil || !g.Is4() || !p.Masked().Contains(g) || g == p.Addr() {
			return &FieldError{"network.gateway", "Write the IPv4 address of the router. It must be in the network of the address."}
		}
		for _, d := range a.Network.DNS {
			if x, err := netip.ParseAddr(d); err != nil || !x.Is4() {
				return &FieldError{"network.dns", "Write IPv4 addresses for the DNS servers."}
			}
		}
	default:
		return &FieldError{"network.mode", "Select the automatic address or a fixed address."}
	}
	if a.Confirm != a.Hostname {
		return &FieldError{"confirm", ErrNotConfirmed.Error()}
	}
	return nil
}

// Redacted gives a copy without the password, for the logs and the API.
func (a Answers) Redacted() Answers {
	if a.Password != "" {
		a.Password = "********"
	}
	return a
}

// Firstboot is the file that the installed system reads at its first start
// (/var/lib/nidaos/firstboot.json, mode 0600). nida-core firstboot applies
// it and deletes it.
type Firstboot struct {
	Hostname string   `json:"hostname"`
	User     string   `json:"user"`
	SSHKey   string   `json:"ssh_key,omitempty"`
	Password string   `json:"password"`
	Keyboard string   `json:"keyboard"`
	Timezone string   `json:"timezone"`
	Network  Network  `json:"network"`
	Groups   []string `json:"groups"`
}

// FirstbootOf gives the first-boot file of the answers.
func FirstbootOf(a Answers, seedKey string) Firstboot {
	key := a.SSHKey
	if key == "seed" {
		key = seedKey
	}
	return Firstboot{
		Hostname: a.Hostname, User: a.User, SSHKey: key, Password: a.Password,
		Keyboard: a.Keyboard, Timezone: a.Timezone, Network: a.Network,
		Groups: []string{"wheel", "nida"},
	}
}

// MarshalFirstboot gives the JSON of the file.
func MarshalFirstboot(f Firstboot) ([]byte, error) {
	return json.MarshalIndent(f, "", "  ")
}
