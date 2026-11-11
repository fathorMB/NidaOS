package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/fathorMB/NidaOS/core/internal/updates"
)

// UpdateHosting gives the system versions to the API (milestone M-06).
type UpdateHosting struct {
	Service *updates.Service
}

func (r *Router) addUpdateRoutes() {
	if r.updates == nil || r.updates.Service == nil {
		return
	}
	r.handle(http.MethodGet, "/api/v1/updates", r.updatesStatus)
	r.handle(http.MethodPost, "/api/v1/updates/check", r.updatesCheck)
	r.handle(http.MethodPost, "/api/v1/updates/download", r.updatesDownload)
	r.handle(http.MethodPost, "/api/v1/updates/restart", r.updatesRestart)
	r.handle(http.MethodPost, "/api/v1/updates/rollback", r.updatesRollback)
}

func (r *Router) updatesStatus(w http.ResponseWriter, req *http.Request) {
	st, err := r.updates.Service.Status(req.Context())
	if err != nil {
		writeUpdateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// updateAccepted answers an operation that started: 202 and the state.
func (r *Router) updateAccepted(w http.ResponseWriter, req *http.Request, err error) {
	if err != nil {
		writeUpdateError(w, err)
		return
	}
	st, err := r.updates.Service.Status(req.Context())
	if err != nil {
		writeUpdateError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, st)
}

func (r *Router) updatesCheck(w http.ResponseWriter, req *http.Request) {
	r.updateAccepted(w, req, r.updates.Service.Check())
}

func (r *Router) updatesDownload(w http.ResponseWriter, req *http.Request) {
	r.updateAccepted(w, req, r.updates.Service.Download())
}

func (r *Router) updatesRestart(w http.ResponseWriter, req *http.Request) {
	r.updateAccepted(w, req, r.updates.Service.Restart(req.Context()))
}

func (r *Router) updatesRollback(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Restart bool `json:"restart"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "bad_body", "The body must be a JSON object with the correct fields.")
		return
	}
	r.updateAccepted(w, req, r.updates.Service.Rollback(req.Context(), body.Restart))
}

func writeUpdateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, updates.ErrBusy):
		writeError(w, http.StatusConflict, "update_busy", "An update operation is in progress. Try again when it is complete.")
	case errors.Is(err, updates.ErrNothingToInstall):
		writeError(w, http.StatusConflict, "nothing_to_install", "No version waits for a restart. Download an update first.")
	case errors.Is(err, updates.ErrNoRollback):
		writeError(w, http.StatusConflict, "no_rollback", "There is no system version before the version in operation.")
	default:
		writeError(w, http.StatusServiceUnavailable, "updates_unavailable", "bootc does not answer: "+err.Error())
	}
}
