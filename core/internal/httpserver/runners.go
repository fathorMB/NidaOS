package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/github"
	"github.com/fathorMB/NidaOS/core/internal/runners"
)

// RunnerHosting gives the runner API its parts (milestone M-05).
type RunnerHosting struct {
	Service *runners.Service
	// PublicURL is the address of nida-core in the browser of the owner,
	// for example http://nidaos.local:8470. GitHub sends the browser back
	// to it at the end of the setup.
	PublicURL string
}

func (r *Router) addRunnerRoutes() {
	if r.runners == nil {
		return
	}
	r.handle(http.MethodGet, "/api/v1/github", r.githubGet)
	r.handle(http.MethodDelete, "/api/v1/github", r.githubRemove)
	r.handle(http.MethodPost, "/api/v1/github/setup", r.githubSetup)
	r.handle(http.MethodGet, "/api/v1/github/setup/{key}", r.githubSetupPage)
	r.handle(http.MethodGet, "/api/v1/github/callback", r.githubCallback)
	r.handle(http.MethodPost, "/api/v1/github/import", r.githubImport)
	r.handle(http.MethodGet, "/api/v1/runners", r.runnersList)
	r.handle(http.MethodGet, "/api/v1/apps/{name}/repository", r.repoGet)
	r.handle(http.MethodPut, "/api/v1/apps/{name}/repository", r.repoConnect)
	r.handle(http.MethodDelete, "/api/v1/apps/{name}/repository", r.repoDisconnect)
	r.handle(http.MethodPost, "/api/v1/apps/{name}/repository/pull-request", r.repoPullRequest)
	r.handle(http.MethodGet, "/api/v1/apps/{name}/jobs", r.jobsList)
	r.handle(http.MethodGet, "/api/v1/jobs/{id}", r.jobGet)
	r.handle(http.MethodGet, "/api/v1/jobs/{id}/log", r.jobLog)
}

func (r *Router) writeRunnerError(w http.ResponseWriter, err error) {
	var fe *runners.FieldError
	var ae *github.APIError
	switch {
	case errors.As(err, &fe):
		writeError(w, http.StatusBadRequest, "bad_field", fe.Error()+".")
	case errors.Is(err, github.ErrNoApp):
		writeError(w, http.StatusConflict, "github_not_configured", "NidaOS has no GitHub App. Run: nida github setup.")
	case errors.Is(err, github.ErrNotInstalled):
		writeError(w, http.StatusConflict, "github_not_installed", "The GitHub App of NidaOS is not installed on the repository. Install it on GitHub and select the repository.")
	case errors.Is(err, github.ErrNotFound):
		writeError(w, http.StatusNotFound, "github_not_found", "GitHub does not have the object.")
	case errors.Is(err, runners.ErrNotPrivate):
		writeError(w, http.StatusConflict, "repository_public", "The repository is public. NidaOS connects runners only to private repositories (decision D27).")
	case errors.Is(err, runners.ErrNoAppHere):
		writeError(w, http.StatusNotFound, "app_not_found", "The app does not exist.")
	case errors.Is(err, runners.ErrConnected):
		writeError(w, http.StatusConflict, "repository_connected", "The app has a connected repository. Disconnect it first.")
	case errors.Is(err, runners.ErrRepoInUse):
		writeError(w, http.StatusConflict, "repository_in_use", "An other app uses this repository.")
	case errors.Is(err, runners.ErrNotConnected):
		writeError(w, http.StatusNotFound, "repository_not_connected", "The app has no connected repository.")
	case errors.Is(err, runners.ErrJobNotFound):
		writeError(w, http.StatusNotFound, "job_not_found", "The job does not exist.")
	case errors.Is(err, runners.ErrSetupExpired):
		writeError(w, http.StatusNotFound, "setup_expired", "The setup of the GitHub App is not valid. Start it again with: nida github setup.")
	case errors.As(err, &ae):
		writeError(w, http.StatusBadGateway, "github_error", ae.Error())
	default:
		r.logger.Error("a runner operation failed", "err", err)
		writeError(w, http.StatusBadGateway, "github_unavailable", err.Error())
	}
}

func (r *Router) githubGet(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, r.runners.Service.AppView())
}

func (r *Router) githubRemove(w http.ResponseWriter, _ *http.Request) {
	if err := r.runners.Service.RemoveApp(); err != nil {
		r.writeRunnerError(w, err)
		return
	}
	r.logger.Info("the GitHub App was removed from NidaOS")
	w.WriteHeader(http.StatusNoContent)
}

// GitHubSetup is the answer of POST /api/v1/github/setup.
type GitHubSetup struct {
	URL     string    `json:"url"`
	Expires time.Time `json:"expires"`
}

func (r *Router) githubSetup(w http.ResponseWriter, req *http.Request) {
	var b struct {
		Organization string `json:"organization"`
	}
	if req.ContentLength != 0 && !readAppJSON(w, req, &b) {
		return
	}
	st, err := r.runners.Service.StartSetup()
	if err != nil {
		r.writeRunnerError(w, err)
		return
	}
	u := r.runners.PublicURL + "/api/v1/github/setup/" + st.Key
	if b.Organization != "" {
		u += "?org=" + url.QueryEscape(b.Organization)
	}
	writeJSON(w, http.StatusCreated, GitHubSetup{URL: u, Expires: st.Expires})
}

// githubSetupPage needs no sign-in: the key of the setup is the access.
func (r *Router) githubSetupPage(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action https:")
	err := r.runners.Service.SetupPage(w, req.PathValue("key"), r.runners.PublicURL, req.URL.Query().Get("org"))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		r.writeRunnerError(w, err)
	}
}

// githubCallback needs no sign-in: the state is the key of the setup.
func (r *Router) githubCallback(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	ctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
	defer cancel()
	install, err := r.runners.Service.Callback(ctx, q.Get("code"), q.Get("state"))
	if err != nil {
		r.writeRunnerError(w, err)
		return
	}
	r.logger.Info("the GitHub App of NidaOS was made")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, req, install, http.StatusFound)
}

func (r *Router) githubImport(w http.ResponseWriter, req *http.Request) {
	var b struct {
		AppID      int64  `json:"app_id"`
		PrivateKey string `json:"private_key"`
	}
	if !readAppJSON(w, req, &b) {
		return
	}
	if b.AppID <= 0 {
		r.writeRunnerError(w, &runners.FieldError{Field: "app_id", Message: "give the ID of the GitHub App"})
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
	defer cancel()
	v, err := r.runners.Service.Import(ctx, b.AppID, b.PrivateKey)
	if err != nil {
		r.writeRunnerError(w, err)
		return
	}
	r.logger.Info("a GitHub App was imported", "app_id", v.ID, "slug", v.Slug)
	writeJSON(w, http.StatusOK, v)
}

// RunnersView is the answer of GET /api/v1/runners.
type RunnersView struct {
	GitHub      runners.AppView      `json:"github"`
	Connections []runners.Connection `json:"connections"`
	// Active are the jobs that wait or run now.
	Active []runners.Job `json:"active"`
}

func (r *Router) runnersList(w http.ResponseWriter, _ *http.Request) {
	s := r.runners.Service.Store
	v := RunnersView{GitHub: r.runners.Service.AppView(), Connections: s.Connections(), Active: []runners.Job{}}
	if v.Connections == nil {
		v.Connections = []runners.Connection{}
	}
	for _, j := range s.Jobs("") {
		if !j.Final() {
			v.Active = append(v.Active, j)
		}
	}
	writeJSON(w, http.StatusOK, v)
}

func (r *Router) repoGet(w http.ResponseWriter, req *http.Request) {
	c, ok := r.runners.Service.Store.Connection(req.PathValue("name"))
	if !ok {
		r.writeRunnerError(w, runners.ErrNotConnected)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (r *Router) repoConnect(w http.ResponseWriter, req *http.Request) {
	var b runners.ConnectRequest
	if !readAppJSON(w, req, &b) {
		return
	}
	b.App = req.PathValue("name")
	ctx, cancel := context.WithTimeout(req.Context(), 60*time.Second)
	defer cancel()
	c, err := r.runners.Service.Connect(ctx, b)
	if err != nil && c.App == "" {
		r.writeRunnerError(w, err)
		return
	}
	if err != nil {
		// Connected, but the pull request failed.
		r.logger.Warn("the pull request of the workflow failed", "app", b.App, "err", err)
		writeJSON(w, http.StatusCreated, map[string]any{"connection": c, "warning": err.Error()})
		return
	}
	r.logger.Info("a repository was connected", "app", c.App, "repo", c.FullName())
	writeJSON(w, http.StatusCreated, map[string]any{"connection": c})
}

func (r *Router) repoDisconnect(w http.ResponseWriter, req *http.Request) {
	ctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
	defer cancel()
	if err := r.runners.Service.Disconnect(ctx, req.PathValue("name")); err != nil {
		r.writeRunnerError(w, err)
		return
	}
	r.logger.Info("a repository was disconnected", "app", req.PathValue("name"))
	w.WriteHeader(http.StatusNoContent)
}

func (r *Router) repoPullRequest(w http.ResponseWriter, req *http.Request) {
	ctx, cancel := context.WithTimeout(req.Context(), 60*time.Second)
	defer cancel()
	c, err := r.runners.Service.OpenPullRequest(ctx, req.PathValue("name"))
	if err != nil {
		r.writeRunnerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (r *Router) jobsList(w http.ResponseWriter, req *http.Request) {
	jobs := r.runners.Service.Store.Jobs(req.PathValue("name"))
	if jobs == nil {
		jobs = []runners.Job{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (r *Router) job(w http.ResponseWriter, req *http.Request) (runners.Job, bool) {
	id, err := strconv.ParseInt(req.PathValue("id"), 10, 64)
	if err != nil {
		r.writeRunnerError(w, runners.ErrJobNotFound)
		return runners.Job{}, false
	}
	j, ok := r.runners.Service.Store.Job(id)
	if !ok {
		r.writeRunnerError(w, runners.ErrJobNotFound)
	}
	return j, ok
}

func (r *Router) jobGet(w http.ResponseWriter, req *http.Request) {
	if j, ok := r.job(w, req); ok {
		writeJSON(w, http.StatusOK, j)
	}
}

func (r *Router) jobLog(w http.ResponseWriter, req *http.Request) {
	j, ok := r.job(w, req)
	if !ok {
		return
	}
	b, err := os.ReadFile(r.runners.Service.Store.LogPath(j.ID))
	if err != nil {
		writeError(w, http.StatusNotFound, "log_not_available", "The log is not available yet: NidaOS gets it from GitHub at the end of the job.")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(b)
}
