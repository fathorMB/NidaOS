package runners

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"
)

// JobTokenPrefix starts each job token.
const JobTokenPrefix = "nidj_"

// JobIdentity is the owner of a job token: one runner pod of one app.
type JobIdentity struct {
	App     string
	Pod     string
	Expires time.Time
}

// Tokens keeps the job tokens in memory. A token is valid only for the
// deploy of its app, and only while its pod runs. After a restart of the
// service, the old tokens are not valid.
type Tokens struct {
	Now func() time.Time

	mu     sync.Mutex
	byHash map[string]JobIdentity
}

// NewTokens makes an empty set.
func NewTokens() *Tokens {
	return &Tokens{Now: time.Now, byHash: map[string]JobIdentity{}}
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

// Issue makes a token for the pod of an app.
func (t *Tokens) Issue(app, pod string, life time.Duration) string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	tok := JobTokenPrefix + hex.EncodeToString(b)
	t.mu.Lock()
	t.byHash[hashToken(tok)] = JobIdentity{App: app, Pod: pod, Expires: t.Now().Add(life)}
	t.mu.Unlock()
	return tok
}

// Check gives the identity of a token.
func (t *Tokens) Check(tok string) (JobIdentity, bool) {
	if !strings.HasPrefix(tok, JobTokenPrefix) {
		return JobIdentity{}, false
	}
	h := hashToken(tok)
	t.mu.Lock()
	defer t.mu.Unlock()
	id, ok := t.byHash[h]
	if !ok {
		return JobIdentity{}, false
	}
	if t.Now().After(id.Expires) {
		delete(t.byHash, h)
		return JobIdentity{}, false
	}
	return id, true
}

// RevokePod removes the tokens of a pod.
func (t *Tokens) RevokePod(pod string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for h, id := range t.byHash {
		if id.Pod == pod {
			delete(t.byHash, h)
		}
	}
}
