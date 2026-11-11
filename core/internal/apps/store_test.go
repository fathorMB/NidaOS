package apps

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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

// deployAndFinish adds a version and writes its result.
func deployAndFinish(t *testing.T, s *Store, image, state string) int {
	t.Helper()
	v, err := s.Deploy("blog", image, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Finish("blog", v.Number, state, ""); err != nil {
		t.Fatal(err)
	}
	return v.Number
}

func TestStoreKeepsFiveVersions(t *testing.T) {
	s, _ := newStore(t)
	_, _ = s.Create(App{Name: "blog", Copies: 1})
	deployAndFinish(t, s, "localhost:5000/blog:1", StateServing)
	for i := 2; i <= 8; i++ {
		deployAndFinish(t, s, "localhost:5000/blog:"+strconv.Itoa(i), StateFailed)
	}
	a, _ := s.Get("blog")
	var numbers []int
	for _, v := range a.Versions {
		numbers = append(numbers, v.Number)
	}
	// Version 1 stays, because it is in operation.
	if fmt.Sprint(numbers) != "[1 4 5 6 7 8]" || a.Active != 1 {
		t.Errorf("versions %v, active %d", numbers, a.Active)
	}
	deployAndFinish(t, s, "localhost:5000/blog:9", StateServing)
	deployAndFinish(t, s, "localhost:5000/blog:10", StateServing)
	a, _ = s.Get("blog")
	if len(a.Versions) != KeepVersions || a.Versions[0].Number != 6 {
		t.Errorf("versions %+v", a.Versions)
	}
}

func TestRollback(t *testing.T) {
	s, _ := newStore(t)
	_, _ = s.Create(App{Name: "blog", Copies: 1, Config: Config{Port: port(8080)}})
	deployAndFinish(t, s, "localhost:5000/blog:1", StateServing)
	cfg := Config{Port: port(9090)}
	_, _, _ = s.Update("blog", Change{Config: &cfg})
	_ = s.Finish("blog", 2, StateServing, "")
	deployAndFinish(t, s, "localhost:5000/blog:3", StateFailed)

	if _, err := s.Rollback("blog", 2); !errors.Is(err, ErrNoRollback) {
		t.Errorf("rollback to the active version: %v", err)
	}
	if _, err := s.Rollback("blog", 3); !errors.Is(err, ErrNoRollback) {
		t.Errorf("rollback to a failed version: %v", err)
	}
	if _, err := s.Rollback("blog", 42); !errors.Is(err, ErrVersionNotFound) {
		t.Errorf("rollback to an unknown version: %v", err)
	}
	v, err := s.Rollback("blog", 1)
	if err != nil {
		t.Fatal(err)
	}
	if v.Number != 4 || v.RollbackOf != 1 || v.Image != "localhost:5000/blog:1" || *v.Config.Port != 8080 || v.State != StateDeploying {
		t.Errorf("rollback version %+v", v)
	}
	if _, err := s.Rollback("blog", 1); !errors.Is(err, ErrBusy) {
		t.Errorf("second rollback during a deploy: %v", err)
	}
	_ = s.Finish("blog", 4, StateServing, "")
	a, _ := s.Get("blog")
	v2, _ := a.Version(2)
	if a.Active != 4 || v2.State != StateReplaced {
		t.Errorf("active %d, version 2 %s", a.Active, v2.State)
	}
}

func TestLinkedSecrets(t *testing.T) {
	s, p := newStore(t)
	_, _ = s.Create(App{Name: "shop", Config: Config{Variables: map[string]string{"MODE": "x"}}})
	_ = s.SetSecret("shop", "TOKEN", "t")
	if err := s.SetLinked("shop", "database shop-db", map[string]string{"MODE": "y"}); !errors.Is(err, ErrNameInUse) {
		t.Errorf("a linked name over a variable: %v", err)
	}
	if err := s.SetLinked("shop", "database shop-db", map[string]string{"DATABASE_URL": "postgresql://x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLinked("shop", "database other", map[string]string{"DATABASE_URL": "postgresql://y"}); !errors.Is(err, ErrNameInUse) {
		t.Errorf("two sources with the same name: %v", err)
	}
	if err := s.SetSecret("shop", "DATABASE_URL", "z"); !IsFieldError(err) {
		t.Errorf("a secret with a linked name: %v", err)
	}
	cfg := Config{Variables: map[string]string{"DATABASE_URL": "z"}}
	if _, _, err := s.Update("shop", Change{Config: &cfg}); !IsFieldError(err) {
		t.Errorf("a variable with a linked name: %v", err)
	}
	if got := s.Secrets("shop"); got["DATABASE_URL"] != "postgresql://x" || got["TOKEN"] != "t" {
		t.Errorf("secrets %v", got)
	}
	if fmt.Sprint(s.SecretNames("shop")) != "[TOKEN]" || s.LinkedNames("shop")["DATABASE_URL"] != "database shop-db" {
		t.Errorf("names %v %v", s.SecretNames("shop"), s.LinkedNames("shop"))
	}
	s2, _ := OpenStore(p)
	if s2.Secrets("shop")["DATABASE_URL"] != "postgresql://x" {
		t.Error("the linked secret is not in the file")
	}
	_ = s.RemoveLinked("shop", "database shop-db")
	if _, ok := s.Secrets("shop")["DATABASE_URL"]; ok {
		t.Error("the linked secret is still there")
	}
}
