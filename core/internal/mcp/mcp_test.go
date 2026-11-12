package mcp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fathorMB/NidaOS/core/internal/agents"
	"github.com/fathorMB/NidaOS/core/internal/auth"
	"github.com/fathorMB/NidaOS/core/internal/client"
	"github.com/fathorMB/NidaOS/core/internal/httpserver"
	"github.com/fathorMB/NidaOS/core/internal/mcp"
	"github.com/fathorMB/NidaOS/core/internal/updates"
)

// session runs the server and sends messages, one at a time.
type session struct {
	t   *testing.T
	in  *io.PipeWriter
	out *bufio.Scanner
	id  int
}

func (s *session) call(method string, params any) map[string]any {
	s.t.Helper()
	s.id++
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": s.id, "method": method, "params": params})
	if _, err := s.in.Write(append(b, '\n')); err != nil {
		s.t.Fatal(err)
	}
	if !s.out.Scan() {
		s.t.Fatalf("no answer to %s", method)
	}
	var resp map[string]any
	if err := json.Unmarshal(s.out.Bytes(), &resp); err != nil {
		s.t.Fatal(err)
	}
	if int(resp["id"].(float64)) != s.id {
		s.t.Fatalf("answer id %v, want %d", resp["id"], s.id)
	}
	return resp
}

func (s *session) tool(name string, args map[string]any) (string, bool) {
	s.t.Helper()
	r := s.call("tools/call", map[string]any{"name": name, "arguments": args})
	res, _ := r["result"].(map[string]any)
	if res == nil {
		s.t.Fatalf("%s: %v", name, r)
	}
	content := res["content"].([]any)[0].(map[string]any)
	return content["text"].(string), res["isError"].(bool)
}

func TestMCPWithAnAgentToken(t *testing.T) {
	dir := t.TempDir()
	store, _ := auth.Open(filepath.Join(dir, "auth.json"))
	ag, _ := agents.Open(filepath.Join(dir, "agents.json"))
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "status" {
			return []byte(`{"status":{"booted":{"image":{"image":{"image":"x"},"version":"45.1"}}}}`), nil
		}
		return nil, nil
	}
	router := httpserver.NewRouter(httpserver.Deps{Auth: store, Agents: ag, Updates: &httpserver.UpdateHosting{Service: updates.New(context.Background(), run, nil)}})
	srv := httptest.NewServer(router)
	defer srv.Close()
	_, secret, err := store.CreateAgentToken("laptop", "Claude Code", nil)
	if err != nil {
		t.Fatal(err)
	}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	server := &mcp.Server{API: client.NewHTTP(srv.URL, secret), Version: "test", Instructions: mcp.Instructions}
	go func() {
		_ = server.Serve(context.Background(), inR, outW)
		outW.Close()
	}()
	s := &session{t: t, in: inW, out: bufio.NewScanner(outR)}
	s.out.Buffer(make([]byte, 1<<20), 4<<20)

	init := s.call("initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"}})
	res := init["result"].(map[string]any)
	if res["protocolVersion"] != "2025-03-26" || !strings.Contains(res["instructions"].(string), "approval_get") {
		t.Fatalf("initialize: %v", res)
	}
	// A notification gets no answer: the next answer is for tools/list.
	_, _ = inW.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"))
	list := s.call("tools/list", nil)["result"].(map[string]any)["tools"].([]any)
	names := []string{}
	for _, x := range list {
		names = append(names, x.(map[string]any)["name"].(string))
	}
	if !strings.Contains(strings.Join(names, ","), "app_deploy") || !strings.Contains(strings.Join(names, ","), "approval_get") {
		t.Errorf("tools = %v", names)
	}

	// A read: allowed.
	text, isErr := s.tool("updates_status", nil)
	if isErr || !strings.Contains(text, "45.1") {
		t.Errorf("updates_status: %v %s", isErr, text)
	}
	// An update: "ask". The tool tells the id of the approval request.
	text, isErr = s.tool("update_check", nil)
	if isErr || !strings.Contains(text, "approve") {
		t.Fatalf("update_check: %v %s", isErr, text)
	}
	pending := ag.List("", agents.StatePending)
	if len(pending) != 1 || !strings.Contains(text, pending[0].ID) {
		t.Fatalf("pending = %+v", pending)
	}
	text, _ = s.tool("approval_get", map[string]any{"id": pending[0].ID})
	if !strings.Contains(text, `"state": "pending"`) {
		t.Errorf("approval_get: %s", text)
	}
	// A wrong argument and an unknown tool.
	if text, isErr := s.tool("app_get", nil); !isErr || !strings.Contains(text, "name") {
		t.Errorf("missing argument: %v %s", isErr, text)
	}
	if r := s.call("tools/call", map[string]any{"name": "nothing"}); r["error"] == nil {
		t.Errorf("unknown tool: %v", r)
	}
	if r := s.call("ping", nil); r["result"] == nil {
		t.Errorf("ping: %v", r)
	}
	inW.Close()
}
