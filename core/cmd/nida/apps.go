package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/apps"
)

const appUsage = `use one of:
  app list
  app show NAME
  app add NAME [--repo OWNER/NAME] [--branch B] [--port N] [--health PATH] [--copies N]
               [--env K=V]... [--folder NAME:PATH:TIER:SIZE]... [--cpu MILLIS] [--memory SIZE]
  app deploy NAME IMAGE [--commit SHA] [--summary TEXT] [--wait]
  app deploy NAME --build DIR [--commit SHA] [--summary TEXT] [--wait]   (in a runner pod)
  app connect NAME OWNER/REPO [--branch B] [--no-pr]
  app disconnect NAME
  app workflow NAME   (open the pull request with the deploy workflow again)
  app jobs NAME
  app job-log JOB
  app rollback NAME VERSION [--wait]
  app set NAME [--port N | --no-port] [--health PATH | --no-health] [--env K=V]... [--unset K]...
               [--copies N] [--folder ...]... [--repo R] [--branch B] [--cpu M] [--memory S] [--wait]
  app secret set NAME KEY [--value V]   (without --value, nida reads the value from stdin)
  app secret rm NAME KEY
  app move NAME FOLDER TIER [--wait]
  app restart NAME
  app remove NAME --yes`

// appView is an app of the API.
type appView struct {
	apps.App
	State   string       `json:"state"`
	Address string       `json:"address"`
	Secrets []string     `json:"secrets"`
	Status  *apps.Status `json:"status"`
}

// listFlag is a flag that the command line can give more than one time.
type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

func appPath(name string, parts ...string) string {
	p := "/api/v1/apps/" + url.PathEscape(name)
	for _, s := range parts {
		p += "/" + url.PathEscape(s)
	}
	return p
}

func (a *app) apps(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: %s", errUsage, appUsage)
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "list", "ls":
		return a.appList(ctx, rest)
	case "show":
		if len(rest) != 1 {
			return fmt.Errorf("%w: use app show NAME", errUsage)
		}
		return a.appShow(ctx, rest[0])
	case "add":
		return a.appAdd(ctx, rest)
	case "deploy":
		return a.appDeploy(ctx, rest)
	case "connect":
		return a.appConnect(ctx, rest)
	case "disconnect":
		return a.appDisconnect(ctx, rest)
	case "workflow":
		return a.appWorkflow(ctx, rest)
	case "jobs":
		return a.appJobs(ctx, rest)
	case "job-log":
		return a.appJobLog(ctx, rest)
	case "rollback":
		return a.appRollback(ctx, rest)
	case "set":
		return a.appSet(ctx, rest)
	case "secret", "secrets":
		return a.appSecret(ctx, rest)
	case "move":
		return a.appMove(ctx, rest)
	case "restart":
		if len(rest) != 1 {
			return fmt.Errorf("%w: use app restart NAME", errUsage)
		}
		if err := a.c.Do(ctx, http.MethodPost, appPath(rest[0], "restart"), nil, nil); err != nil {
			return err
		}
		a.say("All the copies of %s start again.\n", rest[0])
		return nil
	case "remove", "rm":
		return a.appRemove(ctx, rest)
	}
	return fmt.Errorf("%w: unknown app command %q\n%s", errUsage, cmd, appUsage)
}

// say writes a message for a person. With --json, nida writes only JSON.
func (a *app) say(format string, args ...any) {
	if !a.json {
		fmt.Fprintf(a.out, format, args...)
	}
}

func (a *app) appList(ctx context.Context, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("%w: app list has no arguments", errUsage)
	}
	var body struct {
		Apps []appView `json:"apps"`
	}
	if err := a.c.Get(ctx, "/api/v1/apps", nil, &body); err != nil {
		return err
	}
	if a.json {
		return a.writeJSON(body)
	}
	if len(body.Apps) == 0 {
		fmt.Fprintln(a.out, "There are no apps. Add one with: nida app add NAME --port 8080")
		return nil
	}
	w := a.table()
	fmt.Fprintln(w, "NAME\tSTATE\tVERSION\tCOPIES\tADDRESS\tREPOSITORY")
	for _, v := range body.Apps {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", v.Name, v.State, activeText(v.App), copiesText(v), dash(v.Address), dash(v.Repository))
	}
	return w.Flush()
}

func activeText(a apps.App) string {
	if a.Active == 0 {
		return "-"
	}
	return strconv.Itoa(a.Active)
}

func copiesText(v appView) string {
	if v.Status == nil {
		return fmt.Sprintf("?/%d", v.Copies)
	}
	return fmt.Sprintf("%d/%d", v.Status.Ready, v.Copies)
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func (a *app) getApp(ctx context.Context, name string) (appView, error) {
	var v appView
	err := a.c.Get(ctx, appPath(name), nil, &v)
	return v, err
}

func (a *app) appShow(ctx context.Context, name string) error {
	v, err := a.getApp(ctx, name)
	if err != nil {
		return err
	}
	if a.json {
		return a.writeJSON(v)
	}
	w := a.table()
	fmt.Fprintf(w, "App\t%s\n", v.Name)
	fmt.Fprintf(w, "State\t%s\n", v.State)
	fmt.Fprintf(w, "Address\t%s\n", dash(v.Address))
	src := dash(v.Repository)
	if v.Branch != "" {
		src += " (branch " + v.Branch + ")"
	}
	fmt.Fprintf(w, "Repository\t%s\n", src)
	fmt.Fprintf(w, "Copies\t%s\n", copiesText(v))
	if p := v.Config.Port; p != nil {
		check := "the port is open"
		if hc := v.Config.HealthCheck; hc != nil {
			check = "HTTP " + hc.Path
		}
		fmt.Fprintf(w, "Port\t%d (health check: %s)\n", *p, check)
	} else {
		fmt.Fprintf(w, "Port\tnone\n")
	}
	if l := v.Config.Limits; l.CPUMillis > 0 || l.MemoryBytes > 0 {
		fmt.Fprintf(w, "Limits\tprocessor %s, memory %s\n", cpuText(l.CPUMillis), memText(l.MemoryBytes))
	}
	if len(v.Config.Variables) > 0 {
		fmt.Fprintf(w, "Variables\t%s\n", strings.Join(sortedKeys(v.Config.Variables), ", "))
	}
	if len(v.Secrets) > 0 {
		fmt.Fprintf(w, "Secrets\t%s\n", strings.Join(v.Secrets, ", "))
	}
	for _, f := range v.Folders {
		line := fmt.Sprintf("%s on %s, %s, %s", f.Path, f.Tier, size(uint64(f.SizeBytes)), f.Name)
		if f.MovingTo != "" {
			line += " (moving to " + f.MovingTo + ")"
		}
		if f.MoveError != "" {
			line += " (last move failed: " + f.MoveError + ")"
		}
		fmt.Fprintf(w, "Folder\t%s\n", line)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if len(v.Versions) == 0 {
		fmt.Fprintf(a.out, "\nNo version. Deploy one with: nida app deploy %s localhost:5000/%s:TAG\n", v.Name, v.Name)
		return nil
	}
	fmt.Fprintln(a.out)
	t := a.table()
	fmt.Fprintln(t, "VERSION\tSTATE\tIMAGE\tCREATED\tNOTE")
	for i := len(v.Versions) - 1; i >= 0; i-- {
		ver := v.Versions[i]
		state := ver.State
		if ver.Number == v.Active {
			state += " (active)"
		}
		note := firstOf(ver.Reason, ver.Summary)
		fmt.Fprintf(t, "%d\t%s\t%s\t%s\t%s\n", ver.Number, state, ver.Image, stamp(ver.Created), oneLine(note))
	}
	return t.Flush()
}

func firstOf(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func cpuText(m int) string {
	if m == 0 {
		return "no limit"
	}
	return strconv.FormatFloat(float64(m)/1000, 'f', -1, 64) + " cores"
}

func memText(b int64) string {
	if b == 0 {
		return "no limit"
	}
	return size(uint64(b))
}

// parseSizeArg reads a size such as 512M, 10G or 1T (powers of 1024).
func parseSizeArg(s string) (int64, error) {
	u := strings.ToUpper(strings.TrimSpace(s))
	u = strings.TrimSuffix(strings.TrimSuffix(u, "B"), "I")
	mul := int64(1)
	if n := len(u); n > 0 {
		switch u[n-1] {
		case 'K':
			mul, u = 1<<10, u[:n-1]
		case 'M':
			mul, u = 1<<20, u[:n-1]
		case 'G':
			mul, u = 1<<30, u[:n-1]
		case 'T':
			mul, u = 1<<40, u[:n-1]
		}
	}
	n, err := strconv.ParseFloat(u, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("the size %q is not valid: use for example 512M or 10G", s)
	}
	return int64(n * float64(mul)), nil
}

// parseFolder reads NAME:PATH:TIER:SIZE.
func parseFolder(s string) (apps.Folder, error) {
	p := strings.Split(s, ":")
	if len(p) != 4 {
		return apps.Folder{}, fmt.Errorf("%w: use --folder NAME:PATH:TIER:SIZE, for example data:/var/lib/app:standard:10G", errUsage)
	}
	n, err := parseSizeArg(p[3])
	if err != nil {
		return apps.Folder{}, err
	}
	return apps.Folder{Name: p[0], Path: p[1], Tier: p[2], SizeBytes: n}, nil
}

func parseEnv(list []string, into map[string]string) error {
	for _, kv := range list {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return fmt.Errorf("%w: use --env NAME=VALUE", errUsage)
		}
		into[k] = v
	}
	return nil
}

// configFlags are the options of add and set for the config.
type configFlags struct {
	port, copies, cpu  int
	health, memory     string
	repo, branch       string
	noPort, noHealth   bool
	env, unset, folder listFlag
}

func (f *configFlags) register(fs *flag.FlagSet) {
	fs.IntVar(&f.port, "port", 0, "")
	fs.IntVar(&f.copies, "copies", -1, "")
	fs.IntVar(&f.cpu, "cpu", -1, "")
	fs.StringVar(&f.health, "health", "", "")
	fs.StringVar(&f.memory, "memory", "", "")
	fs.StringVar(&f.repo, "repo", "", "")
	fs.StringVar(&f.branch, "branch", "", "")
	fs.BoolVar(&f.noPort, "no-port", false, "")
	fs.BoolVar(&f.noHealth, "no-health", false, "")
	fs.Var(&f.env, "env", "")
	fs.Var(&f.unset, "unset", "")
	fs.Var(&f.folder, "folder", "")
}

// apply changes cfg with the options. It tells if the config changed.
func (f *configFlags) apply(cfg *apps.Config) (bool, error) {
	changed := false
	if f.port != 0 {
		p := f.port
		cfg.Port, changed = &p, true
	}
	if f.noPort {
		cfg.Port, cfg.HealthCheck, changed = nil, nil, true
	}
	if f.health != "" {
		cfg.HealthCheck, changed = &apps.HealthCheck{Path: f.health}, true
	}
	if f.noHealth {
		cfg.HealthCheck, changed = nil, true
	}
	if len(f.env) > 0 || len(f.unset) > 0 {
		if cfg.Variables == nil {
			cfg.Variables = map[string]string{}
		}
		if err := parseEnv(f.env, cfg.Variables); err != nil {
			return false, err
		}
		for _, k := range f.unset {
			delete(cfg.Variables, k)
		}
		changed = true
	}
	if f.cpu >= 0 {
		cfg.Limits.CPUMillis, changed = f.cpu, true
	}
	if f.memory != "" {
		if f.memory == "0" {
			cfg.Limits.MemoryBytes = 0
		} else {
			n, err := parseSizeArg(f.memory)
			if err != nil {
				return false, err
			}
			cfg.Limits.MemoryBytes = n
		}
		changed = true
	}
	return changed, nil
}

func (f *configFlags) folders() ([]apps.Folder, error) {
	var out []apps.Folder
	for _, s := range f.folder {
		fo, err := parseFolder(s)
		if err != nil {
			return nil, err
		}
		out = append(out, fo)
	}
	return out, nil
}

func (a *app) appAdd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("app add", flag.ContinueOnError)
	var f configFlags
	f.register(fs)
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("%w: use app add NAME [options]", errUsage)
	}
	var cfg apps.Config
	if _, err := f.apply(&cfg); err != nil {
		return err
	}
	folders, err := f.folders()
	if err != nil {
		return err
	}
	body := map[string]any{"name": rest[0], "repository": f.repo, "branch": f.branch, "config": cfg, "folders": folders}
	if f.copies >= 0 {
		body["copies"] = f.copies
	}
	var v appView
	if err := a.c.Do(ctx, http.MethodPost, "/api/v1/apps", body, &v); err != nil {
		return err
	}
	if a.json {
		return a.writeJSON(v)
	}
	fmt.Fprintf(a.out, "The app %s is added. Push an image to the local registry, then deploy it:\n\n", v.Name)
	fmt.Fprintf(a.out, "  podman push --tls-verify=false IMAGE localhost:5000/%s:TAG\n", v.Name)
	fmt.Fprintf(a.out, "  nida app deploy %s localhost:5000/%s:TAG --wait\n", v.Name, v.Name)
	return nil
}

func (a *app) appDeploy(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("app deploy", flag.ContinueOnError)
	commit := fs.String("commit", "", "")
	summary := fs.String("summary", "", "")
	wait := fs.Bool("wait", false, "")
	build := fs.String("build", "", "")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if *build != "" && len(rest) == 1 {
		return a.buildDeploy(ctx, rest[0], *build, *commit, *summary, *wait)
	}
	if len(rest) != 2 || *build != "" {
		return fmt.Errorf("%w: use app deploy NAME IMAGE [--commit SHA] [--summary TEXT] [--wait], or app deploy NAME --build DIR", errUsage)
	}
	var v apps.Version
	if err := a.c.Do(ctx, http.MethodPost, appPath(rest[0], "deploys"), map[string]string{"image": rest[1], "commit": *commit, "summary": *summary}, &v); err != nil {
		return err
	}
	return a.afterDeploy(ctx, rest[0], v.Number, *wait)
}

func (a *app) appRollback(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("app rollback", flag.ContinueOnError)
	wait := fs.Bool("wait", false, "")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 2 {
		return fmt.Errorf("%w: use app rollback NAME VERSION [--wait]", errUsage)
	}
	n, err := strconv.Atoi(rest[1])
	if err != nil {
		return fmt.Errorf("%w: the version must be a number", errUsage)
	}
	var v apps.Version
	if err := a.c.Do(ctx, http.MethodPost, appPath(rest[0], "rollback"), map[string]int{"version": n}, &v); err != nil {
		return err
	}
	return a.afterDeploy(ctx, rest[0], v.Number, *wait)
}

// errDeployFailed: the version did not pass the health check.
var errDeployFailed = errors.New("the deploy failed")

// afterDeploy writes the new version. With wait, it waits for the result
// of the deploy.
func (a *app) afterDeploy(ctx context.Context, name string, number int, wait bool) error {
	if !wait {
		if a.json {
			return a.writeJSON(map[string]int{"version": number})
		}
		fmt.Fprintf(a.out, "The deploy of version %d of %s started. See the result with: nida app show %s\n", number, name, name)
		return nil
	}
	a.say("The deploy of version %d of %s started. nida waits for the health check.\n", number, name)
	for {
		app, err := a.getApp(ctx, name)
		if err != nil {
			return err
		}
		v, ok := app.Version(number)
		if !ok {
			return fmt.Errorf("the version %d is not there", number)
		}
		switch v.State {
		case apps.StateServing, apps.StateReplaced:
			if a.json {
				return a.writeJSON(v)
			}
			fmt.Fprintf(a.out, "The version %d is in operation", number)
			if app.Address != "" {
				fmt.Fprintf(a.out, " at %s", app.Address)
			}
			fmt.Fprintln(a.out, ".")
			return nil
		case apps.StateFailed:
			if a.json {
				_ = a.writeJSON(v)
			}
			msg := fmt.Sprintf("%v: %s", errDeployFailed, v.Reason)
			if app.Active != 0 {
				msg += fmt.Sprintf(". The version %d stays in operation", app.Active)
			}
			return errors.New(msg)
		}
		if err := a.sleep(ctx, 2*time.Second); err != nil {
			return err
		}
	}
}

func (a *app) sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

func (a *app) appSet(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("app set", flag.ContinueOnError)
	var f configFlags
	f.register(fs)
	wait := fs.Bool("wait", false, "")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("%w: use app set NAME [options]", errUsage)
	}
	cur, err := a.getApp(ctx, rest[0])
	if err != nil {
		return err
	}
	body := map[string]any{}
	cfg := cur.Config
	changed, err := f.apply(&cfg)
	if err != nil {
		return err
	}
	if changed {
		body["config"] = cfg
	}
	if f.copies >= 0 {
		body["copies"] = f.copies
	}
	if f.repo != "" {
		body["repository"] = f.repo
	}
	if f.branch != "" {
		body["branch"] = f.branch
	}
	folders, err := f.folders()
	if err != nil {
		return err
	}
	if len(folders) > 0 {
		body["add_folders"] = folders
	}
	if len(body) == 0 {
		return fmt.Errorf("%w: give one option or more to change", errUsage)
	}
	var res struct {
		appView
		Deploy int `json:"deploy"`
	}
	if err := a.c.Do(ctx, http.MethodPatch, appPath(rest[0]), body, &res); err != nil {
		return err
	}
	if res.Deploy != 0 {
		return a.afterDeploy(ctx, rest[0], res.Deploy, *wait)
	}
	if a.json {
		return a.writeJSON(res.appView)
	}
	fmt.Fprintf(a.out, "The settings of %s are changed.", rest[0])
	if changed && cur.Active == 0 {
		fmt.Fprint(a.out, " The first deploy uses them.")
	}
	fmt.Fprintln(a.out)
	return nil
}

func (a *app) appSecret(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: use app secret set NAME KEY or app secret rm NAME KEY", errUsage)
	}
	switch args[0] {
	case "set":
		fs := flag.NewFlagSet("app secret set", flag.ContinueOnError)
		value := fs.String("value", "", "")
		rest, err := parseFlags(fs, args[1:])
		if err != nil {
			return err
		}
		if len(rest) != 2 {
			return fmt.Errorf("%w: use app secret set NAME KEY [--value V]", errUsage)
		}
		v := *value
		if v == "" {
			if v, err = readLine(os.Stdin); err != nil {
				return err
			}
		}
		if err := a.c.Do(ctx, http.MethodPut, appPath(rest[0], "secrets", rest[1]), map[string]string{"value": v}, nil); err != nil {
			return err
		}
		a.say("The secret %s of %s is saved. The copies get it at the next deploy.\n", rest[1], rest[0])
		return nil
	case "rm", "delete":
		if len(args) != 3 {
			return fmt.Errorf("%w: use app secret rm NAME KEY", errUsage)
		}
		if err := a.c.Do(ctx, http.MethodDelete, appPath(args[1], "secrets", args[2]), nil, nil); err != nil {
			return err
		}
		a.say("The secret %s of %s is removed. The copies keep it until the next deploy.\n", args[2], args[1])
		return nil
	}
	return fmt.Errorf("%w: unknown secret command %q", errUsage, args[0])
}

// readLine reads the first line of r, without the line end.
func readLine(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return "", errors.New("the value is empty: give it with --value or on stdin")
	}
	return line, nil
}

func (a *app) appMove(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("app move", flag.ContinueOnError)
	wait := fs.Bool("wait", false, "")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 3 {
		return fmt.Errorf("%w: use app move NAME FOLDER TIER [--wait]", errUsage)
	}
	name, folder, tier := rest[0], rest[1], rest[2]
	if err := a.c.Do(ctx, http.MethodPost, appPath(name, "folders", folder, "move"), map[string]string{"tier": tier}, nil); err != nil {
		return err
	}
	a.say("The move of the folder %s of %s to the %s tier started. The app stops during the copy.\n", folder, name, tier)
	if !*wait {
		return nil
	}
	for {
		v, err := a.getApp(ctx, name)
		if err != nil {
			return err
		}
		f, ok := v.Folder(folder)
		if !ok {
			return fmt.Errorf("the folder %s is not there", folder)
		}
		if f.MovingTo == "" {
			if f.MoveError != "" {
				return fmt.Errorf("the move failed: %s. The folder stays on the %s tier", f.MoveError, f.Tier)
			}
			if a.json {
				return a.writeJSON(f)
			}
			fmt.Fprintf(a.out, "The folder %s is on the %s tier.\n", folder, f.Tier)
			return nil
		}
		if err := a.sleep(ctx, 2*time.Second); err != nil {
			return err
		}
	}
}

func (a *app) appRemove(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("app remove", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("%w: use app remove NAME --yes", errUsage)
	}
	if !*yes {
		return fmt.Errorf("%w: the remove deletes the app, its versions, its secrets and the files of its folders. Add --yes to confirm", errUsage)
	}
	if err := a.c.Do(ctx, http.MethodDelete, appPath(rest[0]), nil, nil); err != nil {
		return err
	}
	a.say("The app %s is removed, with the files of its folders.\n", rest[0])
	return nil
}

// ca writes the certificate of the NidaOS authority, to install on a
// device (decision D20).
func (a *app) ca(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ca", flag.ContinueOnError)
	fp := fs.Bool("fingerprint", false, "")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return fmt.Errorf("%w: use ca [--fingerprint]", errUsage)
	}
	if *fp || a.json {
		var info map[string]string
		if err := a.c.Get(ctx, "/api/v1/ca", nil, &info); err != nil {
			return err
		}
		if a.json {
			return a.writeJSON(info)
		}
		fmt.Fprintln(a.out, info["fingerprint_sha256"])
		return nil
	}
	pem, err := a.c.Raw(ctx, "/api/v1/ca.crt")
	if err != nil {
		return err
	}
	_, err = a.out.Write(pem)
	return err
}
