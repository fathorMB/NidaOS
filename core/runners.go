package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/apps"
	"github.com/fathorMB/NidaOS/core/internal/config"
	"github.com/fathorMB/NidaOS/core/internal/github"
	"github.com/fathorMB/NidaOS/core/internal/httpserver"
	"github.com/fathorMB/NidaOS/core/internal/runners"
)

// runnerPart is the GitHub runner part of the service (milestone M-05).
type runnerPart struct {
	hosting *httpserver.RunnerHosting
	jobs    *runners.JobAPI
	addr    string
}

// startRunners opens the runners file and starts the runner controller. It
// gives nil when the service has no runners. done closes when the
// controller stops.
func startRunners(ctx context.Context, cfg config.Config, appStore *apps.Store, done chan struct{}, logger *slog.Logger) *runnerPart {
	if cfg.RunnersFile == "" || appStore == nil {
		return nil
	}
	store, err := runners.OpenStore(cfg.RunnersFile)
	if err != nil {
		logger.Error("the runners file is not available: the service has no GitHub runners", "err", err)
		return nil
	}
	host, portText, _ := net.SplitHostPort(cfg.JobAddr)
	port, _ := strconv.Atoi(portText)
	jobURL := "http://" + cfg.JobAddr

	regHost := cfg.Registry
	if h, p, err := net.SplitHostPort(regHost); err == nil && h == "localhost" {
		regHost = net.JoinHostPort("127.0.0.1", p)
	}
	regURL, err := url.Parse("http://" + regHost)
	if err != nil {
		logger.Error("the registry address is not valid: the service has no GitHub runners", "err", err)
		return nil
	}

	gh := github.New(cfg.GitHubAPI, cfg.GitHubWeb, store.App)
	tokens := runners.NewTokens()
	exists := func(a string) bool { _, ok := appStore.Get(a); return ok }
	runner := runners.NewRunner(store, gh, tokens, cfg.Kubeconfig, runners.Options{
		RunnerVersion:  cfg.RunnerVersion,
		BuildKitImage:  cfg.BuildKitImage,
		JobURL:         jobURL,
		JobHost:        host,
		JobPort:        int32(port),
		RegistryHost:   cfg.JobAddr,
		DeployRegistry: cfg.Registry,
		AppExists:      exists,
		Logger:         logger,
	})
	go func() {
		runner.Run(ctx)
		close(done)
	}()
	svc := &runners.Service{Store: store, GitHub: gh, AppExists: exists, Controller: runner.Controller}
	return &runnerPart{
		hosting: &httpserver.RunnerHosting{Service: svc, PublicURL: cfg.PublicURL},
		jobs: &runners.JobAPI{Tokens: tokens, WithIdentity: httpserver.WithIdentity, Registry: regURL,
			RegistryName: cfg.Registry, NidaBinary: cfg.NidaBinary},
		addr: cfg.JobAddr,
	}
}

// serveJobs runs the job endpoint until ctx stops. The address is on the
// bridge of the pod network: it is there only after k3s starts its first
// pod, thus serveJobs tries again each 5 seconds.
func (p *runnerPart) serveJobs(ctx context.Context, router http.Handler, logger *slog.Logger) {
	p.jobs.API = router
	srv := &http.Server{
		Handler:           p.jobs,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	logged := false
	for {
		ln, err := net.Listen("tcp", p.addr)
		if err == nil {
			logger.Info("the job endpoint is open", "addr", p.addr)
			go func() {
				<-ctx.Done()
				sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = srv.Shutdown(sctx)
			}()
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("the job endpoint stopped", "err", err)
			}
			return
		}
		if !logged {
			msg := err.Error()
			if strings.Contains(msg, "assign requested address") {
				msg = "the pod network is not there yet"
			}
			logger.Info("the job endpoint waits", "addr", p.addr, "reason", msg)
			logged = true
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}
