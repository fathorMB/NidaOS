package system

import "fmt"

// Tier states.
const (
	// TierReady: the file system of the tier is mounted.
	TierReady = "ready"
	// TierNotMounted: the plan gives a disk, but the file system is not mounted.
	TierNotMounted = "not_mounted"
	// TierNoDisk: the plan gives no disk for the tier.
	TierNoDisk = "no_disk"
)

// Tier is one storage tier for the workloads (decision D7).
type Tier struct {
	Name         string `json:"name"`
	StorageClass string `json:"storage_class"`
	Path         string `json:"path"`
	// Disk is the disk of the tier in the plan.
	Disk string `json:"disk"`
	// Device is the partition that is mounted on Path.
	Device string `json:"device,omitempty"`
	FSType string `json:"fs_type,omitempty"`
	// State is ready, not_mounted or no_disk.
	State string `json:"state"`
	// Usage is empty when the tier is not ready, or when the platform
	// cannot measure it.
	Usage *Usage `json:"usage,omitempty"`
}

// Tiers reads the two storage tiers.
func (c *Collector) Tiers() ([]Tier, error) {
	plan, err := c.plan()
	if err != nil {
		return nil, err
	}
	mounts := c.mounts()

	tiers := make([]Tier, 0, 2)
	for _, t := range []struct{ name, disk string }{
		{TierPerformance, plan.performance},
		{TierStandard, plan.standard},
	} {
		tier := Tier{
			Name:         t.name,
			StorageClass: "nida-" + t.name,
			Path:         TierRoot + "/" + t.name,
			Disk:         t.disk,
		}
		m, mounted := mounts["@"+tier.Path]
		switch {
		case mounted:
			tier.State = TierReady
			tier.Device, tier.FSType = m.device, m.fsType
			if c.Statfs != nil {
				u, err := c.Statfs(c.path(tier.Path))
				if err == nil {
					tier.Usage = &u
				} else if err != ErrNoStatfs {
					return nil, fmt.Errorf("tier %s: %w", t.name, err)
				}
			}
		case t.disk == "":
			tier.State = TierNoDisk
		default:
			tier.State = TierNotMounted
		}
		tiers = append(tiers, tier)
	}
	return tiers, nil
}
