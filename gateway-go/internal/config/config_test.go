package config

import (
	"testing"
)

func TestLoad_AllEnv(t *testing.T) {
	t.Setenv("KIT_SIGNAL_URL", "ws://127.0.0.1:49100")
	t.Setenv("TURN_URI", "turn:10.61.3.74:3478")
	t.Setenv("TURN_USERNAME", "isaac")
	t.Setenv("TURN_CREDENTIAL", "secret")
	t.Setenv("LISTEN_ADDR", ":9000")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.KitSignalURL != "ws://127.0.0.1:49100" {
		t.Errorf("KitSignalURL = %q", cfg.KitSignalURL)
	}
	if cfg.TurnURI != "turn:10.61.3.74:3478" {
		t.Errorf("TurnURI = %q", cfg.TurnURI)
	}
	if cfg.TurnUsername != "isaac" {
		t.Errorf("TurnUsername = %q", cfg.TurnUsername)
	}
	if cfg.TurnCredential != "secret" {
		t.Errorf("TurnCredential = %q", cfg.TurnCredential)
	}
	if cfg.ListenAddr != ":9000" {
		t.Errorf("ListenAddr = %q", cfg.ListenAddr)
	}
}

func TestLoad_MissingKitSignal(t *testing.T) {
	t.Setenv("KIT_SIGNAL_URL", "")
	t.Setenv("TURN_URI", "turn:10.61.3.74:3478")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for missing KIT_SIGNAL_URL")
	}
}

func TestLoad_MissingTurnURI(t *testing.T) {
	t.Setenv("KIT_SIGNAL_URL", "ws://127.0.0.1:49100")
	t.Setenv("TURN_URI", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for missing TURN_URI")
	}
}

func TestLoad_DefaultListenAddr(t *testing.T) {
	t.Setenv("KIT_SIGNAL_URL", "ws://127.0.0.1:49100")
	t.Setenv("TURN_URI", "turn:10.61.3.74:3478")
	t.Setenv("TURN_USERNAME", "isaac")
	t.Setenv("TURN_CREDENTIAL", "secret")
	t.Setenv("LISTEN_ADDR", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddr != ":9000" {
		t.Errorf("ListenAddr default = %q, want :9000", cfg.ListenAddr)
	}
}
