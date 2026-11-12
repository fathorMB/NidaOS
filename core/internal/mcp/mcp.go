// Package mcp is the MCP server of NidaOS (milestone M-09). An agent, for
// example Claude Code, starts "nida mcp" and gets the commands of the
// console as tools. The server calls the API of nida-core with the token
// of the agent, thus the rules of the token apply (decision D35).
//
// The transport is the stdio transport of the Model Context Protocol: one
// JSON-RPC 2.0 message on each line.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

// ProtocolVersion is the newest MCP version of this server.
const ProtocolVersion = "2025-06-18"

// Caller calls the API of nida-core. *client.Client implements it.
type Caller interface {
	Do(ctx context.Context, method, path string, body, out any) error
}

// Server answers the messages of one agent.
type Server struct {
	API     Caller
	Version string
	// Instructions is the text that the agent reads before it manages
	// NidaOS.
	Instructions string

	mu  sync.Mutex
	out io.Writer
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Serve reads the messages from in until the end, and writes the answers
// to out.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	s.out = out
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if resp, ok := s.Handle(ctx, s.API, []byte(line)); ok {
			s.write(resp)
		}
	}
	return sc.Err()
}

// Handle answers one message with the API api. It gives false for a
// notification: a notification gets no answer.
func (s *Server) Handle(ctx context.Context, api Caller, msg []byte) ([]byte, bool) {
	var req request
	if err := json.Unmarshal(msg, &req); err != nil {
		b, _ := json.Marshal(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "The message is not JSON."}})
		return b, true
	}
	r, ok := s.handle(ctx, api, req)
	if !ok {
		return nil, false
	}
	b, err := json.Marshal(r)
	if err != nil {
		return nil, false
	}
	return b, true
}

func (s *Server) write(b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.out.Write(append(b, '\n'))
}

func (s *Server) handle(ctx context.Context, api Caller, req request) (response, bool) {
	// A notification has no id and gets no answer.
	notification := len(req.ID) == 0 || string(req.ID) == "null"
	var result any
	var rerr *rpcError
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := ProtocolVersion
		if p.ProtocolVersion != "" && p.ProtocolVersion < ProtocolVersion {
			version = p.ProtocolVersion
		}
		result = map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "nidaos", "title": "NidaOS", "version": s.Version},
			"instructions":    s.Instructions,
		}
	case "notifications/initialized", "notifications/cancelled":
		return response{}, false
	case "ping":
		result = map[string]any{}
	case "tools/list":
		result = map[string]any{"tools": toolList()}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			rerr = &rpcError{-32602, "The parameters of tools/call are not correct."}
			break
		}
		t, ok := tools[p.Name]
		if !ok {
			rerr = &rpcError{-32602, "The tool " + p.Name + " does not exist."}
			break
		}
		args := map[string]any{}
		if len(p.Arguments) > 0 && string(p.Arguments) != "null" {
			if err := json.Unmarshal(p.Arguments, &args); err != nil {
				rerr = &rpcError{-32602, "The arguments must be a JSON object."}
				break
			}
		}
		text, err := t.run(ctx, api, args)
		if err != nil {
			result = toolResult(err.Error(), true)
		} else {
			result = toolResult(text, false)
		}
	default:
		if notification {
			return response{}, false
		}
		rerr = &rpcError{-32601, "The method " + req.Method + " does not exist."}
	}
	if notification {
		return response{}, false
	}
	return response{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rerr}, true
}

// MaxText is the maximum size of the text of one tool result.
const MaxText = 24 << 10

func toolResult(text string, isError bool) map[string]any {
	if len(text) > MaxText {
		text = text[:MaxText] + "\n… (the result is longer: use a filter)"
	}
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": isError}
}

// errArg tells that an argument is missing or not correct.
type errArg struct{ msg string }

func (e errArg) Error() string { return e.msg }

func str(args map[string]any, k string, required bool) (string, error) {
	v, ok := args[k]
	if !ok || v == nil || v == "" {
		if required {
			return "", errArg{"The argument " + k + " is necessary."}
		}
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", errArg{"The argument " + k + " must be a text."}
	}
	return s, nil
}

func num(args map[string]any, k string) (float64, bool, error) {
	v, ok := args[k]
	if !ok || v == nil {
		return 0, false, nil
	}
	f, ok := v.(float64)
	if !ok {
		return 0, false, errArg{"The argument " + k + " must be a number."}
	}
	return f, true, nil
}

// approvalText tells the agent that the owner must approve the operation.
func approvalText(v map[string]any) (string, bool) {
	if v["code"] != "approval_required" {
		return "", false
	}
	a, _ := v["approval"].(map[string]any)
	return fmt.Sprintf("The rules of your token ask the owner to approve this operation: %v. "+
		"The owner sees approval request %v in the NidaOS console. Nothing changed yet. "+
		"Call approval_get with the id %v later to read the result. Do not send the same request again.",
		a["summary"], a["id"], a["id"]), true
}

// pretty gives the JSON of v with indentation.
func pretty(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// apiErrorText gives a short text for an error of the API.
func apiErrorText(err error) error {
	var ae interface{ Error() string }
	if errors.As(err, &ae) {
		return errors.New("NidaOS refused the request: " + ae.Error())
	}
	return err
}
