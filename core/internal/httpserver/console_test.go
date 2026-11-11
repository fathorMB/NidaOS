package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fathorMB/NidaOS/core/internal/console"
	"github.com/fathorMB/NidaOS/core/internal/updates"
)

func consoleRouter(t *testing.T) *Router {
	t.Helper()
	r, _ := authRouter(t, true)
	r.console = console.NewFS(fstest.MapFS{"index.html": {Data: []byte("<div id=root></div>")}})
	return r
}

func TestConsoleIsPublicAndTheAPIIsNot(t *testing.T) {
	r := consoleRouter(t)
	for _, p := range []string{"/", "/apps/blog", "/logs"} {
		rec := do(r, call{method: http.MethodGet, path: p})
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "id=root") {
			t.Errorf("GET %s: %d %q", p, rec.Code, rec.Body)
		}
	}
	// The API keeps its access rules and its JSON errors.
	if rec := do(r, call{method: http.MethodGet, path: "/api/v1/state"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/v1/state: %d", rec.Code)
	}
	if rec := do(r, call{method: http.MethodGet, path: "/api/v1/nothing"}); rec.Code != http.StatusUnauthorized && errCode(rec) != "not_found" {
		t.Errorf("GET /api/v1/nothing: %d %s", rec.Code, errCode(rec))
	}
	// A change out of the API needs the admin level, and has no handler.
	if rec := do(r, call{method: http.MethodPost, path: "/apps"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("POST /apps: %d", rec.Code)
	}
}

func TestClientAddrThroughTraefik(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req.RemoteAddr = "10.42.0.9:41000"
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 192.168.1.20")
	if got := clientAddr(req); got != "192.168.1.20" {
		t.Errorf("from Traefik: %q", got)
	}
	// A device of the home network cannot give an other address.
	req.RemoteAddr = "192.168.1.30:5000"
	if got := clientAddr(req); got != "192.168.1.30" {
		t.Errorf("from the home network: %q", got)
	}
}

func TestConsoleSessionThroughTraefik(t *testing.T) {
	r := consoleRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"password":"`+ownerPassword+`"}`))
	req.Host = "nidaos.local"
	req.RemoteAddr = "10.42.0.9:41000"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Origin", "https://nidaos.local")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	c := rec.Result().Cookies()
	if len(c) != 1 || !c[0].Secure || !c[0].HttpOnly {
		t.Fatalf("cookie = %+v", c)
	}
}

func TestUpdatePaths(t *testing.T) {
	out := `{"status":{"booted":{"image":{"image":{"image":"ghcr.io/x/nidaos:proof"},"version":"45.1","imageDigest":"sha256:aa"}},"staged":null,"rollback":null}}`
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "bootc" && args[0] == "status" {
			return []byte(out), nil
		}
		return nil, nil
	}
	r := NewRouter(Deps{State: &fakeState{}, Updates: &UpdateHosting{Service: updates.New(context.Background(), run, nil)}})
	rec := do(r, call{method: http.MethodGet, path: "/api/v1/updates"})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"version":"45.1"`) {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body)
	}
	if rec := do(r, call{method: http.MethodPost, path: "/api/v1/updates/restart"}); rec.Code != http.StatusConflict || errCode(rec) != "nothing_to_install" {
		t.Errorf("restart: %d %s", rec.Code, errCode(rec))
	}
	if rec := do(r, call{method: http.MethodPost, path: "/api/v1/updates/rollback", body: `{"restart":true}`}); errCode(rec) != "no_rollback" {
		t.Errorf("rollback: %d %s", rec.Code, errCode(rec))
	}
	if rec := do(r, call{method: http.MethodPost, path: "/api/v1/updates/check"}); rec.Code != http.StatusAccepted {
		t.Errorf("check: %d %s", rec.Code, rec.Body)
	}
}
