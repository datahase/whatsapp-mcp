package accounts

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/datahase/whatsapp-mcp/internal/client"
	"github.com/datahase/whatsapp-mcp/internal/config"
	"github.com/datahase/whatsapp-mcp/internal/security"
	"github.com/datahase/whatsapp-mcp/internal/store"
)

// fakeWA is a paired, connected client that records dispatches.
type fakeWA struct {
	phone    string
	sends    atomic.Int32
	sendErr  error
	notDisp  bool
	rateOK   bool
	nextID   atomic.Int32
	closed   atomic.Bool
	lastSend client.Outgoing
	mu       sync.Mutex
}

func (f *fakeWA) IsLoggedIn() bool                                  { return f.phone != "" }
func (f *fakeWA) OwnPhone() string                                  { return f.phone }
func (f *fakeWA) IsConnected() bool                                 { return true }
func (f *fakeWA) Connect(context.Context, client.ConnectOpts) error { return nil }
func (f *fakeWA) QRChannel(context.Context) (<-chan qrItem, error) {
	return make(chan qrItem), nil
}
func (f *fakeWA) PairPhone(context.Context, string) (string, error) { return "ABCD1234", nil }
func (f *fakeWA) Logout(context.Context) error                      { f.phone = ""; return nil }
func (f *fakeWA) Disconnect()                                       {}
func (f *fakeWA) Close()                                            { f.closed.Store(true) }
func (f *fakeWA) Real() live                                        { return f }
func (f *fakeWA) CheckSendRate(types.JID) (bool, time.Duration, string) {
	if !f.rateOK {
		return false, 30 * time.Second, "minimum interval"
	}
	return true, 0, ""
}
func (f *fakeWA) NewMessageID() string { return "3EB0" + string(rune('A'+f.nextID.Add(1))) }
func (f *fakeWA) SendOutgoing(_ context.Context, o client.Outgoing) (client.SendOutcome, error) {
	time.Sleep(5 * time.Millisecond) // widen the race window for concurrent callers
	f.sends.Add(1)
	f.mu.Lock()
	f.lastSend = o
	f.mu.Unlock()
	if f.sendErr != nil {
		return client.SendOutcome{Dispatched: !f.notDisp}, f.sendErr
	}
	return client.SendOutcome{Dispatched: true, ServerTime: time.Now()}, nil
}
func (f *fakeWA) DownloadMessageMedia(context.Context, string, string, string, int64) (string, string, error) {
	return "", "", errors.New("not used")
}
func (f *fakeWA) RequestHistorySync(context.Context, string, time.Time, string, int, bool) (string, error) {
	return "requested", nil
}

func testConfig(t *testing.T, dataDir string, ids ...string) *config.Config {
	t.Helper()
	c := &config.Config{DataDir: dataDir, Auth: config.Auth{TokenFile: "x", AdminPasswordFile: "y"}, LogLevel: "ERROR"}
	for _, id := range ids {
		c.Accounts = append(c.Accounts, config.Account{ID: id, Label: id})
	}
	c.Limits.MaxMediaBytes = 1 << 20
	c.Limits.MediaCacheBytes = 1 << 20
	c.Limits.MessageCacheBytes = 1 << 30
	c.Limits.StagedUploadTTL = time.Hour
	return c
}

// startManager starts accounts with fake clients keyed by account ID.
func startManager(t *testing.T, cfg *config.Config, fakes map[string]*fakeWA) *Manager {
	t.Helper()
	m := NewManager(cfg, &security.Redactor{})
	m.newClient = func(_ context.Context, c client.Config) (wa, error) {
		for id, f := range fakes {
			if filepath.Base(c.StoreDir) == id {
				return f, nil
			}
		}
		return &fakeWA{}, nil
	}
	m.Start(context.Background())
	t.Cleanup(m.Stop)
	waitFor(t, func() bool {
		for _, a := range m.All() {
			if a.State() == StateDisabled {
				return false
			}
		}
		return true
	})
	return m
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// connect simulates whatsmeow's Connected event for the account.
func connect(a *Account) { a.onEvent(&eventsConnected) }

func TestNoDefaultAccount(t *testing.T) {
	m := NewManager(testConfig(t, t.TempDir(), "only"), &security.Redactor{})
	if _, err := m.Get(""); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("empty account_id must fail even with one account: %v", err)
	}
	if _, err := m.Get("other"); !errors.Is(err, ErrUnknownAccount) {
		t.Fatalf("unknown account: %v", err)
	}
}

func TestAccountsWithIdenticalIDsStayIsolated(t *testing.T) {
	data := t.TempDir()
	m := startManager(t, testConfig(t, data, "alpha", "beta"), map[string]*fakeWA{"alpha": {phone: "15550000001"}, "beta": {phone: "15550000002"}})
	ctx := context.Background()
	for _, id := range []string{"alpha", "beta"} {
		a, _ := m.Get(id)
		st, err := a.Store()
		if err != nil {
			t.Fatal(err)
		}
		_ = st.StoreChat("15559999999@s.whatsapp.net", "Same Chat", time.Now())
		if err := st.StoreMessage(ctx, store.Message{ID: "SAMEID", ChatJID: "15559999999@s.whatsapp.net", Sender: "15559999999", Content: "secret-" + id, Timestamp: time.Now()}, nil, nil, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"alpha", "beta"} {
		a, _ := m.Get(id)
		st, _ := a.Store()
		msgs, err := st.ListMessages(ctx, store.ListMessagesParams{ChatJID: "15559999999@s.whatsapp.net"})
		if err != nil || len(msgs) != 1 || msgs[0].Content != "secret-"+id {
			t.Fatalf("%s sees %+v (%v)", id, msgs, err)
		}
	}

	// A media ID from one account is not resolvable through the other.
	alpha, _ := m.Get("alpha")
	beta, _ := m.Get("beta")
	obj, err := alpha.StageUpload(ctx, bytes.NewReader(pngBytes()), "p.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := beta.OpenMedia(obj.ID); err == nil {
		t.Fatal("beta resolved alpha's media_id")
	}
	if _, _, err := alpha.OpenMedia(obj.ID); err != nil {
		t.Fatal(err)
	}
}

func TestOneBrokenAccountLeavesOthersUsable(t *testing.T) {
	data := t.TempDir()
	// Make beta's directory path a regular file so it cannot open.
	_ = os.MkdirAll(filepath.Join(data, "accounts"), 0o700)
	_ = os.WriteFile(filepath.Join(data, "accounts", "beta"), []byte("x"), 0o600)
	m := startManager(t, testConfig(t, data, "alpha", "beta"), map[string]*fakeWA{"alpha": {phone: "15550000001"}})
	alpha, _ := m.Get("alpha")
	beta, _ := m.Get("beta")
	if beta.State() != StateError {
		t.Fatalf("beta state %s", beta.State())
	}
	if _, err := beta.Store(); err == nil {
		t.Fatal("broken account returned a store")
	}
	if _, err := alpha.Store(); err != nil {
		t.Fatalf("alpha unusable: %v", err)
	}
}

func TestStoreLockPreventsSecondInstanceAndIsReleased(t *testing.T) {
	data := t.TempDir()
	cfg := testConfig(t, data, "alpha")
	m1 := NewManager(cfg, &security.Redactor{})
	m1.newClient = func(context.Context, client.Config) (wa, error) { return &fakeWA{}, nil }
	m1.Start(context.Background())

	m2 := NewManager(cfg, &security.Redactor{})
	m2.newClient = m1.newClient
	m2.Start(context.Background())
	a2, _ := m2.Get("alpha")
	if a2.State() != StateError || !strings.Contains(a2.Status().Error, "in use") {
		t.Fatalf("second instance state %s %q", a2.State(), a2.Status().Error)
	}
	m2.Stop()
	m1.Stop()

	m3 := NewManager(cfg, &security.Redactor{})
	m3.newClient = m1.newClient
	m3.Start(context.Background())
	defer m3.Stop()
	a3, _ := m3.Get("alpha")
	if _, err := a3.Store(); err != nil {
		t.Fatalf("lock not released on Stop: %v", err)
	}
}

func TestIdentityRules(t *testing.T) {
	cfg := testConfig(t, t.TempDir(), "alpha", "beta")
	cfg.Accounts[0].ExpectedPhone = "15550000001"
	m := startManager(t, cfg, map[string]*fakeWA{"alpha": {phone: "15550000009"}, "beta": {phone: "15550000002"}})
	alpha, _ := m.Get("alpha")
	if alpha.State() != StateError || !strings.Contains(alpha.Status().Error, "expected_phone") {
		t.Fatalf("expected-phone mismatch not enforced: %s %q", alpha.State(), alpha.Status().Error)
	}
	beta, _ := m.Get("beta")
	connect(beta)
	if beta.State() != StateConnected {
		t.Fatalf("beta %s", beta.State())
	}
	// beta's slot remembers its number; a different number is refused.
	if err := beta.checkIdentity("15550000003", true); err == nil || !strings.Contains(err.Error(), "reset") {
		t.Fatalf("slot re-pair to different number allowed: %v", err)
	}
	// One number cannot back two accounts.
	cfg2 := testConfig(t, t.TempDir(), "x", "y")
	m2 := startManager(t, cfg2, map[string]*fakeWA{"x": {phone: "15550000005"}, "y": {phone: "15550000005"}})
	x, _ := m2.Get("x")
	y, _ := m2.Get("y")
	connect(x)
	connect(y)
	if x.State() == StateConnected && y.State() == StateConnected {
		t.Fatal("duplicate phone connected under two accounts")
	}
}

func sendReq(key, text string) SendRequest {
	return SendRequest{IdempotencyKey: key, Recipient: "15551234567", Text: text}
}

func TestSendIdempotencyConcurrentAndConflict(t *testing.T) {
	f := &fakeWA{phone: "15550000001", rateOK: true}
	m := startManager(t, testConfig(t, t.TempDir(), "alpha"), map[string]*fakeWA{"alpha": f})
	a, _ := m.Get("alpha")
	connect(a)
	ctx := context.Background()

	var wg sync.WaitGroup
	results := make([]*SendResult, 10)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := a.Send(ctx, sendReq("k1", "hello"))
			if err != nil {
				t.Error(err)
			}
			results[i] = r
		}(i)
	}
	wg.Wait()
	if n := f.sends.Load(); n != 1 {
		t.Fatalf("concurrent same-key calls dispatched %d messages", n)
	}
	opID := results[0].Operation.OpID
	for _, r := range results {
		if r == nil || r.Operation.OpID != opID || r.Operation.Status != store.OpAccepted {
			t.Fatalf("inconsistent result %+v", r)
		}
	}
	if f.lastSend.MessageID != results[0].Operation.WAMessageID {
		t.Fatal("dispatch did not use the recorded message ID")
	}
	if _, err := a.Send(ctx, sendReq("k1", "different text")); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("key reuse with other content: %v", err)
	}
}

func TestAmbiguousFailureIsUnknownAndNeverResent(t *testing.T) {
	f := &fakeWA{phone: "15550000001", rateOK: true, sendErr: errors.New("timed out waiting for server ack")}
	data := t.TempDir()
	cfg := testConfig(t, data, "alpha")
	m := startManager(t, cfg, map[string]*fakeWA{"alpha": f})
	a, _ := m.Get("alpha")
	connect(a)
	ctx := context.Background()
	r, err := a.Send(ctx, sendReq("k2", "hi"))
	if err != nil || r.Operation.Status != store.OpUnknown || r.RetryHint == "" {
		t.Fatalf("ambiguous failure: %+v %v", r, err)
	}
	f.sendErr = nil
	r2, err := a.Send(ctx, sendReq("k2", "hi"))
	if err != nil || !r2.Replayed || r2.Operation.Status != store.OpUnknown || f.sends.Load() != 1 {
		t.Fatalf("retry after unknown resent: %+v %v sends=%d", r2, err, f.sends.Load())
	}
	// A later delivery receipt resolves the unknown operation.
	st, _ := a.Store()
	_ = st.ApplyReceipt([]string{r.Operation.WAMessageID}, store.OpDelivered)
	op, _ := a.GetSendStatus(ctx, "k2")
	if op.Status != store.OpDelivered {
		t.Fatalf("receipt not applied: %s", op.Status)
	}
	_ = st.ApplyReceipt([]string{r.Operation.WAMessageID}, store.OpDelivered)
	_ = st.ApplyReceipt([]string{r.Operation.WAMessageID}, store.OpRead)
	_ = st.ApplyReceipt([]string{r.Operation.WAMessageID}, store.OpDelivered)
	if op, _ = a.GetSendStatus(ctx, op.OpID); op.Status != store.OpRead {
		t.Fatalf("receipt state went backwards: %s", op.Status)
	}
}

func TestPreDispatchFailureAndRateLimitCreateNoSend(t *testing.T) {
	f := &fakeWA{phone: "15550000001", rateOK: false}
	m := startManager(t, testConfig(t, t.TempDir(), "alpha"), map[string]*fakeWA{"alpha": f})
	a, _ := m.Get("alpha")
	connect(a)
	ctx := context.Background()
	var rl *RateLimitError
	if _, err := a.Send(ctx, sendReq("k3", "hi")); !errors.As(err, &rl) || rl.RetryAfter == 0 {
		t.Fatalf("rate limit: %v", err)
	}
	f.rateOK = true
	f.sendErr, f.notDisp = errors.New("upload failed"), true
	r, err := a.Send(ctx, sendReq("k3", "hi"))
	if err != nil || r.Operation.Status != store.OpFailed {
		t.Fatalf("pre-dispatch failure: %+v %v", r, err)
	}
	// Disconnected accounts reject sends; there is no offline queue.
	a.setState(StateDisconnected, "")
	if _, err := a.Send(ctx, sendReq("k4", "hi")); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("disconnected send: %v", err)
	}
	if _, err := a.Send(ctx, SendRequest{IdempotencyKey: "k5", Recipient: "Mum", Text: "x"}); err == nil {
		t.Fatal("name recipient accepted")
	}
}

func TestRestartMarksInFlightSendsUnknown(t *testing.T) {
	data := t.TempDir()
	cfg := testConfig(t, data, "alpha")
	dir := filepath.Join(data, "accounts", "alpha")
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	op, _, _ := st.BeginSendOp(ctx, "inflight", "fp", "text", "1@s.whatsapp.net", "", "WAID1")
	_ = st.SetSendOpStatus(op.OpID, store.OpSending, "", nil)
	_, _, _ = st.BeginSendOp(ctx, "queued", "fp", "text", "1@s.whatsapp.net", "", "WAID2")
	st.Close()

	f := &fakeWA{phone: "15550000001", rateOK: true}
	m := startManager(t, cfg, map[string]*fakeWA{"alpha": f})
	a, _ := m.Get("alpha")
	connect(a)
	if op, _ := a.GetSendStatus(ctx, "inflight"); op.Status != store.OpUnknown {
		t.Fatalf("in-flight op after restart: %s", op.Status)
	}
	if op, _ := a.GetSendStatus(ctx, "queued"); op.Status != store.OpFailed {
		t.Fatalf("undispatched op after restart: %s", op.Status)
	}
	if f.sends.Load() != 0 {
		t.Fatal("recovered operations were resent")
	}
}

func TestLoggedOutAccountDoesNotAffectOther(t *testing.T) {
	m := startManager(t, testConfig(t, t.TempDir(), "alpha", "beta"), map[string]*fakeWA{"alpha": {phone: "15550000001", rateOK: true}, "beta": {phone: "15550000002", rateOK: true}})
	alpha, _ := m.Get("alpha")
	beta, _ := m.Get("beta")
	connect(alpha)
	connect(beta)
	alpha.onEvent(&eventsLoggedOut)
	if alpha.State() != StateLoggedOut || beta.State() != StateConnected {
		t.Fatalf("states alpha=%s beta=%s", alpha.State(), beta.State())
	}
	if _, err := beta.Send(context.Background(), sendReq("b1", "hi")); err != nil {
		t.Fatalf("beta send after alpha logout: %v", err)
	}
	if _, err := alpha.Store(); err != nil {
		t.Fatal("logged-out account must keep cached data readable")
	}
	// Reset archives data so a different number can use the slot.
	archived, err := alpha.Reset()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(archived, "messages.db")); err != nil {
		t.Fatalf("archive missing data: %v", err)
	}
	if alpha.State() != StateUnpaired {
		t.Fatalf("after reset: %s", alpha.State())
	}
}

func TestJanitorExpiresUploadsAndBoundsCache(t *testing.T) {
	cfg := testConfig(t, t.TempDir(), "alpha")
	m := startManager(t, cfg, map[string]*fakeWA{"alpha": {phone: "15550000001"}})
	a, _ := m.Get("alpha")
	ctx := context.Background()
	obj, err := a.StageUpload(ctx, bytes.NewReader(pngBytes()), "x.png")
	if err != nil {
		t.Fatal(err)
	}
	st, _ := a.Store()
	past := time.Now().Add(-time.Minute)
	obj.ExpiresAt = &past
	_ = st.PutMedia(obj)
	m.RunJanitor(false)
	if _, _, err := a.OpenMedia(obj.ID); err == nil {
		t.Fatal("expired upload still available")
	}
	if _, err := a.StageUpload(ctx, bytes.NewReader(make([]byte, cfg.Limits.MaxMediaBytes+1)), "big.bin"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversize upload: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(a.Dir, "media", "staged"))
	if len(entries) != 0 {
		t.Fatalf("rejected uploads left files: %v", entries)
	}
	// Message-cache budget pauses storage instead of purging history.
	m.cfg.Limits.MessageCacheBytes = 1
	m.RunJanitor(false)
	err = st.StoreMessage(ctx, store.Message{ID: "n", ChatJID: "1@s.whatsapp.net", Content: "x", Timestamp: time.Now()}, nil, nil, nil, 0)
	if !errors.Is(err, store.ErrCacheFull) || !a.Status().MessageCacheFull {
		t.Fatalf("cache budget not enforced: %v", err)
	}
}

func pngBytes() []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	return b.Bytes()
}

var (
	eventsConnected = events.Connected{}
	eventsLoggedOut = events.LoggedOut{}
)
