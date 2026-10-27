package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"

	"github.com/datahase/whatsapp-mcp/internal/media"
	"github.com/datahase/whatsapp-mcp/internal/security"
	"github.com/datahase/whatsapp-mcp/internal/store"
)

// Send modes. Ordinary audio and voice notes are distinct: only voice_note
// sets WhatsApp's push-to-talk flag.
const (
	ModeImage     = "image"
	ModeVideo     = "video"
	ModeAudio     = "audio"
	ModeVoiceNote = "voice_note"
	ModeDocument  = "document"
)

// OutMedia is a verified file to attach. Path must be inside the account
// directory.
type OutMedia struct {
	Path        string
	Mode        string
	MIME        string
	Filename    string
	DurationSec uint32
}

// Outgoing is one message to dispatch. MessageID is generated and recorded
// by the caller before dispatch so receipts and retries can be reconciled.
type Outgoing struct {
	To        types.JID
	MessageID string
	Text      string // body, or caption for image/video/document
	Media     *OutMedia
}

// SendOutcome reports whether the message reached SendMessage. When an error
// is returned with Dispatched=true, WhatsApp may or may not have the message.
type SendOutcome struct {
	Dispatched bool
	ServerTime time.Time
}

// ErrNotConnected is returned before dispatch when the account is offline.
// There is no offline queue.
var ErrNotConnected = errors.New("account is not connected to WhatsApp; nothing was sent")

// NewMessageID returns a WhatsApp message ID for a future send.
func (c *Client) NewMessageID() string { return string(c.wa.GenerateMessageID()) }

// ParseRecipient converts an exact phone number or JID into a JID. It never
// resolves names; ambiguous input must be resolved by searching first.
func ParseRecipient(recipient string) (types.JID, error) {
	return parseRecipient(recipient)
}

// CheckSendRate applies the account's send limits. It returns the time to
// wait when the send is not allowed.
func (c *Client) CheckSendRate(jid types.JID) (ok bool, retryAfter time.Duration, reason string) {
	if c.limiter == nil {
		return true, 0, ""
	}
	d := c.limiter.AllowSend(c.IsKnownContact(jid))
	return d.Allowed, d.RetryAfter, d.Reason
}

// SendOutgoing uploads any attachment and dispatches the message.
func (c *Client) SendOutgoing(ctx context.Context, o Outgoing) (SendOutcome, error) {
	if !c.connected() {
		return SendOutcome{}, ErrNotConnected
	}
	if o.MessageID == "" {
		return SendOutcome{}, errors.New("message ID must be generated before dispatch")
	}
	msg := &waProto.Message{MessageContextInfo: c.ephemeralContextInfo(ctx, o.To)}
	if o.Media != nil {
		if err := c.attachMedia(ctx, msg, o.Media, o.Text); err != nil {
			return SendOutcome{}, err
		}
	} else {
		msg.Conversation = proto.String(o.Text)
	}
	resp, err := c.sendMessage(ctx, o.To, msg, whatsmeow.SendRequestExtra{ID: types.MessageID(o.MessageID)})
	if err != nil {
		return SendOutcome{Dispatched: true}, fmt.Errorf("WhatsApp send did not confirm: %v", err)
	}
	mediaPath := ""
	if o.Media != nil {
		mediaPath = o.Media.Path
	}
	c.persistSent(ctx, o.To, string(resp.ID), o.Text, mediaPath, msg)
	return SendOutcome{Dispatched: true, ServerTime: resp.Timestamp}, nil
}

func (c *Client) connected() bool {
	if c.isConnected != nil {
		return c.isConnected()
	}
	return c.wa != nil && c.wa.IsConnected()
}

func (c *Client) sendMessage(ctx context.Context, to types.JID, msg *waProto.Message, extra whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error) {
	if c.sendFn != nil {
		return c.sendFn(ctx, to, msg, extra)
	}
	return c.wa.SendMessage(ctx, to, msg, extra)
}

func (c *Client) upload(ctx context.Context, r io.Reader, tmp io.ReadWriteSeeker, t whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
	if c.uploadFn != nil {
		return c.uploadFn(ctx, r, tmp, t)
	}
	return c.wa.UploadReader(ctx, r, tmp, t)
}

// ephemeralContextInfo returns the MessageContextInfo carrying the group's
// disappearing-message timer for chat, or nil if chat isn't a group,
// GetGroupInfo fails, or no timer is set. Shared by send and SendReply.
func (c *Client) ephemeralContextInfo(ctx context.Context, chat types.JID) *waProto.MessageContextInfo {
	if chat.Server != "g.us" {
		return nil
	}
	gi, err := c.wa.GetGroupInfo(ctx, chat)
	if err != nil || gi.DisappearingTimer == 0 {
		return nil
	}
	c.log.Infof("Auto-detected group disappearing timer: %d seconds", gi.DisappearingTimer)
	return &waProto.MessageContextInfo{
		MessageAddOnDurationInSecs: proto.Uint32(gi.DisappearingTimer),
	}
}

// IsKnownContact classifies a send target for the rate limiter. Groups we can
// send to are inherently known contexts, so they are never treated as cold.
// For 1:1 recipients, "known" means we have prior inbound history from that
// JID in the cache. On a store error we fail closed — treat the target as a
// non-contact so the stricter limit applies.
func (c *Client) IsKnownContact(jid types.JID) bool {
	if jid.Server == types.GroupServer {
		return true
	}
	known, err := c.store.HasInboundFrom(jid.String())
	if err != nil {
		c.log.Warnf("contact lookup failed for %s, treating as non-contact: %v", c.redactor.JID(jid.String()), err)
		return false
	}
	return known
}

// parseRecipient normalizes a phone number or JID string into types.JID.
func parseRecipient(recipient string) (types.JID, error) {
	canonical, err := NormalizeRecipient(recipient)
	if err != nil {
		return types.JID{}, err
	}
	if strings.Contains(canonical, "@") {
		jid, err := types.ParseJID(canonical)
		if err != nil {
			return types.JID{}, fmt.Errorf("Error parsing JID: %v", err)
		}
		return jid, nil
	}
	return types.JID{User: canonical, Server: types.DefaultUserServer}, nil
}

// attachMedia streams the file through whatsmeow's encrypting uploader and
// fills the message field for the requested mode. The path is re-checked
// against the account directory, which is also where conversions are
// written, so converted voice notes pass the same confinement check.
func (c *Client) attachMedia(ctx context.Context, msg *waProto.Message, m *OutMedia, caption string) error {
	safePath, err := security.ValidateMediaPath(m.Path, c.storeDir)
	if err != nil {
		return fmt.Errorf("media rejected: %w", err)
	}
	f, err := os.Open(safePath)
	if err != nil {
		return fmt.Errorf("open media: %w", err)
	}
	defer f.Close()
	workDir := filepath.Join(c.storeDir, "media", "work")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(workDir, "upload-*.enc")
	if err != nil {
		return err
	}
	defer func() { tmp.Close(); os.Remove(tmp.Name()) }()

	var waType whatsmeow.MediaType
	switch m.Mode {
	case ModeImage:
		waType = whatsmeow.MediaImage
	case ModeVideo:
		waType = whatsmeow.MediaVideo
	case ModeAudio, ModeVoiceNote:
		waType = whatsmeow.MediaAudio
	case ModeDocument:
		waType = whatsmeow.MediaDocument
	default:
		return fmt.Errorf("unsupported send mode %q", m.Mode)
	}
	resp, err := c.upload(ctx, f, tmp, waType)
	if err != nil {
		return fmt.Errorf("media upload failed: %v", err)
	}
	c.log.Debugf("Media uploaded: url=%s bytes=%d", c.redactor.URL(resp.URL), resp.FileLength)

	switch m.Mode {
	case ModeImage:
		msg.ImageMessage = &waProto.ImageMessage{
			Caption: proto.String(caption), Mimetype: proto.String(m.MIME),
			URL: &resp.URL, DirectPath: &resp.DirectPath, MediaKey: resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256, FileSHA256: resp.FileSHA256, FileLength: &resp.FileLength,
		}
	case ModeVideo:
		msg.VideoMessage = &waProto.VideoMessage{
			Caption: proto.String(caption), Mimetype: proto.String(m.MIME), Seconds: proto.Uint32(m.DurationSec),
			URL: &resp.URL, DirectPath: &resp.DirectPath, MediaKey: resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256, FileSHA256: resp.FileSHA256, FileLength: &resp.FileLength,
		}
	case ModeAudio, ModeVoiceNote:
		voice := m.Mode == ModeVoiceNote
		am := &waProto.AudioMessage{
			Mimetype: proto.String(m.MIME), Seconds: proto.Uint32(m.DurationSec), PTT: proto.Bool(voice),
			URL: &resp.URL, DirectPath: &resp.DirectPath, MediaKey: resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256, FileSHA256: resp.FileSHA256, FileLength: &resp.FileLength,
		}
		if voice {
			am.Waveform = media.PlaceholderWaveform(m.DurationSec)
		}
		msg.AudioMessage = am
	case ModeDocument:
		name := m.Filename
		if name == "" {
			name = "file"
		}
		msg.DocumentMessage = &waProto.DocumentMessage{
			Title: proto.String(name), FileName: proto.String(name), Caption: proto.String(caption), Mimetype: proto.String(m.MIME),
			URL: &resp.URL, DirectPath: &resp.DirectPath, MediaKey: resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256, FileSHA256: resp.FileSHA256, FileLength: &resp.FileLength,
		}
	}
	return nil
}

// persistSent writes a cache row for a message we sent so the local history
// stays complete regardless of device multiplexing.
func (c *Client) persistSent(ctx context.Context, recipientJID types.JID, msgID, message, mediaPath string, msg *waProto.Message) {
	chatJID := recipientJID.String()
	if !strings.HasSuffix(chatJID, "@g.us") {
		chatJID = c.store.ResolveLIDToJID(chatJID)
	}

	chatName := c.store.FindChatName(chatJID)
	if chatName == "" {
		chatName = c.resolveChatNameFallback(ctx, recipientJID, "")
	}

	now := time.Now()
	if err := c.store.StoreChat(chatJID, chatName, now); err != nil {
		c.log.Warnf("Failed to store chat for sent message: %v", err)
	}

	ourJID := ""
	if c.wa.Store.ID != nil {
		ourJID = c.wa.Store.ID.User
	}

	var (
		storedMediaType, storedFilename, storedURL, storedDirectPath string
		storedMediaKey, storedFileSHA256, storedFileEnc              []byte
		storedFileLength                                             uint64
	)
	if mediaPath != "" {
		storedMediaType, storedFilename, storedURL, storedDirectPath,
			storedMediaKey, storedFileSHA256, storedFileEnc, storedFileLength = extractMediaInfo(msg)
	}

	err := c.store.StoreMessage(ctx, store.Message{
		ID:         msgID,
		ChatJID:    chatJID,
		Sender:     ourJID,
		Content:    message,
		Timestamp:  now,
		IsFromMe:   true,
		MediaType:  storedMediaType,
		Filename:   storedFilename,
		URL:        storedURL,
		DirectPath: storedDirectPath,
	}, storedMediaKey, storedFileSHA256, storedFileEnc, storedFileLength)
	if err != nil {
		c.log.Warnf("Failed to store sent message: %v", err)
		return
	}
	c.log.Debugf("[%s] -> %s [%s]: %s", now.Format("2006-01-02 15:04:05"), c.redactor.JID(chatJID), c.redactor.MsgID(msgID), c.redactor.Body(message))
}
