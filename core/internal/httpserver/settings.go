package httpserver

import (
	"bytes"
	"net/http"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/settings"
	"github.com/fathorMB/NidaOS/core/internal/version"
)

// GET /api/v1/settings/backup gives a backup of the settings of NidaOS
// (milestone M-11): the owner password hash, the tokens, the apps, the
// databases, the GitHub App, the agents and the certificate authority.
// It has secrets, thus only the admin level gets it, never an agent.
func (r *Router) addSettingsRoutes() {
	if r.settingsDir == "" {
		return
	}
	r.handle(http.MethodGet, "/api/v1/settings/backup", r.settingsBackup)
}

func (r *Router) settingsBackup(w http.ResponseWriter, _ *http.Request) {
	var buf bytes.Buffer
	now := time.Now()
	if _, err := settings.Backup(&buf, r.settingsDir, version.Version, now); err != nil {
		r.logger.Error("the backup of the settings failed", "err", err)
		writeError(w, http.StatusInternalServerError, "backup_failed", "The backup of the settings failed.")
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="nidaos-settings-`+now.UTC().Format("20060102T150405Z")+`.tar.gz"`)
	_, _ = w.Write(buf.Bytes())
}
