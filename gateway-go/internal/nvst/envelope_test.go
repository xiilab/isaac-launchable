package nvst

import (
	"encoding/json"
	"testing"
)

func TestClassify_Offer(t *testing.T) {
	raw := []byte(`{"type":"offer","sdp":"v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\n"}`)
	m, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Kind() != KindOffer {
		t.Errorf("Kind = %v, want KindOffer", m.Kind())
	}
	if m.SDP() == "" {
		t.Error("SDP empty")
	}
}

func TestClassify_Answer(t *testing.T) {
	raw := []byte(`{"type":"answer","sdp":"v=0"}`)
	m, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Kind() != KindAnswer {
		t.Errorf("Kind = %v, want KindAnswer", m.Kind())
	}
}

func TestClassify_Candidate(t *testing.T) {
	raw := []byte(`{"type":"candidate","candidate":"candidate:1 1 udp 100 10.244.1.2 37879 typ host","sdpMid":"0","sdpMLineIndex":0}`)
	m, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Kind() != KindCandidate {
		t.Errorf("Kind = %v, want KindCandidate", m.Kind())
	}
	if m.Candidate() != "candidate:1 1 udp 100 10.244.1.2 37879 typ host" {
		t.Errorf("Candidate = %q", m.Candidate())
	}
	if m.SdpMid() != "0" {
		t.Errorf("SdpMid = %q, want \"0\"", m.SdpMid())
	}
	if m.SdpMLineIndex() != 0 {
		t.Errorf("SdpMLineIndex = %d", m.SdpMLineIndex())
	}
}

func TestClassify_Unknown(t *testing.T) {
	raw := []byte(`{"type":"config","value":{"supports_localized_text_input":true}}`)
	m, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Kind() != KindOther {
		t.Errorf("Kind = %v, want KindOther", m.Kind())
	}
}

func TestClassify_NoType(t *testing.T) {
	raw := []byte(`{"foo":"bar"}`)
	m, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Kind() != KindOther {
		t.Errorf("Kind = %v, want KindOther", m.Kind())
	}
}

func TestRoundTrip_UnknownPreservesFields(t *testing.T) {
	raw := []byte(`{"type":"config","value":{"nested":{"k":1}},"extra":"yes"}`)
	m, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out, err := m.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var a, b map[string]any
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if err := json.Unmarshal(out, &b); err != nil {
		t.Fatalf("unmarshal out: %v", err)
	}
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	if string(ab) != string(bb) {
		t.Errorf("roundtrip mismatch:\n got  %s\n want %s", out, raw)
	}
}

func TestSetSDP(t *testing.T) {
	raw := []byte(`{"type":"offer","sdp":"old"}`)
	m, _ := Parse(raw)
	m.SetSDP("new")
	out, _ := m.Encode()
	var decoded map[string]any
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded["sdp"] != "new" {
		t.Errorf("sdp = %v, want new", decoded["sdp"])
	}
	if decoded["type"] != "offer" {
		t.Errorf("type mutated: %v", decoded["type"])
	}
}

func TestSetCandidate(t *testing.T) {
	raw := []byte(`{"type":"candidate","candidate":"old","sdpMid":"0","sdpMLineIndex":0}`)
	m, _ := Parse(raw)
	m.SetCandidate("new")
	out, _ := m.Encode()
	var decoded map[string]any
	_ = json.Unmarshal(out, &decoded)
	if decoded["candidate"] != "new" {
		t.Errorf("candidate = %v", decoded["candidate"])
	}
	if decoded["sdpMid"] != "0" {
		t.Errorf("sdpMid mutated: %v", decoded["sdpMid"])
	}
}

func TestNewOffer(t *testing.T) {
	m := NewOffer("v=0\r\n")
	if m.Kind() != KindOffer {
		t.Errorf("Kind = %v", m.Kind())
	}
	if m.SDP() != "v=0\r\n" {
		t.Errorf("SDP = %q", m.SDP())
	}
}

func TestNewAnswer(t *testing.T) {
	m := NewAnswer("v=0\r\n")
	if m.Kind() != KindAnswer {
		t.Errorf("Kind = %v", m.Kind())
	}
}

func TestNewCandidate(t *testing.T) {
	m := NewCandidate("candidate:1 1 udp 100 1.2.3.4 5678 typ host", "0", 0)
	if m.Kind() != KindCandidate {
		t.Errorf("Kind = %v", m.Kind())
	}
	if m.Candidate() == "" {
		t.Error("Candidate empty")
	}
	if m.SdpMid() != "0" {
		t.Errorf("SdpMid = %q", m.SdpMid())
	}
}

func TestParseInvalidJSON(t *testing.T) {
	if _, err := Parse([]byte("not-json")); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
