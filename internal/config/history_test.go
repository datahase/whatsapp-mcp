package config

import (
	"strings"
	"testing"
)

func TestHistoryDefaultsAndSpaceValidation(t *testing.T) {
	h := History{Enabled: true}
	h.defaults()
	if h.Model != "google/embeddinggemma-2" || h.Dimensions != 256 || h.Device != "cpu" || h.AllowGeneration {
		t.Fatalf("unexpected history defaults: %+v", h)
	}
	if err := h.validate(); err != nil {
		t.Fatal(err)
	}
	for _, modify := range []func(*History){func(h *History) { h.Revision = "main" }, func(h *History) { h.Dimensions = 1536 }, func(h *History) { h.Runtime = "hosted" }, func(h *History) { h.MaxDocuments = 1000001 }, func(h *History) { h.RetentionDays = 366 }} {
		invalid := h
		modify(&invalid)
		if invalid.validate() == nil {
			t.Fatalf("invalid embedding configuration accepted: %+v", invalid)
		}
	}
	t.Setenv("HISTORY_MODEL_REVISION", strings.Repeat("b", 40))
	t.Setenv("HISTORY_DIMENSIONS", "128")
	t.Setenv("HISTORY_OFFLINE", "true")
	if err := h.loadEnv(); err != nil {
		t.Fatal(err)
	}
	if h.Revision != strings.Repeat("b", 40) || h.Dimensions != 128 || !h.Offline {
		t.Fatal("history environment overrides lost")
	}
	t.Setenv("HISTORY_DIMENSIONS", "invalid")
	if h.loadEnv() == nil {
		t.Fatal("invalid dimension was ignored")
	}
}
