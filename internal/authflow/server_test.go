package authflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const testIssuer = "https://mcp.example.test"
const callback = "http://127.0.0.1:19876/callback"
const verifier = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~"

func openTest(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir, testIssuer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}
func register(t *testing.T, s *Server) string {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/oauth/register", strings.NewReader(`{"client_name":"Test client","redirect_uris":["`+callback+`"],"token_endpoint_auth_method":"none"}`))
	s.Register(w, r)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var data map[string]any
	json.Unmarshal(w.Body.Bytes(), &data)
	return data["client_id"].(string)
}
func query(id string) url.Values {
	h := sha256.Sum256([]byte(verifier))
	return url.Values{"client_id": {id}, "redirect_uri": {callback}, "response_type": {"code"}, "scope": {Scope}, "resource": {testIssuer + "/mcp"}, "state": {"test-state"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(h[:])}, "code_challenge_method": {"S256"}}
}
func begin(t *testing.T, s *Server, id string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/admin/oauth/authorize?"+query(id).Encode(), nil)
	pending, err := s.Begin(req)
	if err != nil {
		t.Fatal(err)
	}
	return pending
}
func code(t *testing.T, s *Server, id string) string {
	t.Helper()
	pending := begin(t, s, id)
	target, err := s.Decide(context.Background(), pending, "approve")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(target)
	if u.Query().Get("state") != "test-state" || u.Query().Get("iss") != testIssuer {
		t.Fatal("callback state/issuer lost")
	}
	return u.Query().Get("code")
}
func exchange(s *Server, id, code string, changes url.Values) *httptest.ResponseRecorder {
	v := url.Values{"grant_type": {"authorization_code"}, "client_id": {id}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {callback}, "resource": {s.Resource}}
	for k, values := range changes {
		v[k] = values
	}
	r := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.Token(w, r)
	return w
}
func refresh(s *Server, id, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(url.Values{"grant_type": {"refresh_token"}, "client_id": {id}, "refresh_token": {token}, "resource": {s.Resource}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.Token(w, r)
	return w
}
func tokens(t *testing.T, w *httptest.ResponseRecorder) (string, string) {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("token response %d %s", w.Code, w.Body.String())
	}
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	return out["access_token"].(string), out["refresh_token"].(string)
}
func TestPKCETokenRefreshReplayAndRevocation(t *testing.T) {
	s, dir := openTest(t)
	id := register(t, s)
	c := code(t, s, id)
	access, rt := tokens(t, exchange(s, id, c, nil))
	if !s.Validate(context.Background(), access) {
		t.Fatal("issued access token rejected")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "oauth.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{c, access, rt} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("raw credentials persisted")
		}
	}
	if s.Validate(context.Background(), digest(access)) {
		t.Fatal("stored hash accepted as bearer token")
	}
	if w := exchange(s, id, c, nil); w.Code != 400 {
		t.Fatal("code replay accepted")
	}
	fresh, freshRT := tokens(t, refresh(s, id, rt))
	if fresh == access || freshRT == rt || s.Validate(context.Background(), access) || !s.Validate(context.Background(), fresh) {
		t.Fatal("refresh did not rotate credentials")
	}
	if w := refresh(s, id, rt); w.Code != 400 {
		t.Fatal("refresh replay accepted")
	}
	if s.Validate(context.Background(), fresh) {
		t.Fatal("refresh replay did not revoke token family")
	}
	next, nextRT := tokens(t, exchange(s, id, code(t, s, id), nil))
	_ = nextRT
	grants, err := s.Grants(context.Background())
	if err != nil || len(grants) != 1 {
		t.Fatalf("active grants: %v %v", grants, err)
	}
	if err = s.Revoke(context.Background(), grants[0].ID); err != nil || s.Validate(context.Background(), next) {
		t.Fatal("owner revocation not immediate")
	}
}
func TestCodeBoundToClientRedirectResourceAndPKCE(t *testing.T) {
	tests := []struct {
		name    string
		changes url.Values
	}{
		{"wrong redirect", url.Values{"redirect_uri": {"http://127.0.0.1:19876/other"}}},
		{"wrong resource", url.Values{"resource": {"https://other.example/mcp"}}},
		{"wrong verifier", url.Values{"code_verifier": {strings.Repeat("a", 64)}}},
		{"missing verifier", url.Values{"code_verifier": {""}}},
		{"duplicate code", url.Values{"code": {"one", "two"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := openTest(t)
			id := register(t, s)
			c := code(t, s, id)
			if w := exchange(s, id, c, tc.changes); w.Code != 400 {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	s, _ := openTest(t)
	id := register(t, s)
	other := register(t, s)
	c := code(t, s, id)
	if w := exchange(s, other, c, nil); w.Code != 400 && w.Code != 401 {
		t.Fatal("code exchanged by different client")
	}
	tokens(t, exchange(s, id, c, nil))
}
func TestConsentValidationDenialExpiryAndSingleUse(t *testing.T) {
	s, _ := openTest(t)
	id := register(t, s)
	for key, value := range map[string]string{"redirect_uri": "https://attacker.example/callback", "resource": "https://attacker.example/mcp", "code_challenge_method": "plain", "code_challenge": "short", "scope": "admin", "response_type": "token"} {
		q := query(id)
		q.Set(key, value)
		if _, err := s.Begin(httptest.NewRequest("GET", "/admin/oauth/authorize?"+q.Encode(), nil)); err == nil {
			t.Fatalf("invalid %s accepted", key)
		}
	}
	p := begin(t, s, id)
	if _, err := s.Decide(context.Background(), p, "unexpected"); err == nil {
		t.Fatal("invalid decision accepted")
	}
	target, err := s.Decide(context.Background(), p, "deny")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(target)
	if u.Query().Get("error") != "access_denied" || u.Query().Get("code") != "" {
		t.Fatal("denial issued code")
	}
	if _, err = s.Decide(context.Background(), p, "approve"); err == nil {
		t.Fatal("consent reused")
	}
	p = begin(t, s, id)
	expired := s.pending[p]
	expired.Expires = expired.Expires.Add(-24 * 60 * 60 * 1e9)
	s.pending[p] = expired
	if _, err = s.Decide(context.Background(), p, "approve"); err == nil {
		t.Fatal("expired consent accepted")
	}
}
func TestConcurrentCodeExchangeProducesOneToken(t *testing.T) {
	s, _ := openTest(t)
	id := register(t, s)
	c := code(t, s, id)
	var wg sync.WaitGroup
	results := make(chan int, 8)
	for range 8 {
		wg.Go(func() { results <- exchange(s, id, c, nil).Code })
	}
	wg.Wait()
	close(results)
	successes := 0
	for status := range results {
		if status == 200 {
			successes++
		} else if status != 400 {
			t.Fatal(status)
		}
	}
	if successes != 1 {
		t.Fatalf("code exchanged %d times", successes)
	}
}
func TestTokenPersistenceAndIssuerBinding(t *testing.T) {
	s, dir := openTest(t)
	id := register(t, s)
	access, _ := tokens(t, exchange(s, id, code(t, s, id), nil))
	s.Close()
	restored, err := Open(dir, testIssuer)
	if err != nil {
		t.Fatal(err)
	}
	if !restored.Validate(context.Background(), access) {
		t.Fatal("token lost on restart")
	}
	restored.Close()
	changed, err := Open(dir, "https://different.example")
	if err != nil {
		t.Fatal(err)
	}
	defer changed.Close()
	if changed.Validate(context.Background(), access) {
		t.Fatal("token accepted for changed issuer/resource")
	}
}
func TestRedirectAndIssuerSafety(t *testing.T) {
	for _, bad := range []string{"http://example.com/callback", "https://a.example/#fragment", "https://user:pass@a.example/cb", "https://*.example/cb", "file:///tmp/cb", "javascript:alert(1)"} {
		if validRedirect(bad) {
			t.Fatal("unsafe redirect", bad)
		}
	}
	if _, err := Open(t.TempDir(), "http://192.168.1.10"); err == nil {
		t.Fatal("non-loopback plaintext issuer accepted")
	}
}
func TestMetadata(t *testing.T) {
	s, _ := openTest(t)
	w := httptest.NewRecorder()
	s.Metadata(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(w.Body.String(), "S256") || strings.Contains(w.Body.String(), "client_credentials") {
		t.Fatal(w.Body.String())
	}
}

func TestExpiryAndRefreshClientBinding(t *testing.T) {
	s, _ := openTest(t)
	id := register(t, s)
	other := register(t, s)
	access, rt := tokens(t, exchange(s, id, code(t, s, id), nil))
	if w := refresh(s, other, rt); w.Code < 400 {
		t.Fatal("refresh token accepted by different client")
	}
	if !s.Validate(context.Background(), access) {
		t.Fatal("wrong client revoked legitimate active grant")
	}
	var key int
	var raw []byte
	if err := s.db.QueryRow("SELECT id,data FROM tokens WHERE access_hash=?", digest(access)).Scan(&key, &raw); err != nil {
		t.Fatal(err)
	}
	var info map[string]any
	json.Unmarshal(raw, &info)
	info["AccessCreateAt"] = "2000-01-01T00:00:00Z"
	updated, _ := json.Marshal(info)
	s.db.Exec("UPDATE tokens SET data=? WHERE id=?", updated, key)
	if s.Validate(context.Background(), access) {
		t.Fatal("expired access token accepted")
	}
	fresh, _ := tokens(t, refresh(s, id, rt))
	if !s.Validate(context.Background(), fresh) {
		t.Fatal("valid refresh did not recover expired access")
	}
	s.db.Exec("UPDATE grants SET expires=1")
	if s.Validate(context.Background(), fresh) {
		t.Fatal("expired grant accepted")
	}
}
