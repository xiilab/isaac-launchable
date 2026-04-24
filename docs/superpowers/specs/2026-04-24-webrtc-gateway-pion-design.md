# Pion WebRTC Gateway Sidecar — isaac-launchable 설계

**작성일**: 2026-04-24 (오전 3차 재설계, 오후 Headless Chromium pivot + pod-0 scope 축소)
**대상 리포**: `isaac-launchable`
**목표**: `play.py --livestream 2` 와 `train.py --livestream 2` 를 `hostNetwork=false` 인 **pod-0** 에서 브라우저 뷰포트로 로봇 렌더링.
**비목표**: Isaac Lab upstream fix 대기. hostNetwork=true 복귀. NvSt 소스 수정. **pod-1 수정** (사용자 지시로 이번 구현 scope 제외).
**Scope 변경 기록**:
- 2026-04-24 오전: pod-0/-1 공존 포함 설계.
- 2026-04-24 오후: pod-1 수정 금지 지시. 이번 구현은 **pod-0 에 Gateway sidecar + k8s 변경**만. pod-1 적용은 별도 시점 (pod-0 검증 완료 후 사용자 판단).
- 2026-04-24 오후: Go Pion → **Headless Chromium + Node.js Gateway** 로 런타임 pivot (NVST signaling protocol opacity 우회, NVIDIA library 그대로 재활용).

---

## 1. 배경

### 1.1 실증 실패 경로 (재탐색 금지)

| 경로 | 실패 원인 |
|---|---|
| Kit `streamPort` 로 UDP bind pin (Track C) | `streamPort` 는 ICE advertise 값일 뿐. 실제 bind 는 ephemeral(32768–60999) random. Kit CLI 에 portRange key 존재 안 함 (native plugin strings 스캔 확인). |
| TURN relay 강제 (Track D) | NvSt 가 `iceServers` / `iceTransportPolicy` 를 **literally 무시**. coturn allocation 0건 관찰됨. |
| hostNetwork=true (단일 pod) | 동작 확인되었으나 pod-0/-1 공존 불가 (포트 경합). 사용자 제약으로 기각. |

### 1.2 결정적 통찰

- NvSt 의 iceServers 무시는 **Kit 안에서만** 문제. **표준 WebRTC library (Pion)** 는 iceServers 를 respect 한다.
- Kit 을 **pod 내부 peer** 로 격리하고, 외부 브라우저와는 **Pion Gateway 가 peer** 를 맡으면 NvSt 의 버그는 pod 내부에 갇힘 (loopback 에서는 아무 UDP 포트를 써도 도달 가능).
- coturn 은 이미 배포됨 (`coturn-5d4786cccd-m2pl6` Running, `10.61.3.74:3478` UDP+TCP). `kit-turn-override` ConfigMap 에 shared secret (`isaac/isaac`) 준비됨.
- 따라서 **Gateway 외부 UDP hostPort 추가 불필요**. 모든 media 가 coturn 을 경유하므로 Gateway 는 signaling TCP 만 노출하면 됨.

---

## 2. 아키텍처

### 2.1 구성

```
┌─────────── 브라우저 (Chrome, 사용자) ───────────┐
│                                                 │
│  1) signaling: WebSocket over HTTPS             │
│       wss://10.61.3.125/pod-0/signaling         │  ← 기존 ingress + path 추가
│  2) media: WebRTC peer                          │
│       iceServers = [turn:10.61.3.74:3478]       │
│       iceTransportPolicy = "relay"              │
│                                                 │
└──────────────┬──────────────────┬───────────────┘
               │                  │
      signaling (TCP)        media (UDP/TCP, TURN relay)
               │                  │
               ▼                  ▼
    ┌──────────────────┐    ┌─────────────────────┐
    │ ingress          │    │ coturn              │
    │ nginx-ovas 10.61 │    │ 10.61.3.74:3478     │ ← 기존, 변경 없음
    │ .3.125 80/443    │    │ (k8s/base/turn.yaml)│
    └────────┬─────────┘    └──────────┬──────────┘
             │                         │
     route /pod-0/signaling     TURN relay
             │                         │
             ▼                         ▼
    ┌─────────────────────────────────────────────┐
    │ Pod: isaac-launchable-0                     │
    │                                             │
    │  ┌──────────────────────────┐               │
    │  │ Gateway sidecar (Pion Go)│               │
    │  │  - signaling WS server   │◄── 브라우저   │
    │  │    (ClusterIP :9000)     │               │
    │  │  - downstream peer       │◄── TURN relay │
    │  │    (TURN-only)           │               │
    │  │  - upstream peer         │               │
    │  │    (to Kit, loopback)    │──┐            │
    │  └──────────────────────────┘  │            │
    │                                │            │
    │  ┌──────────────────────────┐  │            │
    │  │ vscode container (Kit)   │  │            │
    │  │  omni.kit.livestream.app │◄─┘            │
    │  │  signal TCP 49100        │ pod loopback  │
    │  │  ephemeral UDP           │ (no host exit)│
    │  └──────────────────────────┘               │
    └─────────────────────────────────────────────┘
```

### 2.2 두 개의 WebRTC Peer

Pion Gateway 는 **두 개의 peer connection** 을 동시에 보유:

- **Upstream peer (Gateway ↔ Kit)**
  - Gateway 가 브라우저처럼 Kit 의 signaling endpoint (`ws://127.0.0.1:49100/sign_in?peer_id=gateway`) 에 연결
  - Kit 이 offer SDP 송출, Gateway 가 answer
  - Kit 의 host ICE candidate (pod IP + ephemeral UDP) 를 Gateway 가 그대로 수용 — **pod 내부 loopback 으로 도달 가능**
  - NvSt 의 "iceServers 무시" 는 이 peer 에선 무해함 (loopback 이 성공하면 끝)

- **Downstream peer (Gateway ↔ 브라우저)**
  - Gateway 가 자체 signaling WS server 를 띄움 (`:9000/signaling`)
  - Pion 은 표준 WebRTC 구현이라 iceServers 를 **준수**
  - 양쪽 peer (Gateway, 브라우저) 가 coturn 에 relay allocation → media 는 coturn 경유
  - Gateway 의 외부 UDP bind 불필요

- **Media relay**: upstream 에서 받은 video track 을 downstream peer 로 **track-forwarding** (Pion 의 `TrackLocalStaticRTP` 사용, 재인코딩 없이 RTP 패킷 그대로 전달). CPU 오버헤드 최소.

### 2.3 signaling 경로

- 브라우저는 기존 `/viewer/` 정적 리소스를 통해 web-viewer SPA 를 받음 (변경 없음).
- web-viewer 의 signaling URL 이 현재 Kit 의 `/sign_in` 으로 직접 가는데, **Gateway 의 `/gw-signaling` 으로 우회**하도록 환경변수/ConfigMap 에서 변경.
- ingress `isaac-launchable-ingress-0` 의 rules 에 path `/pod-0/signaling` (또는 hostname 기반) 추가 → `isaac-launchable-svc-0:9000/signaling` 으로 proxy.
- pod-1 도 동일하게 `/pod-1/signaling` → `isaac-launchable-svc-1:9000/signaling`.

### 2.4 pod-0 / pod-1 격리

| 항목 | pod-0 | pod-1 |
|---|---|---|
| Gateway signaling Service | `isaac-launchable-svc-0:9000` | `isaac-launchable-svc-1:9000` |
| ingress path | `/pod-0/signaling` | `/pod-1/signaling` |
| TURN username (coturn) | `isaac0` | `isaac1` |
| TURN credential | `<pod0-secret>` | `<pod1-secret>` |
| Kit signaling (내부) | pod-0 loopback 49100 | pod-1 loopback 49100 |

- coturn `turnserver.conf` 의 user-list 를 pod 별로 분리 (shared secret 방식이면 username prefix 로 충분).
- pod 간 media 교차 없음 (각자의 peer 만 자기 TURN allocation 사용).

---

## 3. 컴포넌트

### 3.1 Pion Gateway 이미지

**언어/라이브러리**: Go 1.22+, `github.com/pion/webrtc/v4`, `github.com/gorilla/websocket`
**레포 위치**: `isaac-launchable/gateway/` (신규 디렉토리)
**빌드**: `Dockerfile` 로 scratch 또는 `distroless/static` 기반 정적 바이너리 이미지
**registry tag**: `10.61.3.124:30002/library/isaac-launchable-gateway:<version>`

**기능 (MVP)**:
1. WebSocket signaling server on `:9000/signaling` — 단일 브라우저 연결 수용.
2. `:49100/sign_in` (Kit) 에 upstream 연결, Kit 이 offer 보내면 answer 생성.
3. 브라우저로부터 offer 수신 시 downstream answer 생성, iceServers/relay 강제.
4. Upstream video/audio track 을 downstream peer 의 `TrackLocalStaticRTP` 로 forward.
5. 브라우저의 data channel (control input) 이 있으면 upstream 에 그대로 proxy — **Phase 2** 범위.
6. 상태 endpoint: `:9000/healthz`, `:9000/metrics` (optional).

**환경변수 (deployment 에서 주입)**:
```
KIT_SIGNAL_URL   = ws://127.0.0.1:49100/sign_in
TURN_URI         = turn:10.61.3.74:3478
TURN_USERNAME    = isaac0     # pod-1 = isaac1
TURN_CREDENTIAL  = <secret>
LISTEN_ADDR      = :9000
LOG_LEVEL        = info
```

### 3.2 web-viewer 수정

**파일**: `isaac-launchable/web-viewer-sample/` (현재 이미 이미지 존재)
**변경**:
- signaling URL default 가 `window.location.hostname:80/sign_in` 인데 → `window.location.hostname/{path-prefix}/signaling` 으로 교체. path-prefix 는 ConfigMap 환경변수 (`SIGNAL_PATH=/pod-0/signaling`) 로 주입.
- WebRTC RTCPeerConnection config 에 `iceServers = [{urls: [...], username, credential}]` + `iceTransportPolicy: 'relay'` 추가. TURN 정보는 web-viewer 컨테이너 env 에서 ConfigMap 으로 주입.
- 나머지 UI (키보드/마우스 입력) 는 기존 data channel 경로 재사용.

### 3.3 k8s manifest 변경

**modify: `k8s/isaac-sim/deployment-0.yaml`**
- `containers:` 에 새 컨테이너 추가:
  ```yaml
  - name: gateway
    image: 10.61.3.124:30002/library/isaac-launchable-gateway:<version>
    env:
      - { name: KIT_SIGNAL_URL, value: "ws://127.0.0.1:49100/sign_in" }
      - { name: TURN_URI,       value: "turn:10.61.3.74:3478" }
      - { name: TURN_USERNAME,  value: "isaac0" }
      - { name: TURN_CREDENTIAL, valueFrom: { secretKeyRef: { name: isaac-launchable-turn, key: pod0-cred } } }
      - { name: LISTEN_ADDR,    value: ":9000" }
    ports:
      - { name: signaling, containerPort: 9000, protocol: TCP }
    resources:
      requests: { cpu: "500m", memory: "256Mi" }
      limits:   { cpu: "2",    memory: "1Gi"   }
  ```
- `containers[0] (vscode)` 의 `webrtc-media` / `webrtc-signal` hostPort 매핑 **제거** (더 이상 외부 노출 불필요). 단 container 내부 `containerPort: 49100` 은 유지 (Gateway 가 loopback 으로 사용).
- `spec.template.spec` 에 `hostNetwork` 는 명시적 `false` 고정.

**modify: `k8s/isaac-sim/deployment-1.yaml`**
- 동일하게 gateway 컨테이너 추가 (TURN_USERNAME=`isaac1`, secret key `pod1-cred`)
- 기존 webrtc-media containerPort 유지 (loopback), hostPort 는 원래 없었으니 변화 없음.

**modify: `k8s/base/services.yaml`**
- 기존 `isaac-launchable-svc-0` / `-svc-1` 의 port 80 proxy 유지.
- 추가: 두 Service 에 `port: 9000 (name: signaling, targetPort: 9000)` 추가 해서 Gateway 노출.
- `isaac-launchable-0-media` / `-1-media` NodePort Service **삭제** (media 는 coturn 경유).

**modify: `k8s/isaac-sim/ingress-0.yaml`, `ingress-1.yaml`**
- path 추가: `/pod-0/signaling` → `service: isaac-launchable-svc-0, port: 9000` (pod-1 대응).

**modify: `k8s/base/configmaps.yaml`**
- web-viewer 컨테이너에 `SIGNAL_PATH`, `TURN_URI`, `TURN_USERNAME`, `TURN_CREDENTIAL_SOURCE` (secretRef) 주입용 ConfigMap 항목 추가.

**modify: `k8s/base/secret.yaml`**
- 신규 Secret `isaac-launchable-turn` — `pod0-cred`, `pod1-cred` 두 key.

### 3.4 Kit 쪽 변경

- **없음**. Kit 은 기존 `omni.kit.livestream.app` 을 그대로 씀.
- `runheadless.sh` 의 `primaryStream.signalPort=49100` 유지. 외부 노출만 제거.
- `--merge-config=/etc/kit-turn.toml` 더 이상 의미 없음 (NvSt 는 어차피 무시). 제거 여부는 선택 — 남겨둬도 무해하지만 cleanup 시 같이 정리 가능.

---

## 4. 데이터 흐름

### 4.1 세션 시작

1. 브라우저가 `https://10.61.3.125/viewer/` 열기. web-viewer SPA 가 로드되며 env 에서 `SIGNAL_PATH=/pod-0/signaling` 읽음.
2. 브라우저가 `wss://10.61.3.125/pod-0/signaling` WebSocket 연결. ingress → `isaac-launchable-svc-0:9000` → Gateway.
3. Gateway 가 브라우저와 signaling 핸드쉐이크 시작. 동시에 pod 내부 `ws://127.0.0.1:49100/sign_in?peer_id=gateway` 로 Kit 과 upstream 핸드쉐이크.
4. Kit 이 offer (video track 포함) 전송 → Gateway upstream peer 가 answer.
5. Kit 이 ICE candidate 교환. pod IP + ephemeral UDP host candidate 를 Gateway 가 loopback 주소로 해석 → Kit 의 UDP 포트와 직접 연결 (pod 내부). media 흐름 시작.
6. Gateway 가 동일 video track 을 downstream peer 의 `TrackLocalStaticRTP` 로 등록, 브라우저 쪽에 offer 재생성.
7. Gateway 가 브라우저에 offer → 브라우저 answer. 양쪽 모두 iceServers=coturn, iceTransportPolicy=relay 이므로 ICE host candidate 미사용, relay candidate 만 교환.
8. 브라우저와 Gateway 가 각각 coturn 에 allocation. coturn 은 둘을 묶어 UDP relay 터널 제공. 브라우저 `<video>` 에 프레임 도달.

### 4.2 세션 종료

- 브라우저 탭 닫으면 WebSocket 종료 → Gateway downstream peer close → (선택) upstream peer 도 close 해서 Kit resource 해제. 또는 upstream peer 유지해서 재접속 시 즉시 공급.
- pod 재기동 시 Gateway sidecar 가 초기 상태로. Kit 도 새로 startup.

### 4.3 control input (Phase 2)

- 브라우저의 키보드/마우스 입력은 web-viewer SPA 가 **data channel** 로 송출.
- Gateway 는 downstream data channel 을 upstream data channel 로 bridge. Kit 이 기존처럼 수신.
- Phase 1 (이 spec 의 범위) 에서는 video-only. Phase 2 에서 data channel bridge 추가.

---

## 5. 에러 처리 / 롤백

### 5.1 런타임 실패

| 케이스 | 동작 |
|---|---|
| Kit 죽음 | upstream peer disconnect → Gateway 가 downstream 에 `SERVER_DISCONNECTED` 전파 + Kit 재기동 대기 |
| coturn 죽음 | downstream peer ICE 실패 → 브라우저 timeout. coturn HA 는 별도 과제 (본 spec 범위 밖) |
| Gateway 죽음 | k8s livenessProbe 로 재기동. 재기동 동안 세션 끊김 |
| 브라우저 연결 끊김 | downstream peer close. upstream peer 는 유지 (선택적) |

### 5.2 롤백

모든 변경은 git commit 단위. 문제 발생 시:
1. `git revert <commit-range>` (gateway 디렉토리, k8s/*, configmaps, secret)
2. `kubectl apply -f k8s/` → 기존 상태로
3. Gateway 이미지는 registry 에 남겨둬도 무해.

롤백 후 Isaac Sim / Kit 이미지는 그대로 (변경 없음).

---

## 6. 검증

### 6.1 Phase 1 — Kit signaling protocol probe

**목표**: Kit `omni.kit.livestream.app` 의 signaling WS 메시지 포맷이 Pion 으로 복제 가능한지 확인.

방법:
1. 기존 브라우저 경로로 `/viewer/` 세션 성립 시, DevTools → Network → `/sign_in` WebSocket → Frames 탭에서 JSON 메시지 dump.
2. 관찰: offer SDP 포맷, ICE candidate 메시지 포맷, message envelope (`{"type":"offer","sdp":"..."}` 같은 키 구조).
3. Pion 의 `SignalingServer` 구현이 동일 메시지 포맷 handle 하도록 작성.

이미 수집된 단서 (오늘 콘솔 로그):
```json
{"type":"config","value":{"supports_localized_text_input":true}}
```
→ `type` 필드 기반 JSON. 거의 표준 형태.

### 6.2 Phase 2 — Gateway 단독 통합 테스트

1. Gateway 를 pod-0 에 배포 (Kit 없이 mock Kit signaling).
2. 브라우저가 Gateway 의 signaling 에 연결 → handshake 성공 확인.
3. coturn allocation 증가 확인 (vs Track D 때 0건 → 이제 양수가 나와야).

### 6.3 Phase 3 — end-to-end

1. `./isaaclab.sh -p scripts/demos/quadrupeds.py --livestream 2` → 브라우저 렌더링.
2. `./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/play.py --task Isaac-Ant-v0 --num_envs 4 --livestream 2 --use_pretrained_checkpoint` → 브라우저 렌더링 (Track C/D 에서 실패한 핵심 목표).
3. pod-0 와 pod-1 각각 다른 브라우저 탭에서 동시 렌더링 → 교차 없이 성공.
4. `./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/train.py --max_iterations 5 --livestream 2` → 학습 장면 렌더링.
5. 30분 이상 유지 (세션 자동 종료 없음).
6. `chrome://webrtc-internals` 에서 `inbound-rtp (kind=video, codec=H264)` 생성 + `candidate-pair` 의 remote 가 TURN relay 주소 (10.61.3.74:3478) 확인.
7. `kubectl logs coturn -f` → allocation 로그 증가 확인.

---

## 7. 운영 고려사항

- **Gateway CPU**: MVP 는 RTP 패킷 re-write 없이 forwarding (SFU 스타일). 1080p60 기준 CPU <0.5 core 예상. limits 에 2 core 여유 할당.
- **latency 증가**: Gateway hop 추가 + coturn relay = 기존 대비 +10–30ms. 허용 가능 범위.
- **보안**: TURN credential 은 k8s Secret 으로만 관리. signaling WS 는 wss 강제 (ingress 에서 TLS termination).
- **관측성**: Gateway Prometheus metrics (peer count, bytes forwarded, error rate) — Phase 2.
- **재사용성**: 동일 Gateway 가 quadrupeds/play/train 구분 없이 동작 (Kit 쪽이 무엇이든 그대로 proxy). `usd-composer` 등 다른 Isaac Sim 파드에도 같은 sidecar 로 확장 가능.

---

## 8. 범위 밖 (명시)

- Isaac Sim / Kit upstream fix
- coturn HA 또는 scalable relay
- multi-party session (여러 브라우저가 한 pod 동시 관전)
- Gateway 의 프레임 재인코딩 / trancoding
- data channel bridge (Phase 2 로 분리)

---

## 9. 열린 질문 (Phase 1 probe 에서 답할 것)

- **Q1**: Kit signaling WS 메시지 포맷이 Pion 에서 1:1 복제 가능한가? 예상은 JSON + standard SDP, 실측 확인.
- **Q2**: Kit 은 복수 signaling peer 를 동시에 허용하는가? Gateway 가 붙은 상태에서 브라우저가 49100 직접 hit 시 충돌?
- **Q3**: Kit 의 video track 이 H264 인가 VP8/VP9 인가? Gateway 가 downstream 에 같은 codec 만 forwarding 하면 transcoding 불필요.
- **Q4**: coturn 의 `lt-cred-mech` 가 활성화돼 있나? 현재 `isaac/isaac` shared credential 이 동작하는지 확인.
- **Q5**: web-viewer 가 signaling URL/iceServers 를 런타임 env 로 주입 받는 구조로 되어 있나? 아니면 빌드타임 hardcoded? 후자면 이미지 재빌드 필요.

---

## 10. 참고

- 선행 spec: `2026-04-23-isaaclab-persistent-kit-design.md` (기각), `2026-04-24-webrtc-port-isolation-design.md` (Track C/D 기각)
- 메모리: `project_isaac_lab_livestream_status.md` (2026-04-24 오전 3차 세션 섹션)
- Pion WebRTC: https://github.com/pion/webrtc
- coturn (기존): `k8s/base/turn.yaml`, `k8s/base/configmaps.yaml` (coturn-config)
- 기존 signaling 관찰: `/sign_in?peer_id=peer-NNNN&version=2&reconnect=1` WebSocket URL, JSON message bodies

---

## 11. 2026-04-24 저녁 재결정 — **C 경로 (Pion SFU with SDP munging) 채택**

### 11.1 왜 다시 Pion 인가

Headless Chromium 경로 (§섹션 참조) 는 commit `bba9a35`–`9b960b5` 에서 구현했으나 `566a863` 에서 simple-proxy 로 downscale 됨. 현재 `gateway/main.js` 는 signaling pass-through 만 수행 — media 는 Kit ↔ 브라우저 직접 peer 이고, 브라우저의 TURN relay candidate 를 Kit 이 받아들이지 못해 `StreamerNoNominatedCandidatePairs` 로 실패.

근본 문제: **Kit (NvSt) 은 iceServers 를 무시하므로 Kit 쪽에서 TURN relay 를 쓸 수 없음**. 따라서 Gateway 가 **실제 WebRTC peer 로 개입**하여 Kit ↔ Gateway 는 pod 내부 loopback 으로, Gateway ↔ 브라우저는 TURN relay 로 분리해야 함.

### 11.2 구현 방식: SDP munging proxy (NVST opacity 우회)

**핵심 통찰**: NVST signaling 의 프로토콜 *envelope* 은 불투명하지만, envelope 안의 offer/answer payload 는 **표준 SDP 문자열**. Gateway 는 envelope 을 그대로 pass-through 하되 offer/answer 메시지만 intercept 해서 SDP 본문을 rewrite 함.

**흐름**:

```
브라우저 ──[NVST envelope: offer payload X]── Gateway ──[pass-through]── Kit
Kit ──[NVST envelope: offer payload X]── Gateway
                                              │
                                              ├── Pion upstream peer.SetRemoteDescription(X)
                                              │   → answer Y (Gateway 의 loopback candidates)
                                              ├── Pion upstream peer.CreateAnswer() → Y
                                              │
                                              └── [NVST envelope: answer payload Y] ── Kit
                                                  (Kit 은 Y 의 candidate 로 loopback UDP peer 확립)

Gateway ── Pion downstream peer.CreateOffer() ──→ offer Z (Gateway 의 TURN relay candidates)
Gateway ──[NVST envelope: offer payload Z]── 브라우저
브라우저 ──[NVST envelope: answer payload W]── Gateway
                                               → downstream peer.SetRemoteDescription(W)

upstream peer.OnTrack(rtpTrack) → track-forward → downstream peer.LocalStaticRTP.WriteRTP
```

**핵심 구성요소**:
1. **NVST envelope parser**: `{"type": "...", "sdp": "...", "candidate": "...", ...}` JSON 파싱. Type 필드 기반 분기.
2. **SDP munger**: `pion/sdp/v3` 이용해 incoming SDP 를 parse → ICE candidates / fingerprint / ufrag-pwd 만 Gateway 것으로 substitute.
3. **Dual PeerConnection manager**: upstream (Kit 방향) + downstream (browser 방향) 를 하나의 session 으로 묶어 관리.
4. **Track forwarder**: upstream.OnTrack → downstream.AddTrack(TrackLocalStaticRTP). RTP 패킷 pass-through (re-encrypt SRTP only).

**NVST 프로토콜 중 Gateway 가 "이해해야" 하는 것**:
- message type 분류 (offer / answer / candidate / 기타)
- offer/answer 메시지의 SDP 필드 위치
- candidate 메시지의 candidate 문자열 + sdpMid / sdpMLineIndex 필드 위치

이 외 NVST 고유 메시지 (config, peerId registration 등) 는 **완전 pass-through** — Gateway 가 내용을 해석하지 않음. 따라서 NVST 프로토콜 리버스 엔지니어링 부담이 크게 축소.

### 11.3 구현 언어: Go + Pion (vs Node.js 유지)

- **Go + Pion (선택)**: 표준 WebRTC 스택, SRTP/DTLS native, 작은 정적 바이너리 이미지 (~30 MB), 경량 컨테이너
- Node.js + `@roamhq/wrtc` (대안): 기존 Node.js 유지 가능하지만 native binary deps 로 이미지 무겁고 (~150 MB), WebRTC API 완성도 Pion 이 우세
- **결론**: Go + Pion. `gateway-go/` 에 신규 구현. `gateway/` (Node.js simple-proxy) 는 롤백 대비 보존.

### 11.4 이번 세션 Scope

| 항목 | 처리 |
|---|---|
| pod-0 gateway sidecar 이미지 교체 (Node.js → Go) | O |
| web-viewer 수정 (현행 유지, NVST library 그대로 사용) | X — 이미 동작 |
| k8s manifest (TURN secret, service, ingress) | 현행 유지 |
| pod-1 | **건드리지 않음** (사용자 지시 유지) |
| Isaac Sim / Kit 이미지 | **변경 없음** |
| coturn | 현행 유지 |

### 11.5 열린 리스크

1. **NVST message type 이름**: offer/answer 의 실제 `type` 문자열이 무엇인지 probe 데이터만으로 확정 필요. `"offer"`/`"answer"`/`"candidate"` 가 표준이나 NVST 가 custom 이름 사용할 가능성 있음 (e.g., `"ice_candidate"`, `"session_offer"`).
2. **multiple m-sections**: Kit 이 audio+video+datachannel 을 한 offer 에 담는 경우 Gateway 가 모든 m-section 을 match 해서 forward 해야 함.
3. **ICE trickle vs non-trickle**: Kit 이 candidate 를 초기 SDP 에 embed 하는지 trickle 로 이후 전송하는지에 따라 SDP munger 구현 경로 분기.
4. **DTLS fingerprint**: Gateway 는 자체 fingerprint 를 SDP 에 주입해야 함 (pion/webrtc 가 자동 처리).
5. **bandwidth / keyframe**: initial keyframe 요청 bridging (RTCP PLI/FIR) 이 없으면 브라우저 <video> 가 영원히 회색. downstream 의 PLI 를 upstream 으로 proxy 필요.

### 11.6 검증 지표

- `chrome://webrtc-internals` 에 **inbound-rtp (video, H264)** 생성 + `candidate-pair` remote address 가 `10.61.3.74:3478` (coturn) 또는 coturn 의 relay transport-address
- `kubectl logs coturn -f` 에 pod0-cred allocation 증가
- `quadrupeds.py` + `play.py` + `train.py` 각 시나리오에서 브라우저에 로봇 움직임 시각 확인
- 30분 이상 세션 유지 (reconnect 없이)
