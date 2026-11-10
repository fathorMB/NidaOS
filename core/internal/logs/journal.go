package logs

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Journal reads the systemd journal with journalctl. The binary format of
// the journal is internal to systemd, thus the service uses the JSON
// output of journalctl and does not read the files.
type Journal struct {
	// Command is the program and its first arguments. The default is
	// "journalctl".
	Command []string
	Logger  *slog.Logger
	// open starts the reader. The tests replace it.
	open func(ctx context.Context) (io.ReadCloser, func() error, error)
}

// journalRetry is the wait before journalctl starts again after a stop.
const journalRetry = 5 * time.Second

// Run reads the new lines of the journal until ctx stops. If journalctl
// stops, Run starts it again.
func (j *Journal) Run(ctx context.Context, emit func(Line)) {
	open := j.open
	if open == nil {
		open = j.exec
	}
	for ctx.Err() == nil {
		r, wait, err := open(ctx)
		if err != nil {
			j.Logger.Warn("the journal reader cannot start", "err", err)
		} else {
			readJournal(r, emit)
			r.Close()
			if err := wait(); err != nil && ctx.Err() == nil {
				j.Logger.Warn("the journal reader stopped", "err", err)
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(journalRetry):
		}
	}
}

func (j *Journal) exec(ctx context.Context) (io.ReadCloser, func() error, error) {
	cmd := j.Command
	if len(cmd) == 0 {
		cmd = []string{"journalctl"}
	}
	// --lines=0: only the new lines. The history is in the store (T-05).
	args := append(append([]string{}, cmd[1:]...), "--follow", "--output=json", "--lines=0", "--no-pager")
	c := exec.CommandContext(ctx, cmd[0], args...)
	out, err := c.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := c.Start(); err != nil {
		return nil, nil, err
	}
	return out, c.Wait, nil
}

// readJournal reads the JSON lines of journalctl until the end of r.
func readJournal(r io.Reader, emit func(Line)) {
	sc := bufio.NewScanner(r)
	// A journal entry can be long, for example a stack trace.
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		if l, ok := parseJournal(sc.Bytes()); ok {
			emit(l)
		}
	}
}

// parseJournal converts one JSON entry of journalctl.
func parseJournal(b []byte) (Line, bool) {
	var e map[string]json.RawMessage
	if err := json.Unmarshal(b, &e); err != nil {
		return Line{}, false
	}
	msg := journalString(e["MESSAGE"])
	if msg == "" {
		return Line{}, false
	}
	l := Line{Message: strings.TrimRight(msg, "\n"), Time: time.Now().UTC()}
	if us, err := strconv.ParseInt(journalString(e["__REALTIME_TIMESTAMP"]), 10, 64); err == nil {
		l.Time = time.UnixMicro(us).UTC()
	}

	// The unit is the best name. A program without a unit (for example
	// the kernel) uses its identifier.
	name := strings.TrimSuffix(journalString(e["_SYSTEMD_UNIT"]), ".service")
	if name == "" || strings.HasSuffix(name, ".scope") || strings.HasSuffix(name, ".slice") {
		if id := journalString(e["SYSLOG_IDENTIFIER"]); id != "" {
			name = id
		}
	}
	if name == "" {
		name = "system"
	}
	l.Source = ServiceSource(name)

	// PRIORITY: 0 to 3 are errors, 4 is a warning, 5 and 6 are info, 7 is debug.
	l.Level = LevelInfo
	if p, err := strconv.Atoi(journalString(e["PRIORITY"])); err == nil {
		switch {
		case p <= 3:
			l.Level = LevelError
		case p == 4:
			l.Level = LevelWarning
		case p == 7:
			l.Level = LevelDebug
		}
	}
	// A program that writes all its lines with the same priority can give
	// the level in the text, for example the JSON log of nida-core.
	if l.Level == LevelInfo {
		l.Level = DetectLevel(l.Message, LevelInfo)
	}
	return l, true
}

// journalString reads a field of journalctl. A field is a string, or an
// array of bytes when the value is not valid UTF-8.
func journalString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var bs []byte
	var ints []int
	if json.Unmarshal(raw, &ints) == nil {
		bs = make([]byte, len(ints))
		for i, v := range ints {
			bs[i] = byte(v)
		}
		return strings.ToValidUTF8(string(bs), "�")
	}
	return ""
}
