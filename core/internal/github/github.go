// Package github is the small GitHub client of NidaOS. It uses the GitHub
// App of NidaOS (decision D25): a JWT signed with the private key of the
// app gives installation tokens, and each installation token is valid for
// one hour and only for one repository.
package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Default addresses of GitHub.
const (
	DefaultAPI = "https://api.github.com"
	DefaultWeb = "https://github.com"
)

// Errors of the client.
var (
	// ErrNoApp: NidaOS has no GitHub App yet.
	ErrNoApp = errors.New("NidaOS has no GitHub App: run nida github setup")
	// ErrNotInstalled: the app is not installed on the repository.
	ErrNotInstalled = errors.New("the GitHub App of NidaOS is not installed on the repository")
	// ErrNotFound: GitHub does not have the object.
	ErrNotFound = errors.New("GitHub does not have the object")
)

// APIError is an answer of GitHub with an error status.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("GitHub answered %d: %s", e.Status, e.Message)
}

// App is the GitHub App of NidaOS.
type App struct {
	ID       int64  `json:"id"`
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	ClientID string `json:"client_id,omitempty"`
	Owner    string `json:"owner,omitempty"`
	HTMLURL  string `json:"html_url,omitempty"`
	// PEM is the private key of the app. Only root can read the file that
	// keeps it, and the API never gives it.
	PEM string `json:"pem"`
}

// Key reads the private key of the app.
func (a App) Key() (*rsa.PrivateKey, error) {
	b, _ := pem.Decode([]byte(a.PEM))
	if b == nil {
		return nil, errors.New("the private key of the app is not PEM")
	}
	if k, err := x509.ParsePKCS1PrivateKey(b.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(b.Bytes)
	if err != nil {
		return nil, fmt.Errorf("the private key of the app: %w", err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("the private key of the app is not RSA")
	}
	return rk, nil
}

// JWT makes the token of the app. GitHub accepts a maximum of 10 minutes;
// the token starts 60 seconds in the past for a clock that is not exact.
func JWT(a App, now time.Time) (string, error) {
	key, err := a.Key()
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	head := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": strconv.FormatInt(a.ID, 10),
	})
	signed := head + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signed + "." + enc.EncodeToString(sig), nil
}

// Client calls the API of GitHub.
type Client struct {
	API  string
	Web  string
	HTTP *http.Client
	Now  func() time.Time
	// App gives the app of NidaOS, or false when there is no app.
	App func() (App, bool)

	mu     sync.Mutex
	tokens map[string]cachedToken
}

type cachedToken struct {
	token   string
	expires time.Time
}

// New makes a client. api and web can be empty for github.com.
func New(api, web string, app func() (App, bool)) *Client {
	if api == "" {
		api = DefaultAPI
	}
	if web == "" {
		web = DefaultWeb
	}
	return &Client{
		API: strings.TrimRight(api, "/"), Web: strings.TrimRight(web, "/"),
		HTTP: &http.Client{Timeout: 30 * time.Second}, Now: time.Now, App: app,
		tokens: map[string]cachedToken{},
	}
}

// request is one call. auth is the value of the Authorization header.
func (c *Client) request(ctx context.Context, method, path, auth string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	u := path
	if !strings.HasPrefix(path, "http") {
		u = c.API + path
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "NidaOS")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("GitHub is not available: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Message == "" {
			e.Message = http.StatusText(resp.StatusCode)
		}
		return &APIError{Status: resp.StatusCode, Message: e.Message}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *Client) app() (App, error) {
	if c.App == nil {
		return App{}, ErrNoApp
	}
	a, ok := c.App()
	if !ok {
		return App{}, ErrNoApp
	}
	return a, nil
}

// Manifest gives the manifest of the GitHub App of NidaOS. GitHub sends
// the browser back to redirect with a code. The app has no webhook: NidaOS
// opens no port to the internet.
func Manifest(name, homepage, redirect string) map[string]any {
	return map[string]any{
		"name":         name,
		"url":          homepage,
		"redirect_url": redirect,
		"public":       false,
		"hook_attributes": map[string]any{
			"url":    homepage,
			"active": false,
		},
		"default_permissions": map[string]string{
			"administration": "write", // the registration of the runners
			"actions":        "read",  // the queued jobs and their logs
			"contents":       "write", // the branch of the workflow
			"workflows":      "write", // the file of the workflow
			"pull_requests":  "write", // the pull request of the workflow
			"metadata":       "read",
		},
	}
}

// Convert changes the code of the manifest flow into the app.
func (c *Client) Convert(ctx context.Context, code string) (App, error) {
	var out struct {
		ID       int64  `json:"id"`
		Slug     string `json:"slug"`
		Name     string `json:"name"`
		ClientID string `json:"client_id"`
		HTMLURL  string `json:"html_url"`
		PEM      string `json:"pem"`
		Owner    struct {
			Login string `json:"login"`
		} `json:"owner"`
	}
	if err := c.request(ctx, http.MethodPost, "/app-manifests/"+url.PathEscape(code)+"/conversions", "", nil, &out); err != nil {
		return App{}, err
	}
	return App{ID: out.ID, Slug: out.Slug, Name: out.Name, ClientID: out.ClientID, HTMLURL: out.HTMLURL,
		PEM: out.PEM, Owner: out.Owner.Login}, nil
}

// AppInfo examines the credentials of an app with GitHub and gives the data
// of the app.
func (c *Client) AppInfo(ctx context.Context, a App) (App, error) {
	jwt, err := JWT(a, c.Now())
	if err != nil {
		return App{}, err
	}
	var out struct {
		Slug     string `json:"slug"`
		Name     string `json:"name"`
		ClientID string `json:"client_id"`
		HTMLURL  string `json:"html_url"`
		Owner    struct {
			Login string `json:"login"`
		} `json:"owner"`
	}
	if err := c.request(ctx, http.MethodGet, "/app", "Bearer "+jwt, nil, &out); err != nil {
		return App{}, err
	}
	a.Slug, a.Name, a.ClientID, a.HTMLURL, a.Owner = out.Slug, out.Name, out.ClientID, out.HTMLURL, out.Owner.Login
	return a, nil
}

// InstallURL is the page where the owner installs the app on repositories.
func (c *Client) InstallURL(a App) string {
	return c.Web + "/apps/" + url.PathEscape(a.Slug) + "/installations/new"
}

// Token gives an installation token for one repository. The client keeps
// the token until 5 minutes before its end.
func (c *Client) Token(ctx context.Context, owner, repo string) (string, error) {
	a, err := c.app()
	if err != nil {
		return "", err
	}
	key := strconv.FormatInt(a.ID, 10) + "/" + strings.ToLower(owner+"/"+repo)
	now := c.Now()
	c.mu.Lock()
	t, ok := c.tokens[key]
	c.mu.Unlock()
	if ok && now.Before(t.expires.Add(-5*time.Minute)) {
		return t.token, nil
	}
	jwt, err := JWT(a, now)
	if err != nil {
		return "", err
	}
	var inst struct {
		ID int64 `json:"id"`
	}
	err = c.request(ctx, http.MethodGet, "/repos/"+path(owner, repo)+"/installation", "Bearer "+jwt, nil, &inst)
	if errors.Is(err, ErrNotFound) {
		return "", ErrNotInstalled
	}
	if err != nil {
		return "", err
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	body := map[string]any{"repositories": []string{repo}}
	if err := c.request(ctx, http.MethodPost, "/app/installations/"+strconv.FormatInt(inst.ID, 10)+"/access_tokens",
		"Bearer "+jwt, body, &out); err != nil {
		return "", err
	}
	c.mu.Lock()
	c.tokens[key] = cachedToken{token: out.Token, expires: out.ExpiresAt}
	c.mu.Unlock()
	return out.Token, nil
}

// Forget removes the kept tokens, for example after a change of the app.
func (c *Client) Forget() {
	c.mu.Lock()
	c.tokens = map[string]cachedToken{}
	c.mu.Unlock()
}

func path(owner, repo string) string { return url.PathEscape(owner) + "/" + url.PathEscape(repo) }

// Repo calls the API for one repository with an installation token.
type Repo struct {
	c     *Client
	Owner string
	Name  string
}

// Repo gives the API of one repository.
func (c *Client) Repo(owner, name string) *Repo { return &Repo{c: c, Owner: owner, Name: name} }

func (r *Repo) do(ctx context.Context, method, sub string, body, out any) error {
	tok, err := r.c.Token(ctx, r.Owner, r.Name)
	if err != nil {
		return err
	}
	return r.c.request(ctx, method, "/repos/"+path(r.Owner, r.Name)+sub, "Bearer "+tok, body, out)
}

// RepoInfo is the data of a repository.
type RepoInfo struct {
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
	HTMLURL       string `json:"html_url"`
}

// Info gives the data of the repository.
func (r *Repo) Info(ctx context.Context) (RepoInfo, error) {
	var out RepoInfo
	err := r.do(ctx, http.MethodGet, "", nil, &out)
	return out, err
}

// Run is a workflow run.
type Run struct {
	ID      int64  `json:"id"`
	Status  string `json:"status"`
	HeadSHA string `json:"head_sha"`
	HTMLURL string `json:"html_url"`
}

// QueuedRuns gives the runs that wait for a runner.
func (r *Repo) QueuedRuns(ctx context.Context) ([]Run, error) {
	var out struct {
		Runs []Run `json:"workflow_runs"`
	}
	err := r.do(ctx, http.MethodGet, "/actions/runs?per_page=30&status=queued", nil, &out)
	return out.Runs, err
}

// Job is a job of a workflow run.
type Job struct {
	ID          int64      `json:"id"`
	RunID       int64      `json:"run_id"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	Conclusion  string     `json:"conclusion"`
	HeadSHA     string     `json:"head_sha"`
	HTMLURL     string     `json:"html_url"`
	Labels      []string   `json:"labels"`
	RunnerName  string     `json:"runner_name"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
}

// Jobs gives the jobs of a run.
func (r *Repo) Jobs(ctx context.Context, runID int64) ([]Job, error) {
	var out struct {
		Jobs []Job `json:"jobs"`
	}
	err := r.do(ctx, http.MethodGet, "/actions/runs/"+strconv.FormatInt(runID, 10)+"/jobs?filter=latest&per_page=50", nil, &out)
	return out.Jobs, err
}

// Job gives one job.
func (r *Repo) Job(ctx context.Context, id int64) (Job, error) {
	var out Job
	err := r.do(ctx, http.MethodGet, "/actions/jobs/"+strconv.FormatInt(id, 10), nil, &out)
	return out, err
}

// JobLog gives the log of a finished job, with a maximum of max bytes.
func (r *Repo) JobLog(ctx context.Context, id int64, max int64) ([]byte, error) {
	tok, err := r.c.Token(ctx, r.Owner, r.Name)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		r.c.API+"/repos/"+path(r.Owner, r.Name)+"/actions/jobs/"+strconv.FormatInt(id, 10)+"/logs", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "NidaOS")
	// GitHub answers with a redirect to a signed address. The client of Go
	// does not send the Authorization header to the other host.
	resp, err := r.c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode >= 300 {
		return nil, &APIError{Status: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
	}
	return io.ReadAll(io.LimitReader(resp.Body, max))
}

// JITRunner is a runner for one job.
type JITRunner struct {
	ID     int64
	Config string
}

// JITConfig registers a runner for one job, with the labels. GitHub removes
// the runner after its job.
func (r *Repo) JITConfig(ctx context.Context, name string, labels []string) (JITRunner, error) {
	var out struct {
		Runner struct {
			ID int64 `json:"id"`
		} `json:"runner"`
		Config string `json:"encoded_jit_config"`
	}
	body := map[string]any{"name": name, "runner_group_id": 1, "labels": labels, "work_folder": "_work"}
	if err := r.do(ctx, http.MethodPost, "/actions/runners/generate-jitconfig", body, &out); err != nil {
		return JITRunner{}, err
	}
	return JITRunner{ID: out.Runner.ID, Config: out.Config}, nil
}

// DeleteRunner removes a runner. A runner that GitHub removed already is
// not an error.
func (r *Repo) DeleteRunner(ctx context.Context, id int64) error {
	err := r.do(ctx, http.MethodDelete, "/actions/runners/"+strconv.FormatInt(id, 10), nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// LatestRunnerVersion gives the version of the last release of the GitHub
// runner, for example "2.330.0". GitHub does not give jobs to a runner that
// is too old.
func (r *Repo) LatestRunnerVersion(ctx context.Context) (string, error) {
	tok, err := r.c.Token(ctx, r.Owner, r.Name)
	if err != nil {
		return "", err
	}
	var out struct {
		Tag string `json:"tag_name"`
	}
	if err := r.c.request(ctx, http.MethodGet, "/repos/actions/runner/releases/latest", "Bearer "+tok, nil, &out); err != nil {
		return "", err
	}
	return strings.TrimPrefix(out.Tag, "v"), nil
}
