# Isaac Lab RL viewport streaming 패치 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Isaac Lab `play.py`/`train.py` 가 `--livestream 2` 에서 Kit viewport 를 WebRTC video track 에 push 하도록, isaac-launchable 이미지 빌드 시 파일 overlay 방식으로 해결한다.

**Architecture:** isaac-launchable 레포에 `isaaclab-patches/` 디렉토리를 만들고 수정된 `play.py`/`train.py` 를 둔다. Dockerfile 의 Isaac Lab clone 단계 뒤에 `COPY` 로 원본을 overlay 한다. 핵심 변경은 2가지: `parser.set_defaults(visualizer=["kit"])` 와 `env.unwrapped.sim.set_camera_view(...)`. 이미 검증된 workaround (`obs_groups` 주입, ONNX/JIT export 주석) 도 `play.py` 에 포함한다.

**Tech Stack:** Isaac Lab 2.1.1, Isaac Sim 6.0.0-rc.22, Python 3.12, Docker, Kubernetes (k0s), HTTPS private registry (10.61.3.124:30002).

**관련 문서**: `docs/superpowers/specs/2026-04-23-isaaclab-rl-viewport-patch-design.md`

---

## File Structure

**Create:**
- `isaac-launchable/isaaclab-patches/play.py` — Isaac Lab 원본 + 4가지 변경
- `isaac-launchable/isaaclab-patches/train.py` — Isaac Lab 원본 + 2가지 변경
- `isaac-launchable/isaaclab-patches/README.md` — patches 유지 가이드

**Modify:**
- `isaac-launchable/isaac-lab/vscode/Dockerfile.isaacsim6` — `ARG ISAACLAB_REF`, `git checkout`, `COPY isaaclab-patches/*.py`
- `isaac-launchable/k8s/isaac-sim/deployment-0.yaml` — vscode 컨테이너 image 태그 업데이트
- `isaac-launchable/k8s/isaac-sim/deployment-1.yaml` — vscode 컨테이너 image 태그 업데이트

**No tests (integration only):** Kit/Isaac Sim 의존으로 유닛 테스트 불가. 각 Task 마다 pod 에서 직접 확인하는 verify step 포함.

---

## Task 1: Isaac Lab 원본 RL 스크립트 import (baseline)

**Files:**
- Create: `isaac-launchable/isaaclab-patches/play.py`
- Create: `isaac-launchable/isaaclab-patches/train.py`

**목적:** 추후 patch 들을 git diff 로 추적할 수 있게, 먼저 깨끗한 원본을 import 한다.

- [ ] **Step 1: 디렉토리 생성**

```bash
mkdir -p ~/git/isaac-launchable/isaaclab-patches
```

- [ ] **Step 2: pod 에서 Isaac Lab git 으로 깨끗한 play.py 복원 후 복사**

```bash
# pod 내 Isaac Lab 은 git repo. git checkout 으로 원본 복원 후 가져오기.
ssh root@10.61.3.75 "k0s kubectl -n isaac-launchable exec deploy/isaac-launchable-0 -c vscode -- bash -c 'cd /workspace/isaaclab && git checkout HEAD -- scripts/reinforcement_learning/rsl_rl/play.py && cat scripts/reinforcement_learning/rsl_rl/play.py'" > ~/git/isaac-launchable/isaaclab-patches/play.py
```

- [ ] **Step 3: 원본 train.py 복사 (train.py 는 오늘 수정 없음)**

```bash
ssh root@10.61.3.75 "k0s kubectl -n isaac-launchable exec deploy/isaac-launchable-0 -c vscode -- cat /workspace/isaaclab/scripts/reinforcement_learning/rsl_rl/train.py" > ~/git/isaac-launchable/isaaclab-patches/train.py
```

- [ ] **Step 4: 파일 크기 검증**

```bash
wc -l ~/git/isaac-launchable/isaaclab-patches/play.py ~/git/isaac-launchable/isaaclab-patches/train.py
```

Expected: `play.py` ~200 줄, `train.py` ~214 줄.

- [ ] **Step 5: baseline commit**

```bash
cd ~/git/isaac-launchable
git add isaaclab-patches/play.py isaaclab-patches/train.py
git commit -m "chore(isaaclab-patches): Isaac Lab 2.1.1 RL 스크립트 원본 import (baseline)"
```

---

## Task 2: play.py 에 4가지 변경 적용

**File:** `isaac-launchable/isaaclab-patches/play.py`

**목적:** viewport 활성화 (변경 1,2) + 기존 workaround (변경 3,4) 를 한 번에 적용.

- [ ] **Step 1: 변경 1 — visualizer 기본값 "kit"**

`parser.parse_known_args()` 호출 직전에 `parser.set_defaults(visualizer=["kit"])` 삽입. 원본 L32-33 근처에서 찾아 수정:

Before:
```python
AppLauncher.add_app_launcher_args(parser)
args_cli, hydra_args = parser.parse_known_args()
```

After:
```python
AppLauncher.add_app_launcher_args(parser)
# isaac-launchable patch: Force Kit viewport visualizer for WebRTC livestream
parser.set_defaults(visualizer=["kit"])
args_cli, hydra_args = parser.parse_known_args()
```

- [ ] **Step 2: 변경 2 — set_camera_view 삽입**

`env = RslRlVecEnvWrapper(env, clip_actions=agent_cfg.clip_actions)` 라인 **직후** 에 카메라 설정 삽입.

After:
```python
env = RslRlVecEnvWrapper(env, clip_actions=agent_cfg.clip_actions)

# isaac-launchable patch: viewport 카메라 위치 설정 (Kit viewport capture 활성화 필수)
env.unwrapped.sim.set_camera_view(eye=[2.5, 2.5, 2.5], target=[0.0, 0.0, 0.0])
```

- [ ] **Step 3: 변경 3 — obs_groups 주입 (Isaac Lab #5363 workaround)**

Before:
```python
if agent_cfg.class_name == "OnPolicyRunner":
    runner = OnPolicyRunner(env, agent_cfg.to_dict(), log_dir=None, device=agent_cfg.device)
```

After:
```python
if agent_cfg.class_name == "OnPolicyRunner":
    # isaac-launchable patch: rsl-rl 4.0.0+ obs_groups 누락 시 hang (Isaac Lab #5363)
    cfg_dict = agent_cfg.to_dict()
    cfg_dict["obs_groups"] = {"actor": ["policy"], "critic": ["policy"]}
    runner = OnPolicyRunner(env, cfg_dict, log_dir=None, device=agent_cfg.device)
```

- [ ] **Step 4: 변경 4 — ONNX/JIT export 주석**

rsl-rl 4.0.0+ 분기의 두 라인 주석 처리.

Before:
```python
if version.parse(installed_version) >= version.parse("4.0.0"):
    # use the new export functions for rsl-rl >= 4.0.0
    runner.export_policy_to_jit(path=export_model_dir, filename="policy.pt")
    runner.export_policy_to_onnx(path=export_model_dir, filename="policy.onnx")
    policy_nn = None  # Not needed for rsl-rl >= 4.0.0
```

After:
```python
if version.parse(installed_version) >= version.parse("4.0.0"):
    # use the new export functions for rsl-rl >= 4.0.0
    # isaac-launchable patch: onnxscript + Python 3.12 TypeError 회피 — export 생략
    # runner.export_policy_to_jit(path=export_model_dir, filename="policy.pt")
    # runner.export_policy_to_onnx(path=export_model_dir, filename="policy.onnx")
    policy_nn = None  # Not needed for rsl-rl >= 4.0.0
```

- [ ] **Step 5: diff 확인**

```bash
cd ~/git/isaac-launchable && git diff isaaclab-patches/play.py | head -60
```

Expected: 4개 변경 위치에서 삽입/수정된 줄 표시.

- [ ] **Step 6: commit**

```bash
cd ~/git/isaac-launchable
git add isaaclab-patches/play.py
git commit -m "patch(play.py): viewport 활성화 + rsl-rl 4.0.0 호환 workaround

- visualizer=[kit]: Kit viewport 를 streaming source 로 등록 (#5364 workaround)
- set_camera_view: viewport 카메라 초기 위치 (quadrupeds.py 패턴)
- obs_groups 주입: OnPolicyRunner init hang 회피 (#5363 workaround)
- export_policy_to_jit/onnx 주석: onnxscript Python 3.12 TypeError 회피"
```

---

## Task 3: train.py 에 2가지 변경 적용

**File:** `isaac-launchable/isaaclab-patches/train.py`

**목적:** 학습 중에도 Kit viewport 로 진행 관찰 가능하게 한다.

- [ ] **Step 1: 변경 1 — visualizer 기본값 "kit"**

train.py 에서 `AppLauncher.add_app_launcher_args(parser)` 와 `args_cli, hydra_args = parser.parse_known_args()` 사이 찾아 수정 (원본 L70 근처).

Before:
```python
AppLauncher.add_app_launcher_args(parser)
args_cli, hydra_args = parser.parse_known_args()
```

After:
```python
AppLauncher.add_app_launcher_args(parser)
# isaac-launchable patch: Force Kit viewport visualizer for WebRTC livestream
parser.set_defaults(visualizer=["kit"])
args_cli, hydra_args = parser.parse_known_args()
```

- [ ] **Step 2: 변경 2 — set_camera_view 삽입**

train.py 의 env 생성 직후 찾아 삽입. 원본 train.py 의 env 생성 라인 확인:

```bash
grep -n "env = \|RslRlVecEnvWrapper" ~/git/isaac-launchable/isaaclab-patches/train.py | head -5
```

그 결과에서 `env = RslRlVecEnvWrapper(env, ...)` 줄을 찾아 그 직후에 삽입:

After:
```python
env = RslRlVecEnvWrapper(env, clip_actions=agent_cfg.clip_actions)

# isaac-launchable patch: viewport 카메라 위치 설정 (Kit viewport capture 활성화 필수)
env.unwrapped.sim.set_camera_view(eye=[2.5, 2.5, 2.5], target=[0.0, 0.0, 0.0])
```

- [ ] **Step 3: diff 확인**

```bash
cd ~/git/isaac-launchable && git diff isaaclab-patches/train.py | head -30
```

Expected: 2개 변경 위치에서 삽입된 줄 표시.

- [ ] **Step 4: commit**

```bash
cd ~/git/isaac-launchable
git add isaaclab-patches/train.py
git commit -m "patch(train.py): viewport 활성화

- visualizer=[kit]: Kit viewport 를 streaming source 로 등록 (#5364 workaround)
- set_camera_view: viewport 카메라 초기 위치 (quadrupeds.py 패턴)"
```

---

## Task 4: isaaclab-patches/README.md 작성

**File:** `isaac-launchable/isaaclab-patches/README.md`

**목적:** Isaac Lab 업그레이드 시 patches 재검증 지침을 남긴다.

- [ ] **Step 1: README 작성**

Create `~/git/isaac-launchable/isaaclab-patches/README.md`:

```markdown
# Isaac Lab RL 스크립트 Overlay Patches

Isaac Lab 의 `play.py` / `train.py` 가 `--livestream 2` 에서 Kit viewport 를
WebRTC video track 에 push 하도록 수정한 버전. isaac-launchable 이미지 빌드 시
Dockerfile 의 `COPY` 로 Isaac Lab 원본을 overlay 한다.

## 적용 대상 버전

- Isaac Lab: **v2.1.1**
- Isaac Sim: 6.0.0-rc.22

Dockerfile 의 `ARG ISAACLAB_REF=v2.1.1` 에 고정. 업그레이드 시 아래 "재검증" 절차 수행.

## 변경 요약

### play.py (4개)

1. `parser.set_defaults(visualizer=["kit"])` — Kit viewport 활성화 (#5364 workaround)
2. `env.unwrapped.sim.set_camera_view(eye=[2.5,2.5,2.5], target=[0,0,0])` — 카메라 위치
3. `OnPolicyRunner` 생성 시 `obs_groups` 주입 (Isaac Lab #5363 workaround)
4. `runner.export_policy_to_jit/onnx` 주석 — onnxscript Python 3.12 TypeError 회피

### train.py (2개)

1. `parser.set_defaults(visualizer=["kit"])`
2. `env.unwrapped.sim.set_camera_view(...)`

## 업그레이드 재검증 절차

Isaac Lab 새 버전 지원 시:

1. Dockerfile 의 `ARG ISAACLAB_REF` 업데이트
2. pod 에서 새 버전의 원본 `play.py`, `train.py` 를 `isaaclab-patches/` 로 다시 가져오기
3. 본 README 의 "변경 요약" 기준으로 4 + 2 = 6 개 수정 재적용
4. 이미지 재빌드 + 검증 체크리스트 (spec 문서 참조) 실행

## 관련 링크

- 설계: `docs/superpowers/specs/2026-04-23-isaaclab-rl-viewport-patch-design.md`
- 계획: `docs/superpowers/plans/2026-04-23-isaaclab-rl-viewport-patch.md`
- 업스트림: Isaac Lab [#5362](https://github.com/isaac-sim/IsaacLab/issues/5362), [#5363](https://github.com/isaac-sim/IsaacLab/issues/5363), [#5364](https://github.com/isaac-sim/IsaacLab/issues/5364)
```

- [ ] **Step 2: commit**

```bash
cd ~/git/isaac-launchable
git add isaaclab-patches/README.md
git commit -m "docs(isaaclab-patches): 유지보수 가이드 README"
```

---

## Task 5: Dockerfile.isaacsim6 수정

**File:** `isaac-launchable/isaac-lab/vscode/Dockerfile.isaacsim6`

**목적:** 이미지 빌드 시 Isaac Lab 버전 고정 + patches overlay.

- [ ] **Step 1: Dockerfile 내 Isaac Lab clone 단계 찾기**

```bash
grep -n "git clone.*IsaacLab\|isaaclab\|ARG ISAACLAB\|COPY isaaclab-patches" ~/git/isaac-launchable/isaac-lab/vscode/Dockerfile.isaacsim6
```

Expected: Isaac Lab 관련 `RUN git clone` 또는 checkout 한 위치 발견. 없다면 전체 파일 읽어서 해당 지점 확인.

- [ ] **Step 2: Isaac Lab clone 후 줄에 버전 pin + patches COPY 추가**

Isaac Lab 을 `/workspace/isaaclab` 로 clone 하는 라인 **직후**에 삽입:

```dockerfile
# Isaac Lab 버전 pinning (patches 는 이 버전에 맞춰 유지)
ARG ISAACLAB_REF=v2.1.1
RUN git -C /workspace/isaaclab checkout ${ISAACLAB_REF}

# isaac-launchable patch: RL 스크립트를 viewport-streaming 호환 버전으로 교체
COPY isaaclab-patches/play.py   /workspace/isaaclab/scripts/reinforcement_learning/rsl_rl/play.py
COPY isaaclab-patches/train.py  /workspace/isaaclab/scripts/reinforcement_learning/rsl_rl/train.py
```

- [ ] **Step 3: Dockerfile 변경 확인**

```bash
cd ~/git/isaac-launchable && git diff isaac-lab/vscode/Dockerfile.isaacsim6
```

Expected: 위 3줄 (`ARG`, `RUN git checkout`, `COPY x 2`) 추가 확인.

- [ ] **Step 4: commit**

```bash
cd ~/git/isaac-launchable
git add isaac-lab/vscode/Dockerfile.isaacsim6
git commit -m "feat(docker): Isaac Lab 버전 pin + RL viewport patches overlay

Isaac Lab v2.1.1 에 고정하고, isaaclab-patches/play.py · train.py 를
원본 위에 COPY 로 overlay. play.py --livestream 2 에서 viewport 렌더가
WebRTC video track 으로 push 되도록 함."
```

---

## Task 6: 이미지 빌드 + 레지스트리 push

**수동 step (로컬에서 docker 사용):**

- [ ] **Step 1: 레지스트리 로그인 (이미 되어 있으면 스킵)**

```bash
docker login 10.61.3.124:30002
```

- [ ] **Step 2: 이미지 빌드**

```bash
cd ~/git/isaac-launchable
docker build \
  -f isaac-lab/vscode/Dockerfile.isaacsim6 \
  -t 10.61.3.124:30002/library/isaac-launchable-vscode:6.0.0-patched-20260423 \
  .
```

Expected: `Successfully built <id>` + `Successfully tagged ...` (약 10~30분 소요, Isaac Lab install 포함).

- [ ] **Step 3: 빌드 성공 검증 — patches 파일 존재**

```bash
docker run --rm 10.61.3.124:30002/library/isaac-launchable-vscode:6.0.0-patched-20260423 \
  bash -c 'grep -c "isaac-launchable patch" /workspace/isaaclab/scripts/reinforcement_learning/rsl_rl/play.py /workspace/isaaclab/scripts/reinforcement_learning/rsl_rl/train.py'
```

Expected:
```
/workspace/isaaclab/scripts/reinforcement_learning/rsl_rl/play.py:4
/workspace/isaaclab/scripts/reinforcement_learning/rsl_rl/train.py:2
```

(play.py 에 4개 patch 주석, train.py 에 2개)

- [ ] **Step 4: 레지스트리 push**

```bash
docker push 10.61.3.124:30002/library/isaac-launchable-vscode:6.0.0-patched-20260423
```

- [ ] **Step 5: push 검증**

```bash
curl -s http://10.61.3.124:30002/v2/library/isaac-launchable-vscode/tags/list | jq .
```

Expected: `"tags"` 배열에 `"6.0.0-patched-20260423"` 포함.

---

## Task 7: deployment yaml image 태그 업데이트 + rollout

**Files:**
- Modify: `k8s/isaac-sim/deployment-0.yaml`
- Modify: `k8s/isaac-sim/deployment-1.yaml`

- [ ] **Step 1: deployment-0.yaml 의 vscode 컨테이너 image 태그 변경**

Before:
```yaml
      - name: vscode
        image: 10.61.3.124:30002/library/isaac-launchable-vscode:6.0.0
```

After:
```yaml
      - name: vscode
        image: 10.61.3.124:30002/library/isaac-launchable-vscode:6.0.0-patched-20260423
```

동일하게 `deployment-1.yaml` 도 수정.

- [ ] **Step 2: apply + rollout status 대기**

```bash
scp ~/git/isaac-launchable/k8s/isaac-sim/deployment-0.yaml root@10.61.3.75:/tmp/
scp ~/git/isaac-launchable/k8s/isaac-sim/deployment-1.yaml root@10.61.3.75:/tmp/
ssh root@10.61.3.75 "k0s kubectl -n isaac-launchable apply -f /tmp/deployment-0.yaml -f /tmp/deployment-1.yaml && k0s kubectl -n isaac-launchable rollout status deploy/isaac-launchable-0 --timeout=180s"
```

Expected: `deployment "isaac-launchable-0" successfully rolled out`.

- [ ] **Step 3: pod 내 patch 확인**

```bash
ssh root@10.61.3.75 "k0s kubectl -n isaac-launchable exec deploy/isaac-launchable-0 -c vscode -- grep -c 'isaac-launchable patch' /workspace/isaaclab/scripts/reinforcement_learning/rsl_rl/play.py /workspace/isaaclab/scripts/reinforcement_learning/rsl_rl/train.py"
```

Expected:
```
/workspace/isaaclab/scripts/reinforcement_learning/rsl_rl/play.py:4
/workspace/isaaclab/scripts/reinforcement_learning/rsl_rl/train.py:2
```

- [ ] **Step 4: commit + push**

```bash
cd ~/git/isaac-launchable
git add k8s/isaac-sim/deployment-0.yaml k8s/isaac-sim/deployment-1.yaml
git commit -m "feat(k8s): vscode 이미지를 RL viewport 패치된 버전 6.0.0-patched-20260423 으로 업데이트

isaac-launchable-vscode:6.0.0-patched-20260423 는 Isaac Lab play.py/train.py
가 --livestream 2 에서 Kit viewport 를 WebRTC video track 에 push 하도록
overlay 된 이미지. 관련: docs/superpowers/specs/2026-04-23-isaaclab-rl-viewport-patch-design.md"
git push
```

---

## Task 8: play.py 검증

**목적:** 뷰포트에 Ant 4마리가 학습된 걸음으로 움직이는지 확인.

- [ ] **Step 1: 기존 프로세스 정리 + play.py 기동**

pod-0 vscode 터미널에서 실행:

```bash
pkill -9 -f play.py 2>/dev/null; pkill -9 -f isaacsim.exp 2>/dev/null; sleep 3
cd /workspace/isaaclab
nohup ./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/play.py \
  --task Isaac-Ant-v0 --num_envs 4 --livestream 2 \
  > /tmp/play.log 2>&1 &
echo "pid=$!"
```

(이미 deployment yaml 에 ISAACSIM_HOST env 추가되어 있으므로 --kit_args 불필요)

- [ ] **Step 2: 40초 대기 후 로그 확인**

```bash
sleep 40
grep -iE "App is loaded|Scene manager|Loading model checkpoint|Actor Model|Error|Fatal|Traceback" /tmp/play.log | tail -15
```

Expected:
- `Simulation App Startup Complete`
- `Scene manager: <class InteractiveScene>`
- `Loading model checkpoint from: /workspace/isaaclab/logs/rsl_rl/ant/...`
- `Actor Model: MLPModel(...)`
- Traceback/Error 없음

- [ ] **Step 3: 포트 49100 + GPU 사용 확인**

```bash
ss -tlnp 2>/dev/null | grep :49100
nvidia-smi --query-gpu=utilization.gpu,memory.used --format=csv,noheader
```

Expected: `LISTEN 0.0.0.0:49100`, GPU util 20-50%, memory 4-6 GiB.

- [ ] **Step 4: 브라우저 접속 + Hard Reload**

브라우저에서 `http://10.61.3.125/viewer/` 접속 → `Cmd+Shift+R` Hard Reload.

Expected:
- Isaac Sim UI 등장
- 뷰포트 중앙에 **Ant 4마리** 격자로 배치되어 학습된 걸음으로 움직이는 모습
- 넘어지면 reset → 다시 걷기 반복

- [ ] **Step 5: chrome://webrtc-internals 에서 video track 확인**

새 탭에서 `chrome://webrtc-internals` → 활성 PeerConnection 클릭 → Stats 섹션.

Expected: `Stats graphs for inbound-rtp (kind=video, codec=H264 ...)` 엔트리 **존재**.

- [ ] **Step 6: 브라우저 console 에서 video element 검증**

Viewer 페이지 DevTools Console 에서:
```javascript
const v = document.querySelector('video');
const t0 = v.currentTime;
setTimeout(() => console.log('Δt:', v.currentTime - t0, 'size:', v.videoWidth+'x'+v.videoHeight), 3000);
```

Expected: `Δt: 2~3` (초 증가), `size: 1920x1080` (또는 유사).

---

## Task 9: train.py 검증

**목적:** 학습 진행 중 뷰포트에서 Ant 가 점점 더 잘 걷게 되는 변화를 관찰.

- [ ] **Step 1: 기존 프로세스 정리 + train.py 기동**

```bash
pkill -9 -f play.py 2>/dev/null; pkill -9 -f train.py 2>/dev/null
pkill -9 -f isaacsim.exp 2>/dev/null; sleep 3
cd /workspace/isaaclab
nohup ./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/train.py \
  --task Isaac-Ant-v0 --num_envs 4 --livestream 2 \
  > /tmp/train.log 2>&1 &
```

- [ ] **Step 2: 40초 대기 후 학습 진행 확인**

```bash
sleep 40
grep -iE "App is loaded|Number of env|Learning iteration|Mean reward|Error|Fatal" /tmp/train.log | tail -10
```

Expected:
- `Isaac Sim Full Streaming App is loaded`
- `Number of environments: 4`
- `Learning iteration N/M` (N 증가하는 로그들)
- `Mean reward: X.XX` (값 출력)

- [ ] **Step 3: 브라우저 Hard Reload + 뷰포트 관찰 (2~3분)**

Expected: Ant 4마리가 뷰포트에 보이고, 학습 iteration 이 증가함에 따라 움직임 품질이 변화 (초반 랜덤 → 점차 걷기 시도).

- [ ] **Step 4: UI 반응성 확인**

뷰포트 영역 클릭 → 마우스 드래그로 카메라 회전 시도. Expected: 정상 반응 (frozen 없음).

---

## Task 10: 최종 정리 및 업스트림 기여 검토

- [ ] **Step 1: 성공 시 patch 를 Isaac Lab 업스트림에 PR 제안 검토**

`visualizer=["kit"]` + `set_camera_view` 조합은 workaround 가 아닌 정당한 해법. Isaac Lab [#5364](https://github.com/isaac-sim/IsaacLab/issues/5364) 에 댓글로 제안:

```
This patch works on our isaac-launchable deployment. Adding
`parser.set_defaults(visualizer=["kit"])` and
`env.unwrapped.sim.set_camera_view(eye, target)` after env creation
makes play.py/train.py's viewport stream to WebRTC correctly.
Would you accept a PR with this change?
```

- [ ] **Step 2: Linear AST-4867 상태 업데이트**

체크리스트 항목들 완료 표시 + 커밋 ID 추가:
- [x] play.py viewport 활성화 patch (commit: <Task 2 commit sha>)
- [x] train.py viewport 활성화 patch (commit: <Task 3 commit sha>)
- [x] Dockerfile overlay 설정 (commit: <Task 5 commit sha>)
- [x] deployment image 업데이트 (commit: <Task 7 commit sha>)
- [x] play.py 브라우저 검증 (Ant 4마리 움직임 확인)
- [x] train.py 브라우저 검증 (학습 진행 관찰)

- [ ] **Step 3: 실패 시 롤백**

검증에서 문제 발견 시:

```bash
# deployment image 태그 이전 버전으로 되돌림
cd ~/git/isaac-launchable
sed -i 's|vscode:6.0.0-patched-20260423|vscode:6.0.0|' k8s/isaac-sim/deployment-0.yaml k8s/isaac-sim/deployment-1.yaml
scp k8s/isaac-sim/deployment-0.yaml k8s/isaac-sim/deployment-1.yaml root@10.61.3.75:/tmp/
ssh root@10.61.3.75 "k0s kubectl -n isaac-launchable apply -f /tmp/deployment-0.yaml -f /tmp/deployment-1.yaml"
git add k8s/isaac-sim/deployment-0.yaml k8s/isaac-sim/deployment-1.yaml
git commit -m "revert(k8s): vscode 이미지를 6.0.0 으로 rollback"
git push
```

patches/ 디렉토리 자체는 보존 (다음 재시도 자료).
