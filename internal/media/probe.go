package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // register decoder for DecodeConfig
	_ "image/jpeg" // register decoder for DecodeConfig
	_ "image/png"  // register decoder for DecodeConfig
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Media types used across the service. They match WhatsApp's message kinds.
const (
	TypeImage    = "image"
	TypeVideo    = "video"
	TypeAudio    = "audio"
	TypeDocument = "document"
)

// Info is the verified description of a file's content. It is derived from
// the bytes (sniffing, image headers, FFprobe), never from the extension
// alone.
type Info struct {
	MIME       string `json:"mime_type"`
	MediaType  string `json:"media_type"`
	Codec      string `json:"codec,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	// Note explains why a file was classified as a document when it looked
	// like media (e.g. an unsupported video codec).
	Note string `json:"note,omitempty"`
}

// Probe is the subset of ffprobe output we use.
type Probe struct {
	Format struct {
		FormatName string `json:"format_name"`
		Duration   string `json:"duration"`
	} `json:"format"`
	Streams []struct {
		CodecType   string `json:"codec_type"`
		CodecName   string `json:"codec_name"`
		Disposition struct {
			AttachedPic int `json:"attached_pic"`
		} `json:"disposition"`
	} `json:"streams"`
}

// DurationMS returns the container duration in milliseconds (0 if unknown).
func (p *Probe) DurationMS() int64 {
	f, err := strconv.ParseFloat(p.Format.Duration, 64)
	if err != nil || f < 0 {
		return 0
	}
	return int64(f * 1000)
}

func (p *Probe) stream(kind string) (codec string, ok bool) {
	for _, s := range p.Streams {
		if s.CodecType == kind && s.Disposition.AttachedPic == 0 {
			return s.CodecName, true
		}
	}
	return "", false
}

// ProbeTimeout bounds every ffprobe run.
const ProbeTimeout = 15 * time.Second

// ProbeFile runs ffprobe with a deadline. Malformed or truncated input returns
// an error; ffprobe runs in a child process, so hostile bytes cannot crash
// this service.
func ProbeFile(ctx context.Context, path string) (*Probe, error) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return nil, errors.New("ffprobe not found on PATH")
	}
	ctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-of", "json",
		"-show_entries", "format=format_name,duration:stream=codec_type,codec_name:stream_disposition=attached_pic",
		sanitizeFFmpegInputPath(path))
	cmd.Env = minimalEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("ffprobe timed out")
		}
		return nil, fmt.Errorf("unreadable media: %s", sanitizeStderr(stderr.String()))
	}
	var p Probe
	if err := json.Unmarshal(stdout.Bytes(), &p); err != nil {
		return nil, fmt.Errorf("parse ffprobe output: %w", err)
	}
	if len(p.Streams) == 0 {
		return nil, errors.New("unreadable media: no streams")
	}
	return &p, nil
}

// Detect classifies a file from its content. filename is used only to pick
// a MIME type for documents that sniffing cannot identify.
func Detect(ctx context.Context, path, filename string) (*Info, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	head := make([]byte, 512)
	n, _ := f.Read(head)
	head = head[:n]
	sniffed := http.DetectContentType(head)

	switch sniffed {
	case "image/jpeg", "image/png", "image/gif":
		if _, err := f.Seek(0, 0); err != nil {
			f.Close()
			return nil, err
		}
		_, _, decErr := image.DecodeConfig(f)
		f.Close()
		if decErr != nil {
			return nil, fmt.Errorf("corrupt image: %v", decErr)
		}
		return &Info{MIME: sniffed, MediaType: TypeImage}, nil
	case "image/webp":
		f.Close()
		if _, err := ProbeFile(ctx, path); err != nil {
			return nil, fmt.Errorf("corrupt image: %v", err)
		}
		return &Info{MIME: sniffed, MediaType: TypeImage}, nil
	}
	f.Close()

	if looksLikeAV(sniffed, head) {
		p, err := ProbeFile(ctx, path)
		if err != nil {
			return nil, err
		}
		return classifyAV(p, sniffed), nil
	}
	return &Info{MIME: documentMIME(sniffed, filename), MediaType: TypeDocument}, nil
}

func looksLikeAV(sniffed string, head []byte) bool {
	if strings.HasPrefix(sniffed, "audio/") || strings.HasPrefix(sniffed, "video/") || sniffed == "application/ogg" {
		return true
	}
	// MP4/M4A/MOV ("ftyp" box), raw AAC/MP3 frames and ID3 tags are not all
	// covered by net/http's sniffer.
	if len(head) >= 12 && string(head[4:8]) == "ftyp" {
		return true
	}
	if len(head) >= 3 && (string(head[:3]) == "ID3" || (head[0] == 0xFF && head[1]&0xE0 == 0xE0)) {
		return true
	}
	return false
}

func classifyAV(p *Probe, sniffed string) *Info {
	formats := p.Format.FormatName
	dur := p.DurationMS()
	if vc, ok := p.stream("video"); ok {
		ac, hasAudio := p.stream("audio")
		info := &Info{MIME: "video/mp4", Codec: vc, DurationMS: dur}
		if strings.Contains(formats, "mp4") && vc == "h264" && (!hasAudio || ac == "aac") {
			info.MediaType = TypeVideo
			return info
		}
		info.MediaType = TypeDocument
		info.MIME = sniffedOr(sniffed, "application/octet-stream")
		info.Note = "video is not H.264/AAC MP4; it can only be sent as a document"
		return info
	}
	ac, ok := p.stream("audio")
	if !ok {
		return &Info{MIME: sniffedOr(sniffed, "application/octet-stream"), MediaType: TypeDocument}
	}
	info := &Info{MediaType: TypeAudio, Codec: ac, DurationMS: dur}
	switch {
	case strings.Contains(formats, "ogg") && ac == "opus":
		info.MIME = "audio/ogg; codecs=opus"
	case strings.Contains(formats, "ogg"):
		info.MIME = "audio/ogg"
	case strings.Contains(formats, "mp3") || ac == "mp3":
		info.MIME = "audio/mpeg"
	case strings.Contains(formats, "mp4") || strings.Contains(formats, "mov"):
		info.MIME = "audio/mp4"
	case strings.Contains(formats, "wav"):
		info.MIME = "audio/wav"
	case strings.Contains(formats, "webm") || strings.Contains(formats, "matroska"):
		info.MIME = "audio/webm"
	case strings.Contains(formats, "flac"):
		info.MIME = "audio/flac"
	default:
		info.MIME = sniffedOr(sniffed, "audio/octet-stream")
	}
	return info
}

func sniffedOr(sniffed, fallback string) string {
	if sniffed == "" || sniffed == "application/octet-stream" || strings.HasPrefix(sniffed, "text/plain") {
		return fallback
	}
	return sniffed
}

func documentMIME(sniffed, filename string) string {
	if sniffed != "application/octet-stream" && !strings.HasPrefix(sniffed, "text/plain") {
		return sniffed
	}
	if m := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename))); m != "" {
		return m
	}
	if strings.HasPrefix(sniffed, "text/plain") {
		return "text/plain"
	}
	return "application/octet-stream"
}

// IsOpusOgg reports whether info describes an Ogg/Opus voice-note-ready file.
func (i *Info) IsOpusOgg() bool {
	return i.MediaType == TypeAudio && i.MIME == "audio/ogg; codecs=opus"
}

// ConvertTimeout bounds one ffmpeg conversion.
const ConvertTimeout = 2 * time.Minute

// ConvertToOpusOgg converts audio to a mono 48 kHz Ogg/Opus voice note,
// truncating at maxSeconds. The output is created inside workDir, which must
// be under the owning account's directory, so the send path's confinement
// check accepts it. The caller removes the returned file.
func ConvertToOpusOgg(ctx context.Context, inputPath, workDir string, maxSeconds int) (string, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return "", errors.New("ffmpeg not found on PATH; install ffmpeg to send converted voice notes")
	}
	if workDir == "" {
		return "", errors.New("conversion work directory is required")
	}
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		return "", fmt.Errorf("create work dir: %w", err)
	}
	tmp, err := os.CreateTemp(workDir, "voice-*.ogg")
	if err != nil {
		return "", fmt.Errorf("create work file: %w", err)
	}
	out := tmp.Name()
	tmp.Close()

	ctx, cancel := context.WithTimeout(ctx, ConvertTimeout)
	defer cancel()
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", sanitizeFFmpegInputPath(inputPath), "-vn", "-map_metadata", "-1"}
	if maxSeconds > 0 {
		args = append(args, "-t", strconv.Itoa(maxSeconds))
	}
	args = append(args, "-ac", "1", "-ar", "48000", "-c:a", "libopus", "-b:a", "32k",
		"-application", "voip", "-f", "ogg", "-y", out)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Env = minimalEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		os.Remove(out)
		if ctx.Err() != nil {
			return "", errors.New("audio conversion timed out")
		}
		return "", fmt.Errorf("ffmpeg conversion failed: %s", sanitizeStderr(stderr.String()))
	}
	return out, nil
}

// ConvertToWAV converts audio to 16 kHz mono 16-bit WAV in workDir, for
// analysis providers that do not accept Ogg/Opus. The caller removes it.
func ConvertToWAV(ctx context.Context, inputPath, workDir string) (string, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return "", errors.New("ffmpeg not found on PATH")
	}
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(workDir, "analysis-*.wav")
	if err != nil {
		return "", err
	}
	out := tmp.Name()
	tmp.Close()
	ctx, cancel := context.WithTimeout(ctx, ConvertTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", sanitizeFFmpegInputPath(inputPath), "-vn", "-map_metadata", "-1",
		"-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", "-f", "wav", "-y", out)
	cmd.Env = minimalEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		os.Remove(out)
		if ctx.Err() != nil {
			return "", errors.New("audio conversion timed out")
		}
		return "", fmt.Errorf("ffmpeg conversion failed: %s", sanitizeStderr(stderr.String()))
	}
	return out, nil
}
