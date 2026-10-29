// Package history provides opt-in local retrieval over an account's cached
// messages. It never sends WhatsApp messages or triggers transcription.
package history

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var ErrScope = errors.New("history account/chat scope is unavailable or no longer selected")
var ErrDisabled = errors.New("history search is disabled; configure the optional local history runtime")

type Scope struct {
	AccountID string   `json:"account_id"`
	ChatIDs   []string `json:"chat_ids"`
}
type Selection struct {
	Mode    string   `json:"mode"`
	ChatIDs []string `json:"chat_ids"`
	Epoch   int64    `json:"epoch"`
}
type Source struct {
	ID              string          `json:"source_id"`
	AccountID       string          `json:"account_id"`
	ChatJID         string          `json:"chat_jid"`
	MessageID       string          `json:"message_id"`
	Sender          string          `json:"sender"`
	FromMe          bool            `json:"is_from_me"`
	Timestamp       time.Time       `json:"timestamp"`
	Kind            string          `json:"kind"`
	Text            string          `json:"text"`
	OriginalText    string          `json:"original_text,omitempty"`
	Language        string          `json:"transcript_language,omitempty"`
	LanguageHint    string          `json:"language_hint,omitempty"`
	Translation     json.RawMessage `json:"translation,omitempty"`
	TranscriptJobID string          `json:"transcript_job_id,omitempty"`
	MediaID         string          `json:"media_id,omitempty"`
	MediaType       string          `json:"media_type,omitempty"`
	MediaCached     bool            `json:"media_cached"`
	ReplyID         string          `json:"reply_id,omitempty"`
	ReplyChatJID    string          `json:"reply_chat_jid,omitempty"`
	Hash            string          `json:"content_hash"`
	Chunk           int             `json:"chunk"`
}
type SyncRequest struct {
	ChatJID     string `json:"chat_jid"`
	Status      string `json:"status"`
	RequestedAt string `json:"requested_at"`
}
type Coverage struct {
	SyncRequests          []SyncRequest `json:"sync_requests,omitempty"`
	AccountID             string        `json:"account_id"`
	ChatIDs               []string      `json:"chat_ids"`
	Messages              int           `json:"cached_messages"`
	Documents             int           `json:"indexed_documents"`
	Pending               int           `json:"pending_messages"`
	MissingTranscripts    int           `json:"missing_transcripts"`
	FailedTranscripts     int           `json:"failed_transcripts"`
	MissingMedia          int           `json:"media_not_cached"`
	Earliest              string        `json:"earliest_cached,omitempty"`
	Latest                string        `json:"latest_cached,omitempty"`
	ContentEpoch          int64         `json:"content_epoch"`
	ScopeEpoch            int64         `json:"scope_epoch"`
	Manifest              Manifest      `json:"embedding"`
	Warnings              []string      `json:"warnings"`
	LastHistorySync       string        `json:"last_history_sync_at,omitempty"`
	LastEvent             string        `json:"last_event_at,omitempty"`
	AccountState          string        `json:"account_state,omitempty"`
	CompleteAccountBackup bool          `json:"complete_account_backup"`
}
type SearchRequest struct {
	Scope
	Query  string `json:"query"`
	Sender string `json:"sender,omitempty"`
	After  string `json:"after,omitempty"`
	Before string `json:"before,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
}
type Hit struct {
	Source  Source   `json:"source"`
	Score   float64  `json:"score"`
	Context []Source `json:"context"`
}
type SearchResult struct {
	Hits       []Hit    `json:"hits"`
	Coverage   Coverage `json:"coverage"`
	Warnings   []string `json:"warnings"`
	NextOffset *int     `json:"next_offset,omitempty"`
}
type Session struct {
	ID string `json:"session_id"`
	Scope
	ExternalConsent bool  `json:"external_consent"`
	ScopeEpoch      int64 `json:"scope_epoch"`
	Expires         int64 `json:"expires"`
}
type Citation struct {
	SourceID string `json:"source_id"`
	Quote    string `json:"quote"`
}
type Claim struct {
	Kind      string     `json:"kind"`
	Text      string     `json:"text"`
	Citations []Citation `json:"citations"`
}
type Action struct {
	ID          string     `json:"id"`
	Description string     `json:"description"`
	Owner       string     `json:"owner"`
	OwnerQuote  string     `json:"owner_quote"`
	Status      string     `json:"status"`
	DueDate     string     `json:"stated_due_date"`
	DueKind     string     `json:"due_date_kind"`
	Citations   []Citation `json:"citations"`
	LastUpdate  string     `json:"last_supporting_update"`
}
type Answer struct {
	Answer         string   `json:"answer"`
	Claims         []Claim  `json:"claims"`
	Actions        []Action `json:"actions"`
	Sources        []Source `json:"sources"`
	Coverage       Coverage `json:"coverage"`
	Warnings       []string `json:"warnings"`
	ReviewComplete bool     `json:"review_complete_for_cached_scope"`
	Reviewed       int      `json:"reviewed_messages"`
}
type Job struct {
	ID        string          `json:"job_id"`
	SessionID string          `json:"session_id,omitempty"`
	Kind      string          `json:"kind"`
	Status    string          `json:"status"`
	Processed int             `json:"processed"`
	Total     int             `json:"total"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	Updated   int64           `json:"updated"`
}

// Guard is attached by HTTP auth. Long-running work rechecks the credential
// without storing bearer tokens or cookies in the database.
type guardKey struct{}

func WithGuard(ctx context.Context, f func() bool) context.Context {
	return context.WithValue(ctx, guardKey{}, f)
}
func checkGuard(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f, ok := ctx.Value(guardKey{}).(func() bool); ok && !f() {
		return ErrScope
	}
	return nil
}
func digest(parts ...string) string {
	s := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(s[:])
}
func bounded(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
