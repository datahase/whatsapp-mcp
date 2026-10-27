package ai

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/datahase/whatsapp-mcp/internal/config"
	"github.com/datahase/whatsapp-mcp/internal/media"
)

// Smoke runs one transcription (+translation) and/or one image OCR through
// the configured provider using local fixture files and the given key. It
// never touches WhatsApp data; use only non-sensitive fixtures.
func Smoke(ctx context.Context, cfg config.AI, key, transcribeKey, audioPath, imagePath, target string) (map[string]any, error) {
	out := map[string]any{"provider": cfg.Provider, "base_url": cfg.BaseURL, "transcribe_provider": cfg.TranscribeProvider}
	var deleted *bool
	onFail := func(string, string) {
		f := false
		deleted = &f
	}
	p, _, err := newProvider(cfg.Provider, cfg.BaseURL, key, cfg.TranscribeMethod, 2*time.Minute, onFail)
	if err != nil {
		return out, err
	}
	tp := p
	if cfg.TranscribeProvider != cfg.Provider && cfg.TranscribeProvider != "" {
		if tp, _, err = newProvider(cfg.TranscribeProvider, cfg.TranscribeBaseURL, transcribeKey, cfg.TranscribeMethod, 2*time.Minute, onFail); err != nil {
			return out, err
		}
	}
	s := &Service{cfg: cfg, p: p, tp: tp}
	s.cfg.MaxOutputTokens = 1024
	if audioPath != "" {
		info, err := media.Detect(ctx, audioPath, audioPath)
		if err != nil || info.MediaType != media.TypeAudio {
			return out, fmt.Errorf("audio fixture: %v", err)
		}
		path, mime := audioPath, strings.TrimSpace(strings.SplitN(info.MIME, ";", 2)[0])
		if cfg.AudioFormat == "wav" {
			wav, err := media.ConvertToWAV(ctx, audioPath, os.TempDir())
			if err != nil {
				return out, err
			}
			defer os.Remove(wav)
			path, mime = wav, "audio/wav"
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return out, err
		}
		start := time.Now()
		transcript, u, err := tp.Transcribe(ctx, cfg.TranscribeModel, data, mime, "", func() {})
		if _, ok := tp.(*geminiProvider); ok {
			out["provider_file_deleted"] = deleted == nil
		}
		if err != nil {
			return out, fmt.Errorf("transcribe: %w", err)
		}
		out["transcript"], out["transcribe_model"], out["transcribe_usage"], out["transcribe_seconds"] = transcript, cfg.TranscribeModel, u, time.Since(start).Seconds()
		if target != "" && transcript != "" {
			start = time.Now()
			tr, u, err := s.translate(ctx, cfg.AnalysisModel, transcript, target, "")
			if err != nil {
				return out, fmt.Errorf("translate: %w", err)
			}
			out["translation"], out["detected_language"], out["translate_model"], out["translate_usage"], out["translate_seconds"] = tr.Translation, tr.SourceLanguage, cfg.AnalysisModel, u, time.Since(start).Seconds()
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
		text, u, err := p.Generate(ctx, cfg.AnalysisModel, GenRequest{System: imageSystem, Image: data, ImageMIME: info.MIME, MaxTokens: 1024, Temperature: 0.2,
			Prompt: "Extract all visible text verbatim in its original script, preserving line breaks. If there is no text, reply exactly: [no text]"}, func() {})
		if err != nil {
			return out, fmt.Errorf("image: %w", err)
		}
		out["ocr"], out["image_model"], out["image_usage"], out["image_seconds"] = text, cfg.AnalysisModel, u, time.Since(start).Seconds()
	}
	return out, nil
}
