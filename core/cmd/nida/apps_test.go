package main

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/fathorMB/NidaOS/core/internal/apps"
	"github.com/fathorMB/NidaOS/core/internal/auth"
	"github.com/fathorMB/NidaOS/core/internal/ca"
	"github.com/fathorMB/NidaOS/core/internal/httpserver"
)

// startAppService starts a router with a real app store and a controller
// on a fake cluster. The controller loop does not run: the test writes the
// result of each deploy itself.
func startAppService(t *testing.T) (map[string]string, *apps.Store) {
	t.Helper()
	dir := t.TempDir()
	authStore, err := auth.Open(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, token, _ := authStore.CreateToken("test", auth.LevelAdmin)
	store, err := apps.OpenStore(filepath.Join(dir, "apps.json"))
	if err != nil {
		t.Fatal(err)
	}
	opts := apps.Options{Domain: "nidaos.local"}
	runner := apps.NewRunner(store, "", opts)
	runner.SetController(apps.New(store, fake.NewClientset(), nil, opts))
	authority, err := ca.Open(filepath.Join(dir, "ca"), "nidaos.local")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(httpserver.NewRouter(httpserver.Deps{
		Auth:      authStore,
		Apps:      &httpserver.AppHosting{Store: store, Runner: runner, Registry: "localhost:5000"},
		Authority: authority,
	}))
	t.Cleanup(srv.Close)
	return map[string]string{"NIDA_SERVER": srv.URL, "NIDA_TOKEN": token}, store
}

func TestAppCommands(t *testing.T) {
	env, store := startAppService(t)
	ctx := context.Background()

	code, out, errOut := nida(ctx, env, "app", "list")
	if code != 0 || !strings.Contains(out, "There are no apps") {
		t.Fatalf("empty list: %d %q %q", code, out, errOut)
	}
	code, out, errOut = nida(ctx, env, "app", "add", "blog", "--repo", "fathor/blog", "--port", "8080",
		"--health", "/healthz", "--env", "MODE=prod", "--folder", "data:/data:standard:1G", "--memory", "256M")
	if code != 0 || !strings.Contains(out, "nida app deploy blog") {
		t.Fatalf("add: %d %q %q", code, out, errOut)
	}
	a, _ := store.Get("blog")
	if *a.Config.Port != 8080 || a.Config.Variables["MODE"] != "prod" || a.Folders[0].SizeBytes != 1<<30 || a.Config.Limits.MemoryBytes != 256<<20 {
		t.Fatalf("stored app %+v", a)
	}

	code, _, errOut = nida(ctx, env, "app", "secret", "set", "blog", "TOKEN", "--value", "s3cret")
	if code != 0 {
		t.Fatalf("secret: %q", errOut)
	}

	// deploy --wait waits for the result of the controller.
	go func() {
		for range 100 {
			if a, _ := store.Get("blog"); len(a.Versions) == 1 {
				time.Sleep(50 * time.Millisecond)
				_ = store.Finish("blog", 1, apps.StateServing, "")
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	code, out, errOut = nida(ctx, env, "app", "deploy", "blog", "localhost:5000/blog:1", "--commit", "abc123", "--wait")
	if code != 0 || !strings.Contains(out, "is in operation at https://blog.nidaos.local") {
		t.Fatalf("deploy --wait: %d %q %q", code, out, errOut)
	}

	// A failed version gives the reason and the exit code 1.
	go func() {
		for range 100 {
			if a, _ := store.Get("blog"); len(a.Versions) == 2 {
				_ = store.Finish("blog", 2, apps.StateFailed, "the health check failed")
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	code, _, errOut = nida(ctx, env, "app", "set", "blog", "--env", "MODE=debug", "--wait")
	if code != 1 || !strings.Contains(errOut, "the health check failed") || !strings.Contains(errOut, "version 1 stays in operation") {
		t.Fatalf("failed set --wait: %d %q", code, errOut)
	}

	code, out, _ = nida(ctx, env, "app", "show", "blog")
	for _, want := range []string{"https://blog.nidaos.local", "fathor/blog", "HTTP /healthz", "TOKEN", "/data on standard", "serving (active)", "failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("show has no %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "s3cret") {
		t.Error("show gives the value of a secret")
	}
	code, out, _ = nida(ctx, env, "app", "list")
	if code != 0 || !strings.Contains(out, "blog") || !strings.Contains(out, "fathor/blog") {
		t.Errorf("list: %q", out)
	}
	if code, _, errOut = nida(ctx, env, "app", "deploy", "blog", "docker.io/library/nginx:1"); code != 1 || !strings.Contains(errOut, "local registry") {
		t.Errorf("image of an other registry: %d %q", code, errOut)
	}
	if code, _, errOut = nida(ctx, env, "app", "remove", "blog"); code != 2 || !strings.Contains(errOut, "--yes") {
		t.Errorf("remove without --yes: %d %q", code, errOut)
	}
	if code, _, errOut = nida(ctx, env, "app", "remove", "blog", "--yes"); code != 0 {
		t.Errorf("remove: %d %q", code, errOut)
	}
	if _, ok := store.Get("blog"); ok {
		t.Error("the app is still there")
	}
}

func TestCACommand(t *testing.T) {
	env, _ := startAppService(t)
	code, out, errOut := nida(context.Background(), env, "ca")
	if code != 0 || !strings.HasPrefix(out, "-----BEGIN CERTIFICATE-----") {
		t.Fatalf("ca: %d %q %q", code, out, errOut)
	}
	code, out, _ = nida(context.Background(), env, "ca", "--fingerprint")
	if code != 0 || len(strings.TrimSpace(out)) != 95 {
		t.Errorf("fingerprint: %q", out)
	}
}
