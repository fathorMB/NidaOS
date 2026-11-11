package runners

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/url"
	"strings"
	"sync"

	"github.com/fathorMB/NidaOS/core/internal/github"
)

// Errors of the service.
var (
	ErrNotPrivate = errors.New("the repository is public: NidaOS connects runners only to private repositories (decision D27)")
	ErrNoAppHere  = errors.New("the app does not exist")
)

// Service is the runner part of nida-core for the API.
type Service struct {
	Store  *Store
	GitHub *github.Client
	// AppExists tells if an app of NidaOS is there.
	AppExists func(string) bool
	// Controller gives the controller, or nil when k3s does not run yet.
	Controller func() *Controller

	mu sync.Mutex
}

// AppView is the public data of the GitHub App: no private key.
type AppView struct {
	Configured bool   `json:"configured"`
	ID         int64  `json:"id,omitempty"`
	Slug       string `json:"slug,omitempty"`
	Name       string `json:"name,omitempty"`
	Owner      string `json:"owner,omitempty"`
	HTMLURL    string `json:"html_url,omitempty"`
	InstallURL string `json:"install_url,omitempty"`
}

// AppView gives the public data of the GitHub App.
func (s *Service) AppView() AppView {
	a, ok := s.Store.App()
	if !ok {
		return AppView{}
	}
	return AppView{Configured: true, ID: a.ID, Slug: a.Slug, Name: a.Name, Owner: a.Owner, HTMLURL: a.HTMLURL,
		InstallURL: s.GitHub.InstallURL(a)}
}

// StartSetup starts the manifest flow. It gives the key of the setup page.
func (s *Service) StartSetup() (Setup, error) {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return s.Store.StartSetup(hex.EncodeToString(b))
}

var setupPage = template.Must(template.New("setup").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>NidaOS - GitHub App</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>body{font-family:system-ui,sans-serif;max-width:40rem;margin:3rem auto;padding:0 1rem;line-height:1.5}
button{font-size:1rem;padding:.6rem 1.2rem}</style></head>
<body><h1>Make the GitHub App of NidaOS</h1>
<p>GitHub makes a private app for this NidaOS computer. The app has no webhook. After this step, select the private repositories of the app on GitHub.</p>
<form method="post" action="{{.Action}}">
<input type="hidden" name="manifest" value="{{.Manifest}}">
<button type="submit">Continue on GitHub</button>
</form></body></html>
`))

// SetupPage writes the page that sends the manifest to GitHub. base is the
// address of nida-core in the browser, for example http://nidaos.local:8470.
// org is empty for a personal account.
func (s *Service) SetupPage(w io.Writer, key, base, org string) error {
	if err := s.Store.CheckSetup(key); err != nil {
		return err
	}
	base = strings.TrimRight(base, "/")
	host := "nidaos"
	if u, err := url.Parse(base); err == nil && u.Hostname() != "" {
		host = strings.Split(u.Hostname(), ".")[0]
	}
	name := "NidaOS " + host + " " + key[:6]
	m, _ := json.Marshal(github.Manifest(name, base, base+"/api/v1/github/callback"))
	action := s.GitHub.Web + "/settings/apps/new?state=" + url.QueryEscape(key)
	if org != "" {
		action = s.GitHub.Web + "/organizations/" + url.PathEscape(org) + "/settings/apps/new?state=" + url.QueryEscape(key)
	}
	return setupPage.Execute(w, map[string]any{"Action": template.URL(action), "Manifest": string(m)})
}

// Callback ends the manifest flow. It gives the page where the owner
// installs the app.
func (s *Service) Callback(ctx context.Context, code, key string) (string, error) {
	if err := s.Store.CheckSetup(key); err != nil {
		return "", err
	}
	a, err := s.GitHub.Convert(ctx, code)
	if err != nil {
		return "", err
	}
	if err := s.Store.SetApp(a); err != nil {
		return "", err
	}
	s.GitHub.Forget()
	return s.GitHub.InstallURL(a), nil
}

// Import keeps an app that exists already, for example the app of the
// tests. GitHub must accept its key.
func (s *Service) Import(ctx context.Context, id int64, pemKey string) (AppView, error) {
	a := github.App{ID: id, PEM: pemKey}
	if _, err := a.Key(); err != nil {
		return AppView{}, &FieldError{Field: "private_key", Message: err.Error()}
	}
	a, err := s.GitHub.AppInfo(ctx, a)
	if err != nil {
		return AppView{}, err
	}
	if err := s.Store.SetApp(a); err != nil {
		return AppView{}, err
	}
	s.GitHub.Forget()
	return s.AppView(), nil
}

// RemoveApp removes the GitHub App from NidaOS. The owner removes it on
// GitHub.
func (s *Service) RemoveApp() error {
	if err := s.Store.RemoveApp(); err != nil {
		return err
	}
	s.GitHub.Forget()
	return nil
}

// ConnectRequest connects an app to a repository.
type ConnectRequest struct {
	App        string `json:"-"`
	Repository string `json:"repository"`
	Branch     string `json:"branch"`
	// NoPullRequest: the owner adds the workflow without NidaOS.
	NoPullRequest bool `json:"no_pull_request"`
}

// Connect examines the repository and keeps the connection. Then it opens
// the pull request with the workflow (T-05).
func (s *Service) Connect(ctx context.Context, r ConnectRequest) (Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.AppExists(r.App) {
		return Connection{}, ErrNoAppHere
	}
	owner, name, err := ParseRepo(r.Repository)
	if err != nil {
		return Connection{}, err
	}
	if _, ok := s.Store.Connection(r.App); ok {
		return Connection{}, ErrConnected
	}
	repo := s.GitHub.Repo(owner, name)
	info, err := repo.Info(ctx)
	if err != nil {
		return Connection{}, err
	}
	if !info.Private {
		return Connection{}, ErrNotPrivate
	}
	if r.Branch == "" {
		r.Branch = info.DefaultBranch
	}
	if err := ValidateBranch(r.Branch); err != nil {
		return Connection{}, err
	}
	// GitHub gives the names with the correct case.
	if o, n, ok := strings.Cut(info.FullName, "/"); ok {
		owner, name = o, n
	}
	c := Connection{App: r.App, Owner: owner, Repo: name, Branch: r.Branch}
	if err := s.Store.Connect(c); err != nil {
		return Connection{}, err
	}
	if !r.NoPullRequest {
		if _, err := s.openPR(ctx, c); err != nil {
			return s.connection(r.App), fmt.Errorf("the repository is connected, but the pull request failed: %w", err)
		}
	}
	if ctl := s.controller(); ctl != nil {
		ctl.Kick()
	}
	return s.connection(r.App), nil
}

func (s *Service) controller() *Controller {
	if s.Controller == nil {
		return nil
	}
	return s.Controller()
}

func (s *Service) connection(app string) Connection {
	c, _ := s.Store.Connection(app)
	return c
}

// WorkflowPath is the file of the deploy workflow of an app in the
// repository. Each app has its own file, thus two NidaOS computers can
// use the same repository.
func WorkflowPath(app string) string { return ".github/workflows/nidaos-deploy-" + app + ".yml" }

// WorkflowBranch is the branch of the pull request with the workflow.
func WorkflowBranch(app string) string { return "nidaos/deploy-" + app }

// Workflow gives the deploy workflow of an app. A push to the branch
// builds the image on NidaOS and deploys it.
func Workflow(app, branch string) []byte {
	return []byte(`# Made by NidaOS. A push to ` + branch + ` builds the app on the NidaOS
# computer and deploys it. The job runs only on the runner of NidaOS.
name: NidaOS deploy ` + app + `

on:
  push:
    branches: [` + quoteYAML(branch) + `]
  workflow_dispatch:

concurrency:
  group: nidaos-deploy-` + app + `
  cancel-in-progress: false

permissions:
  contents: read

jobs:
  deploy:
    runs-on: [self-hosted, nidaos, ` + AppLabel(app) + `]
    timeout-minutes: 60
    steps:
      - uses: actions/checkout@v5
      - name: Build and deploy on NidaOS
        run: nida app deploy ` + app + ` --build . --wait
`)
}

func quoteYAML(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// OpenPullRequest opens (or updates) the pull request with the workflow.
func (s *Service) OpenPullRequest(ctx context.Context, app string) (Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.Store.Connection(app)
	if !ok {
		return Connection{}, ErrNotConnected
	}
	if _, err := s.openPR(ctx, c); err != nil {
		return c, err
	}
	return s.connection(app), nil
}

func (s *Service) openPR(ctx context.Context, c Connection) (github.PullRequest, error) {
	body := "NidaOS adds the workflow that builds and deploys the app `" + c.App + "` on the NidaOS computer.\n\n" +
		"- A push to `" + c.Branch + "` starts the job.\n" +
		"- The job runs only on a runner of NidaOS, with the labels `self-hosted`, `nidaos` and `" + AppLabel(c.App) + "`.\n" +
		"- The build uses the `Containerfile` or the `Dockerfile` at the root of the repository.\n"
	title := "Add the NidaOS deploy workflow of " + c.App
	pr, err := s.GitHub.Repo(c.Owner, c.Repo).OpenPullRequest(ctx, c.Branch, WorkflowBranch(c.App),
		title, body, title, github.FileChange{Path: WorkflowPath(c.App), Content: Workflow(c.App, c.Branch)})
	if err != nil {
		return pr, err
	}
	err = s.Store.UpdateConnection(c.App, func(x *Connection) { x.PullRequest = pr.HTMLURL })
	return pr, err
}

// Disconnect stops the pods of the app and removes its connection. The
// workflow stays in the repository; its jobs wait with no runner.
func (s *Service) Disconnect(ctx context.Context, app string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Store.Connection(app); !ok {
		return ErrNotConnected
	}
	if ctl := s.controller(); ctl != nil {
		ctl.RemoveApp(ctx, app)
	}
	return s.Store.Disconnect(app)
}
