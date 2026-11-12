package installer

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// ExecRunner runs the commands on the computer.
type ExecRunner struct {
	// Line gets each line of the output (standard output and standard
	// error) while the command runs. Nil: no live lines.
	Line func(string)
}

// Streaming tells the engine that the runner gives the lines itself.
func (r ExecRunner) Streaming() bool { return r.Line != nil }

// Run gives the standard output. With Line, it gives the standard output
// and the standard error together.
func (r ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if r.Line == nil {
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			msg := strings.TrimSpace(stderr.String())
			if len(msg) > 400 {
				msg = msg[len(msg)-400:]
			}
			if msg != "" {
				err = fmt.Errorf("%w: %s", err, msg)
			}
		}
		return out, err
	}
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	var all bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			l := sc.Text()
			all.WriteString(l + "\n")
			// The progress of podman and bootc uses carriage returns.
			if i := strings.LastIndex(l, "\r"); i >= 0 {
				l = l[i+1:]
			}
			if strings.TrimSpace(l) != "" {
				r.Line("  " + l)
			}
		}
	}()
	err := cmd.Start()
	if err == nil {
		err = cmd.Wait()
	}
	pw.Close()
	wg.Wait()
	return all.Bytes(), err
}

type streamer interface{ Streaming() bool }
