package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fathorMB/NidaOS/core/internal/agents"
	"github.com/fathorMB/NidaOS/core/internal/auth"
	"github.com/fathorMB/NidaOS/core/internal/updates"
)

func TestClassify(t *testing.T) {
	cases := map[string]string{
		"GET /api/v1/apps":                       auth.ClassRead,
		"GET /api/v1/logs/stream":                auth.ClassRead,
		"GET /api/v1/tokens":                     auth.ClassAccess,
		"POST /api/v1/tokens":                    auth.ClassAccess,
		"PUT /api/v1/auth/password":              auth.ClassAccess,
		"POST /api/v1/approvals/x/approve":       auth.ClassAccess,
		"POST /api/v1/github/setup":              auth.ClassAccess,
		"GET /api/v1/databases/db/connection":    auth.ClassSecrets,
		"POST /api/v1/apps/blog/deploys":         auth.ClassApps,
		"PATCH /api/v1/apps/blog":                auth.ClassApps,
		"PUT /api/v1/apps/blog/secrets/KEY":      auth.ClassApps,
		"DELETE /api/v1/apps/blog/secrets/KEY":   auth.ClassApps,
		"DELETE /api/v1/apps/blog":               auth.ClassDelete,
		"DELETE /api/v1/apps/blog/repository":    auth.ClassDelete,
		"DELETE /api/v1/databases/db":            auth.ClassDelete,
		"DELETE /api/v1/databases/db/backups/b1": auth.ClassDelete,
		"POST /api/v1/databases/db/backups":      auth.ClassDatabases,
		"DELETE /api/v1/databases/db/links/blog": auth.ClassDatabases,
		"POST /api/v1/updates/restart":           auth.ClassUpdates,
		"POST /api/v1/something/else":            auth.ClassAccess,
	}
	for k, want := range cases {
		m, p, _ := strings.Cut(k, " ")
		if got := Classify(m, p); got != want {
			t.Errorf("%s: %s, want %s", k, got, want)
		}
	}
}

func agentRouter(t *testing.T) (*Router, *auth.Store, string, string, *agents.Store) {
	t.Helper()
	dir := t.TempDir()
	store, err := auth.Open(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	ag, err := agents.Open(filepath.Join(dir, "agents.json"))
	if err != nil {
		t.Fatal(err)
	}
	out := `{"status":{"booted":{"image":{"image":{"image":"x"},"version":"45.1"}}}}`
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		if args[0] == "status" {
			return []byte(out), nil
		}
		return nil, nil
	}
	r := NewRouter(Deps{State: &fakeState{}, Auth: store, Agents: ag,
		Updates: &UpdateHosting{Service: updates.New(context.Background(), run, nil)}})
	_, agentSecret, err := store.CreateAgentToken("laptop", "Claude Code", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, adminSecret, err := store.CreateToken("owner-script", auth.LevelAdmin)
	if err != nil {
		t.Fatal(err)
	}
	return r, store, agentSecret, adminSecret, ag
}

func TestAgentRules(t *testing.T) {
	r, store, agent, admin, ag := agentRouter(t)

	// Read: allowed.
	if rec := do(r, call{method: http.MethodGet, path: "/api/v1/state", header: bearer(agent)}); rec.Code != 200 {
		t.Fatalf("read: %d %s", rec.Code, rec.Body)
	}
	// Access: always refused, also the approvals and the tokens.
	for _, c := range []call{
		{method: http.MethodPost, path: "/api/v1/tokens", body: `{"name":"x","level":"admin"}`},
		{method: http.MethodGet, path: "/api/v1/tokens"},
		{method: http.MethodPost, path: "/api/v1/approvals/abc/approve"},
	} {
		c.header = bearer(agent)
		if rec := do(r, c); rec.Code != http.StatusForbidden || errCode(rec) != "agent_rule" {
			t.Errorf("%s %s: %d %s", c.method, c.path, rec.Code, rec.Body)
		}
	}

	// Updates: "ask" by default. The request waits for the owner.
	rec := do(r, call{method: http.MethodPost, path: "/api/v1/updates/check", header: bearer(agent)})
	if rec.Code != http.StatusAccepted || errCode(rec) != "approval_required" {
		t.Fatalf("ask: %d %s", rec.Code, rec.Body)
	}
	var ab ApprovalBody
	_ = json.Unmarshal(rec.Body.Bytes(), &ab)
	id := ab.Approval.ID
	if id == "" || ab.Approval.State != agents.StatePending || !strings.Contains(ab.Approval.Summary, "System update") {
		t.Fatalf("approval = %+v", ab.Approval)
	}
	// The agent reads its request; the owner approves it.
	rec = do(r, call{method: http.MethodGet, path: "/api/v1/approvals/" + id, header: bearer(agent)})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"state":"pending"`) {
		t.Fatalf("agent get: %d %s", rec.Code, rec.Body)
	}
	rec = do(r, call{method: http.MethodPost, path: "/api/v1/approvals/" + id + "/approve", header: bearer(admin)})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"state":"done"`) || !strings.Contains(rec.Body.String(), `"status":202`) {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body)
	}
	if rec := do(r, call{method: http.MethodPost, path: "/api/v1/approvals/" + id + "/approve", header: bearer(admin)}); errCode(rec) != "approval_not_pending" {
		t.Errorf("second approval: %d %s", rec.Code, rec.Body)
	}

	// A denied request.
	rec = do(r, call{method: http.MethodPost, path: "/api/v1/updates/download", header: bearer(agent)})
	_ = json.Unmarshal(rec.Body.Bytes(), &ab)
	rec = do(r, call{method: http.MethodPost, path: "/api/v1/approvals/" + ab.Approval.ID + "/deny", header: bearer(admin), body: `{"reason":"not now"}`})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"state":"denied"`) {
		t.Fatalf("deny: %d %s", rec.Code, rec.Body)
	}

	// The owner changes the rules: the updates become "allow".
	var tokenID string
	for _, tk := range store.Tokens() {
		if tk.Level == auth.LevelAgent {
			tokenID = tk.ID
		}
	}
	rec = do(r, call{method: http.MethodPatch, path: "/api/v1/tokens/" + tokenID, header: bearer(admin), body: `{"rules":{"updates":"allow"}}`})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"updates":"allow"`) {
		t.Fatalf("rules: %d %s", rec.Code, rec.Body)
	}
	if rec := do(r, call{method: http.MethodPatch, path: "/api/v1/tokens/" + tokenID, header: bearer(admin), body: `{"rules":{"access":"allow"}}`}); errCode(rec) != "bad_rules" {
		t.Errorf("access rule: %d %s", rec.Code, rec.Body)
	}
	time := waitIdle(r)
	_ = time
	rec = do(r, call{method: http.MethodPost, path: "/api/v1/updates/check", header: bearer(agent)})
	if rec.Code != http.StatusAccepted || errCode(rec) == "approval_required" {
		t.Fatalf("allowed: %d %s", rec.Code, rec.Body)
	}

	// The record has each change, the newest first.
	outcomes := []string{}
	for _, a := range ag.Actions("", 0) {
		outcomes = append(outcomes, a.Outcome)
	}
	if got := strings.Join(outcomes, ","); got != "done,denied,asked,done,asked,refused,refused,refused" {
		t.Errorf("record = %s", got)
	}
	// An agent sees only its own requests; the body is never in the API.
	rec = do(r, call{method: http.MethodGet, path: "/api/v1/approvals", header: bearer(admin)})
	if strings.Contains(rec.Body.String(), `"body"`) {
		t.Errorf("the API gives the body: %s", rec.Body)
	}
}

// waitIdle waits for the end of the update operation of the fake bootc.
func waitIdle(r *Router) int {
	for i := 0; i < 200; i++ {
		st, _ := r.updates.Service.Status(context.Background())
		if st.Busy == "" {
			return i
		}
		sleepShort()
	}
	return -1
}

func TestMCPOverHTTP(t *testing.T) {
	r, _, agent, _, ag := agentRouter(t)
	post := func(msg string) string {
		rec := do(r, call{method: http.MethodPost, path: "/api/v1/mcp", header: bearer(agent), body: msg})
		if rec.Code != 200 && rec.Code != 202 {
			t.Fatalf("mcp: %d %s", rec.Code, rec.Body)
		}
		return rec.Body.String()
	}
	if out := post(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`); !strings.Contains(out, `"instructions"`) {
		t.Errorf("initialize: %s", out)
	}
	if out := post(`{"jsonrpc":"2.0","method":"notifications/initialized"}`); out != "" {
		t.Errorf("notification: %q", out)
	}
	out := post(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"update_check","arguments":{}}}`)
	if !strings.Contains(out, "approval request") {
		t.Fatalf("update_check: %s", out)
	}
	if acts := ag.Actions("", 0); len(acts) != 1 || acts[0].Outcome != "asked" {
		t.Errorf("record = %+v", acts)
	}
	// Without a token, no access.
	if rec := do(r, call{method: http.MethodPost, path: "/api/v1/mcp", body: `{}`}); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d", rec.Code)
	}
}

func TestSettingsBackupAccess(t *testing.T) {
	r, _, agent, admin, _ := agentRouter(t)
	r.settingsDir = t.TempDir()
	r.addSettingsRoutes()
	if rec := do(r, call{method: http.MethodGet, path: "/api/v1/settings/backup", header: bearer(agent)}); rec.Code != http.StatusForbidden {
		t.Errorf("agent: %d", rec.Code)
	}
	rec := do(r, call{method: http.MethodGet, path: "/api/v1/settings/backup", header: bearer(admin)})
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/gzip" {
		t.Errorf("admin: %d %s", rec.Code, rec.Header())
	}
}
