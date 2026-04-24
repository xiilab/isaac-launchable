// Package nvst parses NVIDIA Omniverse WebRTC streaming library (NvSt)
// signaling envelopes. The protocol wraps standard WebRTC offer/answer/ICE
// exchanges inside a JSON envelope with a "type" discriminator. Unknown
// types (e.g. "config", peer registration) are preserved verbatim so
// they can be passed through a proxy unchanged.
package nvst

import (
	"encoding/json"
	"fmt"
)

// Kind classifies a signaling message by its "type" field.
type Kind int

const (
	KindOther Kind = iota
	KindOffer
	KindAnswer
	KindCandidate
)

// Message is a parsed NVST signaling envelope. The raw map preserves every
// field so unknown types can be re-encoded without loss. `encoded` is
// set by builders that need precise byte-level output (specific key
// ordering for the NVST browser library).
type Message struct {
	raw     map[string]any
	encoded []byte
}

// Parse decodes a single WS frame into a Message.
func Parse(data []byte) (*Message, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("nvst: parse: %w", err)
	}
	return &Message{raw: raw}, nil
}

// NewOffer constructs an offer envelope with the given SDP.
func NewOffer(sdp string) *Message {
	return &Message{raw: map[string]any{"type": "offer", "sdp": sdp}}
}

// NewAnswer constructs an answer envelope with the given SDP.
func NewAnswer(sdp string) *Message {
	return &Message{raw: map[string]any{"type": "answer", "sdp": sdp}}
}

// NewCandidate constructs a candidate envelope.
func NewCandidate(candidate, sdpMid string, sdpMLineIndex int) *Message {
	return &Message{raw: map[string]any{
		"type":          "candidate",
		"candidate":     candidate,
		"sdpMid":        sdpMid,
		"sdpMLineIndex": sdpMLineIndex,
	}}
}

// Kind returns the classification based on the "type" field.
func (m *Message) Kind() Kind {
	switch m.Type() {
	case "offer":
		return KindOffer
	case "answer":
		return KindAnswer
	case "candidate":
		return KindCandidate
	default:
		return KindOther
	}
}

// Type returns the raw "type" field.
func (m *Message) Type() string {
	s, _ := m.raw["type"].(string)
	return s
}

// SDP returns the "sdp" field value.
func (m *Message) SDP() string {
	s, _ := m.raw["sdp"].(string)
	return s
}

// SetSDP overwrites the "sdp" field.
func (m *Message) SetSDP(sdp string) {
	m.raw["sdp"] = sdp
}

// Candidate returns the "candidate" field value.
func (m *Message) Candidate() string {
	s, _ := m.raw["candidate"].(string)
	return s
}

// SetCandidate overwrites the "candidate" field.
func (m *Message) SetCandidate(c string) {
	m.raw["candidate"] = c
}

// SdpMid returns the "sdpMid" field value.
func (m *Message) SdpMid() string {
	s, _ := m.raw["sdpMid"].(string)
	return s
}

// SdpMLineIndex returns the "sdpMLineIndex" field value as int.
func (m *Message) SdpMLineIndex() int {
	switch v := m.raw["sdpMLineIndex"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

// Encode serializes the envelope back to JSON bytes. If the Message
// was built with a preferred byte layout (NewPeerMsgTo{Kit,Browser}),
// that exact payload is returned; otherwise the raw map is re-marshalled.
func (m *Message) Encode() ([]byte, error) {
	if m.encoded != nil {
		return m.encoded, nil
	}
	return json.Marshal(m.raw)
}

// Raw returns the underlying map for advanced inspection.
func (m *Message) Raw() map[string]any {
	return m.raw
}
