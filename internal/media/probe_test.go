package media

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func needFFmpeg(t *testing.T) {
	t.Helper()
	for _, b := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skipf("%s not available", b)
		}
	}
}

func ffmpegFixture(t *testing.T, name string, args ...string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("ffmpeg", append(append([]string{"-hide_banner", "-loglevel", "error"}, args...), "-y", out)...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("fixture %s unavailable in this ffmpeg build: %v %s", name, err, b)
	}
	return out
}

func TestDetectClassifiesContentNotExtension(t *testing.T) {
	needFFmpeg(t)
	ctx := context.Background()
	opus := ffmpegFixture(t, "a.ogg", "-f", "lavfi", "-i", "sine=frequency=440:duration=2", "-c:a", "libopus")
	mp3 := ffmpegFixture(t, "a.mp3", "-f", "lavfi", "-i", "sine=frequency=440:duration=2", "-c:a", "libmp3lame")
	wav := ffmpegFixture(t, "a.wav", "-f", "lavfi", "-i", "sine=frequency=440:duration=2")
	mp4 := ffmpegFixture(t, "v.mp4", "-f", "lavfi", "-i", "testsrc=size=64x64:rate=10:duration=1", "-f", "lavfi", "-i", "sine=duration=1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest")
	webm := ffmpegFixture(t, "v.webm", "-f", "lavfi", "-i", "testsrc=size=64x64:rate=10:duration=1", "-c:v", "libvpx")

	// A voice-note-ready file renamed to .jpg is still Ogg/Opus audio.
	disguised := filepath.Join(t.TempDir(), "photo.jpg")
	b, _ := os.ReadFile(opus)
	_ = os.WriteFile(disguised, b, 0o600)

	cases := []struct {
		path, wantType, wantMIME string
		opus                     bool
	}{
		{opus, TypeAudio, "audio/ogg; codecs=opus", true},
		{disguised, TypeAudio, "audio/ogg; codecs=opus", true},
		{mp3, TypeAudio, "audio/mpeg", false},
		{wav, TypeAudio, "audio/wav", false},
		{mp4, TypeVideo, "video/mp4", false},
		{webm, TypeDocument, "video/webm", false},
	}
	for _, c := range cases {
		info, err := Detect(ctx, c.path, filepath.Base(c.path))
		if err != nil {
			t.Fatalf("%s: %v", c.path, err)
		}
		if info.MediaType != c.wantType || info.MIME != c.wantMIME || info.IsOpusOgg() != c.opus {
			t.Errorf("%s: got %+v, want type=%s mime=%s opus=%v", filepath.Base(c.path), info, c.wantType, c.wantMIME, c.opus)
		}
	}
}

func TestDetectImagesAndDocuments(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.White)
	_ = png.Encode(&buf, img)
	pngPath := filepath.Join(dir, "x.bin")
	_ = os.WriteFile(pngPath, buf.Bytes(), 0o600)
	info, err := Detect(context.Background(), pngPath, "x.bin")
	if err != nil || info.MediaType != TypeImage || info.MIME != "image/png" {
		t.Fatalf("png: %+v %v", info, err)
	}

	// PNG signature followed by garbage must be rejected, not sent.
	bad := filepath.Join(dir, "bad.png")
	_ = os.WriteFile(bad, append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 40)...), 0o600)
	if _, err := Detect(context.Background(), bad, "bad.png"); err == nil {
		t.Fatal("corrupt png accepted")
	}

	pdf := filepath.Join(dir, "doc")
	_ = os.WriteFile(pdf, []byte("%PDF-1.4\n%fake\n"), 0o600)
	info, err = Detect(context.Background(), pdf, "report.pdf")
	if err != nil || info.MediaType != TypeDocument || info.MIME != "application/pdf" {
		t.Fatalf("pdf: %+v %v", info, err)
	}
}

// Malformed and truncated Ogg data must produce an error, never a panic.
func TestMalformedOggRejected(t *testing.T) {
	needFFmpeg(t)
	opus := ffmpegFixture(t, "a.ogg", "-f", "lavfi", "-i", "sine=frequency=440:duration=2", "-c:a", "libopus")
	full, _ := os.ReadFile(opus)
	dir := t.TempDir()
	inputs := map[string][]byte{
		"header-only": []byte("OggS"),
		"page-header": append([]byte("OggS"), bytes.Repeat([]byte{0xFF}, 30)...),
		"truncated":   full[:40],
		"garbage":     append([]byte("OggS\x00\x02"), bytes.Repeat([]byte{0xAB}, 4000)...),
	}
	for name, data := range inputs {
		p := filepath.Join(dir, name+".ogg")
		_ = os.WriteFile(p, data, 0o600)
		info, err := Detect(context.Background(), p, name+".ogg")
		if err == nil && info.IsOpusOgg() && info.DurationMS > 0 {
			t.Errorf("%s: malformed Ogg accepted as a playable voice note: %+v", name, info)
		}
	}
}

func TestConvertWritesInsideWorkDirAndTruncates(t *testing.T) {
	needFFmpeg(t)
	mp3 := ffmpegFixture(t, "a.mp3", "-f", "lavfi", "-i", "sine=frequency=440:duration=5", "-c:a", "libmp3lame")
	work := filepath.Join(t.TempDir(), "acct", "media", "work")
	out, err := ConvertToOpusOgg(context.Background(), mp3, work, 2)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(out) != work {
		t.Fatalf("output %s not in work dir %s", out, work)
	}
	info, err := Detect(context.Background(), out, "")
	if err != nil || !info.IsOpusOgg() {
		t.Fatalf("converted file not opus: %+v %v", info, err)
	}
	if info.DurationMS > 2500 {
		t.Fatalf("max duration not enforced: %dms", info.DurationMS)
	}
}

func TestConvertToWAVForAnalysis(t *testing.T) {
	needFFmpeg(t)
	opus := ffmpegFixture(t, "a.ogg", "-f", "lavfi", "-i", "sine=duration=2", "-c:a", "libopus")
	work := filepath.Join(t.TempDir(), "work")
	out, err := ConvertToWAV(context.Background(), opus, work)
	if err != nil {
		t.Fatal(err)
	}
	info, err := Detect(context.Background(), out, "")
	if err != nil || info.MIME != "audio/wav" || filepath.Dir(out) != work {
		t.Fatalf("%+v %v %s", info, err, out)
	}
}
