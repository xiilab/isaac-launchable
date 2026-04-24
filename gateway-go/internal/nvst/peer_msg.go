// NVIDIA signaling wraps WebRTC offer/answer/candidate payloads inside a
// `peer_msg` envelope whose `msg` field is itself a JSON-encoded string
// (double-encoded). Additional metadata travels alongside: `ackid` /
// `ack` form a reliable-delivery pair, `peer_info` announces peer IDs,
// and `hb` is a heartbeat.
//
// Example frames observed on the wire:
//
//   Kit → gateway (server → client):
//     {"ackid":7,"peer_msg":{"from":1,"msg":"{\"type\":\"offer\",\"sdp\":\"v=0\\r\\n...\"}"}}
//
//   Browser → gateway (client → server):
//     {"peer_msg":{"from":10,"to":1,"msg":"{\"type\":\"answer\",\"sdp\":\"...\"}"}}
//     {"ack":7}
//
//   Kit peer registration broadcast:
//     {"peer_info":{"id":10,"name":"peer-N","connected":true,"peer_role":0,...}}
//
// The gateway's SFU logic must intercept offer/answer/candidate at the
// inner `msg` layer while leaving the outer envelope (including ackid/
// peer_msg routing fields) intact so Kit and the browser continue to
// see a well-formed signaling session.
package nvst

import (
	"encoding/json"
	"fmt"
)

// PeerMsgInner mirrors a standard WebRTC signaling payload carried
// inside peer_msg.msg. Fields are omitempty so the same struct serves
// offer/answer/candidate variants.
type PeerMsgInner struct {
	Type          string `json:"type"`
	SDP           string `json:"sdp,omitempty"`
	Candidate     string `json:"candidate,omitempty"`
	SDPMid        string `json:"sdpMid,omitempty"`
	SDPMLineIndex int    `json:"sdpMLineIndex,omitempty"`
}

// PeerMsgOuter captures the envelope routing fields.
type PeerMsgOuter struct {
	From  int
	To    int
	HasTo bool
	Inner *PeerMsgInner // nil if msg could not be decoded as WebRTC payload
}

// AsPeerMsg returns the parsed peer_msg envelope with inner WebRTC
// payload, if this message carries one. The second return value is
// false for frames that are not peer_msg (ack/ackid/hb/peer_info only).
func (m *Message) AsPeerMsg() (*PeerMsgOuter, bool) {
	pm, ok := m.raw["peer_msg"].(map[string]any)
	if !ok {
		return nil, false
	}
	outer := &PeerMsgOuter{}
	if v, ok := pm["from"]; ok {
		outer.From = asInt(v)
	}
	if v, ok := pm["to"]; ok {
		outer.To = asInt(v)
		outer.HasTo = true
	}
	msgStr, _ := pm["msg"].(string)
	if msgStr == "" {
		return outer, true
	}
	var inner PeerMsgInner
	if err := json.Unmarshal([]byte(msgStr), &inner); err != nil {
		// msg was not a WebRTC-typed JSON payload; return the envelope
		// with Inner=nil so the caller can still passthrough.
		return outer, true
	}
	if inner.Type == "" {
		return outer, true
	}
	outer.Inner = &inner
	return outer, true
}

// SetPeerMsgInner re-encodes the msg field with a new inner payload,
// preserving every other outer field. Returns an error if the message
// does not carry peer_msg.
func (m *Message) SetPeerMsgInner(inner *PeerMsgInner) error {
	pm, ok := m.raw["peer_msg"].(map[string]any)
	if !ok {
		return fmt.Errorf("nvst: no peer_msg in message")
	}
	innerBytes, err := json.Marshal(inner)
	if err != nil {
		return fmt.Errorf("nvst: marshal inner: %w", err)
	}
	pm["msg"] = string(innerBytes)
	m.raw["peer_msg"] = pm
	return nil
}

// Ackid returns the ackid value if present.
func (m *Message) Ackid() (int, bool) {
	v, ok := m.raw["ackid"]
	if !ok {
		return 0, false
	}
	return asInt(v), true
}

// asInt coerces either a float64 (from JSON decode) or a Go int (from
// in-memory construction) into int. Other types return 0.
func asInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case int64:
		return int(x)
	}
	return 0
}

// IsAck reports whether this is a bare {"ack":N} ack frame.
func (m *Message) IsAck() bool {
	_, ok := m.raw["ack"]
	return ok
}

// IsHeartbeat reports whether this is a {"hb":N} heartbeat.
func (m *Message) IsHeartbeat() bool {
	_, ok := m.raw["hb"]
	return ok
}

// PeerInfo returns the peer_info map if present.
func (m *Message) PeerInfo() (map[string]any, bool) {
	pi, ok := m.raw["peer_info"].(map[string]any)
	return pi, ok
}

// NewPeerMsgToKit builds a client→server envelope:
//
//	{"peer_msg":{"from":N,"to":1,"msg":"<JSON>"}}
func NewPeerMsgToKit(from, to int, inner *PeerMsgInner) (*Message, error) {
	innerBytes, err := json.Marshal(inner)
	if err != nil {
		return nil, fmt.Errorf("nvst: marshal inner: %w", err)
	}
	return &Message{raw: map[string]any{
		"peer_msg": map[string]any{
			"from": from,
			"to":   to,
			"msg":  string(innerBytes),
		},
	}}, nil
}

// NewAck builds a bare ack frame: {"ack":N}
func NewAck(ackid int) *Message {
	return &Message{raw: map[string]any{"ack": ackid}}
}

// NewPeerMsgToBrowser builds a server→client envelope with ackid:
//
//	{"ackid":N,"peer_msg":{"from":1,"msg":"<JSON>"}}
func NewPeerMsgToBrowser(ackid, from int, inner *PeerMsgInner) (*Message, error) {
	innerBytes, err := json.Marshal(inner)
	if err != nil {
		return nil, fmt.Errorf("nvst: marshal inner: %w", err)
	}
	return &Message{raw: map[string]any{
		"ackid": ackid,
		"peer_msg": map[string]any{
			"from": from,
			"msg":  string(innerBytes),
		},
	}}, nil
}
