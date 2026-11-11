package logs

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Store keeps the log lines on the disk for the retention time and gives
// a search in them.
//
// The store has one file for each day (UTC), with one JSON object on each
// line: "2026-11-10.jsonl". The day of a file is the day when the service
// received the line. At the end of a day, the store compresses the file
// with gzip ("2026-11-10.jsonl.gz"). A file is only appended to, thus
// the position of a line in its file does not change. A search cursor
// uses this position.
type Store struct {
	Dir       string
	Retention time.Duration
	// MaxBytes is the maximum size of all the files. The store deletes the
	// oldest days first. 0 is no limit.
	MaxBytes int64
	Logger   *slog.Logger
	Now      func() time.Time

	mu     sync.Mutex
	day    string // the day of the open file
	file   *os.File
	buf    *bufio.Writer
	enc    *json.Encoder
	dirty  bool
	closed bool

	// compressMu lets only one compression run at a time.
	compressMu sync.Mutex
}

// Defaults of the store.
const (
	DefaultRetention = 30 * 24 * time.Hour
	DefaultMaxBytes  = 10 << 30 // 10 GiB
	// flushInterval is the maximum time that a line stays in the memory
	// buffer of the file.
	flushInterval = time.Second
	// maintainInterval is the time between two runs of the retention.
	maintainInterval = time.Hour
	dayLayout        = "2006-01-02"
	plainExt         = ".jsonl"
	gzipExt          = ".jsonl.gz"
)

// OpenStore makes the directory and opens the file of today.
func OpenStore(dir string, retention time.Duration, maxBytes int64, logger *slog.Logger) (*Store, error) {
	if retention <= 0 {
		retention = DefaultRetention
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("log store: %w", err)
	}
	s := &Store{Dir: dir, Retention: retention, MaxBytes: maxBytes, Logger: logger, Now: time.Now}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.openDay(s.today()); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) today() string {
	return s.Now().UTC().Format(dayLayout)
}

// openDay opens the file of day for append. The caller holds s.mu.
func (s *Store) openDay(day string) error {
	path := filepath.Join(s.Dir, day+plainExt)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return fmt.Errorf("log store: %w", err)
	}
	s.day, s.file, s.buf = day, f, bufio.NewWriterSize(f, 64*1024)
	s.enc = json.NewEncoder(s.buf)
	// Keep "<", ">" and "&" as text, thus the fast text test of the
	// search finds them.
	s.enc.SetEscapeHTML(false)
	return nil
}

// Append writes one line. The pipeline calls it for each line. An error
// goes to the service log: a full disk must not stop the live stream.
func (s *Store) Append(l Line) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if day := s.today(); day != s.day {
		s.rotate(day)
	}
	if s.buf == nil {
		return
	}
	l.Seq = 0 // the numbers are only for the live stream
	// Encode writes one JSON object and a new line.
	if err := s.enc.Encode(l); err != nil {
		s.Logger.Error("the log store cannot write", "err", err)
		return
	}
	s.dirty = true
}

// rotate closes the file of the old day, compresses it, and opens the
// file of the new day. The caller holds s.mu.
func (s *Store) rotate(day string) {
	old := s.day
	s.closeFile()
	if err := s.openDay(day); err != nil {
		s.Logger.Error("the log store cannot open the file of the new day", "err", err)
	}
	go s.compress(old)
}

func (s *Store) closeFile() {
	if s.buf != nil {
		_ = s.buf.Flush()
	}
	if s.file != nil {
		_ = s.file.Close()
	}
	s.file, s.buf, s.enc, s.dirty = nil, nil, nil, false
}

// Flush writes the buffered lines to the disk.
func (s *Store) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.buf != nil && s.dirty {
		if err := s.buf.Flush(); err != nil {
			s.Logger.Error("the log store cannot write", "err", err)
		}
		s.dirty = false
	}
}

// Close writes the buffered lines and closes the file.
func (s *Store) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeFile()
	s.closed = true
}

// Run flushes the buffer each second and applies the retention each hour,
// until done closes. Then it closes the store.
func (s *Store) Run(done <-chan struct{}) {
	s.Maintain()
	flush := time.NewTicker(flushInterval)
	defer flush.Stop()
	maintain := time.NewTicker(maintainInterval)
	defer maintain.Stop()
	for {
		select {
		case <-done:
			s.Close()
			return
		case <-flush.C:
			s.Flush()
		case <-maintain.C:
			s.Maintain()
		}
	}
}

// dayFile is one file of the store.
type dayFile struct {
	day  string
	path string
	gz   bool
	size int64
}

// files gives the files of the store, the newest day first. When a day
// has the two forms (a compression stopped), the plain file is kept.
func (s *Store) files() []dayFile {
	entries, _ := os.ReadDir(s.Dir)
	byDay := map[string]dayFile{}
	for _, e := range entries {
		name := e.Name()
		var f dayFile
		switch {
		case strings.HasSuffix(name, gzipExt):
			f = dayFile{day: strings.TrimSuffix(name, gzipExt), gz: true}
		case strings.HasSuffix(name, plainExt):
			f = dayFile{day: strings.TrimSuffix(name, plainExt)}
		default:
			continue
		}
		if _, err := time.Parse(dayLayout, f.day); err != nil {
			continue
		}
		f.path = filepath.Join(s.Dir, name)
		if info, err := e.Info(); err == nil {
			f.size = info.Size()
		}
		if prev, ok := byDay[f.day]; ok && !prev.gz {
			continue
		}
		byDay[f.day] = f
	}
	out := make([]dayFile, 0, len(byDay))
	for _, f := range byDay {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].day > out[j].day })
	return out
}

// Maintain compresses the old plain files, deletes the days that are
// older than the retention, and deletes the oldest days when the files
// are larger than MaxBytes. It never deletes the file of today.
func (s *Store) Maintain() {
	s.mu.Lock()
	today := s.day
	s.mu.Unlock()

	for _, f := range s.files() {
		if !f.gz && f.day < today {
			s.compress(f.day)
		}
	}

	oldest := s.Now().UTC().Add(-s.Retention).Format(dayLayout)
	var total int64
	for _, f := range s.files() {
		total += f.size
		tooOld := f.day < oldest
		tooBig := s.MaxBytes > 0 && total > s.MaxBytes
		if f.day != today && (tooOld || tooBig) {
			if err := os.Remove(f.path); err == nil {
				s.Logger.Info("the log store deleted an old day", "day", f.day, "too_old", tooOld, "too_big", tooBig)
			}
		}
	}
}

// compress writes the file of day with gzip, then deletes the plain file.
func (s *Store) compress(day string) {
	s.compressMu.Lock()
	defer s.compressMu.Unlock()
	src := filepath.Join(s.Dir, day+plainExt)
	dst := filepath.Join(s.Dir, day+gzipExt)
	in, err := os.Open(src)
	if err != nil {
		return
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		s.Logger.Error("the log store cannot compress a day", "day", day, "err", err)
		return
	}
	zw := gzip.NewWriter(out)
	_, err = io.Copy(zw, in)
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
		s.Logger.Error("the log store cannot compress a day", "day", day, "err", err)
		return
	}
	in.Close()
	os.Remove(src)
}

// Query is a search in the store.
type Query struct {
	Filter
	// Since and Until limit the time of the lines. A zero value is no limit.
	Since, Until time.Time
	// Limit is the maximum number of lines.
	Limit int
	// Cursor continues a search: the store gives the lines before it.
	Cursor string
}

// Result is one page of a search, the newest line first.
type Result struct {
	Lines []Line `json:"lines"`
	// NextCursor gives the next page (older lines). It is empty at the end.
	NextCursor string `json:"next_cursor,omitempty"`
	// Partial is true when the search stopped at the time limit.
	Partial bool `json:"partial,omitempty"`
}

// ErrBadCursor tells that a cursor is not valid.
var ErrBadCursor = errors.New("the cursor is not valid")

// searchTimeLimit stops a search that reads too many files.
const searchTimeLimit = 10 * time.Second

// Search finds the lines of q, the newest first.
func (s *Store) Search(q Query) (Result, error) {
	if q.Limit <= 0 {
		q.Limit = 100
	}
	curDay, curLine := "", -1
	if q.Cursor != "" {
		d, n, ok := strings.Cut(q.Cursor, ":")
		pos, err := strconv.Atoi(n)
		if _, perr := time.Parse(dayLayout, d); !ok || err != nil || perr != nil || pos < 0 {
			return Result{}, ErrBadCursor
		}
		curDay, curLine = d, pos
	}
	s.Flush()

	// The day of a file is the day when the line came. A line can be
	// some time older than its file, thus keep one more day.
	firstDay, lastDay := "", ""
	if !q.Since.IsZero() {
		firstDay = q.Since.UTC().Add(-24 * time.Hour).Format(dayLayout)
	}
	if !q.Until.IsZero() {
		lastDay = q.Until.UTC().Add(24 * time.Hour).Format(dayLayout)
	}

	deadline := time.Now().Add(searchTimeLimit)
	res := Result{Lines: []Line{}}
	for _, f := range s.files() {
		if (lastDay != "" && f.day > lastDay) || (curDay != "" && f.day > curDay) {
			continue
		}
		if firstDay != "" && f.day < firstDay {
			break
		}
		before := -1 // all the lines of the file
		if f.day == curDay {
			before = curLine
		}
		need := q.Limit - len(res.Lines)
		lines, positions, err := scanDay(f, q, before, need)
		if err != nil {
			s.Logger.Warn("the log store cannot read a day", "day", f.day, "err", err)
			continue
		}
		res.Lines = append(res.Lines, lines...)
		if len(res.Lines) >= q.Limit {
			res.NextCursor = f.day + ":" + strconv.Itoa(positions[len(positions)-1])
			return res, nil
		}
		if time.Now().After(deadline) {
			res.Partial = true
			res.NextCursor = f.day + ":0"
			return res, nil
		}
	}
	return res, nil
}

// scanDay reads one file and gives the last need lines of q before the
// position before (-1: no limit), the newest first, with their positions.
func scanDay(f dayFile, q Query, before, need int) ([]Line, []int, error) {
	fh, err := os.Open(f.path)
	if err != nil {
		return nil, nil, err
	}
	defer fh.Close()
	var r io.Reader = fh
	if f.gz {
		zr, err := gzip.NewReader(fh)
		if err != nil {
			return nil, nil, err
		}
		defer zr.Close()
		r = zr
	}

	// Keep only the last need matches: a ring of the newest lines.
	type hit struct {
		line Line
		pos  int
	}
	ring := make([]hit, 0, need)
	next := 0
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 2*maxLine)
	for pos := 0; sc.Scan(); pos++ {
		if before >= 0 && pos >= before {
			break
		}
		b := sc.Bytes()
		// A fast test before the JSON decode.
		if q.Text != "" && !containsFold(b, q.Text) {
			continue
		}
		var l Line
		if json.Unmarshal(b, &l) != nil {
			continue
		}
		if !q.Match(l) || (!q.Since.IsZero() && l.Time.Before(q.Since)) || (!q.Until.IsZero() && l.Time.After(q.Until)) {
			continue
		}
		h := hit{line: l, pos: pos}
		if len(ring) < need {
			ring = append(ring, h)
		} else {
			ring[next] = h
			next = (next + 1) % need
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, nil, err
	}
	lines := make([]Line, 0, len(ring))
	positions := make([]int, 0, len(ring))
	for i := range ring {
		// From the newest to the oldest.
		h := ring[(next-1-i+2*len(ring))%len(ring)]
		lines = append(lines, h.line)
		positions = append(positions, h.pos)
	}
	return lines, positions, nil
}

func containsFold(b []byte, s string) bool {
	return strings.Contains(strings.ToLower(string(b)), strings.ToLower(s))
}
