package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/datahase/whatsapp-mcp/internal/store"
)

// sources is always called after scope authorization. It never resolves a
// source ID globally or reads another account's database.
func sources(ctx context.Context, st *store.Store, account, chat, id string) ([]Source, error) {
	var src Source
	var deleted bool
	src.AccountID = account
	src.ChatJID = chat
	src.MessageID = id
	src.Kind = "message"
	err := st.DB().QueryRowContext(ctx, `SELECT sender,COALESCE(content,''),timestamp,is_from_me,COALESCE(media_type,''),COALESCE(reply_id,''),COALESCE(reply_chat_jid,''),deleted FROM messages WHERE chat_jid=? AND id=?`, chat, id).Scan(&src.Sender, &src.Text, &src.Timestamp, &src.FromMe, &src.MediaType, &src.ReplyID, &src.ReplyChatJID, &deleted)
	if errors.Is(err, sql.ErrNoRows) || deleted {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = st.DB().QueryRowContext(ctx, "SELECT id FROM media_objects WHERE chat_jid=? AND message_id=? ORDER BY created_at DESC LIMIT 1", chat, id).Scan(&src.MediaID)
	src.MediaCached = src.MediaID != ""
	out := []Source{}
	if src.Text != "" {
		src.OriginalText = src.Text
		out = append(out, src)
	}
	rows, err := st.DB().QueryContext(ctx, `SELECT id,COALESCE(result_json,''),COALESCE(source_media_id,'') FROM analysis_jobs WHERE task='transcribe' AND status='completed' AND source_chat_jid=? AND source_message_id=? ORDER BY updated_at,id`, chat, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var job, raw, media string
		if err = rows.Scan(&job, &raw, &media); err != nil {
			return nil, err
		}
		var r struct {
			Transcript   string          `json:"transcript"`
			Language     string          `json:"source_language"`
			LanguageHint string          `json:"language_hint"`
			Translation  json.RawMessage `json:"translation"`
		}
		if json.Unmarshal([]byte(raw), &r) != nil || r.Transcript == "" {
			continue
		}
		t := src
		t.Kind = "transcript"
		t.OriginalText = src.Text
		t.Text = r.Transcript
		t.TranscriptJobID = job
		t.MediaID = media
		t.Language = r.Language
		if t.Language == "" && len(r.Translation) > 0 {
			var tr struct {
				Language string `json:"source_language"`
			}
			if json.Unmarshal(r.Translation, &tr) == nil {
				t.Language = tr.Language
			}
		}
		t.LanguageHint = r.LanguageHint
		t.Translation = r.Translation
		out = append(out, t)
	}
	return out, rows.Err()
}
func chunks(src Source) []Source {
	// All source text remains in the original cache. Extremely large messages
	// fail indexing explicitly rather than silently truncating their evidence.
	parentHash := digest(src.Text, src.OriginalText, string(src.Translation))
	r := []rune(src.Text)
	out := []Source{}
	for start, n := 0, 0; start < len(r); n++ {
		end := start + 1200
		if end > len(r) {
			end = len(r)
		}
		v := src
		v.Text = string(r[start:end])
		v.OriginalText = bounded(v.OriginalText, 1200)
		v.Chunk = n
		v.ID = "hsrc_" + digest(v.AccountID, v.ChatJID, v.MessageID, v.Kind, v.TranscriptJobID, fmt.Sprint(n))[:40]
		v.Hash = ""
		snapshot := v
		snapshot.MediaID = ""
		snapshot.MediaCached = false
		raw, _ := json.Marshal(snapshot)
		v.Hash = digest(parentHash, string(raw))
		out = append(out, v)
		if end == len(r) {
			break
		}
		start = end - 100
	}
	return out
}
func (s *Service) validateSource(ctx context.Context, scope Scope, ref Source) (Source, error) {
	if ref.AccountID != scope.AccountID {
		return Source{}, ErrScope
	}
	allowed := false
	for _, c := range scope.ChatIDs {
		if c == ref.ChatJID {
			allowed = true
		}
	}
	if !allowed {
		return Source{}, ErrScope
	}
	st, err := s.authorize(ctx, scope)
	if err != nil {
		return Source{}, err
	}
	all, err := sources(ctx, st, scope.AccountID, ref.ChatJID, ref.MessageID)
	if err != nil {
		return Source{}, err
	}
	for _, v := range all {
		for _, c := range chunks(v) {
			if c.ID == ref.ID && c.Hash == ref.Hash {
				return c, nil
			}
		}
	}
	return Source{}, errors.New("source changed or is no longer available")
}
func (s *Service) Context(ctx context.Context, scope Scope, id string) ([]Source, error) {
	st, err := s.authorize(ctx, scope)
	if err != nil {
		return nil, err
	}
	var raw string
	if err = st.DB().QueryRowContext(ctx, "SELECT source_json FROM (SELECT source_id,source_json,chat_jid FROM history_docs UNION ALL SELECT id,source_json,chat_jid FROM history_references) WHERE source_id=? AND chat_jid IN ("+placeholders(len(scope.ChatIDs))+")", append([]any{id}, argsFor(scope.ChatIDs)...)...).Scan(&raw); err != nil {
		return nil, errors.New("source not found in selected scope")
	}
	var ref Source
	if json.Unmarshal([]byte(raw), &ref) != nil {
		return nil, errors.New("invalid cached source")
	}
	current, err := s.validateSource(ctx, scope, ref)
	if err != nil {
		return nil, err
	}
	return s.contextFor(ctx, scope, current)
}
func (s *Service) contextFor(ctx context.Context, scope Scope, src Source) ([]Source, error) {
	st, err := s.authorize(ctx, scope)
	if err != nil {
		return nil, err
	}
	out := []Source{src}
	seen := map[string]bool{src.ID: true}
	// Include chronological neighbors and explicitly quoted messages, but never
	// follow a reply into an unselected chat.
	rows, err := st.DB().QueryContext(ctx, `SELECT id FROM (SELECT id,timestamp FROM messages WHERE chat_jid=? AND deleted=0 AND timestamp<=? ORDER BY timestamp DESC,id DESC LIMIT 3) UNION SELECT id FROM (SELECT id,timestamp FROM messages WHERE chat_jid=? AND deleted=0 AND timestamp>? ORDER BY timestamp,id LIMIT 4)`, src.ChatJID, src.Timestamp, src.ChatJID, src.Timestamp)
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
	if src.ReplyID != "" && (src.ReplyChatJID == "" || src.ReplyChatJID == src.ChatJID) {
		ids = append(ids, src.ReplyID)
	}
	// Later replies can record completion long after the neighboring window.
	rows, err = st.DB().QueryContext(ctx, "SELECT id FROM messages WHERE chat_jid=? AND deleted=0 AND reply_id=? ORDER BY timestamp,id LIMIT 12", src.ChatJID, src.MessageID)
	if err == nil {
		for rows.Next() {
			var id string
			_ = rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close()
	}
	for _, id := range ids {
		all, e := sources(ctx, st, scope.AccountID, src.ChatJID, id)
		if e != nil {
			return nil, e
		}
		for _, v := range all {
			cs := chunks(v)
			if len(cs) == 0 {
				continue
			}
			v = cs[0]
			if !seen[v.ID] {
				seen[v.ID] = true
				out = append(out, v)
			}
		}
		if len(out) >= 20 {
			break
		}
	}
	s.remember(st, out)
	return out, nil
}

// chronological returns a bounded keyset page of source messages, including
// existing transcripts. Cursor is generated by the server and scope-bound by
// the persisted review job, never an SQL fragment from a client.
func (s *Service) chronological(ctx context.Context, scope Scope, ts int64, id, chat string, limit int) ([]Source, int64, string, string, int, error) {
	st, err := s.authorize(ctx, scope)
	if err != nil {
		return nil, 0, "", "", 0, err
	}
	args := argsFor(scope.ChatIDs)
	args = append(args, time.UnixMilli(ts).UTC(), time.UnixMilli(ts).UTC(), id, id, chat, limit)
	q := `SELECT id,chat_jid,timestamp FROM messages WHERE deleted=0 AND chat_jid IN (` + placeholders(len(scope.ChatIDs)) + `) AND (julianday(timestamp)>julianday(?) OR (julianday(timestamp)=julianday(?) AND (id>? OR (id=? AND chat_jid>?)))) ORDER BY julianday(timestamp),id,chat_jid LIMIT ?`
	rows, err := st.DB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, "", "", 0, err
	}
	type key struct {
		id, chat string
		t        time.Time
	}
	keys := []key{}
	for rows.Next() {
		var k key
		if err = rows.Scan(&k.id, &k.chat, &k.t); err != nil {
			rows.Close()
			return nil, 0, "", "", 0, err
		}
		keys = append(keys, k)
	}
	rows.Close()
	out := []Source{}
	for _, k := range keys {
		all, e := sources(ctx, st, scope.AccountID, k.chat, k.id)
		if e != nil {
			return nil, 0, "", "", 0, e
		}
		for _, v := range all {
			out = append(out, chunks(v)...)
		}
		ts, id, chat = k.t.UnixMilli(), k.id, k.chat
	}
	return out, ts, id, chat, len(keys), nil
}

func (s *Service) remember(st *store.Store, sources []Source) {
	for _, src := range sources {
		raw, _ := json.Marshal(src)
		_, _ = st.DB().Exec("INSERT INTO history_references(id,chat_jid,message_id,source_json,created) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET source_json=excluded.source_json,created=excluded.created", src.ID, src.ChatJID, src.MessageID, string(raw), time.Now().Unix())
	}
	_, _ = st.DB().Exec("DELETE FROM history_references WHERE created<?", time.Now().Unix()-int64(s.cfg.RetentionDays)*86400)
}
