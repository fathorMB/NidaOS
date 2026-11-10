package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func init() {
	// The real value is slow on purpose. The tests do not need it.
	pbkdf2Iterations = 1000
}

const goodPassword = "correct horse battery"

type testClock struct{ t time.Time }

func (c *testClock) now() time.Time { return c.t }

func newStore(t *testing.T) (*Store, *testClock, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	c := &testClock{t: time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)}
	s.Now = c.now
	return s, c, path
}

func TestPassword(t *testing.T) {
	s, _, path := newStore(t)
	if s.OwnerSet() {
		t.Fatal("a new store has no owner")
	}
	if _, _, err := s.SignIn(goodPassword); !errors.Is(err, ErrOwnerNotSet) {
		t.Errorf("sign-in without owner: %v", err)
	}
	if err := s.SetPassword("short"); !errors.Is(err, ErrWeakPassword) {
		t.Errorf("short password: %v", err)
	}
	if err := s.SetPassword(goodPassword); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckPassword(goodPassword); err != nil {
		t.Errorf("good password: %v", err)
	}
	if err := s.CheckPassword("correct horse battery!"); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("wrong password: %v", err)
	}

	// The file has only the hash, and only the owner can read it.
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), goodPassword) {
		t.Error("the file contains the password")
	}
	if info, _ := os.Stat(path); info != nil && info.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Errorf("mode = %v", info.Mode())
	}

	// A second Open reads the same password.
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.CheckPassword(goodPassword); err != nil {
		t.Errorf("after reopen: %v", err)
	}
}

func TestSessions(t *testing.T) {
	s, c, path := newStore(t)
	if err := s.SetPassword(goodPassword); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SignIn("wrong password 123"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("wrong: %v", err)
	}
	secret, expires, err := s.SignIn(goodPassword)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, sessionPrefix) || !expires.Equal(c.t.Add(SessionLifetime)) {
		t.Errorf("secret %q, expires %v", secret, expires)
	}
	id, err := s.Authenticate(secret)
	if err != nil || id.Kind != KindOwner || id.Level != LevelAdmin {
		t.Fatalf("id = %+v, err = %v", id, err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), secret) {
		t.Error("the file contains the session secret")
	}

	// The session stays after a restart of the service.
	s2, _ := Open(path)
	s2.Now = c.now
	if _, err := s2.Authenticate(secret); err != nil {
		t.Errorf("after reopen: %v", err)
	}

	// Use each 6 days: the session stays open to its maximum age.
	for i := 0; i < 4; i++ {
		c.t = c.t.Add(6 * 24 * time.Hour)
		if _, err := s.Authenticate(secret); err != nil {
			t.Fatalf("day %d: %v", (i+1)*6, err)
		}
	}
	c.t = c.t.Add(7 * 24 * time.Hour) // 31 days
	if _, err := s.Authenticate(secret); !errors.Is(err, ErrNoAccess) {
		t.Errorf("after 31 days: %v", err)
	}

	// Idle for 7 days: closed.
	secret, _, _ = s.SignIn(goodPassword)
	c.t = c.t.Add(SessionIdle)
	if _, err := s.Authenticate(secret); !errors.Is(err, ErrNoAccess) {
		t.Errorf("idle: %v", err)
	}

	// Sign-out.
	secret, _, _ = s.SignIn(goodPassword)
	if err := s.SignOut(secret); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(secret); !errors.Is(err, ErrNoAccess) {
		t.Errorf("after sign-out: %v", err)
	}

	// A new password closes all the sessions.
	secret, _, _ = s.SignIn(goodPassword)
	if err := s.SetPassword("another long password"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(secret); !errors.Is(err, ErrNoAccess) {
		t.Errorf("after the new password: %v", err)
	}
}

func TestTokens(t *testing.T) {
	s, c, path := newStore(t)
	tok, secret, err := s.CreateToken(" laptop ", LevelRead)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Name != "laptop" || tok.Level != LevelRead || !strings.HasPrefix(secret, tokenPrefix) || !strings.HasPrefix(secret, tok.Hint) {
		t.Errorf("token = %+v, secret = %q", tok, secret)
	}
	id, err := s.Authenticate(secret)
	if err != nil || id.Kind != KindToken || id.Name != "laptop" || id.Level != LevelRead || id.TokenID != tok.ID {
		t.Fatalf("id = %+v, err = %v", id, err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), secret) {
		t.Error("the file contains the token")
	}

	c.t = c.t.Add(time.Hour)
	admin, _, _ := s.CreateToken("claude-code", LevelAdmin)
	list := s.Tokens()
	if len(list) != 2 || list[0].Name != "laptop" || list[1].ID != admin.ID || list[0].LastUsed == nil {
		t.Errorf("list = %+v", list)
	}

	for name, level := range map[string]string{"": LevelRead, "bad/name": LevelRead, strings.Repeat("x", 65): LevelRead} {
		if _, _, err := s.CreateToken(name, level); !errors.Is(err, ErrBadName) {
			t.Errorf("name %q: %v", name, err)
		}
	}
	if _, _, err := s.CreateToken("ok", "root"); !errors.Is(err, ErrBadLevel) {
		t.Errorf("level: %v", err)
	}

	if err := s.DeleteToken(tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(secret); !errors.Is(err, ErrNoAccess) {
		t.Errorf("deleted token: %v", err)
	}
	if err := s.DeleteToken(tok.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete again: %v", err)
	}

	for _, bad := range []string{"", "nida_", "nida_wrong", "nidas_wrong", "Bearer x"} {
		if _, err := s.Authenticate(bad); !errors.Is(err, ErrNoAccess) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestAllows(t *testing.T) {
	for _, c := range []struct {
		have, need string
		ok         bool
	}{
		{LevelAdmin, LevelAdmin, true}, {LevelAdmin, LevelRead, true},
		{LevelRead, LevelRead, true}, {LevelRead, LevelAdmin, false},
		{"", LevelRead, false},
	} {
		if Allows(c.have, c.need) != c.ok {
			t.Errorf("Allows(%q, %q) != %v", c.have, c.need, c.ok)
		}
	}
}

func TestLimiter(t *testing.T) {
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	l := NewLimiter()
	l.Now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		l.Fail("a")
		if w := l.Wait("a"); w != 0 {
			t.Fatalf("failure %d: wait %v", i+1, w)
		}
	}
	l.Fail("a")
	if w := l.Wait("a"); w != time.Second {
		t.Errorf("6th failure: wait %v", w)
	}
	l.Fail("a")
	if w := l.Wait("a"); w != 2*time.Second {
		t.Errorf("7th failure: wait %v", w)
	}
	if l.Wait("b") != 0 {
		t.Error("an other address must not wait")
	}
	for i := 0; i < 30; i++ {
		l.Fail("a")
	}
	if w := l.Wait("a"); w != l.MaxWait {
		t.Errorf("maximum wait = %v", w)
	}
	l.Succeed("a")
	if l.Wait("a") != 0 {
		t.Error("a success must clear the wait")
	}
}
