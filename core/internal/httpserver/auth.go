package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/auth"
)

// SessionCookie is the name of the cookie of a browser session.
const SessionCookie = "nida_session"

// maxAuthBody is the maximum size of the body of an auth request.
const maxAuthBody = 4 << 10

// Authenticator checks the sessions and the tokens. *auth.Store is the
// real one.
type Authenticator interface {
	OwnerSet() bool
	SignIn(password string) (string, time.Time, error)
	SignOut(secret string) error
	Authenticate(secret string) (auth.Identity, error)
	CheckPassword(password string) error
	SetPassword(password string) error
	CreateToken(name, level string) (auth.Token, string, error)
	Tokens() []auth.Token
	DeleteToken(id string) error
}

type identityKey struct{}

// WithIdentity gives each request of h the identity id. The local socket
// uses it (decision D18).
func WithIdentity(h http.Handler, id auth.Identity) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		h.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), identityKey{}, id)))
	})
}

// IdentityOf gives the identity of a request after the access check.
func IdentityOf(req *http.Request) (auth.Identity, bool) {
	id, ok := req.Context().Value(identityKey{}).(auth.Identity)
	return id, ok
}

// access is the rule of a path.
type access int

const (
	accessPublic access = iota // no identity
	accessAny                  // any identity
	accessRead                 // the read level
	accessAdmin                // the admin level
)

// accessOf gives the rule of a request. The default is: read for GET,
// admin for the other methods.
func accessOf(req *http.Request) access {
	p := req.URL.Path
	switch {
	case p == "/healthz" || p == "/readyz" || p == "/api/v1/health":
		return accessPublic
	case p == "/api/v1/auth/login" || p == "/api/v1/auth/status":
		return accessPublic
	case p == "/api/v1/auth/logout" || p == "/api/v1/auth/me":
		return accessAny
	case p == "/api/v1/tokens" || strings.HasPrefix(p, "/api/v1/tokens/") || p == "/api/v1/auth/password":
		return accessAdmin
	case req.Method == http.MethodGet || req.Method == http.MethodHead:
		return accessRead
	}
	return accessAdmin
}

// checkAccess finds the identity of the request and applies the rule of
// the path. It gives the request with the identity, or writes the error.
func (r *Router) checkAccess(w http.ResponseWriter, req *http.Request) (*http.Request, bool) {
	if r.auth == nil {
		return req, true // the tests of the other parts
	}
	rule := accessOf(req)
	if rule == accessPublic {
		return req, true
	}

	id, ok := IdentityOf(req)
	if !ok {
		var viaCookie bool
		id, viaCookie, ok = r.identify(req)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="nidaos"`)
			writeError(w, http.StatusUnauthorized, "sign_in_required", "Sign in, or give a token in the Authorization header.")
			return nil, false
		}
		// A browser sends the cookie also for a form of an other site.
		// For a change, the request must come from the same site.
		if viaCookie && !safeMethod(req.Method) && !sameOrigin(req) {
			writeError(w, http.StatusForbidden, "cross_site", "The request comes from an other site.")
			return nil, false
		}
		req = req.WithContext(context.WithValue(req.Context(), identityKey{}, id))
	}

	need := auth.LevelRead
	if rule == accessAdmin {
		need = auth.LevelAdmin
	}
	if rule != accessAny && !auth.Allows(id.Level, need) {
		writeError(w, http.StatusForbidden, "forbidden", "The level of this token does not give this operation.")
		return nil, false
	}
	return req, true
}

// identify reads the session cookie or the bearer token.
func (r *Router) identify(req *http.Request) (id auth.Identity, viaCookie, ok bool) {
	if h := req.Header.Get("Authorization"); h != "" {
		scheme, secret, found := strings.Cut(h, " ")
		if !found || !strings.EqualFold(scheme, "Bearer") {
			return auth.Identity{}, false, false
		}
		id, err := r.auth.Authenticate(strings.TrimSpace(secret))
		return id, false, err == nil
	}
	if c, err := req.Cookie(SessionCookie); err == nil {
		id, err := r.auth.Authenticate(c.Value)
		return id, true, err == nil
	}
	return auth.Identity{}, false, false
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// sameOrigin tells if the Origin of the request is the host of the
// request. A request without Origin (not from a browser) is accepted.
func sameOrigin(req *http.Request) bool {
	o := req.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && strings.EqualFold(u.Host, req.Host)
}

func (r *Router) addAuthRoutes() {
	if r.auth == nil {
		return
	}
	r.handle(http.MethodGet, "/api/v1/auth/status", r.authStatus)
	r.handle(http.MethodPost, "/api/v1/auth/login", r.authLogin)
	r.handle(http.MethodPost, "/api/v1/auth/logout", r.authLogout)
	r.handle(http.MethodGet, "/api/v1/auth/me", r.authMe)
	r.handle(http.MethodPut, "/api/v1/auth/password", r.authPassword)
	r.handle(http.MethodGet, "/api/v1/tokens", r.tokensList)
	r.handle(http.MethodPost, "/api/v1/tokens", r.tokensCreate)
	r.handle(http.MethodDelete, "/api/v1/tokens/{id}", r.tokensDelete)
}

// authStatus tells the sign-in page if the owner password is set.
func (r *Router) authStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"owner_set": r.auth.OwnerSet()})
}

func readJSON(w http.ResponseWriter, req *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxAuthBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad_body", "The body must be a JSON object with the correct fields.")
		return false
	}
	return true
}

// clientAddr gives the address of the client without the port.
func clientAddr(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return req.RemoteAddr
	}
	return host
}

func (r *Router) authLogin(w http.ResponseWriter, req *http.Request) {
	addr := clientAddr(req)
	if wait := r.limiter.Wait(addr); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds()+0.999)))
		writeError(w, http.StatusTooManyRequests, "too_many_attempts", "Too many wrong passwords. Wait and try again.")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !readJSON(w, req, &body) {
		return
	}
	secret, expires, err := r.auth.SignIn(body.Password)
	switch {
	case errors.Is(err, auth.ErrOwnerNotSet):
		writeError(w, http.StatusConflict, "owner_not_set", "The owner password is not set. Set it on the computer with: nida-core set-password")
		return
	case errors.Is(err, auth.ErrWrongPassword):
		r.limiter.Fail(addr)
		r.logger.Warn("a sign-in failed", "addr", addr)
		writeError(w, http.StatusUnauthorized, "wrong_password", "The password is not correct.")
		return
	case err != nil:
		r.logger.Error("the sign-in failed", "err", err)
		writeError(w, http.StatusInternalServerError, "sign_in_failed", "The sign-in failed.")
		return
	}
	r.limiter.Succeed(addr)
	r.logger.Info("the owner signed in", "addr", addr)
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    secret,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   req.TLS != nil || strings.EqualFold(req.Header.Get("X-Forwarded-Proto"), "https"),
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"expires": expires})
}

func (r *Router) authLogout(w http.ResponseWriter, req *http.Request) {
	if c, err := req.Cookie(SessionCookie); err == nil {
		_ = r.auth.SignOut(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (r *Router) authMe(w http.ResponseWriter, req *http.Request) {
	id, _ := IdentityOf(req)
	writeJSON(w, http.StatusOK, id)
}

// authPassword changes the owner password. A browser session must give
// the current password. The local socket does not: it sets the first
// password, or a new password when the owner forgot it.
func (r *Router) authPassword(w http.ResponseWriter, req *http.Request) {
	id, _ := IdentityOf(req)
	if id.Kind == auth.KindToken {
		writeError(w, http.StatusForbidden, "forbidden", "A token cannot change the password.")
		return
	}
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !readJSON(w, req, &body) {
		return
	}
	if id.Kind != auth.KindLocal {
		addr := clientAddr(req)
		if wait := r.limiter.Wait(addr); wait > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds()+0.999)))
			writeError(w, http.StatusTooManyRequests, "too_many_attempts", "Too many wrong passwords. Wait and try again.")
			return
		}
		if err := r.auth.CheckPassword(body.Current); err != nil {
			r.limiter.Fail(addr)
			writeError(w, http.StatusUnauthorized, "wrong_password", "The current password is not correct.")
			return
		}
	}
	if err := r.auth.SetPassword(body.New); err != nil {
		if errors.Is(err, auth.ErrWeakPassword) {
			writeError(w, http.StatusBadRequest, "weak_password", err.Error()+".")
			return
		}
		r.logger.Error("the password change failed", "err", err)
		writeError(w, http.StatusInternalServerError, "password_failed", "The password change failed.")
		return
	}
	r.logger.Info("the owner password changed: all the sessions are closed", "by", id.Kind)
	w.WriteHeader(http.StatusNoContent)
}

type tokensBody struct {
	Tokens []auth.Token `json:"tokens"`
}

func (r *Router) tokensList(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, tokensBody{Tokens: r.auth.Tokens()})
}

// tokensCreate makes a token. The response is the only place where the
// secret of the token is visible.
func (r *Router) tokensCreate(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Name  string `json:"name"`
		Level string `json:"level"`
	}
	if !readJSON(w, req, &body) {
		return
	}
	t, secret, err := r.auth.CreateToken(body.Name, body.Level)
	switch {
	case errors.Is(err, auth.ErrBadName):
		writeError(w, http.StatusBadRequest, "bad_name", err.Error()+".")
		return
	case errors.Is(err, auth.ErrBadLevel):
		writeError(w, http.StatusBadRequest, "bad_level", err.Error()+".")
		return
	case err != nil:
		r.logger.Error("the token creation failed", "err", err)
		writeError(w, http.StatusInternalServerError, "token_failed", "The token creation failed.")
		return
	}
	by, _ := IdentityOf(req)
	r.logger.Info("a token was made", "name", t.Name, "level", t.Level, "by", by.Kind)
	writeJSON(w, http.StatusCreated, struct {
		auth.Token
		Secret string `json:"secret"`
	}{t, secret})
}

func (r *Router) tokensDelete(w http.ResponseWriter, req *http.Request) {
	err := r.auth.DeleteToken(req.PathValue("id"))
	if errors.Is(err, auth.ErrNotFound) {
		writeError(w, http.StatusNotFound, "token_not_found", "The token does not exist.")
		return
	}
	if err != nil {
		r.logger.Error("the token deletion failed", "err", err)
		writeError(w, http.StatusInternalServerError, "token_failed", "The token deletion failed.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
