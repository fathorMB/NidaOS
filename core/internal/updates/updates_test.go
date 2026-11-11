package updates

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

const statusJSON = `{"apiVersion":"org.containers.bootc/v1","kind":"BootcHost","status":{
 "staged":{"image":{"image":{"image":"10.0.2.2:5000/nidaos:v2","transport":"registry"},"version":"45.2","timestamp":"2026-11-11T10:00:00Z","imageDigest":"sha256:bb"},"pinned":false},
 "booted":{"image":{"image":{"image":"ghcr.io/fathormb/nidaos:proof","transport":"registry"},"version":"45.1","timestamp":"2026-11-01T10:00:00Z","imageDigest":"sha256:aa"},
   "cachedUpdate":{"image":{"image":"ghcr.io/fathormb/nidaos:proof"},"version":"45.3","imageDigest":"sha256:cc"},"pinned":false},
 "rollback":null,"rollbackQueued":false,"type":"bootcHost"}}`

func TestParse(t *testing.T) {
	st, err := Parse([]byte(statusJSON))
	if err != nil {
		t.Fatal(err)
	}
	if st.Booted == nil || st.Booted.Version != "45.1" || st.Booted.Image != "ghcr.io/fathormb/nidaos:proof" || st.Booted.Digest != "sha256:aa" {
		t.Fatalf("booted = %+v", st.Booted)
	}
	if st.Staged == nil || st.Staged.Version != "45.2" || st.Staged.Timestamp == nil {
		t.Fatalf("staged = %+v", st.Staged)
	}
	if st.Rollback != nil {
		t.Errorf("rollback = %+v", st.Rollback)
	}
	if st.Available == nil || st.Available.Version != "45.3" {
		t.Errorf("available = %+v", st.Available)
	}
	if _, err := Parse([]byte("not json")); err == nil {
		t.Error("Parse of a bad text: no error")
	}
}

type fakeBootc struct {
	mu    sync.Mutex
	calls []string
	out   string
	block chan struct{}
	fail  error
}

func (f *fakeBootc) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	block, fail := f.block, f.fail
	f.mu.Unlock()
	if name == "bootc" && len(args) > 0 && args[0] == "status" {
		return []byte(f.out), nil
	}
	if block != nil {
		<-block
	}
	return nil, fail
}

func (f *fakeBootc) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func wait(t *testing.T, s *Service) Status {
	t.Helper()
	for i := 0; i < 200; i++ {
		s.mu.Lock()
		busy := s.busy
		s.mu.Unlock()
		if busy == "" {
			st, _ := s.Status(context.Background())
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the operation does not stop")
	return Status{}
}

func TestOperations(t *testing.T) {
	f := &fakeBootc{out: statusJSON, block: make(chan struct{})}
	s := New(context.Background(), f.run, nil)
	s.RestartDelay = 0
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.Status(context.Background()); st.Busy != "checking" {
		t.Errorf("busy = %q", st.Busy)
	}
	if err := s.Download(); !errors.Is(err, ErrBusy) {
		t.Errorf("a second operation: %v", err)
	}
	close(f.block)
	if st := wait(t, s); st.LastCheck == nil || st.Error != "" {
		t.Errorf("after the check: %+v", st)
	}
	// No rollback version in the status.
	if err := s.Rollback(context.Background(), false); !errors.Is(err, ErrNoRollback) {
		t.Errorf("rollback: %v", err)
	}
	// A staged version: the restart reboots.
	if err := s.Restart(context.Background()); err != nil {
		t.Fatal(err)
	}
	wait(t, s)
	if calls := strings.Join(f.called(), "|"); !strings.Contains(calls, "systemctl reboot") {
		t.Errorf("calls = %s", calls)
	}
	// A failed operation keeps its error.
	f.mu.Lock()
	f.fail = errors.New("no network")
	f.mu.Unlock()
	if err := s.Download(); err != nil {
		t.Fatal(err)
	}
	if st := wait(t, s); !strings.Contains(st.Error, "no network") {
		t.Errorf("error = %q", st.Error)
	}
}

func TestRestartWithoutStaged(t *testing.T) {
	out := strings.Replace(statusJSON, `"staged":{`, `"old":{`, 1)
	out = strings.Replace(out, `"rollback":null`, `"staged":null,"rollback":{"image":{"image":{"image":"x"},"version":"45.0","imageDigest":"sha256:00"}}`, 1)
	f := &fakeBootc{out: out}
	s := New(context.Background(), f.run, nil)
	s.RestartDelay = 0
	if err := s.Restart(context.Background()); !errors.Is(err, ErrNothingToInstall) {
		t.Fatalf("restart: %v", err)
	}
	if err := s.Rollback(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	wait(t, s)
	calls := strings.Join(f.called(), "|")
	if !strings.Contains(calls, "bootc rollback|systemctl reboot") {
		t.Errorf("calls = %s", calls)
	}
}
