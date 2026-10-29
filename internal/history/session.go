package history

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/datahase/whatsapp-mcp/internal/store"
)

func (s *Service) CreateSession(ctx context.Context, scope Scope, externalConsent bool) (Session, error) {
	st, err := s.authorize(ctx, scope)
	if err != nil {
		return Session{}, err
	}
	scope.ChatIDs = canonical(scope.ChatIDs)
	now := time.Now().Unix()
	v := Session{ID: store.NewID("hses_"), Scope: scope, ExternalConsent: externalConsent, ScopeEpoch: epoch(st.DB(), "scope_epoch"), Expires: now + int64(s.cfg.RetentionDays)*86400}
	raw, _ := json.Marshal(v.ChatIDs)
	_, err = st.DB().ExecContext(ctx, "INSERT INTO history_sessions(id,chats_json,scope_epoch,external_consent,created,expires) VALUES(?,?,?,?,?,?)", v.ID, string(raw), v.ScopeEpoch, v.ExternalConsent, now, v.Expires)
	return v, err
}
func (s *Service) Session(ctx context.Context, account, id string) (Session, error) {
	st, err := s.db(ctx, account)
	if err != nil {
		return Session{}, err
	}
	v := Session{ID: id, Scope: Scope{AccountID: account}}
	var raw string
	if st.DB().QueryRowContext(ctx, "SELECT chats_json,scope_epoch,external_consent,expires FROM history_sessions WHERE id=? AND expires>?", id, time.Now().Unix()).Scan(&raw, &v.ScopeEpoch, &v.ExternalConsent, &v.Expires) != nil {
		return v, errors.New("history session not found or expired")
	}
	if json.Unmarshal([]byte(raw), &v.ChatIDs) != nil {
		return v, ErrScope
	}
	if _, err = s.authorize(ctx, v.Scope); err != nil {
		return Session{}, err
	}
	if v.ScopeEpoch != epoch(st.DB(), "scope_epoch") {
		return Session{}, ErrScope
	}
	return v, nil
}
func (s *Service) Sessions(ctx context.Context, account string) ([]Session, error) {
	st, err := s.db(ctx, account)
	if err != nil {
		return nil, err
	}
	rows, err := st.DB().QueryContext(ctx, "SELECT id FROM history_sessions WHERE expires>? ORDER BY created DESC LIMIT 50", time.Now().Unix())
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	out := []Session{}
	for _, id := range ids {
		if v, e := s.Session(ctx, account, id); e == nil {
			out = append(out, v)
		}
	}
	return out, nil
}
func (s *Service) DeleteSession(ctx context.Context, account, id string) error {
	st, err := s.db(ctx, account)
	if err != nil {
		return err
	}
	if _, err = s.Session(ctx, account, id); err != nil {
		return err
	}
	tx, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query("SELECT id FROM history_jobs WHERE json_extract(request_json,'$.session_id')=?", id)
	if err != nil {
		return err
	}
	jobIDs := []string{}
	for rows.Next() {
		var jobID string
		if err = rows.Scan(&jobID); err != nil {
			rows.Close()
			return err
		}
		jobIDs = append(jobIDs, jobID)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	_, err = tx.Exec("DELETE FROM history_turns WHERE session_id=?", id)
	if err != nil {
		return err
	}
	_, err = tx.Exec("DELETE FROM history_jobs WHERE json_extract(request_json,'$.session_id')=?", id)
	if err != nil {
		return err
	}
	_, err = tx.Exec("DELETE FROM history_review_pages WHERE session_id=?", id)
	if err != nil {
		return err
	}
	_, err = tx.Exec("DELETE FROM history_sessions WHERE id=?", id)
	if err != nil {
		return err
	}
	_, err = tx.Exec("DELETE FROM analysis_jobs WHERE task='history_answer' AND json_extract(request_json,'$.history_session')=?", id)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.mu.Lock()
	for _, jobID := range jobIDs {
		key := account + "/" + jobID
		delete(s.guards, key)
		if cancel := s.running[key]; cancel != nil {
			cancel()
		}
	}
	s.mu.Unlock()
	return nil
}

// ReviewPage is retrieval-only: callers can review every cached message in
// chronology, without a server-side model call or a top-K completeness claim.
type ReviewPage struct {
	Sources    []Source `json:"sources"`
	NextCursor string   `json:"next_cursor,omitempty"`
	Complete   bool     `json:"complete_for_cached_snapshot"`
	Coverage   Coverage `json:"coverage"`
	Warnings   []string `json:"warnings"`
}
type reviewCursor struct {
	Session string `json:"session"`
	TS      int64  `json:"ts"`
	ID      string `json:"id"`
	Chat    string `json:"chat"`
	Epoch   int64  `json:"epoch"`
}

func (s *Service) Review(ctx context.Context, account, sessionID, cursor string) (ReviewPage, error) {
	v, err := s.Session(ctx, account, sessionID)
	if err != nil {
		return ReviewPage{}, err
	}
	st, err := s.authorize(ctx, v.Scope)
	if err != nil {
		return ReviewPage{}, err
	}
	r := reviewCursor{Session: sessionID, Epoch: epoch(st.DB(), "content_epoch")}
	if cursor != "" {
		var stored string
		if len(cursor) > 100 || st.DB().QueryRowContext(ctx, "SELECT cursor_json FROM history_review_pages WHERE id=? AND session_id=?", cursor, sessionID).Scan(&stored) != nil || json.Unmarshal([]byte(stored), &r) != nil || r.Session != sessionID || r.Epoch != epoch(st.DB(), "content_epoch") {
			return ReviewPage{}, errors.New("review snapshot changed; restart the chronological review")
		}
	}
	src, ts, id, chat, n, err := s.chronological(ctx, v.Scope, r.TS, r.ID, r.Chat, 20)
	if err != nil {
		return ReviewPage{}, err
	}
	cov, err := s.Coverage(ctx, v.Scope)
	if err != nil {
		return ReviewPage{}, err
	}
	if cov.ContentEpoch != r.Epoch {
		return ReviewPage{}, errors.New("history changed during review; restart")
	}
	page := ReviewPage{Sources: src, Complete: n < 20, Coverage: cov, Warnings: []string{"Review covers only cached text and existing transcripts; missing history or audio may contain additional actions."}}
	if !page.Complete {
		b, _ := json.Marshal(reviewCursor{sessionID, ts, id, chat, r.Epoch})
		page.NextCursor = store.NewID("hpage_")
		if _, err = st.DB().ExecContext(ctx, "INSERT INTO history_review_pages(id,session_id,cursor_json,created) VALUES(?,?,?,?)", page.NextCursor, sessionID, string(b), time.Now().Unix()); err != nil {
			return ReviewPage{}, err
		}
	}
	s.remember(st, src)
	return page, nil
}
