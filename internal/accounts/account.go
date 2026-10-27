// Package accounts owns every configured WhatsApp account: its directory,
// lock, stores, client, pairing state and lifecycle. Every account-specific
// operation goes through Manager.Get, which never picks a default.
package accounts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/datahase/whatsapp-mcp/internal/client"
	"github.com/datahase/whatsapp-mcp/internal/config"
	"github.com/datahase/whatsapp-mcp/internal/ratelimit"
	"github.com/datahase/whatsapp-mcp/internal/security"
	"github.com/datahase/whatsapp-mcp/internal/store"
)

// State is an account lifecycle state.
type State string

const (
	StateUnpaired     State = "unpaired"
	StatePairing      State = "pairing"
	StateConnecting   State = "connecting"
	StateConnected    State = "connected"
	StateDisconnected State = "disconnected"
	StateLoggedOut    State = "logged_out"
	StateDisabled     State = "disabled"
	StateError        State = "error"
)

// PairingTimeout bounds one pairing attempt. A timed-out attempt returns to
// unpaired; nothing restarts pairing automatically.
const PairingTimeout = 3 * time.Minute

// ErrUnknownAccount is returned for an account_id that is not configured.
var ErrUnknownAccount = errors.New("unknown account_id")

// Manager holds all accounts.
type Manager struct {
	cfg      *config.Config
	accounts map[string]*Account
	order    []string
	logLevel string
	redactor *security.Redactor
	log      waLog.Logger
	// newClient is a seam for tests.
	newClient func(ctx context.Context, cfg client.Config) (wa, error)
	wg        sync.WaitGroup
	stopJan   chan struct{}
}

// wa is the client surface the account lifecycle uses. *client.Client
// satisfies it; tests substitute a fake.
type wa interface {
	IsLoggedIn() bool
	OwnPhone() string
	IsConnected() bool
	Connect(ctx context.Context, opts client.ConnectOpts) error
	QRChannel(ctx context.Context) (<-chan qrItem, error)
	PairPhone(ctx context.Context, phone string) (string, error)
	Logout(ctx context.Context) error
	Disconnect()
	Close()
	Real() live
}

// live is what connected operations need. *client.Client satisfies it.
type live interface {
	CheckSendRate(to types.JID) (bool, time.Duration, string)
	NewMessageID() string
	SendOutgoing(ctx context.Context, o client.Outgoing) (client.SendOutcome, error)
	DownloadMessageMedia(ctx context.Context, chatJID, messageID, destPath string, maxBytes int64) (string, string, error)
	RequestHistorySync(ctx context.Context, chatJID string, from time.Time, anchor string, count int, useLID bool) (string, error)
}

// Account is one configured WhatsApp slot.
type Account struct {
	ID            string
	Label         string
	ExpectedPhone string
	Dir           string

	m       *Manager
	log     waLog.Logger
	limiter *ratelimit.Limiter
	sendMu  sync.Mutex // serializes sends, so an idempotency key is claimed once

	mu          sync.Mutex
	state       State
	stateErr    string
	stateSince  time.Time
	store       *store.Store
	lock        *store.FileLock
	cl          wa
	qr          string
	pairCode    string
	pairCancel  context.CancelFunc
	lastEvent   time.Time
	lastHistory time.Time
	ctx         context.Context
	cancel      context.CancelFunc
}

// Status is the public, credential-free view of an account.
type Status struct {
	AccountID         string     `json:"account_id"`
	Label             string     `json:"label"`
	State             State      `json:"state"`
	Error             string     `json:"error,omitempty"`
	Since             time.Time  `json:"state_since"`
	Phone             string     `json:"phone,omitempty"`
	ExpectedPhone     string     `json:"expected_phone,omitempty"`
	SendsEnabled      bool       `json:"sends_enabled"`
	PairingRequired   bool       `json:"pairing_required"`
	LastEventAt       *time.Time `json:"last_event_at,omitempty"`
	LastHistorySyncAt *time.Time `json:"last_history_sync_at,omitempty"`
	MessageCacheFull  bool       `json:"message_cache_full,omitempty"`
	CacheFullSince    string     `json:"message_cache_full_since,omitempty"`
}

// NewManager prepares accounts from config. Store problems affect only the
// account concerned: it is marked error and never falls back to another
// account's data.
func NewManager(cfg *config.Config, redactor *security.Redactor) *Manager {
	m := &Manager{
		cfg: cfg, accounts: map[string]*Account{}, logLevel: cfg.LogLevel, redactor: redactor,
		log:     client.NewStderrLogger("accounts", cfg.LogLevel, false),
		stopJan: make(chan struct{}),
	}
	m.newClient = func(ctx context.Context, c client.Config) (wa, error) {
		cl, err := client.New(ctx, c)
		if err != nil {
			return nil, err
		}
		return realWA{cl}, nil
	}
	for _, ac := range cfg.Accounts {
		a := &Account{
			ID: ac.ID, Label: ac.Label, ExpectedPhone: ac.ExpectedPhone,
			Dir: filepath.Join(cfg.DataDir, "accounts", ac.ID),
			m:   m, log: client.NewStderrLogger("account:"+ac.ID, cfg.LogLevel, false),
			limiter: ratelimit.New(ratelimit.Config{
				MinSendInterval: cfg.Limits.SendMinInterval, MaxSendsPerHour: cfg.Limits.SendsPerHour,
				NonContactMinInterval: cfg.Limits.NonContactMinInterval, NonContactMaxPerHour: cfg.Limits.NonContactPerHour,
				MaxUsyncPerMinute: 3,
			}),
			state: StateDisabled, stateSince: time.Now().UTC(),
		}
		if ac.Disabled {
			a.stateErr = "disabled in configuration"
		}
		m.accounts[ac.ID] = a
		m.order = append(m.order, ac.ID)
	}
	return m
}

// Get resolves an account_id. It never chooses a default.
func (m *Manager) Get(id string) (*Account, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("account_id: required (configured: %s)", strings.Join(m.order, ", "))
	}
	a, ok := m.accounts[id]
	if !ok {
		return nil, fmt.Errorf("%w %q (configured: %s)", ErrUnknownAccount, id, strings.Join(m.order, ", "))
	}
	return a, nil
}

// All returns accounts in configuration order.
func (m *Manager) All() []*Account {
	out := make([]*Account, 0, len(m.order))
	for _, id := range m.order {
		out = append(out, m.accounts[id])
	}
	return out
}

// Start opens and connects every enabled account. Failures are per account.
func (m *Manager) Start(ctx context.Context) {
	var opened []*Account
	for _, ac := range m.cfg.Accounts {
		if ac.Disabled {
			continue
		}
		a := m.accounts[ac.ID]
		a.ctx, a.cancel = context.WithCancel(ctx)
		if err := a.open(); err != nil {
			a.setState(StateError, err.Error())
			continue
		}
		opened = append(opened, a)
	}
	// Clean abandoned work before anything can use the media areas.
	m.RunJanitor(true)
	for _, a := range opened {
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			a.boot()
		}()
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.janitorLoop(ctx)
	}()
}

// Stop disconnects every account and closes its databases and lock.
func (m *Manager) Stop() {
	close(m.stopJan)
	for _, a := range m.All() {
		if a.cancel != nil {
			a.cancel()
		}
	}
	m.wg.Wait()
	for _, a := range m.All() {
		a.close()
	}
}

func (a *Account) open() error {
	if err := os.MkdirAll(a.Dir, 0o700); err != nil {
		return fmt.Errorf("create account dir: %w", err)
	}
	lock, err := store.TryLock(a.Dir)
	if err != nil {
		return fmt.Errorf("account store is in use by another process: %w", err)
	}
	st, err := store.Open(a.Dir)
	if err != nil {
		lock.Release()
		return fmt.Errorf("open account store: %w", err)
	}
	if u, f, err := st.RecoverSendOps(); err != nil {
		a.log.Warnf("send recovery: %v", err)
	} else if u+f > 0 {
		a.log.Warnf("send recovery: %d operations marked unknown, %d failed", u, f)
	}
	if err := st.RecoverJobs(); err != nil {
		a.log.Warnf("job recovery: %v", err)
	}
	for _, sub := range []string{"staged", "received", "work"} {
		if err := os.MkdirAll(filepath.Join(a.Dir, "media", sub), 0o700); err != nil {
			st.Close()
			lock.Release()
			return err
		}
	}
	a.mu.Lock()
	a.store, a.lock = st, lock
	a.mu.Unlock()
	return nil
}

func (a *Account) close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pairCancel != nil {
		a.pairCancel()
	}
	if a.cl != nil {
		a.cl.Close()
		a.cl = nil
	}
	if a.store != nil {
		a.store.Close()
		a.store = nil
	}
	if a.lock != nil {
		a.lock.Release()
		a.lock = nil
	}
}

// ensureClient returns the account's client, creating one if needed.
func (a *Account) ensureClient() (wa, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cl != nil {
		return a.cl, nil
	}
	if a.store == nil {
		return nil, errors.New("account store is not open")
	}
	cl, err := a.m.newClient(a.ctx, client.Config{
		StoreDir: a.Dir, Store: a.store, Logger: a.log, Redactor: a.m.redactor,
		RateLimiter: a.limiter, OnEvent: a.onEvent,
	})
	if err != nil {
		return nil, err
	}
	a.cl = cl
	return cl, nil
}

// boot connects a paired account, retrying the initial connection with
// bounded backoff. After the first success, whatsmeow's own reconnect logic
// takes over; this loop does not compete with it.
func (a *Account) boot() {
	cl, err := a.ensureClient()
	if err != nil {
		a.setState(StateError, err.Error())
		return
	}
	if !cl.IsLoggedIn() {
		a.setState(StateUnpaired, "")
		return
	}
	if err := a.checkIdentity(cl.OwnPhone(), false); err != nil {
		a.setState(StateError, err.Error())
		return
	}
	backoff := 5 * time.Second
	for {
		a.setState(StateConnecting, "")
		err := cl.Connect(a.ctx, client.ConnectOpts{})
		if err == nil {
			return
		}
		if a.ctx.Err() != nil {
			return
		}
		a.setState(StateDisconnected, "connect failed, retrying in "+backoff.String()+": "+err.Error())
		select {
		case <-a.ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 5*time.Minute)
	}
}

// checkIdentity enforces the paired-number rules: expected_phone must match,
// a slot keeps the first number it was paired to (until reset), and one
// number cannot back two accounts.
func (a *Account) checkIdentity(phone string, fresh bool) error {
	if phone == "" {
		return errors.New("paired device has no phone identity")
	}
	if a.ExpectedPhone != "" && phone != a.ExpectedPhone {
		return fmt.Errorf("paired number ends in %s but expected_phone ends in %s; sends disabled", last4(phone), last4(a.ExpectedPhone))
	}
	a.mu.Lock()
	st := a.store
	a.mu.Unlock()
	if prev := st.MetaGet("paired_phone"); prev != "" && prev != phone {
		return fmt.Errorf("this slot was previously paired to a number ending in %s; reset the slot before pairing a different number", last4(prev))
	}
	for _, other := range a.m.All() {
		if other != a && other.phone() == phone {
			return fmt.Errorf("number is already paired to account %q", other.ID)
		}
	}
	if fresh || st.MetaGet("paired_phone") == "" {
		return st.MetaSet("paired_phone", phone)
	}
	return nil
}

func (a *Account) phone() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cl == nil || a.state == StateError {
		return ""
	}
	return a.cl.OwnPhone()
}

// onEvent tracks lifecycle events from the account's single handler.
func (a *Account) onEvent(evt any) {
	a.mu.Lock()
	a.lastEvent = time.Now().UTC()
	a.mu.Unlock()
	switch v := evt.(type) {
	case *events.Connected:
		a.mu.Lock()
		cl := a.cl
		a.mu.Unlock()
		if cl != nil {
			if err := a.checkIdentity(cl.OwnPhone(), false); err != nil {
				a.setState(StateError, err.Error())
				go cl.Disconnect()
				return
			}
		}
		a.setState(StateConnected, "")
	case *events.Disconnected:
		if a.State() == StateConnected {
			a.setState(StateDisconnected, "connection lost; reconnecting automatically")
		}
	case *events.LoggedOut:
		a.setState(StateLoggedOut, "linked device was removed from the phone; pair again from the admin page")
		a.dropClient()
	case *events.StreamReplaced:
		a.setState(StateError, "another client took over this session (is a second instance running on the same data?)")
		a.dropClient()
	case *events.TemporaryBan:
		a.setState(StateError, "WhatsApp temporary ban: "+v.String())
	case *events.ClientOutdated:
		a.setState(StateError, "WhatsApp reports this client version as outdated; upgrade the image")
	case *events.ConnectFailure:
		a.setState(StateError, fmt.Sprintf("connect failure: %v %s", v.Reason, v.Message))
	case *events.HistorySync:
		a.mu.Lock()
		a.lastHistory = time.Now().UTC()
		a.mu.Unlock()
	}
}

// dropClient closes the client outside the event goroutine.
func (a *Account) dropClient() {
	a.mu.Lock()
	cl := a.cl
	a.cl = nil
	a.mu.Unlock()
	if cl != nil {
		go cl.Close()
	}
}

func (a *Account) setState(s State, msg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state != s || a.stateErr != msg {
		a.state, a.stateErr, a.stateSince = s, msg, time.Now().UTC()
		if msg != "" {
			a.log.Infof("state %s: %s", s, msg)
		} else {
			a.log.Infof("state %s", s)
		}
	}
}

// State returns the current lifecycle state.
func (a *Account) State() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

// Store returns the account's store, or an error if it is not open.
func (a *Account) Store() (*store.Store, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.store == nil {
		msg := a.stateErr
		if msg == "" {
			msg = string(a.state)
		}
		return nil, fmt.Errorf("account %q data is unavailable: %s", a.ID, msg)
	}
	return a.store, nil
}

// liveClient returns the client when the account can talk to WhatsApp.
func (a *Account) liveClient() (live, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state != StateConnected || a.cl == nil {
		msg := a.stateErr
		if msg == "" {
			msg = string(a.state)
		}
		return nil, fmt.Errorf("account %q is not connected (%s); cached data is still readable", a.ID, msg)
	}
	return a.cl.Real(), nil
}

// Status returns a credential-free status snapshot.
func (a *Account) Status() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := Status{
		AccountID: a.ID, Label: a.Label, State: a.state, Error: a.stateErr, Since: a.stateSince,
		ExpectedPhone: a.ExpectedPhone, SendsEnabled: a.state == StateConnected,
		PairingRequired: a.state == StateUnpaired || a.state == StateLoggedOut || a.state == StatePairing,
	}
	if a.cl != nil && a.state != StateError {
		s.Phone = a.cl.OwnPhone()
	}
	if !a.lastEvent.IsZero() {
		t := a.lastEvent
		s.LastEventAt = &t
	}
	if !a.lastHistory.IsZero() {
		t := a.lastHistory
		s.LastHistorySyncAt = &t
	}
	if a.store != nil && a.store.CacheFull() {
		s.MessageCacheFull = true
		s.CacheFullSince = a.store.MetaGet("cache_full_since")
	}
	return s
}

// Statuses returns every account's status in configuration order.
func (m *Manager) Statuses() []Status {
	out := []Status{}
	for _, a := range m.All() {
		out = append(out, a.Status())
	}
	return out
}

// PairingInfo is the admin page's view of an in-progress pairing.
type PairingInfo struct {
	State    State
	QR       string
	PairCode string
	Error    string
}

// Pairing returns current pairing data for the admin page.
func (a *Account) Pairing() PairingInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return PairingInfo{State: a.state, QR: a.qr, PairCode: a.pairCode, Error: a.stateErr}
}

// StartPairing begins one bounded pairing attempt (QR, or a phone pairing
// code when phone is set). It is an explicit admin action.
func (a *Account) StartPairing(phone string) error {
	switch a.State() {
	case StateConnected, StateConnecting, StateDisconnected:
		return errors.New("account is already paired; log out first")
	case StateDisabled:
		return errors.New("account is disabled")
	}
	a.mu.Lock()
	if a.pairCancel != nil {
		a.pairCancel()
	}
	if a.cl != nil && a.cl.IsLoggedIn() {
		a.mu.Unlock()
		return errors.New("account is already paired; log out first")
	}
	old := a.cl
	a.cl = nil
	a.qr, a.pairCode = "", ""
	pctx, cancel := context.WithTimeout(a.ctx, PairingTimeout)
	a.pairCancel = cancel
	a.mu.Unlock()
	if old != nil {
		old.Close() // a stale unpaired client; start from a fresh device
	}

	cl, err := a.ensureClient()
	if err != nil {
		cancel()
		return err
	}
	ch, err := cl.QRChannel(pctx)
	if err != nil {
		cancel()
		return fmt.Errorf("start pairing: %w", err)
	}
	if err := cl.Connect(pctx, client.ConnectOpts{AllowUnpaired: true}); err != nil {
		cancel()
		return fmt.Errorf("connect for pairing: %w", err)
	}
	a.setState(StatePairing, "")
	if phone != "" {
		code, err := cl.PairPhone(pctx, strings.TrimPrefix(phone, "+"))
		if err != nil {
			cancel()
			a.setState(StateUnpaired, "pairing code request failed: "+err.Error())
			return err
		}
		a.mu.Lock()
		a.pairCode = client.FormatPairingCode(code)
		a.mu.Unlock()
	}
	go a.watchPairing(pctx, cancel, cl, ch)
	return nil
}

func (a *Account) watchPairing(ctx context.Context, cancel context.CancelFunc, cl wa, ch <-chan qrItem) {
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			if a.State() == StatePairing {
				cl.Disconnect()
				a.setState(StateUnpaired, "pairing timed out; start again from the admin page")
			}
			return
		case item, ok := <-ch:
			if !ok {
				return
			}
			switch item.Event {
			case "code":
				a.mu.Lock()
				a.qr = item.Code
				a.mu.Unlock()
			case "success":
				a.mu.Lock()
				a.qr, a.pairCode = "", ""
				a.mu.Unlock()
				if err := a.checkIdentity(cl.OwnPhone(), true); err != nil {
					a.setState(StateError, err.Error()+" (the new link was removed)")
					_ = cl.Logout(context.Background())
					a.dropClient()
					return
				}
				a.setState(StateConnecting, "paired; finishing initial sync")
				return
			case "timeout":
				a.setState(StateUnpaired, "pairing timed out; start again from the admin page")
				return
			default:
				if item.Error != nil || strings.HasPrefix(item.Event, "err") {
					a.setState(StateUnpaired, "pairing failed: "+item.Event)
					return
				}
			}
		}
	}
}

// Logout unlinks this account's device. Other accounts are unaffected.
func (a *Account) Logout(ctx context.Context) error {
	a.mu.Lock()
	cl := a.cl
	a.mu.Unlock()
	if cl == nil || !cl.IsLoggedIn() {
		return errors.New("account is not paired")
	}
	if err := cl.Logout(ctx); err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	a.setState(StateLoggedOut, "logged out by admin")
	a.dropClient()
	return nil
}

// Reset archives the account's data (messages, media, session) so the slot
// can be paired to a different number without mislabeling old data. The
// account must be unpaired or logged out first.
func (a *Account) Reset() (archived string, err error) {
	switch a.State() {
	case StateConnected, StateConnecting, StateDisconnected, StatePairing:
		return "", errors.New("log out (or wait for pairing to finish) before resetting")
	}
	a.close()
	ts := time.Now().UTC().Format("20060102T150405Z")
	archived = filepath.Join(a.m.cfg.DataDir, "archive", a.ID+"-"+ts)
	if err := os.MkdirAll(filepath.Dir(archived), 0o700); err != nil {
		return "", err
	}
	if err := os.Rename(a.Dir, archived); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("archive account data: %w", err)
	}
	if err := a.open(); err != nil {
		a.setState(StateError, err.Error())
		return archived, err
	}
	a.setState(StateUnpaired, "slot reset; previous data archived")
	return archived, nil
}

func last4(s string) string {
	if len(s) <= 4 {
		return s
	}
	return s[len(s)-4:]
}

// SortedIDs returns configured account IDs, sorted (for messages).
func (m *Manager) SortedIDs() []string {
	ids := append([]string(nil), m.order...)
	sort.Strings(ids)
	return ids
}
