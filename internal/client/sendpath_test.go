package client

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCommon"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	wmstore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"

	"github.com/datahase/whatsapp-mcp/internal/media"
	"github.com/datahase/whatsapp-mcp/internal/security"
	"github.com/datahase/whatsapp-mcp/internal/store"
)

type fakeWire struct {
	uploads int
	sent    []*waProto.Message
	ids     []string
	sendErr error
}

func newSendTestClient(t *testing.T, w *fakeWire) *Client {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := NewStderrLogger("test", "ERROR", false)
	return &Client{
		wa:          whatsmeow.NewClient(&wmstore.Device{}, log),
		store:       st,
		storeDir:    dir,
		log:         log,
		redactor:    &security.Redactor{},
		isConnected: func() bool { return true },
		uploadFn: func(_ context.Context, r io.Reader, _ io.ReadWriteSeeker, _ whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
			n, err := io.Copy(io.Discard, r)
			w.uploads++
			return whatsmeow.UploadResponse{URL: "https://mmg.whatsapp.net/x", DirectPath: "/x", FileLength: uint64(n)}, err
		},
		sendFn: func(_ context.Context, _ types.JID, m *waProto.Message, extra whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error) {
			w.sent = append(w.sent, m)
			w.ids = append(w.ids, string(extra.ID))
			if w.sendErr != nil {
				return whatsmeow.SendResponse{}, w.sendErr
			}
			return whatsmeow.SendResponse{ID: extra.ID, Timestamp: time.Now()}, nil
		},
	}
}

// Regression for the upstream review finding: conversion output used to land
// in the system temp dir, which the send path then rejected.
func TestConvertedVoiceNoteGoesThroughSendAdapter(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	w := &fakeWire{}
	c := newSendTestClient(t, w)
	src := filepath.Join(c.storeDir, "media", "staged", "in.mp3")
	_ = os.MkdirAll(filepath.Dir(src), 0o700)
	if out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=duration=2", "-c:a", "libmp3lame", "-y", src).CombinedOutput(); err != nil {
		t.Skipf("mp3 fixture: %v %s", err, out)
	}
	conv, err := media.ConvertToOpusOgg(context.Background(), src, filepath.Join(c.storeDir, "media", "work"), 600)
	if err != nil {
		t.Fatal(err)
	}
	to := types.NewJID("15550001111", types.DefaultUserServer)
	res, err := c.SendOutgoing(context.Background(), Outgoing{To: to, MessageID: "3EB0TESTVOICE", Media: &OutMedia{Path: conv, Mode: ModeVoiceNote, MIME: "audio/ogg; codecs=opus", DurationSec: 2}})
	if err != nil || !res.Dispatched {
		t.Fatalf("voice note send: %v %+v", err, res)
	}
	am := w.sent[0].GetAudioMessage()
	if am == nil || !am.GetPTT() || len(am.GetWaveform()) == 0 || w.ids[0] != "3EB0TESTVOICE" {
		t.Fatalf("expected PTT voice note with pre-generated ID, got %+v id=%v", am, w.ids)
	}

	// Ordinary audio is a distinct mode: no push-to-talk flag.
	if _, err := c.SendOutgoing(context.Background(), Outgoing{To: to, MessageID: "3EB0TESTAUDIO", Media: &OutMedia{Path: src, Mode: ModeAudio, MIME: "audio/mpeg", DurationSec: 2}}); err != nil {
		t.Fatal(err)
	}
	if w.sent[1].GetAudioMessage().GetPTT() {
		t.Fatal("ordinary audio was marked as a voice note")
	}
}

func TestSendRejectsPathOutsideAccountBeforeDispatch(t *testing.T) {
	w := &fakeWire{}
	c := newSendTestClient(t, w)
	other := filepath.Join(t.TempDir(), "x.jpg")
	_ = os.WriteFile(other, []byte("x"), 0o600)
	res, err := c.SendOutgoing(context.Background(), Outgoing{To: types.NewJID("1555", types.DefaultUserServer), MessageID: "ID1", Media: &OutMedia{Path: other, Mode: ModeImage, MIME: "image/jpeg"}})
	if err == nil || res.Dispatched || w.uploads != 0 || len(w.sent) != 0 {
		t.Fatalf("outside path must fail before upload/dispatch: err=%v res=%+v uploads=%d", err, res, w.uploads)
	}
}

func TestSendFailuresReportDispatch(t *testing.T) {
	w := &fakeWire{sendErr: errors.New("timeout")}
	c := newSendTestClient(t, w)
	res, err := c.SendOutgoing(context.Background(), Outgoing{To: types.NewJID("1555", types.DefaultUserServer), MessageID: "ID2", Text: "hi"})
	if err == nil || !res.Dispatched {
		t.Fatalf("ambiguous send error must be marked dispatched: %v %+v", err, res)
	}
	c.isConnected = func() bool { return false }
	res, err = c.SendOutgoing(context.Background(), Outgoing{To: types.NewJID("1555", types.DefaultUserServer), MessageID: "ID3", Text: "hi"})
	if !errors.Is(err, ErrNotConnected) || res.Dispatched {
		t.Fatalf("offline send must fail without dispatch: %v %+v", err, res)
	}
}

func TestDownloadMessageMediaBoundsAndVerifies(t *testing.T) {
	c := newSendTestClient(t, &fakeWire{})
	ctx := context.Background()
	_ = c.store.StoreChat("1555@s.whatsapp.net", "x", time.Now())
	msg := store.Message{ID: "M1", ChatJID: "1555@s.whatsapp.net", Sender: "1555", Timestamp: time.Now(), MediaType: "image", Filename: "a.jpg", URL: "https://mmg.whatsapp.net/v/t62/x", DirectPath: "/v/t62/x"}
	if err := c.store.StoreMessage(ctx, msg, []byte("k"), []byte("s"), []byte("e"), 5); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(c.storeDir, "media", "received", "obj1")
	calls := 0
	c.downloadToFile = func(_ context.Context, _ whatsmeow.DownloadableMessage, f whatsmeow.File) error {
		calls++
		_, err := f.Write([]byte("12345"))
		return err
	}
	if _, _, err := c.DownloadMessageMedia(ctx, msg.ChatJID, "M1", dest, 4); err == nil || calls != 0 {
		t.Fatalf("over-limit download must be refused before network: err=%v calls=%d", err, calls)
	}
	if _, _, err := c.DownloadMessageMedia(ctx, msg.ChatJID, "M1", dest, 10); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "12345" {
		t.Fatalf("dest content %q", b)
	}
	c.downloadToFile = func(_ context.Context, _ whatsmeow.DownloadableMessage, f whatsmeow.File) error {
		_, err := f.Write([]byte("12"))
		return err
	}
	dest2 := filepath.Join(c.storeDir, "media", "received", "obj2")
	if _, _, err := c.DownloadMessageMedia(ctx, msg.ChatJID, "M1", dest2, 10); err == nil {
		t.Fatal("short download accepted")
	}
	if _, err := os.Stat(dest2); !os.IsNotExist(err) {
		t.Fatal("partial download left at destination")
	}
	if _, _, err := c.DownloadMessageMedia(ctx, msg.ChatJID, "M1", filepath.Join(t.TempDir(), "escape"), 10); err == nil {
		t.Fatal("destination outside account accepted")
	}
}

// Only the original sender (or we, for our own messages) can edit or revoke
// a cached message; another participant's protocol message is ignored.
func TestEditRevokeRequireOriginalSender(t *testing.T) {
	c := newSendTestClient(t, &fakeWire{})
	ctx := context.Background()
	chat := "123-456@g.us"
	_ = c.store.StoreChat(chat, "g", time.Now())
	_ = c.store.StoreMessage(ctx, store.Message{ID: "M1", ChatJID: chat, Sender: "15550000001", Content: "original", Timestamp: time.Now()}, nil, nil, nil, 0)
	edit := func(text string) *waProto.Message {
		return &waProto.Message{ProtocolMessage: &waProto.ProtocolMessage{Type: waProto.ProtocolMessage_MESSAGE_EDIT.Enum(),
			Key: &waCommon.MessageKey{ID: proto.String("M1")}, EditedMessage: &waProto.Message{Conversation: proto.String(text)}}}
	}
	revoke := &waProto.Message{ProtocolMessage: &waProto.ProtocolMessage{Type: waProto.ProtocolMessage_REVOKE.Enum(), Key: &waCommon.MessageKey{ID: proto.String("M1")}}}
	content := func() string {
		mc, err := c.store.GetMessageContext(ctx, chat, "M1", 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		return mc.Message.Content
	}
	c.handleProtocol(chat, "15550000002", false, edit("forged"))
	c.handleProtocol(chat, "15550000002", false, revoke)
	c.handleProtocol(chat, "", true, revoke) // we did not send M1
	if got := content(); got != "original" {
		t.Fatalf("other participant changed the message: %q", got)
	}
	c.handleProtocol(chat, "15550000001", false, edit("fixed typo"))
	if got := content(); got != "fixed typo" {
		t.Fatalf("sender's edit not applied: %q", got)
	}
	c.handleProtocol(chat, "15550000001", false, revoke)
	if got := content(); got != "[message deleted by sender]" {
		t.Fatalf("sender's revoke not applied: %q", got)
	}
}
