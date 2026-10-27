package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"

	"github.com/datahase/whatsapp-mcp/internal/accounts"
)

var randRead = rand.Read

const cookieName = "wamcp_admin"

func (s *Server) mountAdmin() {
	s.mux.HandleFunc("GET /admin/login", s.loginPage)
	s.mux.HandleFunc("POST /admin/login", s.loginSubmit)
	s.mux.HandleFunc("POST /admin/logout", s.admin(s.adminLogout))
	s.mux.HandleFunc("GET /admin", s.admin(s.adminHome))
	s.mux.HandleFunc("GET /admin/accounts/{account}", s.admin(s.accountPage))
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
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
}

type adminHandler func(w http.ResponseWriter, r *http.Request, sess *session)

// admin requires a valid session; state-changing requests also need the
// session's CSRF token.
func (s *Server) admin(h adminHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		secureHeaders(w)
		c, err := r.Cookie(cookieName)
		var sess *session
		if err == nil {
			sess = s.sessions.get(c.Value)
		}
		if sess == nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		if r.Method == http.MethodPost {
			r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
			if subtle.ConstantTimeCompare([]byte(r.PostFormValue("csrf")), []byte(sess.csrf)) != 1 {
				http.Error(w, "invalid or missing CSRF token; reload the page", http.StatusForbidden)
				return
			}
		}
		h(w, r, sess)
	}
}

var page = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
{{if .Refresh}}<meta http-equiv="refresh" content="{{.Refresh}}">{{end}}
<title>{{.Title}} · whatsapp-mcp</title>
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:46rem;margin:2rem auto;padding:0 1rem;color:#1a1a1a}
table{border-collapse:collapse;width:100%}td,th{text-align:left;padding:.4rem;border-bottom:1px solid #ddd}
.err{color:#9b1c1c}.muted{color:#555}button{font:inherit;padding:.35rem .8rem}input{font:inherit;padding:.3rem}
form.inline{display:inline}img{image-rendering:pixelated}</style></head><body>
{{if .Authed}}<form class="inline" method="post" action="/admin/logout"><input type="hidden" name="csrf" value="{{.CSRF}}"><button>Sign out</button></form> <a href="/admin">Accounts</a>{{end}}
<h1>{{.Title}}</h1>
{{if .Flash}}<p role="status" class="{{.FlashClass}}">{{.Flash}}</p>{{end}}
{{.Body}}
</body></html>`))

type pageData struct {
	Title, CSRF, Flash, FlashClass string
	Refresh                        int
	Authed                         bool
	Body                           template.HTML
}

func render(w http.ResponseWriter, d pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = page.Execute(w, d)
}

func renderPart(t *template.Template, data any) template.HTML {
	var b strings.Builder
	_ = t.Execute(&b, data)
	return template.HTML(b.String()) // produced by html/template, already escaped
}

var loginForm = template.Must(template.New("l").Parse(`<form method="post" action="/admin/login">
<label for="pw">Admin password</label><br><input id="pw" name="password" type="password" autocomplete="current-password" required autofocus>
<button>Sign in</button></form>
<p class="muted">The password is the content of the admin password secret file.</p>`))

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	secureHeaders(w)
	render(w, pageData{Title: "Sign in", Body: renderPart(loginForm, nil)})
}

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	secureHeaders(w)
	addr := clientAddr(r)
	if s.logins.blocked(addr) {
		w.WriteHeader(http.StatusTooManyRequests)
		render(w, pageData{Title: "Sign in", Flash: "Too many attempts; wait 15 minutes.", FlashClass: "err", Body: renderPart(loginForm, nil)})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if !equalSecret(r.PostFormValue("password"), s.cfg.AdminPassword) {
		s.logins.fail(addr)
		w.WriteHeader(http.StatusUnauthorized)
		render(w, pageData{Title: "Sign in", Flash: "Incorrect password.", FlashClass: "err", Body: renderPart(loginForm, nil)})
		return
	}
	id, _ := s.sessions.create()
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: id, Path: "/admin", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"), MaxAge: int(sessionTTL.Seconds())})
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) adminLogout(w http.ResponseWriter, r *http.Request, _ *session) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.sessions.drop(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/admin", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

var homeT = template.Must(template.New("h").Parse(`<table><thead><tr><th scope="col">Account</th><th scope="col">State</th><th scope="col">Number</th><th scope="col"></th></tr></thead><tbody>
{{range .}}<tr><td>{{.Label}} <span class="muted">({{.AccountID}})</span></td><td>{{.State}}{{if .Error}}<br><span class="err">{{.Error}}</span>{{end}}</td>
<td>{{.Phone}}</td><td><a href="/admin/accounts/{{.AccountID}}">Manage</a></td></tr>{{end}}
</tbody></table>
<p class="muted">Accounts come from the configuration file. Add one there and restart the container.</p>`))

func (s *Server) adminHome(w http.ResponseWriter, r *http.Request, sess *session) {
	render(w, pageData{Title: "Accounts", Authed: true, CSRF: sess.csrf, Refresh: 15, Body: renderPart(homeT, s.cfg.Manager.Statuses())})
}

var acctT = template.Must(template.New("a").Parse(`<p>State: <strong>{{.Status.State}}</strong>{{if .Status.Phone}} · number {{.Status.Phone}}{{end}}</p>
{{if .Status.Error}}<p class="err">{{.Status.Error}}</p>{{end}}
{{if .Pairing}}
  {{if .QR}}<p>On the phone: WhatsApp → Settings → Linked devices → Link a device, then scan:</p>
  <img src="/admin/accounts/{{.Status.AccountID}}/qr.png?t={{.Now}}" width="256" height="256" alt="WhatsApp pairing QR code for {{.Status.Label}}">{{end}}
  {{if .Code}}<p>Or enter this code on the phone (Link with phone number instead): <strong>{{.Code}}</strong></p>{{end}}
  {{if not (or .QR .Code)}}<p>Waiting for WhatsApp…</p>{{end}}
  <p class="muted">This page refreshes every few seconds. Pairing stops after three minutes.</p>
{{else if .CanPair}}
  <form method="post" action="/admin/accounts/{{.Status.AccountID}}/pair"><input type="hidden" name="csrf" value="{{.CSRF}}">
  <button>Start pairing with QR code</button></form>
  <form method="post" action="/admin/accounts/{{.Status.AccountID}}/pair"><input type="hidden" name="csrf" value="{{.CSRF}}">
  <p><label for="phone">Or pair with a code; phone number with country code (digits only)</label><br>
  <input id="phone" name="phone" inputmode="numeric" pattern="[0-9]{6,15}" required> <button>Get pairing code</button></p></form>
{{end}}
{{if .CanUnlink}}<form method="post" action="/admin/accounts/{{.Status.AccountID}}/unlink"><input type="hidden" name="csrf" value="{{.CSRF}}">
<button>Log out this linked device</button> <span class="muted">Other accounts are not affected. Cached messages stay readable.</span></form>{{end}}
{{if .CanReset}}<form method="post" action="/admin/accounts/{{.Status.AccountID}}/reset"><input type="hidden" name="csrf" value="{{.CSRF}}">
<p><label for="confirm">Reset slot: archive all data for this account (needed to pair a different number). Type <code>{{.Status.AccountID}}</code> to confirm</label><br>
<input id="confirm" name="confirm" required> <button>Archive and reset</button></p></form>{{end}}`))

func (s *Server) accountPage(w http.ResponseWriter, r *http.Request, sess *session) {
	a, err := s.cfg.Manager.Get(r.PathValue("account"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	st := a.Status()
	p := a.Pairing()
	pairing := st.State == accounts.StatePairing
	d := map[string]any{"Status": st, "CSRF": sess.csrf, "Pairing": pairing, "QR": p.QR != "", "Code": p.PairCode, "Now": time.Now().Unix(),
		"CanPair":   st.State == accounts.StateUnpaired || st.State == accounts.StateLoggedOut || (st.State == accounts.StateError && st.Phone == ""),
		"CanUnlink": st.State == accounts.StateConnected || st.State == accounts.StateDisconnected || st.State == accounts.StateConnecting || (st.State == accounts.StateError && st.Phone != ""),
		"CanReset":  st.State == accounts.StateUnpaired || st.State == accounts.StateLoggedOut || st.State == accounts.StateError}
	refresh := 0
	if pairing || st.State == accounts.StateConnecting {
		refresh = 3
	}
	render(w, pageData{Title: st.Label, Authed: true, CSRF: sess.csrf, Refresh: refresh, Flash: r.URL.Query().Get("msg"), Body: renderPart(acctT, d)})
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
	png, err := qrcode.Encode(qr, qrcode.Medium, 256)
	if err != nil {
		http.Error(w, "qr encode failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(png)
}

func back(w http.ResponseWriter, r *http.Request, id, msg string) {
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
		back(w, r, a.ID, "Could not start pairing: "+err.Error())
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
	back(w, r, a.ID, "Logged out.")
}

func (s *Server) reset(w http.ResponseWriter, r *http.Request, _ *session) {
	a, err := s.cfg.Manager.Get(r.PathValue("account"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if r.PostFormValue("confirm") != a.ID {
		back(w, r, a.ID, "Reset not confirmed.")
		return
	}
	if _, err := a.Reset(); err != nil {
		back(w, r, a.ID, "Reset failed: "+err.Error())
		return
	}
	back(w, r, a.ID, "Slot reset; previous data archived under the data directory's archive folder.")
}
