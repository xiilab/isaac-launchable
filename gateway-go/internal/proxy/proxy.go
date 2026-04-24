// Package proxy implements the browser ↔ Gateway ↔ Kit WebSocket signaling
// proxy. By default every frame is passed through unchanged (pure NVST
// opacity preservation). The Session exposes per-direction hooks so a
// higher layer can intercept offer/answer/candidate frames and route
// them into the local PeerConnection pair instead of forwarding.
package proxy

import (
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// Session represents one browser connection paired with one upstream Kit
// connection. Hooks are set synchronously by the SessionFactory before
// any frame pump starts.
type Session struct {
	Client *websocket.Conn
	Kit    *websocket.Conn

	// OnClientMessage is invoked for every frame received from the
	// browser. Returning forward=false consumes the frame (not
	// forwarded to Kit). Errors close the session.
	OnClientMessage func(raw []byte) (forward bool, err error)

	// OnKitMessage is invoked for every frame received from Kit.
	// Returning forward=false consumes the frame.
	OnKitMessage func(raw []byte) (forward bool, err error)

	// OnClose is invoked once after both sides have been torn down.
	OnClose func()

	closeOnce sync.Once
}

// SessionFactory is called once per new browser connection, after the
// upstream Kit connection is established but before any frame pump
// starts. Implementations set hooks on the Session. Returning an error
// aborts the session.
type SessionFactory func(s *Session) error

// NewHandler returns an http.Handler that accepts WS upgrade requests
// from the browser and opens a paired WS connection to kitURL.
func NewHandler(kitURL string, factory SessionFactory) http.Handler {
	upgrader := websocket.Upgrader{
		CheckOrigin:     func(r *http.Request) bool { return true },
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientWS, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("[proxy] client upgrade: %v", err)
			return
		}
		// Forward the same path + query to Kit so `/sign_in` +
		// peer_id/version/reconnect survive. kitURL is the base
		// (e.g. "ws://127.0.0.1:49100") with no path; the request path
		// (typically "/sign_in") comes from the browser via nginx.
		upstreamURL := kitURL + r.URL.Path
		if r.URL.RawQuery != "" {
			upstreamURL = upstreamURL + "?" + r.URL.RawQuery
		}
		kitWS, _, err := websocket.DefaultDialer.Dial(upstreamURL, nil)
		if err != nil {
			log.Printf("[proxy] kit dial %s: %v", upstreamURL, err)
			clientWS.Close()
			return
		}

		sess := &Session{Client: clientWS, Kit: kitWS}
		if factory != nil {
			if err := factory(sess); err != nil {
				log.Printf("[proxy] factory: %v", err)
				clientWS.Close()
				kitWS.Close()
				return
			}
		}

		go pump(sess, true)
		go pump(sess, false)
	})
}

// pump runs one direction of the proxy. When fromClient is true, it
// reads from sess.Client and writes to sess.Kit (invoking
// OnClientMessage). Otherwise the reverse.
func pump(sess *Session, fromClient bool) {
	var src, dst *websocket.Conn
	var hook func([]byte) (bool, error)
	var label string
	if fromClient {
		src, dst = sess.Client, sess.Kit
		hook = sess.OnClientMessage
		label = "client→kit"
	} else {
		src, dst = sess.Kit, sess.Client
		hook = sess.OnKitMessage
		label = "kit→client"
	}

	for {
		mt, data, err := src.ReadMessage()
		if err != nil {
			log.Printf("[proxy] %s read: %v", label, err)
			sess.tearDown()
			return
		}
		forward := true
		if hook != nil {
			fw, hookErr := hook(data)
			if hookErr != nil {
				log.Printf("[proxy] %s hook: %v", label, hookErr)
				sess.tearDown()
				return
			}
			forward = fw
		}
		if !forward {
			continue
		}
		if err := dst.WriteMessage(mt, data); err != nil {
			log.Printf("[proxy] %s write: %v", label, err)
			sess.tearDown()
			return
		}
	}
}

// Send writes a frame to the client side. Safe for use from hooks.
func (s *Session) Send(data []byte) error {
	return s.Client.WriteMessage(websocket.TextMessage, data)
}

// SendToKit writes a frame to the upstream Kit side.
func (s *Session) SendToKit(data []byte) error {
	return s.Kit.WriteMessage(websocket.TextMessage, data)
}

func (s *Session) tearDown() {
	s.closeOnce.Do(func() {
		_ = s.Client.Close()
		_ = s.Kit.Close()
		if s.OnClose != nil {
			s.OnClose()
		}
	})
}
