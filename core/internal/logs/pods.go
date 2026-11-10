package logs

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Pods reads the log files of the pods that the kubelet writes:
//
//	/var/log/pods/<namespace>_<pod>_<uid>/<container>/<restart>.log
//
// Each line has the CRI format: "<time> <stdout|stderr> <F|P> <text>".
// P is a part of a long line, F is the last part.
type Pods struct {
	// Dir is the directory of the pod logs.
	Dir string
	// Poll is the time between two examinations of the files.
	Poll   time.Duration
	Logger *slog.Logger

	files map[string]*podFile
}

// podFile is the read state of one log file.
type podFile struct {
	source  Source
	info    os.FileInfo
	offset  int64
	partial bytes.Buffer // the parts of a long line (P)
	seen    bool
}

// DefaultPodPoll is the default time between two examinations.
const DefaultPodPoll = 500 * time.Millisecond

// maxLine is the maximum length of one line. The rest is cut.
const maxLine = 64 * 1024

// Run reads the new lines until ctx stops. The files that exist at the
// start are read from their end. The files that come later are read
// from their start: they are new containers.
func (p *Pods) Run(ctx context.Context, emit func(Line)) {
	poll := p.Poll
	if poll <= 0 {
		poll = DefaultPodPoll
	}
	p.scan(emit, true)
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.scan(emit, false)
		}
	}
}

// scan finds the log files and reads the new data of each file.
func (p *Pods) scan(emit func(Line), first bool) {
	if p.files == nil {
		p.files = map[string]*podFile{}
	}
	paths, _ := filepath.Glob(filepath.Join(p.Dir, "*", "*", "*.log"))
	for _, f := range p.files {
		f.seen = false
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		f, ok := p.files[path]
		if !ok {
			src, ok := podSourceOf(path)
			if !ok {
				continue
			}
			f = &podFile{source: src, info: info}
			if first {
				f.offset = info.Size()
			}
			p.files[path] = f
		}
		f.seen = true
		// The kubelet rotates a file: it moves the file and makes a new
		// one with the same name. Then read the new file from its start.
		if !os.SameFile(f.info, info) || info.Size() < f.offset {
			f.offset = 0
			f.partial.Reset()
		}
		f.info = info
		if info.Size() > f.offset {
			p.read(path, f, emit)
		}
	}
	for path, f := range p.files {
		if !f.seen {
			delete(p.files, path)
		}
	}
}

func (p *Pods) read(path string, f *podFile, emit func(Line)) {
	fh, err := os.Open(path)
	if err != nil {
		return
	}
	defer fh.Close()
	if _, err := fh.Seek(f.offset, io.SeekStart); err != nil {
		return
	}
	r := bufio.NewReaderSize(fh, 64*1024)
	for {
		b, err := r.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			// A line longer than the buffer: keep the first part only.
			f.offset += int64(len(b))
			for err == bufio.ErrBufferFull {
				var more []byte
				more, err = r.ReadSlice('\n')
				f.offset += int64(len(more))
			}
			if err == nil {
				p.emitCRI(f, b, emit)
			}
			continue
		}
		if err != nil {
			// An incomplete last line: read it again at the next scan.
			return
		}
		f.offset += int64(len(b))
		p.emitCRI(f, b, emit)
	}
}

// emitCRI converts one line of the CRI format.
func (p *Pods) emitCRI(f *podFile, b []byte, emit func(Line)) {
	line := strings.TrimRight(string(b), "\r\n")
	ts, rest, ok1 := strings.Cut(line, " ")
	stream, rest, ok2 := strings.Cut(rest, " ")
	tag, text, ok3 := strings.Cut(rest, " ")
	if !ok1 || !ok2 || !ok3 {
		// Not the CRI format: keep the full text.
		ts, stream, tag, text = "", "stdout", "F", line
	}
	if tag == "P" {
		if f.partial.Len() < maxLine {
			f.partial.WriteString(text)
		}
		return
	}
	if f.partial.Len() > 0 {
		f.partial.WriteString(text)
		text = f.partial.String()
		f.partial.Reset()
	}
	if len(text) > maxLine {
		text = text[:maxLine]
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		t = time.Now()
	}
	_ = stream // stderr is not an error: many programs write all their log on stderr.
	emit(Line{
		Time:    t.UTC(),
		Source:  f.source,
		Level:   DetectLevel(text, LevelInfo),
		Message: text,
	})
}

// podSourceOf reads the source from the path of a log file.
func podSourceOf(path string) (Source, bool) {
	container := filepath.Base(filepath.Dir(path))
	parts := strings.Split(filepath.Base(filepath.Dir(filepath.Dir(path))), "_")
	if len(parts) != 3 {
		return Source{}, false
	}
	return PodSource(parts[0], parts[1], container), true
}
