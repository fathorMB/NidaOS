package installer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func demoServer(t *testing.T, seed Seed) (*Server, *[]string) {
	t.Helper()
	tmp := t.TempDir()
	var mu sync.Mutex
	var power []string
	s := &Server{
		Disks:   func(context.Context) ([]Disk, error) { return DemoDisks(), nil },
		Facts:   DemoFacts,
		Seed:    func(context.Context) (Seed, error) { return seed, nil },
		Measure: func(_ context.Context, p string, _ int64) (float64, error) { return demoSpeeds[p], nil },
		NewEngine: func(log func(string), step func(int, string), lines func() []string) *Engine {
			return &Engine{Run: DemoRunner{Target: filepath.Join(tmp, "target")}, Target: filepath.Join(tmp, "target"),
				TmpMount: filepath.Join(tmp, "tmp"), PayloadRef: "x", UEFI: true, Log: log, Step: step, Lines: lines}
		},
		Power: func(a string) { mu.Lock(); power = append(power, a); mu.Unlock() },
	}
	return s, &power
}

func waitPhase(t *testing.T, s *Server, phase string) State {
	t.Helper()
	for i := 0; i < 300; i++ {
		st := s.Snapshot(true, -1)
		if st.Phase == phase {
			return st
		}
		if st.Phase == PhaseFailed && phase != PhaseFailed {
			t.Fatalf("failed: %s\n%s", st.Error, strings.Join(st.Log, "\n"))
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the phase %s does not come: %s", phase, s.Snapshot(true, -1).Phase)
	return State{}
}

func TestServerProposesThePlan(t *testing.T) {
	s, _ := demoServer(t, Seed{Found: true, HasKey: true, Key: "ssh-ed25519 AAAA operator@pc"})
	s.Start(context.Background())
	st := waitPhase(t, s, PhaseReady)
	if roles(st.Plan) != "system=sdb performance=sdc standard=sda" {
		t.Errorf("plan = %s", roles(st.Plan))
	}
	if st.Disks[1].SpeedMBs != 486 || st.Disks[3].Usable {
		t.Errorf("disks = %+v", st.Disks)
	}

	h := s.Handler(context.Background())
	// A browser of an other computer only follows.
	req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	req.RemoteAddr = "192.168.1.20:5000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var got State
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if got.Control || got.Seed.Key != "" {
		t.Errorf("remote state: control=%v key=%q", got.Control, got.Seed.Key)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/install", strings.NewReader(`{}`))
	req.RemoteAddr = "192.168.1.20:5000"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("remote install: %d", rec.Code)
	}

	// The screen: a wrong answer, then a good one.
	body := `{"roles":{"sdb":"system","sdc":"performance","sda":"standard"},"hostname":"home","password":"short","confirm":"home","ssh_key":"seed"}`
	req = httptest.NewRequest(http.MethodPost, "/api/install", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:4000"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"field":"password"`) {
		t.Errorf("bad answer: %d %s", rec.Code, rec.Body)
	}
	body = strings.Replace(body, `"short"`, `"a long owner password"`, 1)
	req = httptest.NewRequest(http.MethodPost, "/api/install", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:4000"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("install: %d %s", rec.Code, rec.Body)
	}
	st = waitPhase(t, s, PhaseDone)
	for _, x := range st.Steps {
		if x.State != "done" {
			t.Errorf("step %+v", x)
		}
	}
	if st.Answers == nil || st.Answers.Password == "a long owner password" || st.Answers.Hostname != "home" {
		t.Errorf("answers = %+v", st.Answers)
	}
	if strings.Contains(strings.Join(st.Log, "\n"), "a long owner password") {
		t.Error("the log has the password")
	}
}

func TestServerAutomaticInstallation(t *testing.T) {
	auto := &Auto{Answers: Answers{Password: "a long owner password", Hostname: "nidaos"}, Plan: "proposed", Erase: "yes", After: "poweroff"}
	s, power := demoServer(t, Seed{Found: true, HasKey: true, Key: "ssh-ed25519 AAAA operator@pc", Auto: auto})
	s.Start(context.Background())
	st := s.Snapshot(true, -1)
	if st.Phase != PhaseDone || !st.Auto || len(*power) != 1 || (*power)[0] != "poweroff" {
		t.Fatalf("auto: phase=%s power=%v error=%s", st.Phase, *power, st.Error)
	}
}

func TestServerPlanOnly(t *testing.T) {
	s, power := demoServer(t, Seed{Found: true, Auto: &Auto{PlanOnly: true}})
	s.Start(context.Background())
	if st := s.Snapshot(false, 0); st.Phase != PhaseReady || !st.PlanOnly || len(*power) != 0 {
		t.Fatalf("plan only: %+v", st)
	}
}
