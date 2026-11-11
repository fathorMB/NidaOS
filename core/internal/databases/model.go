// Package databases operates the PostgreSQL databases of the owner on k3s
// (milestone M-04).
//
// The parts come from the database controller of GitStack core (decisions
// D4 and D24). NidaOS has one owner. Thus the parts for the owners, the
// quotas, the image signatures and the events are not here.
//
//   - model.go: the data of a database.
//   - manifests.go: the Kubernetes objects of a database.
//   - exec.go: psql and pg_dump in the pod of the database.
//   - roles.go: the roles and the privileges in PostgreSQL.
//   - store.go: the JSON file that keeps the databases.
//   - controller.go: the creation, the links, the backups and the updates.
package databases

import (
	"maps"
	"time"
)

// The states of a database.
const (
	// StateCreating: the controller makes the objects and the roles.
	StateCreating = "creating"
	// StateRestoring: the controller makes the database and restores a
	// backup into it.
	StateRestoring = "restoring"
	// StateUpdating: the controller makes a backup, then changes the image.
	StateUpdating = "updating"
	// StateReady: the database accepts connections.
	StateReady = "ready"
	// StateFailed: the last operation failed. Reason tells why.
	StateFailed = "failed"
)

// The modes of a link.
const (
	ModeRW = "rw"
	ModeRO = "ro"
)

// The storage tiers (decision D7).
const (
	TierPerformance = "performance"
	TierStandard    = "standard"
)

// Link connects a database to an app. The app gets the connection text as
// secret variables. Each link has its own login role.
type Link struct {
	App     string    `json:"app"`
	Mode    string    `json:"mode"`
	Role    string    `json:"role"`
	Created time.Time `json:"created"`
}

// Restore tells the controller to restore a backup into a new database.
type Restore struct {
	// Database is the database of the backup. It can be removed already.
	Database string `json:"database"`
	// Backup is the file name of the backup.
	Backup string `json:"backup"`
}

// Database is one PostgreSQL database of the owner.
type Database struct {
	Name string `json:"name"`
	// Version is the major version: 17 or 18 (decision D23).
	Version int `json:"version"`
	// Image is the image of the server, with the minor version.
	Image       string `json:"image"`
	Tier        string `json:"tier"`
	SizeBytes   int64  `json:"size_bytes"`
	CPUMillis   int    `json:"cpu_millis"`
	MemoryBytes int64  `json:"memory_bytes"`
	State       string `json:"state"`
	Reason      string `json:"reason,omitempty"`
	// LAN is the switch for the home network. LANPort is the port on the
	// computer while LAN is on.
	LAN     bool   `json:"lan"`
	LANPort int    `json:"lan_port,omitempty"`
	Links   []Link `json:"links,omitempty"`
	// Restore is set while the controller restores a backup.
	Restore *Restore `json:"restore,omitempty"`
	// UpdateTo is the target image of an update in progress.
	UpdateTo string    `json:"update_to,omitempty"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
}

// Link gives the link of an app.
func (d *Database) Link(app string) (Link, bool) {
	for _, l := range d.Links {
		if l.App == app {
			return l, true
		}
	}
	return Link{}, false
}

// Busy tells if the controller must work on the database.
func (d *Database) Busy() bool {
	return d.State == StateCreating || d.State == StateRestoring || d.State == StateUpdating
}

func (d Database) clone() Database {
	d.Links = append([]Link(nil), d.Links...)
	if d.Restore != nil {
		r := *d.Restore
		d.Restore = &r
	}
	return d
}

func cloneMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	maps.Copy(out, m)
	return out
}
