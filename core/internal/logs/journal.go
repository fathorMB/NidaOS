package logs

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
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
	// StateFile keeps the cursor of the last entry. After a restart of the
	// service, the reader continues after this entry, thus the store gets
	// the lines of the stop time too. Empty: the reader starts with the
	// new lines.
	StateFile string
	// open starts the reader after the cursor. The tests replace it.
	open func(ctx context.Context, cursor string) (io.ReadCloser, func() error, error)

	cursor    string
	savedAt   time.Time
	savedCurs string
}

// journalRetry is the wait before journalctl starts again after a stop.
const journalRetry = 5 * time.Second

// cursorSaveInterval is the maximum time between two writes of the cursor.
const cursorSaveInterval = 2 * time.Second

// Run reads the new lines of the journal until ctx stops. If journalctl
// stops, Run starts it again.
func (j *Journal) Run(ctx context.Context, emit func(Line)) {
	open := j.open
	if open == nil {
		open = j.exec
	}
	j.loadCursor()
	defer j.saveCursor(true)
	for ctx.Err() == nil {
		start := j.cursor
		r, wait, err := open(ctx, start)
		if err != nil {
			j.Logger.Warn("the journal reader cannot start", "err", err)
		} else {
			n := readJournal(r, emit, j.setCursor)
			r.Close()
			if err := wait(); err != nil && ctx.Err() == nil {
				j.Logger.Warn("the journal reader stopped", "err", err)
				// journalctl stops at once when the journal does not have
				// the cursor any more (for example after a rotation).
				// Then start again with the new lines.
				if n == 0 && start != "" {
					j.Logger.Warn("the journal cursor is not valid: the reader starts with the new lines")
					j.cursor = ""
				}
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(journalRetry):
		}
	}
}

func (j *Journal) exec(ctx context.Context, cursor string) (io.ReadCloser, func() error, error) {
	cmd := j.Command
	if len(cmd) == 0 {
		cmd = []string{"journalctl"}
	}
	args := append(append([]string{}, cmd[1:]...), "--follow", "--output=json", "--no-pager")
	if cursor != "" {
		args = append(args, "--after-cursor="+cursor)
	} else {
		// Only the new lines. The older lines are in the store.
		args = append(args, "--lines=0")
	}
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

// readJournal reads the JSON lines of journalctl until the end of r. It
// gives the cursor of each entry to cursor, and the number of entries.
func readJournal(r io.Reader, emit func(Line), cursor func(string)) int {
	sc := bufio.NewScanner(r)
	// A journal entry can be long, for example a stack trace.
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	n := 0
	for sc.Scan() {
		l, c, ok := parseJournal(sc.Bytes())
		if c != "" {
			n++
		}
		if ok {
			emit(l)
		}
		if c != "" && cursor != nil {
			cursor(c)
		}
	}
	return n
}

// parseJournal converts one JSON entry of journalctl. It also gives the
// cursor of the entry. ok is false for an entry without a message.
func parseJournal(b []byte) (l Line, cursor string, ok bool) {
	var e map[string]json.RawMessage
	if err := json.Unmarshal(b, &e); err != nil {
		return Line{}, "", false
	}
	cursor = journalString(e["__CURSOR"])
	msg := journalString(e["MESSAGE"])
	if msg == "" {
		return Line{}, cursor, false
	}
	l = Line{Message: strings.TrimRight(msg, "\n"), Time: time.Now().UTC()}
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
	return l, cursor, true
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

// setCursor keeps the cursor of the last entry and writes it to the state
// file at most each cursorSaveInterval.
func (j *Journal) setCursor(c string) {
	j.cursor = c
	j.saveCursor(false)
}

// loadCursor reads the state file. A missing file gives no cursor.
func (j *Journal) loadCursor() {
	if j.StateFile == "" {
		return
	}
	b, err := os.ReadFile(j.StateFile)
	if err != nil {
		return
	}
	j.cursor = strings.TrimSpace(string(b))
	j.savedCurs = j.cursor
}

// saveCursor writes the cursor to the state file. Without force, it writes
// only when the last write is older than cursorSaveInterval.
func (j *Journal) saveCursor(force bool) {
	if j.StateFile == "" || j.cursor == j.savedCurs {
		return
	}
	if !force && time.Since(j.savedAt) < cursorSaveInterval {
		return
	}
	// Write a new file, then rename it: a stop during the write does not
	// leave half a cursor.
	tmp := j.StateFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(j.cursor+"\n"), 0o640); err != nil {
		j.Logger.Warn("the journal reader cannot save its cursor", "err", err)
		return
	}
	if err := os.Rename(tmp, j.StateFile); err != nil {
		j.Logger.Warn("the journal reader cannot save its cursor", "err", err)
		return
	}
	j.savedAt, j.savedCurs = time.Now(), j.cursor
}
