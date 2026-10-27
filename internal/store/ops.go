package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// opsSchema holds the tables added on top of the upstream message cache:
// send operations, media objects, analysis jobs and account metadata. All
// live in the account's own messages.db, so nothing here is shared between
// accounts.
const opsSchema = `
CREATE TABLE IF NOT EXISTS account_meta (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS send_ops (
	idempotency_key TEXT PRIMARY KEY,
	op_id TEXT NOT NULL UNIQUE,
	fingerprint TEXT NOT NULL,
	kind TEXT NOT NULL,
	recipient TEXT NOT NULL,
	media_id TEXT,
	wa_message_id TEXT NOT NULL,
	status TEXT NOT NULL,
	error TEXT,
	created_at TIMESTAMP NOT NULL,
	updated_at TIMESTAMP NOT NULL,
	server_timestamp TIMESTAMP
);
CREATE INDEX IF NOT EXISTS send_ops_wa_id ON send_ops(wa_message_id);

CREATE TABLE IF NOT EXISTS media_objects (
	id TEXT PRIMARY KEY,
	origin TEXT NOT NULL,
	rel_path TEXT NOT NULL,
	mime TEXT NOT NULL,
	size INTEGER NOT NULL,
	sha256 TEXT NOT NULL,
	filename TEXT,
	chat_jid TEXT,
	message_id TEXT,
	media_type TEXT,
	duration_ms INTEGER,
	codec TEXT,
	created_at TIMESTAMP NOT NULL,
	last_access TIMESTAMP NOT NULL,
	expires_at TIMESTAMP,
	pinned_until TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS media_objects_msg ON media_objects(chat_jid, message_id) WHERE origin = 'received';

CREATE TABLE IF NOT EXISTS analysis_jobs (
	id TEXT PRIMARY KEY,
	task TEXT NOT NULL,
	cache_key TEXT NOT NULL,
	status TEXT NOT NULL,
	model TEXT NOT NULL,
	tier TEXT NOT NULL,
	source_media_id TEXT,
	source_chat_jid TEXT,
	source_message_id TEXT,
	request_json TEXT NOT NULL,
	result_json TEXT,
	error TEXT,
	dispatched INTEGER NOT NULL DEFAULT 0,
	created_at TIMESTAMP NOT NULL,
	updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS analysis_jobs_cache ON analysis_jobs(cache_key, status);
CREATE INDEX IF NOT EXISTS analysis_jobs_source ON analysis_jobs(source_chat_jid, source_message_id);
`

// Send operation states. accepted means WhatsApp's server acknowledged the
// message; it says nothing about delivery to the recipient's phone.
const (
	OpPending   = "pending"
	OpSending   = "sending"
	OpAccepted  = "accepted"
	OpDelivered = "delivered"
	OpRead      = "read"
	OpFailed    = "failed"
	OpUnknown   = "unknown"
)

// ErrIdempotencyConflict means a key was reused with different content.
var ErrIdempotencyConflict = errors.New("idempotency_key was already used for a different send; use a new key for a new message")

// ErrCacheFull is returned by StoreMessage while the message cache budget is
// exhausted. Messages are not silently purged to make room.
var ErrCacheFull = errors.New("message cache budget exhausted")

// SendOp is a persisted send operation.
type SendOp struct {
	OpID            string     `json:"operation_id"`
	IdempotencyKey  string     `json:"idempotency_key"`
	Kind            string     `json:"kind"`
	Recipient       string     `json:"recipient"`
	MediaID         string     `json:"media_id,omitempty"`
	WAMessageID     string     `json:"whatsapp_message_id"`
	Status          string     `json:"status"`
	Error           string     `json:"error,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	ServerTimestamp *time.Time `json:"server_timestamp,omitempty"`
	fingerprint     string
}

// NewID returns a random hex identifier with a short prefix.
func NewID(prefix string) string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return prefix + hex.EncodeToString(b)
}

// MetaGet returns an account metadata value ("" when absent).
func (s *Store) MetaGet(key string) string {
	var v string
	_ = s.db.QueryRow("SELECT value FROM account_meta WHERE key = ?", key).Scan(&v)
	return v
}

// MetaSet upserts an account metadata value.
func (s *Store) MetaSet(key, value string) error {
	_, err := s.db.Exec("INSERT INTO account_meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, value)
	return err
}

// BeginSendOp returns the existing operation for key, or creates a pending
// one. existing is true when the key was already known; a different
// fingerprint yields ErrIdempotencyConflict. Callers serialize per account so
// two calls with the same key never both create an operation.
func (s *Store) BeginSendOp(ctx context.Context, key, fingerprint, kind, recipient, mediaID, waMessageID string) (op *SendOp, existing bool, err error) {
	if op, err := s.GetSendOpByKey(ctx, key); err == nil {
		if op.fingerprint != fingerprint {
			return nil, true, ErrIdempotencyConflict
		}
		return op, true, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	now := time.Now().UTC()
	op = &SendOp{
		OpID: NewID("op_"), IdempotencyKey: key, Kind: kind, Recipient: recipient,
		MediaID: mediaID, WAMessageID: waMessageID, Status: OpPending,
		CreatedAt: now, UpdatedAt: now, fingerprint: fingerprint,
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO send_ops
		(idempotency_key, op_id, fingerprint, kind, recipient, media_id, wa_message_id, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		key, op.OpID, fingerprint, kind, recipient, mediaID, waMessageID, OpPending, now, now)
	if err != nil {
		return nil, false, fmt.Errorf("record send operation: %w", err)
	}
	return op, false, nil
}

// SetSendOpStatus records a state transition.
func (s *Store) SetSendOpStatus(opID, status, errMsg string, serverTS *time.Time) error {
	_, err := s.db.Exec("UPDATE send_ops SET status = ?, error = ?, server_timestamp = COALESCE(?, server_timestamp), updated_at = ? WHERE op_id = ?",
		status, nullIfEmpty(errMsg), serverTS, time.Now().UTC(), opID)
	return err
}

// ApplyReceipt upgrades operations for the given WhatsApp message IDs. Only
// forward transitions happen: a delivery receipt never downgrades read, and
// receipts also resolve unknown operations because they prove the message
// reached WhatsApp.
func (s *Store) ApplyReceipt(waIDs []string, status string) error {
	from := []string{OpSending, OpAccepted, OpUnknown}
	if status == OpRead {
		from = append(from, OpDelivered)
	}
	for _, id := range waIDs {
		q := "UPDATE send_ops SET status = ?, updated_at = ? WHERE wa_message_id = ? AND status IN (?" + repeatPlaceholders(len(from)-1) + ")"
		args := []any{status, time.Now().UTC(), id}
		for _, f := range from {
			args = append(args, f)
		}
		if _, err := s.db.Exec(q, args...); err != nil {
			return err
		}
	}
	return nil
}

// RecoverSendOps runs at startup. An operation left in sending may or may not
// have reached WhatsApp, so it becomes unknown; pending ones were never
// dispatched and become failed. Neither is resent automatically.
func (s *Store) RecoverSendOps() (unknown, failed int64, err error) {
	now := time.Now().UTC()
	r, err := s.db.Exec("UPDATE send_ops SET status = ?, error = ?, updated_at = ? WHERE status = ?", OpUnknown, "service stopped after dispatch started; check the chat before sending again", now, OpSending)
	if err != nil {
		return 0, 0, err
	}
	unknown, _ = r.RowsAffected()
	r, err = s.db.Exec("UPDATE send_ops SET status = ?, error = ?, updated_at = ? WHERE status = ?", OpFailed, "service stopped before dispatch; nothing was sent", now, OpPending)
	if err != nil {
		return unknown, 0, err
	}
	failed, _ = r.RowsAffected()
	return unknown, failed, nil
}

// GetSendOpByKey looks up an operation by idempotency key.
func (s *Store) GetSendOpByKey(ctx context.Context, key string) (*SendOp, error) {
	return s.scanSendOp(s.db.QueryRowContext(ctx, sendOpSelect+" WHERE idempotency_key = ?", key))
}

// GetSendOp looks up an operation by operation ID or idempotency key.
func (s *Store) GetSendOp(ctx context.Context, idOrKey string) (*SendOp, error) {
	return s.scanSendOp(s.db.QueryRowContext(ctx, sendOpSelect+" WHERE op_id = ? OR idempotency_key = ? LIMIT 1", idOrKey, idOrKey))
}

const sendOpSelect = "SELECT op_id, idempotency_key, fingerprint, kind, recipient, COALESCE(media_id, ''), wa_message_id, status, COALESCE(error, ''), created_at, updated_at, server_timestamp FROM send_ops"

func (s *Store) scanSendOp(row *sql.Row) (*SendOp, error) {
	var op SendOp
	var ts sql.NullTime
	if err := row.Scan(&op.OpID, &op.IdempotencyKey, &op.fingerprint, &op.Kind, &op.Recipient, &op.MediaID, &op.WAMessageID, &op.Status, &op.Error, &op.CreatedAt, &op.UpdatedAt, &ts); err != nil {
		return nil, err
	}
	if ts.Valid {
		op.ServerTimestamp = &ts.Time
	}
	return &op, nil
}

// MediaObject is a file owned by one account: a staged upload, a downloaded
// received attachment, or a derived conversion.
type MediaObject struct {
	ID          string     `json:"media_id"`
	Origin      string     `json:"origin"`
	RelPath     string     `json:"-"`
	MIME        string     `json:"mime_type"`
	Size        int64      `json:"size_bytes"`
	SHA256      string     `json:"sha256"`
	Filename    string     `json:"filename,omitempty"`
	ChatJID     string     `json:"chat_jid,omitempty"`
	MessageID   string     `json:"message_id,omitempty"`
	MediaType   string     `json:"media_type"`
	DurationMS  int64      `json:"duration_ms,omitempty"`
	Codec       string     `json:"codec,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	LastAccess  time.Time  `json:"-"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	PinnedUntil *time.Time `json:"-"`
}

// PutMedia inserts or replaces a media object. A received object replaces
// any earlier row for the same message.
func (s *Store) PutMedia(m *MediaObject) error {
	if m.Origin == "received" {
		if _, err := s.db.Exec("DELETE FROM media_objects WHERE origin = 'received' AND chat_jid = ? AND message_id = ? AND id != ?", m.ChatJID, m.MessageID, m.ID); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(`INSERT OR REPLACE INTO media_objects
		(id, origin, rel_path, mime, size, sha256, filename, chat_jid, message_id, media_type, duration_ms, codec, created_at, last_access, expires_at, pinned_until)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.Origin, m.RelPath, m.MIME, m.Size, m.SHA256, nullIfEmpty(m.Filename), nullIfEmpty(m.ChatJID), nullIfEmpty(m.MessageID),
		m.MediaType, m.DurationMS, nullIfEmpty(m.Codec), m.CreatedAt, m.LastAccess, m.ExpiresAt, m.PinnedUntil)
	return err
}

const mediaSelect = "SELECT id, origin, rel_path, mime, size, sha256, COALESCE(filename,''), COALESCE(chat_jid,''), COALESCE(message_id,''), COALESCE(media_type,''), COALESCE(duration_ms,0), COALESCE(codec,''), created_at, last_access, expires_at, pinned_until FROM media_objects"

func scanMedia(row interface{ Scan(...any) error }) (*MediaObject, error) {
	var m MediaObject
	var exp, pin sql.NullTime
	if err := row.Scan(&m.ID, &m.Origin, &m.RelPath, &m.MIME, &m.Size, &m.SHA256, &m.Filename, &m.ChatJID, &m.MessageID, &m.MediaType, &m.DurationMS, &m.Codec, &m.CreatedAt, &m.LastAccess, &exp, &pin); err != nil {
		return nil, err
	}
	if exp.Valid {
		m.ExpiresAt = &exp.Time
	}
	if pin.Valid {
		m.PinnedUntil = &pin.Time
	}
	return &m, nil
}

// GetMedia returns a media object by ID and refreshes its access time.
func (s *Store) GetMedia(id string) (*MediaObject, error) {
	m, err := scanMedia(s.db.QueryRow(mediaSelect+" WHERE id = ?", id))
	if err != nil {
		return nil, err
	}
	_, _ = s.db.Exec("UPDATE media_objects SET last_access = ? WHERE id = ?", time.Now().UTC(), id)
	return m, nil
}

// GetReceivedMedia returns the cached object for a received message, if any.
func (s *Store) GetReceivedMedia(chatJID, messageID string) (*MediaObject, error) {
	return scanMedia(s.db.QueryRow(mediaSelect+" WHERE origin = 'received' AND chat_jid = ? AND message_id = ?", chatJID, messageID))
}

// PinMedia protects an object from expiry/eviction while an operation uses it.
func (s *Store) PinMedia(id string, until time.Time) error {
	_, err := s.db.Exec("UPDATE media_objects SET pinned_until = ? WHERE id = ?", until.UTC(), id)
	return err
}

// ListMedia returns every media object (for the janitor).
func (s *Store) ListMedia() ([]*MediaObject, error) {
	rows, err := s.db.Query(mediaSelect)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*MediaObject
	for rows.Next() {
		m, err := scanMedia(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// DeleteMedia removes a media row (the caller removes the file).
func (s *Store) DeleteMedia(id string) error {
	_, err := s.db.Exec("DELETE FROM media_objects WHERE id = ?", id)
	return err
}

// Job is an analysis job and, once completed, its cached result.
type Job struct {
	ID              string    `json:"job_id"`
	Task            string    `json:"task"`
	CacheKey        string    `json:"-"`
	Status          string    `json:"status"`
	Model           string    `json:"model"`
	Tier            string    `json:"tier"`
	SourceMediaID   string    `json:"source_media_id,omitempty"`
	SourceChatJID   string    `json:"source_chat_jid,omitempty"`
	SourceMessageID string    `json:"source_message_id,omitempty"`
	RequestJSON     string    `json:"-"`
	ResultJSON      string    `json:"-"`
	Error           string    `json:"error,omitempty"`
	Dispatched      bool      `json:"-"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Job states. interrupted jobs were never sent to the provider and can be
// requested again; unknown_billed jobs were in flight when the service
// stopped and may have been charged.
const (
	JobQueued        = "queued"
	JobRunning       = "running"
	JobCompleted     = "completed"
	JobFailed        = "failed"
	JobInterrupted   = "interrupted"
	JobUnknownBilled = "unknown_billed"
)

// InsertJob creates a job row.
func (s *Store) InsertJob(j *Job) error {
	_, err := s.db.Exec(`INSERT INTO analysis_jobs
		(id, task, cache_key, status, model, tier, source_media_id, source_chat_jid, source_message_id, request_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ID, j.Task, j.CacheKey, j.Status, j.Model, j.Tier, nullIfEmpty(j.SourceMediaID), nullIfEmpty(j.SourceChatJID), nullIfEmpty(j.SourceMessageID), j.RequestJSON, j.CreatedAt, j.UpdatedAt)
	return err
}

// UpdateJob records status, result, error and the dispatched flag.
func (s *Store) UpdateJob(id, status, resultJSON, errMsg string, dispatched bool) error {
	_, err := s.db.Exec("UPDATE analysis_jobs SET status = ?, result_json = COALESCE(?, result_json), error = ?, dispatched = MAX(dispatched, ?), updated_at = ? WHERE id = ?",
		status, nullIfEmpty(resultJSON), nullIfEmpty(errMsg), dispatched, time.Now().UTC(), id)
	return err
}

const jobSelect = "SELECT id, task, cache_key, status, model, tier, COALESCE(source_media_id,''), COALESCE(source_chat_jid,''), COALESCE(source_message_id,''), request_json, COALESCE(result_json,''), COALESCE(error,''), dispatched, created_at, updated_at FROM analysis_jobs"

func scanJob(row interface{ Scan(...any) error }) (*Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.Task, &j.CacheKey, &j.Status, &j.Model, &j.Tier, &j.SourceMediaID, &j.SourceChatJID, &j.SourceMessageID, &j.RequestJSON, &j.ResultJSON, &j.Error, &j.Dispatched, &j.CreatedAt, &j.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// GetJob returns a job by ID.
func (s *Store) GetJob(id string) (*Job, error) {
	return scanJob(s.db.QueryRow(jobSelect+" WHERE id = ?", id))
}

// FindJobByCacheKey returns the newest completed job, else the newest
// queued/running one, for a cache key.
func (s *Store) FindJobByCacheKey(key string) (*Job, error) {
	return scanJob(s.db.QueryRow(jobSelect+` WHERE cache_key = ? AND status IN ('completed', 'queued', 'running')
		ORDER BY CASE status WHEN 'completed' THEN 0 ELSE 1 END, updated_at DESC LIMIT 1`, key))
}

// RecoverJobs runs at startup and closes jobs a previous process left open.
func (s *Store) RecoverJobs() error {
	now := time.Now().UTC()
	if _, err := s.db.Exec("UPDATE analysis_jobs SET status = ?, error = ?, updated_at = ? WHERE status IN ('queued','running') AND dispatched = 0", JobInterrupted, "service restarted before the provider was called; request again", now); err != nil {
		return err
	}
	_, err := s.db.Exec("UPDATE analysis_jobs SET status = ?, error = ?, updated_at = ? WHERE status IN ('queued','running') AND dispatched = 1", JobUnknownBilled, "service restarted while the provider request was in flight; it may have been billed", now)
	return err
}

// InvalidateDerived deletes analysis results and cached media for a message
// that was revoked or edited, so stale interpretations are not served.
func (s *Store) InvalidateDerived(chatJID, messageID string) (mediaPaths []string, err error) {
	if _, err = s.db.Exec("DELETE FROM analysis_jobs WHERE source_chat_jid = ? AND source_message_id = ?", chatJID, messageID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query("SELECT rel_path FROM media_objects WHERE origin = 'received' AND chat_jid = ? AND message_id = ?", chatJID, messageID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil {
			mediaPaths = append(mediaPaths, p)
		}
	}
	rows.Close()
	_, err = s.db.Exec("DELETE FROM media_objects WHERE origin = 'received' AND chat_jid = ? AND message_id = ?", chatJID, messageID)
	return mediaPaths, err
}

// senderMatch restricts edits/revokes to the original author: our own
// messages when fromMe, otherwise rows whose stored sender matches.
const senderMatch = " AND ((? AND is_from_me = 1) OR (NOT ? AND is_from_me = 0 AND sender = ?))"

// RevokeMessage clears the content and media keys of a revoked message. It
// reports whether a row by that sender was changed.
func (s *Store) RevokeMessage(chatJID, messageID, sender string, fromMe bool) (bool, error) {
	r, err := s.db.Exec(`UPDATE messages SET content = '[message deleted by sender]', media_type = NULL, filename = NULL, url = NULL,
		direct_path = NULL, media_key = NULL, file_sha256 = NULL, file_enc_sha256 = NULL, file_length = NULL
		WHERE chat_jid = ? AND id = ?`+senderMatch, chatJID, messageID, fromMe, fromMe, sender)
	return affected(r, err)
}

// EditMessage replaces the text of an edited message by the same sender.
func (s *Store) EditMessage(chatJID, messageID, content, sender string, fromMe bool) (bool, error) {
	r, err := s.db.Exec("UPDATE messages SET content = ? WHERE chat_jid = ? AND id = ?"+senderMatch, content, chatJID, messageID, fromMe, fromMe, sender)
	return affected(r, err)
}

func affected(r sql.Result, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	n, _ := r.RowsAffected()
	return n > 0, nil
}

// SetCacheFull toggles the message-cache budget guard.
func (s *Store) SetCacheFull(full bool) {
	if s.cacheFull.Swap(full) == full {
		return
	}
	since := ""
	if full {
		since = time.Now().UTC().Format(time.RFC3339)
	}
	_ = s.MetaSet("cache_full_since", since)
}

// CacheFull reports whether message persistence is paused by the budget.
func (s *Store) CacheFull() bool { return s.cacheFull.Load() }

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func repeatPlaceholders(n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += ", ?"
	}
	return out
}
