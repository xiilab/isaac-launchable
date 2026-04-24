# Isaac Lab WebRTC Livestream — pod network 격리 해결 설계

**작성일**: 2026-04-24
**대상 리포**: `isaac-launchable` (k8s deployment, Kit settings, optional TURN)
**목적**: `play.py --livestream 2` (그리고 `train.py`)에서 브라우저 뷰포트에 로봇 렌더링이 안정적으로 보이도록 네트워크 레이어를 수정. Isaac Lab upstream fix 의존 없이 isaac-launchable 단독으로 해결.

---

## 1. 배경과 실험 결과

### 1.1 어제 결론 (2026-04-23) 정정

오늘 새벽까지 `play.py --livestream 2` 에서 video track 이 안 보이는 증상을 Isaac Lab 업스트림 버그 [#5364](https://github.com/isaac-sim/IsaacLab/issues/5364) 로 분류하고 업스트림 issue comment 3건 까지 추가했음. 그러나 2026-04-24 오전 `hostNetwork=true` 재현 실험으로 **진짜 원인이 밝혀짐**: 업스트림 코드 버그가 아니라 **Kubernetes pod network 격리와 Kit WebRTC 의 동적 UDP 포트 동작 충돌**. Isaac Lab 이나 Kit 소스는 건드릴 필요 없음.

### 1.2 결정적 실험 (2026-04-24 새벽)

| 조건 | play.py `--livestream 2` 브라우저 결과 |
|---|---|
| `hostNetwork: true` (pod-0 단독 배치) | ✅ **로봇 정상 렌더링** (사용자 직접 확인) |
| `hostNetwork: false` (현 배포, pod-0/-1 공존) | ❌ signaling 성공, `inbound-rtp(kind=video)` 미발생, ~51s 에 `SERVER_DISCONNECTED` |

`quadrupeds.py` 는 동일 hostNetwork=false 에서 우연히 되는 경우가 있었는데, 이는 Kit 이 잡는 ephemeral UDP 포트가 시점에 따라 달라서 30998 hostPort 와 일치할 때 통과되는 **flaky 상태**였던 것으로 해석.

### 1.3 UDP 포트 bind 직접 관측

`hostNetwork=true` 상태에서 play.py 기동 후 `ss -ulnp`:

```
# Kit 기동 전에는 없었고, 기동 후 새로 나타난 UDP 포트
IPv4:  37879, 38674, 47750, 49770
IPv6:  7784, 40604, 42537, 43939, 45255, 58245
```

- 모두 **ephemeral range (32768–60999)** 에서 random 하게 선택됨
- `--kit_args "--/exts/omni.kit.livestream.app/primaryStream/streamPort=30998"` 설정을 주입했는데도 **30998 은 bind 되지 않음** → `streamPort` 는 ICE candidate advertise 용 값일 뿐, 실제 bind 포트와 무관
- 최소 **4개 이상의 UDP 포트**를 동시에 씀

### 1.4 근본 원인 (확정)

1. Kit WebRTC (NVIDIA StreamSDK 기반) 는 **ICE host candidate 에 다수의 ephemeral UDP 포트**를 advertise.
2. pod network (`hostNetwork=false`) 에서는 `hostPort` 에 명시한 30998/UDP 만 노드 외부로 노출. 나머지 ephemeral 포트는 **pod 내부 전용** — 브라우저가 도달 불가.
3. SDP answer 에는 video track 이 포함되지만, 모든 host candidate 의 connectivity check 가 실패 → 세션 level 에서 `SERVER_DISCONNECTED` 로 끊김. 브라우저 쪽에서는 video track 이 **결코 협상 완료되지 않음** 으로 관찰되어 `inbound-rtp(kind=video)` 엔트리 미발생.

---

## 2. 설계 목표

- **S1**: `./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/play.py --task <TASK> --livestream 2 ...` 이 `hostNetwork=false` 인 기존 배포에서 동작. 브라우저 `http://10.61.3.125/viewer/` 에 로봇 렌더링.
- **S2**: 동일 노드 (ws-node074) 에 `isaac-launchable-0` 과 `isaac-launchable-1` 두 파드가 **공존** 하는 기존 운영 모델 유지.
- **S3**: `quadrupeds.py` 등 기존 정상 동작 경로는 regression 없이 유지.
- **S4**: `train.py` 도 동일 원리로 동작 (train 이 rendering pipeline 쓰는 모드일 때).
- **비목표**: Isaac Lab 소스 / Kit native plugin 수정. `hostNetwork=true` 복귀.

---

## 3. 아키텍처

### 3.1 Two-track 접근

현재 Kit WebRTC 가 지원하는 UDP port 제어 수준이 불명확하므로 **Track C 우선 탐색 → Track D fallback** 구조.

```
                  ┌──────────────── choose exactly one ────────────────┐
                  │                                                    │
  Track C: Port-Range 제한 + hostPort 다수 매핑        Track D: TURN relay 전용 강제
  ─────────────────────────────────────────            ──────────────────────────
  Kit settings:                                         Kit settings:
    primaryStream/portRange/min = 30998                   iceServers = [coturn relay]
    primaryStream/portRange/max = 31097  (or similar)     iceTransportPolicy = relay
  Deployment:                                           coturn (already deployed):
    hostPort 30998–31097 (100 ports) for pod-0            3478/TCP+UDP, 5349/TLS
    hostPort 31098–31197 (100 ports) for pod-1            turnserver.conf (static creds)
  Pros:                                                 Pros:
    · 직접 UDP, latency 낮음                               · 단일 relay endpoint 로 단순화
    · TURN infra 의존 안 함                                · pod IP / 포트 격리 완전 우회
  Cons:                                                 Cons:
    · Kit 이 portRange 설정을 지원해야 함 (불확실)            · NVST 가 iceServers 설정 무시 가능성 有 (재검증 필요)
    · YAML 100줄 추가                                      · TURN 대역폭 병목 (RTX 6000 rendering ~10 Mbps)
```

### 3.2 실행 순서 (bisected probe)

```
Phase 1 (≤ 30m): Track C 가능성 확인
  - /isaac-sim/extscache/omni.kit.livestream.webrtc-*/config/extension.toml 읽기
  - portRange 관련 settings 검색
  - NvscStream library 문서 검색 (docs/ 하위)
  - hostNetwork=true 상태에서 Kit settings 주입 실험으로 bind 포트 제한되는지 확인
  Result:
    A. 설정 발견 및 동작 → Track C 구현
    B. 설정 없음 / 동작 안 함 → Track D 로 이동

Phase 2 (≤ 1h, Track C 선택 시): 구현
  - k8s/isaac-sim/deployment-0.yaml, -1.yaml 의 containers.vscode.ports 에 range 추가
  - runheadless.sh (ConfigMap) 에 portRange kit_args 주입
  - pod rollout, 검증
  - isaac-launchable repo commit + docs 업데이트

Phase 2 (≤ 2h, Track D 선택 시): 구현
  - coturn 설정 검증 (k8s/base/turn.yaml, existing coturn deployment)
  - Kit iceServers / iceTransportPolicy 주입 방식 확정
  - 실험: NVST 가 iceServers 를 실제로 사용하는지 확인
  - 동작 시 runheadless.sh 통합 및 commit
  - 미동작 시 이 문서에 실험 결과 업데이트 + 다음 단계 재설계
```

### 3.3 Track C 구체 설계

**k8s/isaac-sim/deployment-0.yaml 변경**:
```yaml
spec:
  template:
    spec:
      containers:
      - name: vscode
        env:
        - name: ISAACSIM_WEBRTC_PORT_MIN
          value: "30998"
        - name: ISAACSIM_WEBRTC_PORT_MAX
          value: "31097"   # 100-port window per pod (tune after experiment)
        ports:
        # existing
        - name: webrtc-signal
          containerPort: 49100
          hostPort: 49100
          protocol: TCP
        # new (generated by templating, see below)
        - name: wrtc-30998
          containerPort: 30998
          hostPort: 30998
          protocol: UDP
        - name: wrtc-30999
          containerPort: 30999
          hostPort: 30999
          protocol: UDP
        # ... 30998 ~ 31097, 100 entries
```

**k8s/isaac-sim/deployment-1.yaml 변경**: 31098–31197 range, ISAACSIM_SIGNAL_PORT 도 49101 (충돌 피함).

**`runheadless.sh` ConfigMap 변경** (추가 kit_args):
```sh
[ -n "${ISAACSIM_WEBRTC_PORT_MIN}" ] && \
  EXTRA_FLAGS="${EXTRA_FLAGS} --/<kit-path-TBD-after-experiment>/min=${ISAACSIM_WEBRTC_PORT_MIN}"
[ -n "${ISAACSIM_WEBRTC_PORT_MAX}" ] && \
  EXTRA_FLAGS="${EXTRA_FLAGS} --/<kit-path-TBD-after-experiment>/max=${ISAACSIM_WEBRTC_PORT_MAX}"
```

**YAML 확장 관리**: 100줄 hostPort entry 를 수동 관리하지 말고 kustomize patch 또는 짧은 jq/yq 스크립트로 생성 (`scripts/gen-webrtc-ports.sh` 같은 helper).

### 3.4 Track D 구체 설계

**coturn 확인** (`k8s/base/turn.yaml`, 이미 `coturn-5d4786cccd-m2pl6` 가동 중):
- 3478/UDP+TCP, 5349/TLS 노출
- static-auth-secret 확인
- realm 및 lt-cred-mech 설정 확인

**Kit iceServers 주입** (`runheadless.sh`):
```sh
# TURN server advertise
EXTRA_FLAGS="${EXTRA_FLAGS} --merge-config=/etc/kit-turn.toml"
```

`/etc/kit-turn.toml` (ConfigMap `kit-turn-override`) 내용 예:
```toml
[settings.exts."omni.kit.livestream.webrtc"]
iceServers = [
  { urls = "turn:coturn.isaac-launchable.svc.cluster.local:3478?transport=udp",
    username = "<shared>", credential = "<shared>" }
]
iceTransportPolicy = "relay"
```

**실험 필수**: 메모리 기록 "NVST 는 iceServers 를 무시할 수도" 재검증. hostNetwork=false 상태에서 위 설정 주입 후 chrome://webrtc-internals 에 relay candidate 가 선택되는지 확인. 안 나오면 Track D 폐기.

### 3.5 파드 격리 전략

두 파드가 같은 노드에 있어도 충돌 없도록 **포트 window 를 분리**:

| pod | WebRTC UDP 범위 | signalPort (TCP) | ISAACSIM_HOST (publicIp) |
|---|---|---|---|
| isaac-launchable-0 | 30998-31097 | 49100 | status.hostIP (10.61.3.74) |
| isaac-launchable-1 | 31098-31197 | 49101 | status.hostIP (10.61.3.74) |

Ingress (`10.61.3.125`) 의 nginx-ovas 쪽 signal proxy 는 Host 기준 routing 이므로 49100/49101 둘 다 upstream 등록 필요.

### 3.6 Track D 선택 시 파드 격리

TURN relay 이면 파드마다 다른 UDP 포트 필요 없음 (coturn 이 single endpoint). 단 Kit signaling 포트는 여전히 분리 (49100 / 49101).

---

## 4. 검증

| 레벨 | 방법 |
|---|---|
| Unit | 해당 없음 (인프라 설정 변경) |
| Integration | pod rollout 후 `ss -ulnp` 로 Kit 이 bind 하는 UDP 포트가 지정 range 안에 있는지 확인 (Track C), 또는 `chrome://webrtc-internals` 에서 selected candidate 가 relay 인지 (Track D) |
| E2E | (a) `./isaaclab.sh -p scripts/demos/quadrupeds.py --livestream 2` 로 browser 렌더링 regression 없음 확인. (b) `./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/play.py --task Isaac-Ant-v0 --num_envs 4 --livestream 2` 로 학습된 policy 의 Ant 가 브라우저에 표시 |
| 멀티 파드 | isaac-launchable-0, -1 동시 기동 후 각각 다른 브라우저 탭 에서 다른 pod 의 뷰포트가 **교차 없이** 표시 |
| Train | `./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/train.py --task <task> --livestream 2 --num_envs 64` 도 viewport 렌더링 확인 |

성공 기준: 4개 경우 모두 30분 이상 안정적으로 세션 유지 (`SERVER_DISCONNECTED` 없음).

---

## 5. 롤백

모든 변경은 k8s manifest + ConfigMap 에 한정. 문제 발생 시:
1. `git revert <commit>`
2. `kubectl apply -f k8s/` (또는 kustomize)
3. `kubectl rollout restart deployment/isaac-launchable-0 deployment/isaac-launchable-1`

Kit/Isaac Lab 소스는 안 건드리므로 이미지 재빌드 불필요.

---

## 6. 운영 고려사항

- **hostPort 소진**: 각 파드 100개씩 = 노드당 총 200개 UDP 포트. 다른 워크로드와의 충돌 확인 필요 (현재 nginx-ingress 80/443, metallb 등은 영향 없음).
- **방화벽 / L2 ACL**: 노드 외부 → 노드 IP 10.61.3.74 로 30998-31197 UDP 포트가 허용돼야 함. 사내 네트워크 정책 확인.
- **Range 크기**: 100개는 초기 추정치. Phase 1 실험에서 Kit 이 실제로 동시에 쓰는 최대 포트 수 측정 후 조정 (최소 10개 예상, 안전 마진 10×).
- **Isaac Sim 6.0.x → 향후 버전 업그레이드**: Kit portRange 설정명이 바뀔 수 있음. runheadless.sh 의 flag 명을 configMap 으로 두어 버전별 tweak 가능하게.

---

## 7. 업스트림 이슈 정정

오늘 새벽 업스트림에 추가한 comment 3건 (#5362, #5363, #5364) 중 **#5364 는 잘못된 원인 진단**. 정정 계획:

- **#5364** 에 새 comment: "Follow-up investigation shows the root cause is Kubernetes pod network isolation + Kit WebRTC ephemeral UDP ports, not an Isaac Lab code bug. With `hostNetwork=true` the same `play.py --livestream 2` renders correctly in the browser. Closing as `not-a-bug` from Isaac Lab side." → Isaac Lab maintainer 에게 **close 요청**.
- **#5362**: Kit experience deadlock 은 별개 이슈. Track C/D 와 무관. **그대로 유지**.
- **#5363**: rsl-rl 4.0+ obs_groups hang 도 별개 이슈. **그대로 유지**.

정정 comment 는 이 설계 구현이 완료되고 동작이 증명된 후 (Phase 2 완료) 에 작성. 그래야 "대안 해결 확인됨" 이라는 명확한 근거 동봉.

---

## 8. 참고 자료

- 메모리: `project_isaac_lab_livestream_status.md` (2026-04-24 추가 기록)
- 메모리: `project_isaac_sim_webrtc_publicip.md`
- 선행 설계 (기각됨): `2026-04-23-isaaclab-persistent-kit-design.md`
- Kit extension: `/isaac-sim/extscache/omni.kit.livestream.webrtc-10.1.2+110.0.0.lx64.r.cp312/`
- k8s manifests: `k8s/isaac-sim/deployment-0.yaml`, `deployment-1.yaml`
- ConfigMap: `runheadless-script-0`, `kit-turn-override`
- coturn: `k8s/base/turn.yaml`, `k8s/base/configmaps.yaml` (turn.toml)

---

## 9. 열린 질문 (구현 시 확인)

- **Q1**: Kit `omni.kit.livestream.webrtc` 에 portRange 설정 경로가 정확히 무엇인가? (phase 1 에서 pin 할 것)
- **Q2**: NVST 가 standard WebRTC iceServers/iceTransportPolicy 를 respect 하는가? (Track D 선택 시 먼저 답해야)
- **Q3**: Kit 이 동시에 bind 하는 UDP 포트 최대 수는? (range 크기 결정)
- **Q4**: ingress (10.61.3.125) 의 nginx 설정이 signaling 을 49100 / 49101 두 개 upstream 으로 분기할 수 있는가?
