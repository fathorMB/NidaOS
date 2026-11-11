package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/databases"
)

// DatabaseHosting gives the database API its parts (milestone M-04).
type DatabaseHosting struct {
	Runner *databases.Runner
	// Host is the name of the computer for the home network, for example
	// "nidaos.local". The certificate of the servers is for this name.
	Host string
}

// DatabaseView is a database in the API.
type DatabaseView struct {
	databases.Database
	// UpdateAvailable is the image of a newer minor version, or empty.
	UpdateAvailable string `json:"update_available,omitempty"`
	// Address is "<host>:<port>" while the switch for the home network is
	// on.
	Address string `json:"address,omitempty"`
	// Live is the last read of the statistics. It is not there before the
	// first read.
	Live *DatabaseLive `json:"live,omitempty"`
}

// DatabaseLive is the last read of the statistics of a database.
type DatabaseLive struct {
	SizeBytes             int64     `json:"size_bytes"`
	Connections           int       `json:"connections"`
	TransactionsPerSecond float64   `json:"transactions_per_second"`
	Observed              time.Time `json:"observed"`
}

func (r *Router) addDatabaseRoutes() {
	if r.dbs == nil {
		return
	}
	r.handle(http.MethodGet, "/api/v1/databases", r.dbList)
	r.handle(http.MethodPost, "/api/v1/databases", r.dbCreate)
	r.handle(http.MethodGet, "/api/v1/databases/{name}", r.dbGet)
	r.handle(http.MethodDelete, "/api/v1/databases/{name}", r.dbRemove)
	r.handle(http.MethodGet, "/api/v1/databases/{name}/connection", r.dbConnection)
	r.handle(http.MethodPost, "/api/v1/databases/{name}/links", r.dbLink)
	r.handle(http.MethodDelete, "/api/v1/databases/{name}/links/{app}", r.dbUnlink)
	r.handle(http.MethodPut, "/api/v1/databases/{name}/lan", r.dbLAN)
	r.handle(http.MethodGet, "/api/v1/databases/{name}/backups", r.dbBackups)
	r.handle(http.MethodPost, "/api/v1/databases/{name}/backups", r.dbBackup)
	r.handle(http.MethodDelete, "/api/v1/databases/{name}/backups/{backup}", r.dbDeleteBackup)
	r.handle(http.MethodPost, "/api/v1/databases/{name}/update", r.dbUpdate)
}

func (r *Router) dbController(w http.ResponseWriter) (*databases.Controller, bool) {
	c := r.dbs.Runner.Controller()
	if c == nil {
		writeError(w, http.StatusServiceUnavailable, "databases_not_ready", "k3s is not ready. Try again in some seconds.")
		return nil, false
	}
	return c, true
}

func (r *Router) dbView(d databases.Database) DatabaseView {
	v := DatabaseView{Database: d}
	if d.LAN && r.dbs.Host != "" {
		v.Address = r.dbs.Host + ":" + strconv.Itoa(d.LANPort)
	}
	if c := r.dbs.Runner.Controller(); c != nil {
		v.UpdateAvailable = c.UpdateAvailable(d)
		for _, l := range c.Live() {
			if l.Name == d.Name && !l.Observed.IsZero() {
				v.Live = &DatabaseLive{SizeBytes: l.Stats.SizeBytes, Connections: l.Stats.Connections,
					TransactionsPerSecond: l.TransactionsPerSecond, Observed: l.Observed}
			}
		}
	}
	return v
}

func (r *Router) writeDBError(w http.ResponseWriter, err error) {
	var fe *databases.FieldError
	switch {
	case errors.As(err, &fe):
		writeError(w, http.StatusBadRequest, "bad_field", fe.Error()+".")
	case errors.Is(err, databases.ErrNotFound):
		writeError(w, http.StatusNotFound, "database_not_found", "The database does not exist.")
	case errors.Is(err, databases.ErrExists):
		writeError(w, http.StatusConflict, "database_exists", "A database with this name exists.")
	case errors.Is(err, databases.ErrBusy), errors.Is(err, databases.ErrNotReady):
		writeError(w, http.StatusConflict, "database_busy", "The database is not ready, or an operation on it is in progress. Wait for its end.")
	case errors.Is(err, databases.ErrAppNotFound):
		writeError(w, http.StatusNotFound, "app_not_found", "The app does not exist.")
	case errors.Is(err, databases.ErrLinked):
		writeError(w, http.StatusConflict, "app_linked", "The app is connected to this database already.")
	case errors.Is(err, databases.ErrNotLinked):
		writeError(w, http.StatusNotFound, "link_not_found", "The app is not connected to this database.")
	case errors.Is(err, databases.ErrNameInUse):
		writeError(w, http.StatusConflict, "name_in_use", capital(err.Error())+". Remove the variable or the secret, or disconnect the other database.")
	case errors.Is(err, databases.ErrNoApps):
		writeError(w, http.StatusServiceUnavailable, "apps_not_ready", "The service has no app hosting.")
	case errors.Is(err, databases.ErrNoPort):
		writeError(w, http.StatusConflict, "no_port", "All the ports for the home network (15432 to 15531) are in use.")
	case errors.Is(err, databases.ErrBackupNotFound):
		writeError(w, http.StatusNotFound, "backup_not_found", "The backup does not exist.")
	case errors.Is(err, databases.ErrNoUpdate):
		writeError(w, http.StatusConflict, "no_update", "The database has the newest minor version already.")
	case errors.Is(err, databases.ErrSessionsRemain):
		writeError(w, http.StatusConflict, "sessions_remain", "Sessions of the app are still open. Try again.")
	default:
		r.logger.Error("a database operation failed", "err", err)
		writeError(w, http.StatusInternalServerError, "database_failed", "The operation failed. The cause is in the log of the service.")
	}
}

func (r *Router) dbList(w http.ResponseWriter, _ *http.Request) {
	list := r.dbs.Runner.Store().List()
	out := make([]DatabaseView, 0, len(list))
	for _, d := range list {
		out = append(out, r.dbView(d))
	}
	writeJSON(w, http.StatusOK, map[string]any{"databases": out, "versions": r.versions()})
}

// versions gives the major versions that a new database can have.
func (r *Router) versions() []int {
	var out []int
	for v := range r.dbs.Runner.Images() {
		out = append(out, v)
	}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

type dbCreateBody struct {
	Name        string `json:"name"`
	Version     int    `json:"version"`
	Tier        string `json:"tier"`
	SizeBytes   int64  `json:"size_bytes"`
	CPUMillis   int    `json:"cpu_millis"`
	MemoryBytes int64  `json:"memory_bytes"`
	// RestoreFrom makes the database from a backup (T-06).
	RestoreFrom *databases.Restore `json:"restore_from"`
}

func (r *Router) dbCreate(w http.ResponseWriter, req *http.Request) {
	var b dbCreateBody
	if !readAppJSON(w, req, &b) {
		return
	}
	if b.Version == 0 {
		b.Version = databases.DefaultVersion
	}
	if b.Tier == "" {
		b.Tier = databases.TierPerformance
	}
	image := r.dbs.Runner.Images()[b.Version]
	if image == "" {
		writeError(w, http.StatusBadRequest, "bad_field", "version: NidaOS gives the versions "+intsText(r.versions())+".")
		return
	}
	d := databases.Database{Name: b.Name, Version: b.Version, Image: image, Tier: b.Tier,
		SizeBytes: b.SizeBytes, CPUMillis: b.CPUMillis, MemoryBytes: b.MemoryBytes}
	var created databases.Database
	var err error
	if b.RestoreFrom != nil {
		c, ok := r.dbController(w)
		if !ok {
			return
		}
		created, err = c.StartRestore(d, *b.RestoreFrom)
	} else {
		created, err = r.dbs.Runner.Store().Create(d)
		if c := r.dbs.Runner.Controller(); c != nil {
			c.Kick()
		}
	}
	if err != nil {
		r.writeDBError(w, err)
		return
	}
	r.logger.Info("a database was added", "database", created.Name, "version", created.Version, "tier", created.Tier)
	writeJSON(w, http.StatusAccepted, r.dbView(created))
}

func intsText(v []int) string {
	s := ""
	for i, n := range v {
		if i > 0 {
			s += " and "
		}
		s += strconv.Itoa(n)
	}
	return s
}

func (r *Router) dbGet(w http.ResponseWriter, req *http.Request) {
	d, ok := r.dbs.Runner.Store().Get(req.PathValue("name"))
	if !ok {
		r.writeDBError(w, databases.ErrNotFound)
		return
	}
	writeJSON(w, http.StatusOK, r.dbView(d))
}

func (r *Router) dbRemove(w http.ResponseWriter, req *http.Request) {
	c, ok := r.dbController(w)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 2*time.Minute)
	defer cancel()
	name := req.PathValue("name")
	if err := c.RemoveDatabase(ctx, name); err != nil {
		r.writeDBError(w, err)
		return
	}
	r.logger.Info("a database was removed", "database", name)
	w.WriteHeader(http.StatusNoContent)
}

// dbConnection gives the connection of the owner role, for a program on a
// device of the home network. It needs the admin level: it has a password.
func (r *Router) dbConnection(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	d, ok := r.dbs.Runner.Store().Get(name)
	if !ok {
		r.writeDBError(w, databases.ErrNotFound)
		return
	}
	body := map[string]any{
		"database": databases.DatabaseName(name),
		"user":     databases.OwnerRole(name),
		"password": r.dbs.Runner.Store().OwnerPassword(name),
		"lan":      d.LAN,
	}
	if d.LAN {
		u := url.URL{Scheme: "postgresql", User: url.UserPassword(databases.OwnerRole(name), r.dbs.Runner.Store().OwnerPassword(name)),
			Host: r.dbs.Host + ":" + strconv.Itoa(d.LANPort), Path: "/" + databases.DatabaseName(name), RawQuery: "sslmode=verify-full"}
		body["host"], body["port"], body["sslmode"], body["url"] = r.dbs.Host, d.LANPort, "verify-full", u.String()
		body["note"] = "Install the certificate of the NidaOS authority (GET /api/v1/ca.crt) and give it to the client as the root certificate."
	} else {
		body["note"] = "The switch for the home network is off: turn it on to connect from a device."
	}
	writeJSON(w, http.StatusOK, body)
}

func (r *Router) dbLink(w http.ResponseWriter, req *http.Request) {
	var b struct {
		App  string `json:"app"`
		Mode string `json:"mode"`
	}
	if !readAppJSON(w, req, &b) {
		return
	}
	c, ok := r.dbController(w)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), time.Minute)
	defer cancel()
	d, err := c.Link(ctx, req.PathValue("name"), b.App, b.Mode)
	if err != nil {
		r.writeDBError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, r.dbView(d))
}

func (r *Router) dbUnlink(w http.ResponseWriter, req *http.Request) {
	c, ok := r.dbController(w)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), time.Minute)
	defer cancel()
	d, err := c.Unlink(ctx, req.PathValue("name"), req.PathValue("app"))
	if err != nil {
		r.writeDBError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, r.dbView(d))
}

func (r *Router) dbLAN(w http.ResponseWriter, req *http.Request) {
	var b struct {
		On bool `json:"on"`
	}
	if !readAppJSON(w, req, &b) {
		return
	}
	c, ok := r.dbController(w)
	if !ok {
		return
	}
	d, err := c.SetLAN(req.Context(), req.PathValue("name"), b.On)
	if err != nil {
		r.writeDBError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, r.dbView(d))
}

func (r *Router) dbBackups(w http.ResponseWriter, req *http.Request) {
	c, ok := r.dbController(w)
	if !ok {
		return
	}
	list, err := c.Backups(req.PathValue("name"))
	if err != nil {
		r.writeDBError(w, err)
		return
	}
	if list == nil {
		list = []databases.Backup{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": list})
}

func (r *Router) dbBackup(w http.ResponseWriter, req *http.Request) {
	c, ok := r.dbController(w)
	if !ok {
		return
	}
	if err := c.StartBackup(context.WithoutCancel(req.Context()), req.PathValue("name")); err != nil {
		r.writeDBError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "The backup started. It is in the list of the backups at its end."})
}

func (r *Router) dbDeleteBackup(w http.ResponseWriter, req *http.Request) {
	c, ok := r.dbController(w)
	if !ok {
		return
	}
	if err := c.DeleteBackup(req.PathValue("name"), req.PathValue("backup")); err != nil {
		r.writeDBError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r *Router) dbUpdate(w http.ResponseWriter, req *http.Request) {
	c, ok := r.dbController(w)
	if !ok {
		return
	}
	d, err := c.StartUpdate(req.PathValue("name"))
	if err != nil {
		r.writeDBError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, r.dbView(d))
}
