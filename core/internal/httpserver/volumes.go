package httpserver

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// GET /api/v1/volumes gives the volume folders of the storage tiers and
// the app or the database that uses each one (milestone M-08). The file
// program of the desktop shows them: the owner cannot read the folder
// "volumes" of a tier, nida-core can. The response has only the names.

// Volume is one volume folder of a tier.
type Volume struct {
	Tier string `json:"tier"`
	Name string `json:"name"`
	Path string `json:"path"`
	// Kind is app, database, system or other.
	Kind string `json:"kind"`
	// User is the name of the app or the database, or the claim.
	User string `json:"user"`
}

// VolumeUser reads the folder name of the local-path provisioner of k3s:
// "<volume>_<namespace>_<claim>".
func VolumeUser(name string) (kind, user string) {
	parts := strings.SplitN(name, "_", 3)
	if len(parts) < 3 || !strings.HasPrefix(parts[0], "pvc-") {
		return "other", ""
	}
	ns, claim := parts[1], parts[2]
	switch {
	case strings.HasPrefix(ns, "nida-app-"):
		return "app", strings.TrimPrefix(ns, "nida-app-")
	case strings.HasPrefix(ns, "nida-db-"):
		return "database", strings.TrimPrefix(ns, "nida-db-")
	case ns == "kube-system" || strings.HasPrefix(ns, "nida-"):
		return "system", claim
	}
	return "other", ns + "/" + claim
}

func (r *Router) addVolumeRoutes() {
	if r.state == nil {
		return
	}
	r.handle(http.MethodGet, "/api/v1/volumes", r.volumes)
}

func (r *Router) volumes(w http.ResponseWriter, _ *http.Request) {
	tiers, err := r.state.Tiers()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "state_unavailable", "The service cannot read the storage tiers.")
		return
	}
	out := []Volume{}
	for _, t := range tiers {
		if t.State != "ready" {
			continue
		}
		dir := filepath.Join(r.volumesRoot, filepath.FromSlash(t.Path), "volumes")
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			kind, user := VolumeUser(e.Name())
			out = append(out, Volume{Tier: t.Name, Name: e.Name(), Path: t.Path + "/volumes/" + e.Name(), Kind: kind, User: user})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	writeJSON(w, http.StatusOK, map[string]any{"volumes": out})
}
