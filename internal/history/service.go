package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/datahase/whatsapp-mcp/internal/accounts"
	"github.com/datahase/whatsapp-mcp/internal/ai"
	"github.com/datahase/whatsapp-mcp/internal/config"
	"github.com/datahase/whatsapp-mcp/internal/store"
)

type Resolver func(string) (*store.Store, error)
type Service struct {
	cfg      config.History
	resolve  Resolver
	embed    Embedder
	ai       *ai.Service
	generate func(context.Context, string, string, ai.GenRequest, func() error) (string, error)
	account  func(string) (*accounts.Account, error)
	ids      func() []string
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	mu       sync.Mutex
	wake     chan struct{}
	guards   map[string]context.Context
	running  map[string]context.CancelFunc
}

func New(cfg config.History, mgr *accounts.Manager, gen *ai.Service, dir string) *Service {
	resolve := func(id string) (*store.Store, error) {
		a, err := mgr.Get(id)
		if err != nil || a.State() == accounts.StateDisabled {
			return nil, ErrScope
		}
		return a.Store()
	}
	s := newWithEmbedder(cfg, resolve, func() []string {
		out := []string{}
		for _, a := range mgr.All() {
			out = append(out, a.ID)
		}
		return out
	}, NewLocalEmbedder(cfg, dir), false)
	s.ai = gen
	s.account = mgr.Get
	if gen != nil {
		s.generate = func(ctx context.Context, account, key string, request ai.GenRequest, guard func() error) (string, error) {
			a, err := mgr.Get(account)
			if err != nil {
				return "", err
			}
			v, err := gen.GenerateHistory(ctx, a, key, accountSession(ctx), request, guard)
			if err != nil {
				return "", err
			}
			for v.Status == store.JobQueued || v.Status == store.JobRunning {
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(300 * time.Millisecond):
				}
				if err = guard(); err != nil {
					return "", err
				}
				v, err = gen.GetJob(a, v.JobID)
				if err != nil {
					return "", err
				}
			}
			return ai.HistoryText(v)
		}
	}
	if cfg.Enabled {
		s.wg.Add(1)
		go s.worker()
	}
	return s
}
func NewWithEmbedder(cfg config.History, resolve Resolver, ids func() []string, e Embedder) *Service {
	return newWithEmbedder(cfg, resolve, ids, e, true)
}
func newWithEmbedder(cfg config.History, resolve Resolver, ids func() []string, e Embedder, start bool) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{cfg: cfg, resolve: resolve, ids: ids, embed: e, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), guards: map[string]context.Context{}, running: map[string]context.CancelFunc{}}
	if cfg.Enabled && start {
		s.wg.Add(1)
		go s.worker()
	}
	return s
}
func (s *Service) Close()        { s.cancel(); s.wg.Wait(); _ = s.embed.Close() }
func (s *Service) Enabled() bool { return s != nil && s.cfg.Enabled }
func (s *Service) Options() map[string]any {
	if s == nil {
		return map[string]any{"enabled": false}
	}
	return map[string]any{"enabled": s.Enabled(), "generation_enabled": s.cfg.AllowGeneration && s.generate != nil, "embedding": s.embed.Manifest(), "external_generation_disclosure": "Selected message evidence and session questions are sent to the configured AI provider only when external answers are enabled and explicitly consented to. Retrieval and embeddings stay local."}
}
func (s *Service) db(ctx context.Context, id string) (*store.Store, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	if err := checkGuard(ctx); err != nil {
		return nil, err
	}
	if strings.TrimSpace(id) == "" {
		return nil, ErrScope
	}
	return s.resolve(id)
}
func meta(db *sql.DB, key string) string {
	var v string
	_ = db.QueryRow("SELECT value FROM history_meta WHERE key=?", key).Scan(&v)
	return v
}
func epoch(db *sql.DB, key string) int64 {
	var v int64
	_ = db.QueryRow("SELECT CAST(value AS INTEGER) FROM history_meta WHERE key=?", key).Scan(&v)
	return v
}
func (s *Service) Selection(ctx context.Context, id string) (Selection, error) {
	st, err := s.db(ctx, id)
	if err != nil {
		return Selection{}, err
	}
	v := Selection{Mode: meta(st.DB(), "mode"), Epoch: epoch(st.DB(), "scope_epoch"), ChatIDs: []string{}}
	rows, err := st.DB().QueryContext(ctx, "SELECT chat_jid FROM history_chats WHERE selected=1 ORDER BY chat_jid")
	if err != nil {
		return v, err
	}
	defer rows.Close()
	for rows.Next() {
		var c string
		if err = rows.Scan(&c); err != nil {
			return v, err
		}
		v.ChatIDs = append(v.ChatIDs, c)
	}
	return v, rows.Err()
}
func (s *Service) Select(ctx context.Context, id, mode string, chats []string) (Selection, error) {
	st, err := s.db(ctx, id)
	if err != nil {
		return Selection{}, err
	}
	if mode != "all" && mode != "selected" && mode != "none" {
		return Selection{}, errors.New("mode must be all, selected or none")
	}
	if len(chats) > 1000 {
		return Selection{}, errors.New("select at most 1000 chats per request")
	}
	tx, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		return Selection{}, err
	}
	defer tx.Rollback()
	if mode == "all" {
		rows, e := tx.QueryContext(ctx, "SELECT jid FROM chats ORDER BY jid")
		if e != nil {
			return Selection{}, e
		}
		chats = nil
		for rows.Next() {
			var c string
			_ = rows.Scan(&c)
			chats = append(chats, c)
		}
		rows.Close()
	}
	if len(chats) > 1000 {
		return Selection{}, errors.New("history currently supports at most 1000 monitored chats per account; select a smaller scope")
	}
	if mode == "none" {
		chats = nil
	}
	for _, c := range chats {
		var n int
		if tx.QueryRowContext(ctx, "SELECT count(*) FROM chats WHERE jid=?", c).Scan(&n) != nil || n != 1 {
			return Selection{}, errors.New("select only existing cached chats from this account")
		}
	}
	// A selection change is an explicit revocation of the old derived scope.
	// Existing WhatsApp caches, original transcripts and media remain untouched.
	for _, q := range []string{"INSERT INTO history_meta VALUES('paused','0') ON CONFLICT(key) DO UPDATE SET value='0'", "DELETE FROM history_review_pages", "DELETE FROM history_turns", "DELETE FROM history_sessions", "DELETE FROM history_jobs", "DELETE FROM analysis_jobs WHERE task='history_answer'", "DELETE FROM history_chats", "UPDATE history_meta SET value=CAST(value AS INTEGER)+1 WHERE key='scope_epoch'"} {
		if _, err = tx.ExecContext(ctx, q); err != nil {
			return Selection{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE history_meta SET value=? WHERE key='mode'", mode); err != nil {
		return Selection{}, err
	}
	keep := canonical(chats)
	for _, table := range []string{"history_docs", "history_references", "history_dirty", "history_sync"} {
		q := "DELETE FROM " + table
		var args []any
		if len(keep) > 0 {
			q += " WHERE chat_jid NOT IN (" + placeholders(len(keep)) + ")"
			args = argsFor(keep)
		}
		if _, err = tx.ExecContext(ctx, q, args...); err != nil {
			return Selection{}, err
		}
	}
	seen := map[string]bool{}
	for _, c := range chats {
		if seen[c] {
			continue
		}
		seen[c] = true
		if _, err = tx.ExecContext(ctx, "INSERT INTO history_chats(chat_jid) VALUES(?)", c); err != nil {
			return Selection{}, err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO history_dirty SELECT m.chat_jid,m.id,(SELECT CAST(value AS INTEGER) FROM history_meta WHERE key='content_epoch') FROM messages m JOIN history_chats h ON h.chat_jid=m.chat_jid WHERE h.selected=1 AND NOT EXISTS(SELECT 1 FROM history_docs d WHERE d.chat_jid=m.chat_jid AND d.message_id=m.id)`)
	if err != nil {
		return Selection{}, err
	}
	if err = tx.Commit(); err != nil {
		return Selection{}, err
	}
	s.mu.Lock()
	for key, cancel := range s.running {
		if strings.HasPrefix(key, id+"/") {
			cancel()
		}
	}
	for key := range s.guards {
		if strings.HasPrefix(key, id+"/") {
			delete(s.guards, key)
		}
	}
	s.mu.Unlock()
	_ = st.MetaSet("history_onboarding", "configured")
	s.signal()
	return s.Selection(ctx, id)
}
func canonical(chats []string) []string {
	m := map[string]bool{}
	for _, c := range chats {
		if c != "" {
			m[c] = true
		}
	}
	out := []string{}
	for c := range m {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}
func (s *Service) authorize(ctx context.Context, scope Scope) (*store.Store, error) {
	st, err := s.db(ctx, scope.AccountID)
	if err != nil {
		return nil, err
	}
	if len(scope.ChatIDs) == 0 || len(scope.ChatIDs) > 1000 {
		return nil, errors.New("explicit chat_ids scope must contain 1-1000 chats")
	}
	for _, c := range canonical(scope.ChatIDs) {
		var n int
		if st.DB().QueryRowContext(ctx, "SELECT count(*) FROM history_chats WHERE chat_jid=? AND selected=1", c).Scan(&n) != nil || n != 1 {
			return nil, ErrScope
		}
	}
	return st, nil
}
func placeholders(n int) string { return strings.TrimRight(strings.Repeat("?,", n), ",") }
func argsFor(chats []string) []any {
	a := make([]any, len(chats))
	for i, c := range chats {
		a[i] = c
	}
	return a
}
func (s *Service) ensureSpace(ctx context.Context, st *store.Store) error {
	want := s.embed.Manifest()
	raw, _ := json.Marshal(want)
	if meta(st.DB(), "manifest") == string(raw) {
		return nil
	}
	tx, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current string
	if err = tx.QueryRowContext(ctx, "SELECT value FROM history_meta WHERE key='manifest'").Scan(&current); err != nil {
		return err
	}
	if current == string(raw) {
		return nil
	}
	for _, q := range []string{"DELETE FROM history_docs", "DELETE FROM history_turns", "DELETE FROM analysis_jobs WHERE task='history_answer'", "UPDATE history_jobs SET status='stale',result_json='',error='Embedding space changed' WHERE kind!='index'", "UPDATE history_meta SET value=CAST(value AS INTEGER)+1 WHERE key='content_epoch'"} {
		if _, err = tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, "UPDATE history_meta SET value=? WHERE key='manifest'", string(raw))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO history_dirty SELECT m.chat_jid,m.id,(SELECT CAST(value AS INTEGER) FROM history_meta WHERE key='content_epoch') FROM messages m JOIN history_chats h ON h.chat_jid=m.chat_jid WHERE h.selected=1`)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) Coverage(ctx context.Context, scope Scope) (Coverage, error) {
	st, err := s.authorize(ctx, scope)
	if err != nil {
		return Coverage{}, err
	}
	if err = s.ensureSpace(ctx, st); err != nil {
		return Coverage{}, err
	}
	v := Coverage{AccountID: scope.AccountID, ChatIDs: canonical(scope.ChatIDs), Manifest: s.embed.Manifest(), ScopeEpoch: epoch(st.DB(), "scope_epoch"), ContentEpoch: epoch(st.DB(), "content_epoch"), Warnings: []string{"Cached history is incomplete by definition; this is not a complete account backup.", "Search never downloads media or triggers paid transcription."}}
	if s.account != nil {
		if a, e := s.account(scope.AccountID); e == nil {
			status := a.Status()
			v.AccountState = string(status.State)
			if status.LastHistorySyncAt != nil {
				v.LastHistorySync = status.LastHistorySyncAt.UTC().Format(time.RFC3339)
			}
			if status.LastEventAt != nil {
				v.LastEvent = status.LastEventAt.UTC().Format(time.RFC3339)
			}
		}
	}
	in := placeholders(len(v.ChatIDs))
	args := argsFor(v.ChatIDs)
	queries := []struct {
		q   string
		dst *int
	}{{"SELECT count(*) FROM messages WHERE deleted=0 AND chat_jid IN (" + in + ")", &v.Messages}, {"SELECT count(*) FROM history_docs WHERE chat_jid IN (" + in + ")", &v.Documents}, {"SELECT count(*) FROM history_dirty WHERE chat_jid IN (" + in + ")", &v.Pending}, {`SELECT count(*) FROM messages m WHERE m.deleted=0 AND m.chat_jid IN (` + in + `) AND m.media_type='audio' AND NOT EXISTS(SELECT 1 FROM analysis_jobs j WHERE j.task='transcribe' AND j.status='completed' AND j.source_chat_jid=m.chat_jid AND j.source_message_id=m.id)`, &v.MissingTranscripts}, {`SELECT count(*) FROM analysis_jobs WHERE task='transcribe' AND status IN ('failed','unknown','unknown_billed','interrupted','cancelled') AND source_chat_jid IN (` + in + `)`, &v.FailedTranscripts}, {`SELECT count(*) FROM messages m WHERE m.deleted=0 AND m.media_type!='' AND m.chat_jid IN (` + in + `) AND NOT EXISTS(SELECT 1 FROM media_objects o WHERE o.chat_jid=m.chat_jid AND o.message_id=m.id)`, &v.MissingMedia}}
	for _, q := range queries {
		if err = st.DB().QueryRowContext(ctx, q.q, args...).Scan(q.dst); err != nil {
			return v, err
		}
	}
	var lo, hi sql.NullString
	_ = st.DB().QueryRowContext(ctx, "SELECT min(timestamp),max(timestamp) FROM messages WHERE deleted=0 AND chat_jid IN ("+in+")", args...).Scan(&lo, &hi)
	rows, err := st.DB().QueryContext(ctx, "SELECT chat_jid,status,requested_at FROM history_sync WHERE chat_jid IN ("+in+") ORDER BY chat_jid", args...)
	if err != nil {
		return v, err
	}
	for rows.Next() {
		var request SyncRequest
		if err = rows.Scan(&request.ChatJID, &request.Status, &request.RequestedAt); err != nil {
			rows.Close()
			return v, err
		}
		v.SyncRequests = append(v.SyncRequests, request)
		if request.Status == "failed" {
			v.Warnings = append(v.Warnings, "An older-history request failed. Reconnect WhatsApp before requesting sync again.")
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return v, err
	}
	rows.Close()
	v.Earliest = lo.String
	v.Latest = hi.String
	if v.Pending > 0 {
		v.Warnings = append(v.Warnings, "Index is not current; new or changed messages are waiting for local indexing.")
	}
	if v.MissingTranscripts > 0 {
		v.Warnings = append(v.Warnings, "Some audio messages have no completed transcript and are not searchable by spoken content.")
	}
	if st.MetaGet("cache_full_since") != "" {
		v.Warnings = append(v.Warnings, "Message cache limit was reached; ingestion may be incomplete.")
	}
	return v, nil
}
func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Service) expire(st *store.Store) {
	now := time.Now().Unix()
	_, _ = st.DB().Exec("DELETE FROM history_turns WHERE session_id IN (SELECT id FROM history_sessions WHERE expires<=?)", now)
	_, _ = st.DB().Exec("DELETE FROM history_sessions WHERE expires<=?", now)
	_, _ = st.DB().Exec("DELETE FROM history_review_pages WHERE session_id NOT IN (SELECT id FROM history_sessions)")
	_, _ = st.DB().Exec("DELETE FROM history_references WHERE created<?", now-int64(s.cfg.RetentionDays)*86400)
	_, _ = st.DB().Exec("DELETE FROM analysis_jobs WHERE task='history_answer' AND updated_at<?", time.Unix(now-int64(s.cfg.RetentionDays)*86400, 0))
	_, _ = st.DB().Exec("DELETE FROM history_jobs WHERE updated<? AND status NOT IN ('queued','running')", now-int64(s.cfg.RetentionDays)*86400)
}
func (s *Service) String() string { return fmt.Sprintf("history(%s)", s.embed.Manifest().Model) }

type sessionContextKey struct{}

func accountSession(ctx context.Context) string {
	v, _ := ctx.Value(sessionContextKey{}).(string)
	return v
}
