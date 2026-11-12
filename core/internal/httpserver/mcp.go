package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/fathorMB/NidaOS/core/internal/auth"
	"github.com/fathorMB/NidaOS/core/internal/client"
	"github.com/fathorMB/NidaOS/core/internal/mcp"
	"github.com/fathorMB/NidaOS/core/internal/version"
)

// The MCP server over HTTP (milestone M-09): the "Streamable HTTP"
// transport of the Model Context Protocol, at POST /api/v1/mcp. An agent
// connects with its token in the header Authorization. Each tool calls the
// API in the service with the identity of the request, thus the rules of
// the token apply to each operation.

var mcpServer = &mcp.Server{Instructions: mcp.Instructions}

func (r *Router) addMCPRoutes() {
	r.handle(http.MethodPost, "/api/v1/mcp", r.mcpPost)
	r.handle(http.MethodGet, "/api/v1/mcp", func(w http.ResponseWriter, _ *http.Request) {
		// The server sends no message without a request.
		w.Header().Set("Allow", "POST")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Send the MCP messages with POST.")
	})
}

func (r *Router) mcpPost(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_body", "The body is too large.")
		return
	}
	id, _ := IdentityOf(req)
	api := &inProcess{r: r, id: id}
	mcpServer.Version = version.Version
	// A batch is not in the MCP version 2025-06-18: one message only.
	resp, ok := mcpServer.Handle(req.Context(), api, bytes.TrimSpace(body))
	if !ok {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(resp)
}

// inProcess calls the API in the service with an identity. It is the
// Caller of the MCP tools over HTTP.
type inProcess struct {
	r  *Router
	id auth.Identity
}

func (p *inProcess) Do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req = req.WithContext(context.WithValue(context.WithoutCancel(ctx), identityKey{}, p.id))
	rec := httptest.NewRecorder()
	p.r.ServeHTTP(rec, req)
	if rec.Code >= 300 {
		e := &client.APIError{Status: rec.Code}
		_ = json.Unmarshal(rec.Body.Bytes(), e)
		return e
	}
	if out == nil || rec.Body.Len() == 0 {
		if out != nil {
			return io.EOF
		}
		return nil
	}
	return json.Unmarshal(rec.Body.Bytes(), out)
}
