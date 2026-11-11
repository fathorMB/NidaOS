package httpserver

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fathorMB/NidaOS/core/internal/version"
)

func newTestRouter() *Router {
	return NewRouter(Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), State: &fakeState{}})
}

func TestHealthPaths(t *testing.T) {
	r := newTestRouter()
	for _, path := range []string{"/healthz", "/readyz", "/api/v1/health"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s: Content-Type = %q", path, ct)
		}
		var h Health
		if err := json.NewDecoder(rec.Body).Decode(&h); err != nil {
			t.Fatalf("%s: decode: %v", path, err)
		}
		if h.Status != "ok" || h.Version != version.Version {
			t.Errorf("%s: body = %+v", path, h)
		}
	}
}

func TestUnknownPath(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/nothing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var e Error
	if err := json.NewDecoder(rec.Body).Decode(&e); err != nil || e.Code != "not_found" {
		t.Errorf("body = %+v, err = %v", e, err)
	}
}

func TestWrongMethod(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/healthz", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != http.MethodGet {
		t.Errorf("Allow = %q, want GET", allow)
	}
}
