package upstream

import (
	"testing"

	"github.com/pion/webrtc/v4"
)

func TestNewKitPeer_NoICEServers(t *testing.T) {
	p, err := NewKitPeer()
	if err != nil {
		t.Fatalf("NewKitPeer: %v", err)
	}
	defer p.Close()

	got := p.pc.GetConfiguration()
	if len(got.ICEServers) != 0 {
		t.Errorf("ICEServers len = %d, want 0 (loopback, no STUN/TURN)", len(got.ICEServers))
	}
	if got.ICETransportPolicy != webrtc.ICETransportPolicyAll {
		t.Errorf("ICETransportPolicy = %v, want all (host candidates needed for loopback)", got.ICETransportPolicy)
	}
}

func TestKitPeer_CloseIdempotent(t *testing.T) {
	p, err := NewKitPeer()
	if err != nil {
		t.Fatalf("NewKitPeer: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestKitPeer_TracksChannelExists(t *testing.T) {
	p, err := NewKitPeer()
	if err != nil {
		t.Fatalf("NewKitPeer: %v", err)
	}
	defer p.Close()

	ch := p.Tracks()
	if ch == nil {
		t.Fatal("Tracks() returned nil channel")
	}
}
