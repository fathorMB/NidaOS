package httpserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"

	"github.com/fathorMB/NidaOS/core/internal/agents"
	"github.com/fathorMB/NidaOS/core/internal/auth"
)

// The access of the agents (milestone M-09). An agent token has rules for
// each class of operation. "allow" runs the request, "ask" keeps it as an
// approval request for the owner, "deny" refuses it.

// approvedKey marks a request that nida-core runs after the approval of
// the owner.
type approvedKey struct{}

// Classify gives the class of an operation for the rules of an agent.
func Classify(method, path string) string {
	p := strings.TrimSuffix(path, "/")
	read := method == http.MethodGet || method == http.MethodHead
	switch {
	case p == "/api/v1/tokens" || strings.HasPrefix(p, "/api/v1/tokens/") || p == "/api/v1/auth/password":
		return auth.ClassAccess
	case strings.HasPrefix(p, "/api/v1/approvals/") && !read:
		return auth.ClassAccess
	case strings.HasPrefix(p, "/api/v1/github") && !read:
		return auth.ClassAccess
	case strings.HasPrefix(p, "/api/v1/databases/") && strings.HasSuffix(p, "/connection"):
		return auth.ClassSecrets
	case read:
		return auth.ClassRead
	case strings.HasPrefix(p, "/api/v1/updates"):
		return auth.ClassUpdates
	case method == http.MethodDelete && (isItem(p, "/api/v1/apps/") || isItem(p, "/api/v1/databases/") ||
		strings.Contains(p, "/backups/") || strings.HasSuffix(p, "/repository")):
		return auth.ClassDelete
	case p == "/api/v1/apps" || strings.HasPrefix(p, "/api/v1/apps/"):
		return auth.ClassApps
	case p == "/api/v1/databases" || strings.HasPrefix(p, "/api/v1/databases/"):
		return auth.ClassDatabases
	}
	return auth.ClassAccess
}

// isItem tells if p is prefix + one name, for example /api/v1/apps/blog.
func isItem(p, prefix string) bool {
	rest, ok := strings.CutPrefix(p, prefix)
	return ok && rest != "" && !strings.Contains(rest, "/")
}

// Summary tells an operation in words, for the console.
func Summary(method, path string) string {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "/api/v1/"), "/"), "/")
	name := func(i int) string {
		if i < len(parts) {
			return parts[i]
		}
		return ""
	}
	switch {
	case parts[0] == "apps" && len(parts) == 2 && method == http.MethodDelete:
		return "Remove the app " + name(1) + " with its folders"
	case parts[0] == "apps" && len(parts) == 2 && method == http.MethodPatch:
		return "Change the settings of the app " + name(1)
	case parts[0] == "apps" && name(2) == "deploys":
		return "Deploy a new version of the app " + name(1)
	case parts[0] == "apps" && name(2) == "rollback":
		return "Go back to an old version of the app " + name(1)
	case parts[0] == "apps" && name(2) == "restart":
		return "Restart the app " + name(1)
	case parts[0] == "apps" && name(2) == "secrets":
		return "Change the secret " + name(3) + " of the app " + name(1)
	case parts[0] == "apps" && name(2) == "repository" && method == http.MethodDelete:
		return "Disconnect the repository of the app " + name(1)
	case parts[0] == "databases" && len(parts) == 2 && method == http.MethodDelete:
		return "Remove the database " + name(1) + " and its data"
	case parts[0] == "databases" && name(2) == "connection":
		return "Read the password of the database " + name(1)
	case parts[0] == "databases" && name(2) == "backups" && method == http.MethodDelete:
		return "Delete a backup of the database " + name(1)
	case parts[0] == "databases" && name(2) == "update":
		return "Update the database " + name(1)
	case parts[0] == "updates":
		return "System update: " + name(1)
	}
	return method + " " + path
}

func (r *Router) addAgentRoutes() {
	if r.agents == nil {
		return
	}
	r.handle(http.MethodGet, "/api/v1/approvals", r.approvalsList)
	r.handle(http.MethodGet, "/api/v1/approvals/{id}", r.approvalGet)
	r.handle(http.MethodPost, "/api/v1/approvals/{id}/approve", r.approvalDecide(true))
	r.handle(http.MethodPost, "/api/v1/approvals/{id}/deny", r.approvalDecide(false))
	r.handle(http.MethodGet, "/api/v1/agents/actions", r.agentActions)
}

// agentCheck applies the rules of an agent identity. It gives false when
// it wrote the response.
func (r *Router) agentCheck(w http.ResponseWriter, req *http.Request, id auth.Identity) bool {
	class := Classify(req.Method, req.URL.Path)
	switch auth.RuleOf(id, class) {
	case auth.RuleAllow:
		return true
	case auth.RuleAsk:
		if v, _ := req.Context().Value(approvedKey{}).(bool); v {
			return true
		}
		if r.agents == nil {
			writeError(w, http.StatusForbidden, "agent_rule", "The rules of this agent token do not give this operation.")
			return false
		}
		r.ask(w, req, id, class)
		return false
	}
	if r.agents != nil {
		r.agents.Record(agents.Action{TokenID: id.TokenID, Token: id.Name, Agent: id.Agent, Class: class,
			Method: req.Method, Path: req.URL.Path, Status: http.StatusForbidden, Outcome: "refused"})
	}
	writeError(w, http.StatusForbidden, "agent_rule", "The rules of this agent token do not give this operation ("+class+").")
	return false
}

// ApprovalBody is the body of a 202 approval_required response.
type ApprovalBody struct {
	Code     string          `json:"code"`
	Message  string          `json:"message"`
	Approval agents.Approval `json:"approval"`
}

func (r *Router) ask(w http.ResponseWriter, req *http.Request, id auth.Identity, class string) {
	var body []byte
	if req.Body != nil {
		b, err := io.ReadAll(io.LimitReader(req.Body, agents.MaxBody+1))
		if err != nil || len(b) > agents.MaxBody {
			writeError(w, http.StatusBadRequest, "bad_body", "The body of the request is too large for an approval request.")
			return
		}
		body = b
	}
	a, err := r.agents.Ask(agents.Approval{TokenID: id.TokenID, Token: id.Name, Agent: id.Agent, Class: class,
		Method: req.Method, Path: req.URL.RequestURI(), Body: body, Summary: Summary(req.Method, req.URL.Path)})
	if err != nil {
		r.logger.Error("the approval request was not kept", "err", err)
		writeError(w, http.StatusInternalServerError, "approval_failed", "The approval request was not kept.")
		return
	}
	r.agents.Record(agents.Action{TokenID: id.TokenID, Token: id.Name, Agent: id.Agent, Class: class,
		Method: req.Method, Path: req.URL.Path, Status: http.StatusAccepted, Approval: a.ID, Outcome: "asked"})
	r.logger.Info("an agent asks for an approval", "agent", id.Agent, "token", id.Name, "summary", a.Summary, "approval", a.ID)
	a.Body = nil
	writeJSON(w, http.StatusAccepted, ApprovalBody{Code: "approval_required",
		Message: "The owner must approve this operation in the console. Read GET /api/v1/approvals/" + a.ID + " for the result.", Approval: a})
}

// recordAgent writes a change of an agent in the record. agentCheck
// records the requests that it refuses or keeps for an approval, and
// runApproved records the approved requests.
func (r *Router) recordAgent(req *http.Request, status int) {
	if r.agents == nil {
		return
	}
	id, ok := IdentityOf(req)
	if !ok || id.Level != auth.LevelAgent {
		return
	}
	if v, _ := req.Context().Value(approvedKey{}).(bool); v {
		return
	}
	class := Classify(req.Method, req.URL.Path)
	if class == auth.ClassRead || req.URL.Path == "/api/v1/mcp" {
		return // the tools of the MCP server record their own requests
	}
	outcome := "done"
	if status >= 400 {
		outcome = "failed"
	}
	r.agents.Record(agents.Action{TokenID: id.TokenID, Token: id.Name, Agent: id.Agent, Class: class,
		Method: req.Method, Path: req.URL.Path, Status: status, Outcome: outcome})
}

// agentView tells if the identity sees only its own data.
func agentView(req *http.Request) (string, bool) {
	id, _ := IdentityOf(req)
	if id.Level == auth.LevelAgent {
		return id.TokenID, true
	}
	return "", false
}

func publicApproval(a agents.Approval) agents.Approval {
	a.Body = nil
	return a
}

func (r *Router) approvalsList(w http.ResponseWriter, req *http.Request) {
	token, _ := agentView(req)
	list := r.agents.List(token, req.URL.Query().Get("state"))
	out := make([]agents.Approval, 0, len(list))
	for _, a := range list {
		out = append(out, publicApproval(a))
	}
	writeJSON(w, http.StatusOK, map[string]any{"approvals": out})
}

func (r *Router) approvalGet(w http.ResponseWriter, req *http.Request) {
	a, err := r.agents.Get(req.PathValue("id"))
	token, isAgent := agentView(req)
	if err != nil || (isAgent && a.TokenID != token) {
		writeError(w, http.StatusNotFound, "approval_not_found", "The approval request does not exist.")
		return
	}
	writeJSON(w, http.StatusOK, publicApproval(a))
}

func (r *Router) approvalDecide(approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Reason string `json:"reason"`
		}
		if req.ContentLength != 0 {
			if !readJSON(w, req, &body) {
				return
			}
		}
		by, _ := IdentityOf(req)
		a, err := r.agents.Decide(req.PathValue("id"), approve, by.Kind+":"+by.Name, body.Reason)
		switch {
		case errors.Is(err, agents.ErrNotFound):
			writeError(w, http.StatusNotFound, "approval_not_found", "The approval request does not exist.")
			return
		case errors.Is(err, agents.ErrNotPending):
			writeError(w, http.StatusConflict, "approval_not_pending", "The approval request is not pending: it is "+a.State+".")
			return
		case err != nil:
			writeError(w, http.StatusInternalServerError, "approval_failed", err.Error())
			return
		}
		if !approve {
			r.logger.Info("the owner denied a request of an agent", "approval", a.ID, "summary", a.Summary)
			r.agents.Record(agents.Action{TokenID: a.TokenID, Token: a.Token, Agent: a.Agent, Class: a.Class, Method: a.Method,
				Path: a.Path, Status: http.StatusForbidden, Approval: a.ID, Outcome: "denied"})
			writeJSON(w, http.StatusOK, publicApproval(a))
			return
		}
		a = r.runApproved(req.Context(), a)
		writeJSON(w, http.StatusOK, publicApproval(a))
	}
}

// runApproved runs the request of an approval with the identity of the
// agent, as the agent sent it.
func (r *Router) runApproved(ctx context.Context, a agents.Approval) agents.Approval {
	fail := func(status int, msg string) agents.Approval {
		done, _ := r.agents.Finish(a.ID, status, msg)
		return done
	}
	tokens := r.auth.Tokens()
	var id auth.Identity
	found := false
	for _, t := range tokens {
		if t.ID == a.TokenID {
			id = auth.Identity{Kind: auth.KindToken, Name: t.Name, Level: t.Level, TokenID: t.ID, Agent: t.Agent, Rules: t.Rules}
			found = true
		}
	}
	if !found {
		return fail(http.StatusGone, "The token of the agent does not exist now.")
	}
	req := httptest.NewRequest(a.Method, a.Path, bytes.NewReader(a.Body))
	if len(a.Body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	c := context.WithValue(context.WithoutCancel(ctx), identityKey{}, id)
	c = context.WithValue(c, approvedKey{}, true)
	req = req.WithContext(c)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	result := strings.TrimSpace(rec.Body.String())
	done, err := r.agents.Finish(a.ID, rec.Code, result)
	if err != nil {
		r.logger.Error("the result of an approval was not kept", "err", err)
	}
	outcome := "done"
	if rec.Code >= 400 {
		outcome = "failed"
	}
	r.agents.Record(agents.Action{TokenID: a.TokenID, Token: a.Token, Agent: a.Agent, Class: a.Class, Method: a.Method,
		Path: a.Path, Status: rec.Code, Approval: a.ID, Outcome: outcome})
	r.logger.Info("the owner approved a request of an agent", "approval", a.ID, "summary", a.Summary, "status", rec.Code)
	return done
}

func (r *Router) agentActions(w http.ResponseWriter, req *http.Request) {
	token, isAgent := agentView(req)
	if !isAgent {
		token = req.URL.Query().Get("token")
	}
	limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
	if limit <= 0 || limit > agents.MaxActions {
		limit = 200
	}
	writeJSON(w, http.StatusOK, map[string]any{"actions": r.agents.Actions(token, limit)})
}

// tokenRules changes the rules of an agent token.
func (r *Router) tokenRules(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Rules map[string]string `json:"rules"`
	}
	if !readJSON(w, req, &body) {
		return
	}
	t, err := r.auth.SetRules(req.PathValue("id"), body.Rules)
	switch {
	case errors.Is(err, auth.ErrNotFound):
		writeError(w, http.StatusNotFound, "token_not_found", "The token does not exist.")
	case errors.Is(err, auth.ErrBadRules):
		writeError(w, http.StatusBadRequest, "bad_rules", err.Error()+".")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "token_failed", "The change of the rules failed.")
	default:
		r.logger.Info("the rules of an agent token changed", "token", t.Name)
		writeJSON(w, http.StatusOK, t)
	}
}
