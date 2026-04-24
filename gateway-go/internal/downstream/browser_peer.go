// Package downstream wraps the Gateway ↔ Browser PeerConnection.
// The browser side is forced to use TURN relay because Kit's iceServers
// are ignored (NvSt bug) and the pod network has no external UDP path;
// relaying media through coturn is the only reliable route.
package downstream

import (
	"fmt"
	"sync"

	"github.com/pion/webrtc/v4"
)

// Config captures TURN credentials for the downstream peer.
type Config struct {
	TurnURI        string
	TurnUsername   string
	TurnCredential string
}

// BrowserPeer is the Gateway-side WebRTC peer that talks to the browser.
type BrowserPeer struct {
	pc     *webrtc.PeerConnection
	mu     sync.Mutex
	closed bool
}

// NewBrowserPeer constructs a PeerConnection with TURN-only ICE policy.
func NewBrowserPeer(cfg Config) (*BrowserPeer, error) {
	if cfg.TurnURI == "" {
		return nil, fmt.Errorf("downstream: TurnURI required")
	}
	rtcCfg := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{{
			URLs:       []string{cfg.TurnURI},
			Username:   cfg.TurnUsername,
			Credential: cfg.TurnCredential,
		}},
		ICETransportPolicy: webrtc.ICETransportPolicyRelay,
	}
	pc, err := webrtc.NewPeerConnection(rtcCfg)
	if err != nil {
		return nil, fmt.Errorf("downstream: new peer: %w", err)
	}
	return &BrowserPeer{pc: pc}, nil
}

// PC returns the underlying PeerConnection for advanced wiring.
func (p *BrowserPeer) PC() *webrtc.PeerConnection { return p.pc }

// AddTrack attaches a TrackLocal (typically TrackLocalStaticRTP forwarded
// from the upstream peer) and returns the resulting RTPSender.
func (p *BrowserPeer) AddTrack(t webrtc.TrackLocal) (*webrtc.RTPSender, error) {
	return p.pc.AddTrack(t)
}

// CreateOffer generates an SDP offer for the browser with candidates
// gathered inline (non-trickle) to match how NVST-library-backed peers
// typically publish offers.
func (p *BrowserPeer) CreateOffer() (string, error) {
	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		return "", fmt.Errorf("downstream: create offer: %w", err)
	}
	gatherComplete := webrtc.GatheringCompletePromise(p.pc)
	if err := p.pc.SetLocalDescription(offer); err != nil {
		return "", fmt.Errorf("downstream: set local offer: %w", err)
	}
	<-gatherComplete
	final := p.pc.LocalDescription()
	if final == nil {
		return offer.SDP, nil
	}
	return final.SDP, nil
}

// SetAnswer applies the browser's SDP answer.
func (p *BrowserPeer) SetAnswer(sdp string) error {
	return p.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  sdp,
	})
}

// AddCandidate trickles in an ICE candidate from the browser.
func (p *BrowserPeer) AddCandidate(candidate string, sdpMid string, sdpMLineIndex uint16) error {
	init := webrtc.ICECandidateInit{
		Candidate:     candidate,
		SDPMid:        &sdpMid,
		SDPMLineIndex: &sdpMLineIndex,
	}
	return p.pc.AddICECandidate(init)
}

// OnICECandidate registers a callback for locally discovered candidates
// which must be forwarded to the browser via signaling.
func (p *BrowserPeer) OnICECandidate(cb func(*webrtc.ICECandidate)) {
	p.pc.OnICECandidate(cb)
}

// Close tears down the peer. Idempotent.
func (p *BrowserPeer) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	return p.pc.Close()
}
