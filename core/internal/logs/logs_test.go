package logs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var quiet = slog.New(slog.DiscardHandler)

func TestDetectLevel(t *testing.T) {
	for msg, want := range map[string]string{
		`{"time":"x","level":"WARN","msg":"pool full"}`:     LevelWarning,
		`{"severity":"error","message":"boom"}`:             LevelError,
		`time=2026-11-10T12:00:00Z level=debug msg="tick"`:  LevelDebug,
		`[ERROR] cannot open the file`:                      LevelError,
		`WARNING: the disk is slow`:                         LevelWarning,
		`2026-11-10 12:00:00.123 ERROR checkout failed`:     LevelError,
		`E1110 12:00:00.000000    1 reflector.go:1] failed`: LevelError,
		`W1110 12:00:00.000000    1 main.go:1] slow`:        LevelWarning,
		`GET /orders 200 in 41 ms`:                          LevelInfo,
		`the error count is 0`:                              LevelInfo, // not at the start
		`{"msg":"no level field"}`:                          LevelInfo,
	} {
		if got := DetectLevel(msg, LevelInfo); got != want {
			t.Errorf("DetectLevel(%q) = %q, want %q", msg, got, want)
		}
	}
}

func TestFilter(t *testing.T) {
	l := Line{Source: ServiceSource("k3s"), Level: LevelWarning, Message: "Node Ready"}
	for f, want := range map[Filter]bool{
		{}:                      true,
		{Source: "service"}:     true,
		{Source: "service/k3s"}: true,
		{Source: "service/k3"}:  false,
		{Source: "pod"}:         false,
		{Level: LevelInfo}:      true,
		{Level: LevelError}:     false,
		{Text: "node ready"}:    true,
		{Text: "failed"}:        false,
		{Source: "service/", Text: "ready", Level: LevelWarning}: true,
	} {
		if got := f.Match(l); got != want {
			t.Errorf("%+v: Match = %v, want %v", f, got, want)
		}
	}
}

func TestParseJournal(t *testing.T) {
	l, ok := parseJournal([]byte(`{"__REALTIME_TIMESTAMP":"1762776000000000","PRIORITY":"4",` +
		`"_SYSTEMD_UNIT":"k3s.service","SYSLOG_IDENTIFIER":"k3s","MESSAGE":"slow disk\n"}`))
	if !ok || l.Source.ID != "service/k3s" || l.Level != LevelWarning || l.Message != "slow disk" ||
		!l.Time.Equal(time.UnixMicro(1762776000000000)) {
		t.Errorf("line = %+v, ok = %v", l, ok)
	}

	// The kernel has no unit. A JSON message of nida-core gives its level.
	l, _ = parseJournal([]byte(`{"PRIORITY":"6","SYSLOG_IDENTIFIER":"kernel","MESSAGE":"usb 1-1: new device"}`))
	if l.Source.Name != "kernel" || l.Level != LevelInfo {
		t.Errorf("kernel line = %+v", l)
	}
	l, _ = parseJournal([]byte(`{"PRIORITY":"6","_SYSTEMD_UNIT":"nida-core.service","MESSAGE":"{\"level\":\"ERROR\",\"msg\":\"x\"}"}`))
	if l.Source.Name != "nida-core" || l.Level != LevelError {
		t.Errorf("nida-core line = %+v", l)
	}

	// A session scope uses the program name. A message that is not UTF-8
	// comes as an array of bytes.
	l, _ = parseJournal([]byte(`{"PRIORITY":"2","_SYSTEMD_UNIT":"session-3.scope","SYSLOG_IDENTIFIER":"sudo","MESSAGE":[104,105,255]}`))
	if l.Source.Name != "sudo" || l.Level != LevelError || l.Message != "hi�" {
		t.Errorf("bytes line = %+v", l)
	}

	if _, ok := parseJournal([]byte(`not json`)); ok {
		t.Error("bad JSON must be ignored")
	}
	if _, ok := parseJournal([]byte(`{"PRIORITY":"6"}`)); ok {
		t.Error("an entry without a message must be ignored")
	}
}

func TestJournalRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	j := &Journal{Logger: quiet, open: func(context.Context) (io.ReadCloser, func() error, error) {
		data := `{"MESSAGE":"one","_SYSTEMD_UNIT":"a.service"}` + "\n" + `{"MESSAGE":"two","_SYSTEMD_UNIT":"b.service"}` + "\n"
		return io.NopCloser(strings.NewReader(data)), func() error { return errors.New("exit 1") }, nil
	}}
	var mu sync.Mutex
	var got []string
	done := make(chan struct{})
	go func() {
		j.Run(ctx, func(l Line) {
			mu.Lock()
			got = append(got, l.Source.Name+":"+l.Message)
			if len(got) == 2 {
				cancel()
			}
			mu.Unlock()
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
	if strings.Join(got, ",") != "a:one,b:two" {
		t.Errorf("got %v", got)
	}
}

// podLog writes a log file of the kubelet.
func podLog(t *testing.T, dir, rel, content string, appendTo bool) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	flag := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if appendTo {
		flag = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	f, err := os.OpenFile(p, flag, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return p
}

type collect struct{ lines []Line }

func (c *collect) emit(l Line) { c.lines = append(c.lines, l) }

func (c *collect) messages() string {
	var s []string
	for _, l := range c.lines {
		s = append(s, l.Message)
	}
	return strings.Join(s, "|")
}

func TestPods(t *testing.T) {
	dir := t.TempDir()
	const web = "apps_photos-7d9f_0f3c2a8e-1b2d-4c5e-8f9a-123456789abc/web/0.log"
	podLog(t, dir, web, "2026-11-10T12:00:00.000000001Z stdout F old line\n", false)

	p := &Pods{Dir: dir, Logger: quiet}
	c := &collect{}

	// The first scan starts at the end of the files that exist.
	p.scan(c.emit, true)
	if len(c.lines) != 0 {
		t.Fatalf("the first scan must skip the old lines: %v", c.messages())
	}

	// New lines, one long line in two parts, and an incomplete line.
	podLog(t, dir, web,
		"2026-11-10T12:00:01.5Z stdout F GET / 200\n"+
			"2026-11-10T12:00:02Z stderr P first part, \n"+
			"2026-11-10T12:00:02Z stderr F second part\n"+
			"2026-11-10T12:00:03Z stderr F level=error msg=\"no thumbnail\"\n"+
			"2026-11-10T12:00:04Z stdout F not compl", true)
	p.scan(c.emit, false)
	if got := c.messages(); got != `GET / 200|first part, second part|level=error msg="no thumbnail"` {
		t.Fatalf("messages = %q", got)
	}
	l := c.lines[0]
	if l.Source.ID != "pod/apps/photos-7d9f/web" || l.Source.Namespace != "apps" || l.Source.Container != "web" ||
		!l.Time.Equal(time.Date(2026, 11, 10, 12, 0, 1, 5e8, time.UTC)) || l.Level != LevelInfo {
		t.Errorf("line = %+v", l)
	}
	if c.lines[2].Level != LevelError {
		t.Errorf("level = %q", c.lines[2].Level)
	}

	// The end of the incomplete line arrives.
	c.lines = nil
	podLog(t, dir, web, "ete\n", true)
	p.scan(c.emit, false)
	if got := c.messages(); got != "not complete" {
		t.Errorf("messages = %q", got)
	}

	// A new container (after the start) is read from its start.
	c.lines = nil
	podLog(t, dir, "db_orders-db-0_11111111-2222-3333-4444-555555555555/postgres/0.log",
		"2026-11-10T12:00:05Z stderr F checkpoint complete\n", false)
	p.scan(c.emit, false)
	if len(c.lines) != 1 || c.lines[0].Source.Name != "orders-db-0" {
		t.Errorf("lines = %+v", c.lines)
	}

	// Rotation: the kubelet makes a new, shorter file with the same name.
	c.lines = nil
	podLog(t, dir, web, "2026-11-10T12:01:00Z stdout F after rotation\n", false)
	p.scan(c.emit, false)
	if got := c.messages(); got != "after rotation" {
		t.Errorf("messages = %q", got)
	}

	// A directory that is not a pod is ignored.
	podLog(t, dir, "not-a-pod/c/0.log", "x\n", false)
	c.lines = nil
	p.scan(c.emit, false)
	if len(c.lines) != 0 {
		t.Errorf("lines = %+v", c.lines)
	}
}

func TestPipeline(t *testing.T) {
	p := NewPipeline(3)
	sub, last := p.Subscribe()
	defer sub.Close()
	if last != 0 {
		t.Fatalf("last = %d", last)
	}
	for i, m := range []string{"a", "b", "c", "d"} {
		lvl := LevelInfo
		if i%2 == 1 {
			lvl = LevelError
		}
		p.Emit(Line{Message: m, Level: lvl, Source: ServiceSource("x")})
	}
	for want := uint64(1); want <= 4; want++ {
		if l := <-sub.C; l.Seq != want {
			t.Errorf("seq = %d, want %d", l.Seq, want)
		}
	}

	// The buffer keeps the last 3 lines, the oldest first.
	join := func(ls []Line) string {
		var s []string
		for _, l := range ls {
			s = append(s, l.Message)
		}
		return strings.Join(s, "")
	}
	if got := join(p.Recent(Filter{}, 10, 0)); got != "bcd" {
		t.Errorf("Recent = %q", got)
	}
	if got := join(p.Recent(Filter{}, 2, 0)); got != "cd" {
		t.Errorf("Recent with limit 2 = %q", got)
	}
	if got := join(p.Recent(Filter{Level: LevelError}, 10, 0)); got != "bd" {
		t.Errorf("Recent errors = %q", got)
	}
	if got := join(p.Recent(Filter{}, 10, 3)); got != "d" {
		t.Errorf("Recent after 3 = %q", got)
	}
	if _, last := p.Subscribe(); last != 4 {
		t.Errorf("last = %d", last)
	}
}

func TestRunSources(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := NewPipeline(10)
	dir := t.TempDir()
	done := make(chan struct{})
	go func() {
		p.RunSources(ctx, &Pods{Dir: dir, Poll: 10 * time.Millisecond, Logger: quiet})
		close(done)
	}()
	time.Sleep(30 * time.Millisecond)
	podLog(t, dir, "a_b_c/d/0.log", "2026-11-10T12:00:00Z stdout F hello\n", false)
	deadline := time.Now().Add(3 * time.Second)
	for len(p.Recent(Filter{}, 10, 0)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no line from the pod source")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}
