// Package apps keeps the apps of the owner in operation on k3s (milestone
// M-03).
//
// The parts come from the app controller of GitStack core (decision D4).
// NidaOS has one owner. Thus the parts for the repositories, the teams, the
// gateway sign-in, the public domains and the managed databases are not here.
//
//   - model.go: the data of an app and of its versions.
//   - store.go: the JSON file that keeps the apps.
//   - manifests.go: the Kubernetes objects of an app.
//   - controller.go: the deploy, the health check and the return to the
//     version before.
package apps

import (
	"maps"
	"time"
)

// The states of a version.
const (
	// StateDeploying: the controller applies the version and waits for the
	// health check.
	StateDeploying = "deploying"
	// StateServing: the version is in operation.
	StateServing = "serving"
	// StateFailed: the version did not pass the health check. The version
	// before stays in operation.
	StateFailed = "failed"
	// StateReplaced: the version was in operation, and a newer version
	// replaced it. A rollback can go back to it.
	StateReplaced = "replaced"
)

// The storage tiers of the folders (decision D7).
const (
	TierPerformance = "performance"
	TierStandard    = "standard"
)

// HealthCheck tells how the controller examines a copy of the app.
type HealthCheck struct {
	// Path is the HTTP path. A response from 200 to 399 is healthy.
	Path string `json:"path"`
	// IntervalSeconds is the time between two checks. 0 gives 5 seconds.
	IntervalSeconds int `json:"interval_seconds,omitempty"`
	// TimeoutSeconds is the maximum time of one check. 0 gives 2 seconds.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// Limits is the maximum processor and memory of one copy. A zero value is
// no limit.
type Limits struct {
	CPUMillis   int   `json:"cpu_millis,omitempty"`
	MemoryBytes int64 `json:"memory_bytes,omitempty"`
}

// Config is the part of an app that each version keeps. A change of the
// config makes a new version, thus a rollback also goes back to the old
// config.
type Config struct {
	// Port is the TCP port where the app listens for HTTP. Nil: the app has
	// no address and no health check on the network (for example a worker).
	Port *int `json:"port,omitempty"`
	// HealthCheck is nil: the controller only examines that the port is
	// open.
	HealthCheck *HealthCheck      `json:"health_check,omitempty"`
	Variables   map[string]string `json:"variables,omitempty"`
	Limits      Limits            `json:"limits"`
}

// Folder is a folder of the app that stays after a deploy. All the other
// files of the app go away at each deploy.
type Folder struct {
	// Name identifies the folder in the API, for example "data".
	Name string `json:"name"`
	// Path is the folder in the container, for example "/var/lib/app".
	Path string `json:"path"`
	// Tier is performance or standard.
	Tier      string `json:"tier"`
	SizeBytes int64  `json:"size_bytes"`
	// MovingTo is the target tier of a move in progress.
	MovingTo string `json:"moving_to,omitempty"`
	// MoveError tells why the last move failed.
	MoveError string `json:"move_error,omitempty"`
}

// Version is one deploy of the app.
type Version struct {
	Number int `json:"number"`
	// Image is the image reference in the local registry (decision D21).
	Image   string `json:"image"`
	Commit  string `json:"commit,omitempty"`
	Summary string `json:"summary,omitempty"`
	// RollbackOf is the number of the old version that this version
	// copies. 0: not a rollback.
	RollbackOf int    `json:"rollback_of,omitempty"`
	Config     Config `json:"config"`
	State      string `json:"state"`
	// Reason tells why the version failed.
	Reason   string     `json:"reason,omitempty"`
	Created  time.Time  `json:"created"`
	Finished *time.Time `json:"finished,omitempty"`
}

// App is one app of the owner.
type App struct {
	Name string `json:"name"`
	// Repository is the GitHub repository, for example "owner/blog". M-05
	// builds the images from it.
	Repository string `json:"repository,omitempty"`
	Branch     string `json:"branch,omitempty"`
	// Config is the config of the next version.
	Config Config `json:"config"`
	// Copies is the number of copies in operation. 0 stops the app. An app
	// with folders has 1 copy at most.
	Copies  int      `json:"copies"`
	Folders []Folder `json:"folders,omitempty"`
	// Active is the number of the version in operation. 0: no version
	// passed the health check.
	Active int `json:"active"`
	// Versions has the newest version last.
	Versions []Version `json:"versions,omitempty"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
}

// Version gives the version with the number n.
func (a *App) Version(n int) (Version, bool) {
	for _, v := range a.Versions {
		if v.Number == n {
			return v, true
		}
	}
	return Version{}, false
}

// Deploying gives the version that the controller must deploy.
func (a *App) Deploying() (Version, bool) {
	for _, v := range a.Versions {
		if v.State == StateDeploying {
			return v, true
		}
	}
	return Version{}, false
}

// Moving gives the folder that the controller must move.
func (a *App) Moving() (Folder, bool) {
	for _, f := range a.Folders {
		if f.MovingTo != "" {
			return f, true
		}
	}
	return Folder{}, false
}

// Folder gives the folder with the name.
func (a *App) Folder(name string) (Folder, bool) {
	for _, f := range a.Folders {
		if f.Name == name {
			return f, true
		}
	}
	return Folder{}, false
}

// clone gives a deep copy, thus a caller cannot change the data of the
// store.
func (a App) clone() App {
	a.Config = a.Config.clone()
	a.Folders = append([]Folder(nil), a.Folders...)
	vs := make([]Version, len(a.Versions))
	for i, v := range a.Versions {
		v.Config = v.Config.clone()
		vs[i] = v
	}
	if a.Versions == nil {
		vs = nil
	}
	a.Versions = vs
	return a
}

func (c Config) clone() Config {
	if c.Port != nil {
		p := *c.Port
		c.Port = &p
	}
	if c.HealthCheck != nil {
		h := *c.HealthCheck
		c.HealthCheck = &h
	}
	if c.Variables != nil {
		m := make(map[string]string, len(c.Variables))
		maps.Copy(m, c.Variables)
		c.Variables = m
	}
	return c
}
