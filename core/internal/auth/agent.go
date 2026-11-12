package auth

import (
	"errors"
	"fmt"
	"strings"
)

// The agent tokens (milestone M-09). An agent, for example Claude Code,
// uses the same API as the console. Its token has rules: for each class of
// operation, the agent can do it ("allow"), must ask the owner first
// ("ask"), or cannot do it ("deny").

// LevelAgent is the level of an agent token.
const LevelAgent = "agent"

// The classes of operation of the rules.
const (
	ClassRead      = "read"      // the state, the metrics, the logs, the lists
	ClassApps      = "apps"      // add an app, deploy, restart, change the settings
	ClassDatabases = "databases" // add a database, links, backups, the home network
	ClassDelete    = "delete"    // remove an app, a database, a backup, a repository
	ClassUpdates   = "updates"   // the system updates and the restarts of the computer
	ClassSecrets   = "secrets"   // read the password of a database
	ClassAccess    = "access"    // tokens, password, GitHub App, approvals: never for an agent
)

// The values of a rule.
const (
	RuleAllow = "allow"
	RuleAsk   = "ask"
	RuleDeny  = "deny"
)

// RuleClasses are the classes that the owner can set, in the order of the
// console.
var RuleClasses = []string{ClassRead, ClassApps, ClassDatabases, ClassDelete, ClassUpdates, ClassSecrets}

// DefaultRules are the rules of a new agent token.
func DefaultRules() map[string]string {
	return map[string]string{
		ClassRead:      RuleAllow,
		ClassApps:      RuleAllow,
		ClassDatabases: RuleAllow,
		ClassDelete:    RuleAsk,
		ClassUpdates:   RuleAsk,
		ClassSecrets:   RuleAsk,
	}
}

// ErrBadRules: a rule has an unknown class or value.
var ErrBadRules = errors.New("the rules are not correct")

// NormalizeRules gives the full rules: the defaults for each missing
// class. The class access is never in the rules: it is always "deny".
func NormalizeRules(in map[string]string) (map[string]string, error) {
	out := DefaultRules()
	for k, v := range in {
		v = strings.ToLower(strings.TrimSpace(v))
		valid := false
		for _, c := range RuleClasses {
			if c == k {
				valid = true
			}
		}
		if !valid {
			return nil, fmt.Errorf("%w: the class %q is unknown", ErrBadRules, k)
		}
		if v != RuleAllow && v != RuleAsk && v != RuleDeny {
			return nil, fmt.Errorf("%w: the value %q is not allow, ask or deny", ErrBadRules, v)
		}
		out[k] = v
	}
	return out, nil
}

// RuleOf gives the rule of an identity for a class. An identity that is
// not an agent gets "allow": its level decides.
func RuleOf(id Identity, class string) string {
	if id.Level != LevelAgent {
		return RuleAllow
	}
	if class == ClassAccess {
		return RuleDeny
	}
	if v, ok := id.Rules[class]; ok {
		return v
	}
	if v, ok := DefaultRules()[class]; ok {
		return v
	}
	return RuleDeny
}

// CreateAgentToken makes a token for an agent with its rules.
func (s *Store) CreateAgentToken(name, agent string, rules map[string]string) (Token, string, error) {
	agent = strings.TrimSpace(agent)
	if agent == "" {
		agent = name
	}
	if !validName(agent) {
		return Token{}, "", ErrBadName
	}
	r, err := NormalizeRules(rules)
	if err != nil {
		return Token{}, "", err
	}
	t, secret, err := s.CreateToken(name, LevelAgent)
	if err != nil {
		return Token{}, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Tokens {
		if s.data.Tokens[i].ID == t.ID {
			s.data.Tokens[i].Agent, s.data.Tokens[i].Rules = agent, r
			t = s.data.Tokens[i].Token
		}
	}
	return t, secret, s.save()
}

// SetRules changes the rules of an agent token. The change applies at
// once.
func (s *Store) SetRules(id string, rules map[string]string) (Token, error) {
	r, err := NormalizeRules(rules)
	if err != nil {
		return Token{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Tokens {
		t := &s.data.Tokens[i]
		if t.ID != id {
			continue
		}
		if t.Level != LevelAgent {
			return Token{}, fmt.Errorf("%w: the token is not an agent token", ErrBadRules)
		}
		t.Rules = r
		return t.Token, s.save()
	}
	return Token{}, ErrNotFound
}

// TokenByID gives the public data of a token.
func (s *Store) TokenByID(id string) (Token, bool) {
	for _, t := range s.Tokens() {
		if t.ID == id {
			return t, true
		}
	}
	return Token{}, false
}
