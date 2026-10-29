package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/datahase/whatsapp-mcp/internal/accounts"
	"github.com/datahase/whatsapp-mcp/internal/history"
)

func (s *Server) mountHistory() {
	s.mux.HandleFunc("GET /admin/api/history/{account}/status", s.admin(s.historyStatus))
	s.mux.HandleFunc("POST /admin/api/history/{account}/{operation}", s.admin(s.historyOperation))
}
func (s *Server) historyStatus(w http.ResponseWriter, r *http.Request, _ *session) {
	id := r.PathValue("account")
	a, err := s.cfg.Manager.Get(id)
	if err != nil || a.State() == accounts.StateDisabled {
		writeJSON(w, 404, map[string]string{"error": "Account unavailable"})
		return
	}
	st, err := a.Store()
	if err != nil {
		writeJSON(w, 409, map[string]string{"error": "Account cache unavailable"})
		return
	}
	chats, err := st.ListChats(r.Context(), "", 1000, 0, false, "last_message_time")
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "Could not list cached chats"})
		return
	}
	options := map[string]any{"enabled": false}
	if s.cfg.History != nil {
		options = s.cfg.History.Options()
	}
	out := map[string]any{"options": options, "chats": chats, "onboarding_dismissed": st.MetaGet("history_onboarding") != "", "selection": history.Selection{Mode: "none", ChatIDs: []string{}}, "jobs": []history.Job{}, "sessions": []history.Session{}}
	if s.cfg.History.Enabled() {
		sel, e := s.cfg.History.Selection(r.Context(), id)
		if e != nil {
			writeJSON(w, 409, map[string]string{"error": e.Error()})
			return
		}
		out["selection"] = sel
		if len(sel.ChatIDs) > 0 {
			cov, e := s.cfg.History.Coverage(r.Context(), history.Scope{AccountID: id, ChatIDs: sel.ChatIDs})
			if e == nil {
				out["coverage"] = cov
			}
		}
		rows, e := st.DB().QueryContext(r.Context(), "SELECT id FROM history_jobs ORDER BY created DESC,id DESC LIMIT 8")
		ids := []string{}
		if e == nil {
			for rows.Next() {
				var x string
				_ = rows.Scan(&x)
				ids = append(ids, x)
			}
			rows.Close()
		}
		jobs := []history.Job{}
		for _, x := range ids {
			if j, e := s.cfg.History.Job(r.Context(), id, x); e == nil {
				jobs = append(jobs, j)
			}
		}
		out["jobs"] = jobs
		out["sessions"], _ = s.cfg.History.Sessions(r.Context(), id)
	}
	writeJSON(w, 200, out)
}
func (s *Server) historyOperation(w http.ResponseWriter, r *http.Request, _ *session) {
	if r.PathValue("operation") == "dismiss" {
		a, e := s.cfg.Manager.Get(r.PathValue("account"))
		if e != nil {
			writeJSON(w, 404, map[string]string{"error": "Account unavailable"})
			return
		}
		st, e := a.Store()
		if e != nil {
			writeJSON(w, 409, map[string]string{"error": "Account cache unavailable"})
			return
		}
		e = st.MetaSet("history_onboarding", "dismissed")
		if e != nil {
			writeJSON(w, 500, map[string]string{"error": "Could not save preference"})
			return
		}
		writeJSON(w, 200, map[string]bool{"dismissed": true})
		return
	}
	if !s.cfg.History.Enabled() {
		writeJSON(w, 409, map[string]string{"error": history.ErrDisabled.Error()})
		return
	}
	var a struct {
		ChatIDs         []string `json:"chat_ids"`
		Mode            string   `json:"mode"`
		Query           string   `json:"query"`
		Question        string   `json:"question"`
		SourceID        string   `json:"source_id"`
		SessionID       string   `json:"session_id"`
		JobID           string   `json:"job_id"`
		Cursor          string   `json:"cursor"`
		ExternalConsent bool     `json:"external_consent"`
		RequestSync     bool     `json:"request_sync"`
		Sender          string   `json:"sender"`
		After           string   `json:"after"`
		Before          string   `json:"before"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&a) != nil {
		writeJSON(w, 400, map[string]string{"error": "Invalid history request"})
		return
	}
	id := r.PathValue("account")
	scope := history.Scope{AccountID: id, ChatIDs: a.ChatIDs}
	var result any
	var err error
	switch r.PathValue("operation") {
	case "selection":
		result, err = s.cfg.History.Select(r.Context(), id, a.Mode, a.ChatIDs)
	case "index":
		result, err = s.cfg.History.StartIndex(r.Context(), scope, a.RequestSync)
	case "search":
		result, err = s.cfg.History.Search(r.Context(), history.SearchRequest{Scope: scope, Query: a.Query, Sender: a.Sender, After: a.After, Before: a.Before, Limit: 8})
	case "context":
		result, err = s.cfg.History.Context(r.Context(), scope, a.SourceID)
	case "session":
		result, err = s.cfg.History.CreateSession(r.Context(), scope, a.ExternalConsent)
	case "delete-session":
		err = s.cfg.History.DeleteSession(r.Context(), id, a.SessionID)
		result = map[string]bool{"deleted": err == nil}
	case "ask":
		result, err = s.cfg.History.Ask(r.Context(), id, a.SessionID, a.Question, a.Mode)
	case "review":
		result, err = s.cfg.History.Review(r.Context(), id, a.SessionID, a.Cursor)
	case "resume":
		result, err = s.cfg.History.Resume(r.Context(), id, a.JobID)
	case "cancel":
		err = s.cfg.History.Cancel(r.Context(), id, a.JobID)
		result = map[string]bool{"cancelled": err == nil}
	default:
		writeJSON(w, 404, map[string]string{"error": "Unknown history operation"})
		return
	}
	if err != nil {
		message := err.Error()
		if strings.Contains(message, "SQL") {
			message = "History storage operation failed"
		}
		writeJSON(w, 400, map[string]string{"error": message})
		return
	}
	writeJSON(w, 200, result)
}
