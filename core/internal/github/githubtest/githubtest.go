// Package githubtest is a fake GitHub API for the tests. It knows only the
// calls that NidaOS makes.
package githubtest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/github"
)

// Token is the installation token that the fake gives.
const Token = "ghs_fake_installation_token"

// RepoData is one repository of the fake.
type RepoData struct {
	Private       bool
	DefaultBranch string
	Installed     bool
	Refs          map[string]string // branch -> sha
	Files         map[string]string // branch + ":" + path -> content
	Pulls         []PullData
	Runs          []github.Run
	Jobs          map[int64][]github.Job // run ID -> jobs
	Logs          map[int64]string       // job ID -> log
}

// PullData is a pull request of the fake.
type PullData struct {
	Number      int
	Title, Head string
	Base, Body  string
	State       string
}

// Runner is a runner that the fake registered.
type Runner struct {
	ID     int64
	Repo   string
	Name   string
	Labels []string
}

// Server is the fake GitHub.
type Server struct {
	*httptest.Server
	App          github.App
	LatestRunner string

	mu      sync.Mutex
	repos   map[string]*RepoData
	codes   map[string]bool
	runners map[int64]Runner
	nextID  int64
	tokens  int
}

// New starts a fake with one app.
func New() *Server {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	p := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	s := &Server{
		App:          github.App{ID: 4242, Slug: "nidaos-test", Name: "NidaOS test", Owner: "owner", PEM: string(p)},
		LatestRunner: "2.330.0",
		repos:        map[string]*RepoData{},
		codes:        map[string]bool{},
		runners:      map[int64]Runner{},
		nextID:       1000,
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// Repo adds a repository and gives it.
func (s *Server) Repo(full string, private bool) *RepoData {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := &RepoData{Private: private, DefaultBranch: "main", Installed: true,
		Refs: map[string]string{"main": "aaaa000000000000000000000000000000000001"}, Files: map[string]string{},
		Jobs: map[int64][]github.Job{}, Logs: map[int64]string{}}
	s.repos[strings.ToLower(full)] = r
	return r
}

// Code adds a code of the manifest flow.
func (s *Server) Code(code string) {
	s.mu.Lock()
	s.codes[code] = true
	s.mu.Unlock()
}

// Lock gives the data of a repository under the lock, for a change of a
// test. Call the function that it gives to release the lock.
func (s *Server) Lock(full string) (*RepoData, func()) {
	s.mu.Lock()
	return s.repos[strings.ToLower(full)], s.mu.Unlock
}

// Runners gives the registered runners.
func (s *Server) Runners() []Runner {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Runner
	for _, r := range s.runners {
		out = append(out, r)
	}
	return out
}

// TokenCount gives the number of installation tokens that the fake made.
func (s *Server) TokenCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokens
}

func (s *Server) id() int64 {
	s.nextID++
	return s.nextID
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func fail(w http.ResponseWriter, status int, msg string) {
	reply(w, status, map[string]string{"message": msg})
}

// checkJWT examines the token of the app: the header, the issuer and the
// time. It does not examine the signature: the test of JWT does it.
func (s *Server) checkJWT(req *http.Request) bool {
	tok, ok := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
	parts := strings.Split(tok, ".")
	if !ok || len(parts) != 3 {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var c struct {
		Iss string `json:"iss"`
		Exp int64  `json:"exp"`
	}
	if json.Unmarshal(b, &c) != nil {
		return false
	}
	return c.Iss == strconv.FormatInt(s.App.ID, 10) && c.Exp > time.Now().Unix()
}

func (s *Server) serve(w http.ResponseWriter, req *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := req.URL.Path
	seg := strings.Split(strings.Trim(p, "/"), "/")

	switch {
	case req.Method == http.MethodPost && len(seg) == 3 && seg[0] == "app-manifests" && seg[2] == "conversions":
		if !s.codes[seg[1]] {
			fail(w, http.StatusNotFound, "Not Found")
			return
		}
		delete(s.codes, seg[1])
		reply(w, http.StatusCreated, map[string]any{"id": s.App.ID, "slug": s.App.Slug, "name": s.App.Name,
			"client_id": "Iv1.fake", "html_url": s.URL + "/apps/" + s.App.Slug, "pem": s.App.PEM,
			"owner": map[string]string{"login": s.App.Owner}})
		return
	case req.Method == http.MethodGet && p == "/app":
		if !s.checkJWT(req) {
			fail(w, http.StatusUnauthorized, "Bad credentials")
			return
		}
		reply(w, http.StatusOK, map[string]any{"slug": s.App.Slug, "name": s.App.Name, "client_id": "Iv1.fake",
			"html_url": s.URL + "/apps/" + s.App.Slug, "owner": map[string]string{"login": s.App.Owner}})
		return
	case req.Method == http.MethodPost && len(seg) == 4 && seg[0] == "app" && seg[1] == "installations" && seg[3] == "access_tokens":
		if !s.checkJWT(req) {
			fail(w, http.StatusUnauthorized, "Bad credentials")
			return
		}
		s.tokens++
		reply(w, http.StatusCreated, map[string]any{"token": Token, "expires_at": time.Now().Add(time.Hour).UTC()})
		return
	case p == "/repos/actions/runner/releases/latest":
		reply(w, http.StatusOK, map[string]string{"tag_name": "v" + s.LatestRunner})
		return
	}

	if len(seg) < 3 || seg[0] != "repos" {
		fail(w, http.StatusNotFound, "Not Found")
		return
	}
	full := strings.ToLower(seg[1] + "/" + seg[2])
	r := s.repos[full]
	rest := seg[3:]
	if r == nil {
		fail(w, http.StatusNotFound, "Not Found")
		return
	}
	if len(rest) == 1 && rest[0] == "installation" {
		if !s.checkJWT(req) {
			fail(w, http.StatusUnauthorized, "Bad credentials")
			return
		}
		if !r.Installed {
			fail(w, http.StatusNotFound, "Not Found")
			return
		}
		reply(w, http.StatusOK, map[string]int64{"id": 77})
		return
	}
	if req.Header.Get("Authorization") != "Bearer "+Token {
		fail(w, http.StatusUnauthorized, "Bad credentials")
		return
	}
	s.repo(w, req, seg[1], full, r, rest)
}

func (s *Server) repo(w http.ResponseWriter, req *http.Request, owner, full string, r *RepoData, rest []string) {
	m := req.Method
	route := strings.Join(rest, "/")
	switch {
	case m == http.MethodGet && route == "":
		reply(w, http.StatusOK, map[string]any{"full_name": full, "private": r.Private,
			"default_branch": r.DefaultBranch, "html_url": s.URL + "/" + full})
	case m == http.MethodGet && strings.HasPrefix(route, "git/ref/heads/"):
		sha, ok := r.Refs[strings.TrimPrefix(route, "git/ref/heads/")]
		if !ok {
			fail(w, http.StatusNotFound, "Not Found")
			return
		}
		reply(w, http.StatusOK, map[string]any{"object": map[string]string{"sha": sha}})
	case m == http.MethodPost && route == "git/refs":
		var in struct{ Ref, SHA string }
		_ = json.NewDecoder(req.Body).Decode(&in)
		r.Refs[strings.TrimPrefix(in.Ref, "refs/heads/")] = in.SHA
		reply(w, http.StatusCreated, nil)
	case strings.HasPrefix(route, "contents/"):
		file := strings.TrimPrefix(route, "contents/")
		if m == http.MethodGet {
			c, ok := r.Files[req.URL.Query().Get("ref")+":"+file]
			if !ok {
				fail(w, http.StatusNotFound, "Not Found")
				return
			}
			reply(w, http.StatusOK, map[string]string{"sha": fmt.Sprintf("sha-%d", len(c))})
			return
		}
		var in struct{ Message, Content, Branch, SHA string }
		_ = json.NewDecoder(req.Body).Decode(&in)
		key := in.Branch + ":" + file
		if _, ok := r.Files[key]; ok && in.SHA == "" {
			fail(w, http.StatusUnprocessableEntity, "sha wasn't supplied")
			return
		}
		b, _ := base64.StdEncoding.DecodeString(in.Content)
		r.Files[key] = string(b)
		reply(w, http.StatusCreated, nil)
	case m == http.MethodGet && route == "pulls":
		head := strings.TrimPrefix(req.URL.Query().Get("head"), owner+":")
		var out []map[string]any
		for _, pr := range r.Pulls {
			if pr.Head == head && pr.State == "open" {
				out = append(out, map[string]any{"number": pr.Number, "html_url": fmt.Sprintf("%s/%s/pull/%d", s.URL, full, pr.Number), "state": "open"})
			}
		}
		reply(w, http.StatusOK, out)
	case m == http.MethodPost && route == "pulls":
		var in struct{ Title, Head, Base, Body string }
		_ = json.NewDecoder(req.Body).Decode(&in)
		n := len(r.Pulls) + 1
		r.Pulls = append(r.Pulls, PullData{Number: n, Title: in.Title, Head: in.Head, Base: in.Base, Body: in.Body, State: "open"})
		reply(w, http.StatusCreated, map[string]any{"number": n, "html_url": fmt.Sprintf("%s/%s/pull/%d", s.URL, full, n), "state": "open"})
	case m == http.MethodGet && route == "actions/runs":
		st := req.URL.Query().Get("status")
		var out []github.Run
		for _, run := range r.Runs {
			if run.Status == st {
				out = append(out, run)
			}
		}
		reply(w, http.StatusOK, map[string]any{"workflow_runs": out})
	case m == http.MethodGet && len(rest) == 4 && rest[0] == "actions" && rest[1] == "runs" && rest[3] == "jobs":
		id, _ := strconv.ParseInt(rest[2], 10, 64)
		reply(w, http.StatusOK, map[string]any{"jobs": r.Jobs[id]})
	case m == http.MethodGet && len(rest) == 3 && rest[0] == "actions" && rest[1] == "jobs":
		id, _ := strconv.ParseInt(rest[2], 10, 64)
		for _, js := range r.Jobs {
			for _, j := range js {
				if j.ID == id {
					reply(w, http.StatusOK, j)
					return
				}
			}
		}
		fail(w, http.StatusNotFound, "Not Found")
	case m == http.MethodGet && len(rest) == 4 && rest[0] == "actions" && rest[1] == "jobs" && rest[3] == "logs":
		id, _ := strconv.ParseInt(rest[2], 10, 64)
		l, ok := r.Logs[id]
		if !ok {
			fail(w, http.StatusNotFound, "Not Found")
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, l)
	case m == http.MethodPost && route == "actions/runners/generate-jitconfig":
		var in struct {
			Name   string   `json:"name"`
			Labels []string `json:"labels"`
		}
		_ = json.NewDecoder(req.Body).Decode(&in)
		id := s.id()
		s.runners[id] = Runner{ID: id, Repo: full, Name: in.Name, Labels: in.Labels}
		reply(w, http.StatusCreated, map[string]any{"runner": map[string]int64{"id": id},
			"encoded_jit_config": base64.StdEncoding.EncodeToString([]byte("jit:" + in.Name))})
	case m == http.MethodDelete && len(rest) == 3 && rest[0] == "actions" && rest[1] == "runners":
		id, _ := strconv.ParseInt(rest[2], 10, 64)
		if _, ok := s.runners[id]; !ok {
			fail(w, http.StatusNotFound, "Not Found")
			return
		}
		delete(s.runners, id)
		w.WriteHeader(http.StatusNoContent)
	default:
		fail(w, http.StatusNotFound, "Not Found")
	}
}
