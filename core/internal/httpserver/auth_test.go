package httpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fathorMB/NidaOS/core/internal/auth"
)

const ownerPassword = "a long owner password"

// authRouter makes a router with a real auth store and the fake state.
func authRouter(t *testing.T, setPassword bool) (*Router, *auth.Store) {
	t.Helper()
	store, err := auth.Open(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if setPassword {
		if err := store.SetPassword(ownerPassword); err != nil {
			t.Fatal(err)
		}
	}
	return NewRouter(Deps{State: &fakeState{}, Auth: store}), store
}

type call struct {
	method, path, body string
	header             map[string]string
	cookie             *http.Cookie
}

func do(r http.Handler, c call) *httptest.ResponseRecorder {
	req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
	req.Host = "nida.local:8470"
	for k, v := range c.header {
		req.Header.Set(k, v)
	}
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func errCode(rec *httptest.ResponseRecorder) string {
	var e Error
	_ = json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&e)
	return e.Code
}

func TestAccessWithoutIdentity(t *testing.T) {
	r, _ := authRouter(t, true)
	for _, p := range []string{"/healthz", "/readyz", "/api/v1/health", "/api/v1/auth/status"} {
		if rec := do(r, call{method: "GET", path: p}); rec.Code != 200 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
	for _, p := range []string{"/api/v1/state", "/api/v1/tokens", "/api/v1/auth/me", "/api/v1/nothing"} {
		rec := do(r, call{method: "GET", path: p})
		if rec.Code != 401 || errCode(rec) != "sign_in_required" || rec.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("%s: %d %s", p, rec.Code, rec.Body)
		}
	}
	rec := do(r, call{method: "GET", path: "/api/v1/state", header: map[string]string{"Authorization": "Bearer nida_wrong"}})
	if rec.Code != 401 {
		t.Errorf("wrong token: %d", rec.Code)
	}
	rec = do(r, call{method: "GET", path: "/api/v1/state", header: map[string]string{"Authorization": "Basic eDp5"}})
	if rec.Code != 401 {
		t.Errorf("basic: %d", rec.Code)
	}
}

func TestLoginAndSession(t *testing.T) {
	r, _ := authRouter(t, true)

	rec := do(r, call{method: "POST", path: "/api/v1/auth/login", body: `{"password":"wrong password here"}`})
	if rec.Code != 401 || errCode(rec) != "wrong_password" {
		t.Fatalf("wrong: %d %s", rec.Code, rec.Body)
	}
	if rec := do(r, call{method: "POST", path: "/api/v1/auth/login", body: `{"pass":"x"}`}); rec.Code != 400 {
		t.Errorf("bad body: %d", rec.Code)
	}

	rec = do(r, call{method: "POST", path: "/api/v1/auth/login", body: `{"password":"` + ownerPassword + `"}`})
	if rec.Code != 200 {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookie {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Value == "" {
		t.Fatalf("cookie = %+v", cookie)
	}

	// The cookie opens the API.
	if rec := do(r, call{method: "GET", path: "/api/v1/state", cookie: cookie}); rec.Code != 200 {
		t.Errorf("state with cookie: %d", rec.Code)
	}
	rec = do(r, call{method: "GET", path: "/api/v1/auth/me", cookie: cookie})
	var me auth.Identity
	json.NewDecoder(rec.Body).Decode(&me)
	if me.Kind != auth.KindOwner || me.Level != auth.LevelAdmin {
		t.Errorf("me = %+v", me)
	}

	// A change from an other site is refused. The same site is accepted.
	rec = do(r, call{method: "POST", path: "/api/v1/tokens", body: `{"name":"x","level":"read"}`, cookie: cookie,
		header: map[string]string{"Origin": "https://evil.example"}})
	if rec.Code != 403 || errCode(rec) != "cross_site" {
		t.Errorf("cross site: %d %s", rec.Code, rec.Body)
	}
	rec = do(r, call{method: "POST", path: "/api/v1/tokens", body: `{"name":"x","level":"read"}`, cookie: cookie,
		header: map[string]string{"Origin": "http://nida.local:8470"}})
	if rec.Code != 201 {
		t.Errorf("same site: %d %s", rec.Code, rec.Body)
	}

	// Sign-out closes the session.
	if rec := do(r, call{method: "POST", path: "/api/v1/auth/logout", cookie: cookie}); rec.Code != 204 {
		t.Errorf("logout: %d", rec.Code)
	}
	if rec := do(r, call{method: "GET", path: "/api/v1/state", cookie: cookie}); rec.Code != 401 {
		t.Errorf("after logout: %d", rec.Code)
	}
}

func TestLoginLimit(t *testing.T) {
	r, _ := authRouter(t, true)
	for i := 0; i < 5; i++ {
		do(r, call{method: "POST", path: "/api/v1/auth/login", body: `{"password":"wrong password here"}`})
	}
	do(r, call{method: "POST", path: "/api/v1/auth/login", body: `{"password":"wrong password here"}`})
	rec := do(r, call{method: "POST", path: "/api/v1/auth/login", body: `{"password":"` + ownerPassword + `"}`})
	if rec.Code != 429 || errCode(rec) != "too_many_attempts" || rec.Header().Get("Retry-After") == "" {
		t.Errorf("after 6 failures: %d %s", rec.Code, rec.Body)
	}
}

func TestOwnerNotSet(t *testing.T) {
	r, _ := authRouter(t, false)
	rec := do(r, call{method: "GET", path: "/api/v1/auth/status"})
	if !strings.Contains(rec.Body.String(), `"owner_set":false`) {
		t.Errorf("status = %s", rec.Body)
	}
	rec = do(r, call{method: "POST", path: "/api/v1/auth/login", body: `{"password":"anything at all"}`})
	if rec.Code != 409 || errCode(rec) != "owner_not_set" {
		t.Errorf("login: %d %s", rec.Code, rec.Body)
	}
}

func TestTokenLevels(t *testing.T) {
	r, store := authRouter(t, false)
	_, readSecret, _ := store.CreateToken("script", auth.LevelRead)
	_, adminSecret, _ := store.CreateToken("agent", auth.LevelAdmin)
	bearer := func(s string) map[string]string { return map[string]string{"Authorization": "Bearer " + s} }

	// read: the state yes, the tokens and the changes no.
	if rec := do(r, call{method: "GET", path: "/api/v1/state", header: bearer(readSecret)}); rec.Code != 200 {
		t.Errorf("read state: %d", rec.Code)
	}
	if rec := do(r, call{method: "GET", path: "/api/v1/tokens", header: bearer(readSecret)}); rec.Code != 403 || errCode(rec) != "forbidden" {
		t.Errorf("read tokens: %d", rec.Code)
	}
	if rec := do(r, call{method: "POST", path: "/api/v1/tokens", body: `{"name":"x","level":"admin"}`, header: bearer(readSecret)}); rec.Code != 403 {
		t.Errorf("read makes a token: %d", rec.Code)
	}

	// admin: makes, lists and deletes tokens. A token has no cookie, thus
	// the Origin test does not apply.
	rec := do(r, call{method: "POST", path: "/api/v1/tokens", body: `{"name":"laptop","level":"read"}`, header: bearer(adminSecret)})
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var created struct {
		auth.Token
		Secret string `json:"secret"`
	}
	json.NewDecoder(rec.Body).Decode(&created)
	if created.Name != "laptop" || !strings.HasPrefix(created.Secret, "nida_") {
		t.Errorf("created = %+v", created)
	}
	rec = do(r, call{method: "GET", path: "/api/v1/tokens", header: bearer(adminSecret)})
	if rec.Code != 200 || strings.Contains(rec.Body.String(), created.Secret) || strings.Count(rec.Body.String(), `"id"`) != 3 {
		t.Errorf("list: %d %s", rec.Code, rec.Body)
	}
	if rec := do(r, call{method: "POST", path: "/api/v1/tokens", body: `{"name":"x","level":"root"}`, header: bearer(adminSecret)}); errCode(rec) != "bad_level" {
		t.Errorf("bad level: %s", rec.Body)
	}
	if rec := do(r, call{method: "DELETE", path: "/api/v1/tokens/" + created.ID, header: bearer(adminSecret)}); rec.Code != 204 {
		t.Errorf("delete: %d", rec.Code)
	}
	if rec := do(r, call{method: "DELETE", path: "/api/v1/tokens/" + created.ID, header: bearer(adminSecret)}); rec.Code != 404 {
		t.Errorf("delete again: %d", rec.Code)
	}
	if rec := do(r, call{method: "GET", path: "/api/v1/state", header: bearer(created.Secret)}); rec.Code != 401 {
		t.Errorf("deleted token: %d", rec.Code)
	}

	// A token cannot change the password, also with the admin level.
	if rec := do(r, call{method: "PUT", path: "/api/v1/auth/password", body: `{"new":"a new long password"}`, header: bearer(adminSecret)}); rec.Code != 403 {
		t.Errorf("token password: %d", rec.Code)
	}
}

func TestPasswordChange(t *testing.T) {
	r, store := authRouter(t, false)
	local := WithIdentity(r, auth.Identity{Kind: auth.KindLocal, Name: "local", Level: auth.LevelAdmin})

	// The local socket sets the first password without the current one.
	if rec := do(local, call{method: "PUT", path: "/api/v1/auth/password", body: `{"new":"short"}`}); rec.Code != 400 || errCode(rec) != "weak_password" {
		t.Errorf("weak: %d %s", rec.Code, rec.Body)
	}
	if rec := do(local, call{method: "PUT", path: "/api/v1/auth/password", body: `{"new":"` + ownerPassword + `"}`}); rec.Code != 204 {
		t.Fatalf("local set: %d %s", rec.Code, rec.Body)
	}
	if !store.OwnerSet() {
		t.Fatal("the password is not set")
	}

	// A browser session must give the current password.
	secret, _, err := store.SignIn(ownerPassword)
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: SessionCookie, Value: secret}
	if rec := do(r, call{method: "PUT", path: "/api/v1/auth/password", body: `{"current":"not the password","new":"the new long password"}`, cookie: cookie}); rec.Code != 401 {
		t.Errorf("wrong current: %d", rec.Code)
	}
	if rec := do(r, call{method: "PUT", path: "/api/v1/auth/password", body: `{"current":"` + ownerPassword + `","new":"the new long password"}`, cookie: cookie}); rec.Code != 204 {
		t.Errorf("change: %d %s", rec.Code, rec.Body)
	}
	// All the sessions closed.
	if rec := do(r, call{method: "GET", path: "/api/v1/state", cookie: cookie}); rec.Code != 401 {
		t.Errorf("old session: %d", rec.Code)
	}
}

func TestLocalIdentity(t *testing.T) {
	r, _ := authRouter(t, true)
	local := WithIdentity(r, auth.Identity{Kind: auth.KindLocal, Name: "local", Level: auth.LevelAdmin})
	if rec := do(local, call{method: "GET", path: "/api/v1/tokens"}); rec.Code != 200 {
		t.Errorf("local tokens: %d", rec.Code)
	}
	rec := do(local, call{method: "GET", path: "/api/v1/auth/me"})
	if !strings.Contains(rec.Body.String(), `"kind":"local"`) {
		t.Errorf("me = %s", rec.Body)
	}
}
