package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/apps"
	"github.com/fathorMB/NidaOS/core/internal/runners"
)

// AppHosting gives the app API its parts (milestone M-03).
type AppHosting struct {
	Store  *apps.Store
	Runner *apps.Runner
	// Registry is the local registry (decision D21). A deploy accepts only
	// an image "<Registry>/...".
	Registry string
}

// CertSource gives the certificate of the NidaOS authority (decision
// D20).
type CertSource interface {
	CertPEM() []byte
	Fingerprint() string
}

// maxAppBody is the maximum size of the body of an app request. A secret
// can have 64 KiB.
const maxAppBody = 1 << 20

// AppView is an app in the API.
type AppView struct {
	apps.App
	// State is a summary for the lists: new, deploying, moving, running,
	// starting, stopped or failed.
	State string `json:"state"`
	// Address is "https://<app>.<domain>", or empty without a port.
	Address string `json:"address,omitempty"`
	// Secrets are the names of the secrets. The API never gives a value.
	Secrets []string `json:"secrets"`
	// Copies is the observed state of the copies. It is not there before
	// the controller reads it.
	Status *apps.Status `json:"status,omitempty"`
}

func (r *Router) addAppRoutes() {
	if r.authority != nil {
		r.handle(http.MethodGet, "/api/v1/ca.crt", r.caCert)
		r.handle(http.MethodGet, "/api/v1/ca", r.caInfo)
	}
	if r.apps == nil {
		return
	}
	r.handle(http.MethodGet, "/api/v1/apps", r.appsList)
	r.handle(http.MethodPost, "/api/v1/apps", r.appsCreate)
	r.handle(http.MethodGet, "/api/v1/apps/{name}", r.appsGet)
	r.handle(http.MethodPatch, "/api/v1/apps/{name}", r.appsUpdate)
	r.handle(http.MethodDelete, "/api/v1/apps/{name}", r.appsRemove)
	r.handle(http.MethodPost, "/api/v1/apps/{name}/deploys", r.appsDeploy)
	r.handle(http.MethodPost, "/api/v1/apps/{name}/rollback", r.appsRollback)
	r.handle(http.MethodPost, "/api/v1/apps/{name}/restart", r.appsRestart)
	r.handle(http.MethodPut, "/api/v1/apps/{name}/secrets/{key}", r.appsSetSecret)
	r.handle(http.MethodDelete, "/api/v1/apps/{name}/secrets/{key}", r.appsDeleteSecret)
	r.handle(http.MethodPost, "/api/v1/apps/{name}/folders/{folder}/move", r.appsMove)
}

// caCert gives the certificate of the authority, to install on a device.
// It needs no sign-in: the certificate is public data.
func (r *Router) caCert(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="nidaos-ca.crt"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(r.authority.CertPEM())
}

func (r *Router) caInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"fingerprint_sha256": r.authority.Fingerprint(),
		"download":           "/api/v1/ca.crt",
	})
}

func (r *Router) view(a apps.App) AppView {
	v := AppView{App: a, Secrets: r.apps.Store.SecretNames(a.Name)}
	if a.Config.Port != nil {
		if h := r.apps.Runner.Host(a.Name); h != "" {
			v.Address = "https://" + h
		}
	}
	if c := r.apps.Runner.Controller(); c != nil {
		if st, ok := c.StatusOf(a.Name); ok {
			v.Status = &st
		}
	}
	v.State = appState(a, v.Status)
	return v
}

func appState(a apps.App, st *apps.Status) string {
	if _, ok := a.Deploying(); ok {
		return "deploying"
	}
	if _, ok := a.Moving(); ok {
		return "moving"
	}
	if a.Active == 0 {
		if len(a.Versions) > 0 && a.Versions[len(a.Versions)-1].State == apps.StateFailed {
			return "failed"
		}
		return "new"
	}
	if a.Copies == 0 {
		return "stopped"
	}
	if st != nil && st.Ready >= a.Copies {
		return "running"
	}
	return "starting"
}

// writeAppError writes the error of an app operation.
func (r *Router) writeAppError(w http.ResponseWriter, err error) {
	var fe *apps.FieldError
	switch {
	case errors.As(err, &fe):
		writeError(w, http.StatusBadRequest, "bad_field", fe.Error()+".")
	case errors.Is(err, apps.ErrNotFound):
		writeError(w, http.StatusNotFound, "app_not_found", "The app does not exist.")
	case errors.Is(err, apps.ErrExists):
		writeError(w, http.StatusConflict, "app_exists", "An app with this name exists.")
	case errors.Is(err, apps.ErrBusy):
		writeError(w, http.StatusConflict, "app_busy", "A deploy of the app is in progress. Wait for its end.")
	case errors.Is(err, apps.ErrMoving):
		writeError(w, http.StatusConflict, "app_moving", "A move of a folder of the app is in progress. Wait for its end.")
	case errors.Is(err, apps.ErrNoRollback):
		writeError(w, http.StatusConflict, "no_rollback", capital(err.Error())+".")
	case errors.Is(err, apps.ErrVersionNotFound):
		writeError(w, http.StatusNotFound, "version_not_found", capital(err.Error())+".")
	case errors.Is(err, apps.ErrFolderNotFound):
		writeError(w, http.StatusNotFound, "folder_not_found", "The folder does not exist.")
	case errors.Is(err, apps.ErrSecretNotFound):
		writeError(w, http.StatusNotFound, "secret_not_found", "The secret does not exist.")
	case errors.Is(err, apps.ErrNoVersion):
		writeError(w, http.StatusConflict, "no_version", "The app has no version in operation.")
	case errors.Is(err, apps.ErrNotReady):
		writeError(w, http.StatusServiceUnavailable, "apps_not_ready", "k3s is not ready. Try again in some seconds.")
	default:
		r.logger.Error("an app operation failed", "err", err)
		writeError(w, http.StatusInternalServerError, "app_failed", "The operation failed. The cause is in the log of the service.")
	}
}

func capital(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// readAppJSON reads a JSON body of an app request.
func readAppJSON(w http.ResponseWriter, req *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxAppBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad_body", "The body must be a JSON object with the correct fields: "+err.Error()+".")
		return false
	}
	return true
}

func (r *Router) appsList(w http.ResponseWriter, _ *http.Request) {
	list := r.apps.Store.Apps()
	out := make([]AppView, 0, len(list))
	for _, a := range list {
		out = append(out, r.view(a))
	}
	writeJSON(w, http.StatusOK, map[string][]AppView{"apps": out})
}

// createBody is the body of POST /api/v1/apps.
type createBody struct {
	Name       string        `json:"name"`
	Repository string        `json:"repository"`
	Branch     string        `json:"branch"`
	Config     apps.Config   `json:"config"`
	Copies     *int          `json:"copies"`
	Folders    []apps.Folder `json:"folders"`
}

func (r *Router) appsCreate(w http.ResponseWriter, req *http.Request) {
	var b createBody
	if !readAppJSON(w, req, &b) {
		return
	}
	copies := 1
	if b.Copies != nil {
		copies = *b.Copies
	}
	for i := range b.Folders {
		b.Folders[i].MovingTo, b.Folders[i].MoveError = "", ""
	}
	a, err := r.apps.Store.Create(apps.App{Name: b.Name, Repository: b.Repository, Branch: b.Branch,
		Config: b.Config, Copies: copies, Folders: b.Folders})
	if err != nil {
		r.writeAppError(w, err)
		return
	}
	r.logger.Info("an app was added", "app", a.Name)
	writeJSON(w, http.StatusCreated, r.view(a))
}

func (r *Router) appsGet(w http.ResponseWriter, req *http.Request) {
	a, ok := r.apps.Store.Get(req.PathValue("name"))
	if !ok {
		r.writeAppError(w, apps.ErrNotFound)
		return
	}
	writeJSON(w, http.StatusOK, r.view(a))
}

// updateBody is the body of PATCH /api/v1/apps/{name}. A missing field
// does not change. "config" replaces the full config.
type updateBody struct {
	Repository *string       `json:"repository"`
	Branch     *string       `json:"branch"`
	Config     *apps.Config  `json:"config"`
	Copies     *int          `json:"copies"`
	AddFolders []apps.Folder `json:"add_folders"`
}

func (r *Router) appsUpdate(w http.ResponseWriter, req *http.Request) {
	var b updateBody
	if !readAppJSON(w, req, &b) {
		return
	}
	for i := range b.AddFolders {
		b.AddFolders[i].MovingTo, b.AddFolders[i].MoveError = "", ""
	}
	a, started, err := r.apps.Store.Update(req.PathValue("name"), apps.Change{
		Repository: b.Repository, Branch: b.Branch, Config: b.Config, Copies: b.Copies, AddFolders: b.AddFolders})
	if err != nil {
		r.writeAppError(w, err)
		return
	}
	r.apps.Runner.Kick()
	body := struct {
		AppView
		// Deploy is the number of the version that applies the change, or 0.
		Deploy int `json:"deploy,omitempty"`
	}{r.view(a), started}
	writeJSON(w, http.StatusOK, body)
}

// busy tells if the controller works on the app now.
func (r *Router) busy(a apps.App) error {
	if _, ok := a.Deploying(); ok {
		return apps.ErrBusy
	}
	if _, ok := a.Moving(); ok {
		return apps.ErrMoving
	}
	if c := r.apps.Runner.Controller(); c != nil && c.Busy(a.Name) {
		return apps.ErrBusy
	}
	return nil
}

func (r *Router) appsRemove(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	a, ok := r.apps.Store.Get(name)
	if !ok {
		r.writeAppError(w, apps.ErrNotFound)
		return
	}
	if err := r.busy(a); err != nil {
		r.writeAppError(w, err)
		return
	}
	c := r.apps.Runner.Controller()
	if c == nil {
		r.writeAppError(w, apps.ErrNotReady)
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
	defer cancel()
	if err := c.Remove(ctx, name); err != nil {
		r.writeAppError(w, err)
		return
	}
	if r.runners != nil {
		if err := r.runners.Service.Disconnect(ctx, name); err != nil && !errors.Is(err, runners.ErrNotConnected) {
			r.logger.Warn("the disconnect of the repository failed", "app", name, "err", err)
		}
	}
	if err := r.apps.Store.Remove(name); err != nil {
		r.writeAppError(w, err)
		return
	}
	r.apps.Runner.Kick()
	r.logger.Info("an app was removed", "app", name)
	w.WriteHeader(http.StatusNoContent)
}

func (r *Router) appsDeploy(w http.ResponseWriter, req *http.Request) {
	var b struct {
		Image   string `json:"image"`
		Commit  string `json:"commit"`
		Summary string `json:"summary"`
	}
	if !readAppJSON(w, req, &b) {
		return
	}
	if r.apps.Registry != "" && !strings.HasPrefix(b.Image, r.apps.Registry+"/") {
		writeError(w, http.StatusBadRequest, "image_not_local",
			"The image must come from the local registry: "+r.apps.Registry+"/<name>:<tag> (decision D21).")
		return
	}
	v, err := r.apps.Store.Deploy(req.PathValue("name"), b.Image, b.Commit, b.Summary)
	if err != nil {
		r.writeAppError(w, err)
		return
	}
	r.apps.Runner.Kick()
	r.logger.Info("a deploy was asked", "app", req.PathValue("name"), "version", v.Number, "image", v.Image)
	writeJSON(w, http.StatusAccepted, v)
}

func (r *Router) appsRollback(w http.ResponseWriter, req *http.Request) {
	var b struct {
		Version int `json:"version"`
	}
	if !readAppJSON(w, req, &b) {
		return
	}
	v, err := r.apps.Store.Rollback(req.PathValue("name"), b.Version)
	if err != nil {
		r.writeAppError(w, err)
		return
	}
	r.apps.Runner.Kick()
	writeJSON(w, http.StatusAccepted, v)
}

func (r *Router) appsRestart(w http.ResponseWriter, req *http.Request) {
	a, ok := r.apps.Store.Get(req.PathValue("name"))
	if !ok {
		r.writeAppError(w, apps.ErrNotFound)
		return
	}
	if a.Active == 0 {
		r.writeAppError(w, apps.ErrNoVersion)
		return
	}
	if err := r.busy(a); err != nil {
		r.writeAppError(w, err)
		return
	}
	c := r.apps.Runner.Controller()
	if c == nil {
		r.writeAppError(w, apps.ErrNotReady)
		return
	}
	if err := c.Restart(req.Context(), a.Name); err != nil {
		r.writeAppError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// appsSetSecret adds or replaces a secret. The value goes to the copies at
// the next deploy.
func (r *Router) appsSetSecret(w http.ResponseWriter, req *http.Request) {
	var b struct {
		Value string `json:"value"`
	}
	if !readAppJSON(w, req, &b) {
		return
	}
	if err := r.apps.Store.SetSecret(req.PathValue("name"), req.PathValue("key"), b.Value); err != nil {
		r.writeAppError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r *Router) appsDeleteSecret(w http.ResponseWriter, req *http.Request) {
	if err := r.apps.Store.DeleteSecret(req.PathValue("name"), req.PathValue("key")); err != nil {
		r.writeAppError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r *Router) appsMove(w http.ResponseWriter, req *http.Request) {
	var b struct {
		Tier string `json:"tier"`
	}
	if !readAppJSON(w, req, &b) {
		return
	}
	a, err := r.apps.Store.RequestMove(req.PathValue("name"), req.PathValue("folder"), b.Tier)
	if err != nil {
		r.writeAppError(w, err)
		return
	}
	r.apps.Runner.Kick()
	writeJSON(w, http.StatusAccepted, r.view(a))
}
