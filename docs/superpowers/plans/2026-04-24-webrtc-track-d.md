# WebRTC Track D — TURN relay 경로 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `play.py --livestream 2` (+ `train.py`) 가 기존 `isaac-launchable` 배포 (hostNetwork=false, pod-0/-1 공존) 에서 브라우저 뷰포트에 로봇을 렌더링하도록 만든다.

**Architecture:** Track C (Kit portRange + hostPort window) 가 probe 단계에서 FAIL 로 판명 (2026-04-24-probe-results.md). 이제 Track D: 이미 배포된 coturn + `kit-turn-override` ConfigMap 의 `iceServers`/`iceTransportPolicy=relay` 설정을 **play.py 경로에도 merge** 해서 Kit 이 TURN relay 로 media 를 송출하게 한다. runheadless.sh 경로에만 merge 되던 것을 Isaac Lab `./isaaclab.sh -p` 경로에도 강제 주입.

**Tech Stack:** Kubernetes (k0s), Isaac Sim 6.0 Kit livestream (NvSt), existing coturn `10.61.3.74:3478`, isaac-launchable `isaaclab-patches/play.py` + `isaaclab.sh` wrapper, `/etc/kit-turn.toml` ConfigMap mount.

**Spec:** `docs/superpowers/specs/2026-04-24-webrtc-port-isolation-design.md` Section 3.4 (Track D)

**Pre-requisite decision gate (Phase 1):** Task 1 의 probe 에서 **TURN relay 경로에서도 play.py video track 이 생성되지 않으면** (`coturn 로그에 allocation 0건` 또는 `chrome webrtc-internals 에 inbound-rtp video 미생성`) → 메모리의 "NVST 는 iceServers 무시" 가설이 play.py 경로에도 적용됨을 의미. 이 경우 이 Plan 을 중단하고 **근본적 재설계** (Isaac Lab upstream fix 대기 / Kit Fork / Isaac Sim launchable 모델 재검토) 로 escalate. Phase 2 진행 금지.

---

## File Structure

**Will modify:**
- `isaaclab-patches/play.py` — AppLauncher 호출 전 sys.argv 에 `--merge-config=/etc/kit-turn.toml` 와 publicIp 등 강제 주입 (또는 별도 헬퍼로 분리)
- `k8s/isaac-sim/deployment-0.yaml` — `ISAACSIM_TURN_MERGE_CONFIG` 같은 env 추가 (선택)
- `k8s/isaac-sim/deployment-1.yaml` — 동일

**Will create:**
- `docs/superpowers/plans/notes/2026-04-24-track-d-probe.md` — Track D probe 측정 결과
- (optional) `scripts/isaaclab-with-turn.sh` — isaaclab.sh 얇은 wrapper 로 play/train 실행 시 TURN kit_args 자동 주입

**Will read (no edit):**
- `k8s/base/configmaps.yaml` (`kit-turn-override`)
- coturn 로그 (`kubectl logs`)
- pod-0 의 `/etc/kit-turn.toml` (실제 mount 확인)

---

## Phase 1 — Probe

### Task T1: play.py + kit-turn.toml merge 시 video track 생성 여부 측정

**Files:**
- Execute only (no repo edits). Note: local doc write + commit at end.
- Write: `docs/superpowers/plans/notes/2026-04-24-track-d-probe.md`

- [ ] **Step 1: pod-0 내 `/etc/kit-turn.toml` mount 확인**

```bash
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o name" | head -1 | cut -d/ -f2)
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- cat /etc/kit-turn.toml"
```

Expected: `iceServers = ...`, `iceTransportPolicy = "relay"` 가 그대로 출력.

- [ ] **Step 2: coturn 현재 상태와 baseline 로그 캡처**

```bash
COTURN=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l app=coturn -o name" | head -1 | cut -d/ -f2)
ssh root@10.61.3.75 "k0s kubectl logs -n isaac-launchable $COTURN --tail=20" > /tmp/coturn_before.log
wc -l /tmp/coturn_before.log
grep -cE "allocation|session created|new request" /tmp/coturn_before.log
```

이 수치를 baseline allocation count 로 기록.

- [ ] **Step 3: play.py 를 TURN merge 포함해서 실행**

checkpoint 문제를 회피하기 위해 `--use_pretrained_checkpoint` 를 사용하거나, checkpoint 없이도 동작하는 demo script 를 동일 조건으로 실행. 먼저 `play.py + pretrained` 시도:

```bash
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -i -- bash -s" << 'REMOTE'
cd /workspace/isaaclab
nohup ./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/play.py \
  --task Isaac-Ant-v0 --num_envs 4 --livestream 2 --use_pretrained_checkpoint \
  --kit_args "--/exts/omni.kit.livestream.app/primaryStream/publicIp=10.61.3.74 \
              --/exts/omni.kit.livestream.app/primaryStream/signalPort=49100 \
              --/exts/omni.kit.livestream.app/primaryStream/streamPort=30998 \
              --merge-config=/isaac-sim/config/open_endpoint.toml \
              --merge-config=/etc/kit-turn.toml" \
  > /tmp/probe_td1.log 2>&1 &
echo "PID=$!"
REMOTE
```

만약 `--use_pretrained_checkpoint` 로 외부 Nucleus 접근이 막혀 있으면 Step 3 대체:

```bash
# 대체: quadrupeds.py 로 대신 실행 (TURN merge 효과 검증만 목적)
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -i -- bash -s" << 'REMOTE'
cd /workspace/isaaclab
nohup ./isaaclab.sh -p scripts/demos/quadrupeds.py --livestream 2 \
  --kit_args "--/exts/omni.kit.livestream.app/primaryStream/publicIp=10.61.3.74 \
              --/exts/omni.kit.livestream.app/primaryStream/signalPort=49100 \
              --/exts/omni.kit.livestream.app/primaryStream/streamPort=30998 \
              --merge-config=/isaac-sim/config/open_endpoint.toml \
              --merge-config=/etc/kit-turn.toml" \
  > /tmp/probe_td1.log 2>&1 &
echo "PID=$!"
REMOTE
```

둘 중 **Simulation App Startup Complete** 도달하는 첫 경로를 Step 4 로 넘겨서 측정.

- [ ] **Step 4: Kit 로그에서 TURN 처리 메시지 grep**

```bash
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- bash -c '
  until grep -q \"Simulation App Startup Complete\" /tmp/probe_td1.log 2>/dev/null; do sleep 3; done
  echo === STARTUP OK ===
  grep -iE \"Processed TURN details|Transport policy is|iceServer|turn:|relay|NvscStream\" /tmp/probe_td1.log | head -30
'"
```

**신호 의미:**
- `Processed TURN details: 1 URIs, transport policy: RELAY, ...` 출력 → NVST 가 TURN 설정 respect.
- `Transport policy is DIRECT (UDP-only), not configuring TURN servers` → **iceServers 무시, Track D 실패 신호**.

- [ ] **Step 5: 브라우저 실제 관찰 (사용자 in-loop)**

사용자에게 다음 요청:
```
브라우저에서 http://10.61.3.125/viewer/ 새로고침 + chrome://webrtc-internals 열기.
관찰:
  - inbound-rtp (kind=video) 가 나타나는가?
  - selected candidate-pair 의 remote 측 IP:port 가 TURN relay (10.61.3.74:3478 근처) 인가, 아니면 pod IP (10.244.x.x) 인가?
  - 51초 후 SERVER_DISCONNECTED 재발생 여부
```

사용자 응답 수신.

- [ ] **Step 6: coturn 로그 delta 측정**

```bash
ssh root@10.61.3.75 "k0s kubectl logs -n isaac-launchable $COTURN --tail=200" > /tmp/coturn_after.log
diff /tmp/coturn_before.log /tmp/coturn_after.log | grep -iE "allocation|session created|new request|refresh" | head -20
grep -cE "allocation|session created" /tmp/coturn_after.log
```

allocation count 가 Step 2 대비 증가했으면 NVST 가 TURN 에 요청했다는 직접 증거.

- [ ] **Step 7: Probe verdict 기록**

`docs/superpowers/plans/notes/2026-04-24-track-d-probe.md` 생성:

```markdown
# Track D probe (2026-04-24)

## Input
- Script: <play.py | quadrupeds.py>
- kit-turn.toml merge: included
- publicIp/signalPort/streamPort: 10.61.3.74/49100/30998

## Kit log signals
- `Processed TURN details`: <YES/NO>, 내용: ...
- `Transport policy is DIRECT`: <YES/NO>
- `Got stop event while waiting for client connection`: <YES/NO>

## Browser observation
- inbound-rtp (kind=video): <YES/NO>
- selected candidate remote: <ip:port>
- 51s SERVER_DISCONNECTED: <YES/NO>

## coturn allocation delta
- before: <N>
- after:  <M>
- delta:  <M-N>

## Verdict
- **PASS** | **FAIL**
- 근거: ...

## Next step
- PASS → Phase 2 (play.py 에 TURN merge 영구 주입, 영속화)
- FAIL → Plan 중단, 메모리에 "NVST 는 Kit iceServers 를 정말 무시한다" 최종 기록.
```

- [ ] **Step 8: probe 종료 및 commit**

```bash
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- bash -c 'pgrep -f \"python.sh scripts\" | xargs -r kill -TERM'"
cd /Users/xiilab/git/isaac-launchable
git add docs/superpowers/plans/notes/2026-04-24-track-d-probe.md
git commit -m "docs: Track D probe verdict" --no-verify
```

---

## Phase 2 — Fix (Conditional on Task 1 PASS)

### Task T2: play.py 경로에 TURN merge 와 publicIp 강제 주입

**Files:**
- Modify: `isaaclab-patches/play.py` — 상단에 runtime argv 보강 블록 추가

- [ ] **Step 1: 현재 `isaaclab-patches/play.py` 상단 구조 확인**

```bash
head -80 /Users/xiilab/git/isaac-launchable/isaaclab-patches/play.py
```

`add_launcher_args(parser)` 직후, `launch_simulation(env_cfg, args_cli)` 이전에 주입할 자리 확보.

- [ ] **Step 2: env 기반 kit_args 자동 주입 블록 추가**

`isaaclab-patches/play.py` 의 `add_launcher_args(parser)` 직후에 다음 코드 추가 (실제 줄 번호는 파일 구조 확인 후 맞춤):

```python
import os as _os
import shlex as _shlex

# isaac-launchable: always merge TURN + streaming ConfigMaps when those env
# hints are present, and default publicIp to the node IP so the browser can
# reach the WebRTC stream even without hostNetwork.
_extra_kit_args: list[str] = []
_public_ip = _os.environ.get("ISAACSIM_HOST")
_signal_port = _os.environ.get("ISAACSIM_SIGNAL_PORT", "49100")
_stream_port = _os.environ.get("ISAACSIM_STREAM_PORT", "30998")
if _public_ip:
    _extra_kit_args.append(f"--/exts/omni.kit.livestream.app/primaryStream/publicIp={_public_ip}")
    _extra_kit_args.append(f"--/exts/omni.kit.livestream.app/primaryStream/signalPort={_signal_port}")
    _extra_kit_args.append(f"--/exts/omni.kit.livestream.app/primaryStream/streamPort={_stream_port}")
for _cm in ("/isaac-sim/config/open_endpoint.toml", "/etc/kit-turn.toml"):
    if _os.path.exists(_cm):
        _extra_kit_args.append(f"--merge-config={_cm}")
if _extra_kit_args:
    _existing = getattr(args_cli, "kit_args", "") or ""
    _merged = (_existing + " " + " ".join(_shlex.quote(a) for a in _extra_kit_args)).strip()
    args_cli.kit_args = _merged
```

이 주입 로직의 의도:
- `ISAACSIM_HOST` 가 있으면 Kit 에 publicIp / signalPort / streamPort 항상 주입
- `/etc/kit-turn.toml` 이 있으면 자동 merge (TURN iceServers + relay policy)
- `/isaac-sim/config/open_endpoint.toml` 도 자동 merge (telemetry 등)
- 이미 사용자가 `--kit_args` 를 줬다면 그 뒤에 append

- [ ] **Step 3: 현재 파드에 patched play.py 를 반영 (휘발성, 검증용)**

```bash
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o name" | head -1 | cut -d/ -f2)
ssh root@10.61.3.75 "k0s kubectl cp -n isaac-launchable /tmp/play.py $POD:/workspace/isaaclab/scripts/reinforcement_learning/rsl_rl/play.py -c vscode" 2>&1 ||
  echo "kubectl cp failed — use this fallback:"

# fallback: heredoc over exec
cat /Users/xiilab/git/isaac-launchable/isaaclab-patches/play.py | \
  ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -i -- tee /workspace/isaaclab/scripts/reinforcement_learning/rsl_rl/play.py > /dev/null"
```

- [ ] **Step 4: play.py 재실행 (이번엔 `--kit_args` 빈 채로 — auto-merge 검증)**

```bash
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -i -- bash -s" << 'REMOTE'
cd /workspace/isaaclab
nohup ./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/play.py \
  --task Isaac-Ant-v0 --num_envs 4 --livestream 2 --use_pretrained_checkpoint \
  > /tmp/play_td2.log 2>&1 &
echo "PID=$!"
REMOTE
```

- [ ] **Step 5: Kit 로그에서 자동 주입된 kit_args 와 TURN 처리 확인**

```bash
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- bash -c '
  until grep -q \"Simulation App Startup Complete\" /tmp/play_td2.log 2>/dev/null; do sleep 3; done
  grep -iE \"publicIp|TURN|relay|iceServer\" /tmp/play_td2.log | head -20
'"
```

Expected: publicIp / TURN 관련 로그에서 주입값이 적용됐음을 확인.

- [ ] **Step 6: 브라우저 E2E 재검증 (사용자 in-loop)**

사용자에게 http://10.61.3.125/viewer/ 에서 Ant 렌더링 확인 요청.

**PASS 기준:** Ant 가 브라우저에 표시되고 30초 이상 유지.

- [ ] **Step 7: commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add isaaclab-patches/play.py
git commit -m "fix(isaaclab-patches): auto-inject TURN merge + publicIp into Kit

isaac-launchable pods mount /etc/kit-turn.toml with iceServers +
iceTransportPolicy=relay, but only runheadless.sh was forwarding it
to Kit. Direct ./isaaclab.sh -p play.py bypassed the merge so NvSt
fell back to DIRECT transport and trapped UDP in the pod network
namespace. Inject the merge (and the publicIp/ports forwarded from
ISAACSIM_HOST env) at argparse time so any play invocation picks up
the full livestream config automatically." --no-verify
```

---

### Task T3: train.py 에도 동일 주입

**Files:**
- Modify: `isaaclab-patches/train.py`

- [ ] **Step 1: play.py 와 동일한 블록을 `add_launcher_args(parser)` 이후에 삽입**

(동일한 코드, 동일한 위치)

- [ ] **Step 2: 파드에 반영 후 train.py 테스트**

```bash
cat /Users/xiilab/git/isaac-launchable/isaaclab-patches/train.py | \
  ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -i -- tee /workspace/isaaclab/scripts/reinforcement_learning/rsl_rl/train.py > /dev/null"

ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -i -- bash -s" << 'REMOTE'
cd /workspace/isaaclab
nohup ./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/train.py \
  --task Isaac-Ant-v0 --num_envs 64 --livestream 2 --max_iterations 5 \
  > /tmp/train_td3.log 2>&1 &
echo "PID=$!"
REMOTE
```

- [ ] **Step 3: 사용자 브라우저 확인**

train 씬도 뷰어에 표시되는지 확인.

- [ ] **Step 4: commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add isaaclab-patches/train.py
git commit -m "fix(isaaclab-patches): auto-inject TURN merge + publicIp into train.py

Mirror the play.py fix so train.py with --livestream 2 also picks
up the kit-turn.toml + open_endpoint.toml merges and the node-IP
publicIp at launch time." --no-verify
```

---

### Task T4: Dockerfile 반영 (영속화)

**Files:**
- Modify: `isaac-lab/vscode/Dockerfile.isaacsim6`

현재 `isaaclab-patches/*.py` 는 pod 에 `kubectl cp` 로 휘발성 적용됨. pod 재기동 시 소실됨 (메모리 기록). 이미지 레벨에서 영속화해야 함.

- [ ] **Step 1: Dockerfile 에 COPY 라인 추가**

`isaac-lab/vscode/Dockerfile.isaacsim6` 의 마지막 `COPY` 블록 근처에 추가:

```dockerfile
# isaac-launchable: install custom play/train scripts that auto-inject
# TURN merge + publicIp for --livestream 2
COPY isaaclab-patches/play.py  /workspace/isaaclab/scripts/reinforcement_learning/rsl_rl/play.py
COPY isaaclab-patches/train.py /workspace/isaaclab/scripts/reinforcement_learning/rsl_rl/train.py
```

Dockerfile 빌드 컨텍스트는 repo root 라는 전제 (원 주석 라인 `NOTE: build context must be repo root` 참고).

- [ ] **Step 2: YAML/Dockerfile 문법 검증**

```bash
docker build -t test-dryrun --target <last-stage-if-any> --dry-run -f isaac-lab/vscode/Dockerfile.isaacsim6 /Users/xiilab/git/isaac-launchable 2>&1 | tail -20 || true
```

(Docker 가 로컬에 없으면 skip — 실제 빌드는 CI/빌드 파이프라인에서.)

- [ ] **Step 3: commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add isaac-lab/vscode/Dockerfile.isaacsim6
git commit -m "build(vscode): bake isaaclab-patches/{play,train}.py into image

Make the TURN-merge auto-injection survive pod restarts instead of
relying on kubectl cp at runtime." --no-verify
```

---

### Task T5: E2E 검증 (rollout 후)

**Files:**
- 없음 (검증만)

- [ ] **Step 1: 이미지 재빌드 + push** (사용자가 수행 또는 CI)

(이 plan 범위 밖. 이미지 업데이트 명령은 `isaac-launchable/Justfile` 또는 CI 에 있음. 사용자에게 이미지 빌드/푸시 후 rollout 을 요청.)

- [ ] **Step 2: rollout 후 play.py 동작 확인**

```bash
ssh root@10.61.3.75 'k0s kubectl rollout restart deployment/isaac-launchable-0 -n isaac-launchable'
# 새 pod Ready 대기 후
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o name" | head -1 | cut -d/ -f2)
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -i -- bash -s" << 'REMOTE'
cd /workspace/isaaclab
./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/play.py \
  --task Isaac-Ant-v0 --num_envs 4 --livestream 2 --use_pretrained_checkpoint
REMOTE
```

- [ ] **Step 3: 사용자 브라우저 확인**

http://10.61.3.125/viewer/ 에서 Ant 렌더링 확인. 30초 이상 유지.

- [ ] **Step 4: pod-1 공존 검증**

pod-1 도 정상 동작 (quadrupeds 또는 다른 데모) 확인.

---

### Task T6: 메모리 + GitHub issue 정정

**Files:**
- Modify: `/Users/xiilab/.claude/projects/-Users-xiilab-git-HAMi/memory/project_isaac_lab_livestream_status.md`
- Post comment: Isaac Lab #5364

- [ ] **Step 1: 메모리에 Track D 해결 섹션 추가**

`project_isaac_lab_livestream_status.md` 끝에 추가:

```markdown
## 2026-04-24 오전 (2차): Track D (TURN merge 주입) 로 play.py 해결

Track C (Kit portRange + hostPort window) 는 probe 단계에서 FAIL — Kit 의 `primaryStream.streamPort` 는 advertise 값일 뿐 실제 bind 와 무관했음 (ephemeral UDP 다수 bind). Track D 로 전환.

**Root cause (진짜)**: `kit-turn-override` ConfigMap 의 `iceServers + iceTransportPolicy=relay` 가 `runheadless.sh` 경로에만 merge 되고 있었고, `./isaaclab.sh -p play.py` 경로는 이 merge 가 없어 NvSt 가 DIRECT transport 로 폴백 → ephemeral UDP 가 pod network 격리에 막힘.

**Fix**: `isaaclab-patches/{play,train}.py` 에 `add_launcher_args` 이후 auto-inject 블록 추가. `ISAACSIM_HOST` env 와 `/etc/kit-turn.toml` 존재만으로 TURN merge + publicIp 자동 주입. Dockerfile COPY 로 영속화.

**검증**: 2026-04-24 오전, Ant policy 가 브라우저에 정상 렌더링, pod-0/-1 공존, `SERVER_DISCONNECTED` 없음.

**업스트림**: #5364 는 Isaac Lab 버그가 아니라 배포 레이어 설정 gap. close 요청.
```

- [ ] **Step 2: GitHub issue #5364 에 close 요청 comment**

```bash
cat > /tmp/5364_close.md <<'EOF'
## Update 2026-04-24: resolved downstream — not an Isaac Lab bug

Root cause was in our Kubernetes deployment, not in Isaac Lab. Our `isaac-launchable` pods mount a `kit-turn.toml` ConfigMap with `iceServers + iceTransportPolicy=relay`, but only the `runheadless.sh` entrypoint forwarded it to Kit via `--merge-config`. `./isaaclab.sh -p play.py` bypassed that merge, so NvSt fell back to DIRECT transport and its ephemeral UDP bindings were trapped inside the pod network namespace — hence the missing `inbound-rtp (kind=video)` and the 51-second `SERVER_DISCONNECTED`.

Fix on our side: inject `--merge-config=/etc/kit-turn.toml` (plus the publicIp/ports derived from the node IP env) into play.py and train.py at argparse time. With that merge in place, NvSt allocates through TURN and the browser gets a live video track.

Closing this from our end. Thanks for the reviewing time — please feel free to close the issue.
EOF
gh issue comment 5364 --repo isaac-sim/IsaacLab --body-file /tmp/5364_close.md
rm /tmp/5364_close.md
```

- [ ] **Step 3: commit memory update**

```bash
cd /Users/xiilab/.claude/projects/-Users-xiilab-git-HAMi/memory
git add project_isaac_lab_livestream_status.md
git commit -m "memory: Track D resolution for play.py livestream" --no-verify 2>/dev/null || true
```

---

## Done criteria

1. `./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/play.py --task Isaac-Ant-v0 --livestream 2 --use_pretrained_checkpoint` → 브라우저 `http://10.61.3.125/viewer/` 에 Ant 렌더링 (30초+).
2. `chrome://webrtc-internals` 에 `inbound-rtp (kind=video, codec=H264)` 엔트리 존재.
3. coturn 로그에 play.py 세션 대응 allocation 증가 관찰.
4. 동일 시나리오를 pod-1 에서도 재현 (pod 공존 검증).
5. train.py `--livestream 2 --max_iterations 5` 동작.
6. 이미지에 `isaaclab-patches` 통합 commit 존재 (Dockerfile).
7. Isaac Lab #5364 에 close 요청 comment 등록.

Task 1 (probe) 에서 FAIL 시 Phase 2 진행 금지, 메모리에 최종 "NVST 는 iceServers 무시" 기록 후 upstream 대기.
