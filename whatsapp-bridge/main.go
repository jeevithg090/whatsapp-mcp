// Command whatsapp-bridge connects to WhatsApp through the multi-device web
// API, mirrors chats and messages into a local SQLite database and exposes a
// small REST API used by the WhatsApp MCP server to send messages and download
// media.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mdp/qrterminal"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// clientBridge adapts the whatsmeow client to the Bridge interface.
type clientBridge struct {
	client   *whatsmeow.Client
	store    *MessageStore
	storeDir string
}

func (b *clientBridge) Send(ctx context.Context, recipient, message, mediaPath string) error {
	return sendWhatsAppMessage(ctx, b.client, recipient, message, mediaPath)
}

func (b *clientBridge) Download(ctx context.Context, messageID, chatJID string) (DownloadResult, error) {
	return downloadMedia(ctx, b.client, b.store, b.storeDir, messageID, chatJID)
}

func (b *clientBridge) Status() BridgeStatus {
	return BridgeStatus{Connected: b.client.IsConnected(), LoggedIn: b.client.IsLoggedIn()}
}

func main() {
	cfg, err := ParseConfig(os.Args[1:], os.Stderr)
	switch {
	case errors.Is(err, flag.ErrHelp):
		return
	case errors.Is(err, errVersion):
		fmt.Println("whatsapp-bridge", version)
		return
	case err != nil:
		fmt.Fprintf(os.Stderr, "whatsapp-bridge: %v\n", err)
		os.Exit(2)
	}
	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "whatsapp-bridge: %v\n", err)
		os.Exit(1)
	}
}

// isTerminal reports whether f is attached to a terminal, so log colours are
// only used where they render.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func run(cfg Config) error {
	color := isTerminal(os.Stdout)
	logger := waLog.Stdout("Client", cfg.LogLevel, color)
	dbLog := waLog.Stdout("Database", cfg.LogLevel, color)

	storeDir, err := filepath.Abs(cfg.StoreDir)
	if err != nil {
		return fmt.Errorf("resolve store directory %q: %w", cfg.StoreDir, err)
	}
	cfg.StoreDir = storeDir
	printBanner(os.Stdout, cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Bind the REST API port before pairing, so a port conflict is reported
	// immediately instead of after the QR code has been scanned.
	listener, err := listen(cfg.ListenAddr)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()

	if err := os.MkdirAll(cfg.StoreDir, 0o700); err != nil {
		return fmt.Errorf("cannot create store directory %s: %w\n  Choose a writable location with --store or WHATSAPP_BRIDGE_STORE_DIR", cfg.StoreDir, err)
	}

	sessionDSN := fmt.Sprintf("file:%s?_foreign_keys=on", filepath.ToSlash(filepath.Join(cfg.StoreDir, "whatsapp.db")))
	container, err := sqlstore.New(ctx, "sqlite3", sessionDSN, dbLog)
	if err != nil {
		return fmt.Errorf("open session database: %w", err)
	}
	defer func() { _ = container.Close() }()

	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		return fmt.Errorf("load device: %w", err)
	}

	messageStore, err := NewMessageStore(cfg.StoreDir)
	if err != nil {
		return fmt.Errorf("open message store: %w", err)
	}
	defer func() { _ = messageStore.Close() }()

	client := whatsmeow.NewClient(deviceStore, logger)
	handler := &eventHandler{client: client, store: messageStore, log: logger}
	client.AddEventHandler(handler.handle)

	if err := connect(ctx, client, logger); err != nil {
		return err
	}
	defer client.Disconnect()

	server := newHTTPServer(cfg.ListenAddr, NewAPIHandler(&clientBridge{
		client:   client,
		store:    messageStore,
		storeDir: cfg.StoreDir,
	}, cfg.APIKey))

	serverErr := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	logger.Infof("REST API listening on http://%s/api", cfg.ListenAddr)
	if cfg.APIKey == "" {
		logger.Warnf("WHATSAPP_BRIDGE_API_KEY is not set; the REST API is unauthenticated")
	}
	if !cfg.IsLoopback() {
		logger.Warnf("REST API is reachable from other machines (%s); anyone who can reach it can send messages as you", cfg.ListenAddr)
	}
	printReady(os.Stdout, cfg)

	select {
	case <-ctx.Done():
	case err := <-serverErr:
		return fmt.Errorf("REST API server: %w", err)
	}

	logger.Infof("Shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return server.Shutdown(shutdownCtx)
}

// listen binds addr, turning the common failures into actionable messages.
func listen(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err == nil {
		return ln, nil
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return nil, fmt.Errorf("cannot listen on %s: the address is already in use.\n"+
			"  Another whatsapp-bridge may already be running; stop it first, or pick another port, e.g.\n"+
			"    whatsapp-bridge --addr 127.0.0.1:8081\n"+
			"  and set WHATSAPP_API_BASE_URL=http://127.0.0.1:8081/api for the MCP server", addr)
	}
	if errors.Is(err, syscall.EACCES) {
		return nil, fmt.Errorf("cannot listen on %s: permission denied; use a port above 1024 (--addr)", addr)
	}
	return nil, fmt.Errorf("cannot listen on %s: %w", addr, err)
}

func printBanner(w io.Writer, cfg Config) {
	auth := "off (set WHATSAPP_BRIDGE_API_KEY to require a key)"
	if cfg.APIKey != "" {
		auth = "on (X-API-Key / Bearer required)"
	}
	fmt.Fprintf(w, "whatsapp-bridge %s\n", version)
	fmt.Fprintf(w, "  store      %s\n", cfg.StoreDir)
	fmt.Fprintf(w, "  REST API   http://%s/api\n", cfg.ListenAddr)
	fmt.Fprintf(w, "  API key    %s\n", auth)
	fmt.Fprintf(w, "  log level  %s\n\n", cfg.LogLevel)
}

func printReady(w io.Writer, cfg Config) {
	fmt.Fprintf(w, "\nReady. Messages are being saved to %s\n", filepath.Join(cfg.StoreDir, "messages.db"))
	fmt.Fprintln(w, "Keep this running while you use the MCP server. Press Ctrl+C to stop.")
	fmt.Fprintln(w)
}

// connect connects the client, pairing it with a QR code when there is no
// stored session.
func connect(ctx context.Context, client *whatsmeow.Client, logger waLog.Logger) error {
	if client.Store.ID != nil {
		logger.Infof("Using saved session for %s", client.Store.ID.User)
		if err := client.Connect(); err != nil {
			return fmt.Errorf("connect to WhatsApp: %w\n  Check your internet connection and try again", err)
		}
		return nil
	}

	qrChan, err := client.GetQRChannel(ctx)
	if err != nil {
		return fmt.Errorf("get QR channel: %w", err)
	}
	if err := client.Connect(); err != nil {
		return fmt.Errorf("connect to WhatsApp: %w\n  Check your internet connection and try again", err)
	}

	first := true
	for evt := range qrChan {
		switch evt.Event {
		case whatsmeow.QRChannelEventCode:
			if first {
				fmt.Print(pairingInstructions)
				first = false
			} else {
				fmt.Println("\nThe previous code expired. Scan this new one:")
			}
			qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
		case whatsmeow.QRChannelSuccess.Event:
			logger.Infof("Paired successfully. History sync can take a few minutes for large accounts.")
			return nil
		case whatsmeow.QRChannelTimeout.Event:
			return errors.New("no QR code was scanned in time; run the bridge again to get a fresh code")
		case whatsmeow.QRChannelEventError:
			return fmt.Errorf("pairing failed: %w\n  Run the bridge again; if it keeps failing, remove a linked device in WhatsApp and retry", evt.Error)
		default:
			logger.Warnf("Pairing event: %s", evt.Event)
		}
	}
	if ctx.Err() != nil {
		return errors.New("pairing cancelled")
	}
	return errors.New("pairing was interrupted before it completed; run the bridge again")
}

const pairingInstructions = `
No saved session found: link this bridge to your WhatsApp account.
  1. Open WhatsApp on your phone.
  2. Go to Settings > Linked devices > Link a device.
  3. Scan the QR code below. A new code appears every ~20 seconds if it expires.
     (If the code looks broken, make the terminal window wider.)

`
