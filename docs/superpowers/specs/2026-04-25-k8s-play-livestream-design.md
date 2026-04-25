# K8s 환경에서 RL policy livestream 시연 — `replay_anymal_d.py` 설계

## 배경

K8s 환경 (isaac-launchable) 에서 학습된 Anymal-D 정책을 브라우저 (`/viewer/`) 로 시연하려는 시도. 표준 IsaacLab 의 `scripts/reinforcement_learning/rsl_rl/play.py` 는 `--livestream 2` 모드에서 다음 증상으로 막힘:

- WebRTC ICE/DTLS/data channels 모두 연결 성공
- `chrome://webrtc-internals` 의 `inbound-rtp (kind=video)` 부재 — video RTP 0건
- 약 50초 후 client timeout 으로 `SERVER_DISCONNECTED`

이는 IsaacLab GitHub issue [#5364](https://github.com/isaac-sim/IsaacLab/issues/5364) 에 이미 등록되었고, NVIDIA 측 응답은 "review soon" 만 남음. 시도된 워크어라운드 (custom experience, dependency 추가, settings 추가 등) 모두 실패. `isaacsim.exp.full` dependency 추가는 [#5362](https://github.com/isaac-sim/IsaacLab/issues/5362) 의 native-layer deadlock 으로 막힘.

## 결정적 사실

| 사실 | 출처 |
|---|---|
| `isaaclab.python.kit` 는 `isaacsim.exp.full` dependency 없고 `[settings.exts."omni.kit.livestream.app"]` 블록 비어있음 | issue #5364 댓글 (검증) |
| `scripts/demos/quadrupeds.py --livestream 2` 는 1080p H264 영상 정상 송출 | 본 인프라에서 직접 검증 (Image #46) |
| `quadrupeds.py` 는 `sim_utils.SimulationContext` 직접 사용 + `sim.step()` 루프 (Manager-based env 안 씀) | `scripts/demos/quadrupeds.py` 코드 |
| `ManagerBasedRLEnv` 가 NVST capture 가능한 viewport 를 만들지 않음 | `chrome://webrtc-internals` 비교 + 시도한 모든 setting/patch 의 무효 |

## 결정 — B′ 접근

IsaacLab fork 또는 Manager-based env 패치 대신, **단일 standalone Python script 로 RL inference 만 직접 실행**. quadrupeds.py 패턴을 베이스로 하여 trained policy 의 actor MLP 만 별도 forward.

이로써:
- ManagerBasedRLEnv 우회 → NVST viewport 문제 자체가 발생하지 않음
- IsaacLab 본체 수정 0
- Kit experience 수정 0
- Kit args / hostPort / publicIp 등 인프라 측 설정은 기존 검증된 그대로 재사용

## Architecture

```
┌─────────────────────────────────────────────────┐
│ replay_anymal_d.py                              │
│                                                 │
│  [1] AppLauncher                                │
│      argparse → AppLauncher.add_app_launcher_args│
│      → app_launcher.app (Kit 부팅)              │
│  [2] SimulationContext + set_camera_view        │
│      → viewport 활성화 (quadrupeds 패턴)        │
│  [3] design_scene()                             │
│      → ground / DomeLight / Anymal-D × N        │
│  [4] load_policy(checkpoint_path)               │
│      → torch.load → state_dict 의 actor 만 추출 │
│  [5] run_inference(sim, robots, policy)         │
│      ↻ obs → policy(obs) → action → sim.step    │
└─────────────────────────────────────────────────┘
```

## Components

각 컴포넌트의 책임 / 입력 / 출력 / 의존성:

### 1. `parse_args()`
- **입력**: `sys.argv`
- **출력**: `args_cli` (argparse Namespace)
- **CLI 인자**:
  - `--checkpoint <path>`: rsl_rl checkpoint .pt 파일 (필수)
  - `--num_robots <int>` (default 7): 시연용 로봇 수 (RL training 의 num_envs 와 무관, scene 시각화용)
  - `--velocity_x <float>` (default 0.5): 명령된 forward velocity
  - `--velocity_y <float>` (default 0.0)
  - `--velocity_yaw <float>` (default 0.0)
  - AppLauncher.add_app_launcher_args 로 `--livestream`, `--headless`, `--kit_args` 등 자동 추가
- **의존성**: argparse, isaaclab.app.AppLauncher

### 2. `design_scene(num_robots, spacing=2.0)`
- **입력**: `(num_robots: int, spacing: float)`
- **출력**: `(robot: Articulation, env_origins: torch.Tensor[num_robots, 3])`
- **책임**: GroundPlane + DomeLight 설치, env_origins grid 계산, Anymal-D Articulation 생성 (단일 Articulation 인스턴스가 num_robots 개 prim 관리)
- **의존성**: `isaaclab.sim` (GroundPlaneCfg, DomeLightCfg), `isaaclab.assets.Articulation`, `isaaclab_assets.robots.anymal.ANYMAL_D_CFG`
- **참조 패턴**: `scripts/demos/quadrupeds.py` 의 `design_scene` (단, 다양한 모델 대신 Anymal-D 단일)

### 3. `load_policy(checkpoint_path, device)`
- **입력**: `(checkpoint_path: str, device: torch.device)`
- **출력**: `Callable[[torch.Tensor], torch.Tensor]` — obs[N,48] → action[N,12]
- **책임**:
  - `torch.load(checkpoint_path, weights_only=False)` 로 rsl_rl 4.x checkpoint dict 로드
  - state dict 에서 actor MLP weights 추출
  - `nn.Sequential(Linear(48,128), ELU, Linear(128,128), ELU, Linear(128,128), ELU, Linear(128,12))` 구성
  - state_dict load + `.to(device).eval()`
  - inference 함수 반환 (`torch.inference_mode` wrapper 포함)
- **의존성**: `torch`, `torch.nn`
- **검증된 model 구조**: 본 프로젝트의 train.py log (`Actor Model: MLPModel(...)`)

### 4. `collect_obs(robot, last_actions, velocity_cmd)`
- **입력**: `(robot: Articulation, last_actions: tensor[N,12], velocity_cmd: tensor[N,3])`
- **출력**: `tensor[N, 48]`
- **48-dim observation 구조** (Isaac-Velocity-Flat-Anymal-D-v0 와 동일):
  - `[0:3]`   `base_lin_vel` (linear velocity in base frame)
  - `[3:6]`   `base_ang_vel`
  - `[6:9]`   `projected_gravity` (R^T · [0,0,-1])
  - `[9:12]`  `velocity_commands` (vx, vy, yaw)
  - `[12:24]` `joint_pos` (12 joints, default 에서의 offset)
  - `[24:36]` `joint_vel`
  - `[36:48]` `actions` (직전 step 의 action)
- **의존성**: `torch`, `isaaclab.utils.math` (quat_rotate_inverse)

### 5. `apply_action(robot, action, default_joint_pos, action_scale=0.5)`
- **입력**: `(robot, action[N,12], default_joint_pos[N,12], action_scale: float)`
- **출력**: 없음 (side effect only)
- **책임**: `joint_pos_target = default_joint_pos + action * action_scale` 계산 후 `robot.set_joint_position_target(...)` + `robot.write_data_to_sim()`
- **의존성**: `isaaclab.assets.Articulation`

### 6. `run_inference(sim, robot, policy, velocity_cmd, num_robots)`
- **입력**: refs + `velocity_cmd: tensor[N,3]`
- **출력**: 없음
- **루프 구조** (quadrupeds.py 의 `run_simulator` 패턴):
  ```python
  sim_dt = sim.get_physics_dt()
  last_actions = torch.zeros(num_robots, 12, device=device)
  default_joint_pos = robot.data.default_joint_pos.clone()
  
  while sim_app.is_running():
      obs = collect_obs(robot, last_actions, velocity_cmd)
      with torch.inference_mode():
          action = policy(obs)
      apply_action(robot, action, default_joint_pos)
      sim.step()
      robot.update(sim_dt)
      last_actions = action
  ```

## 데이터 흐름 — 단일 step

```
[robot state in sim]
    ↓ robot.data.{root_lin_vel_b, root_ang_vel_b, projected_gravity_b, joint_pos, joint_vel}
collect_obs() → obs [N, 48] on cuda:0
    ↓ torch.inference_mode
policy(obs) → action [N, 12]
    ↓ scale + offset
apply_action() → joint_pos_target
    ↓ write_data_to_sim
sim.step() → physics integrate + render (NVST capture)
    ↓
robot.update(dt) → state buffer refresh
    ↓
last_actions = action  (next step 의 obs[36:48] 용)
```

## 실행 방법

vscode terminal 에서:

```bash
cd /workspace/isaaclab
./isaaclab.sh -p /workspace/data/replay/replay_anymal_d.py \
  --checkpoint /workspace/isaaclab/logs/rsl_rl/anymal_d_flat/<run>/model_X.pt \
  --num_robots 7 \
  --velocity_x 0.5 \
  --livestream 2 \
  --kit_args "--/exts/omni.kit.livestream.app/primaryStream/publicIp=${ISAACSIM_HOST} --/exts/omni.kit.livestream.app/primaryStream/streamPort=${ISAACSIM_STREAM_PORT} --/exts/omni.kit.livestream.app/primaryStream/signalPort=${ISAACSIM_SIGNAL_PORT}"
```

브라우저: `http://10.61.3.125/viewer/` (시크릿 창 권장)

## 검증 기준

| 기준 | 측정 방법 | 통과 조건 |
|---|---|---|
| Kit 부팅 | `/tmp/replay.log` | "Simulation App Startup Complete" 라인 |
| TCP 49100 listen | `ss -tlnp | grep :49100` | python3 process 가 LISTEN |
| ICE/DTLS 협상 | `chrome://webrtc-internals` | candidate-pair `<host>:30998 typ host` succeeded |
| **Video track 송출** | `chrome://webrtc-internals` | **`inbound-rtp (kind=video, codec=H264)` 가 frame_counter > 0** ← 핵심 |
| viewer 영상 | 브라우저 viewport | Anymal-D N 마리가 평면 위에서 보행 |
| FPS | webrtc-internals stats | ≥ 30 fps (60 fps 이상이면 우수) |
| disconnect 안정성 | 5분간 모니터 | 50초 SERVER_DISCONNECTED 발생 안 함 |

## Out of scope

- Manager-based env 의 모든 기능 (curriculum, randomization, reward) — inference 에 불필요
- gym 호환성 — 외부 RL framework 와 통합 안 함
- 키보드 입력으로 velocity command 변경 — Phase 2 로 분리 가능
- Anymal-B/C, Spot, Unitree 등 다른 로봇 — Phase 2
- IsaacLab #5364 / #5362 의 upstream 해결 — NVIDIA 영역으로 분리

## 변경 위치

- **신규**: `isaaclab-patches/replay_anymal_d.py` (~150 lines)
- **선택**: 같은 디렉토리에 `README.md` 추가 (사용법 / kit_args 예시)
- **무변경**: IsaacLab 본체, Kit experience, k8s manifest, Dockerfile

## 위험 / 가정

- **가정 1**: rsl_rl checkpoint 의 actor 가 단순 MLP (4 layers, ELU activations) — 본 프로젝트의 train.py 로그에서 확인됨. PPO Runner 의 default 구조 유지 시 변동 없음.
- **가정 2**: Anymal-D 의 default joint 구성과 action scaling factor 는 IsaacLab 의 `Isaac-Velocity-Flat-Anymal-D-v0` task config 와 동일. ANYMAL_D_CFG 와 ManagerBasedRLEnv 의 action manager 가 같은 default 사용.
- **위험 1**: observation 순서가 task 별로 다를 수 있음. 본 spec 은 `Isaac-Velocity-Flat-Anymal-D-v0` 기준. 다른 task 의 checkpoint 사용 시 obs ordering 재확인 필요.
- **위험 2**: `joint_pos` 와 `joint_vel` 이 `default_joint_pos` 기준 offset 인지 absolute 인지 확인 필요. ManagerBasedRLEnv 는 보통 offset 사용.

## 참고

- `scripts/demos/quadrupeds.py` — 검증된 standalone pattern
- `scripts/reinforcement_learning/rsl_rl/play.py` — 기존 broken 시도, model 구조 단서
- IsaacLab issue [#5364](https://github.com/isaac-sim/IsaacLab/issues/5364), [#5362](https://github.com/isaac-sim/IsaacLab/issues/5362)
- 메모리: `~/.claude/projects/-Users-xiilab-git-HAMi/memory/project_isaac_sim_streamport_truth.md`, `project_isaaclab_multienv_livestream_limit.md`
