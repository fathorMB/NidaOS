// Command nida shows the state, the metrics and the logs of a NidaOS
// computer. It reads them from the core service (nida-core).
//
// On the computer, nida uses the local socket of nida-core and needs no
// password (decision D18). From an other computer, give the address of
// the service with --server and a token in NIDA_TOKEN (decision D17).
//
//	nida status
//	nida metrics [--watch]
//	nida logs [-f] [--since 2h] [--source pod/apps] [--level warning] [-q text] [-n 50]
//	nida token list | create NAME [--level read|admin] | delete ID
//	nida app list | show | add | deploy | rollback | set | secret | move | restart | remove
//	nida ca [--fingerprint]
//	nida db list | show | add | link | unlink | lan | connection | backup | backups | update | remove
//	nida version
//
// Each command accepts --json: the output is the JSON of the API, for
// scripts and agents.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/client"
	"github.com/fathorMB/NidaOS/core/internal/version"
)

const usage = `nida shows the state, the metrics and the logs of a NidaOS computer.

Usage:
  nida [global options] <command> [options]

Commands:
  status                  the computer, the disks, the storage tiers and the workloads
  metrics [--watch]       the last metrics; --watch shows each new snapshot
  logs                    the last log lines; -f follows the new lines
  token list              the tokens of the programs
  token create NAME       make a token (--level read or admin)
  token delete ID         delete a token
  app list                the apps; "nida app" shows all the app commands
  app show NAME           one app, with its versions
  app add NAME            add an app (--port, --health, --env, --folder, ...)
  app deploy NAME IMAGE   deploy an image of the local registry (--wait)
  app rollback NAME N     go back to the version N (--wait)
  app set NAME            change the settings (--port, --env, --copies, ...)
  app secret set NAME KEY add or replace a secret (the value from stdin)
  app move NAME FOLDER T  move a folder to the tier T (performance or standard)
  app remove NAME --yes   remove an app and the files of its folders
  ca [--fingerprint]      the certificate of the NidaOS authority, to install on a device
  db list                 the databases; "nida db" shows all the database commands
  db add NAME             add a PostgreSQL database (--version, --tier, --size, --restore, --wait)
  db link NAME APP        connect an app (--ro for read only)
  db lan NAME on|off      the access from the home network, with TLS
  db backup NAME          make a backup now (--wait); db backups NAME lists them
  db update NAME          the newest minor version, with a backup first (--wait)
  db remove NAME --yes    remove a database and its data (the backups stay)
  version                 the version of nida and of nida-core

Global options:
  --server URL            the address of nida-core, for example http://nida.local:8470
                          (also NIDA_SERVER). Without it, nida uses the local socket.
  --socket PATH           the local socket (also NIDA_SOCKET, default /run/nidaos/core.sock)
  --json                  write the JSON of the API

From an other computer, put a token in NIDA_TOKEN.
`

// app is the state of one run of the command.
type app struct {
	c      *client.Client
	out    io.Writer
	errOut io.Writer
	json   bool
	now    func() time.Time
	// retry is the first wait before a stream connects again.
	retry time.Duration
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// errUsage tells that the arguments are not correct. The exit code is 2.
var errUsage = errors.New("usage")

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	g := flag.NewFlagSet("nida", flag.ContinueOnError)
	g.SetOutput(io.Discard)
	server := g.String("server", getenv("NIDA_SERVER"), "")
	socket := g.String("socket", getenv("NIDA_SOCKET"), "")
	asJSON := g.Bool("json", false, "")
	help := g.Bool("help", false, "")
	g.BoolVar(help, "h", false, "")
	if err := g.Parse(args); err != nil {
		fmt.Fprintf(stderr, "nida: %v\n\n%s", err, usage)
		return 2
	}
	rest := g.Args()
	if *help || len(rest) == 0 {
		fmt.Fprint(stdout, usage)
		if len(rest) == 0 && !*help {
			return 2
		}
		return 0
	}

	a := &app{out: stdout, errOut: stderr, json: *asJSON, now: time.Now, retry: 2 * time.Second}
	if *server != "" {
		a.c = client.NewHTTP(*server, getenv("NIDA_TOKEN"))
	} else {
		path := *socket
		if path == "" {
			path = client.DefaultSocket
		}
		a.c = client.NewSocket(path)
	}

	cmd, cmdArgs := rest[0], rest[1:]
	var err error
	switch cmd {
	case "status":
		err = a.status(ctx, cmdArgs)
	case "metrics":
		err = a.metrics(ctx, cmdArgs)
	case "logs":
		err = a.logs(ctx, cmdArgs)
	case "token", "tokens":
		err = a.token(ctx, cmdArgs)
	case "app", "apps":
		err = a.apps(ctx, cmdArgs)
	case "ca":
		err = a.ca(ctx, cmdArgs)
	case "db", "dbs", "database", "databases":
		err = a.databases(ctx, cmdArgs)
	case "version":
		err = a.version(ctx, cmdArgs)
	case "help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "nida: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	switch {
	case err == nil, errors.Is(err, context.Canceled):
		return 0
	case errors.Is(err, errUsage):
		fmt.Fprintf(stderr, "nida %s: %v\n", cmd, strings.TrimPrefix(err.Error(), "usage: "))
		return 2
	default:
		fmt.Fprintf(stderr, "nida: %v\n", err)
		return 1
	}
}

// parseFlags parses the options of a command. Flags and other arguments
// can come in any order.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var rest []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, fmt.Errorf("%w: %v", errUsage, err)
		}
		args = fs.Args()
		if len(args) == 0 {
			return rest, nil
		}
		rest = append(rest, args[0])
		args = args[1:]
	}
}

func (a *app) version(ctx context.Context, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("%w: version has no arguments", errUsage)
	}
	var h struct {
		Version string `json:"version"`
		Uptime  string `json:"uptime"`
	}
	err := a.c.Get(ctx, "/api/v1/health", nil, &h)
	if a.json {
		return a.writeJSON(map[string]string{"nida": version.Version, "nida_core": h.Version})
	}
	fmt.Fprintf(a.out, "nida       %s\n", version.Version)
	if err != nil {
		fmt.Fprintf(a.out, "nida-core  (not available: %v)\n", err)
		return nil
	}
	fmt.Fprintf(a.out, "nida-core  %s (up %s)\n", h.Version, h.Uptime)
	return nil
}
