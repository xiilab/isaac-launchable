package nvst

import (
	"encoding/json"
	"testing"
)

func TestAsPeerMsg_KitOffer(t *testing.T) {
	// Real envelope observed in production logs.
	raw := []byte(`{"ackid":7,"peer_msg":{"from":1,"msg":"{\"type\":\"offer\",\"sdp\":\"v=0\\r\\no=- 1 2 IN IP4 127.0.0.1\\r\\n\"}"}}`)
	m, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	outer, ok := m.AsPeerMsg()
	if !ok {
		t.Fatal("AsPeerMsg returned false")
	}
	if outer.From != 1 {
		t.Errorf("From = %d, want 1", outer.From)
	}
	if outer.HasTo {
		t.Errorf("HasTo = true, want false (kit→client has no 'to')")
	}
	if outer.Inner == nil {
		t.Fatal("Inner is nil")
	}
	if outer.Inner.Type != "offer" {
		t.Errorf("Inner.Type = %q, want offer", outer.Inner.Type)
	}
	if outer.Inner.SDP == "" {
		t.Error("Inner.SDP empty")
	}
	if ackid, ok := m.Ackid(); !ok || ackid != 7 {
		t.Errorf("Ackid = %d (ok=%v), want 7", ackid, ok)
	}
}

func TestAsPeerMsg_BrowserAnswer(t *testing.T) {
	raw := []byte(`{"peer_msg":{"from":10,"to":1,"msg":"{\"type\":\"answer\",\"sdp\":\"v=0\\r\\n\"}"}}`)
	m, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	outer, ok := m.AsPeerMsg()
	if !ok {
		t.Fatal("AsPeerMsg returned false")
	}
	if outer.From != 10 {
		t.Errorf("From = %d", outer.From)
	}
	if !outer.HasTo || outer.To != 1 {
		t.Errorf("To = %d (hasTo=%v)", outer.To, outer.HasTo)
	}
	if outer.Inner == nil || outer.Inner.Type != "answer" {
		t.Errorf("Inner = %+v", outer.Inner)
	}
}

func TestAsPeerMsg_Candidate(t *testing.T) {
	raw := []byte(`{"peer_msg":{"from":10,"to":1,"msg":"{\"type\":\"candidate\",\"candidate\":\"candidate:1 1 udp 100 1.2.3.4 5678 typ host\",\"sdpMid\":\"0\",\"sdpMLineIndex\":0}"}}`)
	m, _ := Parse(raw)
	outer, _ := m.AsPeerMsg()
	if outer.Inner == nil || outer.Inner.Type != "candidate" {
		t.Fatalf("Inner = %+v", outer.Inner)
	}
	if outer.Inner.Candidate == "" {
		t.Error("Candidate empty")
	}
	if outer.Inner.SDPMid != "0" {
		t.Errorf("SDPMid = %q", outer.Inner.SDPMid)
	}
}

func TestAsPeerMsg_Ack(t *testing.T) {
	m, _ := Parse([]byte(`{"ack":3}`))
	if _, ok := m.AsPeerMsg(); ok {
		t.Error("ack frame should not be peer_msg")
	}
	if !m.IsAck() {
		t.Error("IsAck should be true")
	}
}

func TestAsPeerMsg_Heartbeat(t *testing.T) {
	m, _ := Parse([]byte(`{"hb":1}`))
	if _, ok := m.AsPeerMsg(); ok {
		t.Error("hb frame should not be peer_msg")
	}
	if !m.IsHeartbeat() {
		t.Error("IsHeartbeat should be true")
	}
}

func TestAsPeerMsg_PeerInfo(t *testing.T) {
	raw := []byte(`{"ackid":1,"peer_info":{"id":10,"name":"peer-X","peer_role":0,"connected":true}}`)
	m, _ := Parse(raw)
	pi, ok := m.PeerInfo()
	if !ok {
		t.Fatal("PeerInfo not found")
	}
	if id, ok := pi["id"].(float64); !ok || int(id) != 10 {
		t.Errorf("id = %v", pi["id"])
	}
}

func TestAsPeerMsg_MsgWithoutType(t *testing.T) {
	// Inner msg that doesn't look like WebRTC payload — Inner should be nil.
	raw := []byte(`{"peer_msg":{"from":1,"msg":"{\"chat\":\"hi\"}"}}`)
	m, _ := Parse(raw)
	outer, ok := m.AsPeerMsg()
	if !ok {
		t.Fatal("AsPeerMsg returned false")
	}
	if outer.Inner != nil {
		t.Errorf("Inner should be nil for non-WebRTC msg, got %+v", outer.Inner)
	}
}

func TestSetPeerMsgInner_PreservesOuter(t *testing.T) {
	raw := []byte(`{"ackid":7,"peer_msg":{"from":1,"msg":"{\"type\":\"offer\",\"sdp\":\"original\"}"}}`)
	m, _ := Parse(raw)
	newInner := &PeerMsgInner{Type: "offer", SDP: "replaced"}
	if err := m.SetPeerMsgInner(newInner); err != nil {
		t.Fatalf("SetPeerMsgInner: %v", err)
	}
	encoded, _ := m.Encode()

	var got map[string]any
	_ = json.Unmarshal(encoded, &got)

	// ackid preserved
	if ackid, ok := got["ackid"].(float64); !ok || int(ackid) != 7 {
		t.Errorf("ackid lost: %v", got["ackid"])
	}
	pm := got["peer_msg"].(map[string]any)
	if from, _ := pm["from"].(float64); int(from) != 1 {
		t.Errorf("from lost: %v", pm["from"])
	}
	// msg replaced
	msgStr := pm["msg"].(string)
	var innerBack PeerMsgInner
	_ = json.Unmarshal([]byte(msgStr), &innerBack)
	if innerBack.SDP != "replaced" {
		t.Errorf("SDP not replaced: %q", innerBack.SDP)
	}
}

func TestNewPeerMsgToKit(t *testing.T) {
	inner := &PeerMsgInner{Type: "answer", SDP: "v=0"}
	m, err := NewPeerMsgToKit(10, 1, inner)
	if err != nil {
		t.Fatalf("NewPeerMsgToKit: %v", err)
	}
	outer, ok := m.AsPeerMsg()
	if !ok {
		t.Fatal("AsPeerMsg false")
	}
	if outer.From != 10 || outer.To != 1 || !outer.HasTo {
		t.Errorf("envelope: from=%d to=%d hasTo=%v", outer.From, outer.To, outer.HasTo)
	}
	if outer.Inner.Type != "answer" {
		t.Errorf("inner type = %q", outer.Inner.Type)
	}
}

func TestNewPeerMsgToBrowser(t *testing.T) {
	inner := &PeerMsgInner{Type: "offer", SDP: "v=0"}
	m, err := NewPeerMsgToBrowser(42, 1, inner)
	if err != nil {
		t.Fatalf("NewPeerMsgToBrowser: %v", err)
	}
	if ack, ok := m.Ackid(); !ok || ack != 42 {
		t.Errorf("Ackid = %d (ok=%v)", ack, ok)
	}
	outer, _ := m.AsPeerMsg()
	if outer.From != 1 || outer.HasTo {
		t.Errorf("envelope: from=%d hasTo=%v", outer.From, outer.HasTo)
	}
}
