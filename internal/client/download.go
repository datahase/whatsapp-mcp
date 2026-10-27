package client

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/datahase/whatsapp-mcp/internal/security"

	"go.mau.fi/whatsmeow"
)

// MediaDownloader implements whatsmeow.DownloadableMessage for cached media.
type MediaDownloader struct {
	URL           string
	DirectPath    string
	MediaKey      []byte
	FileLength    uint64
	FileSHA256    []byte
	FileEncSHA256 []byte
	MediaType     whatsmeow.MediaType
}

func (d *MediaDownloader) GetDirectPath() string    { return d.DirectPath }
func (d *MediaDownloader) GetURL() string           { return d.URL }
func (d *MediaDownloader) GetMediaKey() []byte      { return d.MediaKey }
func (d *MediaDownloader) GetFileLength() uint64    { return d.FileLength }
func (d *MediaDownloader) GetFileSHA256() []byte    { return d.FileSHA256 }
func (d *MediaDownloader) GetFileEncSHA256() []byte { return d.FileEncSHA256 }
func (d *MediaDownloader) GetMediaType() whatsmeow.MediaType {
	return d.MediaType
}

// ErrNotMedia means the message has no attachment.
var ErrNotMedia = errors.New("not a media message")

// DownloadMessageMedia streams a received attachment to destPath, which must
// lie inside the account directory. The size is checked against maxBytes
// before any network transfer, and whatsmeow verifies the content hashes
// while writing. A partial file never replaces destPath.
func (c *Client) DownloadMessageMedia(ctx context.Context, chatJID, messageID, destPath string, maxBytes int64) (mediaType, filename string, err error) {
	mediaType, filename, url, directPath, mediaKey, fileSHA256, fileEncSHA256, fileLength, err := c.store.GetMediaInfo(messageID, chatJID)
	if err != nil {
		return "", "", fmt.Errorf("message not found in this account: %w", err)
	}
	if mediaType == "" {
		return "", "", ErrNotMedia
	}
	if url == "" || len(mediaKey) == 0 || len(fileSHA256) == 0 || len(fileEncSHA256) == 0 || fileLength == 0 {
		return "", "", errors.New("media keys are not available for this message (old history, view-once or revoked)")
	}
	if maxBytes > 0 && fileLength > uint64(maxBytes) {
		return "", "", fmt.Errorf("attachment is %d bytes, above the %d byte limit", fileLength, maxBytes)
	}
	var waMediaType whatsmeow.MediaType
	switch mediaType {
	case "image":
		waMediaType = whatsmeow.MediaImage
	case "video":
		waMediaType = whatsmeow.MediaVideo
	case "audio":
		waMediaType = whatsmeow.MediaAudio
	case "document":
		waMediaType = whatsmeow.MediaDocument
	default:
		return "", "", fmt.Errorf("unsupported media type: %s", mediaType)
	}
	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	if _, err := security.ValidateOutputPath(destPath, c.storeDir); err != nil {
		return "", "", err
	}
	tmp, err := os.CreateTemp(dir, ".download-*.part")
	if err != nil {
		return "", "", err
	}
	tmpPath := tmp.Name()
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmpPath)
		}
	}()
	d := &MediaDownloader{
		URL: url, DirectPath: preferredDirectPath(directPath, url), MediaKey: mediaKey,
		FileLength: fileLength, FileSHA256: fileSHA256, FileEncSHA256: fileEncSHA256, MediaType: waMediaType,
	}
	if err = c.downloadToFile(ctx, d, tmp); err != nil {
		return "", "", fmt.Errorf("download failed (WhatsApp may have expired this media): %w", err)
	}
	info, err := tmp.Stat()
	if err != nil {
		return "", "", err
	}
	if uint64(info.Size()) != fileLength {
		err = fmt.Errorf("downloaded %d bytes, expected %d", info.Size(), fileLength)
		return "", "", err
	}
	if err = tmp.Close(); err != nil {
		return "", "", err
	}
	if err = os.Rename(tmpPath, destPath); err != nil {
		return "", "", err
	}
	return mediaType, filename, nil
}

// preferredDirectPath picks the value handed to whatsmeow as the download's
// DirectPath. New rows carry a stored directPath captured verbatim from the
// media protobuf (including the signed `?ccb=&oh=&oe=&_nc_sid=` CDN auth
// query), which is authoritative. Legacy rows written before the
// direct_path column existed fall back to parsing URL — the same behaviour
// prior daemon builds had.
func preferredDirectPath(stored, url string) string {
	if stored != "" {
		return stored
	}
	return extractDirectPathFromURL(url)
}

// extractDirectPathFromURL turns a CDN URL into the /direct/path/form that
// whatsmeow's Download requires when the URL does not already contain it.
//
// The query string MUST be preserved: whatsmeow builds the request URL as
// `https://<host><directPath>&hash=…&mms-type=…&__wa-mms=`, which assumes
// directPath already ends in `?ccb=…&oh=…&oe=…&_nc_sid=…` (WhatsApp's signed
// CDN auth params). Stripping the query yields a malformed URL and every
// download returns 403.
func extractDirectPathFromURL(url string) string {
	parts := strings.SplitN(url, ".net/", 2)
	if len(parts) < 2 {
		return url
	}
	return "/" + parts[1]
}
