package client

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	wmstore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/datahase/whatsapp-mcp/internal/ratelimit"
	"github.com/datahase/whatsapp-mcp/internal/security"
	"github.com/datahase/whatsapp-mcp/internal/store"
)

// Config configures a new Client. One Client serves exactly one account.
type Config struct {
	StoreDir string             // the account directory holding whatsapp.db
	Store    *store.Store       // the account's message store
	Logger   waLog.Logger       // optional; defaults to stderr at INFO
	Redactor *security.Redactor // optional; defaults to a redacting instance
	// RateLimiter throttles outbound sends. nil disables limiting (tests).
	RateLimiter *ratelimit.Limiter
	// OnEvent receives every whatsmeow event after the client's own
	// ingestion. It runs on whatsmeow's event goroutine; keep it short.
	OnEvent func(evt any)
}

// Client wraps one whatsmeow.Client with its message store. It owns the
// whatsmeow SQL container and exactly one event handler; Close releases both.
type Client struct {
	wa             *whatsmeow.Client
	container      *sqlstore.Container
	downloadToFile func(context.Context, whatsmeow.DownloadableMessage, whatsmeow.File) error
	store          *store.Store
	storeDir       string
	log            waLog.Logger
	handlerID      uint32
	redactor       *security.Redactor
	limiter        *ratelimit.Limiter
	onEvent        func(any)
	closeOnce      sync.Once

	// Test seams; nil means use the whatsmeow client.
	isConnected func() bool
	sendFn      func(context.Context, types.JID, *waProto.Message, whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error)
	uploadFn    func(context.Context, io.Reader, io.ReadWriteSeeker, whatsmeow.MediaType) (whatsmeow.UploadResponse, error)
}

var waVersionOnce sync.Once

// InitWAVersion fetches the current WhatsApp web version once per process.
// whatsmeow keeps this in library-global state, so it must run before any
// client connects and never concurrently with connections.
func InitWAVersion(ctx context.Context, logger waLog.Logger) {
	waVersionOnce.Do(func() {
		latest, err := whatsmeow.GetLatestVersion(ctx, nil)
		if err != nil {
			logger.Warnf("Failed to get latest WhatsApp version: %v (using built-in default)", err)
			return
		}
		if latest != nil {
			wmstore.SetWAVersion(*latest)
			logger.Infof("Using WhatsApp version: %s", latest.String())
		}
	})
}

// New opens the account's whatsmeow store and loads its device (creating an
// unpaired one if none exists). The returned Client is not connected.
func New(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Store == nil || cfg.StoreDir == "" {
		return nil, errors.New("client: Store and StoreDir are required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = NewStderrLogger("Client", "INFO", false)
	}
	redactor := cfg.Redactor
	if redactor == nil {
		redactor = &security.Redactor{}
	}

	waPath := filepath.Join(cfg.StoreDir, "whatsapp.db")
	container, err := sqlstore.New(ctx, "sqlite3", "file:"+waPath+"?_foreign_keys=on&_busy_timeout=5000", logger.Sub("DB"))
	if err != nil {
		return nil, fmt.Errorf("open whatsmeow sqlstore: %w", err)
	}
	if err := store.ChmodBestEffort(waPath, 0o600); err != nil {
		container.Close()
		return nil, fmt.Errorf("chmod whatsapp.db: %w", err)
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			container.Close()
			return nil, fmt.Errorf("get device: %w", err)
		}
		device = container.NewDevice()
	}
	if device == nil {
		device = container.NewDevice()
	}

	wa := whatsmeow.NewClient(device, redactingLogger{Logger: logger, redactor: redactor})
	c := &Client{
		wa:        wa,
		container: container,
		store:     cfg.Store,
		storeDir:  cfg.StoreDir,
		log:       logger,
		redactor:  redactor,
		limiter:   cfg.RateLimiter,
		onEvent:   cfg.OnEvent,
	}
	c.downloadToFile = wa.DownloadToFile
	c.handlerID = wa.AddEventHandler(c.handleEvent)
	return c, nil
}

// Close removes the event handler, disconnects and closes the whatsmeow
// database. Safe to call more than once.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		if c.wa != nil {
			c.wa.RemoveEventHandler(c.handlerID)
			c.wa.Disconnect()
		}
		if c.container != nil {
			_ = c.container.Close()
		}
	})
}

// IsLoggedIn reports whether the device has a stored session.
func (c *Client) IsLoggedIn() bool { return c.wa.Store.ID != nil }

// OwnPhone returns the paired phone number (user part), or "".
func (c *Client) OwnPhone() string {
	if c.wa.Store.ID == nil {
		return ""
	}
	return c.wa.Store.ID.User
}

// QRChannel exposes whatsmeow's pairing QR channel. Call before Connect on
// an unpaired device.
func (c *Client) QRChannel(ctx context.Context) (<-chan whatsmeow.QRChannelItem, error) {
	return c.wa.GetQRChannel(ctx)
}

// Logout unlinks the device from WhatsApp and clears the local session.
func (c *Client) Logout(ctx context.Context) error { return c.wa.Logout(ctx) }

// ConnectOpts controls Connect.
type ConnectOpts struct {
	// AllowUnpaired lets the pairing flow connect an unpaired device.
	AllowUnpaired bool
}

var errNotPaired = errors.New("device not paired")

// Connect opens the WebSocket. whatsmeow's own auto-reconnect handles later
// drops; callers must not run a competing reconnect loop on top.
func (c *Client) Connect(ctx context.Context, opts ConnectOpts) error {
	if !opts.AllowUnpaired && c.wa.Store.ID == nil {
		return errNotPaired
	}
	return c.wa.ConnectContext(ctx)
}

// Disconnect closes the socket but keeps the database open.
func (c *Client) Disconnect() {
	if c.wa != nil {
		c.wa.Disconnect()
	}
}

// IsConnected reports whether the WebSocket is up.
func (c *Client) IsConnected() bool { return c.wa.IsConnected() }

// Store returns the message cache.
func (c *Client) Store() *store.Store { return c.store }
