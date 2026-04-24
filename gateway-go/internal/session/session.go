// Package session wires the NVST signaling proxy to the upstream
// (Kit) and downstream (browser) Pion PeerConnections. NVST wraps
// WebRTC offer/answer/candidate payloads inside a `peer_msg` envelope
// with a JSON-stringified `msg` field; see internal/nvst for details.
//
// Flow (Kit-initiates-offer pattern, the only one observed on Isaac
// Sim 6.0's omni.kit.livestream.app):
//
//  1. Browser opens WS → nginx → gateway → Kit. Gateway proxies
//     handshake-level frames (ackid/ack/hb/peer_info/headers) verbatim.
//  2. Kit sends peer_info announcing the browser's assigned peer_id.
//     Gateway records it for later envelope construction.
//  3. Kit sends offer wrapped in peer_msg. Gateway intercepts, feeds
//     the SDP to its upstream Pion peer (as answerer), and rewrites the
//     outer envelope's msg field with upstream.answer's SDP — no: we
//     CONSUME Kit's offer, generate a gateway-crafted offer from the
//     downstream peer, and forward that to the browser. Meanwhile we
//     send upstream.answer back to Kit (wrapped in a browser→server
//     peer_msg) so Kit's session progresses.
//  4. Browser sends answer → gateway intercepts → downstream.SetAnswer.
//     Gateway also generates upstream candidates which flow to Kit in
//     client→server peer_msg frames.
//  5. Kit's RTP tracks arrive via upstream.OnTrack → TrackLocalStaticRTP
//     attached to downstream → RTP forwarded packet-by-packet.
package session

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
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

// state tracks signaling identifiers and sequence counters that must
// persist across hook invocations within a single session.
type state struct {
	kitPeerID     int32 // Kit's signaling peer_id (observed in peer_msg.from). Default 1.
	browserPeerID int32 // Browser's signaling peer_id (learned from peer_info or first client peer_msg).
	ackCounter    int32 // monotonic counter for ackid on gateway-originated server→client frames.
}

// Factory returns a proxy.SessionFactory that builds a full SFU session
// per browser connection.
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
	st := &state{kitPeerID: 1} // Kit default id = 1, overwritten once observed

	// Upstream Pion peer does NOT trickle candidates to Kit. HandleOffer
	// waits for GatheringCompletePromise before returning the answer,
	// so all candidates ride inline in the SDP. Log but don't send —
	// forwarding them again via peer_msg would arrive out-of-order.
	kitPeer.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			log.Printf("[session] upstream ICE gather complete (nil candidate)")
			return
		}
		log.Printf("[session] upstream local candidate (inline only): %q", c.ToJSON().Candidate)
	})

	// -- Trace upstream connection state transitions.
	kitPeer.PC().OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		log.Printf("[session] upstream connection state: %s", s)
	})

	// -- Forward locally-gathered downstream ICE candidates to browser
	// via server→client peer_msg (with fresh ackid).
	browserPeer.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			log.Printf("[session] downstream ICE gather complete (nil candidate)")
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
		inner := &nvst.PeerMsgInner{
			Type:          "candidate",
			Candidate:     init.Candidate,
			SDPMid:        sdpMid,
			SDPMLineIndex: idx,
		}
		ackid := int(atomic.AddInt32(&st.ackCounter, 1))
		log.Printf("[session] gw→browser peer_msg CANDIDATE (ackid=%d) %q", ackid, init.Candidate)
		msg, err := nvst.NewPeerMsgToBrowser(ackid, int(atomic.LoadInt32(&st.kitPeerID)), inner)
		if err != nil {
			log.Printf("[session] downstream→browser cand build: %v", err)
			return
		}
		raw, _ := msg.Encode()
		if err := ps.Send(raw); err != nil {
			log.Printf("[session] downstream→browser cand send: %v", err)
		}
	})

	// -- Trace downstream connection state transitions.
	browserPeer.PC().OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		log.Printf("[session] downstream connection state: %s", s)
	})

	// -- Upstream track arrival → attach matching local track to downstream.
	go func() {
		for remote := range kitPeer.Tracks() {
			if err := attachAndForward(ctx, remote, browserPeer); err != nil {
				log.Printf("[session] attach/forward track %s: %v", remote.ID(), err)
			}
		}
	}()

	// -- Hook: messages from Kit → gateway
	ps.OnKitMessage = func(raw []byte) (bool, error) {
		m, err := nvst.Parse(raw)
		if err != nil {
			log.Printf("[session] kit→gw non-JSON %d bytes: %q", len(raw), firstN(raw, 80))
			return true, nil
		}
		// Kit broadcasts multiple peer_info frames: one for the browser
		// (name "peer-NNN…") and one for itself ("OneSdkServer-…"). We
		// must distinguish by name — overwriting browserPeerID with
		// Kit's id (usually 1) breaks outgoing candidate routing.
		if pi, ok := m.PeerInfo(); ok {
			name, _ := pi["name"].(string)
			idVal := 0
			if idF, ok := pi["id"].(float64); ok {
				idVal = int(idF)
			}
			switch {
			case strings.HasPrefix(name, "OneSdkServer"):
				atomic.StoreInt32(&st.kitPeerID, int32(idVal))
				log.Printf("[session] learned Kit peer_id = %d from peer_info (%s)", idVal, name)
			case strings.HasPrefix(name, "peer-"):
				atomic.StoreInt32(&st.browserPeerID, int32(idVal))
				log.Printf("[session] learned browser peer_id = %d from peer_info (%s)", idVal, name)
			default:
				log.Printf("[session] peer_info id=%d name=%q (unknown role)", idVal, name)
			}
			return true, nil // always forward peer_info to browser unchanged
		}
		outer, ok := m.AsPeerMsg()
		if !ok {
			// ack / ackid-only / hb / headers / unknown — passthrough verbatim.
			return true, nil
		}
		if outer.Inner == nil {
			// peer_msg without WebRTC payload — passthrough.
			return true, nil
		}
		atomic.StoreInt32(&st.kitPeerID, int32(outer.From))

		// When we intercept a frame carrying ackid, the browser will
		// never see it and thus never send {"ack":N}. We must ack Kit
		// ourselves or Kit drops the WS as "unacknowledged".
		ackKitIfNeeded := func() {
			ackid, ok := m.Ackid()
			if !ok {
				log.Printf("[session] intercepted frame has no ackid; raw=%q", firstN(raw, 120))
				return
			}
			ackMsg := nvst.NewAck(ackid)
			rawAck, _ := ackMsg.Encode()
			if err := ps.SendToKit(rawAck); err != nil {
				log.Printf("[session] auto-ack %d to kit: %v", ackid, err)
			} else {
				log.Printf("[session] gw→kit auto-ack %d", ackid)
			}
		}

		switch outer.Inner.Type {
		case "offer":
			log.Printf("[session] kit→gw peer_msg OFFER (from=%d, sdp=%d bytes)", outer.From, len(outer.Inner.SDP))
			ackKitIfNeeded()
			// Handle off the pump goroutine: HandleOffer blocks on
			// GatheringCompletePromise and would otherwise prevent the
			// pump from reading Kit's subsequent trickle candidates or
			// heartbeats, which causes Kit to time out and drop the WS.
			sdp := outer.Inner.SDP
			go func() {
				if err := handleKitOffer(ctx, kitPeer, browserPeer, ps, st, sdp); err != nil {
					log.Printf("[session] handleKitOffer: %v", err)
				}
			}()
			return false, nil
		case "answer":
			log.Printf("[session] kit→gw peer_msg ANSWER (from=%d, sdp=%d bytes)", outer.From, len(outer.Inner.SDP))
			ackKitIfNeeded()
			if err := kitPeer.PC().SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeAnswer,
				SDP:  outer.Inner.SDP,
			}); err != nil {
				log.Printf("[session] upstream set answer: %v", err)
			}
			return false, nil
		case "candidate":
			log.Printf("[session] kit→gw peer_msg CANDIDATE %q", outer.Inner.Candidate)
			ackKitIfNeeded()
			if outer.Inner.Candidate != "" {
				if err := kitPeer.AddCandidate(outer.Inner.Candidate, outer.Inner.SDPMid, uint16(outer.Inner.SDPMLineIndex)); err != nil {
					log.Printf("[session] upstream add candidate: %v", err)
				}
			}
			return false, nil
		default:
			log.Printf("[session] kit→gw peer_msg unknown inner type=%q, passthrough", outer.Inner.Type)
			return true, nil
		}
	}

	// -- Hook: messages from browser → gateway
	ps.OnClientMessage = func(raw []byte) (bool, error) {
		m, err := nvst.Parse(raw)
		if err != nil {
			log.Printf("[session] browser→gw non-JSON %d bytes: %q", len(raw), firstN(raw, 80))
			return true, nil
		}
		// Acks from browser are for gateway-originated server→client
		// frames. Kit never saw those, so do NOT forward acks to Kit —
		// consume them here.
		if m.IsAck() {
			return false, nil
		}
		outer, ok := m.AsPeerMsg()
		if !ok {
			return true, nil
		}
		if outer.Inner == nil {
			return true, nil
		}
		atomic.StoreInt32(&st.browserPeerID, int32(outer.From))
		if outer.HasTo {
			atomic.StoreInt32(&st.kitPeerID, int32(outer.To))
		}

		switch outer.Inner.Type {
		case "offer":
			log.Printf("[session] browser→gw peer_msg OFFER (from=%d, sdp=%d bytes) — browser-initiates path", outer.From, len(outer.Inner.SDP))
			if err := browserPeer.PC().SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeOffer,
				SDP:  outer.Inner.SDP,
			}); err != nil {
				log.Printf("[session] downstream set remote offer: %v", err)
				return false, nil
			}
			go bridgeBrowserOffer(ctx, kitPeer, browserPeer, ps, st)
			return false, nil
		case "answer":
			log.Printf("[session] browser→gw peer_msg ANSWER (from=%d, sdp=%d bytes)", outer.From, len(outer.Inner.SDP))
			if err := browserPeer.SetAnswer(outer.Inner.SDP); err != nil {
				log.Printf("[session] downstream set answer: %v", err)
			}
			return false, nil
		case "candidate":
			log.Printf("[session] browser→gw peer_msg CANDIDATE %q", outer.Inner.Candidate)
			if outer.Inner.Candidate != "" {
				if err := browserPeer.AddCandidate(outer.Inner.Candidate, outer.Inner.SDPMid, uint16(outer.Inner.SDPMLineIndex)); err != nil {
					log.Printf("[session] downstream add candidate: %v", err)
				}
			}
			return false, nil
		default:
			log.Printf("[session] browser→gw peer_msg unknown inner type=%q, passthrough", outer.Inner.Type)
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

// handleKitOffer feeds Kit's offer SDP into the upstream Pion peer,
// sends our answer back to Kit (wrapped in a browser→server peer_msg),
// and schedules a downstream offer to the browser.
func handleKitOffer(ctx context.Context, kp *upstream.KitPeer, bp *downstream.BrowserPeer, ps *proxy.Session, st *state, sdp string) error {
	log.Printf("[session] kit offer FULL:\n%s", sdp)
	answerSDP, err := kp.HandleOffer(sdp)
	if err != nil {
		return fmt.Errorf("upstream HandleOffer: %w", err)
	}
	log.Printf("[session] gw answer FULL:\n%s", answerSDP)

	answerInner := &nvst.PeerMsgInner{Type: "answer", SDP: answerSDP}
	answerMsg, err := nvst.NewPeerMsgToKit(
		int(atomic.LoadInt32(&st.browserPeerID)),
		int(atomic.LoadInt32(&st.kitPeerID)),
		answerInner,
	)
	if err != nil {
		return fmt.Errorf("build answer peer_msg: %w", err)
	}
	answerRaw, _ := answerMsg.Encode()
	// Log first 500 bytes of the literal frame payload to verify JSON
	// shape matches Kit's expected envelope.
	log.Printf("[session] gw→kit answer frame (first 500B of %d):\n%s", len(answerRaw), firstSDPN(string(answerRaw), 500))
	if err := ps.SendToKit(answerRaw); err != nil {
		return fmt.Errorf("send answer to kit: %w", err)
	}
	log.Printf("[session] gw→kit peer_msg ANSWER sent (sdp=%d bytes)", len(answerSDP))

	go buildBrowserOfferAfterUpstream(ctx, kp, bp, ps, st)
	return nil
}

func firstSDPN(sdp string, n int) string {
	if len(sdp) <= n {
		return sdp
	}
	return sdp[:n] + "…"
}

// buildBrowserOfferAfterUpstream waits for upstream ICE to reach
// connected state (meaning tracks are flowing), then constructs a
// downstream offer and sends it to the browser.
func buildBrowserOfferAfterUpstream(ctx context.Context, kp *upstream.KitPeer, bp *downstream.BrowserPeer, ps *proxy.Session, st *state) {
	connected := make(chan struct{})
	var once sync.Once
	kp.PC().OnICEConnectionStateChange(func(s webrtc.ICEConnectionState) {
		log.Printf("[session] upstream ICE state: %s", s)
		if s == webrtc.ICEConnectionStateConnected || s == webrtc.ICEConnectionStateCompleted {
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
	inner := &nvst.PeerMsgInner{Type: "offer", SDP: offerSDP}
	ackid := int(atomic.AddInt32(&st.ackCounter, 1))
	msg, err := nvst.NewPeerMsgToBrowser(ackid, int(atomic.LoadInt32(&st.kitPeerID)), inner)
	if err != nil {
		log.Printf("[session] build offer peer_msg: %v", err)
		return
	}
	raw, _ := msg.Encode()
	if err := ps.Send(raw); err != nil {
		log.Printf("[session] send offer to browser: %v", err)
		return
	}
	log.Printf("[session] gw→browser peer_msg OFFER sent (ackid=%d, sdp=%d bytes)", ackid, len(offerSDP))
}

// bridgeBrowserOffer handles the browser-initiates-offer pattern.
// (Not observed on Isaac Sim 6.0 but kept for defensive coverage.)
func bridgeBrowserOffer(ctx context.Context, kp *upstream.KitPeer, bp *downstream.BrowserPeer, ps *proxy.Session, st *state) {
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
	inner := &nvst.PeerMsgInner{Type: "offer", SDP: offer.SDP}
	msg, err := nvst.NewPeerMsgToKit(
		int(atomic.LoadInt32(&st.browserPeerID)),
		int(atomic.LoadInt32(&st.kitPeerID)),
		inner,
	)
	if err != nil {
		log.Printf("[session] build offer peer_msg to kit: %v", err)
		return
	}
	raw, _ := msg.Encode()
	if err := ps.SendToKit(raw); err != nil {
		log.Printf("[session] send offer to kit: %v", err)
		return
	}

	connected := make(chan struct{})
	var once sync.Once
	kp.PC().OnICEConnectionStateChange(func(s webrtc.ICEConnectionState) {
		if s == webrtc.ICEConnectionStateConnected || s == webrtc.ICEConnectionStateCompleted {
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
	answerInner := &nvst.PeerMsgInner{Type: "answer", SDP: answer.SDP}
	ackid := int(atomic.AddInt32(&st.ackCounter, 1))
	answerMsg, err := nvst.NewPeerMsgToBrowser(ackid, int(atomic.LoadInt32(&st.kitPeerID)), answerInner)
	if err != nil {
		log.Printf("[session] build answer peer_msg to browser: %v", err)
		return
	}
	answerRaw, _ := answerMsg.Encode()
	if err := ps.Send(answerRaw); err != nil {
		log.Printf("[session] send answer to browser: %v", err)
	}
}

// attachAndForward creates a TrackLocalStaticRTP matching the remote's
// codec and starts the RTP pump.
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
	go drainRTCP(ctx, sender)
	go func() {
		count, err := relay.Forward(ctx, &remoteRTPReader{t: remote}, local)
		log.Printf("[session] track %s: forwarded %d packets, err=%v", remote.ID(), count, err)
	}()
	return nil
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

type remoteRTPReader struct {
	t *webrtc.TrackRemote
}

func (r *remoteRTPReader) ReadRTP() (*rtp.Packet, error) {
	pkt, _, err := r.t.ReadRTP()
	return pkt, err
}

func firstN(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}
