package downstream

import (
	"testing"

	"github.com/pion/webrtc/v4"
)

func TestNewBrowserPeer_EnforcesRelayPolicy(t *testing.T) {
	cfg := Config{
		TurnURI:        "turn:10.61.3.74:3478",
		TurnUsername:   "isaac",
		TurnCredential: "secret",
	}
	p, err := NewBrowserPeer(cfg)
	if err != nil {
		t.Fatalf("NewBrowserPeer: %v", err)
	}
	defer p.Close()

	got := p.pc.GetConfiguration()
	if got.ICETransportPolicy != webrtc.ICETransportPolicyRelay {
		t.Errorf("ICETransportPolicy = %v, want relay", got.ICETransportPolicy)
	}
	if len(got.ICEServers) != 1 {
		t.Fatalf("ICEServers len = %d, want 1", len(got.ICEServers))
	}
	if got.ICEServers[0].URLs[0] != "turn:10.61.3.74:3478" {
		t.Errorf("ICEServer URL = %q", got.ICEServers[0].URLs[0])
	}
	if got.ICEServers[0].Username != "isaac" {
		t.Errorf("Username = %q", got.ICEServers[0].Username)
	}
	if cred, ok := got.ICEServers[0].Credential.(string); !ok || cred != "secret" {
		t.Errorf("Credential = %v (type %T)", got.ICEServers[0].Credential, got.ICEServers[0].Credential)
	}
}

func TestNewBrowserPeer_MissingTurnURI(t *testing.T) {
	cfg := Config{TurnUsername: "isaac", TurnCredential: "secret"}
	if _, err := NewBrowserPeer(cfg); err == nil {
		t.Fatal("expected error when TurnURI missing")
	}
}

func TestBrowserPeer_CloseIdempotent(t *testing.T) {
	p, err := NewBrowserPeer(Config{
		TurnURI:        "turn:10.61.3.74:3478",
		TurnUsername:   "isaac",
		TurnCredential: "secret",
	})
	if err != nil {
		t.Fatalf("NewBrowserPeer: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}
