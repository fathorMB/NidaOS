package console

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func serve(h *Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestFilesAndPages(t *testing.T) {
	h := NewFS(fstest.MapFS{
		"index.html":       {Data: []byte("<!doctype html><div id=root></div>")},
		"assets/app-1a.js": {Data: []byte("console.log(1)")},
		"favicon.svg":      {Data: []byte("<svg/>")},
	})
	if !h.Built() {
		t.Fatal("Built = false")
	}
	rec := serve(h, "/assets/app-1a.js")
	if rec.Code != 200 || rec.Body.String() != "console.log(1)" {
		t.Fatalf("asset: %d %q", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("asset Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("asset Cache-Control = %q", cc)
	}
	// A page of the console router gives index.html.
	for _, p := range []string{"/", "/apps/blog", "/databases"} {
		rec := serve(h, p)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "id=root") {
			t.Errorf("%s: %d %q", p, rec.Code, rec.Body)
		}
		if rec.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: Cache-Control = %q", p, rec.Header().Get("Cache-Control"))
		}
		if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Errorf("%s: no Content-Security-Policy", p)
		}
	}
	// A missing file is not a page.
	if rec := serve(h, "/assets/missing.js"); rec.Code != http.StatusNotFound {
		t.Errorf("missing file: %d", rec.Code)
	}
	// A path out of the folder stays in the folder.
	if rec := serve(h, "/../../etc/passwd"); !strings.Contains(rec.Body.String(), "id=root") {
		t.Errorf("path out of the folder: %d %q", rec.Code, rec.Body)
	}
}

func TestNotBuilt(t *testing.T) {
	h := NewFS(fstest.MapFS{"README.txt": {Data: []byte("x")}})
	if h.Built() {
		t.Fatal("Built = true")
	}
	rec := serve(h, "/")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "npm run build") {
		t.Fatalf("not built: %d %q", rec.Code, rec.Body)
	}
}

func TestProgramFiles(t *testing.T) {
	// The program always has the folder, with the files or without them.
	if New() == nil {
		t.Fatal("New = nil")
	}
}
