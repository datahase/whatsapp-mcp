package config

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
)

// History is opt-in. The normal message tools do not use its selection policy.
type History struct {
	Enabled         bool   `yaml:"enabled" json:"enabled"`
	AllowGeneration bool   `yaml:"allow_generation" json:"allow_generation"`
	Model           string `yaml:"model" json:"model"`
	Revision        string `yaml:"revision" json:"revision"`
	Dimensions      int    `yaml:"dimensions" json:"dimensions"`
	Runtime         string `yaml:"runtime" json:"runtime"`
	Python          string `yaml:"python" json:"-"`
	Device          string `yaml:"device" json:"device"`
	CacheDir        string `yaml:"cache_dir" json:"-"`
	Offline         bool   `yaml:"offline" json:"offline"`
	MaxDocuments    int    `yaml:"max_documents" json:"max_documents"`
	RetentionDays   int    `yaml:"retention_days" json:"retention_days"`
}

func (h *History) loadEnv() error {
	for key, dst := range map[string]*string{"HISTORY_MODEL": &h.Model, "HISTORY_MODEL_REVISION": &h.Revision, "HISTORY_RUNTIME": &h.Runtime, "HISTORY_PYTHON": &h.Python, "HISTORY_DEVICE": &h.Device, "HISTORY_MODEL_CACHE": &h.CacheDir} {
		setStr(dst, key)
	}
	for key, dst := range map[string]*bool{"HISTORY_ENABLED": &h.Enabled, "HISTORY_ALLOW_GENERATION": &h.AllowGeneration, "HISTORY_OFFLINE": &h.Offline} {
		if v, ok := env(key); ok {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return fmt.Errorf("%s: use true or false", key)
			}
			*dst = b
		}
	}
	for key, dst := range map[string]*int{"HISTORY_DIMENSIONS": &h.Dimensions, "HISTORY_MAX_DOCUMENTS": &h.MaxDocuments, "HISTORY_RETENTION_DAYS": &h.RetentionDays} {
		if v, ok := env(key); ok {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return fmt.Errorf("%s: positive integer required", key)
			}
			*dst = n
		}
	}
	return nil
}

func (h *History) defaults() {
	def := func(v *string, value string) {
		if *v == "" {
			*v = value
		}
	}
	def(&h.Model, "google/embeddinggemma-2")
	def(&h.Revision, "f9972ac22e696a105c482202646979e33e4b5a1e")
	def(&h.Runtime, "sentence_transformers")
	def(&h.Python, "python3")
	def(&h.Device, "cpu")
	if h.Dimensions == 0 {
		h.Dimensions = 256
	}
	if h.MaxDocuments == 0 {
		h.MaxDocuments = 100000
	}
	if h.RetentionDays == 0 {
		h.RetentionDays = 7
	}
}

func (h History) validate() error {
	if !h.Enabled {
		return nil
	}
	if h.Runtime != "sentence_transformers" {
		return errors.New("HISTORY_RUNTIME: supported runtime is sentence_transformers")
	}
	if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(h.Revision) {
		return errors.New("HISTORY_MODEL_REVISION must be an immutable 40-character commit SHA")
	}
	if h.Dimensions != 128 && h.Dimensions != 256 && h.Dimensions != 512 && h.Dimensions != 768 {
		return errors.New("HISTORY_DIMENSIONS must be 128, 256, 512 or 768")
	}
	if h.RetentionDays > 365 || h.MaxDocuments > 1000000 {
		return errors.New("history retention or document limit exceeds supported bounds")
	}
	return nil
}
