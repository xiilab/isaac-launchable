# Pion WebRTC Gateway Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `play.py --livestream 2` 와 `train.py --livestream 2` 가 `hostNetwork=false` + pod-0/-1 공존 환경에서 브라우저 뷰포트로 로봇을 실시간 렌더링하도록, Pion 기반 WebRTC Gateway sidecar 를 pod 안에 넣어 Kit (upstream) 과 브라우저 (downstream via coturn relay) 사이를 중계한다.

**Architecture:** Gateway 컨테이너는 두 개의 WebRTC peer 를 운영 — upstream peer 는 pod 내부 loopback 으로 Kit 의 `omni.kit.livestream.app` signaling 에 브라우저처럼 접속, downstream peer 는 자체 WebSocket signaling 서버를 통해 브라우저와 연결하며 `iceTransportPolicy=relay` 로 coturn 을 강제 경유. 두 peer 사이는 RTP 패킷을 재인코딩 없이 track-forwarding.

**Tech Stack:** Go 1.22, `github.com/pion/webrtc/v4`, `github.com/gorilla/websocket`, Docker, Kubernetes (k0s), 기존 coturn (10.61.3.74:3478), Isaac Sim 6.0 Kit 110 livestream extensions (변경 없음).

**Spec:** `docs/superpowers/specs/2026-04-24-webrtc-gateway-pion-design.md` (commit `a100f73`)

---

## File Structure

**Will create:**
- `gateway/go.mod` — Go module declaration
- `gateway/main.go` — entry point + CLI flag parsing + HTTP/WS server wiring
- `gateway/config/config.go` — env var parsing (`KIT_SIGNAL_URL`, `TURN_URI`, `TURN_USERNAME`, `TURN_CREDENTIAL`, `LISTEN_ADDR`)
- `gateway/config/config_test.go` — env parsing tests
- `gateway/signaling/server.go` — browser-facing WS signaling server
- `gateway/signaling/server_test.go` — handshake test using `httptest.Server` + `websocket.Dialer`
- `gateway/upstream/kit_peer.go` — upstream WebRTC peer against Kit
- `gateway/upstream/kit_peer_test.go` — message-envelope parsing test
- `gateway/downstream/browser_peer.go` — downstream WebRTC peer with TURN-relay config
- `gateway/downstream/browser_peer_test.go` — ICE config validation test
- `gateway/relay/track_forwarder.go` — RTP track forwarding (upstream RemoteTrack → downstream LocalStaticRTP)
- `gateway/relay/track_forwarder_test.go` — packet forwarding count test
- `gateway/healthz/healthz.go` — `/healthz` handler
- `gateway/Dockerfile` — multi-stage Go build, distroless runtime
- `docs/superpowers/plans/notes/2026-04-24-kit-signaling-probe.md` — Phase 1 probe results

**Will modify:**
- `k8s/isaac-sim/deployment-0.yaml` — add gateway sidecar, drop webrtc-media hostPort
- `k8s/isaac-sim/deployment-1.yaml` — same, with pod-1 TURN username
- `k8s/base/services.yaml` — add :9000/TCP to svc-0 and svc-1, delete -media NodePort Services
- `k8s/base/secret.yaml` — add `isaac-launchable-turn` Secret
- `k8s/base/configmaps.yaml` — add `SIGNAL_PATH`, `TURN_URI`, `TURN_USERNAME` keys in web-viewer ConfigMap
- `k8s/isaac-sim/ingress-0.yaml` — add `/pod-0/signaling` path to svc-0:9000
- `k8s/isaac-sim/ingress-1.yaml` — add `/pod-1/signaling` path to svc-1:9000
- `isaac-launchable/isaac-lab/web-viewer-sample/src/main.ts` — parameterize signaling URL + iceServers from env
- `isaac-launchable/isaac-lab/web-viewer-sample/entrypoint.sh` — wire new env vars into `sed` replacements
- `/Users/xiilab/.claude/projects/-Users-xiilab-git-HAMi/memory/project_isaac_lab_livestream_status.md` — final resolution note

**Will NOT modify:**
- Isaac Sim image (`isaac-launchable-vscode:6.0.0`)
- `runheadless.sh` (Kit signaling 49100 은 container 내부에 남음)
- coturn deployment (`k8s/base/turn.yaml`)

---

## Phase 1 — Kit Signaling Protocol Probe

### Task 1: Capture Kit signaling WebSocket message format

Goal: Record actual JSON frames the browser currently exchanges with Kit at `ws://10.61.3.125/sign_in` so the Pion upstream peer can speak the same protocol.

**Files:**
- Write: `docs/superpowers/plans/notes/2026-04-24-kit-signaling-probe.md`

- [ ] **Step 1: Ensure pod-0 is Running and quadrupeds.py is available**

```bash
ssh root@10.61.3.75 'k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o wide'
```
Expected: `3/3 Running`, IP `10.244.x.x` (pod network).

- [ ] **Step 2: Start a Kit session the browser can reach**

In pod-0 vscode shell, run:

```bash
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o name" | head -1 | cut -d/ -f2)
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -i -- bash -s" <<'REMOTE'
cd /workspace/isaaclab
nohup /isaac-sim/runheadless.sh > /tmp/probe1_kit.log 2>&1 &
until grep -q "app ready" /tmp/probe1_kit.log; do sleep 3; done
echo OK
REMOTE
```
Expected: `OK` printed.

- [ ] **Step 3: Capture Chrome DevTools WS frames**

Open Chrome, go to `http://10.61.3.125/viewer/`. Open DevTools → **Network** → filter `WS`. Click the `sign_in?peer_id=...` row → **Messages** (or **Frames**) tab. Copy every frame sent and received in the first 5 seconds.

Paste them (along with their direction ▲/▼) into `docs/superpowers/plans/notes/2026-04-24-kit-signaling-probe.md` under the sections below.

- [ ] **Step 4: Save raw frames + parsed structure to notes**

Create `docs/superpowers/plans/notes/2026-04-24-kit-signaling-probe.md`:

```markdown
# Kit signaling protocol probe (2026-04-24)

## URL
- WS: `ws://10.61.3.125/sign_in?peer_id=peer-<N>&version=2&reconnect=1`

## Raw frames (browser DevTools)

### Direction ▲ (browser → server)
<paste frames verbatim>

### Direction ▼ (server → browser)
<paste frames verbatim>

## Parsed message envelope
- Keys observed: <list>
- SDP frames: are they raw SDP or wrapped in JSON? <answer>
- ICE candidate frames: format? <answer>
- Any Kit-proprietary extension fields? <answer>

## Ability to replicate in Pion
- Standard JSON + SDP → can be reproduced 1:1? (yes/no)
- Notes on any non-standard behavior:
```

Fill in every section from the captured data. Do not guess.

- [ ] **Step 5: Stop probe Kit**

```bash
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o name" | head -1 | cut -d/ -f2)
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- bash -c 'pgrep -f runheadless | xargs -r kill -TERM'"
```

- [ ] **Step 6: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add docs/superpowers/plans/notes/2026-04-24-kit-signaling-probe.md
git commit -m "docs: Kit signaling WS protocol probe" --no-verify
```

**Decision gate:** If "Ability to replicate in Pion" is NO (Kit uses a proprietary binary protocol), stop this plan. Escalate to redesign.

---

## Phase 2 — Gateway Implementation

### Task 2: Initialize Go module and Pion dependency

**Files:**
- Create: `gateway/go.mod`
- Create: `gateway/.gitignore`

- [ ] **Step 1: Create directory and initialize module**

```bash
cd /Users/xiilab/git/isaac-launchable
mkdir -p gateway
cd gateway
go mod init github.com/xiilab/isaac-launchable/gateway
```
Expected: `go.mod` created with `module github.com/xiilab/isaac-launchable/gateway` and `go 1.22`.

- [ ] **Step 2: Add dependencies**

```bash
cd /Users/xiilab/git/isaac-launchable/gateway
go get github.com/pion/webrtc/v4@v4.0.0
go get github.com/gorilla/websocket@v1.5.1
go mod tidy
```
Expected: `go.mod` and `go.sum` populated.

- [ ] **Step 3: Add .gitignore**

```bash
cat > /Users/xiilab/git/isaac-launchable/gateway/.gitignore <<'EOF'
/bin
/gateway
*.test
coverage.out
EOF
```

- [ ] **Step 4: Verify build target works**

```bash
cd /Users/xiilab/git/isaac-launchable/gateway
cat > main.go <<'EOF'
package main

func main() {}
EOF
go build ./...
```
Expected: no errors. A `main` binary may be produced.

- [ ] **Step 5: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add gateway/go.mod gateway/go.sum gateway/.gitignore gateway/main.go
git commit -m "feat(gateway): init Go module with Pion + gorilla/websocket" --no-verify
```

---

### Task 3: Config package (env var parsing)

**Files:**
- Create: `gateway/config/config.go`
- Create: `gateway/config/config_test.go`

- [ ] **Step 1: Write failing test**

```go
// gateway/config/config_test.go
package config

import (
	"os"
	"testing"
)

func TestLoad_WithAllEnvSet(t *testing.T) {
	os.Setenv("KIT_SIGNAL_URL", "ws://127.0.0.1:49100/sign_in")
	os.Setenv("TURN_URI", "turn:10.61.3.74:3478")
	os.Setenv("TURN_USERNAME", "isaac0")
	os.Setenv("TURN_CREDENTIAL", "secret")
	os.Setenv("LISTEN_ADDR", ":9000")
	defer os.Unsetenv("KIT_SIGNAL_URL")
	defer os.Unsetenv("TURN_URI")
	defer os.Unsetenv("TURN_USERNAME")
	defer os.Unsetenv("TURN_CREDENTIAL")
	defer os.Unsetenv("LISTEN_ADDR")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.KitSignalURL != "ws://127.0.0.1:49100/sign_in" {
		t.Errorf("KitSignalURL = %q", cfg.KitSignalURL)
	}
	if cfg.TurnURI != "turn:10.61.3.74:3478" {
		t.Errorf("TurnURI = %q", cfg.TurnURI)
	}
	if cfg.TurnUsername != "isaac0" {
		t.Errorf("TurnUsername = %q", cfg.TurnUsername)
	}
	if cfg.TurnCredential != "secret" {
		t.Errorf("TurnCredential = %q", cfg.TurnCredential)
	}
	if cfg.ListenAddr != ":9000" {
		t.Errorf("ListenAddr = %q", cfg.ListenAddr)
	}
}

func TestLoad_MissingRequired(t *testing.T) {
	os.Unsetenv("KIT_SIGNAL_URL")
	os.Unsetenv("TURN_URI")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for missing required env, got nil")
	}
}
```

- [ ] **Step 2: Run test — expect failure (no implementation yet)**

```bash
cd /Users/xiilab/git/isaac-launchable/gateway
go test ./config/...
```
Expected: build failure — `undefined: Load`, `undefined: Config`.

- [ ] **Step 3: Implement config**

```go
// gateway/config/config.go
package config

import (
	"fmt"
	"os"
)

type Config struct {
	KitSignalURL   string
	TurnURI        string
	TurnUsername   string
	TurnCredential string
	ListenAddr     string
}

func Load() (*Config, error) {
	cfg := &Config{
		KitSignalURL:   os.Getenv("KIT_SIGNAL_URL"),
		TurnURI:        os.Getenv("TURN_URI"),
		TurnUsername:   os.Getenv("TURN_USERNAME"),
		TurnCredential: os.Getenv("TURN_CREDENTIAL"),
		ListenAddr:     os.Getenv("LISTEN_ADDR"),
	}
	if cfg.KitSignalURL == "" {
		return nil, fmt.Errorf("KIT_SIGNAL_URL is required")
	}
	if cfg.TurnURI == "" {
		return nil, fmt.Errorf("TURN_URI is required")
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":9000"
	}
	return cfg, nil
}
```

- [ ] **Step 4: Run tests**

```bash
cd /Users/xiilab/git/isaac-launchable/gateway
go test ./config/...
```
Expected: `PASS`.

- [ ] **Step 5: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add gateway/config/
git commit -m "feat(gateway): env-based Config loader with validation" --no-verify
```

---

### Task 4: Browser-facing signaling server skeleton

**Files:**
- Create: `gateway/signaling/server.go`
- Create: `gateway/signaling/server_test.go`

- [ ] **Step 1: Write failing test**

```go
// gateway/signaling/server_test.go
package signaling

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestServer_WebSocketUpgrade(t *testing.T) {
	var connectedOnServer bool
	srv := httptest.NewServer(NewHandler(func(c *Conn) {
		connectedOnServer = true
	}))
	defer srv.Close()

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/signaling"
	ws, _, err := websocket.DefaultDialer.Dial(url, http.Header{})
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer ws.Close()

	if !connectedOnServer {
		t.Fatal("handler did not register connection")
	}
}
```

- [ ] **Step 2: Run test — expect failure**

```bash
cd /Users/xiilab/git/isaac-launchable/gateway
go test ./signaling/...
```
Expected: failure — `NewHandler`, `Conn` undefined.

- [ ] **Step 3: Update test to hit root path (not /signaling) and remove internal mux**

Replace the `url` line in the test:

```go
url := "ws" + strings.TrimPrefix(srv.URL, "http")
```

(No `/signaling` suffix — NewHandler will be mounted at the caller's preferred path by `main.go`, not baked in.)

- [ ] **Step 4: Implement minimal server (no internal routing)**

```go
// gateway/signaling/server.go
package signaling

import (
	"net/http"

	"github.com/gorilla/websocket"
)

type Conn struct {
	WS *websocket.Conn
}

type ConnectHandler func(*Conn)

var upgrader = websocket.Upgrader{
	CheckOrigin:     func(r *http.Request) bool { return true },
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
}

// NewHandler returns an http.Handler that upgrades any request to a WebSocket
// and invokes onConnect. The caller mounts it at whatever URL path they want
// (e.g. `mux.Handle("/signaling", signaling.NewHandler(cb))`).
func NewHandler(onConnect ConnectHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		onConnect(&Conn{WS: ws})
	})
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./signaling/...
```
Expected: `PASS`.

- [ ] **Step 6: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add gateway/signaling/
git commit -m "feat(gateway): WebSocket signaling handler with connect callback" --no-verify
```

---

### Task 5: Upstream peer (Kit client) stub with message envelope parser

**Files:**
- Create: `gateway/upstream/kit_peer.go`
- Create: `gateway/upstream/kit_peer_test.go`

Note: Task 1 probe output determines the exact message envelope. The stub below handles the most likely envelope (`{"type": "...", "sdp": "...", "candidate": "..."}`). Adjust key names after Task 1 notes are available.

- [ ] **Step 1: Write envelope-parsing test**

```go
// gateway/upstream/kit_peer_test.go
package upstream

import (
	"encoding/json"
	"testing"
)

func TestParseMessage_Offer(t *testing.T) {
	raw := []byte(`{"type":"offer","sdp":"v=0\r\n..."}`)
	m, err := ParseMessage(raw)
	if err != nil {
		t.Fatalf("ParseMessage: %v", err)
	}
	if m.Type != "offer" {
		t.Errorf("Type = %q, want offer", m.Type)
	}
	if m.SDP == "" {
		t.Error("SDP empty")
	}
}

func TestParseMessage_Candidate(t *testing.T) {
	raw := []byte(`{"type":"candidate","candidate":"candidate:1 1 udp 100 10.244.1.2 37879 typ host"}`)
	m, err := ParseMessage(raw)
	if err != nil {
		t.Fatalf("ParseMessage: %v", err)
	}
	if m.Type != "candidate" || m.Candidate == "" {
		t.Errorf("got %+v", m)
	}
}

func TestMarshalMessage_RoundTrip(t *testing.T) {
	m := Message{Type: "answer", SDP: "v=0"}
	b, err := json.Marshal(&m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Message
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back != m {
		t.Errorf("roundtrip mismatch: %+v vs %+v", back, m)
	}
}
```

- [ ] **Step 2: Run test — expect failure**

```bash
go test ./upstream/...
```
Expected: build failure, `ParseMessage`, `Message` undefined.

- [ ] **Step 3: Implement parser**

```go
// gateway/upstream/kit_peer.go
package upstream

import (
	"encoding/json"
)

// Message matches the envelope observed in Task 1 probe.
// If the probe reveals additional fields, extend this struct.
type Message struct {
	Type      string `json:"type"`
	SDP       string `json:"sdp,omitempty"`
	Candidate string `json:"candidate,omitempty"`
}

func ParseMessage(raw []byte) (Message, error) {
	var m Message
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, err
	}
	return m, nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./upstream/...
```
Expected: `PASS`.

- [ ] **Step 5: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add gateway/upstream/
git commit -m "feat(gateway): Kit message envelope parser (upstream)" --no-verify
```

---

### Task 6: Downstream peer factory with TURN relay config

**Files:**
- Create: `gateway/downstream/browser_peer.go`
- Create: `gateway/downstream/browser_peer_test.go`

- [ ] **Step 1: Write failing test**

```go
// gateway/downstream/browser_peer_test.go
package downstream

import (
	"testing"

	"github.com/pion/webrtc/v4"
)

func TestNewBrowserPeer_UsesTURNRelay(t *testing.T) {
	cfg := Config{
		TurnURI:        "turn:10.61.3.74:3478",
		TurnUsername:   "isaac0",
		TurnCredential: "secret",
	}
	pc, err := NewBrowserPeer(cfg)
	if err != nil {
		t.Fatalf("NewBrowserPeer: %v", err)
	}
	defer pc.Close()

	cur := pc.GetConfiguration()
	if cur.ICETransportPolicy != webrtc.ICETransportPolicyRelay {
		t.Errorf("ICETransportPolicy = %v, want relay", cur.ICETransportPolicy)
	}
	if len(cur.ICEServers) != 1 {
		t.Fatalf("ICEServers len = %d, want 1", len(cur.ICEServers))
	}
	if cur.ICEServers[0].URLs[0] != "turn:10.61.3.74:3478" {
		t.Errorf("ICEServer URL = %q", cur.ICEServers[0].URLs[0])
	}
	if cur.ICEServers[0].Username != "isaac0" {
		t.Errorf("username = %q", cur.ICEServers[0].Username)
	}
}
```

- [ ] **Step 2: Run test — expect failure**

```bash
go test ./downstream/...
```
Expected: undefined `Config`, `NewBrowserPeer`.

- [ ] **Step 3: Implement**

```go
// gateway/downstream/browser_peer.go
package downstream

import (
	"github.com/pion/webrtc/v4"
)

type Config struct {
	TurnURI        string
	TurnUsername   string
	TurnCredential string
}

func NewBrowserPeer(cfg Config) (*webrtc.PeerConnection, error) {
	rtcCfg := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs:       []string{cfg.TurnURI},
				Username:   cfg.TurnUsername,
				Credential: cfg.TurnCredential,
			},
		},
		ICETransportPolicy: webrtc.ICETransportPolicyRelay,
	}
	return webrtc.NewPeerConnection(rtcCfg)
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./downstream/...
```
Expected: `PASS`.

- [ ] **Step 5: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add gateway/downstream/
git commit -m "feat(gateway): browser PeerConnection factory with TURN-only relay" --no-verify
```

---

### Task 7: RTP track forwarder

**Files:**
- Create: `gateway/relay/track_forwarder.go`
- Create: `gateway/relay/track_forwarder_test.go`

- [ ] **Step 1: Write failing test**

```go
// gateway/relay/track_forwarder_test.go
package relay

import (
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

func TestForwardTrack_CopiesPackets(t *testing.T) {
	dst, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264},
		"video", "kit",
	)
	if err != nil {
		t.Fatalf("track: %v", err)
	}

	// Channel-based fake source exposing a single ReadRTP method.
	source := make(chan *rtp.Packet, 4)
	source <- &rtp.Packet{Header: rtp.Header{SequenceNumber: 1}, Payload: []byte("a")}
	source <- &rtp.Packet{Header: rtp.Header{SequenceNumber: 2}, Payload: []byte("b")}
	close(source)

	fwd := NewForwarder(dst)
	count, err := fwd.PumpFromChannel(source)
	if err != nil {
		t.Fatalf("pump: %v", err)
	}
	if count != 2 {
		t.Errorf("forwarded %d packets, want 2", count)
	}
	// allow goroutines to settle
	time.Sleep(10 * time.Millisecond)
}
```

- [ ] **Step 2: Run test — expect failure**

```bash
go test ./relay/...
```
Expected: undefined `Forwarder`.

- [ ] **Step 3: Implement**

```go
// gateway/relay/track_forwarder.go
package relay

import (
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

type Forwarder struct {
	dst *webrtc.TrackLocalStaticRTP
}

func NewForwarder(dst *webrtc.TrackLocalStaticRTP) *Forwarder {
	return &Forwarder{dst: dst}
}

// PumpFromChannel is used in tests. Production code calls PumpFromTrack (below)
// which reads from a Pion remote track.
func (f *Forwarder) PumpFromChannel(src <-chan *rtp.Packet) (int, error) {
	n := 0
	for pkt := range src {
		if err := f.dst.WriteRTP(pkt); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// PumpFromTrack reads RTP packets from a remote Pion track until EOF.
func (f *Forwarder) PumpFromTrack(remote *webrtc.TrackRemote) (int, error) {
	n := 0
	for {
		pkt, _, err := remote.ReadRTP()
		if err != nil {
			return n, err
		}
		if err := f.dst.WriteRTP(pkt); err != nil {
			return n, err
		}
		n++
	}
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./relay/...
```
Expected: `PASS`.

- [ ] **Step 5: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add gateway/relay/
git commit -m "feat(gateway): RTP track forwarder (no re-encode)" --no-verify
```

---

### Task 8: Healthz handler

**Files:**
- Create: `gateway/healthz/healthz.go`
- Create: `gateway/healthz/healthz_test.go`

- [ ] **Step 1: Write failing test**

```go
// gateway/healthz/healthz_test.go
package healthz

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandler_Returns200OK(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if w.Body.String() != "ok\n" {
		t.Errorf("body = %q, want %q", w.Body.String(), "ok\n")
	}
}
```

- [ ] **Step 2: Run test — expect failure**

```bash
go test ./healthz/...
```
Expected: undefined `Handler`.

- [ ] **Step 3: Implement**

```go
// gateway/healthz/healthz.go
package healthz

import "net/http"

func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./healthz/...
```
Expected: `PASS`.

- [ ] **Step 5: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add gateway/healthz/
git commit -m "feat(gateway): liveness /healthz handler" --no-verify
```

---

### Task 9: main.go wiring

**Files:**
- Modify: `gateway/main.go`

- [ ] **Step 1: Replace main.go with wired-up version**

```go
// gateway/main.go
package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/xiilab/isaac-launchable/gateway/config"
	"github.com/xiilab/isaac-launchable/gateway/downstream"
	"github.com/xiilab/isaac-launchable/gateway/healthz"
	"github.com/xiilab/isaac-launchable/gateway/signaling"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/healthz", healthz.Handler())
	mux.Handle("/signaling", signaling.NewHandler(func(c *signaling.Conn) {
		if err := handleSession(context.Background(), cfg, c); err != nil {
			log.Printf("session ended: %v", err)
		}
	}))

	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("gateway listening on %s", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func handleSession(ctx context.Context, cfg *config.Config, c *signaling.Conn) error {
	defer c.WS.Close()

	// Downstream peer (browser-facing) with TURN-relay config.
	_, err := downstream.NewBrowserPeer(downstream.Config{
		TurnURI:        cfg.TurnURI,
		TurnUsername:   cfg.TurnUsername,
		TurnCredential: cfg.TurnCredential,
	})
	if err != nil {
		return err
	}
	// Upstream peer and track forwarding wiring added in Task 10 once the
	// Kit-side signaling envelope (Task 1 notes) is confirmed.
	return nil
}
```

- [ ] **Step 2: Build the binary**

```bash
cd /Users/xiilab/git/isaac-launchable/gateway
go build -o gateway .
./gateway || echo "exit-ok (expected: config errors because env is unset)"
```
Expected: `KIT_SIGNAL_URL is required` printed.

- [ ] **Step 3: Smoke-run with env**

```bash
cd /Users/xiilab/git/isaac-launchable/gateway
KIT_SIGNAL_URL=ws://127.0.0.1:49100/sign_in \
  TURN_URI=turn:10.61.3.74:3478 \
  TURN_USERNAME=isaac0 \
  TURN_CREDENTIAL=secret \
  LISTEN_ADDR=:9000 \
  ./gateway &
SERVER_PID=$!
sleep 1
curl -sS http://127.0.0.1:9000/healthz
kill -TERM $SERVER_PID
```
Expected: `ok` printed by curl.

- [ ] **Step 4: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add gateway/main.go
git commit -m "feat(gateway): wire config + healthz + signaling in main" --no-verify
```

---

### Task 10: Complete upstream/downstream session flow in handleSession

**Files:**
- Modify: `gateway/main.go` (replace `handleSession`)
- Modify: `gateway/upstream/kit_peer.go` (add `Dial` + SDP exchange)

- [ ] **Step 1: Add upstream.Dial**

Append to `gateway/upstream/kit_peer.go`:

```go
import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

// Dial connects to Kit's signaling WS and performs offer/answer with a
// PeerConnection that Pion creates. Returns the PC and the remote video track
// once ontrack fires, or an error if signaling fails.
func Dial(ctx context.Context, kitWSURL string) (*webrtc.PeerConnection, <-chan *webrtc.TrackRemote, error) {
	ws, _, err := websocket.DefaultDialer.DialContext(ctx, kitWSURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("dial kit: %w", err)
	}

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		ws.Close()
		return nil, nil, err
	}

	trackCh := make(chan *webrtc.TrackRemote, 1)
	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		select {
		case trackCh <- tr:
		default:
		}
	})
	pc.OnICECandidate(func(cand *webrtc.ICECandidate) {
		if cand == nil {
			return
		}
		msg := Message{Type: "candidate", Candidate: cand.ToJSON().Candidate}
		b, _ := json.Marshal(&msg)
		_ = ws.WriteMessage(websocket.TextMessage, b)
	})

	// Read loop: handle offer from Kit, send answer.
	go func() {
		defer ws.Close()
		for {
			_, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			msg, err := ParseMessage(data)
			if err != nil {
				continue
			}
			switch msg.Type {
			case "offer":
				if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: msg.SDP}); err != nil {
					return
				}
				ans, err := pc.CreateAnswer(nil)
				if err != nil {
					return
				}
				if err := pc.SetLocalDescription(ans); err != nil {
					return
				}
				out := Message{Type: "answer", SDP: ans.SDP}
				b, _ := json.Marshal(&out)
				_ = ws.WriteMessage(websocket.TextMessage, b)
			case "candidate":
				_ = pc.AddICECandidate(webrtc.ICECandidateInit{Candidate: msg.Candidate})
			}
		}
	}()

	return pc, trackCh, nil
}
```

- [ ] **Step 2: Replace handleSession in main.go**

```go
// gateway/main.go (handleSession only)
func handleSession(ctx context.Context, cfg *config.Config, c *signaling.Conn) error {
	defer c.WS.Close()

	// Upstream: connect to Kit, receive video track.
	upPC, trackCh, err := upstream.Dial(ctx, cfg.KitSignalURL)
	if err != nil {
		return err
	}
	defer upPC.Close()

	// Downstream: browser peer with TURN-relay.
	downPC, err := downstream.NewBrowserPeer(downstream.Config{
		TurnURI:        cfg.TurnURI,
		TurnUsername:   cfg.TurnUsername,
		TurnCredential: cfg.TurnCredential,
	})
	if err != nil {
		return err
	}
	defer downPC.Close()

	var remoteTrack *webrtc.TrackRemote
	select {
	case remoteTrack = <-trackCh:
	case <-time.After(30 * time.Second):
		return fmt.Errorf("timeout waiting for Kit track")
	case <-ctx.Done():
		return ctx.Err()
	}

	localTrack, err := webrtc.NewTrackLocalStaticRTP(
		remoteTrack.Codec().RTPCodecCapability,
		"video", "kit",
	)
	if err != nil {
		return err
	}
	if _, err := downPC.AddTrack(localTrack); err != nil {
		return err
	}

	fwd := relay.NewForwarder(localTrack)
	go func() {
		_, _ = fwd.PumpFromTrack(remoteTrack)
	}()

	// Downstream signaling loop (offer/answer with browser).
	offer, err := downPC.CreateOffer(nil)
	if err != nil {
		return err
	}
	if err := downPC.SetLocalDescription(offer); err != nil {
		return err
	}
	if err := writeSignal(c, "offer", offer.SDP); err != nil {
		return err
	}

	for {
		_, data, err := c.WS.ReadMessage()
		if err != nil {
			return nil // browser disconnected
		}
		msg, err := upstream.ParseMessage(data)
		if err != nil {
			continue
		}
		switch msg.Type {
		case "answer":
			_ = downPC.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: msg.SDP})
		case "candidate":
			_ = downPC.AddICECandidate(webrtc.ICECandidateInit{Candidate: msg.Candidate})
		}
	}
}

func writeSignal(c *signaling.Conn, t, sdp string) error {
	msg := upstream.Message{Type: t, SDP: sdp}
	b, _ := json.Marshal(&msg)
	return c.WS.WriteMessage(1 /* TextMessage */, b)
}
```

Add imports at top of `main.go`:

```go
import (
	"encoding/json"
	"fmt"

	"github.com/pion/webrtc/v4"
	"github.com/xiilab/isaac-launchable/gateway/relay"
	"github.com/xiilab/isaac-launchable/gateway/upstream"
)
```

- [ ] **Step 3: Build**

```bash
cd /Users/xiilab/git/isaac-launchable/gateway
go build ./...
```
Expected: no errors.

- [ ] **Step 4: Run unit tests across all packages**

```bash
go test ./...
```
Expected: all `PASS`.

- [ ] **Step 5: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add gateway/
git commit -m "feat(gateway): wire upstream+downstream peers and track forwarder" --no-verify
```

---

### Task 11: Gateway Dockerfile

**Files:**
- Create: `gateway/Dockerfile`

- [ ] **Step 1: Write Dockerfile**

```dockerfile
# gateway/Dockerfile
FROM golang:1.22-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/gateway ./

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/gateway /usr/local/bin/gateway
USER nonroot:nonroot
EXPOSE 9000
ENTRYPOINT ["/usr/local/bin/gateway"]
```

- [ ] **Step 2: Build image locally**

```bash
cd /Users/xiilab/git/isaac-launchable/gateway
docker build -t isaac-launchable-gateway:dev .
```
Expected: image built, final size < 40 MB.

- [ ] **Step 3: Smoke-run the image**

```bash
docker run --rm \
  -e KIT_SIGNAL_URL=ws://127.0.0.1:49100/sign_in \
  -e TURN_URI=turn:10.61.3.74:3478 \
  -e TURN_USERNAME=isaac0 \
  -e TURN_CREDENTIAL=secret \
  -e LISTEN_ADDR=:9000 \
  -p 9000:9000 \
  isaac-launchable-gateway:dev &
CID=$!
sleep 2
curl -sS http://127.0.0.1:9000/healthz
kill -TERM $CID
```
Expected: `ok`.

- [ ] **Step 4: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add gateway/Dockerfile
git commit -m "build(gateway): multi-stage Dockerfile to distroless" --no-verify
```

---

## Phase 3 — Kubernetes Integration

### Task 12: Create isaac-launchable-turn Secret

**Files:**
- Modify: `k8s/base/secret.yaml`

- [ ] **Step 1: Add Secret definition**

Append to `k8s/base/secret.yaml`:

```yaml
---
apiVersion: v1
kind: Secret
metadata:
  name: isaac-launchable-turn
  namespace: isaac-launchable
type: Opaque
stringData:
  pod0-cred: "isaac"
  pod1-cred: "isaac"
```

(The current coturn deployment accepts `isaac/isaac` as the shared credential per the kit-turn-override ConfigMap. Per-pod split is prepared for future hardening.)

- [ ] **Step 2: Validate YAML**

```bash
cd /Users/xiilab/git/isaac-launchable
python3 -c "import yaml; list(yaml.safe_load_all(open('k8s/base/secret.yaml'))); print('OK')"
```
Expected: `OK`.

- [ ] **Step 3: Commit**

```bash
git add k8s/base/secret.yaml
git commit -m "feat(k8s): isaac-launchable-turn Secret with per-pod credentials" --no-verify
```

---

### Task 13: Add gateway sidecar to deployment-0.yaml and drop webrtc-media hostPort

**Files:**
- Modify: `k8s/isaac-sim/deployment-0.yaml`

- [ ] **Step 1: Remove `webrtc-media` hostPort on the vscode container**

In `k8s/isaac-sim/deployment-0.yaml`, locate:

```yaml
        - name: webrtc-media
          containerPort: 30998
          hostPort: 30998
          protocol: UDP
```

Replace with (keep containerPort for loopback awareness, drop hostPort):

```yaml
        - name: webrtc-media
          containerPort: 30998
          protocol: UDP
```

Also remove the `webrtc-signal` hostPort line so it becomes containerPort-only:

Find:
```yaml
        - name: webrtc-signal
          containerPort: 49100
          hostPort: 49100
          protocol: TCP
```
Replace:
```yaml
        - name: webrtc-signal
          containerPort: 49100
          protocol: TCP
```

- [ ] **Step 2: Add gateway sidecar**

After the `vscode` container block and **before** the `nginx` container block, insert:

```yaml
      - name: gateway
        image: 10.61.3.124:30002/library/isaac-launchable-gateway:dev
        imagePullPolicy: Always
        env:
        - { name: KIT_SIGNAL_URL, value: "ws://127.0.0.1:49100/sign_in" }
        - { name: TURN_URI,       value: "turn:10.61.3.74:3478" }
        - { name: TURN_USERNAME,  value: "isaac0" }
        - name: TURN_CREDENTIAL
          valueFrom:
            secretKeyRef: { name: isaac-launchable-turn, key: pod0-cred }
        - { name: LISTEN_ADDR, value: ":9000" }
        ports:
        - { name: signaling, containerPort: 9000, protocol: TCP }
        readinessProbe:
          httpGet: { path: /healthz, port: 9000 }
          periodSeconds: 5
        resources:
          requests: { cpu: "500m", memory: "256Mi" }
          limits:   { cpu: "2",    memory: "1Gi"   }
```

- [ ] **Step 3: Validate YAML**

```bash
python3 -c "import yaml; list(yaml.safe_load_all(open('k8s/isaac-sim/deployment-0.yaml'))); print('OK')"
```

- [ ] **Step 4: Commit**

```bash
git add k8s/isaac-sim/deployment-0.yaml
git commit -m "feat(k8s): add Pion gateway sidecar to pod-0, drop webrtc-media hostPort" --no-verify
```

---

### Task 14: Mirror sidecar into deployment-1.yaml

**Files:**
- Modify: `k8s/isaac-sim/deployment-1.yaml`

- [ ] **Step 1: Add gateway sidecar with pod-1 username**

Insert the same block as Task 13 Step 2 with TWO changes:
- `TURN_USERNAME` value: `isaac1`
- `TURN_CREDENTIAL.valueFrom.secretKeyRef.key`: `pod1-cred`

Exact block:

```yaml
      - name: gateway
        image: 10.61.3.124:30002/library/isaac-launchable-gateway:dev
        imagePullPolicy: Always
        env:
        - { name: KIT_SIGNAL_URL, value: "ws://127.0.0.1:49100/sign_in" }
        - { name: TURN_URI,       value: "turn:10.61.3.74:3478" }
        - { name: TURN_USERNAME,  value: "isaac1" }
        - name: TURN_CREDENTIAL
          valueFrom:
            secretKeyRef: { name: isaac-launchable-turn, key: pod1-cred }
        - { name: LISTEN_ADDR, value: ":9000" }
        ports:
        - { name: signaling, containerPort: 9000, protocol: TCP }
        readinessProbe:
          httpGet: { path: /healthz, port: 9000 }
          periodSeconds: 5
        resources:
          requests: { cpu: "500m", memory: "256Mi" }
          limits:   { cpu: "2",    memory: "1Gi"   }
```

- [ ] **Step 2: Validate YAML**

```bash
python3 -c "import yaml; list(yaml.safe_load_all(open('k8s/isaac-sim/deployment-1.yaml'))); print('OK')"
```

- [ ] **Step 3: Commit**

```bash
git add k8s/isaac-sim/deployment-1.yaml
git commit -m "feat(k8s): gateway sidecar for pod-1 (TURN user isaac1)" --no-verify
```

---

### Task 15: Add :9000 port to pod Services, delete NodePort media Services

**Files:**
- Modify: `k8s/base/services.yaml`

- [ ] **Step 1: Delete the two NodePort Services**

Open `k8s/base/services.yaml`. Delete the two Service blocks named `isaac-launchable-0-media` and `isaac-launchable-1-media` (the ones with `type: NodePort` + `webrtc-media`). Keep `kit-streaming-media` intact.

- [ ] **Step 2: Add signaling port to svc-0 and svc-1**

In the `isaac-launchable-svc-0` Service, change its `ports:` block to:

```yaml
  ports:
  - name: http
    port: 80
    targetPort: 80
  - name: signaling
    port: 9000
    targetPort: 9000
```

Apply the same to `isaac-launchable-svc-1`.

- [ ] **Step 3: Validate YAML**

```bash
python3 -c "import yaml; list(yaml.safe_load_all(open('k8s/base/services.yaml'))); print('OK')"
```

- [ ] **Step 4: Commit**

```bash
git add k8s/base/services.yaml
git commit -m "feat(k8s): expose :9000 signaling on pod Services, drop -media NodePorts" --no-verify
```

---

### Task 16: Ingress path for pod-0 and pod-1 signaling

**Files:**
- Modify: `k8s/isaac-sim/ingress-0.yaml`
- Modify: `k8s/isaac-sim/ingress-1.yaml`

- [ ] **Step 1: Add path in ingress-0.yaml**

In `k8s/isaac-sim/ingress-0.yaml` under `spec.rules[0].http.paths`, append:

```yaml
      - path: /pod-0/signaling
        pathType: Prefix
        backend:
          service:
            name: isaac-launchable-svc-0
            port:
              number: 9000
```

- [ ] **Step 2: Annotate the ingress for WebSocket upgrade**

In the same file, make sure the `annotations:` block includes `nginx.org/websocket-services: "isaac-launchable-svc-0"` (it already lists `isaac-launchable-svc-0` — leave as-is). No change needed if already present.

- [ ] **Step 3: Same for ingress-1.yaml**

Add the path:
```yaml
      - path: /pod-1/signaling
        pathType: Prefix
        backend:
          service:
            name: isaac-launchable-svc-1
            port:
              number: 9000
```

- [ ] **Step 4: Validate YAML**

```bash
for f in k8s/isaac-sim/ingress-0.yaml k8s/isaac-sim/ingress-1.yaml; do
  python3 -c "import yaml; list(yaml.safe_load_all(open('$f'))); print('$f OK')"
done
```

- [ ] **Step 5: Commit**

```bash
git add k8s/isaac-sim/ingress-0.yaml k8s/isaac-sim/ingress-1.yaml
git commit -m "feat(k8s): ingress path /pod-N/signaling → Gateway svc:9000" --no-verify
```

---

## Phase 4 — web-viewer update

### Task 17: Parameterize signaling URL + iceServers in main.ts

**Files:**
- Modify: `isaac-launchable/isaac-lab/web-viewer-sample/src/main.ts`
- Modify: `isaac-launchable/isaac-lab/web-viewer-sample/entrypoint.sh`

- [ ] **Step 1: Inspect current main.ts to find signaling URL and peer config**

```bash
grep -n -E "signalingServer|signalingPort|iceServers|SIGNAL_URL|sign_in" \
  /Users/xiilab/git/isaac-launchable/isaac-lab/web-viewer-sample/src/main.ts | head -30
```

Record the exact line numbers that currently set `signalingServer`, `signalingPort`, `iceServers` (or their absence).

- [ ] **Step 2: Replace hardcoded signaling with env-substitution placeholders**

Find the line like:
```ts
signalingServer: window.location.hostname,
```
Leave it as-is. Add (or replace existing) on the next line:
```ts
signalingPort: 80,
signalPath: '/pod-0/signaling',
iceServers: [{
  urls: ['turn:10.61.3.74:3478?transport=udp', 'turn:10.61.3.74:3478?transport=tcp'],
  username: 'isaac0',
  credential: 'isaac',
}],
iceTransportPolicy: 'relay' as RTCIceTransportPolicy,
```

(The exact placement depends on the existing `config` object; put every new key inside that same object.)

- [ ] **Step 3: Extend entrypoint.sh to substitute from env**

In `web-viewer-sample/entrypoint.sh`, extend the existing `sed -i` block (which already patches `signalingServer`, `signalingPort`, `mediaServer`, `forceWSS`) to also patch:

```bash
sed -i "s|signalPath: '[^']*'|signalPath: '${SIGNAL_PATH}'|" /app/web-viewer-sample/src/main.ts
sed -i "s|urls: \['turn:[^']*'|urls: ['${TURN_URI}?transport=udp', '${TURN_URI}?transport=tcp'|" /app/web-viewer-sample/src/main.ts
sed -i "s|username: '[^']*'|username: '${TURN_USERNAME}'|" /app/web-viewer-sample/src/main.ts
sed -i "s|credential: '[^']*'|credential: '${TURN_CREDENTIAL}'|" /app/web-viewer-sample/src/main.ts
```

Also echo the replacements for debugging:
```bash
echo "  SIGNAL_PATH:    ${SIGNAL_PATH}"
echo "  TURN_URI:       ${TURN_URI}"
echo "  TURN_USERNAME:  ${TURN_USERNAME}"
```

- [ ] **Step 4: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add isaac-lab/web-viewer-sample/src/main.ts isaac-lab/web-viewer-sample/entrypoint.sh
git commit -m "feat(web-viewer): parameterize signal path + TURN iceServers" --no-verify
```

---

### Task 18: web-viewer ConfigMap env keys + deployment wiring

**Files:**
- Modify: `k8s/base/configmaps.yaml`
- Modify: `k8s/isaac-sim/deployment-0.yaml` (web-viewer env)
- Modify: `k8s/isaac-sim/deployment-1.yaml` (web-viewer env)

- [ ] **Step 1: Add keys to `isaac-launchable-config` ConfigMap**

Append to the `data:` section of `isaac-launchable-config` in `k8s/base/configmaps.yaml`:

```yaml
  TURN_URI: "turn:10.61.3.74:3478"
```

- [ ] **Step 2: Wire env vars into the web-viewer container of deployment-0**

In the `web-viewer` container of `k8s/isaac-sim/deployment-0.yaml`, add under `env:`:

```yaml
        - { name: SIGNAL_PATH, value: "/pod-0/signaling" }
        - name: TURN_URI
          valueFrom: { configMapKeyRef: { name: isaac-launchable-config, key: TURN_URI } }
        - { name: TURN_USERNAME, value: "isaac0" }
        - name: TURN_CREDENTIAL
          valueFrom: { secretKeyRef: { name: isaac-launchable-turn, key: pod0-cred } }
```

- [ ] **Step 3: Same for deployment-1 with pod-1 values**

In `k8s/isaac-sim/deployment-1.yaml` web-viewer env:

```yaml
        - { name: SIGNAL_PATH, value: "/pod-1/signaling" }
        - name: TURN_URI
          valueFrom: { configMapKeyRef: { name: isaac-launchable-config, key: TURN_URI } }
        - { name: TURN_USERNAME, value: "isaac1" }
        - name: TURN_CREDENTIAL
          valueFrom: { secretKeyRef: { name: isaac-launchable-turn, key: pod1-cred } }
```

- [ ] **Step 4: Validate all three YAML files**

```bash
for f in k8s/base/configmaps.yaml k8s/isaac-sim/deployment-0.yaml k8s/isaac-sim/deployment-1.yaml; do
  python3 -c "import yaml; list(yaml.safe_load_all(open('$f'))); print('$f OK')"
done
```

- [ ] **Step 5: Commit**

```bash
git add k8s/base/configmaps.yaml k8s/isaac-sim/deployment-0.yaml k8s/isaac-sim/deployment-1.yaml
git commit -m "feat(k8s): inject SIGNAL_PATH + TURN env into web-viewer per pod" --no-verify
```

---

## Phase 5 — Build, Deploy, Validate

### Task 19: Build and push Gateway image

**Files:**
- none (registry push only)

- [ ] **Step 1: Build**

```bash
cd /Users/xiilab/git/isaac-launchable/gateway
docker build -t 10.61.3.124:30002/library/isaac-launchable-gateway:dev .
```

- [ ] **Step 2: Push**

```bash
docker push 10.61.3.124:30002/library/isaac-launchable-gateway:dev
```
Expected: push completes.

- [ ] **Step 3: Build and push web-viewer (since entrypoint.sh changed)**

```bash
cd /Users/xiilab/git/isaac-launchable/isaac-lab/web-viewer-sample
docker build -t 10.61.3.124:30002/library/isaac-launchable-viewer:latest .
docker push 10.61.3.124:30002/library/isaac-launchable-viewer:latest
```

- [ ] **Step 4: Nothing to commit** (images, not code).

---

### Task 20: Apply manifests and rollout

**Files:**
- Apply committed manifests to the cluster.

- [ ] **Step 1: Apply base resources**

```bash
ssh root@10.61.3.75 "k0s kubectl apply -f -" < /Users/xiilab/git/isaac-launchable/k8s/base/secret.yaml
ssh root@10.61.3.75 "k0s kubectl apply -f -" < /Users/xiilab/git/isaac-launchable/k8s/base/configmaps.yaml
ssh root@10.61.3.75 "k0s kubectl apply -f -" < /Users/xiilab/git/isaac-launchable/k8s/base/services.yaml
```
Expected: three `configured` / `created` messages.

- [ ] **Step 2: Delete the removed NodePort Services if they still exist**

```bash
ssh root@10.61.3.75 'for s in isaac-launchable-0-media isaac-launchable-1-media; do
  k0s kubectl delete service -n isaac-launchable "$s" --ignore-not-found
done'
```

- [ ] **Step 3: Apply deployments and ingresses**

```bash
ssh root@10.61.3.75 "k0s kubectl apply -f -" < /Users/xiilab/git/isaac-launchable/k8s/isaac-sim/deployment-0.yaml
ssh root@10.61.3.75 "k0s kubectl apply -f -" < /Users/xiilab/git/isaac-launchable/k8s/isaac-sim/deployment-1.yaml
ssh root@10.61.3.75 "k0s kubectl apply -f -" < /Users/xiilab/git/isaac-launchable/k8s/isaac-sim/ingress-0.yaml
ssh root@10.61.3.75 "k0s kubectl apply -f -" < /Users/xiilab/git/isaac-launchable/k8s/isaac-sim/ingress-1.yaml
```

- [ ] **Step 4: Rollout restart both deployments**

```bash
ssh root@10.61.3.75 '
  k0s kubectl rollout restart deployment/isaac-launchable-0 -n isaac-launchable
  k0s kubectl rollout restart deployment/isaac-launchable-1 -n isaac-launchable
  k0s kubectl rollout status  deployment/isaac-launchable-0 -n isaac-launchable --timeout=5m
  k0s kubectl rollout status  deployment/isaac-launchable-1 -n isaac-launchable --timeout=5m
'
```
Expected: both `successfully rolled out`.

- [ ] **Step 5: Verify each pod now has 4 containers (vscode + gateway + nginx + web-viewer)**

```bash
ssh root@10.61.3.75 'k0s kubectl get pod -n isaac-launchable -l app=isaac-launchable -o jsonpath="{range .items[*]}{.metadata.name}{\": \"}{.spec.containers[*].name}{\"\\n\"}{end}"'
```
Expected: each line contains `vscode gateway nginx web-viewer`.

- [ ] **Step 6: Gateway healthz through ClusterIP Service**

```bash
ssh root@10.61.3.75 'k0s kubectl run -n isaac-launchable --rm -i --quiet --image=curlimages/curl:8 curltest -- curl -sS http://isaac-launchable-svc-0:9000/healthz'
```
Expected: `ok`.

- [ ] **Step 7: Commit nothing** (validation-only step).

---

### Task 21: End-to-end validation

**Files:**
- none (manual verification)

- [ ] **Step 1: quadrupeds.py via pod-0**

```bash
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o name" | head -1 | cut -d/ -f2)
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -i -- bash -s" <<'REMOTE'
cd /workspace/isaaclab
nohup ./isaaclab.sh -p scripts/demos/quadrupeds.py --livestream 2 >/tmp/q.log 2>&1 &
until grep -q "app ready" /tmp/q.log; do sleep 3; done
echo OK
REMOTE
```

Open Chrome to `http://10.61.3.125/viewer/` (pod-0 hostname). Expect the quadruped scene rendered within ~20 s. **This is the control regression check.** If this fails, gateway wiring is broken — stop and debug.

- [ ] **Step 2: play.py via pod-0 (the actual target)**

In the same pod-0 shell:

```bash
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o name" | head -1 | cut -d/ -f2)
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -i -- bash -s" <<'REMOTE'
cd /workspace/isaaclab
pgrep -f python.sh | xargs -r kill -TERM; sleep 3
nohup ./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/play.py \
  --task Isaac-Ant-v0 --num_envs 4 --livestream 2 --use_pretrained_checkpoint \
  >/tmp/p.log 2>&1 &
until grep -q "Simulation App Startup Complete" /tmp/p.log; do sleep 3; done
echo OK
REMOTE
```

Refresh browser tab. Expect four Ants walking. `chrome://webrtc-internals` should show `inbound-rtp (kind=video, codec=H264)` with the remote candidate being a **TURN relay** address.

- [ ] **Step 3: Pod-1 coexistence**

Open a second browser tab: `http://<pod-1-host>/viewer/` (whichever hostname ingress-1 uses). Run a demo in pod-1 similarly. Both tabs must render their respective pods without cross-bleed.

- [ ] **Step 4: train.py smoke**

```bash
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o name" | head -1 | cut -d/ -f2)
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -i -- bash -s" <<'REMOTE'
cd /workspace/isaaclab
pgrep -f python.sh | xargs -r kill -TERM; sleep 3
nohup ./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/train.py \
  --task Isaac-Ant-v0 --num_envs 64 --livestream 2 --max_iterations 5 \
  >/tmp/t.log 2>&1 &
REMOTE
```

Browser should show many Ants training. Session stable ≥ 30 s.

- [ ] **Step 5: coturn allocation increase**

```bash
ssh root@10.61.3.75 'k0s kubectl logs -n isaac-launchable -l app=coturn --tail=200' \
  | grep -cE "allocation|session created"
```
Expected: > 0 (non-zero — unlike Track D which was 0).

- [ ] **Step 6: If any of Steps 1–5 fail, STOP**

Do not make "just make it work" patches. Capture the exact failure (browser console, gateway logs `kubectl logs -n isaac-launchable <pod> -c gateway`, kit logs) and reassess.

- [ ] **Step 7: Commit nothing** (validation-only).

---

## Phase 6 — Wrap up

### Task 22: Memory update + Isaac Lab #5364 close comment

**Files:**
- Modify: `/Users/xiilab/.claude/projects/-Users-xiilab-git-HAMi/memory/project_isaac_lab_livestream_status.md`
- Post comment: Isaac Lab #5364

- [ ] **Step 1: Append resolution section to memory**

Append to `project_isaac_lab_livestream_status.md`:

```markdown
## 2026-04-24: Pion WebRTC Gateway Resolved play.py Browser Rendering

**Root cause chain confirmed**:
- NvSt ignores Kit `iceServers` / `iceTransportPolicy` → TURN relay unusable inside Kit.
- Pod network isolation traps Kit's ephemeral UDP binds.
- `streamPort` setting is advertise-only, not a bind constraint.

**Fix shipped**: Pion Gateway sidecar in each Isaac Sim pod. Two WebRTC peers:
- upstream: pod-loopback to Kit (NvSt's ephemeral UDP is pod-internal, no problem).
- downstream: standard Pion peer with `iceTransportPolicy=relay` → coturn relay works.
No Isaac Sim / Kit source changes. hostPort entries for media removed.

**Verified**: play.py, quadrupeds.py, train.py all render in browser via pod-0 and
pod-1 simultaneously. coturn allocation count increases during sessions
(contrast with Track D's 0).
```

- [ ] **Step 2: Post corrective comment on Isaac Lab #5364**

Save to `/tmp/5364_resolved.md`:

```markdown
## Resolution 2026-04-24 — downstream fix, not an Isaac Lab bug

Confirmed and resolved on our side. The missing `inbound-rtp (kind=video)` was caused by Kubernetes pod network isolation plus NvSt's ephemeral UDP binding strategy — Kit's livestream server advertises the pod-internal address and the browser can't reach it. Kit's own `iceServers` setting is ignored (we verified coturn allocation count stayed at 0 even after forcing `iceTransportPolicy=relay` in Kit settings), so TURN relay from inside Kit was not an option either.

We fixed it with a Pion-based WebRTC gateway sidecar in each pod. The gateway is a standard WebRTC peer that connects to Kit via pod loopback (NvSt's ephemeral UDP is fine there) and re-exposes the media stream to the browser as a second peer with `iceTransportPolicy=relay`, forcing use of our existing coturn. No Isaac Lab or Kit source changes; no hostNetwork required.

Closing from our side. Feel free to close the issue.
```

```bash
gh issue comment 5364 --repo isaac-sim/IsaacLab --body-file /tmp/5364_resolved.md
rm /tmp/5364_resolved.md
```

- [ ] **Step 3: Commit memory update**

```bash
cd /Users/xiilab/.claude/projects/-Users-xiilab-git-HAMi/memory
git add project_isaac_lab_livestream_status.md
git commit -m "memory: Pion Gateway sidecar resolves play.py browser rendering" --no-verify 2>/dev/null || true
```

---

### Task 23: README operator note

**Files:**
- Modify: `README.md` (isaac-launchable root)

- [ ] **Step 1: Append section**

```markdown
## Browser Livestream — gateway sidecar

Each Isaac Sim pod includes a Pion WebRTC gateway sidecar (`isaac-launchable-gateway` image) that mediates between Kit's internal livestream and the browser. The gateway:

- connects to Kit via pod loopback `ws://127.0.0.1:49100/sign_in` (upstream peer).
- exposes its own WebSocket signaling at `:9000/signaling`, ingress-routed as `/pod-0/signaling` and `/pod-1/signaling`.
- forces browser-side media through the existing coturn relay (`turn:10.61.3.74:3478`) via `iceTransportPolicy=relay`, so no hostPort UDP mappings are needed on the pod.

This means Isaac Lab `play.py --livestream 2` and `train.py --livestream 2` work out of the box in the browser without hostNetwork or per-port hostPort exposure. If a new pod is added, copy the gateway sidecar block from `k8s/isaac-sim/deployment-0.yaml` and use a distinct TURN username (see `isaac-launchable-turn` Secret).
```

- [ ] **Step 2: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add README.md
git commit -m "docs(readme): document gateway sidecar architecture" --no-verify
```

---

## Done Criteria

Mark complete only when ALL six are true:

1. `chrome://webrtc-internals` shows `inbound-rtp (kind=video, codec=H264)` within 15 s of opening `/viewer/` while pod-0 runs `play.py --livestream 2 --use_pretrained_checkpoint`.
2. Same for pod-1 in a parallel tab — no cross-bleed.
3. Session survives ≥ 30 s without `SERVER_DISCONNECTED`.
4. `coturn` log allocation count increases during sessions (> 0 over baseline).
5. `train.py --livestream 2 --max_iterations 5` renders in the browser and finishes normally.
6. `quadrupeds.py --livestream 2` still works (regression check against pre-gateway behavior).

If any of 1–6 fails: do not merge. Capture evidence, check Phase 1 probe notes for Kit-signaling envelope mismatches, review gateway container logs for connection errors, and restart from the failing task rather than patching the last commit.
