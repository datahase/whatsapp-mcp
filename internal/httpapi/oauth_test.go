package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/datahase/whatsapp-mcp/internal/accounts"
	"github.com/datahase/whatsapp-mcp/internal/authflow"
	"github.com/datahase/whatsapp-mcp/internal/config"
	wamcp "github.com/datahase/whatsapp-mcp/internal/mcp"
	"github.com/datahase/whatsapp-mcp/internal/security"
)

func TestOAuthBrowserApprovalAndProtectedMCP(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir(), LogLevel: "ERROR"}
	m := accounts.NewManager(cfg, &security.Redactor{})
	m.Start(context.Background())
	defer m.Stop()
	oauth, err := authflow.Open(t.TempDir(), "http://127.0.0.1:9876")
	if err != nil {
		t.Fatal(err)
	}
	defer oauth.Close()
	h := New(Config{Token: token, AdminPassword: adminPW, DataDir: cfg.DataDir, Manager: m, MCP: wamcp.NewServer(m, nil, 1024).Handler(), OAuth: oauth, OAuthOnly: true})
	srv := httptest.NewServer(h)
	defer srv.Close()
	res := do(t, "GET", srv.URL+"/.well-known/oauth-protected-resource/mcp", nil, nil)
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	res.Body.Close()
	res = do(t, "POST", srv.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`), auth())
	if res.StatusCode != 401 || !strings.Contains(res.Header.Get("WWW-Authenticate"), "resource_metadata") {
		t.Fatal("OAuth-only accepted static token or failed discovery")
	}
	res.Body.Close()
	res = do(t, "POST", srv.URL+"/oauth/register", strings.NewReader(`{"client_name":"Fixture client","redirect_uris":["http://127.0.0.1:19876/callback"],"token_endpoint_auth_method":"none"}`), map[string]string{"Content-Type": "application/json"})
	var registration map[string]any
	json.NewDecoder(res.Body).Decode(&registration)
	res.Body.Close()
	id := registration["client_id"].(string)
	verifier := strings.Repeat("a", 64)
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{"client_id": {id}, "redirect_uri": {"http://127.0.0.1:19876/callback"}, "response_type": {"code"}, "resource": {oauth.Resource}, "state": {"preserved"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}}
	res = do(t, "GET", srv.URL+"/admin/oauth/authorize?"+q.Encode(), nil, nil)
	pending := res.Request.URL.Query().Get("authorize")
	if res.Request.URL.Path != "/admin/login" || pending == "" {
		t.Fatal("authorization not carried to login")
	}
	res.Body.Close()
	res = do(t, "GET", srv.URL+"/admin/api/oauth/requests/"+pending, nil, map[string]string{"Accept": "application/json"})
	if res.StatusCode != 401 {
		t.Fatal("consent readable before sign-in")
	}
	res.Body.Close()
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar}
	res, err = browser.PostForm(srv.URL+"/admin/login", url.Values{"password": {adminPW}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	res, _ = browser.Get(srv.URL + "/admin/api/session")
	var session map[string]any
	json.NewDecoder(res.Body).Decode(&session)
	res.Body.Close()
	if session["token"] != "" || session["oauth_enabled"] != true {
		t.Fatal("OAuth-only exposed static token or failed capability")
	}
	endpoint := srv.URL + "/admin/api/oauth/requests/" + pending
	res, _ = browser.PostForm(endpoint, url.Values{"decision": {"approve"}})
	if res.StatusCode != 403 {
		t.Fatal("approval accepted without CSRF")
	}
	res.Body.Close()
	csrf := session["csrf"].(string)
	for _, decisions := range [][]string{{"approve", "deny"}, {"unexpected"}} {
		res, _ = browser.PostForm(endpoint, url.Values{"csrf": {csrf}, "decision": decisions})
		if res.StatusCode != 400 {
			t.Fatal("invalid or duplicate approval decision accepted")
		}
		res.Body.Close()
	}
	res, _ = browser.PostForm(endpoint, url.Values{"csrf": {csrf}, "decision": {"approve"}})
	var response map[string]string
	json.NewDecoder(res.Body).Decode(&response)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("approval: %d %v", res.StatusCode, response)
	}
	callback, _ := url.Parse(response["redirect"])
	if callback.Query().Get("state") != "preserved" {
		t.Fatal("OAuth state lost")
	}
	res, _ = http.PostForm(srv.URL+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "client_id": {id}, "redirect_uri": {"http://127.0.0.1:19876/callback"}, "resource": {oauth.Resource}, "code": {callback.Query().Get("code")}, "code_verifier": {verifier}})
	var credentials map[string]any
	json.NewDecoder(res.Body).Decode(&credentials)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("token exchange: %d", res.StatusCode)
	}
	data := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_accounts","arguments":{}}}`)
	res = do(t, "POST", srv.URL+"/mcp", bytes.NewReader(data), map[string]string{"Authorization": "Bearer " + credentials["access_token"].(string), "Content-Type": "application/json", "Accept": "application/json, text/event-stream"})
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !bytes.Contains(raw, []byte("accounts")) {
		t.Fatal("authorized MCP tool call failed", res.StatusCode)
	}
	res, _ = browser.Get(srv.URL + "/admin/api/oauth/connections")
	var grants []authflow.Grant
	json.NewDecoder(res.Body).Decode(&grants)
	res.Body.Close()
	if len(grants) != 1 {
		t.Fatal("approved client absent")
	}
	req, _ := http.NewRequest("POST", srv.URL+"/admin/api/oauth/connections/"+grants[0].ID+"/revoke", strings.NewReader(""))
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Accept", "application/json")
	res, err = browser.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("revoke failed")
	}
	if oauth.Validate(context.Background(), credentials["access_token"].(string)) {
		t.Fatal("revoked credential still accepted")
	}
}
