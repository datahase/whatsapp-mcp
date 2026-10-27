package httpapi

import (
	"net/http"
	"net/url"
)

func (s *Server) mountOAuth() {
	if s.cfg.OAuth == nil {
		return
	}
	o := s.cfg.OAuth
	s.mux.HandleFunc("GET /.well-known/oauth-authorization-server", o.Metadata)
	s.mux.HandleFunc("GET /.well-known/oauth-protected-resource", o.ResourceMetadata)
	s.mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", o.ResourceMetadata)
	s.mux.HandleFunc("POST /oauth/register", o.Register)
	s.mux.HandleFunc("POST /oauth/token", o.Token)
	s.mux.HandleFunc("POST /oauth/revoke", o.RevokeToken)
	s.mux.HandleFunc("GET /admin/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		secureHeaders(w)
		id, err := o.Begin(r)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid_request", "error_description": err.Error()})
			return
		}
		target := "/admin?authorize=" + url.QueryEscape(id)
		cookie, err := r.Cookie(cookieName)
		if err != nil || s.sessions.get(cookie.Value) == nil {
			target = "/admin/login?authorize=" + url.QueryEscape(id)
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
	})
	s.mux.HandleFunc("GET /admin/api/oauth/requests/{request}", s.admin(func(w http.ResponseWriter, r *http.Request, _ *session) {
		info, err := o.RequestInfo(r.PathValue("request"))
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, info)
	}))
	s.mux.HandleFunc("POST /admin/api/oauth/requests/{request}", s.admin(func(w http.ResponseWriter, r *http.Request, _ *session) {
		if r.ParseForm() != nil || len(r.PostForm["decision"]) != 1 {
			writeJSON(w, 400, map[string]string{"error": "Choose exactly one approval decision."})
			return
		}
		target, err := o.Decide(r.Context(), r.PathValue("request"), r.PostFormValue("decision"))
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]string{"redirect": target})
	}))
	s.mux.HandleFunc("GET /admin/api/oauth/connections", s.admin(func(w http.ResponseWriter, r *http.Request, _ *session) {
		grants, err := o.Grants(r.Context())
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": "Could not load connections."})
			return
		}
		writeJSON(w, 200, grants)
	}))
	s.mux.HandleFunc("POST /admin/api/oauth/connections/{connection}/revoke", s.admin(func(w http.ResponseWriter, r *http.Request, _ *session) {
		if err := o.Revoke(r.Context(), r.PathValue("connection")); err != nil {
			writeJSON(w, 500, map[string]string{"error": "Could not revoke connection."})
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	}))
}
