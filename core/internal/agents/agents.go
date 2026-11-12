// Package agents keeps the approval requests and the record of the actions
// of the agent tokens (milestone M-09).
//
// When the rule of an agent says "ask", nida-core keeps the request as an
// approval request. The owner approves or denies it in the console. On an
// approval, nida-core runs the request with the identity of the agent and
// keeps the result. The agent reads the request to know the result.
package agents

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// States of an approval request.
const (
	StatePending  = "pending"
	StateApproved = "approved" // the owner approved; the request runs
	StateDenied   = "denied"
	StateDone     = "done"   // the request ran and passed
	StateFailed   = "failed" // the request ran and gave an error
	StateExpired  = "expired"
)

// ApprovalTTL is the time that an approval request waits for the owner.
const ApprovalTTL = 24 * time.Hour

// MaxBody is the maximum size of the body of a kept request.
const MaxBody = 64 << 10

// Approval is one request of an agent that waits for the owner.
type Approval struct {
	ID      string `json:"id"`
	TokenID string `json:"token_id"`
	Token   string `json:"token"`
	Agent   string `json:"agent"`
	Class   string `json:"class"`
	Method  string `json:"method"`
	Path    string `json:"path"`
	// Body is the JSON body of the request. The API never gives it to an
	// agent: it can have a secret value.
	Body []byte `json:"body,omitempty"`
	// Summary tells the operation in words, for the console.
	Summary string     `json:"summary"`
	Reason  string     `json:"reason,omitempty"`
	State   string     `json:"state"`
	Created time.Time  `json:"created"`
	Decided *time.Time `json:"decided,omitempty"`
	By      string     `json:"by,omitempty"`
	// Status and Result are the response of the request after the approval.
	Status int    `json:"status,omitempty"`
	Result string `json:"result,omitempty"`
}

// Action is one change of an agent in the record.
type Action struct {
	Time     time.Time `json:"time"`
	TokenID  string    `json:"token_id"`
	Token    string    `json:"token"`
	Agent    string    `json:"agent"`
	Class    string    `json:"class"`
	Method   string    `json:"method"`
	Path     string    `json:"path"`
	Status   int       `json:"status"`
	Approval string    `json:"approval,omitempty"`
	// Outcome is "done", "failed", "asked" or "refused".
	Outcome string `json:"outcome"`
}

// MaxActions is the number of actions that the store keeps.
const MaxActions = 2000

// Errors of the store.
var (
	ErrNotFound   = errors.New("the approval request does not exist")
	ErrNotPending = errors.New("the approval request is not pending")
)

type stored struct {
	Approvals []Approval `json:"approvals"`
	Actions   []Action   `json:"actions"`
}

// Store keeps the approvals and the actions in a file (mode 0600: a body
// can have a secret value).
type Store struct {
	path string
	Now  func() time.Time

	mu   sync.Mutex
	data stored
}

// Open reads the file. A missing file gives an empty store.
func Open(path string) (*Store, error) {
	s := &Store{path: path, Now: time.Now}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, &s.data); err != nil {
			return nil, fmt.Errorf("the agents file %s is not correct: %w", path, err)
		}
	}
	return s, nil
}

func (s *Store) save() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.data, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// expire marks the old pending requests. The caller holds s.mu.
func (s *Store) expire(now time.Time) bool {
	changed := false
	for i := range s.data.Approvals {
		a := &s.data.Approvals[i]
		if a.State == StatePending && now.Sub(a.Created) > ApprovalTTL {
			a.State, a.Body, changed = StateExpired, nil, true
		}
	}
	// Keep the last 500 requests.
	if n := len(s.data.Approvals); n > 500 {
		s.data.Approvals = s.data.Approvals[n-500:]
		changed = true
	}
	return changed
}

// Ask keeps a new approval request.
func (s *Store) Ask(a Approval) (Approval, error) {
	if len(a.Body) > MaxBody {
		return Approval{}, fmt.Errorf("the body of the request is larger than %d bytes", MaxBody)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now().UTC()
	s.expire(now)
	a.ID, a.State, a.Created = newID(), StatePending, now
	s.data.Approvals = append(s.data.Approvals, a)
	return a, s.save()
}

// Get gives one request.
func (s *Store) Get(id string) (Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.expire(s.Now().UTC()) {
		_ = s.save()
	}
	for _, a := range s.data.Approvals {
		if a.ID == id {
			return a, nil
		}
	}
	return Approval{}, ErrNotFound
}

// List gives the requests, the newest first. tokenID "" gives all.
// state "" gives each state.
func (s *Store) List(tokenID, state string) []Approval {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.expire(s.Now().UTC()) {
		_ = s.save()
	}
	var out []Approval
	for _, a := range s.data.Approvals {
		if (tokenID == "" || a.TokenID == tokenID) && (state == "" || a.State == state) {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// Decide approves or denies a pending request. It gives the request with
// its body, thus the caller can run it.
func (s *Store) Decide(id string, approve bool, by, reason string) (Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now().UTC()
	s.expire(now)
	for i := range s.data.Approvals {
		a := &s.data.Approvals[i]
		if a.ID != id {
			continue
		}
		if a.State != StatePending {
			return *a, ErrNotPending
		}
		a.Decided, a.By, a.Reason = &now, by, reason
		if approve {
			a.State = StateApproved
		} else {
			a.State, a.Body = StateDenied, nil
		}
		return *a, s.save()
	}
	return Approval{}, ErrNotFound
}

// Finish keeps the result of an approved request and deletes its body.
func (s *Store) Finish(id string, status int, result string) (Approval, error) {
	if len(result) > 4096 {
		result = result[:4096]
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Approvals {
		a := &s.data.Approvals[i]
		if a.ID != id {
			continue
		}
		a.Status, a.Result, a.Body = status, result, nil
		if status >= 200 && status < 300 {
			a.State = StateDone
		} else {
			a.State = StateFailed
		}
		return *a, s.save()
	}
	return Approval{}, ErrNotFound
}

// Record adds an action to the record.
func (s *Store) Record(a Action) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.Time.IsZero() {
		a.Time = s.Now().UTC()
	}
	s.data.Actions = append(s.data.Actions, a)
	if n := len(s.data.Actions); n > MaxActions {
		s.data.Actions = s.data.Actions[n-MaxActions:]
	}
	_ = s.save()
}

// Actions gives the last actions, the newest first. tokenID "" gives all.
func (s *Store) Actions(tokenID string, limit int) []Action {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Action
	for i := len(s.data.Actions) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		a := s.data.Actions[i]
		if tokenID == "" || a.TokenID == tokenID {
			out = append(out, a)
		}
	}
	return out
}
