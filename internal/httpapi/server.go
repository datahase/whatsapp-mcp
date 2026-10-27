// Package httpapi serves MCP, authenticated media transfers, health and the
// small admin/pairing page from one listener.
package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/datahase/whatsapp-mcp/internal/accounts"
	"github.com/datahase/whatsapp-mcp/internal/ai"
	"github.com/datahase/whatsapp-mcp/internal/authflow"
	wamcp "github.com/datahase/whatsapp-mcp/internal/mcp"
)

// Config wires the HTTP surface.
type Config struct {
	OAuth         *authflow.Server
	OAuthOnly     bool
	Token         string
	AdminPassword string
	AllowedHosts  []string
	MaxUpload     int64
	Manager       *accounts.Manager
	MCP           http.Handler
	AI            *ai.Service
	Version       string
	Log           waLog.Logger
	// DataDir is where one-time sign-in codes are read from.
	DataDir string
}

// Server is the HTTP handler.
type Server struct {
	cfg      Config
	mux      *http.ServeMux
	allowed  map[string]bool
	sessions sessionStore
	logins   loginLimiter
}

// New builds the handler.
func New(cfg Config) *Server {
	s := &Server{cfg: cfg, mux: http.NewServeMux(), allowed: map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}}
	for _, h := range cfg.AllowedHosts {
		s.allowed[strings.ToLower(strings.Trim(h, "[]"))] = true
	}
	s.sessions.m = map[string]*session{}
	s.logins.m = map[string][]time.Time{}

	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, "ok\n")
	})
	s.mux.Handle("/mcp", s.bearer(http.MaxBytesHandler(cfg.MCP, 4<<20)))
	s.mux.Handle("POST /api/accounts/{account}/media", s.bearer(http.HandlerFunc(s.upload)))
	s.mux.Handle("GET /api/accounts/{account}/media/{media}", s.bearer(http.HandlerFunc(s.download)))
	s.mux.Handle("GET /api/status", s.bearer(http.HandlerFunc(s.status)))
	s.mountAdmin()
	s.mountOAuth()
	s.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/admin", http.StatusSeeOther) })
	return s
}

// ServeHTTP applies the Host and Origin checks before any routing.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Header.Del(wamcp.OriginHeader) // only this server sets it
	if r.URL.Path == "/healthz" {
		s.mux.ServeHTTP(w, r)
		return
	}
	host := hostOnly(r.Host)
	if !s.allowed[host] {
		http.Error(w, "host not allowed; add it to allowed_hosts", http.StatusMisdirectedRequest)
		return
	}
	// "Origin: null" on a same-origin navigation (some browsers and privacy
	// modes send it) carries no cross-site risk; Sec-Fetch-Site confirms it.
	if r.Header.Get("Origin") == "null" && r.Header.Get("Sec-Fetch-Site") == "same-origin" {
		r.Header.Del("Origin")
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || !s.allowed[strings.ToLower(u.Hostname())] {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	if s.cfg.OAuth != nil {
		issuer, _ := url.Parse(s.cfg.OAuth.Issuer)
		if issuer.Scheme == "https" && strings.HasPrefix(r.URL.Path, "/admin") && (scheme != "https" || r.Host != issuer.Host) {
			if r.Method == http.MethodGet {
				http.Redirect(w, r, s.cfg.OAuth.Issuer+r.URL.RequestURI(), http.StatusSeeOther)
			} else {
				http.Error(w, "Use the HTTPS admin address", http.StatusBadRequest)
			}
			return
		}
		if strings.HasPrefix(r.URL.Path, "/oauth/") && issuer.Scheme == "https" && (scheme != "https" || r.Host != issuer.Host) {
			http.Error(w, "OAuth requires the configured HTTPS address", http.StatusBadRequest)
			return
		}
	}
	r.Header.Set(wamcp.OriginHeader, scheme+"://"+r.Host)
	s.mux.ServeHTTP(w, r)
}

func hostOnly(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.ToLower(strings.Trim(h, "[]"))
}

func equalSecret(got, want string) bool {
	a, b := sha256.Sum256([]byte(got)), sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1 && want != ""
}

func (s *Server) bearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		valid := ok && !s.cfg.OAuthOnly && equalSecret(got, s.cfg.Token)
		if !valid && ok && s.cfg.OAuth != nil {
			valid = s.cfg.OAuth.Validate(r.Context(), got)
		}
		if !valid {
			challenge := `Bearer realm="whatsapp-mcp"`
			if s.cfg.OAuth != nil {
				challenge = s.cfg.OAuth.Challenge()
			}
			w.Header().Set("WWW-Authenticate", challenge)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) acct(w http.ResponseWriter, r *http.Request) *accounts.Account {
	a, err := s.cfg.Manager.Get(r.PathValue("account"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return nil
	}
	return a
}

// upload streams one file into the account's staged media. It accepts a
// multipart form field named "file" or a raw body (filename via the
// `filename` query parameter). Nothing is buffered whole in memory.
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	a := s.acct(w, r)
	if a == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUpload+1<<20)
	var src io.Reader
	filename := r.URL.Query().Get("filename")
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt == "multipart/form-data" {
		mr, err := r.MultipartReader()
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid multipart body"})
			return
		}
		for {
			p, err := mr.NextPart()
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "multipart field \"file\" is required"})
				return
			}
			if p.FormName() == "file" {
				src, filename = p, p.FileName()
				break
			}
		}
	} else {
		src = r.Body
	}
	obj, err := a.StageUpload(r.Context(), src, filename)
	if err != nil {
		code := http.StatusBadRequest
		var mbe *http.MaxBytesError
		if errors.Is(err, accounts.ErrTooLarge) || errors.As(err, &mbe) {
			code = http.StatusRequestEntityTooLarge
		}
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"account_id": a.ID, "media": obj,
		"next": "pass media_id to send_media or send_voice_note with the same account_id"})
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	a := s.acct(w, r)
	if a == nil {
		return
	}
	obj, path, err := a.OpenMedia(r.PathValue("media"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "media file unavailable"})
		return
	}
	defer f.Close()
	name := obj.Filename
	if name == "" {
		name = obj.ID
	}
	w.Header().Set("Content-Type", obj.MIME)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Media-SHA256", obj.SHA256)
	http.ServeContent(w, r, "", obj.CreatedAt, f)
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"version": s.cfg.Version, "accounts": s.cfg.Manager.Statuses(), "analysis_enabled": s.cfg.AI != nil}
	if s.cfg.AI != nil {
		out["analysis_usage_today"] = s.cfg.AI.UsageToday()
		out["pending_provider_cleanups"] = s.cfg.AI.PendingCleanups()
	}
	writeJSON(w, http.StatusOK, out)
}

// -- sessions -----------------------------------------------------------------

const sessionTTL = 12 * time.Hour

type session struct {
	csrf    string
	expires time.Time
}

type sessionStore struct {
	mu sync.Mutex
	m  map[string]*session
}

func (st *sessionStore) create() (id string, s *session) {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := time.Now()
	for k, v := range st.m {
		if v.expires.Before(now) {
			delete(st.m, k)
		}
	}
	id = randHex(32)
	s = &session{csrf: randHex(32), expires: now.Add(sessionTTL)}
	st.m[id] = s
	return id, s
}

func (st *sessionStore) get(id string) *session {
	st.mu.Lock()
	defer st.mu.Unlock()
	s := st.m[id]
	if s == nil || s.expires.Before(time.Now()) {
		delete(st.m, id)
		return nil
	}
	return s
}

func (st *sessionStore) drop(id string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	delete(st.m, id)
}

// loginLimiter allows five failed logins per client address per 15 minutes.
type loginLimiter struct {
	mu sync.Mutex
	m  map[string][]time.Time
}

func (l *loginLimiter) blocked(addr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := time.Now().Add(-15 * time.Minute)
	var keep []time.Time
	for _, t := range l.m[addr] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	l.m[addr] = keep
	return len(keep) >= 5
}

func (l *loginLimiter) fail(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.m[addr] = append(l.m[addr], time.Now())
}

func clientAddr(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := randRead(b); err != nil {
		panic(fmt.Sprintf("crypto/rand: %v", err))
	}
	return fmt.Sprintf("%x", b)
}
