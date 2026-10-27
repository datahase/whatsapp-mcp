package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// anthropicVersion is the Messages API version this client is written for.
// Under it, temperature/top_p/top_k are rejected, so none are sent.
const anthropicVersion = "2026-10-21"

// anthropicFallbackBeta enables server-side refusal fallbacks ("default"
// routing), so a request a model declines is re-served by another model in
// the same call instead of failing.
const anthropicFallbackBeta = "server-side-fallback-2026-07-01"

// MaxAnthropicImageBytes is Anthropic's per-image limit for base64 input.
const MaxAnthropicImageBytes = 5 << 20

// ErrAnthropicNoAudio explains why Anthropic cannot be the transcriber.
var ErrAnthropicNoAudio = errors.New("the Anthropic API does not accept audio; set AI_TRANSCRIBE_PROVIDER to a provider that can transcribe")

// anthropicProvider calls Anthropic's native Messages API. It handles text
// and images; it cannot transcribe.
type anthropicProvider struct {
	c *Client
}

// NewAnthropicClient returns a client using the x-api-key header.
func NewAnthropicClient(base, key string, timeout time.Duration) *Client {
	c := NewClient(base, key, timeout)
	c.auth = func(r *http.Request) {
		r.Header.Set("x-api-key", key)
		r.Header.Set("anthropic-version", anthropicVersion)
		r.Header.Set("anthropic-beta", anthropicFallbackBeta)
	}
	return c
}

func (p *anthropicProvider) Redact(s string) string { return p.c.redact(s) }

func (p *anthropicProvider) Transcribe(context.Context, string, []byte, string, string, func()) (string, Usage, error) {
	return "", Usage{}, ErrAnthropicNoAudio
}

func (p *anthropicProvider) Generate(ctx context.Context, model string, r GenRequest, dispatched func()) (string, Usage, error) {
	content := []any{}
	if len(r.Image) > 0 {
		if len(r.Image) > MaxAnthropicImageBytes {
			return "", Usage{}, &Error{Kind: KindBadRequest, Message: "image is larger than Anthropic's 5 MB limit"}
		}
		content = append(content, map[string]any{"type": "image", "source": map[string]any{
			"type": "base64", "media_type": r.ImageMIME, "data": base64.StdEncoding.EncodeToString(r.Image)}})
	}
	content = append(content, map[string]any{"type": "text", "text": r.Prompt})
	// Short, single-turn tasks: keep effort low. max_tokens is generous so
	// thinking cannot truncate the answer.
	maxTokens := r.MaxTokens
	if maxTokens < 16000 {
		maxTokens = 16000
	}
	req := map[string]any{
		"model":         model,
		"max_tokens":    maxTokens,
		"messages":      []any{map[string]any{"role": "user", "content": content}},
		"output_config": map[string]any{"effort": "low"},
		"fallbacks":     "default",
	}
	if r.System != "" {
		req["system"] = r.System
	}
	if r.JSONSchema != nil {
		req["output_config"] = map[string]any{"effort": "low", "format": map[string]any{"type": "json_schema", "schema": withNoExtras(r.JSONSchema)}}
	}
	body, _ := json.Marshal(req)
	dispatched()
	_, b, err := p.c.do(ctx, http.MethodPost, p.c.base+"/v1/messages", body, map[string]string{"Content-Type": "application/json"})
	if err != nil {
		return "", Usage{}, err
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			In  int `json:"input_tokens"`
			Out int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", Usage{}, &Error{Kind: KindInvalidOutput, Message: "unparseable Messages API response"}
	}
	u := Usage{InputTokens: out.Usage.In, OutputTokens: out.Usage.Out, TotalTokens: out.Usage.In + out.Usage.Out}
	switch out.StopReason {
	case "refusal":
		return "", u, &Error{Kind: KindBadRequest, Message: "the model declined this request"}
	case "max_tokens":
		return "", u, &Error{Kind: KindInvalidOutput, Message: "response was cut off at max_tokens"}
	}
	var text strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}
	return strings.TrimSpace(text.String()), u, nil
}

// withNoExtras adds additionalProperties:false, which structured outputs
// require on object schemas.
func withNoExtras(schema map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range schema {
		out[k] = v
	}
	if out["type"] == "object" {
		out["additionalProperties"] = false
	}
	return out
}
