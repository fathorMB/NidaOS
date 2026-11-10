// Package system reads the state of the computer: the host, the disks and
// the storage tiers.
//
// The package reads only files: /proc, /sys, /etc/os-release and the disk
// plan of the installer. All paths start at Collector.Root. Thus the tests
// use a copy of these files in testdata, and the service can run on a
// development computer that is not Linux.
package system

import (
	"errors"
	"time"
)

// DefaultPlanFile is the disk plan that the installer writes (M-01, T-08).
const DefaultPlanFile = "/var/lib/nidaos/install-plan.txt"

// Tier names and their storage classes in k3s.
const (
	TierPerformance = "performance"
	TierStandard    = "standard"
)

// TierRoot is the directory of the mount points of the tiers.
const TierRoot = "/var/nida"

// Usage is the capacity of one file system.
type Usage struct {
	TotalBytes uint64 `json:"total_bytes"`
	UsedBytes  uint64 `json:"used_bytes"`
	FreeBytes  uint64 `json:"free_bytes"`
}

// ErrNoStatfs tells that the platform cannot measure a file system.
var ErrNoStatfs = errors.New("the platform cannot measure a file system")

// Collector reads the state of the computer.
type Collector struct {
	// Root is the start of all paths. It is "/" on NidaOS.
	Root string
	// PlanFile is the path of the disk plan, from Root.
	PlanFile string
	// Statfs measures the file system at an absolute path of the host.
	Statfs func(path string) (Usage, error)
	// Now gives the time of each result.
	Now func() time.Time
}

// NewCollector makes a collector for the files under root.
func NewCollector(root, planFile string) *Collector {
	if root == "" {
		root = "/"
	}
	if planFile == "" {
		planFile = DefaultPlanFile
	}
	return &Collector{Root: root, PlanFile: planFile, Statfs: statfs, Now: time.Now}
}

// State is the full state of the computer in one result.
type State struct {
	Time   time.Time `json:"time"`
	System System    `json:"system"`
	Disks  []Disk    `json:"disks"`
	Tiers  []Tier    `json:"tiers"`
}

// State reads the host, the disks and the tiers.
func (c *Collector) State() (State, error) {
	sys, err := c.System()
	if err != nil {
		return State{}, err
	}
	disks, err := c.Disks()
	if err != nil {
		return State{}, err
	}
	tiers, err := c.Tiers()
	if err != nil {
		return State{}, err
	}
	return State{Time: c.Now().UTC(), System: sys, Disks: disks, Tiers: tiers}, nil
}
