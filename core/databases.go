package main

import (
	"context"
	"log/slog"

	"github.com/fathorMB/NidaOS/core/internal/apps"
	"github.com/fathorMB/NidaOS/core/internal/config"
	"github.com/fathorMB/NidaOS/core/internal/databases"
	"github.com/fathorMB/NidaOS/core/internal/httpserver"
	"github.com/fathorMB/NidaOS/core/internal/metrics"
)

// startDatabases opens the databases file and starts the database
// controller. It gives nil when the service has no database hosting. done
// closes when the controller stops.
func startDatabases(ctx context.Context, cfg config.Config, appStore *apps.Store, appRunner *apps.Runner,
	tls func() ([]byte, []byte, error), sampler *metrics.Sampler, done chan struct{}, logger *slog.Logger) *httpserver.DatabaseHosting {
	if cfg.DatabasesFile == "" {
		return nil
	}
	store, err := databases.OpenStore(cfg.DatabasesFile)
	if err != nil {
		logger.Error("the databases file is not available: the service has no database hosting", "err", err)
		return nil
	}
	runner := databases.NewRunner(store, cfg.Kubeconfig, &appLinker{store: appStore, runner: appRunner}, databases.Options{
		Images:    cfg.DatabaseImages,
		TLS:       tls,
		BackupDir: cfg.BackupDir,
		Logger:    logger,
	})
	sampler.Databases = func() []metrics.Database {
		c := runner.Controller()
		if c == nil {
			return nil
		}
		var out []metrics.Database
		for _, l := range c.Live() {
			out = append(out, metrics.Database{Name: l.Name, State: l.State, SizeBytes: l.Stats.SizeBytes,
				Connections: l.Stats.Connections, TransactionsPerSecond: l.TransactionsPerSecond, Observed: l.Observed})
		}
		return out
	}
	go func() {
		runner.Run(ctx)
		close(done)
	}()
	return &httpserver.DatabaseHosting{Runner: runner, Host: cfg.AppsDomain}
}

// appLinker gives the app hosting to the database controller.
type appLinker struct {
	store  *apps.Store
	runner *apps.Runner
}

func (l *appLinker) AppExists(app string) bool {
	_, ok := l.store.Get(app)
	return ok
}

func (l *appLinker) UsedNames(app string) map[string]string { return l.store.UsedNames(app) }

func (l *appLinker) SetLinked(app, source string, values map[string]string) error {
	return l.store.SetLinked(app, source, values)
}

func (l *appLinker) RemoveLinked(app, source string) error { return l.store.RemoveLinked(app, source) }

func (l *appLinker) Namespace(app string) string { return apps.Namespace(app) }

func (l *appLinker) Restart(ctx context.Context, app string) error {
	c := l.runner.Controller()
	if c == nil {
		return nil // the first deploy gives the secrets
	}
	return c.ApplySecrets(ctx, app)
}
