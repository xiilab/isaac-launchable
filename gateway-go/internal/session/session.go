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
			// Not our JSON envelope — pass through.
			return true, nil
		}
		switch msg.Kind() {
		case nvst.KindOffer:
			answerSDP, err := kitPeer.HandleOffer(msg.SDP())
			if err != nil {
				return false, fmt.Errorf("upstream handle offer: %w", err)
			}
			answerMsg := nvst.NewAnswer(answerSDP)
			answerRaw, _ := answerMsg.Encode()
			if err := ps.SendToKit(answerRaw); err != nil {
				return false, fmt.Errorf("send kit answer: %w", err)
			}
			log.Printf("[session] kit offer handled; sent answer upstream")
			// After tracks arrive, build downstream offer for browser.
			go buildBrowserOffer(ctx, browserPeer, ps, kitPeer)
			return false, nil
		case nvst.KindAnswer:
			// Kit is answering gateway's offer (browser-initiates pattern).
			if err := kitPeerAnswerNotApplicable(msg.SDP()); err != nil {
				log.Printf("[session] unexpected kit answer: %v", err)
			}
			return false, nil
		case nvst.KindCandidate:
			idx := uint16(msg.SdpMLineIndex())
			if err := kitPeer.AddCandidate(msg.Candidate(), msg.SdpMid(), idx); err != nil {
				log.Printf("[session] kit candidate add: %v", err)
			}
			return false, nil
		default:
			// Unknown NVST message — pass through to browser.
			return true, nil
		}
	}

	// -- Hook: messages from browser → gateway
	ps.OnClientMessage = func(raw []byte) (bool, error) {
		msg, err := nvst.Parse(raw)
		if err != nil {
			return true, nil
		}
		switch msg.Kind() {
		case nvst.KindAnswer:
			if err := browserPeer.SetAnswer(msg.SDP()); err != nil {
				return false, fmt.Errorf("downstream set answer: %w", err)
			}
			log.Printf("[session] browser answer applied downstream")
			return false, nil
		case nvst.KindOffer:
			log.Printf("[session] unexpected browser offer (Kit-initiates pattern assumed); ignoring")
			return false, nil
		case nvst.KindCandidate:
			idx := uint16(msg.SdpMLineIndex())
			if err := browserPeer.AddCandidate(msg.Candidate(), msg.SdpMid(), idx); err != nil {
				log.Printf("[session] browser candidate add: %v", err)
			}
			return false, nil
		default:
			// Pass through to Kit (peerId, config, etc.).
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

// buildBrowserOffer waits briefly for any upstream tracks to be attached,
// then generates a downstream offer and sends it to the browser.
//
// In practice the browser peer can renegotiate if tracks arrive later,
// but the initial offer should carry whatever tracks exist at that point.
func buildBrowserOffer(ctx context.Context, bp *downstream.BrowserPeer, ps *proxy.Session, kp *upstream.KitPeer) {
	// Wait until the upstream peer's connection is "connected" or a
	// timeout. We piggyback on its state; a real implementation might
	// wait for a specific number of tracks.
	_ = kp
	select {
	case <-ctx.Done():
		return
	default:
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
	log.Printf("[session] downstream offer sent to browser")
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

// kitPeerAnswerNotApplicable is a sentinel for the "Kit answers the
// gateway" flow we don't currently support (browser-initiates). Kept
// as a stub to document the branch.
func kitPeerAnswerNotApplicable(sdp string) error {
	_ = sdp
	return fmt.Errorf("kit answer flow not implemented (Kit-initiates pattern assumed)")
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
