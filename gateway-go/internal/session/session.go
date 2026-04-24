// Package session wires the signaling proxy to the upstream/downstream
// Pion peers. It is the top layer of the SFU: incoming NVST offer/answer/
// candidate frames are routed into the correct PeerConnection, and
// media tracks received from Kit are forwarded to the browser.
//
// Flow (assuming Kit-initiates-offer pattern; see comments):
//
//   1. Browser → Gateway WS: NVST handshake frames (config, peerId, …)
//      pass through to Kit verbatim.
//   2. Kit → Gateway WS: {"type":"offer","sdp":...}
//      Gateway's upstream peer consumes the offer and produces an answer;
//      the answer is sent back to Kit. Gateway does NOT forward the
//      original offer to the browser.
//   3. Upstream peer receives remote tracks → relay forwards RTP into
//      a matching downstream TrackLocalStaticRTP.
//   4. Once all expected tracks have been attached, Gateway's downstream
//      peer creates its own offer (with TURN-relay-only candidates) and
//      sends it to the browser as {"type":"offer",...}.
//   5. Browser → Gateway WS: {"type":"answer",...} is applied to the
//      downstream peer; not forwarded to Kit.
//   6. ICE candidates: Kit-origin → upstream; browser-origin → downstream.
//      Locally-discovered candidates go to the opposite end via its
//      respective signaling direction.
package session

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"

	"github.com/xiilab/isaac-launchable/gateway-go/internal/downstream"
	"github.com/xiilab/isaac-launchable/gateway-go/internal/nvst"
	"github.com/xiilab/isaac-launchable/gateway-go/internal/proxy"
	"github.com/xiilab/isaac-launchable/gateway-go/internal/relay"
	"github.com/xiilab/isaac-launchable/gateway-go/internal/upstream"
)

// Config carries runtime parameters for the session factory.
type Config struct {
	TurnURI        string
	TurnUsername   string
	TurnCredential string
}

// Factory returns a proxy.SessionFactory that builds a full SFU session
// for every new browser connection.
func Factory(cfg Config) proxy.SessionFactory {
	return func(ps *proxy.Session) error {
		return buildSession(ps, cfg)
	}
}

func buildSession(ps *proxy.Session, cfg Config) error {
	kitPeer, err := upstream.NewKitPeer()
	if err != nil {
		return fmt.Errorf("session: upstream: %w", err)
	}
	browserPeer, err := downstream.NewBrowserPeer(downstream.Config{
		TurnURI:        cfg.TurnURI,
		TurnUsername:   cfg.TurnUsername,
		TurnCredential: cfg.TurnCredential,
	})
	if err != nil {
		_ = kitPeer.Close()
		return fmt.Errorf("session: downstream: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// -- Kit-origin ICE candidates → we already have upstream peer, but we
	// also need to push OUR local candidates back to Kit.
	kitPeer.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		init := c.ToJSON()
		sdpMid := ""
		if init.SDPMid != nil {
			sdpMid = *init.SDPMid
		}
		idx := 0
		if init.SDPMLineIndex != nil {
			idx = int(*init.SDPMLineIndex)
		}
		msg := nvst.NewCandidate(init.Candidate, sdpMid, idx)
		raw, _ := msg.Encode()
		if err := ps.SendToKit(raw); err != nil {
			log.Printf("[session] kit candidate forward: %v", err)
		}
	})

	// -- Locally-discovered candidates on downstream → forward to browser.
	browserPeer.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		init := c.ToJSON()
		sdpMid := ""
		if init.SDPMid != nil {
			sdpMid = *init.SDPMid
		}
		idx := 0
		if init.SDPMLineIndex != nil {
			idx = int(*init.SDPMLineIndex)
		}
		msg := nvst.NewCandidate(init.Candidate, sdpMid, idx)
		raw, _ := msg.Encode()
		if err := ps.Send(raw); err != nil {
			log.Printf("[session] browser candidate forward: %v", err)
		}
	})

	// -- Upstream track arrival → attach matching local track to downstream
	// and start RTP forwarding.
	go func() {
		for remote := range kitPeer.Tracks() {
			if err := attachAndForward(ctx, remote, browserPeer); err != nil {
				log.Printf("[session] attach/forward track %s: %v", remote.ID(), err)
			}
		}
	}()

	// -- Hook: messages from Kit → gateway
	ps.OnKitMessage = func(raw []byte) (bool, error) {
		msg, err := nvst.Parse(raw)
		if err != nil {
			log.Printf("[session] kit→gw non-JSON %d bytes: %q", len(raw), firstN(raw, 120))
			return true, nil
		}
		switch msg.Kind() {
		case nvst.KindOffer:
			log.Printf("[session] kit→gw OFFER sdp=%d bytes", len(msg.SDP()))
			answerSDP, err := kitPeer.HandleOffer(msg.SDP())
			if err != nil {
				return false, fmt.Errorf("upstream handle offer: %w", err)
			}
			answerMsg := nvst.NewAnswer(answerSDP)
			answerRaw, _ := answerMsg.Encode()
			if err := ps.SendToKit(answerRaw); err != nil {
				return false, fmt.Errorf("send kit answer: %w", err)
			}
			log.Printf("[session] gw→kit ANSWER sent (%d bytes)", len(answerSDP))
			// After tracks arrive, build downstream offer for browser.
			go buildBrowserOffer(ctx, browserPeer, ps, kitPeer)
			return false, nil
		case nvst.KindAnswer:
			log.Printf("[session] kit→gw ANSWER sdp=%d bytes (browser-initiates pattern)", len(msg.SDP()))
			if err := kitPeer.PC().SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeAnswer, SDP: msg.SDP(),
			}); err != nil {
				log.Printf("[session] upstream set answer: %v", err)
			}
			return false, nil
		case nvst.KindCandidate:
			log.Printf("[session] kit→gw CANDIDATE %q", msg.Candidate())
			idx := uint16(msg.SdpMLineIndex())
			if err := kitPeer.AddCandidate(msg.Candidate(), msg.SdpMid(), idx); err != nil {
				log.Printf("[session] kit candidate add: %v", err)
			}
			return false, nil
		default:
			log.Printf("[session] kit→gw passthrough type=%q raw=%q", msg.Type(), firstN(raw, 120))
			return true, nil
		}
	}

	// -- Hook: messages from browser → gateway
	ps.OnClientMessage = func(raw []byte) (bool, error) {
		msg, err := nvst.Parse(raw)
		if err != nil {
			log.Printf("[session] gw→kit non-JSON %d bytes: %q", len(raw), firstN(raw, 120))
			return true, nil
		}
		switch msg.Kind() {
		case nvst.KindOffer:
			log.Printf("[session] browser→gw OFFER sdp=%d bytes (browser-initiates pattern)", len(msg.SDP()))
			if err := browserPeer.PC().SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeOffer, SDP: msg.SDP(),
			}); err != nil {
				return false, fmt.Errorf("downstream set remote offer: %w", err)
			}
			// Ask Kit for the same media via upstream peer's offer. Kit
			// answers with tracks, then we add them to downstream and
			// answer the browser.
			go bridgeBrowserOffer(ctx, kitPeer, browserPeer, ps)
			return false, nil
		case nvst.KindAnswer:
			log.Printf("[session] browser→gw ANSWER sdp=%d bytes", len(msg.SDP()))
			if err := browserPeer.SetAnswer(msg.SDP()); err != nil {
				return false, fmt.Errorf("downstream set answer: %w", err)
			}
			return false, nil
		case nvst.KindCandidate:
			log.Printf("[session] browser→gw CANDIDATE %q", msg.Candidate())
			idx := uint16(msg.SdpMLineIndex())
			if err := browserPeer.AddCandidate(msg.Candidate(), msg.SdpMid(), idx); err != nil {
				log.Printf("[session] browser candidate add: %v", err)
			}
			return false, nil
		default:
			log.Printf("[session] browser→kit passthrough type=%q raw=%q", msg.Type(), firstN(raw, 120))
			return true, nil
		}
	}

	ps.OnClose = func() {
		cancel()
		_ = browserPeer.Close()
		_ = kitPeer.Close()
	}

	return nil
}

// attachAndForward creates a TrackLocalStaticRTP matching the remote's
// codec, adds it to the downstream peer, and spawns a goroutine that
// forwards RTP packets.
func attachAndForward(ctx context.Context, remote *webrtc.TrackRemote, bp *downstream.BrowserPeer) error {
	codec := remote.Codec()
	local, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{
			MimeType:     codec.MimeType,
			ClockRate:    codec.ClockRate,
			Channels:     codec.Channels,
			SDPFmtpLine:  codec.SDPFmtpLine,
			RTCPFeedback: codec.RTCPFeedback,
		},
		remote.ID(),
		remote.StreamID(),
	)
	if err != nil {
		return fmt.Errorf("new local track: %w", err)
	}
	sender, err := bp.AddTrack(local)
	if err != nil {
		return fmt.Errorf("add track to downstream: %w", err)
	}
	// Drain RTCP from the sender to prevent buffer backup.
	go drainRTCP(ctx, sender)

	go func() {
		count, err := relay.Forward(ctx, &remoteRTPReader{t: remote}, local)
		log.Printf("[session] track %s: forwarded %d packets, err=%v", remote.ID(), count, err)
	}()
	return nil
}

// buildBrowserOffer waits for the upstream peer's ICE connection to
// reach "connected" state (tracks should be attached by then via OnTrack
// and the attachAndForward goroutine), debounces briefly, then generates
// a downstream offer and sends it to the browser.
//
// This is best-effort: if the upstream never connects within the timeout,
// we attempt the offer anyway so the browser sees something. Subsequent
// AddTrack calls trigger OnNegotiationNeeded on the downstream peer and
// re-offer via the same path.
func buildBrowserOffer(ctx context.Context, bp *downstream.BrowserPeer, ps *proxy.Session, kp *upstream.KitPeer) {
	connected := make(chan struct{})
	var once sync.Once
	kp.PC().OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("[session] upstream ICE state: %s", state)
		if state == webrtc.ICEConnectionStateConnected || state == webrtc.ICEConnectionStateCompleted {
			once.Do(func() { close(connected) })
		}
	})

	select {
	case <-ctx.Done():
		return
	case <-connected:
		log.Printf("[session] upstream connected; scheduling downstream offer")
	case <-time.After(20 * time.Second):
		log.Printf("[session] timeout waiting for upstream connected; sending offer anyway")
	}

	// Debounce so any burst of OnTrack calls lands before we build SDP.
	select {
	case <-ctx.Done():
		return
	case <-time.After(300 * time.Millisecond):
	}

	offerSDP, err := bp.CreateOffer()
	if err != nil {
		log.Printf("[session] downstream create offer: %v", err)
		return
	}
	offerMsg := nvst.NewOffer(offerSDP)
	raw, _ := offerMsg.Encode()
	if err := ps.Send(raw); err != nil {
		log.Printf("[session] downstream offer send: %v", err)
		return
	}
	log.Printf("[session] downstream offer sent to browser (%d bytes)", len(offerSDP))
}

func drainRTCP(ctx context.Context, sender *webrtc.RTPSender) {
	buf := make([]byte, 1500)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if _, _, err := sender.Read(buf); err != nil {
			return
		}
	}
}

// bridgeBrowserOffer handles the browser-initiates-offer pattern. The
// browser's offer is already applied to downstream.SetRemoteDescription.
// We now need to obtain tracks from Kit before we can answer the browser.
//
//  1. upstream.PC().CreateOffer() → send to Kit via signaling
//  2. Kit's answer arrives via OnKitMessage KindAnswer → upstream.SetRemoteDescription
//  3. OnTrack fires → tracks added to downstream via attachAndForward
//  4. Wait for debounce; create downstream answer → send to browser
func bridgeBrowserOffer(ctx context.Context, kp *upstream.KitPeer, bp *downstream.BrowserPeer, ps *proxy.Session) {
	// Add recvonly transceivers so Kit knows we want tracks.
	if _, err := kp.PC().AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		log.Printf("[session] upstream add video transceiver: %v", err)
	}
	if _, err := kp.PC().AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		log.Printf("[session] upstream add audio transceiver: %v", err)
	}

	offer, err := kp.PC().CreateOffer(nil)
	if err != nil {
		log.Printf("[session] upstream create offer: %v", err)
		return
	}
	if err := kp.PC().SetLocalDescription(offer); err != nil {
		log.Printf("[session] upstream set local offer: %v", err)
		return
	}

	offerMsg := nvst.NewOffer(offer.SDP)
	raw, _ := offerMsg.Encode()
	if err := ps.SendToKit(raw); err != nil {
		log.Printf("[session] gw→kit OFFER send: %v", err)
		return
	}
	log.Printf("[session] gw→kit OFFER sent (%d bytes)", len(offer.SDP))

	// Wait for upstream connected state + debounce, then answer browser.
	connected := make(chan struct{})
	var once sync.Once
	kp.PC().OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("[session] upstream ICE state: %s", state)
		if state == webrtc.ICEConnectionStateConnected || state == webrtc.ICEConnectionStateCompleted {
			once.Do(func() { close(connected) })
		}
	})
	select {
	case <-ctx.Done():
		return
	case <-connected:
	case <-time.After(20 * time.Second):
		log.Printf("[session] upstream never connected; answering browser anyway")
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(300 * time.Millisecond):
	}

	answer, err := bp.PC().CreateAnswer(nil)
	if err != nil {
		log.Printf("[session] downstream create answer: %v", err)
		return
	}
	if err := bp.PC().SetLocalDescription(answer); err != nil {
		log.Printf("[session] downstream set local answer: %v", err)
		return
	}
	answerMsg := nvst.NewAnswer(answer.SDP)
	answerRaw, _ := answerMsg.Encode()
	if err := ps.Send(answerRaw); err != nil {
		log.Printf("[session] gw→browser ANSWER send: %v", err)
		return
	}
	log.Printf("[session] gw→browser ANSWER sent (%d bytes)", len(answer.SDP))
}

// firstN returns the first n bytes of b, useful for truncated log lines.
func firstN(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}

// remoteRTPReader adapts *webrtc.TrackRemote to relay.RTPSource by
// discarding the second (interceptor attributes) return value from
// ReadRTP.
type remoteRTPReader struct {
	t *webrtc.TrackRemote
}

func (r *remoteRTPReader) ReadRTP() (*rtp.Packet, error) {
	pkt, _, err := r.t.ReadRTP()
	return pkt, err
}
