package httpserver

import (
	"net/http"

	"github.com/fathorMB/NidaOS/core/internal/system"
)

// StateSource gives the state of the computer. *system.Collector is the
// real source. The tests use a different source.
type StateSource interface {
	State() (system.State, error)
	System() (system.System, error)
	Disks() ([]system.Disk, error)
	Tiers() ([]system.Tier, error)
}

// Each body has a list in an object, thus the API can add fields later.
type disksBody struct {
	Disks []system.Disk `json:"disks"`
}

type tiersBody struct {
	Tiers []system.Tier `json:"tiers"`
}

func (r *Router) addStateRoutes() {
	r.handle(http.MethodGet, "/api/v1/state", func(w http.ResponseWriter, req *http.Request) {
		respond(r, w, req, r.state.State, func(s system.State) any { return s })
	})
	r.handle(http.MethodGet, "/api/v1/system", func(w http.ResponseWriter, req *http.Request) {
		respond(r, w, req, r.state.System, func(s system.System) any { return s })
	})
	r.handle(http.MethodGet, "/api/v1/disks", func(w http.ResponseWriter, req *http.Request) {
		respond(r, w, req, r.state.Disks, func(d []system.Disk) any { return disksBody{Disks: d} })
	})
	r.handle(http.MethodGet, "/api/v1/tiers", func(w http.ResponseWriter, req *http.Request) {
		respond(r, w, req, r.state.Tiers, func(t []system.Tier) any { return tiersBody{Tiers: t} })
	})
}

// respond calls read and writes the result, or writes the error 503 with
// the code state_unavailable. The cause goes to the service log, not to
// the client.
func respond[T any](r *Router, w http.ResponseWriter, req *http.Request, read func() (T, error), body func(T) any) {
	v, err := read()
	if err != nil {
		r.logger.Error("the service cannot read the state", "path", req.URL.Path, "err", err)
		writeError(w, http.StatusServiceUnavailable, "state_unavailable", "The service cannot read the state of the computer.")
		return
	}
	writeJSON(w, http.StatusOK, body(v))
}
