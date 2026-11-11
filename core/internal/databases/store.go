package databases

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

// Errors of the store.
var (
	ErrNotFound = errors.New("the database does not exist")
	ErrExists   = errors.New("a database with this name exists")
	// ErrBusy: the controller works on the database now.
	ErrBusy = errors.New("an operation on the database is in progress")
)

// FieldError tells which field of a request is not valid.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Message }

func fieldErr(field, format string, args ...any) error {
	return &FieldError{Field: field, Message: fmt.Sprintf(format, args...)}
}

// Limits and defaults of a database.
const (
	MaxNameLength      = 32
	DefaultSizeBytes   = 1 << 30
	MinSizeBytes       = 64 << 20
	DefaultCPUMillis   = 100
	DefaultMemoryBytes = 256 << 20
	MinMemoryBytes     = 64 << 20
)

var nameRe = regexp.MustCompile(`^[a-z]([a-z0-9-]*[a-z0-9])?$`)

// ValidateName examines the name of a database.
func ValidateName(name string) error {
	if len(name) == 0 || len(name) > MaxNameLength || !nameRe.MatchString(name) {
		return fieldErr("name", "use 1 to %d characters: lowercase letters, digits and '-'. Start with a letter, and do not end with '-'", MaxNameLength)
	}
	return nil
}

// ValidateTier examines a storage tier.
func ValidateTier(tier string) error {
	if tier != TierPerformance && tier != TierStandard {
		return fieldErr("tier", "use performance or standard")
	}
	return nil
}

// Secret keys in the store.
const (
	secretOwner      = "owner"
	secretLinkPrefix = "link/"
)

type fileData struct {
	Databases []Database `json:"databases"`
	// Secrets has the passwords of each database. Only root can read the
	// file. The API never gives a password back.
	Secrets map[string]map[string]string `json:"secrets,omitempty"`
}

// Store keeps the databases in a JSON file (decision D22).
type Store struct {
	path string
	now  func() time.Time

	mu      sync.Mutex
	dbs     map[string]*Database
	secrets map[string]map[string]string
}

// OpenStore reads the databases file. A missing file is an empty store.
func OpenStore(path string) (*Store, error) {
	s := &Store{path: path, now: func() time.Time { return time.Now().UTC() },
		dbs: map[string]*Database{}, secrets: map[string]map[string]string{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var d fileData
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("the databases file %s is not valid: %w", path, err)
	}
	for i := range d.Databases {
		db := d.Databases[i]
		s.dbs[db.Name] = &db
	}
	for k, v := range d.Secrets {
		s.secrets[k] = v
	}
	return s, nil
}

func (s *Store) namesLocked() []string {
	names := make([]string, 0, len(s.dbs))
	for n := range s.dbs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (s *Store) save() error {
	d := fileData{Secrets: s.secrets}
	for _, n := range s.namesLocked() {
		d.Databases = append(d.Databases, *s.dbs[n])
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".databases-*.json")
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

// List gives all the databases, sorted by name.
func (s *Store) List() []Database {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Database, 0, len(s.dbs))
	for _, n := range s.namesLocked() {
		out = append(out, s.dbs[n].clone())
	}
	return out
}

// Get gives one database.
func (s *Store) Get(name string) (Database, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.dbs[name]
	if !ok {
		return Database{}, false
	}
	return d.clone(), true
}

// Change applies fn to a copy of the database and saves it. When fn or
// the save fails, the store does not change.
func (s *Store) Change(name string, fn func(d *Database) error) (Database, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.dbs[name]
	if !ok {
		return Database{}, ErrNotFound
	}
	next := cur.clone()
	if err := fn(&next); err != nil {
		return Database{}, err
	}
	next.Updated = s.now()
	s.dbs[name] = &next
	if err := s.save(); err != nil {
		s.dbs[name] = cur
		return Database{}, err
	}
	return next.clone(), nil
}

// Create adds a database in the state creating, with a new owner password.
// The caller gives the image of the version.
func (s *Store) Create(d Database) (Database, error) {
	if err := ValidateName(d.Name); err != nil {
		return Database{}, err
	}
	if err := ValidateTier(d.Tier); err != nil {
		return Database{}, err
	}
	if d.SizeBytes == 0 {
		d.SizeBytes = DefaultSizeBytes
	}
	if d.CPUMillis == 0 {
		d.CPUMillis = DefaultCPUMillis
	}
	if d.MemoryBytes == 0 {
		d.MemoryBytes = DefaultMemoryBytes
	}
	switch {
	case d.SizeBytes < MinSizeBytes:
		return Database{}, fieldErr("size_bytes", "use %d bytes or more", MinSizeBytes)
	case d.CPUMillis < 10 || d.CPUMillis > 64000:
		return Database{}, fieldErr("cpu_millis", "use a number from 10 to 64000")
	case d.MemoryBytes < MinMemoryBytes:
		return Database{}, fieldErr("memory_bytes", "use %d bytes or more", MinMemoryBytes)
	case d.Image == "":
		return Database{}, fieldErr("version", "NidaOS has no image for the version %d", d.Version)
	}
	password, err := NewPassword()
	if err != nil {
		return Database{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.dbs[d.Name]; ok {
		return Database{}, ErrExists
	}
	now := s.now()
	if d.State == "" {
		d.State = StateCreating
	}
	d.Reason, d.LAN, d.LANPort, d.Links, d.UpdateTo = "", false, 0, nil, ""
	d.Created, d.Updated = now, now
	s.dbs[d.Name] = &d
	s.secrets[d.Name] = map[string]string{secretOwner: password}
	if err := s.save(); err != nil {
		delete(s.dbs, d.Name)
		delete(s.secrets, d.Name)
		return Database{}, err
	}
	return d.clone(), nil
}

// Remove deletes a database and its passwords from the store.
func (s *Store) Remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.dbs[name]
	if !ok {
		return ErrNotFound
	}
	sec := s.secrets[name]
	delete(s.dbs, name)
	delete(s.secrets, name)
	if err := s.save(); err != nil {
		s.dbs[name] = cur
		s.secrets[name] = sec
		return err
	}
	return nil
}

// Secret gives one password of a database.
func (s *Store) Secret(db, key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.secrets[db][key]
}

// SetSecret writes one password of a database.
func (s *Store) SetSecret(db, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.dbs[db]; !ok {
		return ErrNotFound
	}
	old, had := s.secrets[db][key]
	if s.secrets[db] == nil {
		s.secrets[db] = map[string]string{}
	}
	s.secrets[db][key] = value
	if err := s.save(); err != nil {
		if had {
			s.secrets[db][key] = old
		} else {
			delete(s.secrets[db], key)
		}
		return err
	}
	return nil
}

// AllSecrets gives all the passwords. The psql runner removes them from
// the errors.
func (s *Store) AllSecrets() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, m := range s.secrets {
		for _, v := range m {
			out = append(out, v)
		}
	}
	return out
}

// OwnerPassword gives the password of <name>_owner.
func (s *Store) OwnerPassword(db string) string { return s.Secret(db, secretOwner) }

// SetState writes the state and the reason of a database.
func (s *Store) SetState(name, state, reason string) error {
	_, err := s.Change(name, func(d *Database) error {
		d.State, d.Reason = state, reason
		return nil
	})
	return err
}
