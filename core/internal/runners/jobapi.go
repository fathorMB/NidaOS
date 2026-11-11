package runners

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/fathorMB/NidaOS/core/internal/auth"
)

// JobAPI is the job endpoint of nida-core for the runner pods (T-09). It
// listens on the address of the pod network of the computer. Each request
// needs a job token, and the token gives only:
//   - the registry (D21), only for the image of its app;
//   - the deploy of its app, and the state of its app.
//
// GET /tools/nida gives the nida command; it needs no token.
type JobAPI struct {
	Tokens *Tokens
	// API is the router of nida-core. JobAPI gives it the requests that it
	// accepts, with an identity of the admin level.
	API http.Handler
	// WithIdentity gives a request an identity, as httpserver.WithIdentity.
	WithIdentity func(http.Handler, auth.Identity) http.Handler
	// Registry is the address of the local registry, for example
	// http://127.0.0.1:5000.
	Registry *url.URL
	// RegistryName is the name of the registry in the image names of a
	// deploy, for example localhost:5000.
	RegistryName string
	// NidaBinary is the file of the nida command, for example /usr/bin/nida.
	NidaBinary string
}

var (
	deployPath = regexp.MustCompile(`^/api/v1/apps/([a-z][a-z0-9-]*)/deploys$`)
	appPath    = regexp.MustCompile(`^/api/v1/apps/([a-z][a-z0-9-]*)$`)
	// The image of the app, and only it: /v2/<app>/(blobs|manifests|tags)/...
	registryPath = regexp.MustCompile(`^/v2/([a-z][a-z0-9-]*)/(blobs|manifests|tags)/`)
)

func jobError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": msg})
}

// token reads the job token: a bearer token, or the password of the basic
// authentication of the registry clients.
func token(req *http.Request) string {
	if _, pw, ok := req.BasicAuth(); ok {
		return pw
	}
	if t, ok := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer "); ok {
		return strings.TrimSpace(t)
	}
	return ""
}

func (j *JobAPI) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	p := req.URL.Path
	if p == "/tools/nida" && req.Method == http.MethodGet {
		j.serveNida(w, req)
		return
	}
	id, ok := j.Tokens.Check(token(req))
	if !ok {
		if strings.HasPrefix(p, "/v2/") {
			// The registry clients first ask without a password.
			w.Header().Set("WWW-Authenticate", `Basic realm="nidaos"`)
			w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
		}
		jobError(w, http.StatusUnauthorized, "job_token_required", "Give the job token of the runner pod.")
		return
	}

	switch {
	case p == "/v2/" || p == "/v2":
		j.proxy(w, req)
	case strings.HasPrefix(p, "/v2/"):
		m := registryPath.FindStringSubmatch(p)
		if m == nil || m[1] != id.App {
			jobError(w, http.StatusForbidden, "forbidden", "A job can push only the image of its app: "+id.App+".")
			return
		}
		// A blob mount copies a layer from an other image: only from the
		// image of the app.
		if from := req.URL.Query().Get("from"); from != "" && from != id.App {
			jobError(w, http.StatusForbidden, "forbidden", "A job can use only the image of its app.")
			return
		}
		j.proxy(w, req)
	case req.Method == http.MethodPost && deployPath.MatchString(p):
		if deployPath.FindStringSubmatch(p)[1] != id.App {
			jobError(w, http.StatusForbidden, "forbidden", "A job can deploy only its app: "+id.App+".")
			return
		}
		if !j.checkImage(w, req, id.App) {
			return
		}
		j.api(w, req, id)
	case req.Method == http.MethodGet && appPath.MatchString(p):
		if appPath.FindStringSubmatch(p)[1] != id.App {
			jobError(w, http.StatusForbidden, "forbidden", "A job can read only its app: "+id.App+".")
			return
		}
		j.api(w, req, id)
	default:
		jobError(w, http.StatusForbidden, "forbidden", "The job endpoint does not give this operation.")
	}
}

// checkImage reads the body of a deploy and accepts only the image of the
// app in the local registry.
func (j *JobAPI) checkImage(w http.ResponseWriter, req *http.Request, app string) bool {
	body, err := io.ReadAll(io.LimitReader(req.Body, 64<<10))
	if err != nil {
		jobError(w, http.StatusBadRequest, "bad_body", "The body is not available.")
		return false
	}
	var b struct {
		Image string `json:"image"`
	}
	_ = json.Unmarshal(body, &b)
	prefix := j.RegistryName + "/" + app
	if !strings.HasPrefix(b.Image, prefix+":") && !strings.HasPrefix(b.Image, prefix+"@") {
		jobError(w, http.StatusForbidden, "forbidden", "A job can deploy only the image "+prefix+".")
		return false
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	return true
}

func (j *JobAPI) api(w http.ResponseWriter, req *http.Request, id JobIdentity) {
	req.Header.Del("Authorization")
	ident := auth.Identity{Kind: auth.KindToken, Name: "job of " + id.App + " (" + id.Pod + ")", Level: auth.LevelAdmin}
	j.WithIdentity(j.API, ident).ServeHTTP(w, req)
}

func (j *JobAPI) proxy(w http.ResponseWriter, req *http.Request) {
	req.Header.Del("Authorization")
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(j.Registry)
			// The registry makes the upload addresses from the host of the
			// request: they must come back to the job endpoint.
			pr.Out.Host = pr.In.Host
		},
		FlushInterval: -1,
	}
	rp.ServeHTTP(w, req)
}

func (j *JobAPI) serveNida(w http.ResponseWriter, req *http.Request) {
	f, err := os.Open(j.NidaBinary)
	if err != nil {
		jobError(w, http.StatusNotFound, "not_found", "The nida command is not available.")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		jobError(w, http.StatusNotFound, "not_found", "The nida command is not available.")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, req, "nida", st.ModTime(), f)
}
