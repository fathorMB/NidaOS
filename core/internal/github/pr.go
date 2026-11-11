package github

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// FileChange is one file that a pull request adds or replaces.
type FileChange struct {
	Path    string
	Content []byte
}

// PullRequest is a pull request.
type PullRequest struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	State   string `json:"state"`
}

// OpenPullRequest puts the file on the branch head, made from base, and
// opens a pull request to base. If an open pull request from head is there
// already, it updates the file and gives that pull request.
func (r *Repo) OpenPullRequest(ctx context.Context, base, head, title, body, message string, f FileChange) (PullRequest, error) {
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := r.do(ctx, http.MethodGet, "/git/ref/heads/"+escapeRef(base), nil, &ref); err != nil {
		return PullRequest{}, err
	}
	err := r.do(ctx, http.MethodGet, "/git/ref/heads/"+escapeRef(head), nil, nil)
	if errors.Is(err, ErrNotFound) {
		err = r.do(ctx, http.MethodPost, "/git/refs", map[string]string{"ref": "refs/heads/" + head, "sha": ref.Object.SHA}, nil)
	}
	if err != nil {
		return PullRequest{}, err
	}

	put := map[string]string{
		"message": message,
		"content": base64.StdEncoding.EncodeToString(f.Content),
		"branch":  head,
	}
	var cur struct {
		SHA string `json:"sha"`
	}
	err = r.do(ctx, http.MethodGet, "/contents/"+escapePath(f.Path)+"?ref="+url.QueryEscape(head), nil, &cur)
	switch {
	case err == nil:
		put["sha"] = cur.SHA
	case !errors.Is(err, ErrNotFound):
		return PullRequest{}, err
	}
	if err := r.do(ctx, http.MethodPut, "/contents/"+escapePath(f.Path), put, nil); err != nil {
		return PullRequest{}, err
	}

	var open []PullRequest
	q := "?state=open&head=" + url.QueryEscape(r.Owner+":"+head) + "&base=" + url.QueryEscape(base)
	if err := r.do(ctx, http.MethodGet, "/pulls"+q, nil, &open); err != nil {
		return PullRequest{}, err
	}
	if len(open) > 0 {
		return open[0], nil
	}
	var pr PullRequest
	err = r.do(ctx, http.MethodPost, "/pulls", map[string]string{"title": title, "head": head, "base": base, "body": body}, &pr)
	return pr, err
}

func escapeRef(ref string) string { return escapePath(ref) }

// escapePath escapes each segment of a path and keeps the '/'.
func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}
