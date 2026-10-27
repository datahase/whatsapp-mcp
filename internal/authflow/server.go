package authflow

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-oauth2/oauth2/v4"
	oauthErrors "github.com/go-oauth2/oauth2/v4/errors"
	"github.com/go-oauth2/oauth2/v4/manage"
	"github.com/go-oauth2/oauth2/v4/server"
)

const Scope = "whatsapp"
const accessTTL = 15 * time.Minute
const grantTTL = 30 * 24 * time.Hour

var challengeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
var verifierRe = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

type pending struct {
	Request *server.AuthorizeRequest
	Expires time.Time
	Client  clientInfo
}
type Server struct {
	mu       sync.Mutex
	db       *sql.DB
	manager  *manage.Manager
	protocol *server.Server
	Issuer   string
	Resource string
	pending  map[string]pending
	rates    map[string][]time.Time
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b), err
}

type generator struct{}

func (generator) Token(ctx context.Context, data *oauth2.GenerateBasic, refresh bool) (string, string, error) {
	a, err := randomToken()
	if err != nil {
		return "", "", err
	}
	if !refresh {
		return a, "", nil
	}
	r, err := randomToken()
	return a, r, err
}

type codeGenerator struct{}

func (codeGenerator) Token(context.Context, *oauth2.GenerateBasic) (string, error) {
	return randomToken()
}

func Open(dir, issuer string) (*Server, error) {
	u, err := url.Parse(issuer)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return nil, errors.New("public_url must be an HTTPS origin without a path")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1")) {
		return nil, errors.New("OAuth requires HTTPS (HTTP loopback is allowed for local testing)")
	}
	issuer = strings.TrimRight(issuer, "/")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "oauth.db")
	db, err := sql.Open("sqlite3", "file:"+path+"?_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS clients(id TEXT PRIMARY KEY,data BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS grants(id TEXT PRIMARY KEY,client_id TEXT NOT NULL,created INTEGER NOT NULL,expires INTEGER NOT NULL,resource TEXT NOT NULL,revoked INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS tokens(id INTEGER PRIMARY KEY,code_hash TEXT UNIQUE,access_hash TEXT UNIQUE,refresh_hash TEXT UNIQUE,grant_id TEXT NOT NULL,data BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS spent_refresh(hash TEXT PRIMARY KEY,grant_id TEXT NOT NULL);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		db.Close()
		return nil, err
	}
	s := &Server{db: db, Issuer: issuer, Resource: issuer + "/mcp", pending: map[string]pending{}, rates: map[string][]time.Time{}}
	m := manage.NewDefaultManager()
	s.manager = m
	m.MapClientStorage(s)
	m.MapTokenStorage(tokenStore{s})
	m.MapAuthorizeGenerate(codeGenerator{})
	m.MapAccessGenerate(generator{})
	m.SetAuthorizeCodeExp(2 * time.Minute)
	m.SetAuthorizeCodeTokenCfg(&manage.Config{AccessTokenExp: accessTTL, RefreshTokenExp: grantTTL, IsGenerateRefresh: true})
	m.SetRefreshTokenCfg(&manage.RefreshingConfig{AccessTokenExp: accessTTL, IsGenerateRefresh: true, IsRemoveAccess: true, IsRemoveRefreshing: true})
	m.SetValidateURIHandler(func(registered, redirect string) error {
		for _, uri := range strings.Split(registered, "\n") {
			if uri == redirect {
				return nil
			}
		}
		return errors.New("redirect_uri does not match registration")
	})
	m.SetExtractExtensionHandler(func(r *oauth2.TokenGenerateRequest, t oauth2.ExtendableTokenInfo) {
		v := t.GetExtension()
		if v == nil {
			v = url.Values{}
		}
		v.Set("resource", s.Resource)
		t.SetExtension(v)
	})
	s.protocol = server.NewServer(&server.Config{TokenType: "Bearer", AllowedResponseTypes: []oauth2.ResponseType{oauth2.Code}, AllowedGrantTypes: []oauth2.GrantType{oauth2.AuthorizationCode, oauth2.Refreshing}, AllowedCodeChallengeMethods: []oauth2.CodeChallengeMethod{oauth2.CodeChallengeS256}, ForcePKCE: true}, m)
	s.protocol.ClientInfoHandler = server.ClientFormHandler
	s.protocol.ResponseErrorHandler = func(_ context.Context, re *oauthErrors.Response) {
		if re.Error != oauthErrors.ErrServerError && re.StatusCode >= 400 && re.StatusCode < 500 {
			re.StatusCode = http.StatusBadRequest
		}
	}
	return s, nil
}
func (s *Server) Close() error { return s.db.Close() }
func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func oauthError(w http.ResponseWriter, code, msg string) {
	jsonResponse(w, 400, map[string]string{"error": code, "error_description": msg})
}
func (s *Server) allow(addr string) bool {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		addr = host
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	cut := now.Add(-time.Minute)
	// Bound the map even if many unauthenticated addresses visit registration.
	for k, times := range s.rates {
		if len(times) == 0 || times[len(times)-1].Before(cut) {
			delete(s.rates, k)
		}
	}
	if len(s.rates) > 1024 {
		return false
	}
	keep := s.rates[addr][:0]
	for _, v := range s.rates[addr] {
		if v.After(cut) {
			keep = append(keep, v)
		}
	}
	s.rates[addr] = keep
	if len(keep) >= 20 {
		return false
	}
	s.rates[addr] = append(keep, now)
	return true
}
func (s *Server) Metadata(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, 200, map[string]any{
		"issuer": s.Issuer, "authorization_endpoint": s.Issuer + "/admin/oauth/authorize", "token_endpoint": s.Issuer + "/oauth/token", "registration_endpoint": s.Issuer + "/oauth/register", "revocation_endpoint": s.Issuer + "/oauth/revoke",
		"response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "token_endpoint_auth_methods_supported": []string{"none"}, "code_challenge_methods_supported": []string{"S256"}, "scopes_supported": []string{Scope}, "authorization_response_iss_parameter_supported": true,
	})
}
func (s *Server) ResourceMetadata(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, 200, map[string]any{"resource": s.Resource, "authorization_servers": []string{s.Issuer}, "scopes_supported": []string{Scope}, "bearer_methods_supported": []string{"header"}, "resource_name": "WhatsApp MCP"})
}
func validRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || strings.Contains(raw, "*") {
		return false
	}
	return u.Scheme == "https" || u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")
}
func (s *Server) Register(w http.ResponseWriter, r *http.Request) {
	if !s.allow(r.RemoteAddr) {
		jsonResponse(w, 429, map[string]string{"error": "slow_down"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var c struct {
		Name      string   `json:"client_name"`
		Redirects []string `json:"redirect_uris"`
		Method    string   `json:"token_endpoint_auth_method"`
		Grants    []string `json:"grant_types"`
		Responses []string `json:"response_types"`
	}
	if json.NewDecoder(r.Body).Decode(&c) != nil || len(c.Redirects) == 0 || len(c.Redirects) > 8 || len(c.Name) > 80 || c.Method != "" && c.Method != "none" {
		oauthError(w, "invalid_client_metadata", "Use public-client authentication and exact HTTPS or loopback redirect URIs.")
		return
	}
	for _, g := range c.Grants {
		if g != "authorization_code" && g != "refresh_token" {
			oauthError(w, "invalid_client_metadata", "Unsupported grant type.")
			return
		}
	}
	for _, v := range c.Responses {
		if v != "code" {
			oauthError(w, "invalid_client_metadata", "Only authorization code responses are supported.")
			return
		}
	}
	for _, uri := range c.Redirects {
		if len(uri) > 2048 || !validRedirect(uri) {
			oauthError(w, "invalid_redirect_uri", "Redirect URI must be HTTPS or loopback HTTP without a fragment.")
			return
		}
	}
	id, err := randomToken()
	if err != nil {
		oauthError(w, "server_error", "Registration failed.")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var count int
	if s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM clients").Scan(&count) != nil || count >= 256 {
		oauthError(w, "temporarily_unavailable", "Client registration limit reached.")
		return
	}
	name := strings.TrimSpace(c.Name)
	if name == "" {
		name = "MCP client"
	}
	info := clientInfo{ID: id, Name: name, Redirects: c.Redirects}
	raw, _ := json.Marshal(info)
	if _, err = s.db.ExecContext(r.Context(), "INSERT INTO clients(id,data) VALUES(?,?)", id, raw); err != nil {
		oauthError(w, "server_error", "Registration failed.")
		return
	}
	jsonResponse(w, 201, map[string]any{"client_id": id, "client_name": name, "redirect_uris": c.Redirects, "token_endpoint_auth_method": "none", "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}})
}

// Begin validates the full request before it can become an owner-visible consent.
func (s *Server) Begin(r *http.Request) (string, error) {
	if !s.allow(r.RemoteAddr) {
		return "", errors.New("too many authorization attempts; wait a minute")
	}
	if len(r.URL.RawQuery) > 8192 {
		return "", errors.New("authorization request is too large")
	}
	q := r.URL.Query()
	for _, key := range []string{"client_id", "redirect_uri", "response_type", "resource", "scope", "state", "code_challenge", "code_challenge_method"} {
		if len(q[key]) > 1 {
			return "", errors.New("duplicate authorization parameter")
		}
	}
	if q.Get("resource") != s.Resource || q.Get("scope") != "" && q.Get("scope") != Scope || !challengeRe.MatchString(q.Get("code_challenge")) || q.Get("code_challenge_method") != "S256" || len(q.Get("state")) > 1024 {
		return "", errors.New("invalid resource, scope, or PKCE challenge")
	}
	req, err := s.protocol.ValidationAuthorizeRequest(r)
	if err != nil {
		return "", errors.New("invalid authorization request")
	}
	c, err := s.client(r.Context(), req.ClientID)
	if err != nil {
		return "", errors.New("client is not registered")
	}
	matched := false
	for _, v := range c.Redirects {
		if v == req.RedirectURI {
			matched = true
		}
	}
	if !matched {
		return "", errors.New("redirect_uri does not match registration")
	}
	req.Scope = Scope
	id, err := randomToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, v := range s.pending {
		if now.After(v.Expires) {
			delete(s.pending, k)
		}
	}
	if len(s.pending) >= 64 {
		return "", errors.New("too many pending authorizations")
	}
	// Do not retain the browser request and its headers in memory.
	req.Request = nil
	s.pending[id] = pending{Request: req, Expires: now.Add(10 * time.Minute), Client: *c}
	return id, nil
}
func (s *Server) RequestInfo(id string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[id]
	if !ok || time.Now().After(p.Expires) {
		return nil, errors.New("this authorization expired; start sign-in again from your client")
	}
	return map[string]string{"client_name": p.Client.Name, "redirect_uri": p.Request.RedirectURI, "scope": Scope}, nil
}
func (s *Server) Decide(ctx context.Context, id, decision string) (string, error) {
	if decision != "approve" && decision != "deny" {
		return "", errors.New("choose approve or deny")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[id]
	if !ok || time.Now().After(p.Expires) {
		return "", errors.New("this authorization expired or was already used; start sign-in again from your client")
	}
	delete(s.pending, id)
	target, _ := url.Parse(p.Request.RedirectURI)
	q := target.Query()
	for _, key := range []string{"code", "error", "error_description", "state", "iss"} {
		q.Del(key)
	}
	q.Set("state", p.Request.State)
	q.Set("iss", s.Issuer)
	if decision == "deny" {
		q.Set("error", "access_denied")
	} else {
		grant, err := randomToken()
		if err != nil {
			return "", err
		}
		now := time.Now()
		if _, err = s.db.ExecContext(ctx, "INSERT INTO grants(id,client_id,created,expires,resource) VALUES(?,?,?,?,?)", grant, p.Client.ID, now.Unix(), now.Add(grantTTL).Unix(), s.Resource); err != nil {
			return "", err
		}
		p.Request.UserID = grant
		token, err := s.protocol.GetAuthorizeToken(ctx, p.Request)
		if err != nil {
			_, _ = s.db.ExecContext(ctx, "UPDATE grants SET revoked=1 WHERE id=?", grant)
			return "", err
		}
		q.Set("code", token.GetCode())
	}
	target.RawQuery = q.Encode()
	return target.String(), nil
}
func (s *Server) Token(w http.ResponseWriter, r *http.Request) {
	if !s.allow(r.RemoteAddr) {
		jsonResponse(w, 429, map[string]string{"error": "slow_down"})
		return
	}
	if r.URL.RawQuery != "" {
		oauthError(w, "invalid_request", "Use form-encoded POST parameters.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if r.ParseForm() != nil {
		oauthError(w, "invalid_request", "Invalid token request.")
		return
	}
	for _, v := range r.PostForm {
		if len(v) > 1 {
			oauthError(w, "invalid_request", "Duplicate token parameter.")
			return
		}
	}
	if r.PostForm.Get("resource") != s.Resource || r.PostForm.Get("client_secret") != "" || r.Header.Get("Authorization") != "" {
		oauthError(w, "invalid_target", "Use this server's resource and public-client authentication.")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := tokenStore{s}
	grant := r.PostForm.Get("grant_type")
	if _, err := s.client(r.Context(), r.PostForm.Get("client_id")); err != nil {
		oauthError(w, "invalid_client", "Client is not registered.")
		return
	}
	if grant == "authorization_code" {
		info, err := t.GetByCode(r.Context(), r.PostForm.Get("code"))
		if err != nil {
			oauthError(w, "server_error", "Code lookup failed.")
			return
		}
		if info == nil || info.GetClientID() != r.PostForm.Get("client_id") || info.GetRedirectURI() != r.PostForm.Get("redirect_uri") {
			oauthError(w, "invalid_grant", "Invalid authorization code, client, or redirect URI.")
			return
		}
		if !verifierRe.MatchString(r.PostForm.Get("code_verifier")) {
			oauthError(w, "invalid_grant", "A valid PKCE verifier is required.")
			return
		}
	} else if grant == "refresh_token" {
		info, err := t.GetByRefresh(r.Context(), r.PostForm.Get("refresh_token"))
		if err != nil {
			oauthError(w, "server_error", "Token lookup failed.")
			return
		}
		if info == nil {
			var family string
			if s.db.QueryRowContext(r.Context(), "SELECT grant_id FROM spent_refresh WHERE hash=?", digest(r.PostForm.Get("refresh_token"))).Scan(&family) == nil {
				_, _ = s.db.ExecContext(r.Context(), "UPDATE grants SET revoked=1 WHERE id=?", family)
			}
			oauthError(w, "invalid_grant", "Refresh token expired, was revoked, or was already used.")
			return
		}
		if info.GetClientID() != r.PostForm.Get("client_id") || r.PostForm.Get("scope") != "" && r.PostForm.Get("scope") != Scope {
			oauthError(w, "invalid_grant", "Token does not belong to this client or scope.")
			return
		}
	}
	if err := s.protocol.HandleTokenRequest(w, r); err != nil {
		return
	}
}
func (s *Server) Validate(ctx context.Context, raw string) bool {
	if raw == "" {
		return false
	}
	info, err := s.manager.LoadAccessToken(ctx, raw)
	if err != nil || info == nil {
		return false
	}
	ext, ok := info.(oauth2.ExtendableTokenInfo)
	return ok && info.GetScope() == Scope && ext.GetExtension().Get("resource") == s.Resource
}
func (s *Server) RevokeToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if r.ParseForm() != nil {
		oauthError(w, "invalid_request", "Invalid revocation request.")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := tokenStore{s}
	raw := r.PostFormValue("token")
	info, _ := t.GetByAccess(r.Context(), raw)
	if info == nil {
		info, _ = t.GetByRefresh(r.Context(), raw)
	}
	if info != nil && info.GetClientID() == r.PostFormValue("client_id") {
		_, _ = s.db.ExecContext(r.Context(), "UPDATE grants SET revoked=1 WHERE id=?", info.GetUserID())
	}
	jsonResponse(w, 200, map[string]any{})
}
func (s *Server) Challenge() string {
	return fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp", scope="%s"`, s.Issuer, Scope)
}
