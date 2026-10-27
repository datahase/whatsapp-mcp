package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// File is a Files API resource.
type File struct {
	Name     string `json:"name"`
	URI      string `json:"uri"`
	MimeType string `json:"mimeType"`
	State    string `json:"state"`
}

// UploadFile uploads data with the resumable protocol and waits until the
// file is ACTIVE. The caller must DeleteFile afterwards.
func (c *Client) UploadFile(ctx context.Context, data []byte, mimeType string) (*File, error) {
	meta, _ := json.Marshal(map[string]any{"file": map[string]string{"display_name": "whatsapp-mcp-media"}})
	resp, _, err := c.do(ctx, http.MethodPost, c.base+"/upload/v1beta/files", meta, map[string]string{
		"X-Goog-Upload-Protocol":              "resumable",
		"X-Goog-Upload-Command":               "start",
		"X-Goog-Upload-Header-Content-Length": strconv.Itoa(len(data)),
		"X-Goog-Upload-Header-Content-Type":   mimeType,
		"Content-Type":                        "application/json",
	})
	if err != nil {
		return nil, err
	}
	uploadURL := resp.Header.Get("X-Goog-Upload-URL")
	if uploadURL == "" {
		return nil, &Error{Kind: KindInvalidOutput, Message: "Files API did not return an upload URL"}
	}
	_, b, err := c.do(ctx, http.MethodPost, uploadURL, data, map[string]string{
		"X-Goog-Upload-Offset":  "0",
		"X-Goog-Upload-Command": "upload, finalize",
		"Content-Type":          mimeType,
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		File File `json:"file"`
	}
	if err := json.Unmarshal(b, &out); err != nil || out.File.Name == "" || out.File.URI == "" {
		return nil, &Error{Kind: KindInvalidOutput, Message: "Files API returned no usable file"}
	}
	f := &out.File
	for i := 0; f.State == "PROCESSING" && i < 30; i++ {
		if err := c.sleep(ctx, 2*time.Second); err != nil {
			return f, &Error{Kind: KindNetwork, Message: "cancelled while the provider processed the file"}
		}
		_, b, err := c.do(ctx, http.MethodGet, c.base+"/v1beta/"+f.Name, nil, nil)
		if err != nil {
			return f, err
		}
		var g File
		if json.Unmarshal(b, &g) == nil && g.Name != "" {
			f = &g
		}
	}
	switch f.State {
	case "", "ACTIVE":
		return f, nil
	case "FAILED":
		return f, &Error{Kind: KindBadRequest, Message: "provider could not process the file"}
	default:
		return f, &Error{Kind: KindTransient, Message: "provider did not finish processing the file in time"}
	}
}

// DeleteFile removes an uploaded file. Deletion is not a guarantee of zero
// provider-side retention.
func (c *Client) DeleteFile(ctx context.Context, name string) error {
	_, _, err := c.do(ctx, http.MethodDelete, c.base+"/v1beta/"+name, nil, nil)
	var ge *Error
	if errors.As(err, &ge) && ge.Status == 404 {
		return nil // already gone
	}
	return err
}

// Part is one Interactions input item.
type Part struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	URI      string `json:"uri,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

// Request is an Interactions API request. Tools, grounding and code
// execution are never set.
type Request struct {
	Model             string         `json:"model"`
	Input             []Part         `json:"input"`
	SystemInstruction string         `json:"system_instruction,omitempty"`
	GenerationConfig  map[string]any `json:"generation_config,omitempty"`
	ResponseFormat    map[string]any `json:"response_format,omitempty"`
	Store             bool           `json:"store"`
}

// Usage is provider-reported token usage.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// Response is the parsed part of an interaction we use.
type Response struct {
	ID     string
	Status string
	Text   string
	Usage  Usage
}

// Interact runs one non-streaming interaction.
func (c *Client) Interact(ctx context.Context, r Request) (*Response, error) {
	body, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	_, b, err := c.do(ctx, http.MethodPost, c.base+"/v1beta/interactions", body, map[string]string{"Content-Type": "application/json"})
	if err != nil {
		return nil, err
	}
	var raw struct {
		ID         string `json:"id"`
		Status     string `json:"status"`
		OutputText string `json:"output_text"`
		Steps      []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"steps"`
		Outputs []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"outputs"`
		Usage struct {
			In    int `json:"total_input_tokens"`
			Out   int `json:"total_output_tokens"`
			Total int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, &Error{Kind: KindInvalidOutput, Message: "unparseable interaction response"}
	}
	text := raw.OutputText
	if text == "" {
		var parts []string
		for _, s := range raw.Steps {
			if s.Type != "" && s.Type != "model_output" {
				continue
			}
			for _, ct := range s.Content {
				if ct.Type == "text" || ct.Type == "" {
					parts = append(parts, ct.Text)
				}
			}
		}
		for _, o := range raw.Outputs {
			if o.Type == "text" {
				parts = append(parts, o.Text)
			}
		}
		text = strings.Join(parts, "\n")
	}
	if raw.Status != "" && raw.Status != "completed" {
		return nil, &Error{Kind: KindInvalidOutput, Message: fmt.Sprintf("interaction ended with status %q", raw.Status)}
	}
	return &Response{ID: raw.ID, Status: raw.Status, Text: strings.TrimSpace(text),
		Usage: Usage{InputTokens: raw.Usage.In, OutputTokens: raw.Usage.Out, TotalTokens: raw.Usage.Total}}, nil
}
