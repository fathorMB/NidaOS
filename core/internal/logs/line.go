// Package logs collects the log lines of the computer and of each workload
// and sends them as a live stream (decision D8).
//
// Two sources give the lines:
//   - the systemd journal, for the services of the host (journal.go);
//   - the log files of the kubelet in /var/log/pods, for the pods (pods.go).
//
// Each line has its time, its source and its level. The Pipeline gives
// each line a sequence number, keeps the last lines in memory and sends
// each line to all the clients.
package logs

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// Levels. The order is important: a filter "warning" keeps warning and error.
const (
	LevelDebug   = "debug"
	LevelInfo    = "info"
	LevelWarning = "warning"
	LevelError   = "error"
)

// levelRank gives the order of the levels.
var levelRank = map[string]int{LevelDebug: 0, LevelInfo: 1, LevelWarning: 2, LevelError: 3}

// ParseLevel gives the level for a name. It accepts the usual synonyms,
// for example "warn" or "fatal". ok is false for an unknown name.
func ParseLevel(s string) (level string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug", "trace", "dbg", "verbose":
		return LevelDebug, true
	case "info", "information", "notice", "inf":
		return LevelInfo, true
	case "warning", "warn", "wrn":
		return LevelWarning, true
	case "error", "err", "fatal", "critical", "crit", "panic", "alert", "emerg", "emergency":
		return LevelError, true
	}
	return "", false
}

// AtLeast tells if level is the same as min or more important.
func AtLeast(level, min string) bool {
	return levelRank[level] >= levelRank[min]
}

// Source kinds.
const (
	// SourceService is a unit or a program of the systemd journal.
	SourceService = "service"
	// SourcePod is a container of a pod of k3s.
	SourcePod = "pod"
)

// Source tells where a line comes from.
type Source struct {
	Kind string `json:"kind"`
	// ID is the full name of the source, for example "service/k3s" or
	// "pod/apps/photos-7d9f/web". A filter uses a prefix of it.
	ID string `json:"id"`
	// Name is the short name for the interfaces: the unit, or the pod.
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Container string `json:"container,omitempty"`
}

// ServiceSource makes the source of a journal unit or program.
func ServiceSource(name string) Source {
	return Source{Kind: SourceService, ID: SourceService + "/" + name, Name: name}
}

// PodSource makes the source of a container of a pod.
func PodSource(namespace, pod, container string) Source {
	return Source{
		Kind:      SourcePod,
		ID:        SourcePod + "/" + namespace + "/" + pod + "/" + container,
		Name:      pod,
		Namespace: namespace,
		Container: container,
	}
}

// Line is one log line.
type Line struct {
	// Seq is the sequence number. It increases by one for each line of
	// the service. A stream uses it as the event id.
	Seq     uint64    `json:"seq"`
	Time    time.Time `json:"time"`
	Source  Source    `json:"source"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

// Patterns for the level in the text of a line.
var (
	// level=warn, "level":"error", severity: ERROR, lvl=info
	levelField = regexp.MustCompile(`(?i)\b(?:level|lvl|severity|loglevel)["']?\s*[:=]\s*["']?([a-z]+)`)
	// [ERROR], ERROR:, <warn>, E1110 (klog), and a word at the start.
	levelPrefix = regexp.MustCompile(`(?i)^\s*(?:\d{4}[-/]\d{2}[-/]\d{2}[T ][\d:.,]+Z?\s*)?[\[<(]?(debug|trace|info|notice|warn|warning|error|err|fatal|critical|panic)[\]>):]?\s`)
	klogPrefix  = regexp.MustCompile(`^([IWEF])\d{4} `)
)

// DetectLevel finds the level in the text of a line. It gives def when the
// text has no level.
func DetectLevel(msg, def string) string {
	// A JSON line: read the usual fields.
	if strings.HasPrefix(msg, "{") {
		var m map[string]any
		if json.Unmarshal([]byte(msg), &m) == nil {
			for _, k := range []string{"level", "lvl", "severity", "log.level", "levelname"} {
				if s, ok := m[k].(string); ok {
					if l, ok := ParseLevel(s); ok {
						return l
					}
				}
			}
		}
	}
	if m := klogPrefix.FindStringSubmatch(msg); m != nil {
		return map[string]string{"I": LevelInfo, "W": LevelWarning, "E": LevelError, "F": LevelError}[m[1]]
	}
	if m := levelField.FindStringSubmatch(msg); m != nil {
		if l, ok := ParseLevel(m[1]); ok {
			return l
		}
	}
	if m := levelPrefix.FindStringSubmatch(msg); m != nil {
		if l, ok := ParseLevel(m[1]); ok {
			return l
		}
	}
	return def
}

// Filter selects lines. An empty field selects all lines.
type Filter struct {
	// Source is a prefix of Source.ID, for example "pod/apps".
	Source string
	// Level is the minimum level.
	Level string
	// Text must be in the message. The comparison ignores the case.
	Text string
}

// Match tells if the filter selects the line.
func (f Filter) Match(l Line) bool {
	if f.Source != "" && !matchSource(l.Source.ID, f.Source) {
		return false
	}
	if f.Level != "" && !AtLeast(l.Level, f.Level) {
		return false
	}
	if f.Text != "" && !strings.Contains(strings.ToLower(l.Message), strings.ToLower(f.Text)) {
		return false
	}
	return true
}

// matchSource tells if id is the source prefix or is below it. Thus
// "service/k3s" does not select "service/k3s-agent".
func matchSource(id, prefix string) bool {
	prefix = strings.TrimSuffix(prefix, "/")
	return id == prefix || strings.HasPrefix(id, prefix+"/")
}
