package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// kitEcho is a minimal WS server that mimics Kit: on connect, sends a
// config frame, then echoes every incoming frame. Used to verify the
// proxy round-trips frames between browser client and Kit.
func kitEcho(t *testing.T) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("kitEcho upgrade: %v", err)
			return
		}
		defer ws.Close()
		// Initial greeting from Kit.
		_ = ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"config","value":{"ok":true}}`))
		for {
			mt, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			if err := ws.WriteMessage(mt, data); err != nil {
				return
			}
		}
	}))
}

func TestProxy_FramesRoundTripBothDirections(t *testing.T) {
	kit := kitEcho(t)
	defer kit.Close()
	kitWS := "ws" + strings.TrimPrefix(kit.URL, "http")

	h := NewHandler(kitWS, func(s *Session) error {
		// No-op hooks: default pass-through mode.
		return nil
	})
	gw := httptest.NewServer(h)
	defer gw.Close()

	clientURL := "ws" + strings.TrimPrefix(gw.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(clientURL, nil)
	if err != nil {
		t.Fatalf("client dial: %v", err)
	}
	defer client.Close()

	// 1) Read the server-originated greeting (kit → gateway → client).
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("client read greeting: %v", err)
	}
	if !strings.Contains(string(data), `"type":"config"`) {
		t.Errorf("greeting = %q, want config frame", data)
	}

	// 2) Send from client → kit → echoed back.
	if err := client.WriteMessage(websocket.TextMessage, []byte(`{"type":"offer","sdp":"hello"}`)); err != nil {
		t.Fatalf("client write: %v", err)
	}
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, echoed, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("client read echo: %v", err)
	}
	if string(echoed) != `{"type":"offer","sdp":"hello"}` {
		t.Errorf("echoed = %q", echoed)
	}
}

func TestProxy_HookInterceptsOffer(t *testing.T) {
	kit := kitEcho(t)
	defer kit.Close()
	kitWS := "ws" + strings.TrimPrefix(kit.URL, "http")

	var mu sync.Mutex
	seenOffer := false
	h := NewHandler(kitWS, func(s *Session) error {
		s.OnClientMessage = func(raw []byte) (forward bool, err error) {
			if strings.Contains(string(raw), `"type":"offer"`) {
				mu.Lock()
				seenOffer = true
				mu.Unlock()
				// Hook consumes offer (no pass-through)
				return false, nil
			}
			return true, nil
		}
		return nil
	})
	gw := httptest.NewServer(h)
	defer gw.Close()

	clientURL := "ws" + strings.TrimPrefix(gw.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(clientURL, nil)
	if err != nil {
		t.Fatalf("client dial: %v", err)
	}
	defer client.Close()

	// Drain greeting.
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, _ = client.ReadMessage()

	// Send offer — hook should intercept, no echo back.
	_ = client.WriteMessage(websocket.TextMessage, []byte(`{"type":"offer","sdp":"x"}`))
	client.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, _, err := client.ReadMessage(); err == nil {
		t.Error("expected read timeout (offer was intercepted), got frame")
	}

	mu.Lock()
	if !seenOffer {
		t.Error("hook did not see offer")
	}
	mu.Unlock()
}

func TestProxy_ClientCloseTearsDownUpstream(t *testing.T) {
	kit := kitEcho(t)
	defer kit.Close()
	kitWS := "ws" + strings.TrimPrefix(kit.URL, "http")

	var mu sync.Mutex
	upstreamClosed := false
	h := NewHandler(kitWS, func(s *Session) error {
		s.OnClose = func() {
			mu.Lock()
			upstreamClosed = true
			mu.Unlock()
		}
		return nil
	})
	gw := httptest.NewServer(h)
	defer gw.Close()

	clientURL := "ws" + strings.TrimPrefix(gw.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(clientURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, _ = client.ReadMessage()
	client.Close()

	// Wait for OnClose callback.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := upstreamClosed
		mu.Unlock()
		if done {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("OnClose hook never fired")
}
