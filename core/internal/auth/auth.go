// Package auth keeps the owner password, the sign-in sessions and the
// tokens of the programs (decisions D16 and D17).
//
// The store keeps everything in one JSON file that only root can read.
// It keeps only hashes: the slow hash of the password, and the SHA-256
// hash of each session secret and each token. A copy of the file does
// not give access.
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Levels of access.
const (
	// LevelRead gives the state, the metrics and the logs.
	LevelRead = "read"
	// LevelAdmin gives all the operations.
	LevelAdmin = "admin"
)

// ValidLevel tells if l is a level.
func ValidLevel(l string) bool { return l == LevelRead || l == LevelAdmin }

// Allows tells if an identity with level have can do an operation that
// needs the level need.
func Allows(have, need string) bool {
	return have == LevelAdmin || (have == LevelRead && need == LevelRead)
}

// Kinds of identity.
const (
	KindOwner = "owner" // a browser session of the owner
	KindToken = "token" // a program with a token
	KindLocal = "local" // a request through the local socket (D18)
)

// Identity is the caller of a request.
type Identity struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Level string `json:"level"`
	// TokenID is the id of the token, for KindToken.
	TokenID string `json:"token_id,omitempty"`
}

// Parameters of the password hash. 600 000 iterations of PBKDF2 with
// SHA-256 is the value that OWASP gives for 2023 and later.
var pbkdf2Iterations = 600_000 // a variable: the tests use fewer iterations

const (
	saltBytes = 16
	keyBytes  = 32
	// MinPasswordLength is the minimum number of characters.
	MinPasswordLength = 12
	maxPasswordLength = 1024
)

// Times of the sessions.
const (
	// SessionLifetime is the maximum age of a session.
	SessionLifetime = 30 * 24 * time.Hour
	// SessionIdle closes a session that is not used for this time.
	SessionIdle = 7 * 24 * time.Hour
	// touchInterval limits the writes of the file for the "last used" times.
	touchInterval = time.Minute
)

// Prefixes of the secrets. A prefix makes a leaked token easy to find,
// for example by a secret scanner.
const (
	tokenPrefix   = "nida_"
	sessionPrefix = "nidas_"
)

// Errors of the store.
var (
	ErrOwnerNotSet   = errors.New("the owner password is not set")
	ErrWrongPassword = errors.New("the password is not correct")
	ErrWeakPassword  = fmt.Errorf("the password must have %d characters or more", MinPasswordLength)
	ErrNoAccess      = errors.New("the session or the token is not valid")
	ErrNotFound      = errors.New("the token does not exist")
	ErrBadName       = errors.New("the name must have 1 to 64 characters: letters, digits, '-', '_', '.' or ' '")
	ErrBadLevel      = errors.New("the level must be read or admin")
)

// passwordHash is the stored form of the password.
type passwordHash struct {
	Algorithm  string    `json:"algorithm"`
	Iterations int       `json:"iterations"`
	Salt       string    `json:"salt"`
	Key        string    `json:"key"`
	Changed    time.Time `json:"changed"`
}

type session struct {
	Hash     string    `json:"hash"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"last_used"`
}

// Token is the public data of a token. The secret is never in it.
type Token struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Level string `json:"level"`
	// Hint is the start of the token, to find it in a list.
	Hint     string     `json:"hint"`
	Created  time.Time  `json:"created"`
	LastUsed *time.Time `json:"last_used,omitempty"`
}

type storedToken struct {
	Token
	Hash string `json:"hash"`
}

// file is the content of the store file.
type file struct {
	Owner    *passwordHash `json:"owner,omitempty"`
	Sessions []session     `json:"sessions"`
	Tokens   []storedToken `json:"tokens"`
}

// Store keeps the data of the sign-in in a file.
type Store struct {
	path string
	Now  func() time.Time

	mu      sync.Mutex
	data    file
	touched time.Time
	dirty   bool

	// verify limits the number of password checks that run at the same
	// time: each check uses the processor for some hundred milliseconds.
	verify chan struct{}
}

// Open reads the store file. A missing file gives an empty store.
func Open(path string) (*Store, error) {
	s := &Store{path: path, Now: time.Now, verify: make(chan struct{}, 2)}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("auth store: %w", err)
	default:
		if err := json.Unmarshal(b, &s.data); err != nil {
			return nil, fmt.Errorf("auth store %s: %w", path, err)
		}
	}
	return s, nil
}

// save writes the file: a new file, then a rename. The caller holds s.mu.
func (s *Store) save() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("auth store: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("auth store: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("auth store: %w", err)
	}
	s.dirty = false
	return nil
}

// OwnerSet tells if the owner has a password.
func (s *Store) OwnerSet() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Owner != nil
}

// SetPassword sets the owner password. It closes all the sessions.
func (s *Store) SetPassword(password string) error {
	if err := checkPassword(password); err != nil {
		return err
	}
	salt := make([]byte, saltBytes)
	rand.Read(salt)
	key, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, keyBytes)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Owner = &passwordHash{
		Algorithm:  "pbkdf2-sha256",
		Iterations: pbkdf2Iterations,
		Salt:       hex.EncodeToString(salt),
		Key:        hex.EncodeToString(key),
		Changed:    s.Now().UTC(),
	}
	s.data.Sessions = nil
	return s.save()
}

func checkPassword(p string) error {
	if utf8.RuneCountInString(p) < MinPasswordLength || len(p) > maxPasswordLength {
		return ErrWeakPassword
	}
	return nil
}

// CheckPassword compares password with the owner password.
func (s *Store) CheckPassword(password string) error {
	s.mu.Lock()
	owner := s.data.Owner
	s.mu.Unlock()
	if owner == nil {
		return ErrOwnerNotSet
	}
	if len(password) > maxPasswordLength {
		return ErrWrongPassword
	}
	salt, err1 := hex.DecodeString(owner.Salt)
	want, err2 := hex.DecodeString(owner.Key)
	if err1 != nil || err2 != nil || owner.Iterations < 1 {
		return fmt.Errorf("auth store: the password hash is damaged")
	}
	s.verify <- struct{}{}
	got, err := pbkdf2.Key(sha256.New, password, salt, owner.Iterations, len(want))
	<-s.verify
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrWrongPassword
	}
	return nil
}

// SignIn checks the password and makes a session. It gives the session
// secret for the cookie and the time when the session ends.
func (s *Store) SignIn(password string) (secret string, expires time.Time, err error) {
	if err := s.CheckPassword(password); err != nil {
		return "", time.Time{}, err
	}
	secret = sessionPrefix + randomSecret()
	now := s.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneSessions(now)
	s.data.Sessions = append(s.data.Sessions, session{Hash: hashSecret(secret), Created: now, LastUsed: now})
	if err := s.save(); err != nil {
		return "", time.Time{}, err
	}
	return secret, now.Add(SessionLifetime), nil
}

// SignOut closes the session of secret. An unknown secret is not an error.
func (s *Store) SignOut(secret string) error {
	h := hashSecret(secret)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, ss := range s.data.Sessions {
		if ss.Hash == h {
			s.data.Sessions = append(s.data.Sessions[:i], s.data.Sessions[i+1:]...)
			return s.save()
		}
	}
	return nil
}

// pruneSessions removes the old sessions. The caller holds s.mu.
func (s *Store) pruneSessions(now time.Time) {
	keep := s.data.Sessions[:0]
	for _, ss := range s.data.Sessions {
		if now.Sub(ss.Created) < SessionLifetime && now.Sub(ss.LastUsed) < SessionIdle {
			keep = append(keep, ss)
		}
	}
	s.data.Sessions = keep
}

// Authenticate finds the identity of a session secret or a token.
func (s *Store) Authenticate(secret string) (Identity, error) {
	if secret == "" {
		return Identity{}, ErrNoAccess
	}
	h := hashSecret(secret)
	now := s.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.touch(now)

	if strings.HasPrefix(secret, sessionPrefix) {
		for i := range s.data.Sessions {
			ss := &s.data.Sessions[i]
			if subtle.ConstantTimeCompare([]byte(ss.Hash), []byte(h)) != 1 {
				continue
			}
			if now.Sub(ss.Created) >= SessionLifetime || now.Sub(ss.LastUsed) >= SessionIdle {
				return Identity{}, ErrNoAccess
			}
			if now.Sub(ss.LastUsed) >= touchInterval {
				ss.LastUsed, s.dirty = now, true
			}
			return Identity{Kind: KindOwner, Name: "owner", Level: LevelAdmin}, nil
		}
		return Identity{}, ErrNoAccess
	}
	if strings.HasPrefix(secret, tokenPrefix) {
		for i := range s.data.Tokens {
			t := &s.data.Tokens[i]
			if subtle.ConstantTimeCompare([]byte(t.Hash), []byte(h)) != 1 {
				continue
			}
			if t.LastUsed == nil || now.Sub(*t.LastUsed) >= touchInterval {
				t.LastUsed, s.dirty = &now, true
			}
			return Identity{Kind: KindToken, Name: t.Name, Level: t.Level, TokenID: t.ID}, nil
		}
	}
	return Identity{}, ErrNoAccess
}

// touch writes the "last used" times, at most each touchInterval. The
// caller holds s.mu.
func (s *Store) touch(now time.Time) {
	if s.dirty && now.Sub(s.touched) >= touchInterval {
		s.touched = now
		_ = s.save()
	}
}

// CreateToken makes a token. It gives the secret only one time.
func (s *Store) CreateToken(name, level string) (Token, string, error) {
	name = strings.TrimSpace(name)
	if !validName(name) {
		return Token{}, "", ErrBadName
	}
	if !ValidLevel(level) {
		return Token{}, "", ErrBadLevel
	}
	secret := tokenPrefix + randomSecret()
	t := Token{
		ID:      randomSecret()[:12],
		Name:    name,
		Level:   level,
		Hint:    secret[:len(tokenPrefix)+4],
		Created: s.Now().UTC(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Tokens = append(s.data.Tokens, storedToken{Token: t, Hash: hashSecret(secret)})
	if err := s.save(); err != nil {
		return Token{}, "", err
	}
	return t, secret, nil
}

// Tokens gives the tokens, the oldest first.
func (s *Store) Tokens() []Token {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Token, 0, len(s.data.Tokens))
	for _, t := range s.data.Tokens {
		c := t.Token
		if t.LastUsed != nil {
			lu := *t.LastUsed
			c.LastUsed = &lu
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

// DeleteToken removes a token. Its secret stops at once.
func (s *Store) DeleteToken(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range s.data.Tokens {
		if t.ID == id {
			s.data.Tokens = append(s.data.Tokens[:i], s.data.Tokens[i+1:]...)
			return s.save()
		}
	}
	return ErrNotFound
}

// Flush writes the "last used" times that are not on the disk.
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	return s.save()
}

func validName(n string) bool {
	if n == "" || len(n) > 64 {
		return false
	}
	for _, r := range n {
		ok := r == '-' || r == '_' || r == '.' || r == ' ' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return false
		}
	}
	return true
}

// randomSecret gives 160 random bits as lower-case base32 text.
func randomSecret() string {
	b := make([]byte, 20)
	rand.Read(b)
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
}

func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
