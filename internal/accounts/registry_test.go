package accounts

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/datahase/whatsapp-mcp/internal/client"
	"github.com/datahase/whatsapp-mcp/internal/security"
)

func TestOwnerAddedAccountsSurviveRestartAndConcurrentReads(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(t, dir)
	m := startManager(t, cfg, nil)
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for range 8 {
		wg.Go(func() {
			a, err := m.Add("")
			if err != nil {
				t.Error(err)
				return
			}
			ids <- a.ID
			for range 10 {
				_ = m.Statuses()
				_, _ = m.Get(a.ID)
				_ = m.SortedIDs()
			}
		})
	}
	wg.Wait()
	close(ids)
	if len(m.All()) != 8 {
		t.Fatalf("accounts: %d", len(m.All()))
	}
	info, err := os.Stat(m.registryPath())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("registry permissions: %v %v", info, err)
	}
	m.Stop()
	restored := NewManager(testConfig(t, dir), &security.Redactor{})
	if err := restored.LoadRegistry(); err != nil {
		t.Fatal(err)
	}
	restored.newClient = func(context.Context, client.Config) (wa, error) { return &fakeWA{}, nil }
	restored.Start(context.Background())
	defer restored.Stop()
	for id := range ids {
		a, err := restored.Get(id)
		if err != nil || a.Label != "WhatsApp" {
			t.Fatalf("restored %q: %v", id, err)
		}
		if _, err := a.Store(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Add("after stop"); err == nil {
		t.Fatal("added after stop")
	}
}

func TestInvalidRegistryFailsAndWriteFailureDoesNotAddAccount(t *testing.T) {
	cfg := testConfig(t, t.TempDir())
	m := startManager(t, cfg, nil)
	// A file in place of the service directory makes the save fail.
	if err := os.WriteFile(filepath.Join(cfg.DataDir, "service"), []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Add("new"); err == nil || len(m.All()) != 0 {
		t.Fatal("failed persistence added account")
	}
	os.Remove(filepath.Join(cfg.DataDir, "service"))
	os.MkdirAll(filepath.Dir(m.registryPath()), 0700)
	for _, raw := range []string{`broken`, `[{"id":"../escape"}]`, `[{"id":"same"},{"id":"same"}]`} {
		os.WriteFile(m.registryPath(), []byte(raw), 0600)
		if err := m.LoadRegistry(); err == nil {
			t.Fatalf("accepted invalid registry %s", raw)
		}
	}
}
