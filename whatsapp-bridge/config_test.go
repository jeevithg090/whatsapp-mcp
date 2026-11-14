package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"net"
	"strings"
	"testing"
)

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("WHATSAPP_BRIDGE_STORE_DIR", "")
	t.Setenv("WHATSAPP_BRIDGE_ADDR", "")
	t.Setenv("WHATSAPP_BRIDGE_API_KEY", "")
	t.Setenv("WHATSAPP_BRIDGE_LOG_LEVEL", "")
	cfg := LoadConfig()
	if cfg.StoreDir != "store" || cfg.ListenAddr != "127.0.0.1:8080" || cfg.APIKey != "" || cfg.LogLevel != "INFO" {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadConfigFromEnv(t *testing.T) {
	t.Setenv("WHATSAPP_BRIDGE_STORE_DIR", "/data")
	t.Setenv("WHATSAPP_BRIDGE_ADDR", "0.0.0.0:9000")
	t.Setenv("WHATSAPP_BRIDGE_API_KEY", " key ")
	t.Setenv("WHATSAPP_BRIDGE_LOG_LEVEL", "debug")
	cfg := LoadConfig()
	if cfg.StoreDir != "/data" || cfg.ListenAddr != "0.0.0.0:9000" || cfg.APIKey != "key" || cfg.LogLevel != "DEBUG" {
		t.Errorf("unexpected config: %+v", cfg)
	}
}

func TestParseConfigFlagsOverrideEnv(t *testing.T) {
	t.Setenv("WHATSAPP_BRIDGE_ADDR", "127.0.0.1:9000")
	t.Setenv("WHATSAPP_BRIDGE_LOG_LEVEL", "WARN")
	var out bytes.Buffer
	cfg, err := ParseConfig([]string{"--addr", "127.0.0.1:9100", "--log-level", "debug", "--store", "/tmp/s"}, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ListenAddr != "127.0.0.1:9100" || cfg.LogLevel != "DEBUG" || cfg.StoreDir != "/tmp/s" {
		t.Errorf("unexpected config: %+v", cfg)
	}
}

func TestParseConfigHelpAndVersion(t *testing.T) {
	var out bytes.Buffer
	if _, err := ParseConfig([]string{"--help"}, &out); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("want ErrHelp, got %v", err)
	}
	for _, want := range []string{"Usage:", "--addr", "WHATSAPP_BRIDGE_API_KEY", "Linked devices"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help output missing %q", want)
		}
	}
	if _, err := ParseConfig([]string{"--version"}, &out); !errors.Is(err, errVersion) {
		t.Fatalf("want errVersion, got %v", err)
	}
}

func TestParseConfigRejectsBadValues(t *testing.T) {
	cases := map[string][]string{
		"log level":  {"--log-level", "LOUD"},
		"address":    {"--addr", "8080"},
		"port":       {"--addr", "127.0.0.1:99999"},
		"store":      {"--store", " "},
		"positional": {"extra"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseConfig(args, io.Discard); err == nil {
				t.Errorf("expected an error for %v", args)
			}
		})
	}
}

func TestIsLoopback(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:8080": true,
		"localhost:8080": true,
		"[::1]:8080":     true,
		"0.0.0.0:8080":   false,
		":8080":          false,
	} {
		if got := (Config{ListenAddr: addr}).IsLoopback(); got != want {
			t.Errorf("IsLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestListenReportsPortInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	_, err = listen(ln.Addr().String())
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("want an 'already in use' error, got %v", err)
	}
}
