package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/config"
	"github.com/fathorMB/NidaOS/core/internal/version"
)

func TestRunVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"version"}, &out, &errOut); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, errOut.String())
	}
	if strings.TrimSpace(out.String()) != version.Version {
		t.Errorf("output = %q", out.String())
	}
}

func TestRunBadCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"jump"}, &out, &errOut); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if code := run([]string{"serve", "now"}, &out, &errOut); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

// TestServeAndStop starts the service on a free port and on a local
// socket. It examines the access rules, then stops the context and makes
// sure that serve returns without an error, also with an open stream.
func TestServeAndStop(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cfg := config.Config{
		ShutdownTimeout: 2 * time.Second,
		Root:            "internal/system/testdata/host",
		PodLogDir:       "/var/log/pods",
		LogDir:          filepath.Join(dir, "logs"),
		AuthFile:        filepath.Join(dir, "auth.json"),
		Socket:          filepath.Join(dir, "core.sock"),
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	done := make(chan error, 1)
	go func() { done <- serve(ctx, ln, cfg, logger) }()
	// Stop the service before the removal of the temporary directory.
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			cancel()
			<-done
		}
	})

	tcp := "http://" + ln.Addr().String()
	status := func(c *http.Client, url, token string) int {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", url, err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if code := status(http.DefaultClient, tcp+"/healthz", ""); code != http.StatusOK {
		t.Fatalf("/healthz: status = %d", code)
	}
	// Through TCP, the API needs a sign-in.
	if code := status(http.DefaultClient, tcp+"/api/v1/state", ""); code != http.StatusUnauthorized {
		t.Fatalf("/api/v1/state without a token: status = %d", code)
	}

	// Through the local socket, the API is open (D18).
	var sock *http.Client
	for i := 0; ; i++ {
		sock = socketClient(cfg.Socket)
		if _, err := sock.Get("http://local/healthz"); err == nil {
			break
		} else if i > 50 {
			t.Fatalf("the socket does not answer: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, path := range []string{"/api/v1/state", "/api/v1/logs/search?since=1h"} {
		if code := status(sock, "http://local"+path, ""); code != http.StatusOK {
			t.Fatalf("socket %s: status = %d", path, code)
		}
	}

	// A token made through the socket opens the API through TCP.
	resp, err := sock.Post("http://local/api/v1/tokens", "application/json", strings.NewReader(`{"name":"test","level":"read"}`))
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		Secret string `json:"secret"`
	}
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if code := status(http.DefaultClient, tcp+"/api/v1/state", created.Secret); code != http.StatusOK {
		t.Fatalf("/api/v1/state with the token: status = %d", code)
	}

	// set-password goes through the socket of the running service. Then
	// the owner can sign in through TCP.
	t.Setenv("NIDA_CORE_SOCKET", cfg.Socket)
	t.Setenv("NIDA_CORE_AUTH_FILE", filepath.Join(dir, "not-used.json"))
	var out, errOut bytes.Buffer
	if code := runSetPassword(strings.NewReader("the owner password\n"), &out, &errOut); code != 0 {
		t.Fatalf("set-password: code %d: %s", code, errOut.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "not-used.json")); !os.IsNotExist(err) {
		t.Error("set-password wrote a file: it must use the socket of the running service")
	}
	resp, err = http.Post(tcp+"/api/v1/auth/login", "application/json", strings.NewReader(`{"password":"the owner password"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: status = %d", resp.StatusCode)
	}

	// Open a live stream. The stop must not wait for it.
	req, _ := http.NewRequest(http.MethodGet, tcp+"/api/v1/metrics/stream", nil)
	req.Header.Set("Authorization", "Bearer "+created.Secret)
	stream, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/metrics/stream: %v", err)
	}
	defer stream.Body.Close()
	if stream.StatusCode != http.StatusOK {
		t.Fatalf("stream: status = %d", stream.StatusCode)
	}

	start := time.Now()
	cancel()
	select {
	case err := <-done:
		stopped = true
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
		if d := time.Since(start); d > time.Second {
			t.Errorf("the stop took %v: the open stream held it", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop")
	}
	if _, err := os.Stat(cfg.Socket); !os.IsNotExist(err) {
		t.Errorf("the socket file stays after the stop: %v", err)
	}
}

// TestSetPasswordWithoutService writes the auth file directly.
func TestSetPasswordWithoutService(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NIDA_CORE_AUTH_FILE", filepath.Join(dir, "auth.json"))
	t.Setenv("NIDA_CORE_SOCKET", filepath.Join(dir, "none.sock"))
	var out, errOut bytes.Buffer
	if code := runSetPassword(strings.NewReader("short\n"), &out, &errOut); code != 1 {
		t.Errorf("short password: code %d", code)
	}
	if code := runSetPassword(strings.NewReader("a long enough password\n"), &out, &errOut); code != 0 {
		t.Fatalf("code %d: %s", code, errOut.String())
	}
	b, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil || !strings.Contains(string(b), "pbkdf2-sha256") {
		t.Errorf("auth file = %s, err = %v", b, err)
	}
}

func socketClient(path string) *http.Client {
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
	}}
}
