// Package upstream wraps the Gateway ↔ Kit PeerConnection.
// Kit advertises host candidates on the pod network (pod IP + ephemeral
// UDP port), which the Gateway reaches trivially over pod loopback.
// No STUN/TURN is needed — NvSt's ignore-iceServers bug is confined to
// Kit's own stack, which never has to leave the pod.
package upstream

import (
	"fmt"
	"sync"

	"github.com/pion/webrtc/v4"
)

// KitPeer is the Gateway-side WebRTC peer that talks to Kit.
type KitPeer struct {
	pc     *webrtc.PeerConnection
	tracks chan *webrtc.TrackRemote
	mu     sync.Mutex
	closed bool
}

// NewKitPeer constructs a PeerConnection that participates in ICE with
// Kit over pod loopback. No external iceServers; all host candidates.
func NewKitPeer() (*KitPeer, error) {
	rtcCfg := webrtc.Configuration{
		// Empty ICEServers: loopback is reachable without STUN/TURN.
		ICEServers:         []webrtc.ICEServer{},
		ICETransportPolicy: webrtc.ICETransportPolicyAll,
	}
	pc, err := webrtc.NewPeerConnection(rtcCfg)
	if err != nil {
		return nil, fmt.Errorf("upstream: new peer: %w", err)
	}
	p := &KitPeer{
		pc:     pc,
		tracks: make(chan *webrtc.TrackRemote, 4),
	}
	pc.OnTrack(func(t *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		select {
		case p.tracks <- t:
		default:
			// Caller is lagging; drop the announcement to avoid blocking
			// the ICE/SRTP goroutine. Subsequent tracks still arrive.
		}
	})
	return p, nil
}

// PC returns the underlying PeerConnection.
func (p *KitPeer) PC() *webrtc.PeerConnection { return p.pc }

// Tracks returns a receive channel for remote tracks arriving from Kit.
func (p *KitPeer) Tracks() <-chan *webrtc.TrackRemote { return p.tracks }

// HandleOffer applies Kit's offer SDP and returns the Gateway's answer
// SDP immediately (trickle mode — candidates flow via OnICECandidate).
// Kit advertises `a=ice-options:trickle`; a non-trickle answer with
// `a=end-of-candidates` was observed to provoke Kit to close the WS.
func (p *KitPeer) HandleOffer(sdp string) (string, error) {
	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdp}
	if err := p.pc.SetRemoteDescription(offer); err != nil {
		return "", fmt.Errorf("upstream: set remote offer: %w", err)
	}
	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		return "", fmt.Errorf("upstream: create answer: %w", err)
	}
	if err := p.pc.SetLocalDescription(answer); err != nil {
		return "", fmt.Errorf("upstream: set local answer: %w", err)
	}
	return answer.SDP, nil
}

// AddCandidate trickles in an ICE candidate from Kit.
func (p *KitPeer) AddCandidate(candidate string, sdpMid string, sdpMLineIndex uint16) error {
	return p.pc.AddICECandidate(webrtc.ICECandidateInit{
		Candidate:     candidate,
		SDPMid:        &sdpMid,
		SDPMLineIndex: &sdpMLineIndex,
	})
}

// OnICECandidate registers a callback for locally discovered candidates
// to forward to Kit via signaling.
func (p *KitPeer) OnICECandidate(cb func(*webrtc.ICECandidate)) {
	p.pc.OnICECandidate(cb)
}

// Close tears down the peer. Idempotent.
func (p *KitPeer) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	close(p.tracks)
	return p.pc.Close()
}
