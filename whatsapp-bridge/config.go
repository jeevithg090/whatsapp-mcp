package main

import (
	"os"
	"strings"
)

// Config holds the runtime configuration of the bridge. Every value can be
// overridden with an environment variable so the bridge can run unmodified in
// different setups (local machine, container, CI).
type Config struct {
	// StoreDir is the directory holding the WhatsApp session database
	// (whatsapp.db), the message history (messages.db) and downloaded media.
	StoreDir string
	// ListenAddr is the host:port the REST API binds to. It defaults to the
	// loopback interface because the API can send messages on your behalf and
	// read local files; only expose it on other interfaces deliberately.
	ListenAddr string
	// APIKey, when non-empty, must be supplied by API clients in the
	// "X-API-Key" header (or as a bearer token).
	APIKey string
	// LogLevel is the whatsmeow log level (DEBUG, INFO, WARN, ERROR).
	LogLevel string
}

const (
	defaultStoreDir   = "store"
	defaultListenAddr = "127.0.0.1:8080"
	defaultLogLevel   = "INFO"
)

// LoadConfig reads the configuration from the environment, applying defaults.
func LoadConfig() Config {
	return Config{
		StoreDir:   envOr("WHATSAPP_BRIDGE_STORE_DIR", defaultStoreDir),
		ListenAddr: envOr("WHATSAPP_BRIDGE_ADDR", defaultListenAddr),
		APIKey:     strings.TrimSpace(os.Getenv("WHATSAPP_BRIDGE_API_KEY")),
		LogLevel:   strings.ToUpper(envOr("WHATSAPP_BRIDGE_LOG_LEVEL", defaultLogLevel)),
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
