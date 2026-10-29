package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/datahase/whatsapp-mcp/internal/history"
	wamcp "github.com/datahase/whatsapp-mcp/internal/mcp"
	"github.com/skip2/go-qrcode"
)

var randRead = rand.Read

const cookieName = "wamcp_admin"

//go:embed all:static*
var staticFiles embed.FS

func (s *Server) mountAdmin() {
	sub, _ := fs.Sub(staticFiles, "static")
	s.mux.Handle("GET /admin/_next/", http.StripPrefix("/admin/", http.FileServerFS(sub)))
	s.mux.Handle("GET /admin/brand/", http.StripPrefix("/admin/", http.FileServerFS(sub)))
	s.mux.HandleFunc("GET /admin/login", s.loginPage)
	s.mux.HandleFunc("POST /admin/login", s.loginSubmit)
	s.mux.HandleFunc("POST /admin/logout", s.admin(s.adminLogout))
	s.mux.HandleFunc("GET /admin", s.admin(s.adminShell))
	s.mux.HandleFunc("GET /admin/accounts/{account}", s.admin(s.adminShell))
	s.mux.HandleFunc("GET /admin/api/session", s.admin(s.adminSession))
	s.mux.HandleFunc("POST /admin/api/accounts", s.admin(s.addAccount))
	s.mux.HandleFunc("GET /admin/accounts/{account}/state", s.admin(s.accountState))
	s.mux.HandleFunc("GET /admin/accounts/{account}/qr.png", s.admin(s.qrPNG))
	s.mux.HandleFunc("POST /admin/accounts/{account}/pair", s.admin(s.pair))
	s.mux.HandleFunc("POST /admin/accounts/{account}/unlink", s.admin(s.unlink))
	s.mux.HandleFunc("POST /admin/accounts/{account}/reset", s.admin(s.reset))
}

func secureHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	hashes, _ := staticFiles.ReadFile("static/script-hashes.txt")
	h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data:; font-src 'self'; style-src 'self'; script-src 'self' "+strings.TrimSpace(string(hashes))+"; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
}

type adminHandler func(http.ResponseWriter, *http.Request, *session)

func (s *Server) admin(h adminHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		secureHeaders(w)
		c, err := r.Cookie(cookieName)
		var sess *session
		if err == nil {
			sess = s.sessions.get(c.Value)
		}
		if sess == nil {
			if strings.HasPrefix(r.URL.Path, "/admin/api/") || strings.Contains(r.Header.Get("Accept"), "application/json") {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Sign in to continue."})
				return
			}
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		if r.Method == http.MethodPost {
			limit := int64(16 << 10)
			if strings.HasPrefix(r.URL.Path, "/admin/api/history/") {
				limit = 128 << 10
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			csrf := r.Header.Get("X-CSRF-Token")
			if csrf == "" {
				csrf = r.PostFormValue("csrf")
			}
			if subtle.ConstantTimeCompare([]byte(csrf), []byte(sess.csrf)) != 1 {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "This form expired. Reload the page and try again."})
				return
			}
		}
		h(w, r.WithContext(history.WithGuard(r.Context(), func() bool { return s.sessions.get(c.Value) != nil })), sess)
	}
}

func serveAdminShell(w http.ResponseWriter) {
	secureHeaders(w)
	raw, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "Admin UI is not built. Run pnpm --dir web build before building Go.", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(raw)
}
func (s *Server) adminShell(w http.ResponseWriter, r *http.Request, _ *session) { serveAdminShell(w) }
func (s *Server) loginPage(w http.ResponseWriter, r *http.Request)              { serveAdminShell(w) }
func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	secureHeaders(w)
	fail := func(code int, msg string) { writeJSON(w, code, map[string]string{"error": msg}) }
	addr := clientAddr(r)
	if s.logins.blocked(addr) {
		fail(http.StatusTooManyRequests, "Too many attempts. Wait 15 minutes, then try again.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	ok := false
	if code := r.PostFormValue("code"); code != "" {
		ok = consumeLoginCode(s.cfg.DataDir, code)
	} else if pw := r.PostFormValue("password"); pw != "" {
		ok = equalSecret(pw, s.cfg.AdminPassword)
	}
	if !ok {
		s.logins.fail(addr)
		fail(http.StatusUnauthorized, "That code or password did not work. Generate a new code and try again.")
		return
	}
	id, _ := s.sessions.create()
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: id, Path: "/admin", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"), MaxAge: int(sessionTTL.Seconds())})
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}
func (s *Server) adminLogout(w http.ResponseWriter, r *http.Request, _ *session) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.sessions.drop(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/admin", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}
func (s *Server) adminSession(w http.ResponseWriter, r *http.Request, sess *session) {
	endpoint := r.Header.Get(wamcp.OriginHeader) + "/mcp"
	if s.cfg.OAuth != nil {
		endpoint = s.cfg.OAuth.Resource
	}
	token := ""
	if !s.cfg.OAuthOnly {
		token = s.cfg.Token
	}
	writeJSON(w, http.StatusOK, map[string]any{"csrf": sess.csrf, "accounts": s.cfg.Manager.Statuses(), "endpoint": endpoint, "token": token, "version": s.cfg.Version, "oauth_enabled": s.cfg.OAuth != nil, "oauth_only": s.cfg.OAuthOnly})
}
func (s *Server) addAccount(w http.ResponseWriter, r *http.Request, _ *session) {
	a, err := s.cfg.Manager.Add(r.PostFormValue("label"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, a.Status())
}
func (s *Server) accountState(w http.ResponseWriter, r *http.Request, _ *session) {
	a, err := s.cfg.Manager.Get(r.PathValue("account"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p := a.Pairing()
	writeJSON(w, http.StatusOK, map[string]any{"account": a.Status(), "state": p.State, "qr": p.QR != "", "pair_code": p.PairCode, "t": time.Now().Unix()})
}

func (s *Server) qrPNG(w http.ResponseWriter, r *http.Request, _ *session) {
	a, err := s.cfg.Manager.Get(r.PathValue("account"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	qr := a.Pairing().QR
	if qr == "" {
		http.NotFound(w, r)
		return
	}
	png, err := qrcode.Encode(qr, qrcode.Medium, 464)
	if err != nil {
		http.Error(w, "qr encode failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(png)
}

func back(w http.ResponseWriter, r *http.Request, id, msg string) {
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		if msg != "" && (strings.HasPrefix(msg, "Could not") || strings.HasPrefix(msg, "Logout failed") || strings.HasPrefix(msg, "Reset failed")) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"message": msg})
		return
	}
	http.Redirect(w, r, "/admin/accounts/"+id+"?msg="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (s *Server) pair(w http.ResponseWriter, r *http.Request, _ *session) {
	a, err := s.cfg.Manager.Get(r.PathValue("account"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	phone := strings.TrimPrefix(strings.TrimSpace(r.PostFormValue("phone")), "+")
	if err := a.StartPairing(phone); err != nil {
		back(w, r, a.ID, "Could not start linking: "+err.Error())
		return
	}
	back(w, r, a.ID, "")
}

func (s *Server) unlink(w http.ResponseWriter, r *http.Request, _ *session) {
	a, err := s.cfg.Manager.Get(r.PathValue("account"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := a.Logout(ctx); err != nil {
		back(w, r, a.ID, "Logout failed: "+err.Error())
		return
	}
	back(w, r, a.ID, "Logged out. The device is removed from the phone.")
}

func (s *Server) reset(w http.ResponseWriter, r *http.Request, _ *session) {
	a, err := s.cfg.Manager.Get(r.PathValue("account"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if r.PostFormValue("confirm") != a.ID {
		back(w, r, a.ID, "Could not reset: type the account ID exactly to confirm.")
		return
	}
	if _, err := a.Reset(); err != nil {
		back(w, r, a.ID, "Reset failed: "+err.Error())
		return
	}
	back(w, r, a.ID, "Slot reset. The previous data is archived in the data volume.")
}
