package session

import (
	"testing"

	"github.com/xiilab/isaac-launchable/gateway-go/internal/proxy"
)

func TestFactory_ReturnsNonNil(t *testing.T) {
	f := Factory(Config{
		TurnURI:        "turn:10.61.3.74:3478",
		TurnUsername:   "isaac",
		TurnCredential: "secret",
	})
	if f == nil {
		t.Fatal("Factory returned nil")
	}
	// Don't actually invoke — buildSession dials a real WS which
	// requires a live peer. Smoke-testing the constructor is enough;
	// per-peer behaviour is covered in the upstream/downstream packages.
	var _ proxy.SessionFactory = f
}
