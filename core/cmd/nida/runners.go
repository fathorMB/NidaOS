package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/runners"
)

const githubUsage = `use one of:
  github                     the GitHub App of NidaOS
  github setup [--org ORG]   make the GitHub App (decision D25): open the address in a browser
  github import --app-id N --key-file FILE   use a GitHub App that exists already
  github remove --yes        remove the GitHub App from NidaOS (remove it on GitHub too)`

// githubView is the GitHub App in the API.
type githubView struct {
	Configured bool   `json:"configured"`
	ID         int64  `json:"id"`
	Slug       string `json:"slug"`
	Name       string `json:"name"`
	Owner      string `json:"owner"`
	HTMLURL    string `json:"html_url"`
	InstallURL string `json:"install_url"`
}

func (a *app) github(ctx context.Context, args []string) error {
	if len(args) == 0 {
		var v githubView
		if err := a.c.Get(ctx, "/api/v1/github", nil, &v); err != nil {
			return err
		}
		if a.json {
			return a.writeJSON(v)
		}
		if !v.Configured {
			fmt.Fprintln(a.out, "NidaOS has no GitHub App. Make it with: nida github setup")
			return nil
		}
		t := a.table()
		fmt.Fprintf(t, "App\t%s (%s)\n", v.Name, v.Slug)
		fmt.Fprintf(t, "ID\t%d\n", v.ID)
		fmt.Fprintf(t, "Owner\t%s\n", v.Owner)
		fmt.Fprintf(t, "Settings\t%s\n", dash(v.HTMLURL))
		fmt.Fprintf(t, "Repositories\t%s\n", v.InstallURL)
		return t.Flush()
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "setup":
		fs := flag.NewFlagSet("github setup", flag.ContinueOnError)
		org := fs.String("org", "", "")
		if r, err := parseFlags(fs, rest); err != nil || len(r) > 0 {
			return fmt.Errorf("%w: use github setup [--org ORG]", errUsage)
		}
		var st struct {
			URL     string    `json:"url"`
			Expires time.Time `json:"expires"`
		}
		if err := a.c.Do(ctx, http.MethodPost, "/api/v1/github/setup", map[string]string{"organization": *org}, &st); err != nil {
			return err
		}
		if a.json {
			return a.writeJSON(st)
		}
		fmt.Fprintf(a.out, `Open this address in a browser of the home network before %s:

  %s

1. Click "Continue on GitHub", then "Create GitHub App".
2. GitHub opens the installation page. Select only the private repositories
   of your apps, and click "Install".
3. Connect an app to its repository with: nida app connect NAME OWNER/REPO
`, clock(st.Expires), st.URL)
		return nil
	case "import":
		fs := flag.NewFlagSet("github import", flag.ContinueOnError)
		id := fs.Int64("app-id", 0, "")
		keyFile := fs.String("key-file", "", "")
		if r, err := parseFlags(fs, rest); err != nil || len(r) > 0 || *id <= 0 || *keyFile == "" {
			return fmt.Errorf("%w: use github import --app-id N --key-file FILE", errUsage)
		}
		key, err := os.ReadFile(*keyFile)
		if err != nil {
			return err
		}
		var v githubView
		if err := a.c.Do(ctx, http.MethodPost, "/api/v1/github/import",
			map[string]any{"app_id": *id, "private_key": string(key)}, &v); err != nil {
			return err
		}
		if a.json {
			return a.writeJSON(v)
		}
		fmt.Fprintf(a.out, "NidaOS uses the GitHub App %s (%d). Select its repositories at %s\n", v.Slug, v.ID, v.InstallURL)
		return nil
	case "remove", "rm":
		if len(rest) != 1 || rest[0] != "--yes" {
			return fmt.Errorf("%w: use github remove --yes", errUsage)
		}
		if err := a.c.Do(ctx, http.MethodDelete, "/api/v1/github", nil, nil); err != nil {
			return err
		}
		a.say("NidaOS does not use the GitHub App any more. Remove it on GitHub in the settings of the app.\n")
		return nil
	}
	return fmt.Errorf("%w: unknown github command %q\n%s", errUsage, cmd, githubUsage)
}

// appConnect connects an app to a private repository and opens the pull
// request with the workflow.
func (a *app) appConnect(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("app connect", flag.ContinueOnError)
	branch := fs.String("branch", "", "")
	noPR := fs.Bool("no-pr", false, "")
	rest, err := parseFlags(fs, args)
	if err != nil || len(rest) != 2 {
		return fmt.Errorf("%w: use app connect NAME OWNER/REPO [--branch B] [--no-pr]", errUsage)
	}
	var res struct {
		Connection runners.Connection `json:"connection"`
		Warning    string             `json:"warning"`
	}
	body := map[string]any{"repository": rest[1], "branch": *branch, "no_pull_request": *noPR}
	if err := a.c.Do(ctx, http.MethodPut, appPath(rest[0], "repository"), body, &res); err != nil {
		return err
	}
	if a.json {
		return a.writeJSON(res)
	}
	c := res.Connection
	fmt.Fprintf(a.out, "The app %s is connected to %s (branch %s).\n", c.App, c.FullName(), c.Branch)
	switch {
	case res.Warning != "":
		fmt.Fprintf(a.out, "Warning: %s\nTry again with: nida app workflow %s\n", res.Warning, c.App)
	case c.PullRequest != "":
		fmt.Fprintf(a.out, "Merge the pull request with the deploy workflow: %s\nThen each push to %s deploys the app.\n", c.PullRequest, c.Branch)
	default:
		fmt.Fprintf(a.out, "Add the deploy workflow to the repository; nida app workflow %s opens a pull request with it.\n", c.App)
	}
	return nil
}

func (a *app) appDisconnect(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: use app disconnect NAME", errUsage)
	}
	if err := a.c.Do(ctx, http.MethodDelete, appPath(args[0], "repository"), nil, nil); err != nil {
		return err
	}
	a.say("The app %s has no repository now. The jobs of its workflow wait with no runner.\n", args[0])
	return nil
}

func (a *app) appWorkflow(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: use app workflow NAME", errUsage)
	}
	var c runners.Connection
	if err := a.c.Do(ctx, http.MethodPost, appPath(args[0], "repository", "pull-request"), nil, &c); err != nil {
		return err
	}
	if a.json {
		return a.writeJSON(c)
	}
	fmt.Fprintf(a.out, "The pull request with the deploy workflow: %s\n", c.PullRequest)
	return nil
}

func jobDuration(j runners.Job) string {
	if j.Started == nil {
		return "-"
	}
	end := time.Now()
	if j.Finished != nil {
		end = *j.Finished
	}
	return end.Sub(*j.Started).Round(time.Second).String()
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return dash(s)
}

func (a *app) appJobs(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: use app jobs NAME", errUsage)
	}
	var body struct {
		Jobs []runners.Job `json:"jobs"`
	}
	if err := a.c.Get(ctx, appPath(args[0], "jobs"), nil, &body); err != nil {
		return err
	}
	if a.json {
		return a.writeJSON(body)
	}
	if len(body.Jobs) == 0 {
		fmt.Fprintf(a.out, "The app %s has no deploy job yet.\n", args[0])
		return nil
	}
	t := a.table()
	fmt.Fprintln(t, "JOB\tSTATE\tCOMMIT\tCREATED\tTIME\tMESSAGE")
	for _, j := range body.Jobs {
		fmt.Fprintf(t, "%d\t%s\t%s\t%s\t%s\t%s\n", j.ID, j.State, shortSHA(j.SHA), stamp(j.Created), jobDuration(j), dash(j.Message))
	}
	if err := t.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "\nThe log of a job: nida app job-log JOB\n")
	return nil
}

func (a *app) appJobLog(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: use app job-log JOB", errUsage)
	}
	if _, err := strconv.ParseInt(args[0], 10, 64); err != nil {
		return fmt.Errorf("%w: the job is a number", errUsage)
	}
	b, err := a.c.Raw(ctx, "/api/v1/jobs/"+args[0]+"/log")
	if err != nil {
		return err
	}
	_, err = a.out.Write(b)
	return err
}

func (a *app) runners(ctx context.Context, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("%w: runners has no arguments", errUsage)
	}
	var v struct {
		GitHub      githubView           `json:"github"`
		Connections []runners.Connection `json:"connections"`
		Active      []runners.Job        `json:"active"`
	}
	if err := a.c.Get(ctx, "/api/v1/runners", nil, &v); err != nil {
		return err
	}
	if a.json {
		return a.writeJSON(v)
	}
	if !v.GitHub.Configured {
		fmt.Fprintln(a.out, "NidaOS has no GitHub App. Make it with: nida github setup")
	} else {
		fmt.Fprintf(a.out, "GitHub App: %s (%s)\n\n", v.GitHub.Name, v.GitHub.Slug)
	}
	t := a.table()
	fmt.Fprintln(t, "APP\tREPOSITORY\tBRANCH\tJOBS NOW\tPROBLEM")
	for _, c := range v.Connections {
		n := 0
		for _, j := range v.Active {
			if j.App == c.App {
				n++
			}
		}
		fmt.Fprintf(t, "%s\t%s\t%s\t%d\t%s\n", c.App, c.FullName(), c.Branch, n, dash(c.Problem))
	}
	return t.Flush()
}

// buildDeploy builds the image of the folder dir with BuildKit, pushes it
// through the job endpoint and deploys it. It works only in a runner pod of
// NidaOS (decision D26).
func (a *app) buildDeploy(ctx context.Context, name, dir, commit, summary string, wait bool) error {
	getenv := a.getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	reg, deployReg, token := getenv("NIDA_REGISTRY"), getenv("NIDA_DEPLOY_REGISTRY"), getenv("NIDA_TOKEN")
	if reg == "" || token == "" {
		return errors.New("--build works only in a runner pod of NidaOS: NIDA_REGISTRY and NIDA_TOKEN are not set")
	}
	if deployReg == "" {
		deployReg = "localhost:5000"
	}
	file := ""
	for _, f := range []string{"Containerfile", "Dockerfile"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			file = f
			break
		}
	}
	if file == "" {
		return fmt.Errorf("the folder %s has no Containerfile and no Dockerfile", dir)
	}
	if commit == "" {
		commit = getenv("GITHUB_SHA")
	}
	tag := time.Now().UTC().Format("20060102-150405")
	if len(commit) >= 12 {
		tag = commit[:12]
	}
	if summary == "" && getenv("GITHUB_RUN_ID") != "" {
		summary = fmt.Sprintf("GitHub run %s/%s/actions/runs/%s", getenv("GITHUB_SERVER_URL"), getenv("GITHUB_REPOSITORY"), getenv("GITHUB_RUN_ID"))
	}

	// buildctl reads the password of the registry from a Docker config.
	cfgDir, err := os.MkdirTemp("", "nida-docker-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(cfgDir)
	auth := base64.StdEncoding.EncodeToString([]byte("job:" + token))
	cfg, _ := json.Marshal(map[string]any{"auths": map[string]any{reg: map[string]string{"auth": auth}}})
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), cfg, 0o600); err != nil {
		return err
	}
	env := append(os.Environ(), "DOCKER_CONFIG="+cfgDir)

	run := a.runCommand
	if runProgram != nil {
		run = runProgram
	}
	// BuildKit starts with the pod: wait until it answers.
	deadline := time.Now().Add(2 * time.Minute)
	for {
		err := run(ctx, env, "buildctl", "debug", "workers")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("BuildKit does not answer: %w", err)
		}
		if err := a.sleep(ctx, 2*time.Second); err != nil {
			return err
		}
	}
	push := reg + "/" + name + ":" + tag
	a.say("nida builds %s from %s/%s.\n", push, dir, file)
	if err := run(ctx, env, "buildctl", "build",
		"--frontend", "dockerfile.v0",
		"--local", "context="+dir, "--local", "dockerfile="+dir,
		"--opt", "filename="+file,
		"--output", "type=image,name="+push+",push=true,registry.insecure=true"); err != nil {
		return fmt.Errorf("the build failed: %w", err)
	}
	image := deployReg + "/" + name + ":" + tag
	var v struct {
		Number int `json:"number"`
	}
	if err := a.c.Do(ctx, http.MethodPost, appPath(name, "deploys"), map[string]string{"image": image, "commit": commit, "summary": summary}, &v); err != nil {
		return err
	}
	return a.afterDeploy(ctx, name, v.Number, wait)
}

// runProgram replaces runCommand in the tests.
var runProgram func(ctx context.Context, env []string, name string, args ...string) error

// runCommand runs a program with its output on the output of nida.
func (a *app) runCommand(ctx context.Context, env []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = a.errOut, a.errOut
	return cmd.Run()
}
