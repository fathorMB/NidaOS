package httpserver

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/logs"
)

// LogSearcher searches the log lines on the disk.
type LogSearcher interface {
	Search(q logs.Query) (logs.Result, error)
}

// Limits of the "limit" parameter of the search.
const (
	defaultSearchLimit = 100
	maxSearchLimit     = 1000
)

func (r *Router) addLogSearchRoutes() {
	if r.logStore == nil {
		return
	}
	r.handle(http.MethodGet, "/api/v1/logs/search", r.logsSearch)
}

// logsSearch finds the old lines, the newest first. Parameters: source,
// level and q (as the stream), since and until (a time in RFC 3339, or a
// duration before now, for example "2h" or "7d"), limit, and cursor (the
// next_cursor of the previous page).
func (r *Router) logsSearch(w http.ResponseWriter, req *http.Request) {
	f, _, ok := logQuery(w, req)
	if !ok {
		return
	}
	v := req.URL.Query()
	q := logs.Query{Filter: f, Limit: defaultSearchLimit, Cursor: v.Get("cursor")}

	now := r.now()
	var err error
	if q.Since, err = parseTimeParam(v.Get("since"), now); err != nil {
		writeError(w, http.StatusBadRequest, "bad_since", "The since parameter must be a time (RFC 3339) or a duration (for example 2h or 7d).")
		return
	}
	if q.Until, err = parseTimeParam(v.Get("until"), now); err != nil {
		writeError(w, http.StatusBadRequest, "bad_until", "The until parameter must be a time (RFC 3339) or a duration (for example 2h or 7d).")
		return
	}
	if !q.Since.IsZero() && !q.Until.IsZero() && q.Until.Before(q.Since) {
		writeError(w, http.StatusBadRequest, "bad_range", "The until time is before the since time.")
		return
	}
	if s := v.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > maxSearchLimit {
			writeError(w, http.StatusBadRequest, "bad_limit", "The limit must be a number from 1 to 1000.")
			return
		}
		q.Limit = n
	}

	res, err := r.logStore.Search(q)
	if errors.Is(err, logs.ErrBadCursor) {
		writeError(w, http.StatusBadRequest, "bad_cursor", "The cursor is not valid. Start the search again without a cursor.")
		return
	}
	if err != nil {
		r.logger.Error("the log search failed", "err", err)
		writeError(w, http.StatusInternalServerError, "search_failed", "The search failed.")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// parseTimeParam reads a time in RFC 3339, or a duration before now
// ("90m", "2h", "7d"). An empty value gives the zero time.
func parseTimeParam(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	var d time.Duration
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil || days < 0 {
			return time.Time{}, errors.New("bad duration")
		}
		d = time.Duration(days) * 24 * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil || d < 0 {
			return time.Time{}, errors.New("bad duration")
		}
	}
	return now.Add(-d), nil
}
