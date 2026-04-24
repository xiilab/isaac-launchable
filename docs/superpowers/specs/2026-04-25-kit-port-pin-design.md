# Kit UDP Port Pin via Kernel ephemeral range — Design

**작성일**: 2026-04-25
**대상 리포**: `isaac-launchable`
**목표**: `play.py --livestream 2` / `train.py --livestream 2` 및 `quadrupeds.py` 를 pod-0 에서 브라우저 뷰포트로 렌더링.
**비목표**:
- pod-1 수정 (건드리지 않음)
- Kit / Isaac Sim 바이너리 수정
- hostNetwork=true 사용

---

## 1. 배경

### 1.1 근본 블로커

Kit (`omni.kit.livestream.webrtc-10.1.2/libNvStreamServer.so`) 는 WebRTC media 용 UDP 소켓을 `bind(port=0)` (ephemeral) 로 요청한다. 커널은 `/proc/sys/net/ipv4/ip_local_port_range` 기본값(32768–60999) 범위에서 random 포트를 부여.

- SDP 에서는 `publicIp:streamPort` (예: `10.61.3.74:30998`) 를 advertise
- 실제 UDP bind 는 다른 ephemeral 포트 (과거 관찰: 37879, 38674, 47750, …)
- Advertise 와 bind 의 불일치 + hostPort 매핑 없음 → 외부에서 Kit 의 media UDP 에 도달 불가
- → 브라우저 `StreamerNoNominatedCandidatePairs` 지속

이전 시도:
- Pion SFU 로 중계 (`gateway-go/`) → Kit 가 advertise 한 port 로 Pion 도 도달 불가 (같은 원인)
- `publicIp=127.0.0.1` 로 loopback candidate advertise → Kit 는 여전히 30998 을 bind 하지 않음 (`ss -unp` 실측)
- `hostNetwork=true` → 사용자 명시 금지

### 1.2 핵심 통찰

Kit 자체는 "bind port 를 제어할 수 있는 knob" 을 노출하지 않지만, **커널의 ephemeral allocator 는 사용자 공간에서 제어 가능**하다.

`/proc/sys/net/ipv4/ip_local_port_range` 를 **단일 포트** `30998 30998` 로 축소하면 Kit 의 bind(0) 호출이 **반드시 30998** 을 할당받는다. 이후 hostPort: 30998/UDP + `publicIp=hostIP` 로 advertise 와 실제 bind 가 정확히 일치한다.

---

## 2. 아키텍처

```
┌─ 브라우저 (외부) ──────────────────┐      ┌─ ws-node074 (10.61.3.74) ─────────────────┐
│                                    │      │                                           │
│  http://10.61.3.125/viewer/        │ TCP  │ ingress → svc-0:80 → nginx sidecar        │
│  ├─ signaling WS                   │ 80   │  ├─ /        → web-viewer (:5173)         │
│  │   /sign_in?peer_id=…            │─────►│  ├─ /viewer/ → web-viewer                 │
│  │                                 │      │  └─ /sign_in → Kit :49100 (직결)          │
│  │                                 │      │                                           │
│  └─ media UDP 30998 (직접)         │ UDP  │ hostPort 30998/UDP ──► pod-0 netns        │
│                                    │30998 │                         └─ Kit (bind 30998)│
└────────────────────────────────────┘      └───────────────────────────────────────────┘
```

### 2.1 Kit 의 UDP bind 동작 강제

1. **runheadless.sh** (ConfigMap) 실행 초반에 port range pin:
   ```bash
   echo "30998 30998" > /proc/sys/net/ipv4/ip_local_port_range
   ```
2. **vscode 컨테이너** 에 `securityContext.capabilities.add: ["NET_ADMIN"]` 부여 — sysctl write 권한.
3. **ISAACSIM_HOST=hostIP** 로 Kit 가 `hostIP:30998` advertise.
4. **hostPort: 30998/UDP** 매핑으로 호스트 인터페이스의 30998 UDP 가 pod 의 30998 UDP 로 NAT 연결.

### 2.2 Signaling 경로 단순화

Gateway 및 SFU 불필요. 브라우저 ↔ Kit 직접 WebRTC.

- `nginx-config-0` 의 `/sign_in` location → `proxy_pass http://localhost:49100/sign_in` (Kit 직결)
- gateway 컨테이너 삭제 (deployment-0 에서 제거)
- `/pod-0/signaling` ingress path 삭제
- `svc-0` 의 `port: 9000` 삭제

### 2.3 Pod-1 영향

- pod-1 은 건드리지 않음.
- pod-1 이 같은 노드에 있더라도 hostPort 충돌 없음 (pod-1 은 pod 네트워크, hostPort 미사용).

---

## 3. 컴포넌트 (파일별 변경)

### 3.1 `k8s/base/configmaps.yaml` — `runheadless-script-0`

**변경**: 스크립트 최상단에 port range pin 코드 추가.

```bash
#!/bin/bash
set -e

# Kit ephemeral UDP bind 를 30998 로 pinning (hostPort 매핑 대응).
# 요구사항: vscode 컨테이너 securityContext.capabilities=["NET_ADMIN"]
if [ -w /proc/sys/net/ipv4/ip_local_port_range ]; then
  echo "30998 30998" > /proc/sys/net/ipv4/ip_local_port_range
  echo "[runheadless] pinned ip_local_port_range to 30998"
else
  echo "[runheadless] WARN: ip_local_port_range not writable" >&2
  echo "[runheadless] WARN: NET_ADMIN capability required" >&2
fi

# (기존 Kit 기동 명령)
```

**pod-1 의 `runheadless-script-1` 은 변경하지 않음**.

### 3.2 `k8s/isaac-sim/deployment-0.yaml`

**3.2.1** vscode 컨테이너에 securityContext 추가:
```yaml
- name: vscode
  image: 10.61.3.124:30002/library/isaac-launchable-vscode:6.0.0
  securityContext:
    capabilities:
      add: ["NET_ADMIN"]
```

**3.2.2** ISAACSIM_HOST 복원:
```yaml
- name: ISAACSIM_HOST
  valueFrom:
    fieldRef:
      fieldPath: status.hostIP
```

**3.2.3** `webrtc-media` 에 hostPort 복원:
```yaml
ports:
- name: webrtc-media
  containerPort: 30998
  hostPort: 30998
  protocol: UDP
```

**3.2.4** gateway 컨테이너 삭제 (블록 전체 제거).

### 3.3 `k8s/base/configmaps.yaml` — `nginx-config-0`

`/sign_in` location 을 Kit 직결로 원복 (HTTP 80, HTTPS 443 양쪽):
```nginx
location /sign_in {
  proxy_pass http://localhost:49100/sign_in;
}
```

### 3.4 `k8s/base/services.yaml`

svc-0 의 `port: 9000 (signaling)` 제거. svc-1 변경 없음.

### 3.5 `k8s/isaac-sim/ingress-0.yaml`

`/pod-0/signaling` path rule 제거. ingress-1 변경 없음.

### 3.6 `isaac-lab/web-viewer-sample/entrypoint.sh`

`RTCPeerConnection` monkey-patch (TURN relay 강제) 제거. Kit 의 host candidate 가 이제 도달 가능하므로 강제 relay 불필요. 이미지 재빌드 + 배포 필요.

### 3.7 건드리지 않는 것

- pod-1 관련 모든 파일 (deployment-1, nginx-config-1, ingress-1, runheadless-script-1)
- Isaac Sim 이미지 (`isaac-launchable-vscode:6.0.0`)
- coturn 배포 (`k8s/base/turn.yaml`) — 배포 유지, 사용 안함
- `gateway/` 와 `gateway-go/` 소스 — 보존 (rollback 대비)

---

## 4. 데이터 흐름

### 4.1 Pod 기동

1. pod-0 생성 (vscode / nginx / web-viewer 3 컨테이너; gateway 없음)
2. vscode 내부 runheadless.sh 실행:
   - `ip_local_port_range` 를 30998 단일 포트로 pin
   - Kit 프로세스 fork
3. Kit 초기화:
   - signaling server :49100 TCP listen
   - 이후 세션 시 WebRTC UDP bind(0) → 커널이 30998 할당

### 4.2 브라우저 세션

1. 브라우저 → `http://10.61.3.125/viewer/` → web-viewer SPA 로드
2. NVST library → `ws://10.61.3.125/sign_in?peer_id=…`
3. nginx `/sign_in` → `localhost:49100/sign_in` (Kit 직결)
4. Kit signaling 교환:
   - offer SDP candidate: `10.61.3.74:30998 typ host` (publicIp=hostIP, streamPort=30998)
   - browser answer + trickle candidates
5. ICE:
   - browser → UDP `10.61.3.74:30998` → host kernel → hostPort → pod:30998 → Kit
   - Kit STUN response → browser
   - nominated pair 확정
6. DTLS/SRTP 수립, RTP media 흐름
7. Isaac Lab play/train 렌더링 표시

### 4.3 세션 종료

- 브라우저 WebSocket close → Kit 세션 정리
- 다음 세션: Kit UDP bind(0) → 다시 30998 (범위 단일 포트)
- Kit graceful shutdown 시 SIGTERM → 소켓 close

---

## 5. 에러 처리 / 롤백

### 5.1 런타임 에러

| 증상 | 원인 | 진단/대응 |
|------|------|-----------|
| runheadless.sh 로그 `WARN: not writable` | NET_ADMIN 없음 | deployment-0 securityContext 확인 |
| Kit stderr `EADDRINUSE` | 30998 선점됨 | `ss -unp \| grep 30998` 으로 선점 프로세스 확인 |
| pod `FailedScheduling` `hostPort conflict` | 다른 pod 가 hostPort 30998 사용 중 | 해당 pod 확인/종료 |
| 브라우저 `StreamerNoNominated…` 재발 | port pin 미적용 또는 hostPort 미매핑 | pod 내부 `ss -unp`, `cat /proc/sys/…port_range` 확인 |

### 5.2 롤백

이 spec 의 구현 commit 전체를 `git revert` → `kubectl apply` 로 이전 상태 (gateway-go + Node.js proxy 병행) 로 복귀 가능. 소스는 보존.

---

## 6. 검증

### 6.1 설정 검증

```bash
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o jsonpath='{.items[0].metadata.name}'")

# (a) port range pin 적용
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- cat /proc/sys/net/ipv4/ip_local_port_range"
# 기대: "30998   30998"

# (b) Kit bind 검증 (Kit 기동 후, 세션 발생 시)
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- ss -unp 2>/dev/null | grep 30998"
# 기대: UDP 30998 이 kit 프로세스로 바인드됨

# (c) hostPort 매핑 (노드에서)
ssh root@10.61.3.74 "sudo ss -unp 2>/dev/null | grep 30998"
# 기대: 호스트 인터페이스에 30998 UDP 리스너
```

### 6.2 E2E 시나리오

1. **quadrupeds.py** — 기준 검증 (과거 성공 재현):
   ```
   ./isaaclab.sh -p scripts/demos/quadrupeds.py --livestream 2
   ```
   브라우저에서 4족 로봇 확인.

2. **play.py**:
   ```
   ./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/play.py \
     --task Isaac-Ant-v0 --num_envs 4 --livestream 2 --use_pretrained_checkpoint
   ```

3. **train.py**:
   ```
   ./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/train.py \
     --max_iterations 5 --livestream 2
   ```

4. **30분 세션 유지**: reconnect 없이 30분 연속 재생.

5. **chrome://webrtc-internals**:
   - `inbound-rtp` (kind=video) 활성
   - `candidate-pair` remote address 가 `10.61.3.74:30998` (host candidate, NOT relay)

---

## 7. 범위 밖 (명시)

- pod-1 기능 통합 (같은 구성 적용하려면 별도 hostPort 배정 필요 — 다른 포트)
- SFU / gateway 재도입 (현재 설계는 direct WebRTC 로 충분)
- 멀티 세션 동시 (pod-0 는 단일 브라우저 세션 가정)

---

## 8. 참고

- 선행 spec: `2026-04-24-webrtc-gateway-pion-design.md` (Pion SFU, 기각)
- 관련 memory: `project_isaac_lab_livestream_status.md`
- Linux kernel ephemeral port allocation: `ip_local_port_range` sysctl
- Kit livestream extension: `omni.kit.livestream.webrtc-10.1.2+110.0.0.lx64.r.cp312`
