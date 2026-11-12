package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// tool is one command of NidaOS for the agent.
type tool struct {
	description string
	// props are the arguments: name -> JSON schema.
	props    map[string]any
	required []string
	readOnly bool
	// destructive: the tool can delete data.
	destructive bool
	run         func(ctx context.Context, api Caller, args map[string]any) (string, error)
}

func s(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func n(desc string) map[string]any { return map[string]any{"type": "number", "description": desc} }

// call sends a request and gives the JSON object of the response. An
// empty response (204) gives an empty object.
func call(ctx context.Context, api Caller, method, path string, body any) (map[string]any, error) {
	var v map[string]any
	err := api.Do(ctx, method, path, body, &v)
	if errors.Is(err, io.EOF) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, apiErrorText(err)
	}
	if v == nil {
		v = map[string]any{}
	}
	return v, nil
}

// change sends a request that changes NidaOS. It tells the agent when the
// owner must approve the request.
func change(ctx context.Context, api Caller, method, path string, body any, done string) (string, error) {
	v, err := call(ctx, api, method, path, body)
	if err != nil {
		return "", err
	}
	if text, ok := approvalText(v); ok {
		return text, nil
	}
	if len(v) == 0 {
		return done, nil
	}
	return done + "\n" + pretty(v), nil
}

func esc(s string) string { return url.PathEscape(s) }

var tools = map[string]tool{
	"nidaos_status": {
		description: "The state of the NidaOS computer: the system, the storage tiers, the apps, the databases and the items that need attention. Start with this tool.",
		readOnly:    true,
		run: func(ctx context.Context, api Caller, _ map[string]any) (string, error) {
			out := map[string]any{}
			for k, p := range map[string]string{"system": "/api/v1/system", "tiers": "/api/v1/tiers", "apps": "/api/v1/apps", "databases": "/api/v1/databases"} {
				v, err := call(ctx, api, http.MethodGet, p, nil)
				if err != nil {
					out[k] = "not available: " + err.Error()
					continue
				}
				out[k] = compact(k, v)
			}
			if v, err := call(ctx, api, http.MethodGet, "/api/v1/approvals?state=pending", nil); err == nil {
				out["pending_approvals"] = v["approvals"]
			}
			return pretty(out), nil
		},
	},
	"metrics": {
		description: "The last metric snapshot: processor, memory, disks, network, and the processor and memory of each pod and service.",
		readOnly:    true,
		run: func(ctx context.Context, api Caller, _ map[string]any) (string, error) {
			v, err := call(ctx, api, http.MethodGet, "/api/v1/metrics", nil)
			if err != nil {
				return "", err
			}
			return pretty(v), nil
		},
	},
	"logs": {
		description: "Log lines of the system, the apps (source pod/nida-app-NAME) and the databases (pod/nida-db-NAME). Without since: the last lines in memory. With since (for example 2h, 7d): a search in the log store, the newest first.",
		props: map[string]any{"source": s("A prefix of the source id, for example pod/nida-app-blog or service/k3s."), "level": s("The minimum level: debug, info, warning or error."),
			"q": s("A text in the message."), "since": s("A duration before now, for example 90m or 2d."), "limit": n("The maximum number of lines, default 100.")},
		readOnly: true,
		run: func(ctx context.Context, api Caller, a map[string]any) (string, error) {
			q := url.Values{}
			for _, k := range []string{"source", "level", "q", "since"} {
				v, err := str(a, k, false)
				if err != nil {
					return "", err
				}
				if v != "" {
					q.Set(k, v)
				}
			}
			limit, ok, err := num(a, "limit")
			if err != nil {
				return "", err
			}
			if !ok || limit <= 0 {
				limit = 100
			}
			path := "/api/v1/logs"
			if q.Get("since") != "" {
				path = "/api/v1/logs/search"
				q.Set("limit", fmt.Sprint(int(min(limit, 1000))))
			} else {
				q.Set("tail", fmt.Sprint(int(min(limit, 5000))))
			}
			v, err := call(ctx, api, http.MethodGet, path+"?"+q.Encode(), nil)
			if err != nil {
				return "", err
			}
			lines, _ := v["lines"].([]any)
			var b strings.Builder
			for _, l := range lines {
				m, _ := l.(map[string]any)
				src, _ := m["source"].(map[string]any)
				fmt.Fprintf(&b, "%v %-7v %v %v\n", m["time"], m["level"], src["id"], m["message"])
			}
			if b.Len() == 0 {
				return "No line agrees with the filter.", nil
			}
			return b.String(), nil
		},
	},
	"apps_list": {
		description: "The apps with their state, address, version in operation and copies.",
		readOnly:    true,
		run: func(ctx context.Context, api Caller, _ map[string]any) (string, error) {
			v, err := call(ctx, api, http.MethodGet, "/api/v1/apps", nil)
			if err != nil {
				return "", err
			}
			return pretty(compact("apps", v)), nil
		},
	},
	"app_get": {
		description: "One app with its settings, its versions (the last 5) and the state of its copies. The values of the secrets are never in it.",
		props:       map[string]any{"name": s("The name of the app.")}, required: []string{"name"}, readOnly: true,
		run: func(ctx context.Context, api Caller, a map[string]any) (string, error) {
			name, err := str(a, "name", true)
			if err != nil {
				return "", err
			}
			v, err := call(ctx, api, http.MethodGet, "/api/v1/apps/"+esc(name), nil)
			if err != nil {
				return "", err
			}
			return pretty(v), nil
		},
	},
	"app_deploy": {
		description: "Deploy a new version of an app from an image of the local registry (localhost:5000/NAME:TAG). If the new version does not pass the health check, the version before stays in operation.",
		props:       map[string]any{"name": s("The name of the app."), "image": s("The image, for example localhost:5000/blog:1.4."), "summary": s("Optional: the change in words.")},
		required:    []string{"name", "image"},
		run: func(ctx context.Context, api Caller, a map[string]any) (string, error) {
			name, err := str(a, "name", true)
			if err != nil {
				return "", err
			}
			image, err := str(a, "image", true)
			if err != nil {
				return "", err
			}
			summary, _ := str(a, "summary", false)
			return change(ctx, api, http.MethodPost, "/api/v1/apps/"+esc(name)+"/deploys", map[string]any{"image": image, "summary": summary},
				"The deploy started. Read app_get until the state of the new version is serving or failed.")
		},
	},
	"app_restart": {
		description: "Restart all the copies of an app.",
		props:       map[string]any{"name": s("The name of the app.")}, required: []string{"name"},
		run: func(ctx context.Context, api Caller, a map[string]any) (string, error) {
			name, err := str(a, "name", true)
			if err != nil {
				return "", err
			}
			return change(ctx, api, http.MethodPost, "/api/v1/apps/"+esc(name)+"/restart", nil, "The app restarts.")
		},
	},
	"app_rollback": {
		description: "Go back to an old version of an app (a version in the state replaced). The rollback also has the health check.",
		props:       map[string]any{"name": s("The name of the app."), "version": n("The number of the version.")}, required: []string{"name", "version"},
		run: func(ctx context.Context, api Caller, a map[string]any) (string, error) {
			name, err := str(a, "name", true)
			if err != nil {
				return "", err
			}
			ver, ok, err := num(a, "version")
			if err != nil || !ok {
				return "", errArg{"The argument version is necessary."}
			}
			return change(ctx, api, http.MethodPost, "/api/v1/apps/"+esc(name)+"/rollback", map[string]any{"version": int(ver)}, "The rollback started.")
		},
	},
	"app_set": {
		description: "Change the settings of an app: the copies (0 stops the app), the memory limit, the processor limit, or environment variables. A change of the settings makes a new version with the same image.",
		props: map[string]any{"name": s("The name of the app."), "copies": n("The number of copies, 0 to 10."), "memory_mb": n("The memory limit in MB, 0 for no limit."),
			"cpu_cores": n("The processor limit in cores, 0 for no limit."), "set_variables": map[string]any{"type": "object", "description": "Variables to add or change: name -> value.", "additionalProperties": map[string]any{"type": "string"}},
			"remove_variables": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Names of variables to remove."}},
		required: []string{"name"},
		run: func(ctx context.Context, api Caller, a map[string]any) (string, error) {
			name, err := str(a, "name", true)
			if err != nil {
				return "", err
			}
			app, err := call(ctx, api, http.MethodGet, "/api/v1/apps/"+esc(name), nil)
			if err != nil {
				return "", err
			}
			body := map[string]any{}
			if c, ok, err := num(a, "copies"); err != nil {
				return "", err
			} else if ok {
				body["copies"] = int(c)
			}
			cfg, _ := app["config"].(map[string]any)
			if cfg == nil {
				cfg = map[string]any{}
			}
			changed := false
			limits, _ := cfg["limits"].(map[string]any)
			if limits == nil {
				limits = map[string]any{}
			}
			if m, ok, err := num(a, "memory_mb"); err != nil {
				return "", err
			} else if ok {
				limits["memory_bytes"], changed = int64(m*1024*1024), true
			}
			if c, ok, err := num(a, "cpu_cores"); err != nil {
				return "", err
			} else if ok {
				limits["cpu_millis"], changed = int64(c*1000), true
			}
			cfg["limits"] = limits
			vars, _ := cfg["variables"].(map[string]any)
			if vars == nil {
				vars = map[string]any{}
			}
			if sv, ok := a["set_variables"].(map[string]any); ok {
				for k, v := range sv {
					vars[k], changed = fmt.Sprint(v), true
				}
			}
			if rv, ok := a["remove_variables"].([]any); ok {
				for _, k := range rv {
					delete(vars, fmt.Sprint(k))
					changed = true
				}
			}
			cfg["variables"] = vars
			if changed {
				body["config"] = cfg
			}
			if len(body) == 0 {
				return "", errArg{"Give one setting to change."}
			}
			return change(ctx, api, http.MethodPatch, "/api/v1/apps/"+esc(name), body, "The settings changed.")
		},
	},
	"app_set_secret": {
		description: "Add or replace a secret variable of an app. NidaOS never shows the value again. The app restarts.",
		props:       map[string]any{"name": s("The name of the app."), "key": s("The name of the variable."), "value": s("The value.")},
		required:    []string{"name", "key", "value"},
		run: func(ctx context.Context, api Caller, a map[string]any) (string, error) {
			name, err := str(a, "name", true)
			if err != nil {
				return "", err
			}
			key, err := str(a, "key", true)
			if err != nil {
				return "", err
			}
			value, err := str(a, "value", true)
			if err != nil {
				return "", err
			}
			return change(ctx, api, http.MethodPut, "/api/v1/apps/"+esc(name)+"/secrets/"+esc(key), map[string]any{"value": value}, "The secret is saved.")
		},
	},
	"app_remove": {
		description: "Remove an app with its versions, its secrets and the files of its folders. This deletes data.",
		props:       map[string]any{"name": s("The name of the app.")}, required: []string{"name"}, destructive: true,
		run: func(ctx context.Context, api Caller, a map[string]any) (string, error) {
			name, err := str(a, "name", true)
			if err != nil {
				return "", err
			}
			return change(ctx, api, http.MethodDelete, "/api/v1/apps/"+esc(name), nil, "The app is removed.")
		},
	},
	"databases_list": {
		description: "The PostgreSQL databases with their state, size, connected apps and available updates.",
		readOnly:    true,
		run: func(ctx context.Context, api Caller, _ map[string]any) (string, error) {
			v, err := call(ctx, api, http.MethodGet, "/api/v1/databases", nil)
			if err != nil {
				return "", err
			}
			return pretty(compact("databases", v)), nil
		},
	},
	"database_get": {
		description: "One database with its settings, its connected apps and its backups.",
		props:       map[string]any{"name": s("The name of the database.")}, required: []string{"name"}, readOnly: true,
		run: func(ctx context.Context, api Caller, a map[string]any) (string, error) {
			name, err := str(a, "name", true)
			if err != nil {
				return "", err
			}
			v, err := call(ctx, api, http.MethodGet, "/api/v1/databases/"+esc(name), nil)
			if err != nil {
				return "", err
			}
			if b, err := call(ctx, api, http.MethodGet, "/api/v1/databases/"+esc(name)+"/backups", nil); err == nil {
				v["backups"] = b["backups"]
			}
			return pretty(v), nil
		},
	},
	"database_backup": {
		description: "Make a backup of a database now.",
		props:       map[string]any{"name": s("The name of the database.")}, required: []string{"name"},
		run: func(ctx context.Context, api Caller, a map[string]any) (string, error) {
			name, err := str(a, "name", true)
			if err != nil {
				return "", err
			}
			return change(ctx, api, http.MethodPost, "/api/v1/databases/"+esc(name)+"/backups", nil, "The backup started. Read database_get for the result.")
		},
	},
	"database_update": {
		description: "Update a database to the newest minor version. NidaOS makes a backup first.",
		props:       map[string]any{"name": s("The name of the database.")}, required: []string{"name"},
		run: func(ctx context.Context, api Caller, a map[string]any) (string, error) {
			name, err := str(a, "name", true)
			if err != nil {
				return "", err
			}
			return change(ctx, api, http.MethodPost, "/api/v1/databases/"+esc(name)+"/update", nil, "The update started.")
		},
	},
	"database_remove": {
		description: "Remove a database and its data. The backups stay. This deletes data.",
		props:       map[string]any{"name": s("The name of the database.")}, required: []string{"name"}, destructive: true,
		run: func(ctx context.Context, api Caller, a map[string]any) (string, error) {
			name, err := str(a, "name", true)
			if err != nil {
				return "", err
			}
			return change(ctx, api, http.MethodDelete, "/api/v1/databases/"+esc(name), nil, "The database is removed.")
		},
	},
	"updates_status": {
		description: "The system versions: in operation, downloaded for the next restart, kept for a rollback, and available.",
		readOnly:    true,
		run: func(ctx context.Context, api Caller, _ map[string]any) (string, error) {
			v, err := call(ctx, api, http.MethodGet, "/api/v1/updates", nil)
			if err != nil {
				return "", err
			}
			return pretty(v), nil
		},
	},
	"update_check": {
		description: "Ask the registry for a newer system version.",
		run: func(ctx context.Context, api Caller, _ map[string]any) (string, error) {
			return change(ctx, api, http.MethodPost, "/api/v1/updates/check", nil, "The check started. Read updates_status later.")
		},
	},
	"approvals_list": {
		description: "Your approval requests and their state: pending, done, failed, denied or expired.",
		readOnly:    true,
		run: func(ctx context.Context, api Caller, _ map[string]any) (string, error) {
			v, err := call(ctx, api, http.MethodGet, "/api/v1/approvals", nil)
			if err != nil {
				return "", err
			}
			return pretty(v), nil
		},
	},
	"approval_get": {
		description: "One approval request. When the owner approved it, result has the response of NidaOS.",
		props:       map[string]any{"id": s("The id of the approval request.")}, required: []string{"id"}, readOnly: true,
		run: func(ctx context.Context, api Caller, a map[string]any) (string, error) {
			id, err := str(a, "id", true)
			if err != nil {
				return "", err
			}
			v, err := call(ctx, api, http.MethodGet, "/api/v1/approvals/"+esc(id), nil)
			if err != nil {
				return "", err
			}
			return pretty(v), nil
		},
	},
}

// toolList gives the tools for tools/list, sorted by name.
func toolList() []any {
	names := make([]string, 0, len(tools))
	for k := range tools {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]any, 0, len(names))
	for _, k := range names {
		t := tools[k]
		props := t.props
		if props == nil {
			props = map[string]any{}
		}
		schema := map[string]any{"type": "object", "properties": props}
		if len(t.required) > 0 {
			schema["required"] = t.required
		}
		out = append(out, map[string]any{
			"name":        k,
			"description": t.description,
			"inputSchema": schema,
			"annotations": map[string]any{"readOnlyHint": t.readOnly, "destructiveHint": t.destructive, "openWorldHint": false},
		})
	}
	return out
}

// compact keeps the fields of the lists that an agent needs first.
func compact(kind string, v map[string]any) any {
	switch kind {
	case "apps":
		list, _ := v["apps"].([]any)
		out := []any{}
		for _, x := range list {
			m, _ := x.(map[string]any)
			out = append(out, pick(m, "name", "state", "address", "active", "copies", "status", "repository"))
		}
		return out
	case "databases":
		list, _ := v["databases"].([]any)
		out := []any{}
		for _, x := range list {
			m, _ := x.(map[string]any)
			out = append(out, pick(m, "name", "state", "version", "tier", "links", "live", "update_available", "reason"))
		}
		return out
	case "tiers":
		return v["tiers"]
	}
	return v
}

func pick(m map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	return out
}
