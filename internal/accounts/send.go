package accounts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/types"

	"github.com/datahase/whatsapp-mcp/internal/client"
	"github.com/datahase/whatsapp-mcp/internal/media"
	"github.com/datahase/whatsapp-mcp/internal/store"
)

// SendRequest is one explicitly requested send.
type SendRequest struct {
	IdempotencyKey string
	Recipient      string
	Text           string
	MediaID        string
	// Mode is "" for text, or image/video/audio/voice_note/document.
	// For send_media an empty mode means "infer from verified content".
	Mode     string
	IsMedia  bool
	MaxVoice int // seconds; 0 = no cap
}

// SendResult is the outcome of a send request.
type SendResult struct {
	AccountID string        `json:"account_id"`
	Replayed  bool          `json:"replayed"`
	Operation *store.SendOp `json:"operation"`
	RetryHint string        `json:"note,omitempty"`
}

// RateLimitError carries the wait time before another send is allowed.
type RateLimitError struct {
	Reason     string
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate limited: %s; retry after %s (nothing was sent)", e.Reason, e.RetryAfter.Round(time.Second))
}

// Send executes an explicit send. A repeated call with the same idempotency
// key returns the recorded operation instead of sending again, including
// after timeouts and restarts.
func (a *Account) Send(ctx context.Context, req SendRequest) (*SendResult, error) {
	key := strings.TrimSpace(req.IdempotencyKey)
	if key == "" || len(key) > 128 {
		return nil, errors.New("idempotency_key: required, 1-128 characters; reuse it only to retry the same send")
	}
	to, err := client.ParseRecipient(req.Recipient)
	if err != nil {
		return nil, err
	}
	if to.Server != types.DefaultUserServer && to.Server != types.GroupServer {
		return nil, fmt.Errorf("recipient: unsupported JID server %q", to.Server)
	}
	st, err := a.Store()
	if err != nil {
		return nil, err
	}
	kind := "text"
	if req.IsMedia {
		kind = "media"
		if req.Mode == client.ModeVoiceNote {
			kind = "voice_note"
		}
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{kind, to.String(), req.Text, req.MediaID, req.Mode}, "\x00")))
	fp := hex.EncodeToString(sum[:])

	a.sendMu.Lock()
	defer a.sendMu.Unlock()

	if op, err := st.GetSendOpByKey(ctx, key); err == nil {
		if _, _, err := st.BeginSendOp(ctx, key, fp, kind, to.String(), req.MediaID, op.WAMessageID); err != nil {
			return nil, err
		}
		res := &SendResult{AccountID: a.ID, Replayed: true, Operation: op}
		if op.Status == store.OpUnknown {
			res.RetryHint = "outcome is unknown; check the chat before sending again with a new idempotency_key"
		}
		return res, nil
	}

	cl, err := a.liveClient()
	if err != nil {
		return nil, err
	}

	var out *client.OutMedia
	var cleanup string
	if req.IsMedia {
		out, cleanup, err = a.prepareMedia(ctx, req)
		if cleanup != "" {
			defer os.Remove(cleanup)
		}
		if err != nil {
			return nil, err
		}
		if req.Mode == client.ModeAudio || req.Mode == client.ModeVoiceNote {
			req.Text = "" // WhatsApp audio messages have no caption
		}
	} else if strings.TrimSpace(req.Text) == "" {
		return nil, errors.New("message: required")
	}

	if ok, wait, reason := cl.CheckSendRate(to); !ok {
		return nil, &RateLimitError{Reason: reason, RetryAfter: wait}
	}

	op, _, err := st.BeginSendOp(ctx, key, fp, kind, to.String(), req.MediaID, cl.NewMessageID())
	if err != nil {
		return nil, err
	}
	_ = st.SetSendOpStatus(op.OpID, store.OpSending, "", nil)
	res, sendErr := cl.SendOutgoing(ctx, client.Outgoing{To: to, MessageID: op.WAMessageID, Text: req.Text, Media: out})
	switch {
	case sendErr == nil:
		ts := res.ServerTime.UTC()
		_ = st.SetSendOpStatus(op.OpID, store.OpAccepted, "", &ts)
	case res.Dispatched:
		_ = st.SetSendOpStatus(op.OpID, store.OpUnknown, sendErr.Error(), nil)
	default:
		_ = st.SetSendOpStatus(op.OpID, store.OpFailed, sendErr.Error(), nil)
	}
	final, err := st.GetSendOp(ctx, op.OpID)
	if err != nil {
		return nil, err
	}
	r := &SendResult{AccountID: a.ID, Operation: final}
	if final.Status == store.OpUnknown {
		r.RetryHint = "WhatsApp did not confirm the send; it may still arrive. Check get_send_status or the chat before sending again with a new idempotency_key"
	}
	return r, nil
}

// prepareMedia resolves the account-owned media and verifies that the
// requested mode matches its content. Voice notes are converted to Ogg/Opus
// inside the account's work directory when needed.
func (a *Account) prepareMedia(ctx context.Context, req SendRequest) (out *client.OutMedia, cleanup string, err error) {
	obj, path, err := a.OpenMedia(req.MediaID)
	if err != nil {
		return nil, "", err
	}
	st, _ := a.Store()
	_ = st.PinMedia(obj.ID, time.Now().Add(10*time.Minute))

	mode := req.Mode
	switch mode {
	case "":
		mode = obj.MediaType // image/video/audio/document from verified content
	case client.ModeDocument:
	case client.ModeVoiceNote:
		if obj.MediaType != media.TypeAudio {
			return nil, "", fmt.Errorf("media_id is %s content, not audio; cannot send as a voice note", obj.MediaType)
		}
	default:
		if mode != obj.MediaType {
			return nil, "", fmt.Errorf("mode %q does not match the file's verified content (%s, %s); use mode=document to send it as a file", mode, obj.MediaType, obj.MIME)
		}
	}
	out = &client.OutMedia{Path: path, Mode: mode, MIME: obj.MIME, Filename: obj.Filename, DurationSec: secs(obj.DurationMS)}
	if mode == client.ModeDocument && obj.MediaType != media.TypeDocument && out.MIME == "" {
		out.MIME = "application/octet-stream"
	}
	if mode != client.ModeVoiceNote {
		return out, "", nil
	}
	if req.MaxVoice > 0 && obj.DurationMS > int64(req.MaxVoice)*1000 {
		return nil, "", fmt.Errorf("audio is %ds, above the %ds voice-note limit", obj.DurationMS/1000, req.MaxVoice)
	}
	if obj.Codec == "opus" && obj.MIME == "audio/ogg; codecs=opus" {
		return out, "", nil
	}
	conv, err := media.ConvertToOpusOgg(ctx, path, a.WorkDir(), 0)
	if err != nil {
		return nil, "", err
	}
	info, err := media.Detect(ctx, conv, "")
	if err != nil || !info.IsOpusOgg() {
		return nil, conv, fmt.Errorf("converted audio failed verification: %v", err)
	}
	out.Path, out.MIME, out.DurationSec = conv, info.MIME, secs(info.DurationMS)
	return out, conv, nil
}

func secs(ms int64) uint32 {
	if ms <= 0 {
		return 1
	}
	return uint32((ms + 999) / 1000)
}

// GetSendStatus returns a persisted operation by operation ID or key.
func (a *Account) GetSendStatus(ctx context.Context, idOrKey string) (*store.SendOp, error) {
	st, err := a.Store()
	if err != nil {
		return nil, err
	}
	op, err := st.GetSendOp(ctx, idOrKey)
	if err != nil {
		return nil, fmt.Errorf("no send operation %q in account %q", idOrKey, a.ID)
	}
	return op, nil
}

// RequestHistorySync asks the phone for older messages in one chat. The
// result arrives asynchronously as a history-sync event.
func (a *Account) RequestHistorySync(ctx context.Context, chatJID string, count int) (string, error) {
	cl, err := a.liveClient()
	if err != nil {
		return "", err
	}
	if count <= 0 || count > 50 {
		count = 50
	}
	return cl.RequestHistorySync(ctx, chatJID, time.Time{}, client.AnchorOldest, count, false)
}
