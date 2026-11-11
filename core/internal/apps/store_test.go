package apps

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "apps.json")
	s, err := OpenStore(p)
	if err != nil {
		t.Fatal(err)
	}
	return s, p
}

func TestStoreKeepsTheAppsInTheFile(t *testing.T) {
	s, p := newStore(t)
	a := App{Name: "blog", Repository: "fathor/blog", Branch: "main", Copies: 1,
		Config:  Config{Port: port(8080), Variables: map[string]string{"MODE": "prod"}},
		Folders: []Folder{{Name: "data", Path: "/data", Tier: TierStandard, SizeBytes: 1 << 30}}}
	if _, err := s.Create(a); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(a); !errors.Is(err, ErrExists) {
		t.Errorf("second create: %v, want ErrExists", err)
	}
	if err := s.SetSecret("blog", "TOKEN", "s3cret"); err != nil {
		t.Fatal(err)
	}
	v, err := s.Deploy("blog", "localhost:5000/blog:1", "abc123", "first")
	if err != nil || v.Number != 1 || v.State != StateDeploying || *v.Config.Port != 8080 {
		t.Fatalf("deploy: %+v %v", v, err)
	}
	if _, err := s.Deploy("blog", "localhost:5000/blog:2", "", ""); !errors.Is(err, ErrBusy) {
		t.Errorf("second deploy while busy: %v, want ErrBusy", err)
	}
	if err := s.Finish("blog", 1, StateServing, ""); err != nil {
		t.Fatal(err)
	}

	// A new store reads the same data from the file.
	s2, err := OpenStore(p)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s2.Get("blog")
	if !ok || got.Active != 1 || got.Versions[0].State != StateServing || got.Folders[0].Tier != TierStandard {
		t.Fatalf("after reopen: %+v", got)
	}
	if s2.Secrets("blog")["TOKEN"] != "s3cret" {
		t.Error("the secret is not in the file")
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(p)
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("mode %v, want 0600", fi.Mode().Perm())
		}
	}
}

func TestStoreGivesCopies(t *testing.T) {
	s, _ := newStore(t)
	if _, err := s.Create(App{Name: "blog", Config: Config{Variables: map[string]string{"A": "1"}}}); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Get("blog")
	a.Config.Variables["A"] = "changed"
	b, _ := s.Get("blog")
	if b.Config.Variables["A"] != "1" {
		t.Error("a change of the result changed the store")
	}
}

func TestSettingsChangeMakesAVersion(t *testing.T) {
	s, _ := newStore(t)
	_, _ = s.Create(App{Name: "blog", Copies: 1, Config: Config{Port: port(8080)}})

	// Before the first deploy, a change only changes the app.
	cfg := Config{Port: port(9090)}
	_, n, err := s.Update("blog", Change{Config: &cfg})
	if err != nil || n != 0 {
		t.Fatalf("update before a deploy: %d %v", n, err)
	}
	_, _ = s.Deploy("blog", "localhost:5000/blog:1", "abc", "")
	_ = s.Finish("blog", 1, StateServing, "")

	cfg.Variables = map[string]string{"MODE": "debug"}
	a, n, err := s.Update("blog", Change{Config: &cfg})
	if err != nil || n != 2 {
		t.Fatalf("update: %d %v", n, err)
	}
	v, _ := a.Version(2)
	if v.Image != "localhost:5000/blog:1" || v.Config.Variables["MODE"] != "debug" || v.State != StateDeploying {
		t.Errorf("new version %+v", v)
	}
	two := 2
	if _, _, err := s.Update("blog", Change{Copies: &two}); !errors.Is(err, ErrBusy) {
		// A change of the copies only does not make a version, thus it
		// is possible during a deploy.
		if err != nil {
			t.Errorf("copies during a deploy: %v", err)
		}
	}
	_ = s.Finish("blog", 2, StateFailed, "no answer")
	a, _ = s.Get("blog")
	if a.Active != 1 {
		t.Errorf("active %d after a failed version, want 1", a.Active)
	}
}

func TestValidation(t *testing.T) {
	s, _ := newStore(t)
	cases := []struct {
		app   App
		field string
	}{
		{App{Name: "Blog"}, "name"},
		{App{Name: "blog-"}, "name"},
		{App{Name: strings.Repeat("a", 33)}, "name"},
		{App{Name: "blog", Repository: "no-slash"}, "repository"},
		{App{Name: "blog", Config: Config{Port: port(0)}}, "port"},
		{App{Name: "blog", Config: Config{HealthCheck: &HealthCheck{Path: "/h"}}}, "health_check"},
		{App{Name: "blog", Config: Config{Port: port(80), HealthCheck: &HealthCheck{Path: "h"}}}, "health_check.path"},
		{App{Name: "blog", Config: Config{Variables: map[string]string{"NIDA_APP": "x"}}}, "variables"},
		{App{Name: "blog", Config: Config{Variables: map[string]string{"1A": "x"}}}, "variables"},
		{App{Name: "blog", Config: Config{Limits: Limits{MemoryBytes: 1000}}}, "limits.memory_bytes"},
		{App{Name: "blog", Copies: 11}, "copies"},
		{App{Name: "blog", Copies: 2, Folders: []Folder{{Name: "d", Path: "/d", Tier: TierStandard, SizeBytes: MinFolderBytes}}}, "copies"},
		{App{Name: "blog", Folders: []Folder{{Name: "d", Path: "rel", Tier: TierStandard, SizeBytes: MinFolderBytes}}}, "folders.path"},
		{App{Name: "blog", Folders: []Folder{{Name: "d", Path: "/d", Tier: "fast", SizeBytes: MinFolderBytes}}}, "folders.tier"},
		{App{Name: "blog", Folders: []Folder{{Name: "d", Path: "/d", Tier: TierStandard, SizeBytes: 10}}}, "folders.size_bytes"},
	}
	for _, c := range cases {
		_, err := s.Create(c.app)
		var fe *FieldError
		if !errors.As(err, &fe) || fe.Field != c.field {
			t.Errorf("%+v: error %v, want a FieldError for %s", c.app, err, c.field)
		}
	}
	if _, err := s.Deploy("x", "localhost:5000/x:1", "", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("deploy of an unknown app: %v", err)
	}
	_, _ = s.Create(App{Name: "blog", Config: Config{Variables: map[string]string{"MODE": "x"}}})
	if _, err := s.Deploy("blog", "Not An Image", "", ""); !IsFieldError(err) {
		t.Errorf("bad image: %v", err)
	}
	if err := s.SetSecret("blog", "MODE", "x"); !IsFieldError(err) {
		t.Errorf("a secret with the name of a variable: %v", err)
	}
	if err := s.DeleteSecret("blog", "NONE"); !errors.Is(err, ErrSecretNotFound) {
		t.Errorf("delete of an unknown secret: %v", err)
	}
}

func TestRemoveDeletesTheSecrets(t *testing.T) {
	s, _ := newStore(t)
	_, _ = s.Create(App{Name: "blog"})
	_ = s.SetSecret("blog", "TOKEN", "x")
	if err := s.Remove("blog"); err != nil {
		t.Fatal(err)
	}
	_, _ = s.Create(App{Name: "blog"})
	if len(s.SecretNames("blog")) != 0 {
		t.Error("a new app with the same name got the old secrets")
	}
}
