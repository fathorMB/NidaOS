package httpserver

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/fathorMB/NidaOS/core/internal/apps"
	"github.com/fathorMB/NidaOS/core/internal/auth"
)

type fakeCerts struct{}

func (fakeCerts) CertPEM() []byte {
	return []byte("-----BEGIN CERTIFICATE-----\nX\n-----END CERTIFICATE-----\n")
}
func (fakeCerts) Fingerprint() string { return "AA:BB" }

// appRouter makes a router with a real app store and a controller on a
// fake cluster. The controller loop does not run: the deploys stay in the
// state deploying.
func appRouter(t *testing.T) (*Router, *apps.Store) {
	t.Helper()
	store, err := apps.OpenStore(filepath.Join(t.TempDir(), "apps.json"))
	if err != nil {
		t.Fatal(err)
	}
	runner := apps.NewRunner(store, "", apps.Options{Domain: "nidaos.local"})
	runner.SetController(apps.New(store, fake.NewClientset(), nil, apps.Options{Domain: "nidaos.local"}))
	r := NewRouter(Deps{Apps: &AppHosting{Store: store, Runner: runner, Registry: "localhost:5000"}, Authority: fakeCerts{}})
	return r, store
}

func decode[T any](t *testing.T, body []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return v
}

func TestAppLife(t *testing.T) {
	r, store := appRouter(t)
	rec := do(r, call{method: "POST", path: "/api/v1/apps", body: `{"name":"blog","repository":"fathor/blog","config":{"port":8080,"health_check":{"path":"/healthz"}},"folders":[{"name":"data","path":"/data","tier":"standard","size_bytes":1073741824}]}`})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	v := decode[AppView](t, rec.Body.Bytes())
	if v.Name != "blog" || v.Copies != 1 || v.State != "new" || v.Address != "https://blog.nidaos.local" {
		t.Errorf("created app: %+v", v)
	}
	if rec := do(r, call{method: "POST", path: "/api/v1/apps", body: `{"name":"blog"}`}); rec.Code != http.StatusConflict || errCode(rec) != "app_exists" {
		t.Errorf("second create: %d %s", rec.Code, rec.Body)
	}
	if rec := do(r, call{method: "POST", path: "/api/v1/apps", body: `{"name":"Bad Name"}`}); rec.Code != http.StatusBadRequest || errCode(rec) != "bad_field" {
		t.Errorf("bad name: %d %s", rec.Code, rec.Body)
	}

	// The secret value never comes back.
	if rec := do(r, call{method: "PUT", path: "/api/v1/apps/blog/secrets/TOKEN", body: `{"value":"s3cret"}`}); rec.Code != http.StatusNoContent {
		t.Fatalf("secret: %d %s", rec.Code, rec.Body)
	}
	rec = do(r, call{method: "GET", path: "/api/v1/apps/blog"})
	if strings.Contains(rec.Body.String(), "s3cret") || !strings.Contains(rec.Body.String(), `"secrets":["TOKEN"]`) {
		t.Errorf("app view: %s", rec.Body)
	}

	// A deploy accepts only an image of the local registry.
	if rec := do(r, call{method: "POST", path: "/api/v1/apps/blog/deploys", body: `{"image":"docker.io/library/nginx:1"}`}); errCode(rec) != "image_not_local" {
		t.Errorf("image of an other registry: %d %s", rec.Code, rec.Body)
	}
	rec = do(r, call{method: "POST", path: "/api/v1/apps/blog/deploys", body: `{"image":"localhost:5000/blog:1","commit":"abc"}`})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("deploy: %d %s", rec.Code, rec.Body)
	}
	if ver := decode[apps.Version](t, rec.Body.Bytes()); ver.Number != 1 || ver.State != apps.StateDeploying {
		t.Errorf("version %+v", ver)
	}
	if rec := do(r, call{method: "POST", path: "/api/v1/apps/blog/deploys", body: `{"image":"localhost:5000/blog:2"}`}); errCode(rec) != "app_busy" {
		t.Errorf("deploy during a deploy: %d %s", rec.Code, rec.Body)
	}
	if rec := do(r, call{method: "DELETE", path: "/api/v1/apps/blog"}); errCode(rec) != "app_busy" {
		t.Errorf("remove during a deploy: %d %s", rec.Code, rec.Body)
	}
	_ = store.Finish("blog", 1, apps.StateServing, "")

	if rec := do(r, call{method: "POST", path: "/api/v1/apps/blog/rollback", body: `{"version":1}`}); errCode(rec) != "no_rollback" {
		t.Errorf("rollback to the active version: %d %s", rec.Code, rec.Body)
	}
	rec = do(r, call{method: "PATCH", path: "/api/v1/apps/blog", body: `{"copies":0}`})
	if rec.Code != http.StatusOK || decode[AppView](t, rec.Body.Bytes()).State != "stopped" {
		t.Errorf("stop: %d %s", rec.Code, rec.Body)
	}
	rec = do(r, call{method: "PATCH", path: "/api/v1/apps/blog", body: `{"config":{"port":9090}}`})
	var upd struct {
		Deploy int `json:"deploy"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &upd)
	if rec.Code != http.StatusOK || upd.Deploy != 2 {
		t.Errorf("config change: %d %s", rec.Code, rec.Body)
	}
	_ = store.Finish("blog", 2, apps.StateServing, "")
	if rec := do(r, call{method: "POST", path: "/api/v1/apps/blog/folders/data/move", body: `{"tier":"performance"}`}); rec.Code != http.StatusAccepted || decode[AppView](t, rec.Body.Bytes()).State != "moving" {
		t.Errorf("move: %d %s", rec.Code, rec.Body)
	}
	if rec := do(r, call{method: "POST", path: "/api/v1/apps/blog/folders/none/move", body: `{"tier":"performance"}`}); rec.Code != http.StatusConflict && errCode(rec) != "folder_not_found" {
		t.Errorf("move of an unknown folder: %d %s", rec.Code, rec.Body)
	}
	_ = store.FinishMove("blog", "data", "")

	rec = do(r, call{method: "GET", path: "/api/v1/apps"})
	list := decode[map[string][]AppView](t, rec.Body.Bytes())
	if len(list["apps"]) != 1 || list["apps"][0].Folders[0].Tier != "performance" {
		t.Errorf("list: %s", rec.Body)
	}
	if rec := do(r, call{method: "DELETE", path: "/api/v1/apps/blog"}); rec.Code != http.StatusNoContent {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}
	if rec := do(r, call{method: "GET", path: "/api/v1/apps/blog"}); errCode(rec) != "app_not_found" {
		t.Errorf("after remove: %d %s", rec.Code, rec.Body)
	}
}

func TestAppAccessRules(t *testing.T) {
	store, err := auth.Open(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	appStore, _ := apps.OpenStore(filepath.Join(t.TempDir(), "apps.json"))
	runner := apps.NewRunner(appStore, "", apps.Options{Domain: "nidaos.local"})
	r := NewRouter(Deps{Auth: store, Apps: &AppHosting{Store: appStore, Runner: runner}, Authority: fakeCerts{}})
	_, readSecret, _ := store.CreateToken("viewer", auth.LevelRead)
	bearer := map[string]string{"Authorization": "Bearer " + readSecret}

	// The certificate of the authority needs no sign-in.
	rec := do(r, call{method: "GET", path: "/api/v1/ca.crt"})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "BEGIN CERTIFICATE") {
		t.Errorf("ca.crt: %d %s", rec.Code, rec.Body)
	}
	if rec := do(r, call{method: "GET", path: "/api/v1/apps"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("apps without a sign-in: %d", rec.Code)
	}
	if rec := do(r, call{method: "GET", path: "/api/v1/apps", header: bearer}); rec.Code != http.StatusOK {
		t.Errorf("apps with a read token: %d", rec.Code)
	}
	if rec := do(r, call{method: "POST", path: "/api/v1/apps", body: `{"name":"x"}`, header: bearer}); rec.Code != http.StatusForbidden {
		t.Errorf("create with a read token: %d", rec.Code)
	}
	// Without k3s, a remove waits for the controller.
	_, _ = appStore.Create(apps.App{Name: "x"})
	_, adminSecret, _ := store.CreateToken("admin", auth.LevelAdmin)
	if rec := do(r, call{method: "DELETE", path: "/api/v1/apps/x", header: map[string]string{"Authorization": "Bearer " + adminSecret}}); errCode(rec) != "apps_not_ready" {
		t.Errorf("remove without k3s: %d %s", rec.Code, rec.Body)
	}
}
