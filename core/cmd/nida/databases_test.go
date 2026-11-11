package main

import (
	"context"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/fathorMB/NidaOS/core/internal/auth"
	"github.com/fathorMB/NidaOS/core/internal/databases"
	"github.com/fathorMB/NidaOS/core/internal/httpserver"
)

type okSQL struct{}

func (okSQL) Exec(context.Context, string, string, string) error { return nil }
func (okSQL) Query(context.Context, string, string, string) ([]string, error) {
	return []string{"8192 1 10"}, nil
}

type dumpPod struct{}

func (dumpPod) Exec(_ context.Context, _, _, _ string, _ []string, _ io.Reader, out io.Writer) error {
	_, err := io.WriteString(out, "PGDMP backup")
	return err
}

func startDBService(t *testing.T) (map[string]string, *databases.Store) {
	t.Helper()
	dir := t.TempDir()
	authStore, _ := auth.Open(filepath.Join(dir, "auth.json"))
	_, token, _ := authStore.CreateToken("test", auth.LevelAdmin)
	store, err := databases.OpenStore(filepath.Join(dir, "databases.json"))
	if err != nil {
		t.Fatal(err)
	}
	opts := databases.Options{
		Images:    map[int]string{17: "docker.io/library/postgres:17.7-alpine", 18: "docker.io/library/postgres:18.1-alpine"},
		BackupDir: filepath.Join(dir, "backups"),
	}
	runner := databases.NewRunner(store, "", nil, opts)
	runner.SetController(databases.NewController(store, fake.NewClientset(), dumpPod{}, okSQL{}, opts))
	srv := httptest.NewServer(httpserver.NewRouter(httpserver.Deps{
		Auth:      authStore,
		Databases: &httpserver.DatabaseHosting{Runner: runner, Host: "nidaos.local"},
	}))
	t.Cleanup(srv.Close)
	return map[string]string{"NIDA_SERVER": srv.URL, "NIDA_TOKEN": token}, store
}

func TestDatabaseCommands(t *testing.T) {
	env, store := startDBService(t)
	ctx := context.Background()

	code, out, errOut := nida(ctx, env, "db", "add", "shop-db", "--version", "17", "--tier", "standard", "--size", "2G")
	if code != 0 || !strings.Contains(out, "PostgreSQL 17.7, standard tier") {
		t.Fatalf("add: %d %q %q", code, out, errOut)
	}
	d, _ := store.Get("shop-db")
	if d.SizeBytes != 2<<30 || d.Version != 17 {
		t.Errorf("stored %+v", d)
	}
	_ = store.SetState("shop-db", databases.StateReady, "")

	if code, out, _ = nida(ctx, env, "db", "lan", "shop-db", "on"); code != 0 || !strings.Contains(out, "nidaos.local:15432") {
		t.Errorf("lan on: %d %q", code, out)
	}
	code, out, _ = nida(ctx, env, "db", "connection", "shop-db")
	if code != 0 || !strings.Contains(out, store.OwnerPassword("shop-db")) || !strings.Contains(out, "verify-full") {
		t.Errorf("connection: %d %q", code, out)
	}
	code, out, errOut = nida(ctx, env, "db", "backup", "shop-db", "--wait")
	if code != 0 || !strings.Contains(out, "-manual-pg17.dump is ready") {
		t.Fatalf("backup --wait: %d %q %q", code, out, errOut)
	}
	code, out, _ = nida(ctx, env, "db", "backups", "shop-db")
	if code != 0 || !strings.Contains(out, "manual") || !strings.Contains(out, "--restore shop-db/BACKUP") {
		t.Errorf("backups: %q", out)
	}
	code, out, _ = nida(ctx, env, "db", "list")
	if code != 0 || !strings.Contains(out, "shop-db") || !strings.Contains(out, "17.7") || !strings.Contains(out, "nidaos.local:15432") {
		t.Errorf("list: %q", out)
	}
	code, out, _ = nida(ctx, env, "db", "show", "shop-db")
	if code != 0 || !strings.Contains(out, "update available: 17.7") && !strings.Contains(out, "PostgreSQL 17.7") {
		t.Errorf("show: %q", out)
	}
	if code, _, errOut = nida(ctx, env, "db", "remove", "shop-db"); code != 2 || !strings.Contains(errOut, "--yes") {
		t.Errorf("remove without --yes: %d %q", code, errOut)
	}
	if code, _, errOut = nida(ctx, env, "db", "remove", "shop-db", "--yes"); code != 0 {
		t.Errorf("remove: %d %q", code, errOut)
	}
	if code, _, errOut = nida(ctx, env, "db", "add", "bad", "--version", "16"); code != 1 || !strings.Contains(errOut, "17 and 18") {
		t.Errorf("version 16: %d %q", code, errOut)
	}
}
