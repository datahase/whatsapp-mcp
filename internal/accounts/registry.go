package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/datahase/whatsapp-mcp/internal/config"
	"github.com/datahase/whatsapp-mcp/internal/store"
)

func (m *Manager) registryPath() string {
	return filepath.Join(m.cfg.DataDir, "service", "accounts.json")
}

// LoadRegistry restores accounts added by the owner. Invalid registries fail
// startup instead of silently hiding a paired device. Call before Start.
func (m *Manager) LoadRegistry() error {
	raw, err := os.ReadFile(m.registryPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var defs []config.Account
	if err := json.Unmarshal(raw, &defs); err != nil {
		return fmt.Errorf("account registry: %w", err)
	}
	seen := map[string]bool{}
	for _, ac := range defs {
		if !regexp.MustCompile(`^[a-z0-9_-]{1,32}$`).MatchString(ac.ID) || seen[ac.ID] || len(ac.Label) > 80 {
			return errors.New("invalid account registry")
		}
		seen[ac.ID] = true
		if _, exists := m.accounts[ac.ID]; exists {
			return fmt.Errorf("account %s appears in both config and saved registry", ac.ID)
		}
	}
	m.managed = defs
	for _, ac := range defs {
		m.addDefinition(ac)
	}
	return nil
}

// Add creates a private account definition, commits it atomically, then opens
// its stores. No WhatsApp pairing or message sending happens implicitly.
func (m *Manager) Add(label string) (*Account, error) {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	if m.ctx == nil || m.stopped || m.ctx.Err() != nil {
		return nil, errors.New("account manager is stopping")
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = "WhatsApp"
	}
	if len(label) > 80 || strings.ContainsAny(label, "\r\n\x00") {
		return nil, errors.New("name must be at most 80 characters")
	}
	if len(m.All()) >= 32 {
		return nil, errors.New("account limit reached (32)")
	}
	ac := config.Account{ID: store.NewID("wa_")[:27], Label: label}
	defs := append(append([]config.Account(nil), m.managed...), ac)
	raw, err := json.Marshal(defs)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(m.registryPath())
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(dir, "accounts-*.tmp")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	cerr := f.Close()
	if err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), m.registryPath())
	}
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	a := m.addDefinition(ac)
	a.ctx, a.cancel = context.WithCancel(m.ctx)
	if err := a.open(); err != nil {
		a.setState(StateError, err.Error())
	}
	m.managed = defs
	m.mu.Unlock()
	if a.State() != StateError {
		m.wg.Add(1)
		go func() { defer m.wg.Done(); a.boot() }()
	}
	return a, nil
}
