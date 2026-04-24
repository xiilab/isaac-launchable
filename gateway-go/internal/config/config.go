package config

import (
	"fmt"
	"os"
)

type Config struct {
	KitSignalURL   string
	TurnURI        string
	TurnUsername   string
	TurnCredential string
	ListenAddr     string
}

func Load() (*Config, error) {
	cfg := &Config{
		KitSignalURL:   os.Getenv("KIT_SIGNAL_URL"),
		TurnURI:        os.Getenv("TURN_URI"),
		TurnUsername:   os.Getenv("TURN_USERNAME"),
		TurnCredential: os.Getenv("TURN_CREDENTIAL"),
		ListenAddr:     os.Getenv("LISTEN_ADDR"),
	}
	if cfg.KitSignalURL == "" {
		return nil, fmt.Errorf("KIT_SIGNAL_URL required")
	}
	if cfg.TurnURI == "" {
		return nil, fmt.Errorf("TURN_URI required")
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":9000"
	}
	return cfg, nil
}
