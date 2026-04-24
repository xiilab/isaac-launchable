# Kit UDP Port Pin Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** pod-0 의 Kit UDP media port 를 커널 ephemeral range 축소로 30998 에 고정시켜 브라우저 직접 WebRTC 로 `play.py --livestream 2` / `train.py --livestream 2` 가 작동하도록 한다.

**Architecture:** `/proc/sys/net/ipv4/ip_local_port_range` 를 `30998 30998` 로 축소하면 Kit 의 `bind(0)` ephemeral 할당이 무조건 30998 이 된다. `hostPort: 30998/UDP` + `ISAACSIM_HOST=hostIP` 로 advertise 와 실제 bind 가 일치, 브라우저 ↔ Kit 직접 WebRTC. Gateway (gateway-go / Node.js proxy) 는 경로에서 제거, coturn 은 미사용 유지.

**Tech Stack:** Kubernetes (k0s), Linux sysctl, Isaac Sim 6.0 Kit 110 livestream extensions (수정 없음), Docker (web-viewer 이미지만 재빌드).

**Spec:** `docs/superpowers/specs/2026-04-25-kit-port-pin-design.md` (commit `a081fb4`)

---

## File Structure

**Will modify:**
- `k8s/base/configmaps.yaml` — `runheadless-script-0` 에 port pin 추가, `nginx-config-0` 의 `/sign_in` 을 `localhost:49100` 로 원복 (pod-0 만)
- `k8s/isaac-sim/deployment-0.yaml` — vscode securityContext + ISAACSIM_HOST 복원 + webrtc-media hostPort 복원 + gateway 컨테이너 삭제 + web-viewer SIGNAL_PATH/TURN env 정리
- `k8s/base/services.yaml` — svc-0 의 `port: 9000` 제거
- `k8s/isaac-sim/ingress-0.yaml` — `/pod-0/signaling` path 제거
- `isaac-lab/web-viewer-sample/entrypoint.sh` — RTCPeerConnection monkey-patch 블록 삭제

**Will NOT modify:**
- `k8s/isaac-sim/deployment-1.yaml`, `ingress-1.yaml`, `nginx-config-1`, `runheadless-script-1` (pod-1 관련 전부)
- Isaac Sim 이미지 (`isaac-launchable-vscode:6.0.0`)
- `k8s/base/turn.yaml` (coturn 배포 유지)
- `gateway/`, `gateway-go/` 소스 (보존)

**Will create:**
- `docs/superpowers/plans/notes/2026-04-25-kit-port-pin-verification.md` — 검증 결과 기록

---

## Phase A — Single-node ConfigMap change

### Task 1: runheadless-script-0 에 port range pin 추가

**Files:**
- Modify: `k8s/base/configmaps.yaml` (lines 275–306, `runheadless-script-0.data.runheadless.sh`)

- [ ] **Step 1: 기존 script 확인**

Run:
```bash
cd /Users/xiilab/git/isaac-launchable
sed -n '275,306p' k8s/base/configmaps.yaml
```
Expected: `#! /bin/sh` 로 시작하는 스크립트 내용 출력. `pkill -f 'isaacsim.exp.full.streaming.kit'` line 이 상단에 있음.

- [ ] **Step 2: script 의 `#! /bin/sh` 다음 줄에 port pin 블록 추가**

`runheadless-script-0` 의 `runheadless.sh:` 블록을 아래로 교체 (기존 `#! /bin/sh` 다음에 port pin 추가):

```yaml
  runheadless.sh: |
    #! /bin/sh
    # ─────────────────────────────────────────────────────────────
    # Kit ephemeral UDP bind 를 30998 로 pinning (pod-0 전용).
    # 요구: vscode 컨테이너 securityContext.capabilities=["NET_ADMIN"]
    # 원리: bind(port=0) → 커널이 ip_local_port_range 범위에서 할당 →
    #       범위가 단일 포트이면 항상 그 포트를 할당.
    # ─────────────────────────────────────────────────────────────
    if [ -w /proc/sys/net/ipv4/ip_local_port_range ]; then
      echo "30998 30998" > /proc/sys/net/ipv4/ip_local_port_range
      echo "[runheadless] pinned ip_local_port_range to 30998"
    else
      echo "[runheadless] WARN: /proc/sys/net/ipv4/ip_local_port_range not writable" >&2
      echo "[runheadless] WARN: NET_ADMIN capability required; Kit will use random ephemeral port" >&2
    fi

    # Kill any orphaned Kit streaming process holding TCP 49100 before starting
    pkill -f 'isaacsim.exp.full.streaming.kit' 2>/dev/null || true
    sleep 1

    # Symlink user extensions into Kit's extsUser search path
    mkdir -p /isaac-sim/extsUser
    for ext in /isaac-sim/user-exts/*/; do
      name=$(basename "$ext")
      ln -sfn "$ext" "/isaac-sim/extsUser/$name"
    done

    # Hide tutorial extension so Example menus don't appear in the UI
    tut=/isaac-sim/exts/isaacsim.robot_motion.motion_generation.tutorials
    [ -d "$tut" ] && mv "$tut" "${tut}.disabled" 2>/dev/null || true

    EXTRA_FLAGS=""
    [ -n "${OMNI_SERVER}" ] && EXTRA_FLAGS="${EXTRA_FLAGS} --/persistent/isaac/asset_root/default=${OMNI_SERVER}"
    [ -n "${ISAACSIM_HOST}" ] && EXTRA_FLAGS="${EXTRA_FLAGS} --/exts/omni.kit.livestream.app/primaryStream/publicIp=${ISAACSIM_HOST}"
    [ -n "${ISAACSIM_SIGNAL_PORT}" ] && EXTRA_FLAGS="${EXTRA_FLAGS} --/exts/omni.kit.livestream.app/primaryStream/signalPort=${ISAACSIM_SIGNAL_PORT}"
    [ -n "${ISAACSIM_STREAM_PORT}" ] && EXTRA_FLAGS="${EXTRA_FLAGS} --/exts/omni.kit.livestream.app/primaryStream/streamPort=${ISAACSIM_STREAM_PORT}"
    # TURN override 자동 병합 (파일 존재 시에만)
    TURN_FLAG=""
    [ -f /etc/kit-turn.toml ] && TURN_FLAG="--merge-config=/etc/kit-turn.toml"

    /isaac-sim/license.sh && /isaac-sim/privacy.sh && /isaac-sim/isaac-sim.streaming.sh \
        --merge-config="/isaac-sim/config/open_endpoint.toml" \
        --enable omni.clipboard.service \
        $TURN_FLAG \
        $EXTRA_FLAGS \
        "$@"
```

**IMPORTANT:** `runheadless-script-1` (line 313+) 는 **건드리지 말 것**.

Use Edit tool to replace the exact block:

- `old_string` begins with `  runheadless.sh: |\n    #! /bin/sh\n    # Kill any orphaned Kit streaming process`
- `new_string` begins with `  runheadless.sh: |\n    #! /bin/sh\n    # ───` (the pinned version)

- [ ] **Step 3: 변경 결과 확인**

Run:
```bash
cd /Users/xiilab/git/isaac-launchable
sed -n '275,320p' k8s/base/configmaps.yaml | head -30
```
Expected: `ip_local_port_range` line 이 `pkill` 보다 위에 있음.

- [ ] **Step 4: pod-1 스크립트 변경되지 않음 확인**

Run:
```bash
cd /Users/xiilab/git/isaac-launchable
grep -n "ip_local_port_range" k8s/base/configmaps.yaml
```
Expected: **정확히 1개** match (runheadless-script-0 안). runheadless-script-1 에는 없어야 함.

- [ ] **Step 5: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add k8s/base/configmaps.yaml
git commit -m "feat(k8s): pin Kit UDP port via ip_local_port_range (pod-0 only)

runheadless-script-0 에 커널 ephemeral port range 를 30998 단일 포트로
축소하는 설정을 스크립트 시작 시 주입. Kit 의 bind(0) 가 반드시
30998 을 할당받게 되어 hostPort 매핑이 Kit 의 실제 UDP bind 와
일치한다.

NET_ADMIN capability 필요 (deployment-0 에서 함께 추가).
pod-1 의 runheadless-script-1 은 변경하지 않음."
```

---

### Task 2: nginx-config-0 의 `/sign_in` 을 Kit 직결로 원복

**Files:**
- Modify: `k8s/base/configmaps.yaml` lines 81–85 (nginx-config-0 HTTP 80 server), lines 111–113 (HTTPS 443 server)

- [ ] **Step 1: 현재 값 확인**

Run:
```bash
cd /Users/xiilab/git/isaac-launchable
grep -n "localhost:9000\|localhost:49100" k8s/base/configmaps.yaml | head -10
```
Expected: pod-0 의 `/sign_in` 2곳 (line 85, 113) 이 `localhost:9000`. pod-1 의 2곳 (line 177, 204) 이 `localhost:49100`.

- [ ] **Step 2: pod-0 HTTP 80 server 의 `/sign_in` 교체**

Use Edit tool:

`old_string`:
```
        location /sign_in {
          # Route signaling via gateway-go SFU (it proxies to Kit loopback
          # :49100 after terminating WebRTC on both sides). Browser →
          # nginx → gateway → Kit; media via coturn TURN relay only.
          proxy_pass http://localhost:9000/sign_in;
        }
        location /api/clipboard/ {
          proxy_pass http://localhost:8011/clipboard/;
          proxy_set_header Connection "";
        }
      }
      server {
        listen 443 ssl default_server;
```

`new_string`:
```
        location /sign_in {
          proxy_pass http://localhost:49100/sign_in;
        }
        location /api/clipboard/ {
          proxy_pass http://localhost:8011/clipboard/;
          proxy_set_header Connection "";
        }
      }
      server {
        listen 443 ssl default_server;
```

(pod-0 의 HTTP server 에만 매치되는 유일한 컨텍스트 — HTTPS 직전에 위치.)

- [ ] **Step 3: pod-0 HTTPS 443 server 의 `/sign_in` 교체**

Use Edit tool:

`old_string`:
```
        location /sign_in {
          # Pod-0: route via gateway-go SFU (see HTTP server comment above).
          proxy_pass http://localhost:9000/sign_in;
        }
        location /api/clipboard/ {
          proxy_pass http://localhost:8011/clipboard/;
          proxy_set_header Connection "";
        }
      }
    }
---
# Isaac Sim (Pod-1) nginx 설정
```

`new_string`:
```
        location /sign_in {
          proxy_pass http://localhost:49100/sign_in;
        }
        location /api/clipboard/ {
          proxy_pass http://localhost:8011/clipboard/;
          proxy_set_header Connection "";
        }
      }
    }
---
# Isaac Sim (Pod-1) nginx 설정
```

- [ ] **Step 4: 주석 헤더 수정 (line 30)**

Use Edit tool:

`old_string`: `# - /sign_in → localhost:9000 (gateway-go SFU → Kit loopback; Kit 직결 아님)`

`new_string`: `# - /sign_in → localhost:49100 (Kit 직결; gateway 제거됨)`

- [ ] **Step 5: 변경 결과 검증**

Run:
```bash
cd /Users/xiilab/git/isaac-launchable
grep -n "localhost:9000\|localhost:49100" k8s/base/configmaps.yaml
```
Expected: `localhost:9000` 0개. `localhost:49100` 은 pod-0 HTTP + HTTPS + pod-1 HTTP + HTTPS = **4개** (+ comment 1개).

- [ ] **Step 6: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add k8s/base/configmaps.yaml
git commit -m "feat(k8s): nginx-config-0 /sign_in 을 Kit 직결로 원복

Gateway 경로 제거에 따라 nginx 의 /sign_in location 을
localhost:9000 (gateway-go) 에서 localhost:49100 (Kit 직통) 으로
복구. pod-0 HTTP 80 + HTTPS 443 server 양쪽.

pod-1 의 nginx-config-1 은 원래 49100 직결이므로 변경 없음."
```

---

## Phase B — Deployment 정리

### Task 3: deployment-0 — ISAACSIM_HOST 복원

**Files:**
- Modify: `k8s/isaac-sim/deployment-0.yaml` lines 44–49

- [ ] **Step 1: 현재 상태 확인**

Run:
```bash
cd /Users/xiilab/git/isaac-launchable
sed -n '44,49p' k8s/isaac-sim/deployment-0.yaml
```
Expected: `value: "127.0.0.1"` 로 되어있는 ISAACSIM_HOST.

- [ ] **Step 2: status.hostIP fieldRef 로 교체**

Use Edit tool:

`old_string`:
```
        # hostNetwork=true 구성에서는 Kit 의 UDP bind 가 host 네임스페이스에
        # 있으므로 ISAACSIM_HOST 를 127.0.0.1 (loopback) 로 두면 같은 host 의
        # gateway Pion 이 loopback 으로 바로 Kit 에 도달 가능하다. 브라우저
        # 방향은 gateway 의 downstream Pion 이 coturn TURN relay 로 통신.
        - name: ISAACSIM_HOST
          value: "127.0.0.1"
```

`new_string`:
```
        # ISAACSIM_HOST = host IP. Kit 이 `publicIp=host IP` 로 advertise
        # 하므로 브라우저가 외부에서 hostIP:30998/UDP 로 Kit 에 도달. 실제
        # Kit UDP bind 는 runheadless-script-0 의 ip_local_port_range pin
        # (30998 단일 포트) + deployment-0 의 hostPort 30998 매핑으로
        # advertise 와 일치.
        - name: ISAACSIM_HOST
          valueFrom:
            fieldRef:
              fieldPath: status.hostIP
```

- [ ] **Step 3: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add k8s/isaac-sim/deployment-0.yaml
git commit -m "feat(k8s): deployment-0 ISAACSIM_HOST=hostIP 복원

Kit 의 advertised candidate 가 host IP:30998 이 되어야 브라우저가
외부에서 직접 도달 가능. 이전 gateway-go SFU 실험에서 썼던
loopback (127.0.0.1) override 는 더 이상 필요 없음."
```

---

### Task 4: deployment-0 — webrtc-media hostPort 복원

**Files:**
- Modify: `k8s/isaac-sim/deployment-0.yaml` lines 71–73

- [ ] **Step 1: 현재 ports 블록 확인**

Run:
```bash
cd /Users/xiilab/git/isaac-launchable
sed -n '68,77p' k8s/isaac-sim/deployment-0.yaml
```
Expected:
```yaml
        ports:
        - name: vscode
          containerPort: 8080
        - name: webrtc-media
          containerPort: 30998
          protocol: UDP
        - name: webrtc-signal
          containerPort: 49100
          protocol: TCP
```

- [ ] **Step 2: webrtc-media 에 hostPort 추가**

Use Edit tool:

`old_string`:
```
        - name: webrtc-media
          containerPort: 30998
          protocol: UDP
```

`new_string`:
```
        - name: webrtc-media
          containerPort: 30998
          hostPort: 30998
          protocol: UDP
```

- [ ] **Step 3: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add k8s/isaac-sim/deployment-0.yaml
git commit -m "feat(k8s): deployment-0 webrtc-media hostPort 30998 복원

runheadless-script-0 의 port pin 으로 Kit 이 30998 UDP 에 bind 한
상태에서, hostPort 매핑이 host 인터페이스의 30998 UDP 를 pod 의
30998 UDP 로 노출. 브라우저가 host IP:30998 으로 Kit 에 직접 도달."
```

---

### Task 5: deployment-0 — vscode 컨테이너에 NET_ADMIN capability 추가

**Files:**
- Modify: `k8s/isaac-sim/deployment-0.yaml` (vscode 컨테이너 spec — line 32 직후)

- [ ] **Step 1: securityContext block 삽입 위치 확인**

Run:
```bash
cd /Users/xiilab/git/isaac-launchable
sed -n '32,37p' k8s/isaac-sim/deployment-0.yaml
```
Expected:
```yaml
      - name: vscode
        image: 10.61.3.124:30002/library/isaac-launchable-vscode:6.0.0
        imagePullPolicy: Always
        env:
        - name: ACCEPT_EULA
```

- [ ] **Step 2: `imagePullPolicy: Always` 다음에 securityContext 삽입**

Use Edit tool:

`old_string`:
```
      - name: vscode
        image: 10.61.3.124:30002/library/isaac-launchable-vscode:6.0.0
        imagePullPolicy: Always
        env:
        - name: ACCEPT_EULA
```

`new_string`:
```
      - name: vscode
        image: 10.61.3.124:30002/library/isaac-launchable-vscode:6.0.0
        imagePullPolicy: Always
        # NET_ADMIN = /proc/sys/net/ipv4/ip_local_port_range 쓰기 권한.
        # runheadless-script-0 이 Kit ephemeral UDP bind 를 30998 로 pinning 할 때만 사용.
        # privileged:true 와 달리 커널 네트워크 sysctl write 만 허용하는 좁은 capability.
        securityContext:
          capabilities:
            add: ["NET_ADMIN"]
        env:
        - name: ACCEPT_EULA
```

- [ ] **Step 3: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add k8s/isaac-sim/deployment-0.yaml
git commit -m "feat(k8s): deployment-0 vscode 에 NET_ADMIN capability 추가

runheadless-script-0 가 /proc/sys/net/ipv4/ip_local_port_range 에
쓰려면 NET_ADMIN 이 필요. privileged:true 보다 좁은 권한이라
hostNetwork 수준의 보안 영향 없음.

pod-1 의 vscode 컨테이너는 변경 없음 (pod-1 은 ephemeral port pin
안함)."
```

---

### Task 6: deployment-0 — gateway 컨테이너 삭제

**Files:**
- Modify: `k8s/isaac-sim/deployment-0.yaml` lines 106–139 (gateway container block)

- [ ] **Step 1: gateway 블록 확인**

Run:
```bash
cd /Users/xiilab/git/isaac-launchable
sed -n '106,139p' k8s/isaac-sim/deployment-0.yaml
```
Expected: gateway 컨테이너 블록 (name: gateway, image: isaac-launchable-gateway-go:dev, resources 포함).

- [ ] **Step 2: gateway 컨테이너 블록 전체 삭제**

Use Edit tool:

`old_string`:
```
      - name: gateway
        # Go Pion SFU gateway: upstream peer ↔ Kit (pod loopback),
        # downstream peer ↔ browser (TURN relay enforced). Replaces the
        # Node.js signaling-only proxy so Kit's ephemeral UDP candidates
        # stay inside the pod while the browser reaches media via coturn.
        # Rollback: swap image back to isaac-launchable-gateway:dev.
        image: 10.61.3.124:30002/library/isaac-launchable-gateway-go:dev
        imagePullPolicy: Always
        env:
        - { name: KIT_SIGNAL_URL, value: "ws://127.0.0.1:49100" }
        - { name: TURN_URI, value: "turn:10.61.3.74:3478" }
        - { name: TURN_USERNAME, value: "isaac" }
        - name: TURN_CREDENTIAL
          valueFrom:
            secretKeyRef:
              name: isaac-launchable-turn
              key: pod0-cred
        - { name: LISTEN_ADDR, value: ":9000" }
        ports:
        - name: signaling
          containerPort: 9000
          protocol: TCP
        readinessProbe:
          httpGet:
            path: /healthz
            port: 9000
          periodSeconds: 5
        resources:
          requests:
            cpu: "500m"
            memory: "512Mi"
          limits:
            cpu: "2"
            memory: "2Gi"
      - name: nginx
```

`new_string`:
```
      - name: nginx
```

- [ ] **Step 3: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add k8s/isaac-sim/deployment-0.yaml
git commit -m "feat(k8s): deployment-0 gateway 컨테이너 삭제

Kit UDP port pin 으로 브라우저 ↔ Kit 직접 WebRTC 가 가능해져 SFU
gateway (Go Pion) 가 경로에서 불필요. 컨테이너 블록 제거.

롤백 대비로 gateway-go/ 소스는 보존. 이미지도 레지스트리에
남아있어 필요 시 다시 컨테이너 추가 가능."
```

---

### Task 7: deployment-0 — web-viewer SIGNAL_PATH / TURN env 정리

**Files:**
- Modify: `k8s/isaac-sim/deployment-0.yaml` (web-viewer env block, lines ~150–189)

- [ ] **Step 1: web-viewer env 현황 확인**

Run:
```bash
cd /Users/xiilab/git/isaac-launchable
sed -n '150,193p' k8s/isaac-sim/deployment-0.yaml
```
Expected: SIGNAL_PATH, TURN_URI, TURN_USERNAME, TURN_CREDENTIAL env 들이 포함된 web-viewer 블록.

- [ ] **Step 2: SIGNAL_PATH 라인 삭제 (gateway 경로 전용)**

Use Edit tool:

`old_string`:
```
        - name: SIGNAL_URL
          value: /sign_in
        - { name: SIGNAL_PATH, value: "/pod-0/signaling" }
        - name: TURN_URI
```

`new_string`:
```
        - name: SIGNAL_URL
          value: /sign_in
        - name: TURN_URI
```

(`SIGNAL_PATH` 는 gateway 경로로 signaling 을 우회시키는 env 였음. Kit 직결 복귀로 불필요. `SIGNAL_URL=/sign_in` 은 web-viewer 의 default 를 명시한 것이라 유지.)

- [ ] **Step 3: TURN env 삭제 (direct WebRTC 에서 불필요)**

Use Edit tool:

`old_string`:
```
        - name: TURN_URI
          valueFrom:
            configMapKeyRef:
              name: isaac-launchable-config
              key: TURN_URI
        - { name: TURN_USERNAME, value: "isaac" }
        - name: TURN_CREDENTIAL
          valueFrom:
            secretKeyRef:
              name: isaac-launchable-turn
              key: pod0-cred
        ports:
        - name: viewer
```

`new_string`:
```
        ports:
        - name: viewer
```

- [ ] **Step 4: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add k8s/isaac-sim/deployment-0.yaml
git commit -m "feat(k8s): deployment-0 web-viewer SIGNAL_PATH + TURN env 제거

Kit 직결 방식에서 브라우저는 default signalingPath '/sign_in' 을
그대로 사용하므로 SIGNAL_PATH 주입 불필요. direct WebRTC 에서는
TURN relay 강제도 불필요해 TURN_URI/USERNAME/CREDENTIAL env 삭제.

web-viewer 이미지 자체에서 RTCPeerConnection monkey-patch 도 제거
필요 (다음 태스크). 이 env 삭제만으로는 patch 가 조건적으로 스킵
되지만 이미지 재빌드로 명시적 제거도 추가로 진행."
```

---

## Phase C — Service + Ingress 정리

### Task 8: services.yaml — svc-0 의 port 9000 제거

**Files:**
- Modify: `k8s/base/services.yaml` lines 33–35

- [ ] **Step 1: 현재 svc-0 ports 확인**

Run:
```bash
cd /Users/xiilab/git/isaac-launchable
sed -n '22,36p' k8s/base/services.yaml
```
Expected:
```yaml
metadata:
  name: isaac-launchable-svc-0
  ...
  ports:
  - name: http
    port: 80
    targetPort: 80
  - name: signaling
    port: 9000
    targetPort: 9000
```

- [ ] **Step 2: signaling port 제거**

Use Edit tool:

`old_string`:
```
spec:
  type: ClusterIP
  selector:
    app: isaac-launchable
    instance: pod-0
  ports:
  - name: http
    port: 80
    targetPort: 80
  - name: signaling
    port: 9000
    targetPort: 9000
---
# Isaac Sim (Pod-1) ClusterIP 서비스
```

`new_string`:
```
spec:
  type: ClusterIP
  selector:
    app: isaac-launchable
    instance: pod-0
  ports:
  - name: http
    port: 80
    targetPort: 80
---
# Isaac Sim (Pod-1) ClusterIP 서비스
```

- [ ] **Step 3: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add k8s/base/services.yaml
git commit -m "chore(k8s): svc-0 의 signaling port 9000 제거

Gateway 제거로 svc-0 의 ClusterIP 9000 미사용. port 80 (nginx) 만
유지. svc-1 은 변경 없음."
```

---

### Task 9: ingress-0.yaml — `/pod-0/signaling` path 제거

**Files:**
- Modify: `k8s/isaac-sim/ingress-0.yaml` lines 22–28

- [ ] **Step 1: 현재 rule 확인**

Run:
```bash
cd /Users/xiilab/git/isaac-launchable
cat k8s/isaac-sim/ingress-0.yaml
```
Expected: `/pod-0/signaling` 와 `/` 두 path.

- [ ] **Step 2: /pod-0/signaling path 제거**

Use Edit tool:

`old_string`:
```
      paths:
      - path: /pod-0/signaling
        pathType: Prefix
        backend:
          service:
            name: isaac-launchable-svc-0
            port:
              number: 9000
      - path: /
        pathType: Prefix
        backend:
          service:
            name: isaac-launchable-svc-0
            port:
              number: 80
```

`new_string`:
```
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: isaac-launchable-svc-0
            port:
              number: 80
```

- [ ] **Step 3: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add k8s/isaac-sim/ingress-0.yaml
git commit -m "chore(k8s): ingress-0 의 /pod-0/signaling path 제거

Gateway 제거로 gateway 용 signaling path 불필요. / (nginx sidecar
80) rule 만 유지. ingress-1 은 변경 없음."
```

---

## Phase D — Web-viewer 이미지 재빌드

### Task 10: entrypoint.sh 에서 RTCPeerConnection monkey-patch 제거

**Files:**
- Modify: `isaac-lab/web-viewer-sample/entrypoint.sh` lines 82–119

- [ ] **Step 1: 현재 entrypoint.sh 의 patch 블록 확인**

Run:
```bash
cd /Users/xiilab/git/isaac-launchable
sed -n '82,119p' isaac-lab/web-viewer-sample/entrypoint.sh
```
Expected: `isaac-launchable-turn-override` 주석 및 `RTCPeerConnection` monkey-patch 블록.

- [ ] **Step 2: monkey-patch 블록 전체 제거**

Use Edit tool:

`old_string`:
```
  # Browser uses the library's default signalingPath ('/sign_in') and hits it
  # through the nginx sidecar's /sign_in -> 49100 proxy. The gateway sidecar
  # is unused on this path; we intentionally do NOT inject a signalingPath
  # override because the library appends its own '/sign_in' suffix, which
  # would produce a duplicated path.

  # Inject an RTCPeerConnection override at the top of main.ts so the browser
  # peer uses the coturn relay. DirectConfig does NOT expose iceServers /
  # iceTransportPolicy (verified against
  # @nvidia/omniverse-webrtc-streaming-library.d.ts in v1.x), so we can't pass
  # them via streamConfig. Overriding window.RTCPeerConnection before the
  # library imports is the supported hook to force
  # iceTransportPolicy='relay' with our TURN credentials on whatever
  # PeerConnection the library constructs internally.
  if [ -n "${TURN_URI}" ] && ! grep -q "isaac-launchable-turn-override" /app/web-viewer-sample/src/main.ts; then
    cat > /tmp/pc-override.snippet <<EOF
// isaac-launchable-turn-override: injected by entrypoint for coturn relay
;(function() {
  const OrigPC = window.RTCPeerConnection;
  const injected = {
    iceServers: [
      { urls: ['${TURN_URI}?transport=udp', '${TURN_URI}?transport=tcp'],
        username: '${TURN_USERNAME}', credential: '${TURN_CREDENTIAL}' }
    ],
    iceTransportPolicy: 'relay',
  };
  const Patched: any = function(cfg: any) {
    const merged = Object.assign({}, cfg || {}, injected);
    return new OrigPC(merged);
  };
  Patched.prototype = OrigPC.prototype;
  (window as any).RTCPeerConnection = Patched;
})();
EOF
    cat /tmp/pc-override.snippet /app/web-viewer-sample/src/main.ts > /tmp/main.ts.patched
    mv /tmp/main.ts.patched /app/web-viewer-sample/src/main.ts
    rm -f /tmp/pc-override.snippet
  fi

  exec npm run dev -- --host 0.0.0.0
}
```

`new_string`:
```
  # Browser uses the library's default signalingPath ('/sign_in') which nginx
  # proxies directly to Kit's :49100 (no gateway in path). ICE uses the host
  # candidate advertised by Kit (hostIP:30998), reachable via hostPort mapping,
  # so no TURN relay override is needed.

  exec npm run dev -- --host 0.0.0.0
}
```

- [ ] **Step 3: 불필요해진 TURN_* 변수 선언 제거**

Use Edit tool:

`old_string`:
```
# WebRTC Gateway routing: signaling path (default '/sign_in' preserves upstream behavior)
# and coturn TURN credentials for forced-relay peer connections on the browser side.
SIGNAL_PATH=${SIGNAL_PATH:-/sign_in}
TURN_URI=${TURN_URI:-}
TURN_USERNAME=${TURN_USERNAME:-}
TURN_CREDENTIAL=${TURN_CREDENTIAL:-}
```

`new_string`:
```
# signalingPath default '/sign_in' — browser library appends it automatically;
# nginx proxies /sign_in directly to Kit. No SIGNAL_PATH/TURN env needed in
# this deployment topology.
```

- [ ] **Step 4: main() 의 echo 로그에서 TURN_URI / TURN_USERNAME 제거**

Use Edit tool:

`old_string`:
```
  echo "  FORCE_WSS:        ${FORCE_WSS}"
  echo "  SIGNAL_PATH:      ${SIGNAL_PATH}"
  echo "  TURN_URI:         ${TURN_URI}"
  echo "  TURN_USERNAME:    ${TURN_USERNAME}"
```

`new_string`:
```
  echo "  FORCE_WSS:        ${FORCE_WSS}"
```

- [ ] **Step 5: 변경 결과 확인**

Run:
```bash
cd /Users/xiilab/git/isaac-launchable
grep -c "TURN_URI\|isaac-launchable-turn-override\|RTCPeerConnection" isaac-lab/web-viewer-sample/entrypoint.sh
```
Expected: `0` (모든 흔적 제거됨).

- [ ] **Step 6: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add isaac-lab/web-viewer-sample/entrypoint.sh
git commit -m "feat(web-viewer): remove RTCPeerConnection TURN override

direct WebRTC (Kit ↔ 브라우저) 로 전환했으므로 TURN relay 를 강제
하는 entrypoint.sh 의 window.RTCPeerConnection monkey-patch 및
TURN_URI/USERNAME/CREDENTIAL env 처리 제거.

signalingPath 는 기본값 '/sign_in' 그대로 사용 (nginx 가 Kit 49100
으로 직접 proxy). 이미지 재빌드 필요."
```

---

### Task 11: web-viewer 이미지 재빌드 + push

**Files:**
- No file changes; build + push only

- [ ] **Step 1: 작업 디렉토리로 sync (ws-node074 에서 빌드)**

Run:
```bash
rsync -avz --exclude='.git' /Users/xiilab/git/isaac-launchable/isaac-lab/web-viewer-sample/ root@10.61.3.74:/root/web-viewer-build/
```
Expected: entrypoint.sh, Dockerfile, src/ 등 전송 완료.

- [ ] **Step 2: ws-node074 에서 docker build**

Run:
```bash
ssh root@10.61.3.74 'cd /root/web-viewer-build && docker build -t 10.61.3.124:30002/library/isaac-launchable-viewer:latest .'
```
Expected: `naming to ... isaac-launchable-viewer:latest` 로 끝. 에러 없음.

- [ ] **Step 3: Harbor 로그인 + push**

Run:
```bash
ssh root@10.61.3.74 'echo "Harbor12345" | docker login 10.61.3.124:30002 -u admin --password-stdin && docker push 10.61.3.124:30002/library/isaac-launchable-viewer:latest'
```
Expected: `Login Succeeded`, push 후 `digest: sha256:... size: ...` 출력.

- [ ] **Step 4: push 결과 기록 (commit 없음 — 이미지 태그만 레지스트리 업데이트)**

Run:
```bash
ssh root@10.61.3.74 'docker images --no-trunc 10.61.3.124:30002/library/isaac-launchable-viewer:latest --format "{{.CreatedAt}} {{.ID}}"'
```
Expected: 방금 빌드한 이미지의 timestamp + digest 확인.

---

## Phase E — Cluster apply + rollout

### Task 12: ConfigMap + Service + Ingress + Deployment apply

**Files:**
- Apply only; no file changes

- [ ] **Step 1: 변경된 yaml 들을 cluster 에 복사**

Run:
```bash
scp /Users/xiilab/git/isaac-launchable/k8s/base/configmaps.yaml root@10.61.3.75:/tmp/configmaps.yaml
scp /Users/xiilab/git/isaac-launchable/k8s/base/services.yaml root@10.61.3.75:/tmp/services.yaml
scp /Users/xiilab/git/isaac-launchable/k8s/isaac-sim/deployment-0.yaml root@10.61.3.75:/tmp/deployment-0.yaml
scp /Users/xiilab/git/isaac-launchable/k8s/isaac-sim/ingress-0.yaml root@10.61.3.75:/tmp/ingress-0.yaml
```
Expected: 4 파일 모두 복사 성공.

- [ ] **Step 2: ConfigMaps + Service + Ingress apply (deployment 제외 먼저)**

Run:
```bash
ssh root@10.61.3.75 'k0s kubectl apply -f /tmp/configmaps.yaml -f /tmp/services.yaml -f /tmp/ingress-0.yaml'
```
Expected:
```
configmap/... configured
service/isaac-launchable-svc-0 configured
ingress.networking.k8s.io/isaac-launchable-ingress-0 configured
```
(pod-1 관련 리소스는 `unchanged`.)

- [ ] **Step 3: Deployment-0 apply**

Run:
```bash
ssh root@10.61.3.75 'k0s kubectl apply -f /tmp/deployment-0.yaml'
```
Expected: `deployment.apps/isaac-launchable-0 configured`.

- [ ] **Step 4: 오래된 pod 삭제 (resource deadlock 방지)**

Run:
```bash
ssh root@10.61.3.75 'k0s kubectl delete pod -n isaac-launchable -l instance=pod-0 --wait=false'
```
Expected: `pod "..." deleted`.

- [ ] **Step 5: Rollout 완료 대기**

Run:
```bash
sleep 25
ssh root@10.61.3.75 'k0s kubectl rollout status -n isaac-launchable deployment/isaac-launchable-0 --timeout=180s'
```
Expected: `deployment "isaac-launchable-0" successfully rolled out`.

- [ ] **Step 6: 컨테이너 개수 확인 (gateway 제거 — 3/3 이어야)**

Run:
```bash
ssh root@10.61.3.75 'k0s kubectl get pod -n isaac-launchable -l instance=pod-0'
```
Expected: `READY 3/3` (vscode + nginx + web-viewer; gateway 없음).

---

### Task 13: port pin 적용 + Kit bind 검증

**Files:**
- No file changes; runtime verification

- [ ] **Step 1: /proc/sys port range 확인**

Run:
```bash
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o jsonpath='{.items[0].metadata.name}'")
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- cat /proc/sys/net/ipv4/ip_local_port_range"
```
Expected: `30998	30998` (tab-separated).

**If Expected fails** (e.g. `32768 60999`): runheadless.sh 가 아직 실행되지 않았거나 NET_ADMIN 누락. Step 2 진행 후 재확인.

- [ ] **Step 2: Kit 기동 (runheadless.sh)**

Run:
```bash
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o jsonpath='{.items[0].metadata.name}'")
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- bash -c 'nohup /isaac-sim/runheadless.sh > /tmp/kit.log 2>&1 &'"
```
Expected: 백그라운드 실행 시작.

- [ ] **Step 3: port range pin 재확인 (runheadless.sh 첫 줄 실행 후)**

Run:
```bash
sleep 3
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o jsonpath='{.items[0].metadata.name}'")
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- cat /proc/sys/net/ipv4/ip_local_port_range"
```
Expected: `30998	30998` (pin 적용됨).

- [ ] **Step 4: Kit signaling TCP 49100 listen 대기**

Run:
```bash
for i in 1 2 3 4 5 6; do
  sleep 20
  POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o jsonpath='{.items[0].metadata.name}'")
  STATE=$(ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- ss -tlnp 2>/dev/null | grep 49100 || echo PENDING")
  echo "T+$((i*20))s: $STATE"
  [[ "$STATE" == *LISTEN* ]] && break
done
```
Expected: 80–120초 이내에 `LISTEN ... :49100 ... kit` 출력.

- [ ] **Step 5: Kit bind 검증 (WebRTC 세션이 시작 전까지는 UDP 가 안 열릴 수 있음 — note)**

Run:
```bash
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o jsonpath='{.items[0].metadata.name}'")
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- ss -unp 2>/dev/null | grep 30998 || echo NO-UDP-YET"
```
Expected: Kit 이 세션을 받으면 30998/UDP 가 `kit` 프로세스로 바인드됨. 세션 전에는 `NO-UDP-YET` 도 정상 (Task 14 에서 재확인).

- [ ] **Step 6: verification note 파일 작성**

Create: `docs/superpowers/plans/notes/2026-04-25-kit-port-pin-verification.md`

```markdown
# Kit port pin verification (2026-04-25)

## Pre-flight
- `/proc/sys/net/ipv4/ip_local_port_range` = `30998  30998` ✓
- Kit signaling TCP :49100 listen ✓
- Pod READY 3/3 (gateway 제거됨) ✓

## Kit bind snapshot (세션 전/후)
(실제 값 기록)

## 관찰된 ICE candidate
(브라우저 chrome://webrtc-internals 스크린샷 혹은 로그 요약)
```

- [ ] **Step 7: Commit (note 만)**

```bash
cd /Users/xiilab/git/isaac-launchable
git add docs/superpowers/plans/notes/2026-04-25-kit-port-pin-verification.md
git commit -m "docs: Kit port pin pre-flight verification notes"
```

---

## Phase F — E2E 검증

### Task 14: quadrupeds.py — 기준 시나리오

**Files:**
- None (run existing Isaac Lab script in pod)

- [ ] **Step 1: vscode 안에서 quadrupeds.py 실행 (기존 Kit 대체)**

Run:
```bash
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o jsonpath='{.items[0].metadata.name}'")
# Stop existing Kit first
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- bash -c 'pgrep -f runheadless | xargs -r kill -TERM'"
sleep 5
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- bash -c 'cd /workspace/isaaclab && nohup ./isaaclab.sh -p scripts/demos/quadrupeds.py --livestream 2 > /tmp/quadrupeds.log 2>&1 &'"
echo "quadrupeds.py launched"
```

- [ ] **Step 2: Isaac Lab startup 대기 (2분)**

Run:
```bash
sleep 120
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o jsonpath='{.items[0].metadata.name}'")
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- tail -30 /tmp/quadrupeds.log"
```
Expected: `app ready` 또는 `Simulation is now running` 메시지.

- [ ] **Step 3: 브라우저에서 http://10.61.3.125/viewer/ 접속 (사용자 수동)**

Manual verification:
- Browser URL: `http://10.61.3.125/viewer/`
- Expected: 4족 로봇이 걷는 장면
- `chrome://webrtc-internals` → `inbound-rtp` (kind=video, codec=H264) 활성, `candidate-pair` remote = `10.61.3.74:30998` (`typ host`, NOT `relay`)

- [ ] **Step 4: Kit UDP 30998 bind 확인 (세션 중)**

Run:
```bash
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o jsonpath='{.items[0].metadata.name}'")
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- ss -unp 2>/dev/null | grep 30998"
```
Expected: `UNCONN ... 0.0.0.0:30998 ... kit,pid=N`

- [ ] **Step 5: 노드 hostPort 매핑 확인**

Run:
```bash
ssh root@10.61.3.74 'sudo ss -ulnp 2>/dev/null | grep 30998 | head -3'
```
Expected: host 에서 30998/UDP 가 k8s CNI portmap 바이너리로 listen (LISTEN 이 없는 UDP 는 `UNCONN` 상태).

- [ ] **Step 6: Verification note 업데이트**

Append to `docs/superpowers/plans/notes/2026-04-25-kit-port-pin-verification.md`:

```markdown
## Task 14 — quadrupeds.py
- Isaac Lab startup: OK (log 에 `app ready` 확인)
- UDP 30998 bind: ss -unp 출력에서 kit 프로세스 확인됨
- 브라우저 렌더링: (사용자 확인 결과 기록)
- chrome://webrtc-internals candidate-pair remote: `10.61.3.74:30998` typ host
```

- [ ] **Step 7: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add docs/superpowers/plans/notes/2026-04-25-kit-port-pin-verification.md
git commit -m "docs: quadrupeds.py verification OK — port pin works"
```

---

### Task 15: play.py — 핵심 목표

**Files:**
- None (run existing Isaac Lab script in pod)

- [ ] **Step 1: quadrupeds.py 종료**

Run:
```bash
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o jsonpath='{.items[0].metadata.name}'")
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- bash -c 'pkill -TERM -f quadrupeds 2>/dev/null; sleep 3; pkill -KILL -f kit 2>/dev/null; true'"
sleep 10
```
Expected: 이전 Kit 프로세스가 종료.

- [ ] **Step 2: play.py 실행**

Run:
```bash
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o jsonpath='{.items[0].metadata.name}'")
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- bash -c 'cd /workspace/isaaclab && nohup ./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/play.py --task Isaac-Ant-v0 --num_envs 4 --livestream 2 --use_pretrained_checkpoint > /tmp/play.log 2>&1 &'"
echo "play.py launched"
```

- [ ] **Step 3: Startup 대기 + log 확인**

Run:
```bash
sleep 150
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o jsonpath='{.items[0].metadata.name}'")
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- tail -40 /tmp/play.log"
```
Expected: `app ready` + 시뮬레이션 steps 진행 로그 (`[INFO]: ...`).

- [ ] **Step 4: 브라우저에서 로봇 움직임 확인 (사용자 수동)**

Manual verification:
- Browser URL: `http://10.61.3.125/viewer/`
- Expected: Ant (4족 로봇) 이 policy 에 따라 걷는 장면
- 에러 없이 inbound-rtp 계속 활성

- [ ] **Step 5: Verification note 업데이트**

Append to `docs/superpowers/plans/notes/2026-04-25-kit-port-pin-verification.md`:

```markdown
## Task 15 — play.py
- Script: scripts/reinforcement_learning/rsl_rl/play.py --task Isaac-Ant-v0 --num_envs 4 --livestream 2 --use_pretrained_checkpoint
- Startup: OK
- 브라우저 렌더링: (사용자 확인 결과)
- 이전 Isaac Lab upstream issue #5364 회피 성공 여부: (기록)
```

- [ ] **Step 6: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add docs/superpowers/plans/notes/2026-04-25-kit-port-pin-verification.md
git commit -m "docs: play.py verification"
```

---

### Task 16: train.py — 장기 검증

**Files:**
- None (run existing Isaac Lab script in pod)

- [ ] **Step 1: play.py 종료**

Run:
```bash
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o jsonpath='{.items[0].metadata.name}'")
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- bash -c 'pkill -TERM -f play.py 2>/dev/null; sleep 3; pkill -KILL -f kit 2>/dev/null; true'"
sleep 10
```

- [ ] **Step 2: train.py 실행 (5 iterations 단기 검증)**

Run:
```bash
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o jsonpath='{.items[0].metadata.name}'")
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- bash -c 'cd /workspace/isaaclab && nohup ./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/train.py --task Isaac-Ant-v0 --num_envs 4 --max_iterations 5 --livestream 2 > /tmp/train.log 2>&1 &'"
echo "train.py launched"
```

- [ ] **Step 3: Startup 대기 + 완료 확인**

Run:
```bash
sleep 180
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o jsonpath='{.items[0].metadata.name}'")
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- tail -40 /tmp/train.log"
```
Expected: `iteration 5/5` 또는 비슷한 완료 로그, Kit 이 계속 running.

- [ ] **Step 4: 브라우저에서 학습 장면 확인 (사용자 수동)**

Manual verification:
- Ant 들이 랜덤 움직임에서 학습되어가는 모습
- 5 iterations 중 연속 스트림 (끊김 없음)

- [ ] **Step 5: Verification note 업데이트**

Append to `docs/superpowers/plans/notes/2026-04-25-kit-port-pin-verification.md`:

```markdown
## Task 16 — train.py (5 iterations)
- Script: train.py --max_iterations 5 --livestream 2
- 5 iterations 완료: (확인)
- 세션 연속성 (reconnect 없이): (확인)
```

- [ ] **Step 6: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add docs/superpowers/plans/notes/2026-04-25-kit-port-pin-verification.md
git commit -m "docs: train.py 5-iteration verification"
```

---

## Phase G — 정리 + 메모리 업데이트

### Task 17: 메모리 / 문서 마무리

**Files:**
- Modify: `/Users/xiilab/.claude/projects/-Users-xiilab-git-HAMi/memory/project_isaac_lab_livestream_status.md`

- [ ] **Step 1: 메모리 업데이트**

해당 memory 파일 끝에 다음 섹션 추가:

```markdown
## 2026-04-25 세션 — A1 (Kernel ip_local_port_range pin) 성공

### 해결 방식
- Kit 의 `bind(port=0)` ephemeral 할당을 `/proc/sys/net/ipv4/ip_local_port_range = 30998 30998` 로 강제
- `runheadless-script-0` 맨 위에 `echo "30998 30998" > ...` 추가
- vscode 컨테이너에 `NET_ADMIN` capability
- `ISAACSIM_HOST=status.hostIP` + `hostPort: 30998/UDP` 로 advertise 와 bind 일치
- Gateway (gateway-go / Node.js proxy) 는 경로에서 제거

### 관련 spec/plan
- `docs/superpowers/specs/2026-04-25-kit-port-pin-design.md`
- `docs/superpowers/plans/2026-04-25-kit-port-pin.md`
- `docs/superpowers/plans/notes/2026-04-25-kit-port-pin-verification.md`

### 검증 결과
- quadrupeds.py: OK (기준)
- play.py: OK (Isaac Lab issue #5364 의 증상이 Kit 네트워크 도달성 문제였음으로 확인)
- train.py: OK (5 iterations 연속 스트림)

### pod-1 / 업스트림 영향
- pod-1: 건드리지 않음
- Isaac Lab issue #5362 / #5363: 별건 (AppLauncher / obs_groups 관련)
- #5364: 네트워크 경로 회피로 해결됐지만 업스트림 bug 자체는 남아있음 — 동일 환경에 hostPort 없으면 재발
```

- [ ] **Step 2: Commit (memory 는 별도 repo — 그냥 저장만)**

메모리 파일은 git 대상이 아니므로 저장만 하면 됨.

- [ ] **Step 3: gateway-go / Node.js gateway 소스 정리 여부 결정**

옵션 A (보존 — 권장): 소스는 레포에 남기고 README 에 deprecated 표시
옵션 B (제거): `rm -rf gateway/ gateway-go/` + git commit

**이번 plan 에서는 옵션 A 로 진행.** gateway README 에 경고문 추가:

Create: `gateway/DEPRECATED.md`
```markdown
# DEPRECATED: Node.js signaling proxy

2026-04-25 부로 사용 중단. 대체: Kit UDP port pin
(docs/superpowers/specs/2026-04-25-kit-port-pin-design.md).

컨테이너 이미지는 레지스트리 (10.61.3.124:30002/library/
isaac-launchable-gateway:dev) 에 남아있어 rollback 시 사용 가능.
```

Create: `gateway-go/DEPRECATED.md`
```markdown
# DEPRECATED: Go Pion SFU gateway

2026-04-25 부로 사용 중단. 대체: Kit UDP port pin
(docs/superpowers/specs/2026-04-25-kit-port-pin-design.md).

Pion SFU 가 Kit 의 실제 UDP bind port 에 도달할 수 없다는 본질적
블로커가 있었으나 (Kit 이 advertise 하는 streamPort 와 bind port
가 서로 다른 ephemeral scheme), kernel 레벨 port pin 으로 이
문제를 우회.

컨테이너 이미지는 레지스트리 (10.61.3.124:30002/library/
isaac-launchable-gateway-go:dev) 에 남아있어 rollback 시 사용 가능.
```

- [ ] **Step 4: Commit DEPRECATED 마커**

```bash
cd /Users/xiilab/git/isaac-launchable
git add gateway/DEPRECATED.md gateway-go/DEPRECATED.md
git commit -m "docs: mark gateway/ and gateway-go/ as deprecated

Kit UDP port pin (2026-04-25-kit-port-pin-design) 으로 대체.
이미지는 registry 에 보존, rollback 경로 유지."
```

---

## Final Checklist

- [ ] Phase A–G 모든 task 완료 체크
- [ ] quadrupeds.py / play.py / train.py 모두 브라우저에서 로봇 렌더링 확인
- [ ] chrome://webrtc-internals candidate-pair remote 가 `10.61.3.74:30998 typ host` 인지 확인
- [ ] pod-1 관련 파일 git diff 가 비어있음 검증: `git diff HEAD~20 -- k8s/base/configmaps.yaml k8s/isaac-sim/deployment-1.yaml k8s/isaac-sim/ingress-1.yaml | grep -E '^[+-]' | grep -v '^[+-]{3}' | wc -l` → pod-1 관련 변경 라인 0 이어야.
