# Isaac Lab RL 스크립트 viewport streaming 활성화 패치 — 설계

작성: 2026-04-23  
관련 이슈: [AST-4867](https://linear.app/xiilab/issue/AST-4867), Isaac Lab [#5364](https://github.com/isaac-sim/IsaacLab/issues/5364)

## 목적

`isaac-launchable` 배포는 **Isaac Lab 의 RL 학습·재생 과정을 브라우저로 실시간 시각화**하는 것이 본 목적이다. 현재 `play.py` / `train.py` 같은 `ManagerBasedRLEnv` 기반 스크립트는 `--livestream 2` 로 실행해도 Kit viewport 가 WebRTC video track 에 프레임을 push 하지 않는다. 반면 `scripts/demos/quadrupeds.py` 는 동일 환경에서 정상 동작한다. 차이는 스크립트 레벨의 두 가지 누락에서 발생한다.

이 설계는 Isaac Lab upstream fix 를 기다리지 않고, isaac-launchable 이미지 빌드 시 RL 스크립트를 viewport-streaming 호환 버전으로 overlay 하여 근본 문제를 해소한다.

## 목표 (Goal)

- `play.py` — 학습된 체크포인트를 로드해 브라우저 뷰포트에서 Ant 가 걷는 모습 관찰 가능
- `train.py` — 학습 진행 상황(에피소드 리셋, reward 증가 추세)을 브라우저 뷰포트에서 관찰 가능 (`--num_envs` 작게 쓸 때)
- patch 는 이미지에 고정되어 pod 재기동이나 런타임 개입 없이 항상 동작

## 범위

| 포함 | 제외 |
|---|---|
| `play.py`, `train.py` 두 스크립트 overlay | 다른 RL 라이브러리 스크립트 (`skrl`, `sb3` 등) — 필요 시 추후 확장 |
| Isaac Lab 2.1.1 고정 지원 | Isaac Lab master 자동 추적 |
| isaac-launchable Dockerfile 수정 | Isaac Lab fork 또는 upstream PR (선택적 후속) |
| 이미 검증된 workaround 포함 (`obs_groups` 주입, ONNX 주석) | 오늘 시도했다 효과 없던 변경들 (`use_fabric=False`, `render_mode` 강제) |

## 핵심 원인 분석

`quadrupeds.py` 가 viewport streaming 에 연결되고 RL 스크립트는 안 되는 이유는 두 가지:

1. **`parser.set_defaults(visualizer=["kit"])` 누락**  
   `AppLauncher.add_app_launcher_args(parser)` 가 `--visualizer` CLI 옵션을 등록한다. `quadrupeds.py` 는 파서 기본값을 `["kit"]` 으로 override 하여 Kit viewport 모드를 강제한다. RL 스크립트는 기본값 그대로라 viewport streaming source 등록이 안 된다.

2. **`sim.set_camera_view(eye, target)` 누락**  
   `quadrupeds.py` 는 `SimulationContext` 생성 직후 기본 카메라 위치를 명시한다. `ManagerBasedRLEnv` 의 `self.sim` 은 이 호출 없이 기본 상태라 frustum 이 scene 밖을 향하거나 설정 미완료 상태가 된다.

두 누락 각각만으로는 증상이 부분적일 수 있지만 합쳐지면 video track 자체가 frame 을 못 받는 오늘의 증상과 일치한다.

## Architecture

isaac-launchable 레포 구조에 `isaaclab-patches/` 디렉토리를 추가하고 Dockerfile 에서 overlay:

```
isaac-launchable/
├── isaaclab-patches/
│   ├── play.py            # Isaac Lab 원본 + 4개 변경
│   └── train.py           # Isaac Lab 원본 + 2개 변경
├── isaac-lab/vscode/
│   └── Dockerfile.isaacsim6   # COPY isaaclab-patches/*.py 추가
└── docs/superpowers/specs/
    └── 2026-04-23-isaaclab-rl-viewport-patch-design.md
```

**관리 방식**: 완전 파일 overlay (sed 명령 나열 아님)

- 장점: git diff 로 변경 추적 가능, quote/escape 이슈 없음, Isaac Lab 구조 변경 시 수동 재검증이 명확
- 단점: Isaac Lab 버전 업그레이드 시 patches/*.py 를 새 버전에 맞춰 수동 rebase 필요
- 대안 (sed 명령 나열) 기각 사유: 2026-04-23 세션에서 sed escape 꼬임 반복 경험, 패턴이 Isaac Lab 업데이트 시 drift 위험

## Patch 내용

### `play.py` 변경 (4가지)

**변경 1 — visualizer 기본값 "kit"** (argparse 설정 직후)
```python
parser.set_defaults(visualizer=["kit"])
```

**변경 2 — 뷰포트 카메라 위치** (`env.reset()` 전)
```python
env.unwrapped.sim.set_camera_view(eye=[2.5, 2.5, 2.5], target=[0.0, 0.0, 0.0])
```

**변경 3 — `obs_groups` 주입** (Isaac Lab #5363 workaround, OnPolicyRunner 생성)
```python
cfg_dict = agent_cfg.to_dict()
cfg_dict["obs_groups"] = {"actor": ["policy"], "critic": ["policy"]}
runner = OnPolicyRunner(env, cfg_dict, log_dir=None, device=agent_cfg.device)
```

**변경 4 — ONNX/JIT export 주석** (onnxscript Python 3.12 TypeError 회피)
```python
# runner.export_policy_to_jit(...)
# runner.export_policy_to_onnx(...)
policy_nn = None
```

### `train.py` 변경 (2가지)

**변경 1 — visualizer 기본값 "kit"** (동일)
**변경 2 — 뷰포트 카메라 위치** (env/InteractiveScene 생성 후 동일 호출)

`train.py` 는 `OnPolicyRunner` 를 다른 경로로 생성할 수 있어 `obs_groups` 누락 영향 여부는 구현 시 검증 필요 (2026-04-23 세션에서 train.py 64 envs 는 정상 동작, 4 envs 에서도 돌았음).

## Dockerfile 수정

`isaac-launchable/isaac-lab/vscode/Dockerfile.isaacsim6` 의 Isaac Lab clone 단계 직후에 추가:

```dockerfile
# Isaac Lab 버전 pinning (patches 는 이 버전에 맞춰 유지)
ARG ISAACLAB_REF=v2.1.1
RUN git -C /workspace/isaaclab checkout ${ISAACLAB_REF}

# RL 스크립트를 viewport-streaming 호환 버전으로 교체
COPY isaaclab-patches/play.py   /workspace/isaaclab/scripts/reinforcement_learning/rsl_rl/play.py
COPY isaaclab-patches/train.py  /workspace/isaaclab/scripts/reinforcement_learning/rsl_rl/train.py
```

## 빌드 흐름

1. isaac-launchable 레포 루트에서 `docker build -f isaac-lab/vscode/Dockerfile.isaacsim6 -t 10.61.3.124:30002/library/isaac-launchable-vscode:6.0.0-patched-20260423 .`
2. 레지스트리 push
3. `deployment-0.yaml`, `deployment-1.yaml` 의 vscode 컨테이너 image 태그 업데이트 → commit
4. `k0s kubectl rollout restart deploy/isaac-launchable-0` (그리고 -1)

기존 이미지 태그 `6.0.0` 은 유지 (롤백용).

## 검증 체크리스트

### play.py 검증
- [ ] Pod 재기동 후 `./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/play.py --task Isaac-Ant-v0 --num_envs 4 --livestream 2` (kit_args 는 ISAACSIM_HOST env 로 이미 주입됨)
- [ ] `/tmp/play.log` 진행 로그: `Loading model checkpoint` → `Actor Model: MLPModel(...)` → rollout 진입
- [ ] 브라우저 Hard Reload → 뷰포트에 Ant 4마리가 학습된 걸음으로 움직이는 모습 관찰
- [ ] `chrome://webrtc-internals` 에 `inbound-rtp (kind=video, codec=H264)` 엔트리 존재
- [ ] `document.querySelector('video')` 가 `videoWidth > 0`, `currentTime` 증가

### train.py 검증
- [ ] `./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/train.py --task Isaac-Ant-v0 --num_envs 4 --livestream 2`
- [ ] `Learning iteration N/M` 로그가 진행되는 동안 뷰포트에서 Ant 가 점점 잘 걷게 되는 변화 관찰 가능
- [ ] 브라우저 UI 반응성 유지 (메뉴 클릭 가능)

## 실패 시 롤백

- `deployment-0/1.yaml` 의 image 태그를 이전(`6.0.0`) 으로 되돌리고 rollout
- `isaaclab-patches/` 디렉토리 자체는 git 에 남겨 둠 (다음 시도 자료)

## 성공 시 후속

1. **Isaac Lab #5364 에 PR 제출 검토** — `visualizer=["kit"]` 추가는 workaround 가 아닌 정당한 해결책. upstream 기여 가치 있음.
2. `docs/` 에 "Isaac Lab 업그레이드 시 RL patches 재검증 가이드" 작성
3. skrl/sb3 등 다른 RL 라이브러리 스크립트가 필요해지면 동일 패턴으로 overlay 추가

## 참고 링크

- Linear: [AST-4867 Isaac Lab + Isaac Sim WebRTC livestream 검증 완료 및 업스트림 이슈 등록](https://linear.app/xiilab/issue/AST-4867)
- Isaac Lab [#5362 AppLauncher deadlock with --experience](https://github.com/isaac-sim/IsaacLab/issues/5362)
- Isaac Lab [#5363 play.py OnPolicyRunner hang (rsl-rl 4.0.0+)](https://github.com/isaac-sim/IsaacLab/issues/5363)
- Isaac Lab [#5364 play.py --livestream 2 no video track](https://github.com/isaac-sim/IsaacLab/issues/5364)
- isaac-launchable commit `38bb95b` (ISAACSIM_HOST env 추가, 2026-04-23)
