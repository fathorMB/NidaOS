package main

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployBuildPushesThroughTheJobEndpoint(t *testing.T) {
	env, store := startAppService(t)
	ctx := context.Background()
	if code, _, errOut := nida(ctx, env, "app", "add", "blog", "--port", "8080"); code != 0 {
		t.Fatalf("add: %s", errOut)
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "Containerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var calls []string
	var dockerConfig string
	runProgram = func(_ context.Context, e []string, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		for _, kv := range e {
			if dir, ok := strings.CutPrefix(kv, "DOCKER_CONFIG="); ok {
				b, _ := os.ReadFile(filepath.Join(dir, "config.json"))
				dockerConfig = string(b)
			}
		}
		return nil
	}
	t.Cleanup(func() { runProgram = nil })

	// Without the variables of a runner pod, --build stops.
	if code, _, errOut := nida(ctx, env, "app", "deploy", "blog", "--build", src); code != 1 || !strings.Contains(errOut, "runner pod") {
		t.Fatalf("outside a pod: %d %s", code, errOut)
	}

	env["NIDA_REGISTRY"] = "10.42.0.1:8471"
	env["NIDA_DEPLOY_REGISTRY"] = "localhost:5000"
	env["GITHUB_SHA"] = "0123456789abcdef0123456789abcdef01234567"
	env["GITHUB_RUN_ID"] = "99"
	env["GITHUB_REPOSITORY"] = "me/blog"
	env["GITHUB_SERVER_URL"] = "https://github.com"
	code, out, errOut := nida(ctx, env, "app", "deploy", "blog", "--build", src)
	if code != 0 {
		t.Fatalf("deploy: %d %s %s", code, out, errOut)
	}
	if len(calls) != 2 || calls[0] != "buildctl debug workers" {
		t.Fatalf("calls = %q", calls)
	}
	for _, want := range []string{"--local context=" + src, "--opt filename=Containerfile",
		"type=image,name=10.42.0.1:8471/blog:0123456789ab,push=true,registry.insecure=true"} {
		if !strings.Contains(calls[1], want) {
			t.Fatalf("the build has no %q: %s", want, calls[1])
		}
	}
	auth := base64.StdEncoding.EncodeToString([]byte("job:" + env["NIDA_TOKEN"]))
	if !strings.Contains(dockerConfig, `"10.42.0.1:8471":{"auth":"`+auth+`"}`) {
		t.Fatalf("docker config = %s", dockerConfig)
	}
	a, _ := store.Get("blog")
	v, ok := a.Version(1)
	if !ok || v.Image != "localhost:5000/blog:0123456789ab" || v.Commit != env["GITHUB_SHA"] || !strings.Contains(v.Summary, "actions/runs/99") {
		t.Fatalf("version = %+v", v)
	}
}

func TestDeployBuildNeedsAContainerfile(t *testing.T) {
	env, _ := startAppService(t)
	env["NIDA_REGISTRY"] = "10.42.0.1:8471"
	code, _, errOut := nida(context.Background(), env, "app", "deploy", "blog", "--build", t.TempDir())
	if code != 1 || !strings.Contains(errOut, "no Containerfile") {
		t.Fatalf("%d %s", code, errOut)
	}
}
