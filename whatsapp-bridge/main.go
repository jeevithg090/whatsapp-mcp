// Command whatsapp-bridge connects to WhatsApp through the multi-device web
// API, mirrors chats and messages into a local SQLite database and exposes a
// small REST API used by the WhatsApp MCP server to send messages and download
// media.
package main

import (
	"context"
	"errors"
	"fmt"
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

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "whatsapp-bridge: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := LoadConfig()
	logger := waLog.Stdout("Client", cfg.LogLevel, true)
	dbLog := waLog.Stdout("Database", cfg.LogLevel, true)
	logger.Infof("Starting WhatsApp bridge (store: %s)", cfg.StoreDir)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(cfg.StoreDir, 0o700); err != nil {
		return fmt.Errorf("create store directory: %w", err)
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
		logger.Infof("REST API listening on http://%s/api", cfg.ListenAddr)
		if cfg.APIKey == "" {
			logger.Warnf("WHATSAPP_BRIDGE_API_KEY is not set; the REST API is unauthenticated")
		}
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	logger.Infof("Bridge is running. Press Ctrl+C to disconnect and exit.")
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

// connect connects the client, pairing it with a QR code when there is no
// stored session.
func connect(ctx context.Context, client *whatsmeow.Client, logger waLog.Logger) error {
	if client.Store.ID != nil {
		if err := client.Connect(); err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		return nil
	}

	qrChan, err := client.GetQRChannel(ctx)
	if err != nil {
		return fmt.Errorf("get QR channel: %w", err)
	}
	if err := client.Connect(); err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	for evt := range qrChan {
		switch evt.Event {
		case whatsmeow.QRChannelEventCode:
			fmt.Println("\nScan this QR code with WhatsApp (Settings > Linked devices > Link a device):")
			qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
		case whatsmeow.QRChannelSuccess.Event:
			logger.Infof("Successfully paired with WhatsApp")
			return nil
		case whatsmeow.QRChannelTimeout.Event:
			return errors.New("timed out waiting for the QR code to be scanned; run the bridge again")
		case whatsmeow.QRChannelEventError:
			return fmt.Errorf("pairing failed: %w", evt.Error)
		default:
			logger.Warnf("Pairing event: %s", evt.Event)
		}
	}
	return errors.New("pairing was interrupted before it completed")
}
