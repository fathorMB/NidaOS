// Command nida-core is the core service of NidaOS. It gives one API for the
// state of the computer, and the metrics and the logs as a live stream
// (decision D8). The web console, the desktop, the nida command and the
// agents read from it.
//
// The structure comes from GitStack core (decisions D4 and D9): the
// configuration comes from environment variables, the log is JSON on the
// standard output, and the service stops cleanly on SIGINT or SIGTERM.
//
// Commands:
//
//	nida-core               start the service (the same as "serve")
//	nida-core serve         start the service
//	nida-core set-password  set the owner password (from the first line of stdin)
//	nida-core version       print the version
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/apps"
	"github.com/fathorMB/NidaOS/core/internal/auth"
	"github.com/fathorMB/NidaOS/core/internal/ca"
	"github.com/fathorMB/NidaOS/core/internal/config"
	"github.com/fathorMB/NidaOS/core/internal/httpserver"
	"github.com/fathorMB/NidaOS/core/internal/logs"
	"github.com/fathorMB/NidaOS/core/internal/mdns"
	"github.com/fathorMB/NidaOS/core/internal/metrics"
	"github.com/fathorMB/NidaOS/core/internal/system"
	"github.com/fathorMB/NidaOS/core/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}
	if len(args) > 1 {
		fmt.Fprintf(stderr, "nida-core: too many arguments: %v\n", args)
		return 2
	}

	switch cmd {
	case "version":
		fmt.Fprintln(stdout, version.Version)
		return 0
	case "set-password":
		return runSetPassword(os.Stdin, stdout, stderr)
	case "serve":
	default:
		fmt.Fprintf(stderr, "nida-core: unknown command %q (use serve, set-password, mdns-lookup or version)\n", cmd)
		return 2
	}

	cfg, err := config.Load()
	if err != nil {
		slog.New(slog.NewJSONHandler(stderr, nil)).Error("the configuration is not valid", "err", err)
		return 1
	}
	logger := slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: cfg.LogLevel})).
		With("service", "nida-core")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		logger.Error("the service cannot listen", "addr", cfg.Addr, "err", err)
		return 1
	}
	if err := serve(ctx, ln, cfg, logger); err != nil {
		logger.Error("the service stopped with an error", "err", err)
		return 1
	}
	return 0
}

// serve runs the HTTP server on ln until ctx stops. Then it waits for the
// open requests for a maximum of cfg.ShutdownTimeout.
func serve(ctx context.Context, ln net.Listener, cfg config.Config, logger *slog.Logger) error {
	ctx, stopAll := context.WithCancel(ctx)
	defer stopAll()

	interval := cfg.MetricsInterval
	if interval <= 0 {
		interval = config.DefaultMetricsInterval
	}
	sampler := metrics.NewSampler(cfg.Root, cfg.PodLogDir, interval, logger)
	go sampler.Run(ctx)

	// The sign-in data. Without it, the service does not start: it must
	// not give the API without an access check.
	authStore, err := auth.Open(cfg.AuthFile)
	if err != nil {
		return err
	}
	defer authStore.Flush()
	if !authStore.OwnerSet() {
		logger.Warn("the owner password is not set: set it with nida-core set-password")
	}

	// The app hosting (milestone M-03). The controller starts when k3s is
	// ready.
	appsDone := make(chan struct{})
	if cfg.AppsFile != "" {
		appStore, err := apps.OpenStore(cfg.AppsFile)
		if err != nil {
			logger.Error("the apps file is not available: the service has no app hosting", "err", err)
			close(appsDone)
		} else {
			opts := apps.Options{Domain: cfg.AppsDomain, Logger: logger}
			// The certificate authority of the app addresses (D20).
			authority, err := ca.Open(cfg.CADir, cfg.AppsDomain)
			if err != nil {
				logger.Error("the certificate authority is not available: the apps have the default certificate of Traefik", "err", err)
			} else {
				opts.TLS = authority.ServerCert
				logger.Info("the certificate authority is ready", "fingerprint", authority.Fingerprint())
			}
			runner := apps.NewRunner(appStore, cfg.Kubeconfig, opts)
			go func() {
				runner.Run(ctx)
				close(appsDone)
			}()
			// The names of the apps on the home network (D19).
			if cfg.MDNS {
				responder := &mdns.Responder{Names: func() []string { return appNames(appStore, cfg.AppsDomain) }, Logger: logger}
				go func() {
					if err := responder.Run(ctx); err != nil {
						logger.Error("the mDNS responder is not available: the devices cannot find the app names", "err", err)
					}
				}()
			}
		}
	} else {
		close(appsDone)
	}

	pipeline := logs.NewPipeline(cfg.LogBufferLines)
	deps := httpserver.Deps{
		Auth:    authStore,
		Logger:  logger,
		State:   system.NewCollector(cfg.Root, cfg.PlanFile),
		Metrics: sampler.Hub,
		Logs:    pipeline,
	}

	// The log store. Without it, the service keeps the logs only in memory.
	var store *logs.Store
	journalState := ""
	if cfg.LogDir != "" {
		var err error
		store, err = logs.OpenStore(cfg.LogDir, cfg.LogRetention, cfg.LogMaxBytes, logger)
		if err != nil {
			logger.Error("the log store is not available: the logs stay only in memory", "err", err)
		} else {
			pipeline.Sink = store.Append
			deps.LogStore = store
			journalState = filepath.Join(cfg.LogDir, "journal.cursor")
		}
	}

	readers := []logs.Reader{&logs.Pods{
		Dir:    filepath.Join(cfg.Root, filepath.FromSlash(cfg.PodLogDir)),
		Logger: logger,
	}}
	if len(cfg.Journalctl) > 0 {
		readers = append(readers, &logs.Journal{Command: cfg.Journalctl, Logger: logger, StateFile: journalState})
	}
	sourcesDone := make(chan struct{})
	go func() {
		pipeline.RunSources(ctx, readers...)
		close(sourcesDone)
	}()
	// The store closes after the sources: it writes their last lines.
	storeDone := make(chan struct{})
	if store != nil {
		go func() {
			store.Run(sourcesDone)
			close(storeDone)
		}()
	} else {
		close(storeDone)
	}
	defer func() {
		stopAll() // also when the server stops with an error
		<-sourcesDone
		<-storeDone
		<-appsDone
	}()

	router := httpserver.NewRouter(deps)
	newServer := func(h http.Handler) *http.Server {
		return &http.Server{
			Handler:           h,
			ReadHeaderTimeout: 10 * time.Second,
			// No WriteTimeout: the live streams keep the connection open.
			IdleTimeout: 120 * time.Second,
			BaseContext: func(net.Listener) context.Context { return ctx },
		}
	}
	servers := []*http.Server{newServer(router)}
	listeners := []net.Listener{ln}

	// The local socket gives the admin level without a sign-in (D18).
	if cfg.Socket != "" {
		sl, err := listenSocket(cfg.Socket, cfg.SocketGroup, logger)
		if err != nil {
			logger.Error("the local socket is not available", "path", cfg.Socket, "err", err)
		} else {
			defer os.Remove(cfg.Socket)
			servers = append(servers, newServer(httpserver.WithIdentity(router, localIdentity)))
			listeners = append(listeners, sl)
			logger.Info("the local socket is open", "path", cfg.Socket)
		}
	}

	errc := make(chan error, len(servers))
	for i, srv := range servers {
		go func() { errc <- srv.Serve(listeners[i]) }()
	}
	logger.Info("the service started", "addr", ln.Addr().String(), "version", version.Version)

	var serveErr error
	select {
	case serveErr = <-errc:
	case <-ctx.Done():
	}

	logger.Info("the service stops")
	sctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	for _, srv := range servers {
		if err := srv.Shutdown(sctx); err != nil && serveErr == nil {
			serveErr = fmt.Errorf("shutdown: %w", err)
		}
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	logger.Info("the service stopped")
	return nil
}
