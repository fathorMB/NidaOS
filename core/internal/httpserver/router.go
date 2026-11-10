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
}

// NewRouter makes the router with all the paths of the service.
func NewRouter(d Deps) *Router {
	r := &Router{
		mux:       http.NewServeMux(),
		logger:    d.Logger,
		state:     d.State,
		metrics:   d.Metrics,
		started:   time.Now(),
		now:       time.Now,
		heartbeat: SSEHeartbeat,
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
	r.mux.ServeHTTP(rec, req)
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
	writeError(w, http.StatusNotFound, "not_found", "The path does not exist.")
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
