// Package runners connects the apps of NidaOS to private GitHub
// repositories. For each queued deploy job, it starts a new runner pod with
// a just-in-time registration (decision D26). The pod builds the image with
// rootless BuildKit and deploys it through the job endpoint of nida-core.
package runners

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/github"
)

// Errors of the store.
var (
	ErrNotConnected = errors.New("the app has no connected repository")
	ErrConnected    = errors.New("the app has a connected repository: disconnect it first")
	ErrRepoInUse    = errors.New("an other app uses this repository")
	ErrJobNotFound  = errors.New("the job does not exist")
	ErrSetupExpired = errors.New("the setup of the GitHub App is not valid: start it again")
)

// FieldError tells which field of a request is not valid.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Message }

var (
	ownerRe  = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,38})$`)
	repoRe   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	branchRe = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)
)

// ParseRepo reads "owner/name".
func ParseRepo(full string) (owner, name string, err error) {
	owner, name, ok := strings.Cut(strings.TrimSpace(full), "/")
	if !ok || !ownerRe.MatchString(owner) || !repoRe.MatchString(name) || name == "." || name == ".." {
		return "", "", &FieldError{Field: "repository", Message: "use owner/name, for example me/shop"}
	}
	return owner, name, nil
}

// ValidateBranch examines the name of a branch.
func ValidateBranch(b string) error {
	if !branchRe.MatchString(b) || strings.Contains(b, "..") || strings.HasPrefix(b, "/") || strings.HasSuffix(b, "/") {
		return &FieldError{Field: "branch", Message: "the name of the branch is not valid"}
	}
	return nil
}

// Connection is the link of an app to a GitHub repository.
type Connection struct {
	App     string    `json:"app"`
	Owner   string    `json:"owner"`
	Repo    string    `json:"repo"`
	Branch  string    `json:"branch"`
	Created time.Time `json:"created"`
	// PullRequest is the address of the pull request with the workflow.
	PullRequest string `json:"pull_request,omitempty"`
	// Problem is the last error of the dispatcher for this repository.
	Problem string `json:"problem,omitempty"`
}

// FullName is "owner/repo".
func (c Connection) FullName() string { return c.Owner + "/" + c.Repo }

// Job states.
const (
	JobQueued    = "queued"    // a pod waits for the job
	JobRunning   = "running"   // the runner of the pod runs the job
	JobSucceeded = "succeeded" // the job passed
	JobFailed    = "failed"    // the job or the pod failed
	JobCancelled = "cancelled" // the job was cancelled on GitHub
)

// Job is one deploy job of an app.
type Job struct {
	ID       int64      `json:"id"` // the ID of the job on GitHub
	App      string     `json:"app"`
	RunID    int64      `json:"run_id"`
	Name     string     `json:"name"`
	SHA      string     `json:"sha"`
	URL      string     `json:"url"`
	State    string     `json:"state"`
	Pod      string     `json:"pod,omitempty"`
	RunnerID int64      `json:"runner_id,omitempty"`
	Runner   string     `json:"runner,omitempty"`
	Created  time.Time  `json:"created"`
	Started  *time.Time `json:"started,omitempty"`
	Finished *time.Time `json:"finished,omitempty"`
	Message  string     `json:"message,omitempty"`
	HasLog   bool       `json:"has_log"`
}

// Final tells if the job does not change any more.
func (j Job) Final() bool {
	return j.State == JobSucceeded || j.State == JobFailed || j.State == JobCancelled
}

// Setup is a manifest flow that waits for GitHub.
type Setup struct {
	Key     string    `json:"key"`
	Expires time.Time `json:"expires"`
}

type fileData struct {
	// App is the GitHub App of NidaOS (decision D25). Only root can read the
	// file, because it has the private key.
	App         *github.App  `json:"app,omitempty"`
	Setup       *Setup       `json:"setup,omitempty"`
	Connections []Connection `json:"connections"`
	Jobs        []Job        `json:"jobs"`
}

// KeepJobs is the number of jobs that stay for each app.
const KeepJobs = 20

// Store keeps the GitHub App, the connections and the jobs in a JSON file
// (decision D22). The job logs are files in the folder logs next to it.
type Store struct {
	path string
	now  func() time.Time

	mu   sync.Mutex
	data fileData
}

// OpenStore reads the file at path, or starts with no data.
func OpenStore(path string) (*Store, error) {
	s := &Store{path: path, now: time.Now}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, &s.data); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	if err := os.MkdirAll(s.logDir(), 0o700); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) logDir() string { return filepath.Join(filepath.Dir(s.path), "runner-logs") }

// save writes the file. The caller holds the lock.
func (s *Store) save() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// App gives the GitHub App.
func (s *Store) App() (github.App, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.App == nil {
		return github.App{}, false
	}
	return *s.data.App, true
}

// SetApp keeps the GitHub App and ends the setup.
func (s *Store) SetApp(a github.App) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.App, s.data.Setup = &a, nil
	return s.save()
}

// RemoveApp removes the GitHub App. The connections must be removed first.
func (s *Store) RemoveApp() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.data.Connections) > 0 {
		return ErrConnected
	}
	s.data.App = nil
	return s.save()
}

// StartSetup keeps the key of a new manifest flow. The key is valid for
// 30 minutes.
func (s *Store) StartSetup(key string) (Setup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Setup{Key: key, Expires: s.now().Add(30 * time.Minute).UTC()}
	s.data.Setup = &st
	return st, s.save()
}

// CheckSetup tells if key is the key of the setup that waits.
func (s *Store) CheckSetup(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.data.Setup
	if st == nil || key == "" || subtle.ConstantTimeCompare([]byte(key), []byte(st.Key)) != 1 || s.now().After(st.Expires) {
		return ErrSetupExpired
	}
	return nil
}

// Connections gives the connections, sorted by app.
func (s *Store) Connections() []Connection {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]Connection(nil), s.data.Connections...)
	sort.Slice(out, func(i, j int) bool { return out[i].App < out[j].App })
	return out
}

// Connection gives the connection of an app.
func (s *Store) Connection(app string) (Connection, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.data.Connections {
		if c.App == app {
			return c, true
		}
	}
	return Connection{}, false
}

// Connect adds a connection.
func (s *Store) Connect(c Connection) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, o := range s.data.Connections {
		if o.App == c.App {
			return ErrConnected
		}
		if strings.EqualFold(o.FullName(), c.FullName()) {
			return ErrRepoInUse
		}
	}
	c.Created = s.now().UTC()
	s.data.Connections = append(s.data.Connections, c)
	return s.save()
}

// UpdateConnection changes the connection of an app with f.
func (s *Store) UpdateConnection(app string, f func(*Connection)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Connections {
		if s.data.Connections[i].App == app {
			before := s.data.Connections[i]
			f(&s.data.Connections[i])
			if s.data.Connections[i] == before {
				return nil
			}
			return s.save()
		}
	}
	return ErrNotConnected
}

// Disconnect removes the connection of an app. The jobs stay.
func (s *Store) Disconnect(app string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.data.Connections {
		if c.App == app {
			s.data.Connections = append(s.data.Connections[:i], s.data.Connections[i+1:]...)
			return s.save()
		}
	}
	return ErrNotConnected
}

// Jobs gives the jobs of an app, the newest first. An empty app gives all
// the jobs.
func (s *Store) Jobs(app string) []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Job
	for _, j := range s.data.Jobs {
		if app == "" || j.App == app {
			out = append(out, j)
		}
	}
	sort.Slice(out, func(i, k int) bool {
		if !out[i].Created.Equal(out[k].Created) {
			return out[i].Created.After(out[k].Created)
		}
		return out[i].ID > out[k].ID
	})
	return out
}

// Job gives one job.
func (s *Store) Job(id int64) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.data.Jobs {
		if j.ID == id {
			return j, true
		}
	}
	return Job{}, false
}

// AddJob keeps a new job. It removes the oldest final jobs of the app over
// KeepJobs, with their logs.
func (s *Store) AddJob(j Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, o := range s.data.Jobs {
		if o.ID == j.ID {
			return nil
		}
	}
	if j.Created.IsZero() {
		j.Created = s.now().UTC()
	}
	s.data.Jobs = append(s.data.Jobs, j)
	s.prune(j.App)
	return s.save()
}

// prune removes the old final jobs of app. The caller holds the lock.
func (s *Store) prune(app string) {
	var idx []int
	for i, j := range s.data.Jobs {
		if j.App == app {
			idx = append(idx, i)
		}
	}
	if len(idx) <= KeepJobs {
		return
	}
	sort.Slice(idx, func(a, b int) bool { return s.data.Jobs[idx[a]].Created.Before(s.data.Jobs[idx[b]].Created) })
	drop := map[int]bool{}
	for _, i := range idx[:len(idx)-KeepJobs] {
		if s.data.Jobs[i].Final() {
			drop[i] = true
			_ = os.Remove(s.LogPath(s.data.Jobs[i].ID))
		}
	}
	kept := s.data.Jobs[:0]
	for i, j := range s.data.Jobs {
		if !drop[i] {
			kept = append(kept, j)
		}
	}
	s.data.Jobs = kept
}

// UpdateJob changes a job with f.
func (s *Store) UpdateJob(id int64, f func(*Job)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Jobs {
		if s.data.Jobs[i].ID == id {
			f(&s.data.Jobs[i])
			return s.save()
		}
	}
	return ErrJobNotFound
}

// LogPath is the file of the log of a job.
func (s *Store) LogPath(id int64) string {
	return filepath.Join(s.logDir(), fmt.Sprintf("%d.log", id))
}

// WriteLog keeps the log of a job with the mode 0600.
func (s *Store) WriteLog(id int64, log []byte) error {
	if err := os.WriteFile(s.LogPath(id), log, 0o600); err != nil {
		return err
	}
	return s.UpdateJob(id, func(j *Job) { j.HasLog = true })
}
