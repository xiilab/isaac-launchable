# Isaac Lab + Persistent Kit Viewport 설계

**작성일**: 2026-04-23
**대상 리포**: `isaac-launchable` + 관련 Kit extension
**목적**: 브라우저에서 `play.py` 실행 결과를 안정적으로 볼 수 있는 단일 경로 확보 (Isaac Lab 업스트림 버그 #5364 / #5362 우회)

---

## 1. 배경과 문제

### 1.1 현재 증상

`./isaaclab.sh -p play.py --livestream 2` 로 실행하면:

- Isaac Sim이 정상 startup (로그 `Simulation App Startup Complete`)
- `49100/TCP` signaling 포트는 리스닝 — config 메시지까지 교환됨
- **WebRTC video track이 생성되지 않음** → 브라우저 `WAITING FOR STREAM...` 상태로 고착
- 약 51초 후 서버 측 `SERVER_DISCONNECTED` → 세션 종료

### 1.2 실제로 막힌 업스트림 이슈

| # | 제목 | 영향 |
|---|---|---|
| [5364](https://github.com/isaac-sim/IsaacLab/issues/5364) | `play.py --livestream 2` 에 WebRTC video track 미생성 | 지금 막고 있는 그 버그 |
| [5362](https://github.com/isaac-sim/IsaacLab/issues/5362) | `--experience=isaacsim.exp.full.streaming.kit` 지정 시 [6.3s] deadlock | streaming.kit 직접 사용 경로 차단 |
| [5363](https://github.com/isaac-sim/IsaacLab/issues/5363) | rsl-rl 4.0.0+ `obs_groups` 누락 hang | 별도 workaround 존재 (dict inject) |

### 1.3 이미 기각된 접근 (오늘 2026-04-23 재검증)

| 시도 | 결과 |
|---|---|
| `--livestream 2 + --kit_args "publicIp/signalPort/streamPort"` | signaling 만 OK, video track ✗ |
| 위 + `--enable omni.services.livestream.session` + streaming.kit 의 설정 값 주입 (`streamType/allowDynamicResize/...`) | 같은 증상 |
| `--experience=isaacsim.exp.full.streaming.kit` | [6.3s] deadlock (업스트림 #5362) |

**결론**: `AppLauncher → isaaclab.python.kit` 경로는 Isaac Lab fix 없이 고칠 수 없음.

### 1.4 움직일 수 있는 것 / 없는 것

| 사실 | 설계에 주는 제약 |
|---|---|
| SimulationApp은 **프로세스 싱글톤**, 다른 프로세스에 원격 attach 불가 | Kit과 play 로직은 **같은 프로세스** 내에 있어야 함 |
| Kit은 `*.kit` app 파일의 `[dependencies]`를 읽어 기동 시 extension load | 커스텀 `.kit` 파일로 "streaming 베이스 + Isaac Lab extension" 조합이 가능 |
| `runheadless.sh`는 이미 `primaryStream.publicIp` 등 configmap 경유로 주입 | 새 설계도 같은 메커니즘 재사용 |
| web-viewer(30998/UDP + 49100/TCP hostPort)는 고정 동작 | viewer 쪽은 재사용 |

---

## 2. 설계 목표

- **S1**: 사용자는 브라우저 한 탭(`http://10.61.3.125/viewer/`)을 유지한 채 `play.py` 를 돌리면 해당 탭에 Ant/로봇이 실시간으로 보인다.
- **S2**: Isaac Lab 업스트림 fix 없이 동작한다.
- **S3**: 한 번에 하나의 play 세션만 지원 (사용자 범위 A 확정).
- **S4**: play 실행 트리거는 VSCode 터미널의 한 줄 명령 (또는 CLI wrapper).
- **비목표**: train.py / 인터랙티브 씬 편집 / 다중 세션 동시 지원.

---

## 3. 아키텍처

### 3.1 최상위 구조

```
┌──────────────────────── pod: isaac-launchable-0 ────────────────────────┐
│                                                                         │
│  [container: vscode]                                                    │
│    entrypoint → runheadless.sh                                          │
│         │                                                               │
│         └─→ /isaac-sim/kit/kit  /workspace/apps/isaaclab.streaming.kit  │
│                 │                                                       │
│                 ├ isaacsim.exp.full (RTX/USD/...)                       │
│                 ├ omni.kit.livestream.app (+ session)                   │
│                 ├ isaaclab, isaaclab_tasks, isaaclab_rl, isaaclab_assets│
│                 └ isaac_launchable.play_runner  ← 커스텀 extension      │
│                       │                                                 │
│                       └ HTTP POST /run_play, /stop_play                 │
│                           └ ManagerBasedRLEnv + RSL-RL runner           │
│                                                                         │
│  [container: nginx]  ───────► signaling 49100 / HTTP 8011 proxy         │
│  [container: web-viewer] ───► http://10.61.3.125/viewer/                │
└─────────────────────────────────────────────────────────────────────────┘
```

### 3.2 Kit app 파일 (`isaaclab.streaming.kit`)

`isaacsim.exp.full.streaming.kit` 을 템플릿으로, Isaac Lab extension 들을 dependency 에 **추가**한 새 파일.

```toml
[package]
title = "Isaac Lab Full Streaming"
version = "1.0.0"
keywords = ["experience", "app", "usd", "isaaclab"]

[dependencies]
"isaacsim.exp.full" = {}
"omni.kit.livestream.app" = {}
"omni.services.livestream.session" = {}
"isaaclab" = {}
"isaaclab_tasks" = {}
"isaaclab_rl" = {}
"isaaclab_assets" = {}
"isaac_launchable.play_runner" = {}  # <- 신규

[settings.app.exts.folders]
'++' = [
    "${app}",
    "${app}/../exts",
    "${app}/../extscache",
    "${app}/../extsUser",
    "/workspace/isaaclab/source",        # Isaac Lab 소스 루트
    "/isaac-sim/user-exts",              # 우리 extension 루트
]

# streaming 관련 설정은 runheadless.sh 가 --kit_args 로 주입 (현행 유지)
[settings.exts."omni.kit.livestream.app"]
primaryStream.streamType = "webrtc"
primaryStream.allowDynamicResize = true
primaryStream.enableEventTracing = true
primaryStream.enableOpenTelemetry = false

[settings.exts."omni.services.livestream.session"]
quitOnSessionEnded = false         # play 종료해도 Kit 유지 (영속성 확보)
resumeTimeoutSeconds = 30
waitForSessionReadyEvent = false

[settings.app.livestream]
allowResize = true
outDirectory = "${data}"
```

**핵심 결정**:
- `quitOnSessionEnded = false` — 브라우저 탭 닫혀도 Kit 살아있음
- `extsFolders` 에 `/workspace/isaaclab/source` 추가 → Isaac Lab extension 들이 Kit dependency 시스템 통해 자동 load (`AppLauncher._load_extensions` 역할을 .kit 파일이 대신)
- 모든 `primaryStream.publicIp/signalPort/streamPort` 는 `.kit` 파일에 박지 **않음** → runheadless.sh 의 환경변수 경로 재사용 (운영 일관성)

### 3.3 커스텀 Extension: `isaac_launchable.play_runner`

역할: Kit 프로세스에 임베드되어 HTTP endpoint를 노출하고, 요청이 오면 ManagerBasedRLEnv 생성 + inference loop 를 별도 async task 로 실행.

**디렉토리**:
```
extensions/
└─ isaac_launchable.play_runner/
   ├─ config/extension.toml
   └─ isaac_launchable/play_runner/
      ├─ __init__.py
      ├─ extension.py          # Kit entrypoint (on_startup/on_shutdown)
      ├─ http_service.py       # omni.services.core 기반 endpoint
      ├─ runner.py             # ManagerBasedRLEnv 수명주기
      └─ config.py             # 요청 스키마
```

**HTTP API** (omni.services.transport.server.http 사용):

| Method | Path | 요청 | 응답 |
|---|---|---|---|
| POST | `/play/run` | `{task, checkpoint, num_envs?, seed?, device?}` | `{status, session_id}` 또는 409 (이미 실행 중) |
| POST | `/play/stop` | `{}` | `{status}` |
| GET  | `/play/status` | — | `{running, task, elapsed_steps}` |

**실행 흐름** (extension.on_startup 이후):
1. 첫 요청 수신 전: Kit viewport 는 빈 `/World` stage (isaacsim.exp.full 의 기본)
2. `/play/run` 수신
   - 기존 세션 있으면 409 반환 (S3 제약)
   - `env = gym.make(task, cfg=env_cfg)` → `env.unwrapped.sim.set_camera_view(...)`
   - rsl_rl `OnPolicyRunner.load(checkpoint)` + `runner.get_inference_policy()`
   - `obs_groups` workaround 주입 (#5363)
   - asyncio task 로 inference loop 시작
3. inference loop 는 `env.unwrapped.sim.app.update()` 호출 없이 Kit 메인 루프의 update 주기에 맞춰 step → viewport 가 자연스럽게 갱신됨
4. `/play/stop` 수신 시 loop 취소 + `env.close()` + stage 초기화

**중요 구현 메모**:
- 기존 standalone script 의 `AppLauncher(...)` 블록은 **사용하지 않음**. Kit 은 이미 떠있음.
- `env.unwrapped.sim` 은 이미 존재하는 `SimulationContext` 를 얻어야 함 (새로 만들면 충돌). Isaac Lab 은 `SimulationContext` 싱글톤 패턴이라 기존 인스턴스 재사용 가능 — 검증 포인트 P1.
- `launch_simulation()` context manager 는 사용 **X** (AppLauncher 재생성 시도)
- checkpoint 경로는 pod PVC(`/workspace/data`) 기준 상대 경로로만 허용 (경로 traversal 차단)

### 3.4 트리거 CLI (`ilplay`)

사용자 UX 를 "명령 한 줄" 로 맞추기 위한 얇은 wrapper (bash).

```bash
# /workspace/isaaclab/bin/ilplay
#!/bin/bash
task="$1"; ckpt="$2"; shift 2
curl -sS -X POST http://127.0.0.1:8011/play/run \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg t "$task" --arg c "$ckpt" '{task:$t, checkpoint:$c}')" \
  | jq .
```

사용:
```bash
ilplay Isaac-Ant-v0 logs/rsl_rl/ant/2026-04-23_12-01-17/model_999.pt
```

### 3.5 배포 변경

| 항목 | 현재 | 변경 |
|---|---|---|
| `deployment-0.yaml` 컨테이너 이미지 | `isaac-launchable-vscode:6.0.0` | 동일 (Dockerfile 수정만) |
| `runheadless.sh` 끝의 `isaac-sim.streaming.sh` 호출 | `isaacsim.exp.full.streaming.kit` 사용 | `/workspace/apps/isaaclab.streaming.kit` 사용 |
| Kit 포트 공개 | 30998/UDP + 49100/TCP | 동일 + **8011/TCP** (play runner HTTP, 클러스터 내부만) |
| Dockerfile | - | `COPY extensions/isaac_launchable.play_runner /isaac-sim/user-exts/` + `COPY apps/isaaclab.streaming.kit /workspace/apps/` |

**주의**: 8011/TCP 는 `omni.services.transport` 기본 포트라 Kit 가 이미 내부용으로 쓰고 있음 (오늘 실험 중 관측). 외부 노출 금지 — 서비스 프레임워크에 인증 없음. 파드 내부(`127.0.0.1`) 또는 클러스터 내부(ClusterIP) 만.

---

## 4. 데이터 흐름

```
사용자 CLI                  play_runner extension            Kit main loop
  │                               │                                │
  ├── POST /play/run ────────────►│                                │
  │      {task, ckpt}             │                                │
  │                               │─ gym.make(task) ──────────────►│
  │                               │                                │── load USD / spawn robots
  │                               │◄── env (handle) ───────────────│
  │                               │                                │
  │                               │─ OnPolicyRunner.load(ckpt)     │
  │                               │─ asyncio.create_task(loop)     │
  │◄──── {status:"running"} ──────│                                │
  │                               │                                │
  │                               │◄── every frame ────────────────│
  │                               │    policy(obs) → env.step()    │
  │                               │                                │── viewport render
  │                               │                                │    ↓
  │                               │                                │  WebRTC track
  │                               │                                │    ↓
  │                               │                                │  브라우저 viewport
  │                               │                                │
  ├── POST /play/stop ───────────►│                                │
  │                               │─ loop.cancel(); env.close()    │
  │◄──── {status:"stopped"} ──────│                                │
```

---

## 5. 에러 처리

| 상황 | 동작 |
|---|---|
| 이미 실행 중인 세션 있음 | HTTP 409, 에러 메시지 `"session already running, call /play/stop first"` |
| 존재하지 않는 task | HTTP 400, gym registry 오류 그대로 반환 |
| checkpoint 경로 없음 / traversal | HTTP 400, 400 전에 path whitelist 검증 |
| inference loop 중 예외 | 로그 + `last_error` 필드에 저장, `/play/status` 로 조회 가능, Kit 은 생존 |
| Kit crash (전체) | runheadless.sh 가 k8s livenessProbe 없이 재기동 의존 (현행과 동일) |

---

## 6. 테스트

| 레벨 | 대상 | 방법 |
|---|---|---|
| 유닛 | `runner.py` 의 경로 검증, 상태 머신 | pytest in isaac-sim python env |
| 통합 | Kit 실행 + HTTP 요청 | 파드에서 `curl localhost:8011/play/run` → 5초 내 200 응답, `/play/status` running 확인 |
| E2E | 브라우저 viewport 렌더링 | 수동: Chrome DevTools console 에서 `WAITING FOR STREAM...` 사라지고 video track 붙는 것 확인, 30초 이상 유지 |
| 회귀 | 업스트림 fix 후 native `--livestream 2` 경로와 공존 | 두 경로 모두 수동 체크 |

**주 검증 포인트 (P1)**: "Kit 이 떠있는 상태에서 `gym.make('Isaac-Ant-v0', ...)` 가 새 SimulationContext 생성 없이 기존 것을 재사용하는가" — 설계의 기술적 전제. 구현 단계에서 제일 먼저 검증할 것.

---

## 7. 마이그레이션 / 롤백

### 적용
1. `extensions/isaac_launchable.play_runner` + `apps/isaaclab.streaming.kit` 신규 추가
2. `Dockerfile.isaacsim6` 에 COPY 라인 2줄 추가
3. `runheadless-script-0` ConfigMap 의 마지막 줄 kit 파일 경로 교체
4. 이미지 빌드 → push → pod rollout

### 롤백
- ConfigMap 의 kit 파일 경로만 `isaacsim.exp.full.streaming.kit` 로 되돌리면 즉시 복원 (extension 은 load 안 되고, play runner 안 떠도 문제 없음)
- Dockerfile COPY 는 그대로 둬도 무해

---

## 8. 열린 질문

- **Q1**: Kit dependency 시스템이 `/workspace/isaaclab/source` 아래 extension 들을 `extension.toml` 없이도 로드하는가? 현재 `isaaclab_tasks-1.5.11` 로 Kit 에서 startup 되고 있으므로 표준 Kit extension 으로 패키징되어 있을 가능성 큼 — 구현 첫 단계에서 `.kit` 파일 minimal 버전으로 실제 load 확인 필요.
- **Q2**: rsl-rl import 가 Kit 부팅 시점에 필요한가, lazy 로 미뤄도 되는가? lazy 가 startup 시간 절약.
- **Q3**: 미래에 `--livestream 2` 업스트림 fix 들어오면 이 설계 폐기? 또는 "영속 viewport" 가치가 남아서 유지?

---

## 9. 참고

- 메모리: `project_isaac_sim_webrtc_publicip.md`, `project_isaac_lab_livestream_status.md`
- `isaacsim.exp.full.streaming.kit`: `/isaac-sim/apps/` (파드 내부)
- AppLauncher 소스: `/workspace/isaaclab/source/isaaclab/isaaclab/app/app_launcher.py`
- `launch_simulation` 소스: `/workspace/isaaclab/source/isaaclab_tasks/isaaclab_tasks/utils/sim_launcher.py`
- Omniverse Kit services: https://docs.omniverse.nvidia.com/kit/docs/omni.services.core
