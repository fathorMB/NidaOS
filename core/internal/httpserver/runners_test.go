package httpserver

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fathorMB/NidaOS/core/internal/auth"
	"github.com/fathorMB/NidaOS/core/internal/github"
	"github.com/fathorMB/NidaOS/core/internal/github/githubtest"
	"github.com/fathorMB/NidaOS/core/internal/runners"
)

func runnerRouter(t *testing.T) (*Router, *runners.Store, *githubtest.Server, string, string) {
	t.Helper()
	gh := githubtest.New()
	t.Cleanup(gh.Close)
	store, err := runners.OpenStore(filepath.Join(t.TempDir(), "runners.json"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &runners.Service{Store: store, GitHub: github.New(gh.URL, gh.URL, store.App),
		AppExists: func(a string) bool { return a == "shop" }}
	authStore, _ := auth.Open(filepath.Join(t.TempDir(), "auth.json"))
	_, admin, _ := authStore.CreateToken("admin", auth.LevelAdmin)
	_, reader, _ := authStore.CreateToken("reader", auth.LevelRead)
	r := NewRouter(Deps{Auth: authStore, Runners: &RunnerHosting{Service: svc, PublicURL: "http://nidaos.local:8470"}})
	return r, store, gh, admin, reader
}

func bearer(tok string) map[string]string { return map[string]string{"Authorization": "Bearer " + tok} }

func TestGitHubSetupThroughTheAPI(t *testing.T) {
	r, store, gh, admin, reader := runnerRouter(t)

	if rec := do(r, call{method: "POST", path: "/api/v1/github/setup", header: bearer(reader)}); rec.Code != http.StatusForbidden {
		t.Fatalf("setup with read: %d", rec.Code)
	}
	rec := do(r, call{method: "POST", path: "/api/v1/github/setup", header: bearer(admin)})
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body)
	}
	var st GitHubSetup
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	path := strings.TrimPrefix(st.URL, "http://nidaos.local:8470")
	if !strings.HasPrefix(path, "/api/v1/github/setup/") {
		t.Fatalf("url = %s", st.URL)
	}
	key := strings.TrimPrefix(path, "/api/v1/github/setup/")

	// The browser has no session: the key is the access.
	rec = do(r, call{method: "GET", path: path})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "settings/apps/new?state="+key) {
		t.Fatalf("page: %d %s", rec.Code, rec.Body)
	}
	if rec := do(r, call{method: "GET", path: "/api/v1/github/setup/wrong"}); rec.Code != http.StatusNotFound || errCode(rec) != "setup_expired" {
		t.Fatalf("wrong key: %d %s", rec.Code, rec.Body)
	}

	gh.Code("c0de")
	rec = do(r, call{method: "GET", path: "/api/v1/github/callback?code=c0de&state=" + key})
	if rec.Code != http.StatusFound || !strings.HasSuffix(rec.Header().Get("Location"), "/apps/nidaos-test/installations/new") {
		t.Fatalf("callback: %d %v", rec.Code, rec.Header())
	}
	rec = do(r, call{method: "GET", path: "/api/v1/github", header: bearer(reader)})
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "PRIVATE") || !strings.Contains(rec.Body.String(), `"configured":true`) {
		t.Fatalf("view: %d %s", rec.Code, rec.Body)
	}
	if _, ok := store.App(); !ok {
		t.Fatal("the store has no app")
	}
}

func TestRepositoryAndJobsThroughTheAPI(t *testing.T) {
	r, store, gh, admin, reader := runnerRouter(t)
	gh.Repo("me/shop", true)
	gh.Repo("me/open", false)

	rec := do(r, call{method: "PUT", path: "/api/v1/apps/shop/repository", body: `{"repository":"me/shop"}`, header: bearer(admin)})
	if rec.Code != http.StatusConflict || errCode(rec) != "github_not_configured" {
		t.Fatalf("no app: %d %s", rec.Code, rec.Body)
	}
	body, _ := json.Marshal(map[string]any{"app_id": gh.App.ID, "private_key": gh.App.PEM})
	if rec := do(r, call{method: "POST", path: "/api/v1/github/import", body: string(body), header: bearer(admin)}); rec.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	rec = do(r, call{method: "PUT", path: "/api/v1/apps/shop/repository", body: `{"repository":"me/open"}`, header: bearer(admin)})
	if rec.Code != http.StatusConflict || errCode(rec) != "repository_public" {
		t.Fatalf("public: %d %s", rec.Code, rec.Body)
	}
	rec = do(r, call{method: "PUT", path: "/api/v1/apps/shop/repository", body: `{"repository":"me"}`, header: bearer(admin)})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad name: %d %s", rec.Code, rec.Body)
	}
	rec = do(r, call{method: "PUT", path: "/api/v1/apps/shop/repository", body: `{"repository":"me/shop"}`, header: bearer(admin)})
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), "/me/shop/pull/1") {
		t.Fatalf("connect: %d %s", rec.Code, rec.Body)
	}
	if rec := do(r, call{method: "GET", path: "/api/v1/apps/shop/repository", header: bearer(reader)}); rec.Code != http.StatusOK {
		t.Fatalf("get: %d", rec.Code)
	}

	_ = store.AddJob(runners.Job{ID: 5, App: "shop", State: runners.JobSucceeded})
	_ = store.WriteLog(5, []byte("deploy passed\n"))
	rec = do(r, call{method: "GET", path: "/api/v1/apps/shop/jobs", header: bearer(reader)})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":5`) {
		t.Fatalf("jobs: %d %s", rec.Code, rec.Body)
	}
	rec = do(r, call{method: "GET", path: "/api/v1/jobs/5/log", header: bearer(reader)})
	if rec.Code != http.StatusOK || rec.Body.String() != "deploy passed\n" {
		t.Fatalf("log: %d %s", rec.Code, rec.Body)
	}
	if rec := do(r, call{method: "GET", path: "/api/v1/jobs/6", header: bearer(reader)}); errCode(rec) != "job_not_found" {
		t.Fatalf("missing job: %s", rec.Body)
	}
	rec = do(r, call{method: "GET", path: "/api/v1/runners", header: bearer(reader)})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"repo":"shop"`) {
		t.Fatalf("runners: %d %s", rec.Code, rec.Body)
	}
	if rec := do(r, call{method: "DELETE", path: "/api/v1/apps/shop/repository", header: bearer(admin)}); rec.Code != http.StatusNoContent {
		t.Fatalf("disconnect: %d %s", rec.Code, rec.Body)
	}
}
