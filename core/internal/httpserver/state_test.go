package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fathorMB/NidaOS/core/internal/system"
)

type fakeState struct{ err error }

func (f *fakeState) State() (system.State, error) {
	sys, _ := f.System()
	disks, _ := f.Disks()
	tiers, _ := f.Tiers()
	return system.State{System: sys, Disks: disks, Tiers: tiers}, f.err
}

func (f *fakeState) System() (system.System, error) {
	return system.System{Hostname: "nida"}, f.err
}

func (f *fakeState) Disks() ([]system.Disk, error) {
	return []system.Disk{{Name: "sda", Kind: system.KindSSD, Role: "os"}}, f.err
}

func (f *fakeState) Tiers() ([]system.Tier, error) {
	return []system.Tier{{Name: system.TierPerformance, State: system.TierReady}}, f.err
}

func get(t *testing.T, r http.Handler, path string, out any) int {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if out != nil {
		if err := json.NewDecoder(rec.Body).Decode(out); err != nil {
			t.Fatalf("%s: decode: %v", path, err)
		}
	}
	return rec.Code
}

func TestStateRoutes(t *testing.T) {
	r := newTestRouter()

	var st system.State
	if code := get(t, r, "/api/v1/state", &st); code != 200 || st.System.Hostname != "nida" || len(st.Disks) != 1 || len(st.Tiers) != 1 {
		t.Errorf("/state: %d %+v", code, st)
	}
	var sys system.System
	if code := get(t, r, "/api/v1/system", &sys); code != 200 || sys.Hostname != "nida" {
		t.Errorf("/system: %d %+v", code, sys)
	}
	var disks disksBody
	if code := get(t, r, "/api/v1/disks", &disks); code != 200 || len(disks.Disks) != 1 || disks.Disks[0].Role != "os" {
		t.Errorf("/disks: %d %+v", code, disks)
	}
	var tiers tiersBody
	if code := get(t, r, "/api/v1/tiers", &tiers); code != 200 || tiers.Tiers[0].State != system.TierReady {
		t.Errorf("/tiers: %d %+v", code, tiers)
	}
}

func TestStateError(t *testing.T) {
	r := NewRouter(Deps{State: &fakeState{err: errors.New("no /proc")}})
	for _, p := range []string{"/api/v1/state", "/api/v1/system", "/api/v1/disks", "/api/v1/tiers"} {
		var e Error
		if code := get(t, r, p, &e); code != http.StatusServiceUnavailable || e.Code != "state_unavailable" {
			t.Errorf("%s: %d %+v", p, code, e)
		}
	}
}

// TestStateFromFixture reads the real collector on the fixture of the
// system package, through the HTTP router.
func TestStateFromFixture(t *testing.T) {
	c := system.NewCollector("../system/testdata/host", "")
	r := NewRouter(Deps{State: c})
	var st system.State
	if code := get(t, r, "/api/v1/state", &st); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if st.System.CPU.Threads != 8 || len(st.Disks) != 3 || st.Tiers[1].State != system.TierNotMounted {
		t.Errorf("state = %+v", st)
	}
}
