package github_test

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/github"
	"github.com/fathorMB/NidaOS/core/internal/github/githubtest"
)

func client(s *githubtest.Server) *github.Client {
	app := s.App
	return github.New(s.URL, s.URL, func() (github.App, bool) { return app, true })
}

func TestJWTIsSignedWithTheKeyOfTheApp(t *testing.T) {
	s := githubtest.New()
	defer s.Close()
	now := time.Unix(1_800_000_000, 0)
	tok, err := github.JWT(s.App, now)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("the token has %d parts", len(parts))
	}
	key, _ := s.App.Key()
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("the signature is not correct: %v", err)
	}
	b, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c struct {
		Iat, Exp int64
		Iss      string
	}
	_ = json.Unmarshal(b, &c)
	if c.Iss != "4242" || c.Iat != now.Unix()-60 || c.Exp-c.Iat > 600 {
		t.Fatalf("claims = %+v", c)
	}
}

func TestConvertGivesTheAppOnce(t *testing.T) {
	s := githubtest.New()
	defer s.Close()
	s.Code("abc")
	c := github.New(s.URL, s.URL, nil)
	a, err := c.Convert(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != 4242 || a.Slug != "nidaos-test" || a.Owner != "owner" || !strings.Contains(a.PEM, "PRIVATE KEY") {
		t.Fatalf("app = %+v", a)
	}
	if _, err := c.Convert(context.Background(), "abc"); !errors.Is(err, github.ErrNotFound) {
		t.Fatalf("second convert: %v", err)
	}
	if !strings.HasSuffix(c.InstallURL(a), "/apps/nidaos-test/installations/new") {
		t.Fatal(c.InstallURL(a))
	}
}

func TestAppInfoExaminesTheCredentials(t *testing.T) {
	s := githubtest.New()
	defer s.Close()
	c := github.New(s.URL, s.URL, nil)
	a, err := c.AppInfo(context.Background(), github.App{ID: s.App.ID, PEM: s.App.PEM})
	if err != nil || a.Slug != "nidaos-test" {
		t.Fatalf("app = %+v, err = %v", a, err)
	}
	if _, err := c.AppInfo(context.Background(), github.App{ID: 1, PEM: s.App.PEM}); err == nil {
		t.Fatal("an other app ID passed")
	}
}

func TestTokenIsKeptAndNeedsTheInstallation(t *testing.T) {
	s := githubtest.New()
	defer s.Close()
	s.Repo("me/shop", true)
	r := s.Repo("me/other", true)
	r.Installed = false
	c := client(s)
	for range 3 {
		if _, err := c.Repo("me", "shop").Info(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if n := s.TokenCount(); n != 1 {
		t.Fatalf("the client made %d tokens", n)
	}
	if _, err := c.Repo("me", "other").Info(context.Background()); !errors.Is(err, github.ErrNotInstalled) {
		t.Fatalf("err = %v", err)
	}
	none := github.New(s.URL, s.URL, func() (github.App, bool) { return github.App{}, false })
	if _, err := none.Repo("me", "shop").Info(context.Background()); !errors.Is(err, github.ErrNoApp) {
		t.Fatalf("err = %v", err)
	}
}

func TestJITRunnerAndDelete(t *testing.T) {
	s := githubtest.New()
	defer s.Close()
	s.Repo("me/shop", true)
	repo := client(s).Repo("me", "shop")
	j, err := repo.JITConfig(context.Background(), "nida-1", []string{"self-hosted", "nidaos"})
	if err != nil || j.ID == 0 || j.Config == "" {
		t.Fatalf("jit = %+v, err = %v", j, err)
	}
	if rs := s.Runners(); len(rs) != 1 || rs[0].Labels[1] != "nidaos" {
		t.Fatalf("runners = %+v", rs)
	}
	if err := repo.DeleteRunner(context.Background(), j.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteRunner(context.Background(), j.ID); err != nil {
		t.Fatalf("a removed runner: %v", err)
	}
	if v, err := repo.LatestRunnerVersion(context.Background()); err != nil || v != "2.330.0" {
		t.Fatalf("version = %q, err = %v", v, err)
	}
}

func TestOpenPullRequestIsIdempotent(t *testing.T) {
	s := githubtest.New()
	defer s.Close()
	s.Repo("me/shop", true)
	repo := client(s).Repo("me", "shop")
	f := github.FileChange{Path: ".github/workflows/nidaos-deploy.yml", Content: []byte("one")}
	pr, err := repo.OpenPullRequest(context.Background(), "main", "nidaos/deploy", "Add", "Body", "msg", f)
	if err != nil || pr.Number != 1 {
		t.Fatalf("pr = %+v, err = %v", pr, err)
	}
	f.Content = []byte("two")
	pr, err = repo.OpenPullRequest(context.Background(), "main", "nidaos/deploy", "Add", "Body", "msg", f)
	if err != nil || pr.Number != 1 {
		t.Fatalf("second pr = %+v, err = %v", pr, err)
	}
	r, unlock := s.Lock("me/shop")
	defer unlock()
	if got := r.Files["nidaos/deploy:.github/workflows/nidaos-deploy.yml"]; got != "two" {
		t.Fatalf("file = %q", got)
	}
	if len(r.Pulls) != 1 || r.Refs["nidaos/deploy"] != r.Refs["main"] {
		t.Fatalf("pulls = %+v, refs = %+v", r.Pulls, r.Refs)
	}
}

func TestManifestHasNoWebhook(t *testing.T) {
	m := github.Manifest("NidaOS home", "https://nidaos.local", "https://nidaos.local/cb")
	hook := m["hook_attributes"].(map[string]any)
	if hook["active"] != false || m["public"] != false {
		t.Fatalf("manifest = %+v", m)
	}
	perms := m["default_permissions"].(map[string]string)
	if perms["administration"] != "write" || perms["workflows"] != "write" {
		t.Fatalf("permissions = %+v", perms)
	}
}
