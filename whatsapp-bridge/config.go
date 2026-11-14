package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
)

// version is the bridge version. Release builds override it with
// -ldflags "-X main.version=...".
var version = "dev"

// Config holds the runtime configuration of the bridge. Every value can be
// set with an environment variable or, taking precedence, a command-line flag.
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

var validLogLevels = []string{"DEBUG", "INFO", "WARN", "ERROR"}

// LoadConfig reads the configuration from the environment, applying defaults.
func LoadConfig() Config {
	return Config{
		StoreDir:   envOr("WHATSAPP_BRIDGE_STORE_DIR", defaultStoreDir),
		ListenAddr: envOr("WHATSAPP_BRIDGE_ADDR", defaultListenAddr),
		APIKey:     strings.TrimSpace(os.Getenv("WHATSAPP_BRIDGE_API_KEY")),
		LogLevel:   strings.ToUpper(envOr("WHATSAPP_BRIDGE_LOG_LEVEL", defaultLogLevel)),
	}
}

// errVersion is returned by ParseConfig when --version was requested.
var errVersion = errors.New("version requested")

// ParseConfig builds the configuration from the environment and the
// command-line arguments (flags win over environment variables) and validates
// it. It returns flag.ErrHelp for -h/--help and errVersion for --version;
// usage and errors are written to out.
func ParseConfig(args []string, out io.Writer) (Config, error) {
	cfg := LoadConfig()

	fs := flag.NewFlagSet("whatsapp-bridge", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&cfg.StoreDir, "store", cfg.StoreDir, "`directory` holding the session (whatsapp.db), message history (messages.db)\nand downloaded media; relative paths start from the current directory (env WHATSAPP_BRIDGE_STORE_DIR)")
	fs.StringVar(&cfg.ListenAddr, "addr", cfg.ListenAddr, "listen address of the REST API, as `host:port`; keep it on 127.0.0.1 (env WHATSAPP_BRIDGE_ADDR)")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "log `level`: DEBUG, INFO, WARN or ERROR (env WHATSAPP_BRIDGE_LOG_LEVEL)")
	showVersion := fs.Bool("version", false, "print the version and exit")
	fs.Usage = func() { printUsage(fs) }

	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	if *showVersion {
		return cfg, errVersion
	}
	if fs.NArg() > 0 {
		return cfg, fmt.Errorf("unexpected argument %q (run with --help for usage)", fs.Arg(0))
	}
	cfg.LogLevel = strings.ToUpper(strings.TrimSpace(cfg.LogLevel))
	return cfg, cfg.Validate()
}

// Validate reports configuration mistakes with a message saying how to fix them.
func (c Config) Validate() error {
	if strings.TrimSpace(c.StoreDir) == "" {
		return errors.New("store directory must not be empty (set --store or WHATSAPP_BRIDGE_STORE_DIR)")
	}
	if !slices.Contains(validLogLevels, c.LogLevel) {
		return fmt.Errorf("invalid log level %q: use one of %s (--log-level or WHATSAPP_BRIDGE_LOG_LEVEL)",
			c.LogLevel, strings.Join(validLogLevels, ", "))
	}
	_, port, err := net.SplitHostPort(c.ListenAddr)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: expected host:port such as 127.0.0.1:8080 (--addr or WHATSAPP_BRIDGE_ADDR)", c.ListenAddr)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return fmt.Errorf("invalid port %q in listen address %q (--addr or WHATSAPP_BRIDGE_ADDR)", port, c.ListenAddr)
	}
	return nil
}

// IsLoopback reports whether the REST API only listens on the local machine.
func (c Config) IsLoopback() bool {
	host, _, err := net.SplitHostPort(c.ListenAddr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func printUsage(fs *flag.FlagSet) {
	out := fs.Output()
	fmt.Fprintf(out, `whatsapp-bridge %s

Links to your WhatsApp account as a "linked device", mirrors chats and messages
into a local SQLite database and serves a small REST API that the WhatsApp MCP
server uses to send messages and download media.

Usage:
  whatsapp-bridge [flags]

On the first run a QR code is printed. On your phone open WhatsApp >
Settings > Linked devices > Link a device and scan it. The session is saved in
the store directory, so later runs reconnect without a QR code.

Flags:
`, version)
	fs.PrintDefaults()
	fmt.Fprint(out, `
Environment:
  WHATSAPP_BRIDGE_STORE_DIR   same as --store (default "store")
  WHATSAPP_BRIDGE_ADDR        same as --addr (default "127.0.0.1:8080")
  WHATSAPP_BRIDGE_LOG_LEVEL   same as --log-level (default "INFO")
  WHATSAPP_BRIDGE_API_KEY     optional shared secret; when set, every REST
                              request needs an X-API-Key or Bearer header

Examples:
  whatsapp-bridge                           # pair (first run) and start
  whatsapp-bridge --addr 127.0.0.1:8081     # use another port
  WHATSAPP_BRIDGE_API_KEY=$(openssl rand -hex 32) whatsapp-bridge
`)
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
