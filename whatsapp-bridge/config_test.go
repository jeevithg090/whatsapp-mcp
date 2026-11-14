package main

import "testing"

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
