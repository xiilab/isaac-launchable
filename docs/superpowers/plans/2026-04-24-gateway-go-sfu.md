# Go Pion Gateway (SFU with SDP munging) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 현재 Node.js signaling-only proxy 를 **Go Pion 기반 SFU 게이트웨이** 로 교체하여 `play.py --livestream 2` / `train.py --livestream 2` 가 `hostNetwork=false` + pod-0 환경에서 브라우저 뷰포트로 실시간 렌더링되도록 한다.

**Architecture:** Gateway 컨테이너가 WebRTC peer 두 개를 운영 — upstream peer 는 pod 내부 loopback 으로 Kit 의 NVST signaling 에 접속(Kit 의 offer/answer/candidate envelope 은 proxy 를 통해 전달하되 SDP 를 intercept), downstream peer 는 자체 NVST-envelope signaling 으로 브라우저와 통신하며 `iceTransportPolicy=relay` + coturn 강제. 두 peer 사이는 RTP track-forwarding.

**Tech Stack:** Go 1.22, `github.com/pion/webrtc/v4`, `github.com/pion/sdp/v3`, `github.com/gorilla/websocket`, Docker, k0s, 기존 coturn (10.61.3.74:3478).

**Spec:** `docs/superpowers/specs/2026-04-24-webrtc-gateway-pion-design.md` §11 (commit HEAD 갱신)

---

## File Structure

**Will create (gateway-go/):**
- `gateway-go/go.mod`, `gateway-go/go.sum`
- `gateway-go/.gitignore`
- `gateway-go/cmd/gateway/main.go` — entry point
- `gateway-go/internal/config/config.go` — env 파싱
- `gateway-go/internal/config/config_test.go`
- `gateway-go/internal/nvst/envelope.go` — NVST 메시지 envelope (JSON) parse/encode
- `gateway-go/internal/nvst/envelope_test.go`
- `gateway-go/internal/sdp/munger.go` — SDP candidate/fingerprint/ufrag rewrite
- `gateway-go/internal/sdp/munger_test.go`
- `gateway-go/internal/upstream/kit_peer.go` — Gateway ↔ Kit Pion peer
- `gateway-go/internal/upstream/kit_peer_test.go`
- `gateway-go/internal/downstream/browser_peer.go` — Gateway ↔ Browser Pion peer (TURN relay)
- `gateway-go/internal/downstream/browser_peer_test.go`
- `gateway-go/internal/relay/forwarder.go` — RTP track pass-through
- `gateway-go/internal/relay/forwarder_test.go`
- `gateway-go/internal/session/session.go` — per-browser session (upstream+downstream+relay wiring)
- `gateway-go/internal/session/session_test.go`
- `gateway-go/Dockerfile` — multi-stage, distroless/static runtime
- `gateway-go/README.md`

**Will modify:**
- `k8s/isaac-sim/deployment-0.yaml` — gateway image tag 변경 only (Node.js → Go)

**Will NOT modify (현행 유지):**
- `gateway/` (Node.js simple-proxy, 롤백 대비 보존)
- `k8s/base/services.yaml`, `secret.yaml`, `configmaps.yaml`, `ingress-0.yaml` — 모두 기존 그대로
- `isaac-lab/web-viewer-sample/` — entrypoint.sh 의 RTCPeerConnection override 현행 유지 (TURN relay 강제)
- Isaac Sim 이미지, runheadless.sh, coturn

---

## Phase A — Foundation

### Task C2: Scaffold Go module

**Files:**
- Create: `gateway-go/go.mod`, `gateway-go/.gitignore`, `gateway-go/cmd/gateway/main.go`

- [ ] **Step 1: Create directory + init module**

```bash
cd /Users/xiilab/git/isaac-launchable
mkdir -p gateway-go/cmd/gateway gateway-go/internal
cd gateway-go
go mod init github.com/xiilab/isaac-launchable/gateway-go
```
Expected: `go.mod` created with `module github.com/xiilab/isaac-launchable/gateway-go`, `go 1.22`.

- [ ] **Step 2: Add dependencies**

```bash
cd /Users/xiilab/git/isaac-launchable/gateway-go
go get github.com/pion/webrtc/v4
go get github.com/pion/sdp/v3
go get github.com/gorilla/websocket
go mod tidy
```
Expected: go.sum populated with pion/webrtc, pion/sdp, gorilla/websocket and transitive deps.

- [ ] **Step 3: Placeholder main**

```go
// gateway-go/cmd/gateway/main.go
package main

import "log"

func main() {
	log.Println("gateway-go: starting (stub)")
}
```

- [ ] **Step 4: .gitignore**

```
/bin/
/gateway
*.test
coverage.out
```

- [ ] **Step 5: Build verification**

```bash
cd /Users/xiilab/git/isaac-launchable/gateway-go
go build ./...
```
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add gateway-go/
git commit -m "feat(gateway-go): scaffold Go module with Pion + gorilla/websocket"
```

---

## Phase B — Protocol Primitives

### Task C3: Config loader

**Files:**
- Create: `gateway-go/internal/config/config.go`, `gateway-go/internal/config/config_test.go`

- [ ] **Step 1: Write failing test**

```go
// gateway-go/internal/config/config_test.go
package config

import (
	"testing"
)

func TestLoad_AllEnv(t *testing.T) {
	t.Setenv("KIT_SIGNAL_URL", "ws://127.0.0.1:49100")
	t.Setenv("TURN_URI", "turn:10.61.3.74:3478")
	t.Setenv("TURN_USERNAME", "isaac")
	t.Setenv("TURN_CREDENTIAL", "secret")
	t.Setenv("LISTEN_ADDR", ":9000")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.KitSignalURL != "ws://127.0.0.1:49100" {
		t.Errorf("KitSignalURL = %q", cfg.KitSignalURL)
	}
	if cfg.TurnURI != "turn:10.61.3.74:3478" {
		t.Errorf("TurnURI = %q", cfg.TurnURI)
	}
	if cfg.TurnUsername != "isaac" {
		t.Errorf("TurnUsername = %q", cfg.TurnUsername)
	}
	if cfg.TurnCredential != "secret" {
		t.Errorf("TurnCredential = %q", cfg.TurnCredential)
	}
	if cfg.ListenAddr != ":9000" {
		t.Errorf("ListenAddr = %q", cfg.ListenAddr)
	}
}

func TestLoad_MissingKitSignal(t *testing.T) {
	t.Setenv("TURN_URI", "turn:10.61.3.74:3478")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for missing KIT_SIGNAL_URL")
	}
}

func TestLoad_DefaultListenAddr(t *testing.T) {
	t.Setenv("KIT_SIGNAL_URL", "ws://127.0.0.1:49100")
	t.Setenv("TURN_URI", "turn:10.61.3.74:3478")
	t.Setenv("TURN_USERNAME", "isaac")
	t.Setenv("TURN_CREDENTIAL", "secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddr != ":9000" {
		t.Errorf("ListenAddr default = %q, want :9000", cfg.ListenAddr)
	}
}
```

- [ ] **Step 2: Run test (expect build failure)**

```bash
cd /Users/xiilab/git/isaac-launchable/gateway-go
go test ./internal/config/
```
Expected: undefined `Load`, `Config`.

- [ ] **Step 3: Implement**

```go
// gateway-go/internal/config/config.go
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
		return nil, fmt.Errorf("KIT_SIGNAL_URL required")
	}
	if cfg.TurnURI == "" {
		return nil, fmt.Errorf("TURN_URI required")
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":9000"
	}
	return cfg, nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/config/
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add gateway-go/internal/config/
git commit -m "feat(gateway-go): config loader with env validation"
```

---

### Task C4: NVST envelope parser

NVST signaling 은 JSON envelope 기반. 관찰된 타입: `config`, offer/answer/candidate 는 브라우저 라이브러리의 emit 패턴에서 확정 필요. 일단 **범용 parser** 로 raw 필드 보존 + known type 분기.

**Files:**
- Create: `gateway-go/internal/nvst/envelope.go`, `envelope_test.go`

- [ ] **Step 1: Write test**

```go
// gateway-go/internal/nvst/envelope_test.go
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
	if m.Candidate() == "" {
		t.Error("Candidate empty")
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
	_ = json.Unmarshal(raw, &a)
	_ = json.Unmarshal(out, &b)
	if !equalJSON(a, b) {
		t.Errorf("roundtrip mismatch:\n got  %s\n want %s", out, raw)
	}
}

func equalJSON(a, b map[string]any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

func TestReplaceSDP(t *testing.T) {
	raw := []byte(`{"type":"offer","sdp":"old"}`)
	m, _ := Parse(raw)
	m.SetSDP("new")
	out, _ := m.Encode()
	var decoded map[string]any
	_ = json.Unmarshal(out, &decoded)
	if decoded["sdp"] != "new" {
		t.Errorf("sdp = %v, want new", decoded["sdp"])
	}
}
```

- [ ] **Step 2: Run test — expect build failure**

```bash
go test ./internal/nvst/
```

- [ ] **Step 3: Implement**

```go
// gateway-go/internal/nvst/envelope.go
package nvst

import (
	"encoding/json"
	"fmt"
)

type Kind int

const (
	KindOther Kind = iota
	KindOffer
	KindAnswer
	KindCandidate
)

type Message struct {
	raw map[string]any
}

func Parse(data []byte) (*Message, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("nvst parse: %w", err)
	}
	return &Message{raw: raw}, nil
}

func (m *Message) Kind() Kind {
	t, _ := m.raw["type"].(string)
	switch t {
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

func (m *Message) Type() string {
	t, _ := m.raw["type"].(string)
	return t
}

func (m *Message) SDP() string {
	s, _ := m.raw["sdp"].(string)
	return s
}

func (m *Message) SetSDP(sdp string) {
	m.raw["sdp"] = sdp
}

func (m *Message) Candidate() string {
	c, _ := m.raw["candidate"].(string)
	return c
}

func (m *Message) SetCandidate(c string) {
	m.raw["candidate"] = c
}

func (m *Message) SdpMid() string {
	s, _ := m.raw["sdpMid"].(string)
	return s
}

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

func (m *Message) Encode() ([]byte, error) {
	return json.Marshal(m.raw)
}

func (m *Message) Raw() map[string]any {
	return m.raw
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/nvst/
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add gateway-go/internal/nvst/
git commit -m "feat(gateway-go): NVST signaling envelope parser with type classification"
```

---

## Phase C — Core Plumbing

### Task C5: Signaling proxy pump

현재 Node.js `gateway/main.js` 의 proxy 로직을 Go 로 포팅. Browser WS ↔ Upstream (Kit) WS 양방향 frame pump + `NewHandler` callback 으로 offer/answer intercept hook.

**Files:**
- Create: `gateway-go/internal/proxy/proxy.go`, `proxy_test.go`

(상세 TDD 단계는 상기 패턴과 동일 — 생략. 테스트는 loopback WS 서버 2개 세우고 frame round-trip 검증.)

핵심 시그니처:
```go
type SessionFactory func(browserWS, kitWS *websocket.Conn) error

func Handler(kitURL string, factory SessionFactory) http.Handler
```

**커밋**:
```bash
git add gateway-go/internal/proxy/
git commit -m "feat(gateway-go): WS signaling proxy with session factory hook"
```

---

### Task C6: SDP munger

`pion/sdp/v3` 기반. 입력 SDP 를 parse → ICE candidate lines, ice-ufrag/pwd, DTLS fingerprint 를 `ReplaceWith` 로 교체. 출력 SDP marshalling.

**Files:**
- Create: `gateway-go/internal/sdp/munger.go`, `munger_test.go`

테스트 샘플 SDP 는 WebRTC 표준 H264 offer 사용 (multi m-section). `pion/sdp` 의 `SessionDescription.Marshal` round-trip 검증.

**커밋**:
```bash
git add gateway-go/internal/sdp/
git commit -m "feat(gateway-go): SDP munger for ICE/DTLS rewrite"
```

---

### Task C7: Upstream Pion peer (Gateway ↔ Kit)

Gateway 가 Kit 의 offer 를 receive → Pion PeerConnection 으로 answer 생성. Kit 의 ephemeral UDP candidates 를 loopback 에서 그대로 사용 (NO relay 강제).

**Files:**
- Create: `gateway-go/internal/upstream/kit_peer.go`, `kit_peer_test.go`

핵심 API:
```go
type KitPeer struct {
	pc     *webrtc.PeerConnection
	tracks chan *webrtc.TrackRemote
}

func NewKitPeer() (*KitPeer, error)
func (p *KitPeer) HandleOffer(sdp string) (answer string, err error)
func (p *KitPeer) AddCandidate(candidate string, sdpMid string, sdpMLineIndex uint16) error
func (p *KitPeer) Tracks() <-chan *webrtc.TrackRemote
func (p *KitPeer) Close() error
```

SettingEngine 설정:
- NAT1To1IPs = none (loopback)
- ICETransportPolicy = all (host candidates OK, loopback)

**커밋**:
```bash
git commit -m "feat(gateway-go): upstream Pion peer for Kit loopback connection"
```

---

### Task C8: Downstream Pion peer (Gateway ↔ Browser)

브라우저 쪽 peer. TURN-relay 강제, Gateway 가 **offerer** (먼저 offer 생성).

**Files:**
- Create: `gateway-go/internal/downstream/browser_peer.go`, `browser_peer_test.go`

핵심 API:
```go
type BrowserPeer struct {
	pc *webrtc.PeerConnection
}

func NewBrowserPeer(turnURI, user, cred string) (*BrowserPeer, error)
func (p *BrowserPeer) AddTrack(track webrtc.TrackLocal) error
func (p *BrowserPeer) CreateOffer() (string, error)
func (p *BrowserPeer) SetAnswer(sdp string) error
func (p *BrowserPeer) AddCandidate(candidate string, sdpMid string, sdpMLineIndex uint16) error
func (p *BrowserPeer) OnICECandidate(func(*webrtc.ICECandidate))
func (p *BrowserPeer) Close() error
```

Configuration:
```go
webrtc.Configuration{
	ICEServers: []webrtc.ICEServer{{URLs: []string{turnURI}, Username: user, Credential: cred}},
	ICETransportPolicy: webrtc.ICETransportPolicyRelay,
}
```

**커밋**:
```bash
git commit -m "feat(gateway-go): downstream Pion peer with TURN relay enforcement"
```

---

### Task C9: RTP track forwarder

upstream `TrackRemote.ReadRTP` → downstream `TrackLocalStaticRTP.WriteRTP` loop. 각 upstream track 마다 goroutine 하나.

**Files:**
- Create: `gateway-go/internal/relay/forwarder.go`, `forwarder_test.go`

```go
func Forward(ctx context.Context, src *webrtc.TrackRemote, dst *webrtc.TrackLocalStaticRTP) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		pkt, _, err := src.ReadRTP()
		if err != nil {
			return err
		}
		if err := dst.WriteRTP(pkt); err != nil {
			if errors.Is(err, io.ErrClosedPipe) {
				return nil
			}
			return err
		}
	}
}
```

테스트: in-memory fake track (pion `mediaengine.Mock*`) 로 packet count 검증.

**커밋**:
```bash
git commit -m "feat(gateway-go): RTP track forwarder (upstream→downstream)"
```

---

## Phase D — Session Orchestration

### Task C10: Session manager + main wiring

한 browser WS connection 당 `Session`:
1. Kit WS 열기
2. 브라우저 WS 메시지 pump start
3. Kit→브라우저 메시지 pump start
4. offer(Kit→Gateway) intercept → upstream peer.HandleOffer → upstream peer 가 tracks 수신 시작
5. 각 upstream track → TrackLocalStaticRTP 생성 → downstream peer.AddTrack → downstream peer.CreateOffer → NVST envelope 으로 브라우저에 송신
6. 브라우저의 answer → downstream peer.SetAnswer
7. 브라우저의 candidate → downstream peer.AddCandidate
8. upstream peer 의 ICE candidate → NVST envelope 으로 Kit 에 송신

**Files:**
- Create: `gateway-go/internal/session/session.go`, `session_test.go`
- Create: `gateway-go/cmd/gateway/main.go` (replace stub)

**커밋**:
```bash
git commit -m "feat(gateway-go): session manager + main wiring with healthz"
```

---

## Phase E — Deploy + Verify

### Task C11: Dockerfile

```dockerfile
# gateway-go/Dockerfile
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/gateway ./cmd/gateway

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/gateway /usr/local/bin/gateway
USER nonroot
ENTRYPOINT ["/usr/local/bin/gateway"]
```

```bash
git commit -m "build(gateway-go): multi-stage Dockerfile with distroless runtime"
```

### Task C12: Build + push on ws-node074

```bash
# ws-node074 에서 (scp or git fetch)
cd /root/gateway-go-build
docker build -t 10.61.3.124:30002/library/isaac-launchable-gateway-go:dev .
docker push 10.61.3.124:30002/library/isaac-launchable-gateway-go:dev
```

### Task C13: deployment-0.yaml image swap

```yaml
- name: gateway
  image: 10.61.3.124:30002/library/isaac-launchable-gateway-go:dev
```

```bash
ssh root@10.61.3.75 'k0s kubectl rollout restart -n isaac-launchable deployment/isaac-launchable-0'
ssh root@10.61.3.75 'k0s kubectl rollout status -n isaac-launchable deployment/isaac-launchable-0 --timeout=300s'
```

### Task C14: E2E validation

각 시나리오별로:

1. Kit start via Isaac Lab script
2. 브라우저 → `http://10.61.3.125/viewer/` 접속
3. DevTools → chrome://webrtc-internals → `inbound-rtp (video, H264)` 존재 확인
4. candidate-pair remote 가 coturn relay address 인지 확인
5. `kubectl logs -l app=coturn -f` → pod0-cred allocation 증가 확인
6. 브라우저 viewport 에서 로봇 움직임 시각 확인

시나리오:
- `./isaaclab.sh -p scripts/demos/quadrupeds.py --livestream 2`
- `./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/play.py --task Isaac-Ant-v0 --num_envs 4 --livestream 2 --use_pretrained_checkpoint`
- `./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/train.py --max_iterations 5 --livestream 2`

### Task C15: Memory + docs

- `project_isaac_lab_livestream_status.md` 에 C 결과 appendix
- `gateway-go/README.md` 에 operational notes

---

## Rollback

문제 발생 시 1 commit 으로 복귀:
```yaml
- name: gateway
  image: 10.61.3.124:30002/library/isaac-launchable-gateway:dev  # Node.js simple-proxy
```

Node.js gateway 는 **절대 삭제 금지** — 현재 signaling 이라도 동작하는 유일한 baseline.
