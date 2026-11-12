package logs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

var day1 = time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)

func openTestStore(t *testing.T, dir string, c *clock) *Store {
	t.Helper()
	s, err := openStore(dir, 0, 0, quiet, c.now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func add(s *Store, c *clock, at time.Time, src, level, msg string) {
	c.set(at)
	s.Append(Line{Seq: 99, Time: at, Source: ServiceSource(src), Level: level, Message: msg})
}

func msgs(ls []Line) string {
	var out []string
	for _, l := range ls {
		out = append(out, l.Message)
	}
	return strings.Join(out, ",")
}

// fill writes 3 lines on day 1 and 3 lines on day 2. Day 1 is compressed.
func fill(t *testing.T) (*Store, *clock, string) {
	dir := t.TempDir()
	c := &clock{t: day1}
	s := openTestStore(t, dir, c)
	add(s, c, day1, "k3s", LevelInfo, "a1")
	add(s, c, day1.Add(time.Minute), "nida-core", LevelError, "a2 <failed> & stopped")
	add(s, c, day1.Add(2*time.Minute), "k3s", LevelWarning, "a3")
	day2 := day1.Add(24 * time.Hour)
	add(s, c, day2, "k3s", LevelInfo, "b1")
	add(s, c, day2.Add(time.Minute), "sshd", LevelInfo, "b2 accepted")
	add(s, c, day2.Add(2*time.Minute), "k3s", LevelError, "b3")
	s.Maintain() // waits for the compression of day 1
	return s, c, dir
}

func TestStoreFiles(t *testing.T) {
	_, _, dir := fill(t)
	for _, name := range []string{"2026-11-09.jsonl.gz", "2026-11-10.jsonl"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-11-09.jsonl")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the plain file of day 1 must be deleted after the compression: %v", err)
	}
}

func TestStoreSearch(t *testing.T) {
	s, _, _ := fill(t)
	all, err := s.Search(Query{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if got := msgs(all.Lines); got != "b3,b2 accepted,b1,a3,a2 <failed> & stopped,a1" {
		t.Errorf("all = %q", got)
	}
	if all.NextCursor != "" {
		t.Errorf("a complete search has no cursor: %q", all.NextCursor)
	}
	if all.Lines[0].Seq != 0 {
		t.Errorf("the store must not keep the live numbers: %d", all.Lines[0].Seq)
	}

	for q, want := range map[*Query]string{
		{Filter: Filter{Level: LevelWarning}}:                                               "b3,a3,a2 <failed> & stopped",
		{Filter: Filter{Source: "service/k3s"}}:                                             "b3,b1,a3,a1",
		{Filter: Filter{Text: "<FAILED> &"}}:                                                "a2 <failed> & stopped",
		{Since: day1.Add(90 * time.Second), Until: day1.Add(24*time.Hour + 90*time.Second)}: "b2 accepted,b1,a3",
	} {
		q.Limit = 100
		res, err := s.Search(*q)
		if err != nil {
			t.Fatal(err)
		}
		if got := msgs(res.Lines); got != want {
			t.Errorf("%+v: got %q, want %q", *q, got, want)
		}
	}
}

func TestStorePages(t *testing.T) {
	s, _, _ := fill(t)
	var pages []string
	cursor := ""
	for i := 0; i < 10; i++ {
		res, err := s.Search(Query{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		pages = append(pages, msgs(res.Lines))
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	// The last page is empty: the store knows the end only after a read.
	if got := strings.Join(pages, "|"); got != "b3,b2 accepted|b1,a3|a2 <failed> & stopped,a1|" {
		t.Errorf("pages = %q", got)
	}

	for _, bad := range []string{"x", "2026-11-10", "2026-11-10:-1", "day:1"} {
		if _, err := s.Search(Query{Cursor: bad}); !errors.Is(err, ErrBadCursor) {
			t.Errorf("cursor %q: err = %v", bad, err)
		}
	}
}

func TestStoreNewLinesAfterCursor(t *testing.T) {
	// A cursor stays valid when new lines come: the next page does not
	// give the new lines again.
	s, c, _ := fill(t)
	first, _ := s.Search(Query{Limit: 2})
	add(s, c, c.now().Add(time.Minute), "k3s", LevelInfo, "b4")
	next, _ := s.Search(Query{Limit: 2, Cursor: first.NextCursor})
	if got := msgs(next.Lines); got != "b1,a3" {
		t.Errorf("next = %q", got)
	}
}

func TestStoreReopen(t *testing.T) {
	s, c, dir := fill(t)
	s.Close()
	s2 := openTestStore(t, dir, c)
	add(s2, c, c.now().Add(time.Minute), "k3s", LevelInfo, "b4")
	res, _ := s2.Search(Query{Limit: 2})
	if got := msgs(res.Lines); got != "b4,b3" {
		t.Errorf("after reopen = %q", got)
	}
}

func TestStoreRetention(t *testing.T) {
	s, c, dir := fill(t)
	// 31 days after day 1: day 1 is too old, day 2 is not.
	c.set(day1.Add(31*24*time.Hour + time.Hour))
	s.Maintain()
	if _, err := os.Stat(filepath.Join(dir, "2026-11-09.jsonl.gz")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("day 1 must be deleted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-11-10.jsonl")); err != nil {
		t.Errorf("day 2 must stay: %v", err)
	}
}

func TestStoreMaxBytes(t *testing.T) {
	s, c, dir := fill(t)
	// A new day makes day 2 old. A very small limit keeps only today.
	add(s, c, day1.Add(48*time.Hour), "k3s", LevelInfo, "c1")
	s.Flush()
	s.MaxBytes = 1
	s.Maintain()
	files := s.files()
	if len(files) != 1 || files[0].day != "2026-11-11" {
		t.Errorf("files = %+v", files)
	}
	_ = dir
}

func TestJournalCursor(t *testing.T) {
	state := filepath.Join(t.TempDir(), "journal.cursor")
	var mu sync.Mutex
	var starts []string
	entries := `{"__CURSOR":"s=1","MESSAGE":"one"}` + "\n" + `{"__CURSOR":"s=2"}` + "\n"

	run := func(data string, waitErr error, stopAfter int) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		got := 0
		j := &Journal{Logger: quiet, StateFile: state, open: func(_ context.Context, cursor string) (io.ReadCloser, func() error, error) {
			mu.Lock()
			starts = append(starts, cursor)
			if len(starts) >= stopAfter {
				cancel()
			}
			mu.Unlock()
			return io.NopCloser(strings.NewReader(data)), func() error { return waitErr }, nil
		}}
		j.Run(ctx, func(Line) { got++ })
	}

	// The first run has no cursor. The entry without a message moves the
	// cursor too.
	run(entries, nil, 1)
	b, err := os.ReadFile(state)
	if err != nil || strings.TrimSpace(string(b)) != "s=2" {
		t.Fatalf("state = %q, err = %v", b, err)
	}

	// The second run starts after the saved cursor.
	run("", nil, 1)
	if starts[1] != "s=2" {
		t.Errorf("starts = %q", starts)
	}

	// journalctl does not accept the cursor and gives no entry: the
	// reader starts again with the new lines.
	starts = nil
	ctx, cancel := context.WithCancel(context.Background())
	j := &Journal{Logger: quiet, StateFile: state, open: func(_ context.Context, cursor string) (io.ReadCloser, func() error, error) {
		mu.Lock()
		starts = append(starts, cursor)
		if len(starts) == 2 {
			cancel()
		}
		mu.Unlock()
		return io.NopCloser(strings.NewReader("")), func() error { return errors.New("exit 1") }, nil
	}}
	done := make(chan struct{})
	go func() { j.Run(ctx, func(Line) {}); close(done) }()
	select {
	case <-done:
	case <-time.After(journalRetry + 3*time.Second):
		t.Fatal("Run did not stop")
	}
	if len(starts) != 2 || starts[0] != "s=2" || starts[1] != "" {
		t.Errorf("starts = %q", starts)
	}
}
