package databases

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// Executor runs a command in the container of the database pod.
type Executor interface {
	// Exec runs cmd with stdin, and writes the output to stdout.
	Exec(ctx context.Context, namespace, pod, container string, cmd []string, stdin io.Reader, stdout io.Writer) error
}

// PodExecutor is the real Executor: the "exec" of the Kubernetes API.
type PodExecutor struct {
	Config *rest.Config
	Client kubernetes.Interface
}

// Exec implements Executor. The output goes to stdout without a copy in
// memory, because a backup can be large.
func (e *PodExecutor) Exec(ctx context.Context, namespace, pod, container string, cmd []string, stdin io.Reader, stdout io.Writer) error {
	req := e.Client.CoreV1().RESTClient().Post().Resource("pods").Namespace(namespace).Name(pod).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{Container: container, Command: cmd, Stdin: stdin != nil, Stdout: true, Stderr: true}, scheme.ParameterCodec)
	ex, err := remotecommand.NewSPDYExecutor(e.Config, http.MethodPost, req.URL())
	if err != nil {
		return err
	}
	var errb bytes.Buffer
	if err := ex.StreamWithContext(ctx, remotecommand.StreamOptions{Stdin: stdin, Stdout: stdout, Stderr: &errb}); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(errb.String()))
	}
	return nil
}

// SQLRunner runs SQL as the superuser of the controller in a database.
type SQLRunner interface {
	// Exec runs one or more statements, without a result.
	Exec(ctx context.Context, db, database, sql string) error
	// Query runs one statement and gives the first column of each row.
	Query(ctx context.Context, db, database, sql string) ([]string, error)
}

// Psql is the real SQLRunner: psql in the pod, on the local socket. The
// SQL (with the passwords) goes through stdin, never on the command line.
// The errors do not show the known secrets.
type Psql struct {
	Pod Executor
	// Redact gives the values to remove from the errors.
	Redact func() []string
}

func (p *Psql) run(ctx context.Context, db, database, sql string, tuples bool) (string, error) {
	cmd := []string{"psql", "-X", "-q", "-v", "ON_ERROR_STOP=1", "-h", "/var/run/postgresql", "-U", AdminRole, "-d", database, "-f", "-"}
	if tuples {
		cmd = append(cmd, "-t", "-A")
	}
	var out bytes.Buffer
	if err := p.Pod.Exec(ctx, Namespace(db), PodName, WorkloadName, cmd, strings.NewReader(sql), &out); err != nil {
		return "", errors.New(p.scrub(err.Error()))
	}
	return out.String(), nil
}

func (p *Psql) scrub(s string) string {
	if p.Redact != nil {
		for _, v := range p.Redact() {
			if v != "" {
				s = strings.ReplaceAll(s, v, "***")
			}
		}
	}
	return s
}

// Exec implements SQLRunner.
func (p *Psql) Exec(ctx context.Context, db, database, sql string) error {
	_, err := p.run(ctx, db, database, sql, false)
	return err
}

// Query implements SQLRunner.
func (p *Psql) Query(ctx context.Context, db, database, sql string) ([]string, error) {
	out, err := p.run(ctx, db, database, sql, true)
	if err != nil {
		return nil, err
	}
	var rows []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimRight(l, "\r"); l != "" {
			rows = append(rows, l)
		}
	}
	return rows, nil
}
