// Package httpserver makes the HTTP router of the core service.
//
// The router has two groups of paths:
//   - /healthz and /readyz are the probes for systemd and the monitors. They
//     have no version.
//   - /api/v1/... is the API for the interfaces, the nida command and the
//     agents: the health, the state of the computer (state.go), and later
//     the metrics and the logs.
package httpserver

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/agents"
	"github.com/fathorMB/NidaOS/core/internal/auth"
	"github.com/fathorMB/NidaOS/core/internal/version"
)

// Health is the body of the health responses.
type Health struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Uptime  string `json:"uptime"`
}

// Router is the HTTP handler of the core service.
type Router struct {
	mux     *http.ServeMux
	logger  *slog.Logger
	state   StateSource
	metrics MetricsSource
	logs    LogSource
	// logStore searches the old lines.
	logStore LogSearcher
	// apps is the app hosting. Nil: no app paths.
	apps *AppHosting
	// dbs is the database hosting. Nil: no database paths.
	dbs *DatabaseHosting
	// runners is the GitHub runner part. Nil: no runner paths.
	runners *RunnerHosting
	// updates is the update part. Nil: no update paths.
	updates *UpdateHosting
	// console serves the web console. Nil: no console.
	console http.Handler
	// agents keeps the approval requests and the actions of the agent
	// tokens. Nil: an "ask" rule refuses the request.
	agents *agents.Store
	// settingsDir is the folder of the settings (/var/lib/nidaos). Empty:
	// no backup path.
	settingsDir string
	// volumesRoot is the root of the paths of the tiers ("/" on the
	// computer).
	volumesRoot string
	// authority gives the certificate of the NidaOS authority.
	authority CertSource
	// auth checks the sessions and the tokens. Nil: no access check.
	auth    Authenticator
	limiter *auth.Limiter
	// heartbeat is the time between two comments in a stream.
	heartbeat time.Duration
	started   time.Time
	now       func() time.Time
	// methods gives the accepted methods of each known path.
	methods map[string][]string
}

// Deps are the parts that the router uses. A nil source removes its paths.
type Deps struct {
	Logger  *slog.Logger
	State   StateSource
	Metrics MetricsSource
	Logs    LogSource
	// LogStore must be nil, not a nil pointer, when there is no store.
	LogStore LogSearcher
	// Auth checks the access to each path. Nil gives the access to all,
	// only for the tests. main always sets it.
	Auth Authenticator
	// Apps is the app hosting (milestone M-03). Nil: no app paths.
	Apps *AppHosting
	// Authority gives the certificate of the NidaOS authority. Nil: no
	// certificate paths.
	Authority CertSource
	// Databases is the database hosting (milestone M-04). Nil: no
	// database paths.
	Databases *DatabaseHosting
	// Runners is the GitHub runner part (milestone M-05). Nil: no runner
	// paths.
	Runners *RunnerHosting
	// Updates gives the versions of the system (milestone M-06). Nil: no
	// update paths.
	Updates *UpdateHosting
	// Console serves the files of the web console (decision D29) for each
	// GET out of /api/. Nil: no console.
	Console http.Handler
	// Agents keeps the approval requests and the record of the agents
	// (milestone M-09). Nil: no approval paths.
	Agents *agents.Store
	// SettingsDir is the folder of the settings for the backup (M-11).
	SettingsDir string
	// Root is the root of the paths of the computer, for the volumes of
	// the tiers. Empty: "/".
	Root string
}

// NewRouter makes the router with all the paths of the service.
func NewRouter(d Deps) *Router {
	r := &Router{
		mux:         http.NewServeMux(),
		logger:      d.Logger,
		state:       d.State,
		metrics:     d.Metrics,
		logs:        d.Logs,
		logStore:    d.LogStore,
		auth:        d.Auth,
		apps:        d.Apps,
		dbs:         d.Databases,
		runners:     d.Runners,
		updates:     d.Updates,
		console:     d.Console,
		agents:      d.Agents,
		settingsDir: d.SettingsDir,
		volumesRoot: d.Root,
		authority:   d.Authority,
		limiter:     auth.NewLimiter(),
		started:     time.Now(),
		now:         time.Now,
		heartbeat:   SSEHeartbeat,
	}
	if r.logger == nil {
		r.logger = slog.New(slog.DiscardHandler)
	}
	r.handle(http.MethodGet, "/healthz", r.health)
	r.handle(http.MethodGet, "/readyz", r.health)
	r.handle(http.MethodGet, "/api/v1/health", r.health)
	if r.state != nil {
		r.addStateRoutes()
	}
	r.addMetricsRoutes()
	r.addLogRoutes()
	r.addLogSearchRoutes()
	r.addAuthRoutes()
	r.addAppRoutes()
	r.addDatabaseRoutes()
	r.addRunnerRoutes()
	r.addUpdateRoutes()
	r.addAgentRoutes()
	r.addSettingsRoutes()
	r.addVolumeRoutes()
	if r.volumesRoot == "" {
		r.volumesRoot = "/"
	}
	if r.auth != nil {
		r.addMCPRoutes()
	}
	r.mux.HandleFunc("/", r.noRoute)
	return r
}

// handle adds a path for one method. It also keeps the method in r.methods,
// thus noRoute can give 405 for a known path with a wrong method.
func (r *Router) handle(method, path string, h http.HandlerFunc) {
	if r.methods == nil {
		r.methods = map[string][]string{}
	}
	r.methods[path] = append(r.methods[path], method)
	r.mux.HandleFunc(method+" "+path, h)
}

// ServeHTTP sends the request to the handler of its path and logs it.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	start := r.now()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	if req, ok := r.checkAccess(rec, req); ok {
		r.mux.ServeHTTP(rec, req)
		r.recordAgent(req, rec.status)
	}
	r.logger.Debug("request",
		"method", req.Method,
		"path", req.URL.Path,
		"status", rec.status,
		"duration", r.now().Sub(start).String(),
	)
}

func (r *Router) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, Health{
		Status:  "ok",
		Version: version.Version,
		Uptime:  r.now().Sub(r.started).Truncate(time.Second).String(),
	})
}

// noRoute answers each request that has no handler.
func (r *Router) noRoute(w http.ResponseWriter, req *http.Request) {
	if methods, ok := r.methods[req.URL.Path]; ok {
		w.Header().Set("Allow", strings.Join(methods, ", "))
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "The path does not accept this method.")
		return
	}
	if r.console != nil && consolePath(req) {
		r.console.ServeHTTP(w, req)
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "The path does not exist.")
}

// consolePath tells if the web console serves the request: a GET out of
// the API and the probes.
func consolePath(req *http.Request) bool {
	p := req.URL.Path
	return safeMethod(req.Method) && req.Method != http.MethodOptions &&
		p != "/api" && !strings.HasPrefix(p, "/api/") && p != "/healthz" && p != "/readyz"
}

// Error is the body of each error response.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, Error{Code: code, Message: message})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// statusRecorder keeps the status code for the request log.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush lets the live streams of the next tasks send data at once.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
