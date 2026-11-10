package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/auth"
	"github.com/fathorMB/NidaOS/core/internal/httpserver"
	"github.com/fathorMB/NidaOS/core/internal/logs"
	"github.com/fathorMB/NidaOS/core/internal/metrics"
	"github.com/fathorMB/NidaOS/core/internal/stream"
	"github.com/fathorMB/NidaOS/core/internal/system"
)

// syncBuffer is a buffer that two goroutines can use.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// service is a real router of nida-core with the fixture of a host.
type service struct {
	url      string
	socket   string
	token    string
	metrics  *stream.Hub[metrics.Snapshot]
	pipeline *logs.Pipeline
}

var t0 = time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)

func startService(t *testing.T) *service {
	t.Helper()
	dir := t.TempDir()
	authStore, err := auth.Open(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, token, _ := authStore.CreateToken("test", auth.LevelAdmin)

	hub := stream.NewHub[metrics.Snapshot](4)
	temp := 47.0
	hub.Publish(metrics.Snapshot{Seq: 1, Time: t0, Host: metrics.Host{
		CPU:     metrics.HostCPU{UsagePercent: 12.5, Threads: []float64{10, 15}, TemperatureC: &temp},
		Memory:  metrics.HostMemory{TotalBytes: 32 << 30, UsedBytes: 8 << 30, UsedPercent: 25},
		Load:    [3]float64{0.5, 0.4, 0.3},
		Disks:   []metrics.DiskIO{{Name: "sda", ReadBytesPerSecond: 2e6, WriteBytesPerSecond: 5e5}},
		Network: []metrics.NetIO{{Name: "eno1", ReceiveBytesPerSecond: 1500, TransmitBytesPerSecond: 300}},
	}, Workloads: []metrics.Workload{
		{Kind: "service", Name: "k3s", CPUCores: 0.2, MemoryBytes: 600 << 20},
		{Kind: "pod", Namespace: "apps", Name: "photos-7d9f", CPUCores: 0.75, MemoryBytes: 50 << 20},
	}})

	store, err := logs.OpenStore(filepath.Join(dir, "logs"), 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	p := logs.NewPipeline(100)
	p.Sink = store.Append
	now := time.Now().UTC()
	p.Emit(logs.Line{Time: now.Add(-time.Minute), Source: logs.ServiceSource("k3s"), Level: logs.LevelInfo, Message: "node ready"})
	p.Emit(logs.Line{Time: now, Source: logs.PodSource("apps", "photos-7d9f", "web"), Level: logs.LevelError, Message: "no thumbnail\nfor IMG_4471"})

	router := httpserver.NewRouter(httpserver.Deps{
		State:    system.NewCollector("../../internal/system/testdata/host", ""),
		Metrics:  hub,
		Logs:     p,
		LogStore: store,
		Auth:     authStore,
	})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	// The local socket, as nida-core opens it.
	sock := filepath.Join(dir, "core.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ss := &http.Server{Handler: httpserver.WithIdentity(router, auth.Identity{Kind: auth.KindLocal, Name: "local", Level: auth.LevelAdmin})}
	go ss.Serve(ln)
	t.Cleanup(func() { ss.Close() })

	return &service{url: srv.URL, socket: sock, token: token, metrics: hub, pipeline: p}
}

// nida runs the command with the environment env.
func nida(ctx context.Context, env map[string]string, args ...string) (code int, out, errOut string) {
	var o, e syncBuffer
	code = run(ctx, args, func(k string) string { return env[k] }, &o, &e)
	return code, o.String(), e.String()
}

func (s *service) http() map[string]string {
	return map[string]string{"NIDA_SERVER": s.url, "NIDA_TOKEN": s.token}
}

func (s *service) local() map[string]string {
	return map[string]string{"NIDA_SOCKET": s.socket}
}

func TestStatus(t *testing.T) {
	s := startService(t)
	for name, env := range map[string]map[string]string{"http": s.http(), "socket": s.local()} {
		code, out, errOut := nida(context.Background(), env, "status")
		if code != 0 {
			t.Fatalf("%s: code %d: %s", name, code, errOut)
		}
		for _, want := range []string{
			"nida  Fedora Linux 45 (NidaOS) · image v1", "12.5%", "47 °C", "8 threads",
			"sda", "SSD", "os", "Samsung SSD 850", "read 2.0 MB/s, write 500.0 kB/s",
			"performance", "ready", "sdb1", "standard", "not mounted",
			"eno1", "in 1.5 kB/s",
			"apps/photos-7d9f", "0.75 cores",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: the status has no %q:\n%s", name, want, out)
			}
		}
		// The pod uses more processor: it comes first.
		if strings.Index(out, "photos-7d9f") > strings.Index(out, "k3s") {
			t.Errorf("%s: the workloads are not in the order of the processor usage:\n%s", name, out)
		}
	}

	code, out, _ := nida(context.Background(), s.local(), "--json", "status")
	var v struct {
		State   system.State      `json:"state"`
		Metrics *metrics.Snapshot `json:"metrics"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &v) != nil || v.State.System.Hostname != "nida" || v.Metrics == nil {
		t.Errorf("json: %d %s", code, out)
	}
}

func TestMetrics(t *testing.T) {
	s := startService(t)
	code, out, _ := nida(context.Background(), s.local(), "metrics")
	if code != 0 || !strings.Contains(out, "12.5%") || !strings.Contains(out, "2.0 MB/s / 500.0 kB/s") {
		t.Errorf("metrics: %d\n%s", code, out)
	}

	// --watch --json: one JSON line for each snapshot.
	ctx, cancel := context.WithCancel(context.Background())
	var o, e syncBuffer
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, []string{"--json", "metrics", "--watch"}, func(k string) string { return s.local()[k] }, &o, &e)
	}()
	for s.metrics.Subscribers() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	s.metrics.Publish(metrics.Snapshot{Seq: 2, Time: t0.Add(2 * time.Second)})
	deadline := time.Now().Add(3 * time.Second)
	for strings.Count(o.String(), "\n") < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if code := <-done; code != 0 {
		t.Errorf("watch: code %d: %s", code, e.String())
	}
	lines := strings.Split(strings.TrimSpace(o.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"seq":1`) || !strings.Contains(lines[1], `"seq":2`) {
		t.Errorf("watch output:\n%s", o.String())
	}
}

func TestLogs(t *testing.T) {
	s := startService(t)
	code, out, errOut := nida(context.Background(), s.http(), "logs")
	if code != 0 {
		t.Fatalf("code %d: %s", code, errOut)
	}
	if !strings.Contains(out, "INFO   service/k3s  node ready") ||
		!strings.Contains(out, "ERROR  pod/apps/photos-7d9f/web  no thumbnail ⏎ for IMG_4471") ||
		strings.Index(out, "node ready") > strings.Index(out, "no thumbnail") {
		t.Errorf("logs:\n%s", out)
	}

	_, out, _ = nida(context.Background(), s.http(), "logs", "--level", "error")
	if strings.Contains(out, "node ready") || !strings.Contains(out, "no thumbnail") {
		t.Errorf("logs --level error:\n%s", out)
	}

	// --since searches the store; the oldest line comes first.
	_, out, errOut = nida(context.Background(), s.http(), "logs", "--since", "1h", "--source", "service")
	if strings.TrimSpace(out) == "" || !strings.Contains(out, "node ready") || strings.Contains(out, "thumbnail") {
		t.Errorf("logs --since: %s %s", out, errOut)
	}
	code, out, _ = nida(context.Background(), s.http(), "logs", "--since", "1h", "-n", "1")
	if code != 0 || !strings.Contains(out, "thumbnail") || strings.Contains(out, "node ready") {
		t.Errorf("logs --since -n 1:\n%s", out)
	}

	// -f: the last lines, then the new lines.
	ctx, cancel := context.WithCancel(context.Background())
	var o, e syncBuffer
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, []string{"logs", "-f", "-n", "1"}, func(k string) string { return s.local()[k] }, &o, &e)
	}()
	for s.pipeline.Hub.Subscribers() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	s.pipeline.Emit(logs.Line{Time: time.Now(), Source: logs.ServiceSource("sshd"), Level: logs.LevelWarning, Message: "slow"})
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(o.String(), "slow") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if got := o.String(); !strings.Contains(got, "thumbnail") || strings.Contains(got, "node ready") || !strings.Contains(got, "WARN   service/sshd  slow") {
		t.Errorf("logs -f:\n%s", got)
	}

	if code, _, errOut := nida(context.Background(), s.http(), "logs", "-f", "--since", "1h"); code != 2 || !strings.Contains(errOut, "-f does not accept") {
		t.Errorf("-f --since: %d %s", code, errOut)
	}
}

func TestTokens(t *testing.T) {
	s := startService(t)
	code, out, errOut := nida(context.Background(), s.local(), "token", "create", "laptop", "--level", "read")
	if code != 0 || !strings.Contains(out, "nida_") {
		t.Fatalf("create: %d %s %s", code, out, errOut)
	}
	secret := ""
	for f := range strings.FieldsSeq(out) {
		if strings.HasPrefix(f, "nida_") {
			secret = f
		}
	}

	// The new token works, with the read level only.
	env := map[string]string{"NIDA_SERVER": s.url, "NIDA_TOKEN": secret}
	if code, _, errOut := nida(context.Background(), env, "status"); code != 0 {
		t.Errorf("status with the new token: %d %s", code, errOut)
	}
	if code, _, errOut := nida(context.Background(), env, "token", "list"); code != 1 || !strings.Contains(errOut, "level") {
		t.Errorf("list with a read token: %d %s", code, errOut)
	}

	code, out, _ = nida(context.Background(), s.local(), "token", "list")
	if code != 0 || !strings.Contains(out, "laptop") || !strings.Contains(out, "read") || strings.Contains(out, secret) {
		t.Errorf("list: %d\n%s", code, out)
	}
	var list struct {
		Tokens []auth.Token `json:"tokens"`
	}
	_, out, _ = nida(context.Background(), s.local(), "--json", "token", "list")
	json.Unmarshal([]byte(out), &list)
	var id string
	for _, tk := range list.Tokens {
		if tk.Name == "laptop" {
			id = tk.ID
		}
	}
	if code, _, errOut := nida(context.Background(), s.local(), "token", "delete", id); code != 0 {
		t.Errorf("delete: %d %s", code, errOut)
	}
	if code, _, _ := nida(context.Background(), env, "status"); code != 1 {
		t.Errorf("status with the deleted token: %d", code)
	}
}

func TestErrors(t *testing.T) {
	s := startService(t)
	if code, _, errOut := nida(context.Background(), map[string]string{"NIDA_SERVER": s.url}, "status"); code != 1 || !strings.Contains(errOut, "Sign in") {
		t.Errorf("no token: %d %s", code, errOut)
	}
	if code, _, errOut := nida(context.Background(), map[string]string{"NIDA_SOCKET": filepath.Join(t.TempDir(), "none.sock")}, "status"); code != 1 || !strings.Contains(errOut, "local socket") {
		t.Errorf("no socket: %d %s", code, errOut)
	}
	if code, _, _ := nida(context.Background(), nil, "jump"); code != 2 {
		t.Errorf("unknown command: %d", code)
	}
	if code, out, _ := nida(context.Background(), nil); code != 2 || !strings.Contains(out, "Usage") {
		t.Errorf("no command: %d", code)
	}
	if code, _, _ := nida(context.Background(), nil, "--help"); code != 0 {
		t.Errorf("help: %d", code)
	}
	if code, _, _ := nida(context.Background(), s.local(), "status", "extra"); code != 2 {
		t.Errorf("extra argument: %d", code)
	}
	code, out, _ := nida(context.Background(), s.local(), "version")
	if code != 0 || !strings.Contains(out, "nida-core  0.1.0-dev") {
		t.Errorf("version: %d %s", code, out)
	}
}

// TestFollowReconnects uses a stream that stops after each event. The
// command tells the operator and connects again with Last-Event-ID.
func TestFollowReconnects(t *testing.T) {
	var mu sync.Mutex
	var lastIDs []string
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		lastIDs = append(lastIDs, r.Header.Get("Last-Event-ID"))
		n++
		id := n
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		l := logs.Line{Seq: uint64(id), Time: t0, Source: logs.ServiceSource("x"), Level: "info", Message: fmt.Sprintf("line %d", id)}
		b, _ := json.Marshal(l)
		fmt.Fprintf(w, ": heartbeat\n\nid: %d\nevent: log\ndata: %s\n\n", id, b)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	var o, e syncBuffer
	code := 0
	done := make(chan struct{})
	go func() {
		code = run(ctx, []string{"--server", srv.URL, "logs", "-f"}, func(string) string { return "" }, &o, &e)
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for strings.Count(o.String(), "line ") < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(lastIDs) < 3 || lastIDs[0] != "" || lastIDs[1] != "1" || lastIDs[2] != "2" {
		t.Errorf("Last-Event-ID = %q", lastIDs)
	}
	if !strings.Contains(e.String(), "the stream stopped") || !strings.Contains(e.String(), "last data at") {
		t.Errorf("stderr = %s", e.String())
	}
	if code != 0 {
		t.Errorf("code = %d", code)
	}
}
