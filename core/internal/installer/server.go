package installer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Phases of the installer.
const (
	PhaseStarting   = "starting"
	PhaseMeasuring  = "measuring"
	PhaseReady      = "ready"
	PhaseInstalling = "installing"
	PhaseDone       = "done"
	PhaseFailed     = "failed"
)

// StepState is one step of the installation for the progress view.
type StepState struct {
	Name  string `json:"name"`
	State string `json:"state"` // "", run, done, failed
	// Seconds is the time of the step, also while it runs.
	Seconds int `json:"seconds"`
	started time.Time
}

// State is the body of GET /api/state.
type State struct {
	Phase string `json:"phase"`
	// Control: this browser can change the choices. Only the screen of the
	// computer can: a browser of an other computer only follows.
	Control bool   `json:"control"`
	Facts   Facts  `json:"facts"`
	Disks   []Disk `json:"disks"`
	Plan    Plan   `json:"plan"`
	Seed    Seed   `json:"seed"`
	// Measuring is the disk that the installer measures now.
	Measuring string      `json:"measuring,omitempty"`
	Steps     []StepState `json:"steps"`
	Error     string      `json:"error,omitempty"`
	// Answers are the choices of the installation, without the password.
	Answers *Answers `json:"answers,omitempty"`
	// Auto: the seed volume starts an installation without questions.
	Auto     bool     `json:"auto"`
	PlanOnly bool     `json:"plan_only,omitempty"`
	Log      []string `json:"log"`
	Started  string   `json:"started,omitempty"`
	Finished string   `json:"finished,omitempty"`
}

// Server is the backend of the installer page.
type Server struct {
	Logger  *slog.Logger
	Disks   func(ctx context.Context) ([]Disk, error)
	Facts   func() Facts
	Seed    func(ctx context.Context) (Seed, error)
	Measure Measurer
	// NewEngine gives the engine of an installation. log and step are the
	// callbacks of the progress.
	NewEngine func(log func(string), step func(int, string), lines func() []string) *Engine
	// Power runs "poweroff" or "reboot" at the end.
	Power func(action string)
	UI    http.Handler

	mu      sync.Mutex
	factsAt time.Time
	st      State
	seed    Seed
	busy    bool
	ready   chan struct{}
}

const maxLog = 4000

func (s *Server) logger() *slog.Logger {
	if s.Logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return s.Logger
}

// Start reads the computer, measures the disks and makes the plan. With an
// automatic seed, it then installs. It returns when the work is complete.
func (s *Server) Start(ctx context.Context) {
	s.mu.Lock()
	s.st = State{Phase: PhaseStarting, Steps: newSteps()}
	s.ready = make(chan struct{})
	s.mu.Unlock()

	facts := s.Facts()
	seed, err := s.Seed(ctx)
	if err != nil {
		s.logLine("the seed volume: " + err.Error())
	}
	disks, err := s.Disks(ctx)
	s.mu.Lock()
	s.st.Facts, s.seed, s.factsAt = facts, seed, time.Now()
	s.st.Seed = seed
	if seed.Auto != nil {
		s.st.Auto, s.st.PlanOnly = true, seed.Auto.PlanOnly
	}
	if err != nil {
		s.st.Phase, s.st.Error = PhaseFailed, "The installer cannot read the disks: "+err.Error()
		s.mu.Unlock()
		close(s.ready)
		return
	}
	s.st.Disks, s.st.Phase = disks, PhaseMeasuring
	s.mu.Unlock()
	s.logLine("the computer: " + facts.Computer + ", " + facts.Firmware)

	s.measureAll(ctx)
	close(s.ready)

	if seed.Auto != nil && !seed.Auto.PlanOnly {
		s.autoInstall(ctx, seed)
	}
}

func newSteps() []StepState {
	out := make([]StepState, len(StepNames))
	for i, n := range StepNames {
		out[i].Name = n
	}
	return out
}

// measureAll measures each disk that the plan can use, then makes the plan.
func (s *Server) measureAll(ctx context.Context) {
	s.mu.Lock()
	disks := append([]Disk(nil), s.st.Disks...)
	s.st.Phase = PhaseMeasuring
	s.mu.Unlock()
	for i, d := range disks {
		if !d.Usable {
			continue
		}
		s.mu.Lock()
		s.st.Measuring = d.Name
		s.mu.Unlock()
		v, err := s.Measure(ctx, d.Path, d.SizeBytes)
		if err != nil {
			s.logLine("the speed of " + d.Name + ": " + err.Error())
			continue
		}
		disks[i].SpeedMBs = float64(int(v + 0.5))
		s.logLine("the speed of " + d.Name + ": " + strconv.Itoa(int(v+0.5)) + " MB/s")
		s.mu.Lock()
		s.st.Disks = append([]Disk(nil), disks...)
		s.mu.Unlock()
	}
	plan := Propose(disks)
	s.mu.Lock()
	s.st.Disks, s.st.Plan, s.st.Measuring, s.st.Phase = disks, plan, "", PhaseReady
	s.mu.Unlock()
	s.logLine("the proposed plan: system " + plan.DiskOf(RoleSystem) + ", performance " + plan.DiskOf(RolePerformance) + ", standard " + plan.DiskOf(RoleStandard))
}

func (s *Server) autoInstall(ctx context.Context, seed Seed) {
	a := seed.Auto.Answers
	a.Normalize()
	s.mu.Lock()
	if seed.Auto.Plan == "proposed" || len(a.Roles) == 0 {
		a.Roles = s.st.Plan.Roles
	}
	s.mu.Unlock()
	if a.SSHKey == "" && seed.HasKey {
		a.SSHKey = "seed"
	}
	a.Confirm = a.Hostname
	if err := s.install(ctx, a); err != nil {
		s.logLine("the automatic installation did not start: " + err.Error())
		s.mu.Lock()
		s.st.Phase, s.st.Error = PhaseFailed, err.Error()
		s.mu.Unlock()
		return
	}
	s.wait()
	s.mu.Lock()
	phase := s.st.Phase
	s.mu.Unlock()
	if phase == PhaseDone && seed.Auto.After != "stay" && s.Power != nil {
		s.logLine("the automatic installation is complete: " + seed.Auto.After)
		time.Sleep(3 * time.Second)
		s.Power(seed.Auto.After)
	}
}

// wait waits for the end of the installation.
func (s *Server) wait() {
	for {
		s.mu.Lock()
		busy := s.busy
		s.mu.Unlock()
		if !busy {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// ErrBusy: an installation runs, or the installer did not complete the plan.
var ErrBusy = errors.New("the installer is not ready for an installation")

// install starts the installation in the background.
func (s *Server) install(ctx context.Context, a Answers) error {
	s.mu.Lock()
	if s.busy || (s.st.Phase != PhaseReady && s.st.Phase != PhaseFailed) || s.st.Disks == nil {
		s.mu.Unlock()
		return ErrBusy
	}
	disks := append([]Disk(nil), s.st.Disks...)
	seedKey := s.seed.Key
	s.mu.Unlock()
	if err := a.Validate(disks, seedKey); err != nil {
		return err
	}
	red := a.Redacted()
	s.mu.Lock()
	s.busy = true
	s.st.Phase, s.st.Error, s.st.Answers = PhaseInstalling, "", &red
	s.st.Steps = newSteps()
	s.st.Started, s.st.Finished = time.Now().UTC().Format(time.RFC3339), ""
	s.mu.Unlock()
	s.logLine("the installation starts")
	e := s.NewEngine(s.logLine, s.step, s.lines)
	s.mu.Lock()
	e.Settings = s.seed.Settings
	s.mu.Unlock()
	go func() {
		err := e.Install(ctx, a, disks, seedKey)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.busy = false
		s.st.Finished = time.Now().UTC().Format(time.RFC3339)
		if err != nil {
			s.st.Phase, s.st.Error = PhaseFailed, err.Error()
			s.logger().Error("the installation failed", "err", err)
			return
		}
		s.st.Phase = PhaseDone
		s.logger().Info("the installation is complete")
	}()
	return nil
}

func (s *Server) step(i int, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i < 0 || i >= len(s.st.Steps) {
		return
	}
	st := &s.st.Steps[i]
	switch state {
	case "run":
		st.started = time.Now()
	default:
		st.Seconds = int(time.Since(st.started).Seconds())
	}
	st.State = state
}

func (s *Server) logLine(l string) {
	s.logger().Info(l)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.Log = append(s.st.Log, time.Now().Format("15:04:05")+" "+l)
	if len(s.st.Log) > maxLog {
		s.st.Log = s.st.Log[len(s.st.Log)-maxLog:]
	}
}

func (s *Server) lines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.st.Log...)
}

// Snapshot gives the state for a client. The facts come again each 10
// seconds: the network cable can connect after the start.
func (s *Server) Snapshot(control bool, logTail int) State {
	s.mu.Lock()
	refresh := s.Facts != nil && !s.factsAt.IsZero() && time.Since(s.factsAt) > 10*time.Second
	s.mu.Unlock()
	if refresh {
		f := s.Facts()
		s.mu.Lock()
		s.st.Facts, s.factsAt = f, time.Now()
		s.mu.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st
	st.Control = control
	st.Steps = append([]StepState(nil), s.st.Steps...)
	for i := range st.Steps {
		if st.Steps[i].State == "run" {
			st.Steps[i].Seconds = int(time.Since(s.st.Steps[i].started).Seconds())
		}
	}
	if logTail >= 0 && len(st.Log) > logTail {
		st.Log = st.Log[len(st.Log)-logTail:]
	}
	st.Log = append([]string(nil), st.Log...)
	return st
}

// isLocal tells if the request comes from the screen of the computer.
func isLocal(req *http.Request) bool {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Handler gives the HTTP handler of the installer.
func (s *Server) Handler(ctx context.Context) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, req *http.Request) {
		writeJSON(w, http.StatusOK, s.Snapshot(isLocal(req), 200))
	})
	mux.HandleFunc("GET /api/log", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, strings.Join(s.lines(), "\n")+"\n")
	})
	mux.HandleFunc("POST /api/measure", func(w http.ResponseWriter, req *http.Request) {
		if !local(w, req) {
			return
		}
		s.mu.Lock()
		ok := s.st.Phase == PhaseReady && !s.busy
		if ok {
			s.st.Phase = PhaseMeasuring
		}
		s.mu.Unlock()
		if !ok {
			writeErr(w, http.StatusConflict, "busy", "The installer measures the disks or installs now.")
			return
		}
		go s.measureAll(ctx)
		writeJSON(w, http.StatusAccepted, s.Snapshot(true, 0))
	})
	mux.HandleFunc("POST /api/install", func(w http.ResponseWriter, req *http.Request) {
		if !local(w, req) {
			return
		}
		var a Answers
		dec := json.NewDecoder(http.MaxBytesReader(w, req.Body, 64<<10))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&a); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_body", "The body is not correct: "+err.Error())
			return
		}
		a.Normalize()
		err := s.install(ctx, a)
		var fe *FieldError
		switch {
		case errors.As(err, &fe):
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_field", "field": fe.Field, "message": fe.Message})
		case errors.Is(err, ErrBusy):
			writeErr(w, http.StatusConflict, "busy", "The installer is not ready, or an installation runs.")
		case err != nil:
			writeErr(w, http.StatusInternalServerError, "install_failed", err.Error())
		default:
			writeJSON(w, http.StatusAccepted, s.Snapshot(true, 0))
		}
	})
	mux.HandleFunc("POST /api/restart", func(w http.ResponseWriter, req *http.Request) {
		if !local(w, req) {
			return
		}
		s.mu.Lock()
		done := s.st.Phase == PhaseDone
		s.mu.Unlock()
		if !done {
			writeErr(w, http.StatusConflict, "not_done", "The installation is not complete.")
			return
		}
		w.WriteHeader(http.StatusAccepted)
		if s.Power != nil {
			go func() {
				time.Sleep(time.Second)
				s.Power("reboot")
			}()
		}
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeErr(w, http.StatusNotFound, "not_found", "The path does not exist.")
	})
	if s.UI != nil {
		mux.Handle("/", s.UI)
	}
	return mux
}

func local(w http.ResponseWriter, req *http.Request) bool {
	if isLocal(req) {
		return true
	}
	writeErr(w, http.StatusForbidden, "remote", "Only the screen of the computer can change the installation. This browser can follow it.")
	return false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"code": code, "message": msg})
}
