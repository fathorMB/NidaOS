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
//	nida-core            start the service (the same as "serve")
//	nida-core serve      start the service
//	nida-core version    print the version
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
	"syscall"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/config"
	"github.com/fathorMB/NidaOS/core/internal/httpserver"
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
	case "serve":
	default:
		fmt.Fprintf(stderr, "nida-core: unknown command %q (use serve or version)\n", cmd)
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
	interval := cfg.MetricsInterval
	if interval <= 0 {
		interval = config.DefaultMetricsInterval
	}
	sampler := metrics.NewSampler(cfg.Root, cfg.PodLogDir, interval, logger)
	go sampler.Run(ctx)

	srv := &http.Server{
		Handler: httpserver.NewRouter(httpserver.Deps{
			Logger:  logger,
			State:   system.NewCollector(cfg.Root, cfg.PlanFile),
			Metrics: sampler.Hub,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: the live streams keep the connection open.
		IdleTimeout: 120 * time.Second,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	logger.Info("the service started", "addr", ln.Addr().String(), "version", version.Version)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	logger.Info("the service stops")
	sctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	logger.Info("the service stopped")
	return nil
}
