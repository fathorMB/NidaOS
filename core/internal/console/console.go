// Package console serves the files of the web console (milestone M-06).
//
// The build of console/ (React and Vite, decision D10) writes the files in
// dist/. The image build makes them before it builds nida-core, thus the
// program has the console of its own version (decision D29). A build
// without the files serves a short page that tells how to make them.
package console

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var files embed.FS

// Handler serves the console. Each path without a file gives index.html,
// thus the router of the console in the browser finds the page.
type Handler struct {
	fs    fs.FS
	page  string
	index []byte
}

// New gives the handler of the files in the program.
func New() *Handler {
	sub, err := fs.Sub(files, "dist")
	if err != nil {
		panic(err) // dist is in the program
	}
	return NewFS(sub)
}

// NewFS gives a handler for the files of fsys. It is for the tests.
func NewFS(fsys fs.FS) *Handler { return NewFSIndex(fsys, "index.html") }

// NewFSIndex gives a handler with an other page for the paths without a
// file, for example installer.html for the installer (M-07).
func NewFSIndex(fsys fs.FS, index string) *Handler {
	h := &Handler{fs: fsys, page: index}
	if b, err := fs.ReadFile(fsys, index); err == nil {
		h.index = b
	}
	return h
}

// Built tells if the program has the files of the console.
func (h *Handler) Built() bool { return h.index != nil }

// notBuilt is the page of a program without the console files.
const notBuilt = `<!doctype html><html lang="en"><meta charset="utf-8"><title>NidaOS</title>
<body style="font:15px/1.5 system-ui,sans-serif;margin:40px;max-width:60ch">
<h1>NidaOS</h1><p>This build of nida-core has no web console. Build it with
<code>npm ci &amp;&amp; npm run build</code> in the folder <code>console</code>,
then build nida-core again. The API is at <code>/api/v1/</code>.</p></body></html>`

// ServeHTTP serves one file, or index.html.
func (h *Handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	securityHeaders(w.Header())
	name := strings.TrimPrefix(path.Clean("/"+req.URL.Path), "/")
	if name != "" && name != h.page {
		if b, err := fs.ReadFile(h.fs, name); err == nil {
			if strings.HasPrefix(name, "assets/") {
				// Vite puts a hash in the names of these files.
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			w.Header().Set("Content-Type", contentType(name))
			if req.Method != http.MethodHead {
				_, _ = w.Write(b)
			}
			return
		}
		// A missing file with an extension is a missing file, not a page.
		if path.Ext(name) != "" {
			http.NotFound(w, req)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	body := h.index
	if body == nil {
		body = []byte(notBuilt)
	}
	if req.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// securityHeaders stops the scripts of other sites and the frames.
func securityHeaders(hd http.Header) {
	hd.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("X-Frame-Options", "DENY")
	hd.Set("Referrer-Policy", "same-origin")
}

func contentType(name string) string {
	switch path.Ext(name) {
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".html":
		return "text/html; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".ico":
		return "image/x-icon"
	case ".json", ".webmanifest":
		return "application/json"
	case ".woff2":
		return "font/woff2"
	case ".txt":
		return "text/plain; charset=utf-8"
	}
	return "application/octet-stream"
}
