package gemini

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/datahase/whatsapp-mcp/internal/media"
)

// Smoke runs one transcription (+translation) and/or one image OCR against
// the provider using local fixture files. It never touches WhatsApp data.
// Use only non-sensitive fixtures; the test tier is for exactly this.
func Smoke(ctx context.Context, base, key, transcribeModel, analysisModel, audioPath, imagePath, target string) (map[string]any, error) {
	c := NewClient(base, key, 2*time.Minute)
	s := &Service{client: c}
	s.cfg.MaxOutputTokens = 1024
	out := map[string]any{}
	if audioPath != "" {
		info, err := media.Detect(ctx, audioPath, audioPath)
		if err != nil || info.MediaType != media.TypeAudio {
			return nil, fmt.Errorf("audio fixture: %v", err)
		}
		data, err := os.ReadFile(audioPath)
		if err != nil {
			return nil, err
		}
		mime := strings.TrimSpace(strings.SplitN(info.MIME, ";", 2)[0])
		start := time.Now()
		var transcript string
		var usage Usage
		var uploaded string
		f, err := c.UploadFile(ctx, data, mime)
		if f != nil {
			uploaded = f.Name
		}
		if err == nil {
			var resp *Response
			resp, err = c.Interact(ctx, Request{Model: transcribeModel, Input: []Part{{Type: "audio", URI: f.URI, MimeType: mime}},
				GenerationConfig: map[string]any{"transcription_config": map[string]any{"mode": map[string]string{"type": "verbatim"}}}})
			if resp != nil {
				transcript, usage = resp.Text, resp.Usage
			}
		}
		if uploaded != "" {
			dctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			derr := c.DeleteFile(dctx, uploaded)
			cancel()
			out["provider_file_deleted"] = derr == nil
		}
		if err != nil {
			return out, fmt.Errorf("transcribe: %w", err)
		}
		out["transcript"], out["transcribe_model"], out["transcribe_usage"], out["transcribe_seconds"] = transcript, transcribeModel, usage, time.Since(start).Seconds()
		if target != "" && transcript != "" {
			start = time.Now()
			tr, u, err := s.translate(ctx, analysisModel, transcript, target, "")
			if err != nil {
				return out, fmt.Errorf("translate: %w", err)
			}
			out["translation"], out["detected_language"], out["translate_model"], out["translate_usage"], out["translate_seconds"] = tr.Translation, tr.SourceLanguage, analysisModel, u, time.Since(start).Seconds()
		}
	}
	if imagePath != "" {
		info, err := media.Detect(ctx, imagePath, imagePath)
		if err != nil || info.MediaType != media.TypeImage {
			return out, fmt.Errorf("image fixture: %v", err)
		}
		data, err := os.ReadFile(imagePath)
		if err != nil {
			return out, err
		}
		start := time.Now()
		resp, err := c.Interact(ctx, Request{Model: analysisModel, SystemInstruction: imageSystem,
			Input: []Part{{Type: "text", Text: "Extract all visible text verbatim in its original script, preserving line breaks. If there is no text, reply exactly: [no text]"},
				{Type: "image", Data: base64.StdEncoding.EncodeToString(data), MimeType: info.MIME}},
			GenerationConfig: map[string]any{"max_output_tokens": 1024, "temperature": 0.2}})
		if err != nil {
			return out, fmt.Errorf("image: %w", err)
		}
		out["ocr"], out["image_model"], out["image_usage"], out["image_seconds"] = resp.Text, analysisModel, resp.Usage, time.Since(start).Seconds()
	}
	return out, nil
}
