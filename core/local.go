package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/auth"
	"github.com/fathorMB/NidaOS/core/internal/config"
)

// listenSocket opens the local socket (decision D18). Only the owner of
// the process (root) and the group can open it: the mode is 0660. When
// the group does not exist, the mode is 0600.
func listenSocket(path, group string, logger *slog.Logger) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	// A socket file of an old process stays after a crash.
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSocket != 0 {
		if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
			c.Close()
			return nil, fmt.Errorf("an other process uses the socket %s", path)
		}
		os.Remove(path)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	mode := os.FileMode(0o600)
	if group != "" {
		if g, err := user.LookupGroup(group); err == nil {
			gid, _ := strconv.Atoi(g.Gid)
			if err := os.Chown(path, -1, gid); err == nil {
				mode = 0o660
			} else {
				logger.Warn("the socket cannot get its group: only root can open it", "group", group, "err", err)
			}
		} else {
			logger.Warn("the group of the socket does not exist: only root can open it", "group", group)
		}
	}
	if err := os.Chmod(path, mode); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

// localIdentity is the identity of each request through the socket.
var localIdentity = auth.Identity{Kind: auth.KindLocal, Name: "local", Level: auth.LevelAdmin}

// runSetPassword sets the owner password. It reads the password from the
// first line of stdin, thus the installer can give it through a pipe:
//
//	printf '%s\n' "$password" | nida-core set-password
//
// When the service runs, the command sends the password through the
// local socket: the service keeps the auth data in memory. When the
// service does not run, the command writes the auth file.
func runSetPassword(stdin io.Reader, stdout, stderr io.Writer) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(stderr, "nida-core:", err)
		return 1
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintln(stderr, "nida-core: cannot read the password:", err)
		return 1
	}
	password := strings.TrimRight(line, "\r\n")

	if cfg.Socket != "" {
		done, err := setPasswordThroughSocket(cfg.Socket, password)
		if done {
			if err != nil {
				fmt.Fprintln(stderr, "nida-core:", err)
				return 1
			}
			fmt.Fprintln(stdout, "The owner password is set. All the sessions are closed.")
			return 0
		}
	}

	store, err := auth.Open(cfg.AuthFile)
	if err != nil {
		fmt.Fprintln(stderr, "nida-core:", err)
		return 1
	}
	if err := store.SetPassword(password); err != nil {
		fmt.Fprintln(stderr, "nida-core:", err)
		return 1
	}
	fmt.Fprintln(stdout, "The owner password is set in", cfg.AuthFile)
	return 0
}

// setPasswordThroughSocket sends the password to the running service.
// done is false when no service answers on the socket.
func setPasswordThroughSocket(socket, password string) (done bool, err error) {
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		}},
	}
	body, _ := json.Marshal(map[string]string{"new": password})
	req, _ := http.NewRequest(http.MethodPut, "http://local/api/v1/auth/password", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return false, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return true, nil
	}
	var e struct {
		Message string `json:"message"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&e)
	if e.Message == "" {
		e.Message = resp.Status
	}
	return true, errors.New(e.Message)
}
