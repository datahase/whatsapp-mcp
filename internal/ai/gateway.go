package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
)

// gatewayProvider uses Vercel's versioned transcription protocol for recorded
// audio and its OpenAI-compatible chat API for translation and image analysis.
// Transcription models cannot use /audio/transcriptions or chat input_audio.
type gatewayProvider struct{ *openAIProvider }

func (g *gatewayProvider) Transcribe(ctx context.Context, model string, audio []byte, mime, _ string, dispatched func()) (string, Usage, error) {
	body, err := json.Marshal(map[string]string{
		"audio":     base64.StdEncoding.EncodeToString(audio),
		"mediaType": mime,
	})
	if err != nil {
		return "", Usage{}, err
	}
	// BaseURL is the chat-compatible /v1 origin. Keep custom proxy prefixes
	// when deriving the dedicated transcription endpoint.
	endpoint := strings.TrimSuffix(g.c.base, "/v1") + "/v4/ai/transcription-model"
	dispatched()
	_, raw, err := g.c.do(ctx, http.MethodPost, endpoint, body, map[string]string{
		"Content-Type":                                 "application/json",
		"ai-gateway-protocol-version":                  "0.0.1",
		"ai-transcription-model-specification-version": "4",
		"ai-model-id":                                  model,
	})
	if err != nil {
		return "", Usage{}, err
	}
	var response struct {
		Text *string `json:"text"`
	}
	if err := json.Unmarshal(raw, &response); err != nil || response.Text == nil {
		return "", Usage{}, &Error{Kind: KindInvalidOutput, Message: "transcription response had no text field"}
	}
	// The recorded-audio API auto-detects language and does not return token
	// counts. Do not invent token usage or forward undocumented language hints.
	return strings.TrimSpace(*response.Text), Usage{}, nil
}
