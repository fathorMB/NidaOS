// Package updates gives the versions of the system and starts the update
// operations of bootc (milestone M-06, the updates page of the console).
//
// bootc keeps two system versions on the disk: the version in operation
// (booted) and the version before it (rollback). An update downloads a new
// version (staged) that starts at the next restart. The package only calls
// the bootc command: bootc does the work.
package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Errors of the operations.
var (
	// ErrBusy: an other operation is in progress.
	ErrBusy = errors.New("an update operation is in progress")
	// ErrNothingToInstall: no staged version and no rollback to apply.
	ErrNothingToInstall = errors.New("no version waits for a restart")
	// ErrNoRollback: bootc has no version before the version in operation.
	ErrNoRollback = errors.New("there is no version for a rollback")
	// ErrUnavailable: the bootc command does not answer.
	ErrUnavailable = errors.New("bootc is not available")
)

// Deployment is one system version on the disk.
type Deployment struct {
	// Image is the address of the image, for example ghcr.io/fathormb/nidaos:proof.
	Image     string     `json:"image"`
	Version   string     `json:"version,omitempty"`
	Timestamp *time.Time `json:"timestamp,omitempty"`
	Digest    string     `json:"digest,omitempty"`
	Pinned    bool       `json:"pinned,omitempty"`
}

// Status is the body of GET /api/v1/updates.
type Status struct {
	Booted   *Deployment `json:"booted,omitempty"`
	Staged   *Deployment `json:"staged,omitempty"`
	Rollback *Deployment `json:"rollback,omitempty"`
	// Available is the newer version that the last check found. It is not
	// on the disk yet.
	Available *Deployment `json:"available,omitempty"`
	// RollbackQueued: the next restart starts the rollback version.
	RollbackQueued bool `json:"rollback_queued,omitempty"`
	// Busy is the operation in progress: checking, downloading,
	// rolling_back or restarting.
	Busy      string     `json:"busy,omitempty"`
	LastCheck *time.Time `json:"last_check,omitempty"`
	// Error is the result of the last operation that failed.
	Error string `json:"error,omitempty"`
}

// Runner runs a command and gives its standard output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// ExecRunner runs the command on the computer.
func ExecRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 500 {
			msg = msg[len(msg)-500:]
		}
		if msg != "" {
			return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, msg)
		}
		return out, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return out, nil
}

// Service keeps the state of the operations.
type Service struct {
	run    Runner
	logger *slog.Logger
	// RestartDelay is the time before the restart, thus the response goes
	// to the browser first.
	RestartDelay time.Duration
	now          func() time.Time

	mu        sync.Mutex
	busy      string
	lastErr   string
	lastCheck *time.Time
	cache     *Status
	cacheAt   time.Time
	// bg is the context of the operations. They continue after the end
	// of the HTTP request.
	bg context.Context
}

// New makes the service. bg stops the operations when the service stops.
func New(bg context.Context, run Runner, logger *slog.Logger) *Service {
	if run == nil {
		run = ExecRunner
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Service{run: run, logger: logger, RestartDelay: 2 * time.Second, now: time.Now, bg: bg}
}

// bootc host status (bootc status --format=json). Only the fields that
// NidaOS uses.
type hostStatus struct {
	Status struct {
		Staged         *bootEntry `json:"staged"`
		Booted         *bootEntry `json:"booted"`
		Rollback       *bootEntry `json:"rollback"`
		RollbackQueued bool       `json:"rollbackQueued"`
	} `json:"status"`
}

type bootEntry struct {
	Image        *imageStatus `json:"image"`
	CachedUpdate *imageStatus `json:"cachedUpdate"`
	Pinned       bool         `json:"pinned"`
}

type imageStatus struct {
	Image struct {
		Image string `json:"image"`
	} `json:"image"`
	Version     string     `json:"version"`
	Timestamp   *time.Time `json:"timestamp"`
	ImageDigest string     `json:"imageDigest"`
}

func (i *imageStatus) deployment(pinned bool) *Deployment {
	if i == nil {
		return nil
	}
	return &Deployment{Image: i.Image.Image, Version: i.Version, Timestamp: i.Timestamp, Digest: i.ImageDigest, Pinned: pinned}
}

func (b *bootEntry) deployment() *Deployment {
	if b == nil {
		return nil
	}
	return b.Image.deployment(b.Pinned)
}

// Parse reads the output of "bootc status --format=json".
func Parse(b []byte) (Status, error) {
	var h hostStatus
	if err := json.Unmarshal(b, &h); err != nil {
		return Status{}, fmt.Errorf("the output of bootc status is not correct: %w", err)
	}
	st := Status{
		Booted:         h.Status.Booted.deployment(),
		Staged:         h.Status.Staged.deployment(),
		Rollback:       h.Status.Rollback.deployment(),
		RollbackQueued: h.Status.RollbackQueued,
	}
	if b := h.Status.Booted; b != nil && b.CachedUpdate != nil {
		st.Available = b.CachedUpdate.deployment(false)
		// A staged version with the same digest is not "available" again.
		if st.Staged != nil && st.Staged.Digest == st.Available.Digest {
			st.Available = nil
		}
	}
	return st, nil
}

// Status gives the versions. It reads bootc at most each 3 seconds.
func (s *Service) Status(ctx context.Context) (Status, error) {
	s.mu.Lock()
	if s.cache != nil && s.now().Sub(s.cacheAt) < 3*time.Second {
		st := s.withState(*s.cache)
		s.mu.Unlock()
		return st, nil
	}
	s.mu.Unlock()
	out, err := s.run(ctx, "bootc", "status", "--format=json")
	if err != nil {
		s.logger.Warn("bootc status failed", "err", err)
		return Status{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	st, err := Parse(out)
	if err != nil {
		return Status{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache, s.cacheAt = &st, s.now()
	return s.withState(st), nil
}

func (s *Service) withState(st Status) Status {
	st.Busy, st.Error, st.LastCheck = s.busy, s.lastErr, s.lastCheck
	return st
}

// start runs op in the background, when no other operation runs.
func (s *Service) start(name string, op func(ctx context.Context) error) error {
	s.mu.Lock()
	if s.busy != "" {
		s.mu.Unlock()
		return ErrBusy
	}
	s.busy, s.lastErr = name, ""
	s.mu.Unlock()
	s.logger.Info("an update operation starts", "operation", name)
	go func() {
		err := op(s.bg)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.busy, s.cache = "", nil
		if err != nil {
			s.lastErr = err.Error()
			s.logger.Error("an update operation failed", "operation", name, "err", err)
			return
		}
		s.logger.Info("an update operation is complete", "operation", name)
	}()
	return nil
}

// Check asks the registry for a newer version (bootc upgrade --check).
func (s *Service) Check() error {
	return s.start("checking", func(ctx context.Context) error {
		_, err := s.run(ctx, "bootc", "upgrade", "--check")
		now := s.now()
		s.mu.Lock()
		s.lastCheck = &now
		s.mu.Unlock()
		return err
	})
}

// Download gets the newer version and stages it for the next restart.
func (s *Service) Download() error {
	return s.start("downloading", func(ctx context.Context) error {
		_, err := s.run(ctx, "bootc", "upgrade")
		return err
	})
}

// Rollback makes the version before the version for the next restart.
// With restart, the computer restarts after it.
func (s *Service) Rollback(ctx context.Context, restart bool) error {
	st, err := s.Status(ctx)
	if err != nil {
		return err
	}
	if st.Rollback == nil {
		return ErrNoRollback
	}
	return s.start("rolling_back", func(ctx context.Context) error {
		if !st.RollbackQueued {
			if _, err := s.run(ctx, "bootc", "rollback"); err != nil {
				return err
			}
		}
		if restart {
			return s.reboot(ctx)
		}
		return nil
	})
}

// Restart starts the staged version or the queued rollback.
func (s *Service) Restart(ctx context.Context) error {
	st, err := s.Status(ctx)
	if err != nil {
		return err
	}
	if st.Staged == nil && !st.RollbackQueued {
		return ErrNothingToInstall
	}
	return s.start("restarting", s.reboot)
}

func (s *Service) reboot(ctx context.Context) error {
	s.logger.Warn("the computer restarts for an update")
	select {
	case <-time.After(s.RestartDelay):
	case <-ctx.Done():
		return ctx.Err()
	}
	_, err := s.run(ctx, "systemctl", "reboot")
	return err
}

// CheckEvery runs Check after first, then each interval, until ctx stops
// (milestone M-11, task T-02). The check only asks the registry: it
// downloads nothing. The owner downloads and installs the update in the
// console, thus no update runs without the approval of the owner.
func (s *Service) CheckEvery(ctx context.Context, first, interval time.Duration) {
	if interval <= 0 {
		return
	}
	t := time.NewTimer(first)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := s.Check(); err != nil && !errors.Is(err, ErrBusy) {
			s.logger.Warn("the automatic check for an update did not start", "err", err)
		}
		t.Reset(interval)
	}
}
