package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/datahase/whatsapp-mcp/internal/store"
)

type jobRequest struct {
	Scope        Scope  `json:"scope"`
	SessionID    string `json:"session_id,omitempty"`
	Question     string `json:"question,omitempty"`
	ScopeEpoch   int64  `json:"scope_epoch"`
	ContentEpoch int64  `json:"content_epoch"`
	PageTS       int64  `json:"page_ts"`
	PageID       string `json:"page_id"`
	PageChat     string `json:"page_chat"`
	Review       Answer `json:"review"`
	RequestSync  bool   `json:"request_sync"`
	SyncPosition int    `json:"sync_position"`
}

func (s *Service) insertJob(ctx context.Context, kind string, r jobRequest) (Job, error) {
	st, err := s.authorize(ctx, r.Scope)
	if err != nil {
		return Job{}, err
	}
	if err = s.ensureSpace(ctx, st); err != nil {
		return Job{}, err
	}
	r.Scope.ChatIDs = canonical(r.Scope.ChatIDs)
	r.ScopeEpoch = epoch(st.DB(), "scope_epoch")
	r.ContentEpoch = epoch(st.DB(), "content_epoch")
	var dedup any
	if kind != "index" {
		dedup = digest(kind, r.Scope.AccountID, r.SessionID, r.Question, fmt.Sprint(r.ScopeEpoch), fmt.Sprint(r.ContentEpoch))
	}
	raw, _ := json.Marshal(r)
	now := time.Now().Unix()
	j := Job{ID: store.NewID("hjob_"), Kind: kind, Status: "queued", Updated: now}
	var n int
	_ = st.DB().QueryRow("SELECT count(*) FROM history_jobs WHERE status IN ('queued','running')").Scan(&n)
	if n >= 8 {
		return Job{}, errors.New("history queue is full")
	}
	if _, err = st.DB().ExecContext(ctx, "INSERT INTO history_jobs(id,kind,status,request_json,dedup,created,updated) VALUES(?,?,?,?,?,?,?)", j.ID, kind, j.Status, string(raw), dedup, now, now); err != nil {
		if dedup != nil {
			var existing string
			if st.DB().QueryRowContext(ctx, "SELECT id FROM history_jobs WHERE dedup=?", dedup).Scan(&existing) == nil {
				return s.Job(ctx, r.Scope.AccountID, existing)
			}
		}
		return Job{}, err
	}
	s.mu.Lock()
	s.guards[r.Scope.AccountID+"/"+j.ID] = context.WithoutCancel(ctx)
	s.mu.Unlock()
	s.signal()
	return j, nil
}
func (s *Service) StartIndex(ctx context.Context, scope Scope, syncHistory bool) (Job, error) {
	st, err := s.authorize(ctx, scope)
	if err != nil {
		return Job{}, err
	}
	_, _ = st.DB().Exec("INSERT INTO history_meta VALUES('paused','0') ON CONFLICT(key) DO UPDATE SET value='0'")
	var id string
	if st.DB().QueryRow("SELECT id FROM history_jobs WHERE kind='index' AND status IN ('queued','running') ORDER BY created LIMIT 1").Scan(&id) == nil {
		return s.Job(ctx, scope.AccountID, id)
	}
	// Resume a failed job only within the same still-authorized selection.
	// Dirty rows are the durable cursor; completed source upserts stay intact.
	var raw string
	if st.DB().QueryRow("SELECT id,request_json FROM history_jobs WHERE kind='index' AND status='failed' ORDER BY created DESC,id DESC LIMIT 1").Scan(&id, &raw) == nil {
		var r jobRequest
		if json.Unmarshal([]byte(raw), &r) == nil && r.ScopeEpoch == epoch(st.DB(), "scope_epoch") &&
			r.Scope.AccountID == scope.AccountID && slices.Equal(canonical(r.Scope.ChatIDs), canonical(scope.ChatIDs)) && !syncHistory {
			if _, err = st.DB().ExecContext(ctx, "UPDATE history_jobs SET status='queued',error='',updated=? WHERE id=? AND status='failed'", time.Now().Unix(), id); err != nil {
				return Job{}, err
			}
			s.mu.Lock()
			s.guards[scope.AccountID+"/"+id] = context.WithoutCancel(ctx)
			s.mu.Unlock()
			s.signal()
			return s.Job(ctx, scope.AccountID, id)
		}
	}
	return s.insertJob(ctx, "index", jobRequest{Scope: scope, RequestSync: syncHistory})
}
func (s *Service) Job(ctx context.Context, account, id string) (Job, error) {
	st, err := s.db(ctx, account)
	if err != nil {
		return Job{}, err
	}
	var j Job
	var request string
	var result string
	if err = st.DB().QueryRowContext(ctx, "SELECT id,kind,status,processed,total,result_json,error,updated,request_json FROM history_jobs WHERE id=?", id).Scan(&j.ID, &j.Kind, &j.Status, &j.Processed, &j.Total, &result, &j.Error, &j.Updated, &request); err != nil {
		return j, errors.New("history job not found")
	}
	var r jobRequest
	if json.Unmarshal([]byte(request), &r) != nil {
		return j, ErrScope
	}
	if _, err = s.authorize(ctx, r.Scope); err != nil {
		return Job{}, err
	}
	j.SessionID = r.SessionID
	if result != "" {
		j.Result = json.RawMessage(result)
	}
	return j, nil
}
func (s *Service) Cancel(ctx context.Context, account, id string) error {
	j, err := s.Job(ctx, account, id)
	if err != nil {
		return err
	}
	st, err := s.db(ctx, account)
	if err != nil {
		return err
	}
	_, err = st.DB().Exec("UPDATE history_jobs SET status='cancelled',result_json='',updated=? WHERE id=?", time.Now().Unix(), id)
	if j.Kind == "index" {
		_, _ = st.DB().Exec("INSERT INTO history_meta VALUES('paused','1') ON CONFLICT(key) DO UPDATE SET value='1'")
	}
	s.mu.Lock()
	delete(s.guards, account+"/"+id)
	if f := s.running[account+"/"+id]; f != nil {
		f()
	}
	s.mu.Unlock()
	return err
}
func (s *Service) worker() {
	defer s.wg.Done()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	initialized := map[*store.Store]bool{}
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.wake:
		case <-ticker.C:
		}
		for _, account := range s.ids() {
			if s.ctx.Err() != nil {
				return
			}
			st, err := s.resolve(account)
			if err != nil {
				continue
			}
			if !initialized[st] {
				_, _ = st.DB().Exec("UPDATE history_jobs SET status='queued' WHERE status='running' AND kind='index'")
				_, _ = st.DB().Exec("UPDATE history_jobs SET status='awaiting_authorization',error='Resume with a fresh authorized request after restart' WHERE status IN ('queued','running') AND kind!='index'")
				initialized[st] = true
			}
			s.expire(st)
			if s.ensureSpace(s.ctx, st) != nil {
				continue
			}
			var id, kind, raw string
			err = st.DB().QueryRow("SELECT id,kind,request_json FROM history_jobs WHERE status='queued' ORDER BY created,id LIMIT 1").Scan(&id, &kind, &raw)
			if errors.Is(err, sql.ErrNoRows) && meta(st.DB(), "paused") != "1" {
				sel, e := s.Selection(s.ctx, account)
				if e == nil && len(sel.ChatIDs) > 0 {
					var dirty int
					_ = st.DB().QueryRow("SELECT count(*) FROM history_dirty").Scan(&dirty)
					if dirty > 0 {
						_, _ = s.StartIndex(s.ctx, Scope{account, sel.ChatIDs}, false)
						s.signal()
					}
				}
				continue
			}
			if err != nil {
				continue
			}
			var r jobRequest
			if json.Unmarshal([]byte(raw), &r) != nil {
				continue
			}
			key := account + "/" + id
			s.mu.Lock()
			origin := s.guards[key]
			if origin == nil {
				origin = s.ctx
			}
			ctx, cancel := context.WithCancel(origin)
			s.running[key] = cancel
			s.mu.Unlock()
			// Service shutdown cancels any in-flight local model or external generation.
			stop := context.AfterFunc(s.ctx, cancel)
			_, _ = st.DB().Exec("UPDATE history_jobs SET status='running',error='',updated=? WHERE id=? AND status='queued'", time.Now().Unix(), id)
			if kind == "index" {
				err = s.index(ctx, st, id, r)
			} else {
				err = s.answerJob(ctx, st, id, kind, r)
			}
			stop()
			cancel()
			s.mu.Lock()
			delete(s.running, key)
			var nextStatus string
			_ = st.DB().QueryRow("SELECT status FROM history_jobs WHERE id=?", id).Scan(&nextStatus)
			if nextStatus != "queued" {
				delete(s.guards, key)
			}
			s.mu.Unlock()
			if err != nil {
				if kind == "index" && s.ctx.Err() == nil && !errors.Is(err, ErrScope) && !errors.Is(err, context.Canceled) {
					_, _ = st.DB().Exec("INSERT INTO history_meta VALUES('paused','1') ON CONFLICT(key) DO UPDATE SET value='1'")
				}
				state := "failed"
				if s.ctx.Err() != nil {
					state = "queued"
				} else if errors.Is(err, ErrScope) || errors.Is(err, context.Canceled) {
					state = "cancelled"
				}
				msg := "History job failed; inspect configuration or retry. No message content was logged."
				if errors.Is(err, errLocalEmbedding) {
					msg = errLocalEmbedding.Error()
				}
				if errors.Is(err, ErrScope) {
					msg = ErrScope.Error()
				}
				if s.ctx.Err() != nil {
					msg = "Interrupted; resumable local work will continue after restart"
				}
				_, _ = st.DB().Exec("UPDATE history_jobs SET status=?,error=?,result_json='',updated=? WHERE id=? AND status='running'", state, msg, time.Now().Unix(), id)
			}
			s.signal()
		}
	}
}
func (s *Service) jobGuard(ctx context.Context, st *store.Store, id string, r jobRequest) error {
	cur, err := s.authorize(ctx, r.Scope)
	if err != nil || cur != st {
		return ErrScope
	}
	if epoch(st.DB(), "scope_epoch") != r.ScopeEpoch {
		return ErrScope
	}
	var status string
	if st.DB().QueryRow("SELECT status FROM history_jobs WHERE id=?", id).Scan(&status) != nil || status != "running" {
		return ErrScope
	}
	return nil
}
func (s *Service) index(ctx context.Context, st *store.Store, id string, r jobRequest) error {
	if r.RequestSync && s.account != nil {
		a, err := s.account(r.Scope.AccountID)
		if err != nil {
			return err
		}
		for pos := r.SyncPosition; pos < len(r.Scope.ChatIDs); pos++ {
			chat := r.Scope.ChatIDs[pos]
			if err = s.jobGuard(ctx, st, id, r); err != nil {
				return err
			}
			// "requested" means accepted by the local client, never complete history.
			_, syncErr := a.RequestHistorySync(ctx, chat, 50)
			state := "requested"
			if syncErr != nil {
				state = "failed"
			}
			if _, err = st.DB().ExecContext(ctx, "INSERT INTO history_sync(chat_jid,status,requested_at) VALUES(?,?,?) ON CONFLICT(chat_jid) DO UPDATE SET status=excluded.status,requested_at=excluded.requested_at", chat, state, time.Now().UTC().Format(time.RFC3339)); err != nil {
				return err
			}
			r.SyncPosition = pos + 1
			raw, _ := json.Marshal(r)
			if _, err = st.DB().ExecContext(ctx, "UPDATE history_jobs SET request_json=? WHERE id=?", string(raw), id); err != nil {
				return err
			}
		}
		r.RequestSync = false
		raw, _ := json.Marshal(r)
		_, _ = st.DB().Exec("UPDATE history_jobs SET request_json=? WHERE id=?", string(raw), id)
	}
	for {
		if err := s.jobGuard(ctx, st, id, r); err != nil {
			return err
		}
		var chat, msg string
		var version int64
		err := st.DB().QueryRowContext(ctx, "SELECT chat_jid,message_id,version FROM history_dirty WHERE chat_jid IN ("+placeholders(len(r.Scope.ChatIDs))+") ORDER BY version,chat_jid,message_id LIMIT 1", argsFor(r.Scope.ChatIDs)...).Scan(&chat, &msg, &version)
		if errors.Is(err, sql.ErrNoRows) {
			_, err = st.DB().Exec("UPDATE history_jobs SET status='completed',updated=? WHERE id=? AND status='running'", time.Now().Unix(), id)
			return err
		}
		if err != nil {
			return err
		}
		all, err := sources(ctx, st, r.Scope.AccountID, chat, msg)
		if err != nil {
			return err
		}
		docs := []Source{}
		for _, v := range all {
			if len([]rune(v.Text)) > 64000 {
				return errors.New("source exceeds supported indexing size")
			}
			docs = append(docs, chunks(v)...)
		}
		var count int
		_ = st.DB().QueryRow("SELECT count(*) FROM history_docs").Scan(&count)
		if count+len(docs) > s.cfg.MaxDocuments {
			return errors.New("history document limit reached")
		}
		vectors := [][]float32{}
		for i := 0; i < len(docs); i += 16 {
			end := i + 16
			if end > len(docs) {
				end = len(docs)
			}
			texts := []string{}
			for _, d := range docs[i:end] {
				texts = append(texts, d.Text)
			}
			v, e := s.embed.Embed(ctx, texts, "document")
			if e != nil {
				return e
			}
			if len(v) != len(texts) {
				return errors.New("embedding batch size mismatch")
			}
			vectors = append(vectors, v...)
		}
		if err = s.jobGuard(ctx, st, id, r); err != nil {
			return err
		}
		tx, err := st.DB().BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		var current int64
		err = tx.QueryRow("SELECT version FROM history_dirty WHERE chat_jid=? AND message_id=?", chat, msg).Scan(&current)
		if err != nil || current != version {
			tx.Rollback()
			continue
		}
		_, err = tx.Exec("DELETE FROM history_docs WHERE chat_jid=? AND message_id=?", chat, msg)
		for i, d := range docs {
			if err != nil {
				break
			}
			if err = normalize(vectors[i], s.embed.Manifest().Dimensions); err != nil {
				break
			}
			raw, _ := json.Marshal(d)
			_, err = tx.Exec(`INSERT INTO history_docs(source_id,chat_jid,message_id,kind,chunk,content_hash,body,source_json,ts,sender,vector,space) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, d.ID, chat, msg, d.Kind, d.Chunk, d.Hash, d.Text, string(raw), d.Timestamp.UnixMilli(), d.Sender, encodeVector(vectors[i]), s.embed.Manifest().Key())
			if err == nil {
				_, err = tx.Exec("INSERT INTO history_fts(docid,body) VALUES(last_insert_rowid(),?)", d.Text)
			}
		}
		if err == nil {
			_, err = tx.Exec("DELETE FROM history_dirty WHERE chat_jid=? AND message_id=? AND version=?", chat, msg, version)
		}
		if err == nil {
			_, err = tx.Exec("UPDATE history_jobs SET processed=processed+1,total=max(total,processed+1),updated=? WHERE id=?", time.Now().Unix(), id)
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
}

// Resume answers only after a new authenticated request. Credentials are never
// persisted with jobs; an ambiguous billed provider call is not redispatched.
func (s *Service) Resume(ctx context.Context, account, id string) (Job, error) {
	j, err := s.Job(ctx, account, id)
	if err != nil {
		return Job{}, err
	}
	if j.Status != "awaiting_authorization" {
		return Job{}, errors.New("only interrupted jobs awaiting authorization can be resumed")
	}
	st, err := s.db(ctx, account)
	if err != nil {
		return Job{}, err
	}
	_, err = st.DB().ExecContext(ctx, "UPDATE history_jobs SET status='queued',error='',updated=? WHERE id=? AND status='awaiting_authorization'", time.Now().Unix(), id)
	if err != nil {
		return Job{}, err
	}
	s.mu.Lock()
	s.guards[account+"/"+id] = context.WithoutCancel(ctx)
	s.mu.Unlock()
	s.signal()
	return s.Job(ctx, account, id)
}
