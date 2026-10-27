package accounts

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/datahase/whatsapp-mcp/internal/media"
	"github.com/datahase/whatsapp-mcp/internal/security"
	"github.com/datahase/whatsapp-mcp/internal/store"
)

// ErrTooLarge is returned when an upload exceeds the media object limit.
var ErrTooLarge = errors.New("file exceeds the media size limit")

// StageUpload streams r into the account's staged media area, hashing as it
// goes and never holding the whole file in memory. Content is verified after
// writing; an unreadable or oversized file is removed.
func (a *Account) StageUpload(ctx context.Context, r io.Reader, filename string) (*store.MediaObject, error) {
	st, err := a.Store()
	if err != nil {
		return nil, err
	}
	max := a.m.cfg.Limits.MaxMediaBytes
	id := store.NewID("med_")
	rel := filepath.Join("media", "staged", id)
	final := filepath.Join(a.Dir, rel)
	tmp, err := os.OpenFile(final+".part", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, max+1))
	cerr := tmp.Close()
	if err == nil {
		err = cerr
	}
	if err == nil && n > max {
		err = fmt.Errorf("%w (%d bytes)", ErrTooLarge, max)
	}
	if err == nil && n == 0 {
		err = errors.New("empty upload")
	}
	if err != nil {
		os.Remove(final + ".part")
		return nil, err
	}
	if err := os.Rename(final+".part", final); err != nil {
		os.Remove(final + ".part")
		return nil, err
	}
	safeName := ""
	if filename != "" {
		safeName = security.SafeFilename(filepath.Base(filename))
	}
	info, err := media.Detect(ctx, final, safeName)
	if err != nil {
		os.Remove(final)
		return nil, fmt.Errorf("file rejected: %w", err)
	}
	now := time.Now().UTC()
	exp := now.Add(a.m.cfg.Limits.StagedUploadTTL)
	obj := &store.MediaObject{
		ID: id, Origin: "upload", RelPath: rel, MIME: info.MIME, Size: n, SHA256: hex.EncodeToString(h.Sum(nil)),
		Filename: safeName, MediaType: info.MediaType, DurationMS: info.DurationMS, Codec: info.Codec,
		CreatedAt: now, LastAccess: now, ExpiresAt: &exp,
	}
	if err := st.PutMedia(obj); err != nil {
		os.Remove(final)
		return nil, err
	}
	return obj, nil
}

// OpenMedia resolves a media ID owned by this account to its object and
// absolute path. IDs from another account are not found.
func (a *Account) OpenMedia(id string) (*store.MediaObject, string, error) {
	st, err := a.Store()
	if err != nil {
		return nil, "", err
	}
	if !strings.HasPrefix(id, "med_") {
		return nil, "", fmt.Errorf("media_id %q not found in account %q", id, a.ID)
	}
	obj, err := st.GetMedia(id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", fmt.Errorf("media_id %q not found in account %q (it may have expired)", id, a.ID)
	} else if err != nil {
		return nil, "", err
	}
	path, err := security.ValidateMediaPath(filepath.Join(a.Dir, obj.RelPath), a.Dir)
	if err != nil {
		return nil, "", fmt.Errorf("media_id %q is no longer available: %w", id, err)
	}
	return obj, path, nil
}

// FetchReceived returns the cached copy of a received attachment, downloading
// only that item when it is not cached yet.
func (a *Account) FetchReceived(ctx context.Context, chatJID, messageID string) (*store.MediaObject, string, error) {
	st, err := a.Store()
	if err != nil {
		return nil, "", err
	}
	if obj, err := st.GetReceivedMedia(chatJID, messageID); err == nil {
		if o, p, err := a.OpenMedia(obj.ID); err == nil {
			return o, p, nil
		}
		_ = st.DeleteMedia(obj.ID)
	}
	cl, err := a.liveClient()
	if err != nil {
		return nil, "", err
	}
	id := store.NewID("med_")
	rel := filepath.Join("media", "received", id)
	dest := filepath.Join(a.Dir, rel)
	mediaType, filename, err := cl.DownloadMessageMedia(ctx, chatJID, messageID, dest, a.m.cfg.Limits.MaxMediaBytes)
	if err != nil {
		return nil, "", err
	}
	info, derr := media.Detect(ctx, dest, filename)
	mime, dur, codec := "application/octet-stream", int64(0), ""
	if derr == nil {
		mime, dur, codec = info.MIME, info.DurationMS, info.Codec
	}
	sum, size, err := hashFile(dest)
	if err != nil {
		os.Remove(dest)
		return nil, "", err
	}
	now := time.Now().UTC()
	obj := &store.MediaObject{
		ID: id, Origin: "received", RelPath: rel, MIME: mime, Size: size, SHA256: sum,
		Filename: security.SafeFilename(filename), ChatJID: chatJID, MessageID: messageID, MediaType: mediaType,
		DurationMS: dur, Codec: codec, CreatedAt: now, LastAccess: now,
	}
	if err := st.PutMedia(obj); err != nil {
		os.Remove(dest)
		return nil, "", err
	}
	return obj, dest, nil
}

func hashFile(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

// WorkDir is where conversions for this account are written.
func (a *Account) WorkDir() string { return filepath.Join(a.Dir, "media", "work") }

// janitorLoop enforces media expiry, the shared media cache quota and the
// message cache budget, at startup and then periodically.
func (m *Manager) janitorLoop(ctx context.Context) {
	t := time.NewTicker(15 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopJan:
			return
		case <-t.C:
			m.RunJanitor(false)
		}
	}
}

// RunJanitor performs one cleanup pass. startup additionally removes
// abandoned partial and work files left by a previous process.
func (m *Manager) RunJanitor(startup bool) {
	now := time.Now().UTC()
	type cached struct {
		a   *Account
		obj *store.MediaObject
	}
	var received []cached
	var receivedBytes int64
	var dbBytes int64
	for _, a := range m.All() {
		st, err := a.Store()
		if err != nil {
			continue
		}
		if startup {
			removeMatching(filepath.Join(a.Dir, "media", "staged"), ".part")
			removeMatching(filepath.Join(a.Dir, "media", "received"), ".part")
			removeMatching(a.WorkDir(), "")
		}
		objs, err := st.ListMedia()
		if err != nil {
			continue
		}
		for _, o := range objs {
			pinned := o.PinnedUntil != nil && o.PinnedUntil.After(now)
			if o.Origin == "upload" && o.ExpiresAt != nil && o.ExpiresAt.Before(now) && !pinned {
				_ = os.Remove(filepath.Join(a.Dir, o.RelPath))
				_ = st.DeleteMedia(o.ID)
				continue
			}
			if o.Origin == "received" {
				receivedBytes += o.Size
				if !pinned {
					received = append(received, cached{a, o})
				}
			}
		}
		for _, f := range []string{"messages.db", "messages.db-wal"} {
			if fi, err := os.Stat(filepath.Join(a.Dir, f)); err == nil {
				dbBytes += fi.Size()
			}
		}
	}
	// Least-recently-used eviction of received media across accounts.
	// Session databases and staged uploads are never evicted here.
	sort.Slice(received, func(i, j int) bool { return received[i].obj.LastAccess.Before(received[j].obj.LastAccess) })
	for _, c := range received {
		if receivedBytes <= m.cfg.Limits.MediaCacheBytes {
			break
		}
		st, err := c.a.Store()
		if err != nil {
			continue
		}
		_ = os.Remove(filepath.Join(c.a.Dir, c.obj.RelPath))
		_ = st.DeleteMedia(c.obj.ID)
		receivedBytes -= c.obj.Size
	}
	full := dbBytes > m.cfg.Limits.MessageCacheBytes
	for _, a := range m.All() {
		if st, err := a.Store(); err == nil {
			st.SetCacheFull(full)
		}
	}
	if full {
		m.log.Warnf("message cache budget exceeded (%d > %d bytes); new messages are not being stored", dbBytes, m.cfg.Limits.MessageCacheBytes)
	}
}

func removeMatching(dir, suffix string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), suffix) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
