package accounts

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/datahase/whatsapp-mcp/internal/client"
	"github.com/datahase/whatsapp-mcp/internal/security"
	"go.mau.fi/whatsmeow/types/events"
)

// pairLifecycleFake models the socket's use of Connect's context, separately
// from the QR-channel context. The old implementation canceled both on success.
type pairLifecycleFake struct {
	*fakeWA
	paired           atomic.Bool
	connected        atomic.Bool
	disconnects      atomic.Int32
	logoutCalls      atomic.Int32
	contexts         sync.Mutex
	socketCtx, qrCtx context.Context
	qr               chan qrItem
	logoutErr        error
}

func (f *pairLifecycleFake) IsLoggedIn() bool { return f.paired.Load() }
func (f *pairLifecycleFake) OwnPhone() string {
	if f.paired.Load() {
		return "15550001234"
	}
	return ""
}
func (f *pairLifecycleFake) IsConnected() bool { return f.connected.Load() }
func (f *pairLifecycleFake) Connect(ctx context.Context, _ client.ConnectOpts) error {
	f.contexts.Lock()
	f.socketCtx = ctx
	f.contexts.Unlock()
	f.connected.Store(true)
	return nil
}
func (f *pairLifecycleFake) QRChannel(ctx context.Context) (<-chan qrItem, error) {
	f.contexts.Lock()
	f.qrCtx = ctx
	f.contexts.Unlock()
	return f.qr, nil
}
func (f *pairLifecycleFake) Disconnect() { f.disconnects.Add(1); f.connected.Store(false) }
func (f *pairLifecycleFake) Close()      { f.Disconnect(); f.fakeWA.Close() }
func (f *pairLifecycleFake) Logout(context.Context) error {
	f.logoutCalls.Add(1)
	if f.logoutErr != nil {
		return f.logoutErr
	}
	f.paired.Store(false)
	f.connected.Store(false)
	return nil
}

func pairingAccount(t *testing.T) *Account {
	t.Helper()
	m := NewManager(testConfig(t, t.TempDir(), "alpha", "beta"), &security.Redactor{})
	m.newClient = func(context.Context, client.Config) (wa, error) {
		return &pairLifecycleFake{fakeWA: &fakeWA{}, qr: make(chan qrItem, 8)}, nil
	}
	m.Start(context.Background())
	t.Cleanup(m.Stop)
	a, _ := m.Get("alpha")
	waitFor(t, func() bool { return a.State() == StateUnpaired })
	return a
}
func startFakePair(t *testing.T, a *Account) *pairLifecycleFake {
	t.Helper()
	if err := a.StartPairing(""); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cl.(*pairLifecycleFake)
}
func finishFakePair(t *testing.T, a *Account, f *pairLifecycleFake) {
	t.Helper()
	f.paired.Store(true)
	f.qr <- qrItem{Event: "success"}
	waitFor(t, func() bool { f.contexts.Lock(); defer f.contexts.Unlock(); return f.qrCtx.Err() != nil })
}

func TestPairSuccessKeepsSocketAndReconnectContextAlive(t *testing.T) {
	a := pairingAccount(t)
	f := startFakePair(t, a)
	f.qr <- qrItem{Event: "code", Code: "synthetic-qr"}
	waitFor(t, func() bool { return a.Pairing().QR != "" })
	finishFakePair(t, a, f)
	if a.State() != StateConnecting || a.Status().Error != "" || a.Pairing().QR != "" {
		t.Fatalf("after pair: %+v", a.Status())
	}
	f.contexts.Lock()
	socketErr := f.socketCtx.Err()
	f.contexts.Unlock()
	if socketErr != nil || f.disconnects.Load() != 0 {
		t.Fatalf("pair success killed live socket: %v disconnects=%d", socketErr, f.disconnects.Load())
	}
	a.onEvent(&events.Connected{})
	if a.State() != StateConnected {
		t.Fatal(a.State())
	}
	a.onEvent(&events.Disconnected{})
	if a.State() != StateDisconnected {
		t.Fatal(a.State())
	}
	a.onEvent(&events.Connected{})
	if a.State() != StateConnected {
		t.Fatal(a.State())
	}
}
func TestConnectedBeforePairSuccessIsNotDowngraded(t *testing.T) {
	a := pairingAccount(t)
	f := startFakePair(t, a)
	f.paired.Store(true)
	a.onEvent(&events.Connected{})
	finishFakePair(t, a, f)
	if a.State() != StateConnected || a.Status().Error != "" {
		t.Fatalf("late QR success downgraded readiness: %+v", a.Status())
	}
}
func TestOldPairingCancellationCannotOverwriteNewAttempt(t *testing.T) {
	a := pairingAccount(t)
	old := startFakePair(t, a)
	next := startFakePair(t, a)
	old.qr <- qrItem{Event: "timeout"}
	next.qr <- qrItem{Event: "code", Code: "second-attempt"}
	waitFor(t, func() bool { return a.Pairing().QR == "second-attempt" })
	if a.State() != StatePairing || next.disconnects.Load() != 0 {
		t.Fatalf("old watcher changed new attempt: %+v", a.Pairing())
	}
}
func TestPairingDeadlineDisconnectsOnlyUnpairedSocket(t *testing.T) {
	a := pairingAccount(t)
	f := startFakePair(t, a)
	a.mu.Lock()
	cancel := a.pairCancel
	a.mu.Unlock()
	cancel()
	waitFor(t, func() bool { return a.State() == StateUnpaired })
	if f.disconnects.Load() == 0 || a.Pairing().QR != "" {
		t.Fatal("timed out pairing socket was left open")
	}
}
func TestClosedQRChannelDoesNotLeavePairingStuck(t *testing.T) {
	a := pairingAccount(t)
	f := startFakePair(t, a)
	close(f.qr)
	waitFor(t, func() bool { return a.State() == StateUnpaired })
	if f.IsConnected() {
		t.Fatal("closed QR channel left socket connected")
	}
}
func TestDisconnectDuringInitialSyncBecomesReconnecting(t *testing.T) {
	a := pairingAccount(t)
	f := startFakePair(t, a)
	finishFakePair(t, a, f)
	a.onEvent(&events.Disconnected{})
	if a.State() != StateDisconnected {
		t.Fatalf("initial sync remains stuck: %+v", a.Status())
	}
}
func TestOfflineAndFailedLogoutKeepPairedSession(t *testing.T) {
	a := pairingAccount(t)
	f := startFakePair(t, a)
	finishFakePair(t, a, f)
	a.onEvent(&events.Connected{})
	f.connected.Store(false)
	if err := a.Logout(context.Background()); !errors.Is(err, ErrLogoutOffline) {
		t.Fatalf("offline logout: %v", err)
	}
	if f.logoutCalls.Load() != 0 || !f.IsLoggedIn() {
		t.Fatal("offline logout lost saved link or sent network request")
	}
	f.connected.Store(true)
	f.logoutErr = errors.New("temporary server failure")
	if err := a.Logout(context.Background()); err == nil || !f.IsLoggedIn() || a.State() == StateLoggedOut {
		t.Fatal("failed logout cleared saved link")
	}
	f.logoutErr = nil
	if err := a.Logout(context.Background()); err != nil || a.State() != StateLoggedOut {
		t.Fatalf("connected logout: %v state=%s", err, a.State())
	}
	other, _ := a.m.Get("beta")
	if _, err := other.Store(); err != nil {
		t.Fatal("other account lost its store")
	}
}
