package apps

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"
)

// Errors of the store.
var (
	ErrNotFound = errors.New("the app does not exist")
	ErrExists   = errors.New("an app with this name exists")
	// ErrBusy: a deploy of the app is in progress.
	ErrBusy = errors.New("a deploy of the app is in progress")
	// ErrNoVersion: the app has no version in operation.
	ErrNoVersion = errors.New("the app has no version in operation")
)

// fileData is the content of the apps file.
type fileData struct {
	Apps []App `json:"apps"`
	// Secrets has the secret values of each app. Only root can read the
	// file. The API never gives a value back.
	Secrets map[string]map[string]string `json:"secrets,omitempty"`
}

// Store keeps the apps in a JSON file (decision D22). Each change writes
// the full file again, with a rename, thus a stop of the computer cannot
// leave a half file.
type Store struct {
	path string
	now  func() time.Time

	mu      sync.Mutex
	apps    map[string]*App
	secrets map[string]map[string]string
}

// OpenStore reads the apps file. A missing file is an empty store.
func OpenStore(path string) (*Store, error) {
	s := &Store{path: path, now: func() time.Time { return time.Now().UTC() },
		apps: map[string]*App{}, secrets: map[string]map[string]string{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var d fileData
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("the apps file %s is not valid: %w", path, err)
	}
	for i := range d.Apps {
		a := d.Apps[i]
		s.apps[a.Name] = &a
	}
	for k, v := range d.Secrets {
		s.secrets[k] = v
	}
	return s, nil
}

// save writes the file. The caller holds s.mu.
func (s *Store) save() error {
	d := fileData{Secrets: s.secrets}
	for _, name := range s.namesLocked() {
		d.Apps = append(d.Apps, *s.apps[name])
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".apps-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

func (s *Store) namesLocked() []string {
	names := make([]string, 0, len(s.apps))
	for n := range s.apps {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// change applies fn to a copy of the app and saves it. When fn or the
// save fails, the store does not change.
func (s *Store) change(name string, fn func(a *App) error) (App, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.apps[name]
	if !ok {
		return App{}, ErrNotFound
	}
	next := cur.clone()
	if err := fn(&next); err != nil {
		return App{}, err
	}
	next.Updated = s.now()
	s.apps[name] = &next
	if err := s.save(); err != nil {
		s.apps[name] = cur
		return App{}, err
	}
	return next.clone(), nil
}

// Apps gives all the apps, sorted by name.
func (s *Store) Apps() []App {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]App, 0, len(s.apps))
	for _, n := range s.namesLocked() {
		out = append(out, s.apps[n].clone())
	}
	return out
}

// Get gives one app.
func (s *Store) Get(name string) (App, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.apps[name]
	if !ok {
		return App{}, false
	}
	return a.clone(), true
}

// Create adds an app. The app has no version: the first deploy makes
// version 1.
func (s *Store) Create(a App) (App, error) {
	a.Active, a.Versions = 0, nil
	if err := validateApp(a); err != nil {
		return App{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.apps[a.Name]; ok {
		return App{}, ErrExists
	}
	now := s.now()
	a.Created, a.Updated = now, now
	a = a.clone()
	s.apps[a.Name] = &a
	if err := s.save(); err != nil {
		delete(s.apps, a.Name)
		return App{}, err
	}
	return a.clone(), nil
}

// Change is a change of the settings of an app. A nil field does not
// change.
type Change struct {
	Repository *string
	Branch     *string
	Config     *Config
	Copies     *int
	// AddFolders adds new folders. A change cannot remove a folder or
	// change it: the move to a different tier is a separate operation.
	AddFolders []Folder
}

// Update applies a change. When the config changes and the app has a
// version in operation, Update makes a new version with the same image
// and the new config. The result tells the new version, or 0.
func (s *Store) Update(name string, ch Change) (App, int, error) {
	var started int
	a, err := s.change(name, func(a *App) error {
		if ch.Repository != nil {
			a.Repository = *ch.Repository
		}
		if ch.Branch != nil {
			a.Branch = *ch.Branch
		}
		if ch.Copies != nil {
			a.Copies = *ch.Copies
		}
		for _, f := range ch.AddFolders {
			if _, ok := a.Folder(f.Name); ok {
				return fieldErr("folders.name", "the folder %q exists", f.Name)
			}
			a.Folders = append(a.Folders, f)
		}
		configChanged := false
		if ch.Config != nil {
			a.Config = ch.Config.clone()
			configChanged = true
		}
		if err := validateApp(*a); err != nil {
			return err
		}
		// A new folder needs a new Deployment, the same as a new config.
		if (configChanged || len(ch.AddFolders) > 0) && a.Active != 0 {
			if _, busy := a.Deploying(); busy {
				return ErrBusy
			}
			active, _ := a.Version(a.Active)
			started = s.addVersion(a, Version{Image: active.Image, Commit: active.Commit, Summary: "settings change", Config: a.Config.clone()})
		}
		return nil
	})
	return a, started, err
}

// addVersion adds a version in the state deploying. It gives the number.
func (s *Store) addVersion(a *App, v Version) int {
	n := 1
	if len(a.Versions) > 0 {
		n = a.Versions[len(a.Versions)-1].Number + 1
	}
	v.Number, v.State, v.Created = n, StateDeploying, s.now()
	a.Versions = append(a.Versions, v)
	prune(a)
	return n
}

// KeepVersions is the number of versions that an app keeps.
const KeepVersions = 5

// prune keeps the newest KeepVersions versions. It also keeps the version
// in operation, if it is older.
func prune(a *App) {
	if len(a.Versions) <= KeepVersions {
		return
	}
	first := len(a.Versions) - KeepVersions
	kept := make([]Version, 0, KeepVersions+1)
	for i, v := range a.Versions {
		if i >= first || v.Number == a.Active {
			kept = append(kept, v)
		}
	}
	a.Versions = kept
}

// ErrNoRollback: the version cannot be the target of a rollback.
var ErrNoRollback = errors.New("only a version that was in operation can be the target of a rollback")

// Rollback adds a new version that copies the image and the config of an
// old version. The controller deploys it as each other version.
func (s *Store) Rollback(name string, number int) (Version, error) {
	var v Version
	_, err := s.change(name, func(a *App) error {
		if _, busy := a.Deploying(); busy {
			return ErrBusy
		}
		old, ok := a.Version(number)
		if !ok {
			return fmt.Errorf("%w: the app keeps only the last %d versions", ErrVersionNotFound, KeepVersions)
		}
		if old.Number == a.Active {
			return fmt.Errorf("%w: the version %d is in operation now", ErrNoRollback, number)
		}
		if old.State != StateReplaced {
			return ErrNoRollback
		}
		n := s.addVersion(a, Version{Image: old.Image, Commit: old.Commit, RollbackOf: old.Number,
			Summary: fmt.Sprintf("rollback to version %d", old.Number), Config: old.Config.clone()})
		v, _ = a.Version(n)
		return nil
	})
	return v, err
}

// ErrVersionNotFound: the version does not exist.
var ErrVersionNotFound = errors.New("the version does not exist")

// Deploy adds a new version of the image. The controller deploys it.
func (s *Store) Deploy(name, image, commit, summary string) (Version, error) {
	if err := ValidateImage(image); err != nil {
		return Version{}, err
	}
	if len(commit) > 64 || len(summary) > 500 {
		return Version{}, fieldErr("summary", "the commit or the summary is too long")
	}
	var v Version
	_, err := s.change(name, func(a *App) error {
		if _, busy := a.Deploying(); busy {
			return ErrBusy
		}
		n := s.addVersion(a, Version{Image: image, Commit: commit, Summary: summary, Config: a.Config.clone()})
		v, _ = a.Version(n)
		return nil
	})
	return v, err
}

// Finish writes the result of a deploy (the Source interface).
func (s *Store) Finish(name string, number int, state, reason string) error {
	_, err := s.change(name, func(a *App) error {
		i := slices.IndexFunc(a.Versions, func(v Version) bool { return v.Number == number })
		if i < 0 {
			return fmt.Errorf("the version %d of %s does not exist", number, name)
		}
		now := s.now()
		a.Versions[i].State, a.Versions[i].Reason, a.Versions[i].Finished = state, reason, &now
		if state == StateServing {
			for j := range a.Versions {
				if a.Versions[j].Number == a.Active && j != i {
					a.Versions[j].State = StateReplaced
				}
			}
			a.Active = number
		}
		return nil
	})
	return err
}

// Remove deletes an app and its secrets from the store.
func (s *Store) Remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.apps[name]
	if !ok {
		return ErrNotFound
	}
	sec := s.secrets[name]
	delete(s.apps, name)
	delete(s.secrets, name)
	if err := s.save(); err != nil {
		s.apps[name] = cur
		if sec != nil {
			s.secrets[name] = sec
		}
		return err
	}
	return nil
}

// Secrets gives the secret values of an app (the Source interface).
func (s *Store) Secrets(name string) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.secrets[name]))
	for k, v := range s.secrets[name] {
		out[k] = v
	}
	return out
}

// SecretNames gives the names of the secrets of an app, sorted.
func (s *Store) SecretNames(name string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.secrets[name]))
	for k := range s.secrets[name] {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// SetSecret adds or replaces a secret. The value goes to the copies at the
// next deploy.
func (s *Store) SetSecret(name, key, value string) error {
	if err := ValidateSecret(key, value); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.apps[name]
	if !ok {
		return ErrNotFound
	}
	if _, isVar := a.Config.Variables[key]; isVar {
		return fieldErr("name", "a variable has the name %q: use a different name, or remove the variable", key)
	}
	old, had := s.secrets[name][key]
	if s.secrets[name] == nil {
		s.secrets[name] = map[string]string{}
	}
	s.secrets[name][key] = value
	if err := s.save(); err != nil {
		if had {
			s.secrets[name][key] = old
		} else {
			delete(s.secrets[name], key)
		}
		return err
	}
	return nil
}

// ErrSecretNotFound: the secret does not exist.
var ErrSecretNotFound = errors.New("the secret does not exist")

// DeleteSecret removes a secret.
func (s *Store) DeleteSecret(name, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.apps[name]; !ok {
		return ErrNotFound
	}
	old, ok := s.secrets[name][key]
	if !ok {
		return ErrSecretNotFound
	}
	delete(s.secrets[name], key)
	if err := s.save(); err != nil {
		s.secrets[name][key] = old
		return err
	}
	return nil
}
