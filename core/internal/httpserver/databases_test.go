package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/fathorMB/NidaOS/core/internal/auth"
	"github.com/fathorMB/NidaOS/core/internal/databases"
)

type nopSQL struct{}

func (nopSQL) Exec(context.Context, string, string, string) error { return nil }
func (nopSQL) Query(context.Context, string, string, string) ([]string, error) {
	return []string{"1"}, nil
}

type nopPod struct{}

func (nopPod) Exec(_ context.Context, _, _, _ string, _ []string, _ io.Reader, out io.Writer) error {
	_, err := io.WriteString(out, "PGDMP")
	return err
}

func dbRouter(t *testing.T, withController bool) (*Router, *databases.Store, *auth.Store) {
	t.Helper()
	store, err := databases.OpenStore(filepath.Join(t.TempDir(), "databases.json"))
	if err != nil {
		t.Fatal(err)
	}
	opts := databases.Options{
		Images:    map[int]string{17: "docker.io/library/postgres:17.7-alpine", 18: "docker.io/library/postgres:18.1-alpine"},
		BackupDir: t.TempDir(),
	}
	runner := databases.NewRunner(store, "", nil, opts)
	if withController {
		runner.SetController(databases.NewController(store, fake.NewClientset(), nopPod{}, nopSQL{}, opts))
	}
	authStore, _ := auth.Open(filepath.Join(t.TempDir(), "auth.json"))
	r := NewRouter(Deps{Auth: authStore, Databases: &DatabaseHosting{Runner: runner, Host: "nidaos.local"}})
	return r, store, authStore
}

func TestDatabaseAPI(t *testing.T) {
	r, store, authStore := dbRouter(t, true)
	_, admin, _ := authStore.CreateToken("admin", auth.LevelAdmin)
	_, reader, _ := authStore.CreateToken("reader", auth.LevelRead)
	as := func(token string) map[string]string { return map[string]string{"Authorization": "Bearer " + token} }

	rec := do(r, call{method: "POST", path: "/api/v1/databases", body: `{"name":"shop-db"}`, header: as(admin)})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	v := decode[DatabaseView](t, rec.Body.Bytes())
	if v.Version != 18 || v.Tier != databases.TierPerformance || v.State != databases.StateCreating {
		t.Errorf("created %+v", v)
	}
	if rec := do(r, call{method: "POST", path: "/api/v1/databases", body: `{"name":"old","version":16}`, header: as(admin)}); errCode(rec) != "bad_field" || !strings.Contains(rec.Body.String(), "17 and 18") {
		t.Errorf("version 16: %d %s", rec.Code, rec.Body)
	}
	if rec := do(r, call{method: "POST", path: "/api/v1/databases", body: `{"name":"x"}`, header: as(reader)}); rec.Code != http.StatusForbidden {
		t.Errorf("create with a read token: %d", rec.Code)
	}

	rec = do(r, call{method: "GET", path: "/api/v1/databases", header: as(reader)})
	var list struct {
		Databases []DatabaseView `json:"databases"`
		Versions  []int          `json:"versions"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Databases) != 1 || len(list.Versions) != 2 || list.Versions[0] != 17 {
		t.Errorf("list %s", rec.Body)
	}

	// The connection has the owner password: a read token cannot get it.
	if rec := do(r, call{method: "GET", path: "/api/v1/databases/shop-db/connection", header: as(reader)}); rec.Code != http.StatusForbidden {
		t.Errorf("connection with a read token: %d", rec.Code)
	}
	rec = do(r, call{method: "GET", path: "/api/v1/databases/shop-db/connection", header: as(admin)})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), store.OwnerPassword("shop-db")) {
		t.Errorf("connection: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(do(r, call{method: "GET", path: "/api/v1/databases/shop-db", header: as(reader)}).Body.String(), store.OwnerPassword("shop-db")) {
		t.Error("the view of a database has the password")
	}

	// A database that is not ready refuses the switch and the backups.
	if rec := do(r, call{method: "PUT", path: "/api/v1/databases/shop-db/lan", body: `{"on":true}`, header: as(admin)}); errCode(rec) != "database_busy" {
		t.Errorf("LAN before ready: %d %s", rec.Code, rec.Body)
	}
	_ = store.SetState("shop-db", databases.StateReady, "")
	rec = do(r, call{method: "PUT", path: "/api/v1/databases/shop-db/lan", body: `{"on":true}`, header: as(admin)})
	if rec.Code != http.StatusOK || decode[DatabaseView](t, rec.Body.Bytes()).Address != "nidaos.local:15432" {
		t.Errorf("LAN on: %d %s", rec.Code, rec.Body)
	}
	if rec := do(r, call{method: "POST", path: "/api/v1/databases/shop-db/update", header: as(admin)}); errCode(rec) != "no_update" {
		t.Errorf("update: %d %s", rec.Code, rec.Body)
	}
	if rec := do(r, call{method: "POST", path: "/api/v1/databases/shop-db/links", body: `{"app":"shop"}`, header: as(admin)}); errCode(rec) != "apps_not_ready" {
		t.Errorf("link without apps: %d %s", rec.Code, rec.Body)
	}
	if rec := do(r, call{method: "GET", path: "/api/v1/databases/shop-db/backups", header: as(reader)}); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"backups":[]`) {
		t.Errorf("backups: %d %s", rec.Code, rec.Body)
	}
	if rec := do(r, call{method: "DELETE", path: "/api/v1/databases/shop-db", header: as(admin)}); rec.Code != http.StatusNoContent {
		t.Errorf("remove: %d %s", rec.Code, rec.Body)
	}
	if rec := do(r, call{method: "GET", path: "/api/v1/databases/shop-db", header: as(reader)}); errCode(rec) != "database_not_found" {
		t.Errorf("after remove: %d", rec.Code)
	}
}

func TestDatabaseAPIWithoutK3s(t *testing.T) {
	r, _, authStore := dbRouter(t, false)
	_, admin, _ := authStore.CreateToken("admin", auth.LevelAdmin)
	h := map[string]string{"Authorization": "Bearer " + admin}
	// A new database waits for the controller.
	if rec := do(r, call{method: "POST", path: "/api/v1/databases", body: `{"name":"a"}`, header: h}); rec.Code != http.StatusAccepted {
		t.Errorf("create without k3s: %d %s", rec.Code, rec.Body)
	}
	if rec := do(r, call{method: "DELETE", path: "/api/v1/databases/a", header: h}); errCode(rec) != "databases_not_ready" {
		t.Errorf("remove without k3s: %d %s", rec.Code, rec.Body)
	}
}
