# `replay_anymal_d.py` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Standalone Python script (`isaaclab-patches/replay_anymal_d.py`) that loads a trained Anymal-D rsl_rl checkpoint and replays it via Isaac Sim's livestream WebRTC, bypassing IsaacLab #5364 by using `quadrupeds.py` style direct `SimulationContext` instead of `ManagerBasedRLEnv`.

**Architecture:** Single ~150-line script. AppLauncher boots Kit with `--livestream 2` + publicIp/streamPort kit_args. `SimulationContext` is created directly. Anymal-D `Articulation` with N robot prims. Trained actor MLP is extracted from `model_X.pt` and run in `torch.inference_mode()` each step. `sim.step()` drives physics + render (NVST captures the viewport, same as quadrupeds.py).

**Tech Stack:** Python 3.12, IsaacLab 3.0.0-beta1 (`isaaclab.app.AppLauncher`, `isaaclab.sim`, `isaaclab.assets.Articulation`, `isaaclab_assets.robots.anymal.ANYMAL_D_CFG`), PyTorch (CUDA), Isaac Sim 6.0.0-rc.22, K8s pod `isaac-launchable-0` on `ws-node074`.

---

## File Structure

| File | Responsibility |
|---|---|
| `isaaclab-patches/replay_anymal_d.py` | The single replay script (everything in one file, mirroring `scripts/demos/quadrupeds.py` style) |
| `isaaclab-patches/README.md` | Append usage section for the replay script |

No tests — there is no pytest/CI infrastructure in `isaac-launchable`. Verification is end-to-end via `chrome://webrtc-internals` `inbound-rtp (kind=video)` presence and visual confirmation in `/viewer/`. Each task ends with a smoke-test on the live K8s pod.

---

## Working environment notes

- **Author** (this Mac): edits `isaac-launchable/isaaclab-patches/replay_anymal_d.py`
- **Runtime** (K8s pod `isaac-launchable-0` vscode container on `ws-node074`): the script is pushed via `kubectl exec -i ... -- bash -c 'cat > /workspace/data/replay/replay_anymal_d.py'` (the `/workspace/data` PVC is mounted in the pod). The pod name changes between rollouts — always re-fetch with `ssh root@10.61.3.75 'k0s kubectl -n isaac-launchable get pods -l instance=pod-0 -o jsonpath="{.items[0].metadata.name}"'`.
- **Existing live Kit args** (already verified in this repo): `--/exts/omni.kit.livestream.app/primaryStream/publicIp=$ISAACSIM_HOST --/exts/omni.kit.livestream.app/primaryStream/streamPort=$ISAACSIM_STREAM_PORT --/exts/omni.kit.livestream.app/primaryStream/signalPort=$ISAACSIM_SIGNAL_PORT`. `ISAACSIM_HOST=10.61.3.74`, `STREAM_PORT=30998`, `SIGNAL_PORT=49100` are env-injected from the pod spec.
- **Reference patterns**: `/workspace/isaaclab/scripts/demos/quadrupeds.py` is the verified working livestream pattern. Always mirror its structure.

---

## Task 1 — Skeleton: argparse + AppLauncher + main stub

**Files:**
- Create: `isaac-launchable/isaaclab-patches/replay_anymal_d.py`

- [ ] **Step 1: Create the file with skeleton**

```python
# isaac-launchable/isaaclab-patches/replay_anymal_d.py
"""Replay a trained Anymal-D rsl_rl policy via Isaac Sim livestream.

Usage:
    cd /workspace/isaaclab
    ./isaaclab.sh -p /workspace/data/replay/replay_anymal_d.py \
        --checkpoint /workspace/isaaclab/logs/rsl_rl/anymal_d_flat/<run>/model_X.pt \
        --num_robots 7 \
        --velocity_x 0.5 \
        --livestream 2 \
        --kit_args "--/exts/omni.kit.livestream.app/primaryStream/publicIp=$ISAACSIM_HOST --/exts/omni.kit.livestream.app/primaryStream/streamPort=$ISAACSIM_STREAM_PORT --/exts/omni.kit.livestream.app/primaryStream/signalPort=$ISAACSIM_SIGNAL_PORT"

Bypasses IsaacLab #5364 by using SimulationContext directly (quadrupeds.py
pattern) instead of ManagerBasedRLEnv. NVST captures the viewport correctly
in this mode and produces an inbound-rtp (kind=video) stream.
"""
import argparse

from isaaclab.app import AppLauncher

# CLI args
parser = argparse.ArgumentParser(description="Replay a trained Anymal-D rsl_rl policy.")
parser.add_argument("--checkpoint", type=str, required=True,
                    help="Path to rsl_rl checkpoint (.pt)")
parser.add_argument("--num_robots", type=int, default=7,
                    help="Number of Anymal-D robots in the scene")
parser.add_argument("--velocity_x", type=float, default=0.5,
                    help="Forward velocity command (m/s)")
parser.add_argument("--velocity_y", type=float, default=0.0,
                    help="Lateral velocity command (m/s)")
parser.add_argument("--velocity_yaw", type=float, default=0.0,
                    help="Yaw rate command (rad/s)")
AppLauncher.add_app_launcher_args(parser)
parser.set_defaults(visualizer=["kit"])
args_cli = parser.parse_args()

# Boot Kit
app_launcher = AppLauncher(args_cli)
simulation_app = app_launcher.app


def main():
    print("[INFO] replay_anymal_d.py: skeleton main() reached")
    print(f"[INFO]   checkpoint: {args_cli.checkpoint}")
    print(f"[INFO]   num_robots: {args_cli.num_robots}")
    print(f"[INFO]   velocity:   ({args_cli.velocity_x}, {args_cli.velocity_y}, {args_cli.velocity_yaw})")


if __name__ == "__main__":
    main()
    simulation_app.close()
```

- [ ] **Step 2: Push to pod and run smoke test**

```bash
# From Mac
POD=$(ssh root@10.61.3.75 "k0s kubectl -n isaac-launchable get pods -l instance=pod-0 -o jsonpath='{.items[0].metadata.name}'")
ssh root@10.61.3.75 "k0s kubectl -n isaac-launchable exec $POD -c vscode -- mkdir -p /workspace/data/replay"
cat /Users/xiilab/git/isaac-launchable/isaaclab-patches/replay_anymal_d.py | \
  ssh root@10.61.3.75 "k0s kubectl -n isaac-launchable exec -i $POD -c vscode -- bash -c 'cat > /workspace/data/replay/replay_anymal_d.py'"
```

Then in the vscode terminal of the pod (or via `kubectl exec -t`):

```bash
cd /workspace/isaaclab
./isaaclab.sh -p /workspace/data/replay/replay_anymal_d.py --checkpoint dummy.pt --num_robots 7 --livestream 2 \
  --kit_args "--/exts/omni.kit.livestream.app/primaryStream/publicIp=$ISAACSIM_HOST --/exts/omni.kit.livestream.app/primaryStream/streamPort=$ISAACSIM_STREAM_PORT --/exts/omni.kit.livestream.app/primaryStream/signalPort=$ISAACSIM_SIGNAL_PORT"
```

**Expected:**
- `Simulation App Startup Complete` line appears
- `[INFO] replay_anymal_d.py: skeleton main() reached` line appears
- TCP `:49100` LISTEN by the python3 process (`ss -tlnp | grep :49100`)
- Process exits cleanly (Kit closes)

If Kit fails to boot, kill any leftover Kit (`pkill -9 -f isaaclab; pkill -9 -f /app/kit/kit; sleep 5`) and retry.

- [ ] **Step 3: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add isaaclab-patches/replay_anymal_d.py
git commit -m "feat(replay): skeleton — argparse + AppLauncher boot for Anymal-D replay"
```

---

## Task 2 — `design_scene()`: ground + DomeLight + origins grid

**Files:**
- Modify: `isaac-launchable/isaaclab-patches/replay_anymal_d.py`

- [ ] **Step 1: Add imports + `design_scene` function**

After the `from isaaclab.app import AppLauncher` line, add (these imports must be **after** `AppLauncher(args_cli)`, see step 2 for placement):

(no edit yet — see step 2 which performs the full insertion)

- [ ] **Step 2: Insert imports and helpers after `simulation_app = app_launcher.app`**

Replace the line `def main():` and everything after the `simulation_app = app_launcher.app` line (so up to but not including the `def main()` definition) with:

```python
"""Rest everything follows."""

import math
from typing import Tuple

import numpy as np
import torch

import isaaclab.sim as sim_utils
from isaaclab.assets import Articulation
from isaaclab_assets.robots.anymal import ANYMAL_D_CFG  # isort:skip


def define_origins(num_origins: int, spacing: float) -> torch.Tensor:
    """Grid of env origins (Z=0). Same pattern as quadrupeds.py."""
    env_origins = torch.zeros(num_origins, 3)
    num_cols = int(math.floor(math.sqrt(num_origins)))
    num_rows = int(math.ceil(num_origins / num_cols))
    xx, yy = torch.meshgrid(
        torch.arange(num_rows), torch.arange(num_cols), indexing="xy"
    )
    env_origins[:, 0] = (
        spacing * xx.flatten()[:num_origins] - spacing * (num_rows - 1) / 2.0
    )
    env_origins[:, 1] = (
        spacing * yy.flatten()[:num_origins] - spacing * (num_cols - 1) / 2.0
    )
    return env_origins


def design_scene(num_robots: int, spacing: float = 2.0) -> torch.Tensor:
    """Place a ground plane, a DomeLight, and return the env origins.

    Returns:
        env_origins: tensor[N, 3] of robot spawn positions.
    """
    # Ground
    ground = sim_utils.GroundPlaneCfg()
    ground.func("/World/defaultGroundPlane", ground)
    # Dome light
    light = sim_utils.DomeLightCfg(intensity=2000.0, color=(0.75, 0.75, 0.75))
    light.func("/World/Light", light)
    # Origins grid
    return define_origins(num_robots, spacing)


def main():
    # Initialize the simulation context
    sim_cfg = sim_utils.SimulationCfg(dt=0.005, device=args_cli.device)
    sim = sim_utils.SimulationContext(sim_cfg)
    # Set main camera (NVST captures whatever the persp camera shows)
    sim.set_camera_view(eye=(2.5, 2.5, 2.5), target=(0.0, 0.0, 0.0))
    # Design the scene
    env_origins = design_scene(args_cli.num_robots).to(sim.device)
    print(f"[INFO] design_scene: {args_cli.num_robots} origins on device {sim.device}")
    # Play the simulator (resets state)
    sim.reset()
    print("[INFO] Setup complete. Looping until shutdown...")
    while simulation_app.is_running():
        sim.step()
```

- [ ] **Step 3: Push and smoke test**

```bash
POD=$(ssh root@10.61.3.75 "k0s kubectl -n isaac-launchable get pods -l instance=pod-0 -o jsonpath='{.items[0].metadata.name}'")
cat /Users/xiilab/git/isaac-launchable/isaaclab-patches/replay_anymal_d.py | \
  ssh root@10.61.3.75 "k0s kubectl -n isaac-launchable exec -i $POD -c vscode -- bash -c 'cat > /workspace/data/replay/replay_anymal_d.py'"
```

Run in pod (vscode terminal). Open `http://10.61.3.125/viewer/` in Chrome incognito.

```bash
cd /workspace/isaaclab
./isaaclab.sh -p /workspace/data/replay/replay_anymal_d.py --checkpoint dummy.pt --num_robots 7 --livestream 2 \
  --kit_args "--/exts/omni.kit.livestream.app/primaryStream/publicIp=$ISAACSIM_HOST --/exts/omni.kit.livestream.app/primaryStream/streamPort=$ISAACSIM_STREAM_PORT --/exts/omni.kit.livestream.app/primaryStream/signalPort=$ISAACSIM_SIGNAL_PORT"
```

**Expected:**
- Browser viewer renders an empty ground plane with the dome light (no robots yet — that's Task 3)
- `chrome://webrtc-internals` shows `inbound-rtp (kind=video, codec=H264)` with `framesDecoded > 0`
- This confirms the livestream path works end-to-end without `ManagerBasedRLEnv`

If `inbound-rtp video` is **absent**, abort — the pattern itself isn't reproducing quadrupeds.py. Re-read `scripts/demos/quadrupeds.py` and align.

Stop with Ctrl+C in the vscode terminal.

- [ ] **Step 4: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add isaaclab-patches/replay_anymal_d.py
git commit -m "feat(replay): scene — ground/dome/origins, livestream verified empty scene"
```

---

## Task 3 — Anymal-D Articulation

**Files:**
- Modify: `isaac-launchable/isaaclab-patches/replay_anymal_d.py`

- [ ] **Step 1: Modify `design_scene` to spawn Anymal-D robots and return the Articulation**

Change the `design_scene` signature and body to:

```python
def design_scene(num_robots: int, spacing: float = 2.0) -> Tuple[Articulation, torch.Tensor]:
    """Place a ground plane, a DomeLight, and N Anymal-D robots.

    Returns:
        (robot, env_origins) — single Articulation managing N prims, and
        their initial spawn origins.
    """
    # Ground
    ground = sim_utils.GroundPlaneCfg()
    ground.func("/World/defaultGroundPlane", ground)
    # Dome light
    light = sim_utils.DomeLightCfg(intensity=2000.0, color=(0.75, 0.75, 0.75))
    light.func("/World/Light", light)
    # Origins
    env_origins = define_origins(num_robots, spacing)
    # Spawn Anymal-D under /World/envs/env_<i>/Robot using a regex prim_path
    # The Articulation will manage all N prims via the regex.
    anymal_cfg = ANYMAL_D_CFG.replace(prim_path="/World/envs/env_.*/Robot")
    # Create per-env Xform prims so the regex path resolves
    for i, origin in enumerate(env_origins.tolist()):
        prim_utils_create_xform(f"/World/envs/env_{i}", origin)
    robot = Articulation(cfg=anymal_cfg)
    return robot, env_origins
```

- [ ] **Step 2: Add the `prim_utils_create_xform` helper above `design_scene`**

Insert after `from isaaclab_assets.robots.anymal import ANYMAL_D_CFG  # isort:skip`:

```python
import isaacsim.core.utils.prims as prim_utils


def prim_utils_create_xform(prim_path: str, translation):
    """Create an Xform prim if it doesn't exist (Isaac Sim 6.0 helper)."""
    prim_utils.create_prim(prim_path, "Xform", translation=tuple(translation))
```

- [ ] **Step 3: Update `main()` to receive the Articulation, reset its state, and step the sim**

Replace the body of `main()` with:

```python
def main():
    sim_cfg = sim_utils.SimulationCfg(dt=0.005, device=args_cli.device)
    sim = sim_utils.SimulationContext(sim_cfg)
    sim.set_camera_view(eye=(2.5, 2.5, 2.5), target=(0.0, 0.0, 0.0))
    robot, env_origins = design_scene(args_cli.num_robots)
    env_origins = env_origins.to(sim.device)
    sim.reset()
    print(f"[INFO] {args_cli.num_robots} Anymal-D robots placed. Stepping sim...")
    sim_dt = sim.get_physics_dt()
    while simulation_app.is_running():
        sim.step()
        robot.update(sim_dt)
```

- [ ] **Step 4: Push, smoke test, verify visually**

Push file (same as Task 2 step 3), then in the pod:

```bash
cd /workspace/isaaclab
./isaaclab.sh -p /workspace/data/replay/replay_anymal_d.py --checkpoint dummy.pt --num_robots 7 --livestream 2 \
  --kit_args "--/exts/omni.kit.livestream.app/primaryStream/publicIp=$ISAACSIM_HOST --/exts/omni.kit.livestream.app/primaryStream/streamPort=$ISAACSIM_STREAM_PORT --/exts/omni.kit.livestream.app/primaryStream/signalPort=$ISAACSIM_SIGNAL_PORT"
```

**Expected:**
- Browser viewer shows 7 Anymal-D robots arranged in a grid on the ground
- Robots collapse / fall (no policy yet — they're passive ragdolls). That's fine.
- `chrome://webrtc-internals` `inbound-rtp (kind=video)` `framesDecoded` keeps incrementing

Ctrl+C, then commit.

- [ ] **Step 5: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add isaaclab-patches/replay_anymal_d.py
git commit -m "feat(replay): spawn Anymal-D × N as Articulation"
```

---

## Task 4 — `load_policy()`: extract actor MLP from rsl_rl checkpoint

**Files:**
- Modify: `isaac-launchable/isaaclab-patches/replay_anymal_d.py`

- [ ] **Step 1: Add `load_policy()` after `prim_utils_create_xform`**

```python
def load_policy(checkpoint_path: str, device: torch.device) -> torch.nn.Module:
    """Load the actor MLP from an rsl_rl checkpoint and return a forward-only model.

    rsl_rl 4.x (IsaacLab default) saves under 'actor_state_dict' with keys like
    'mlp.0.weight', 'mlp.0.bias', plus a non-MLP 'distribution.std_param' for
    the action Gaussian. Older runners may use 'model_state_dict' with prefixed
    keys ('actor.mlp.*' / 'policy.mlp.*'). Both formats are handled.

    Architecture (verified via train.py log on this project):
        Linear(48, 128) -> ELU -> Linear(128, 128) -> ELU
        -> Linear(128, 128) -> ELU -> Linear(128, 12)
    """
    state = torch.load(checkpoint_path, map_location=device, weights_only=False)
    if "actor_state_dict" in state:
        actor_sd_raw = state["actor_state_dict"]
    elif "model_state_dict" in state:
        actor_sd_raw = state["model_state_dict"]
    else:
        raise RuntimeError(
            f"No actor state_dict found. Top-level keys: {list(state.keys())}"
        )
    actor_sd = {}
    for k, v in actor_sd_raw.items():
        # Skip non-MLP entries (e.g. distribution.std_param for the Gaussian).
        if not any(k.startswith(p) for p in ("actor.mlp.", "policy.mlp.", "actor.", "mlp.")):
            continue
        # Strip prefixes (longest first so partial matches don't win).
        for prefix in ("actor.mlp.", "policy.mlp.", "actor.", "mlp."):
            if k.startswith(prefix):
                actor_sd[k[len(prefix):]] = v
                break
    if not actor_sd:
        raise RuntimeError(
            f"No actor weights found in checkpoint. Keys: "
            f"{list(actor_sd_raw.keys())[:10]}..."
        )
    mlp = torch.nn.Sequential(
        torch.nn.Linear(48, 128),
        torch.nn.ELU(),
        torch.nn.Linear(128, 128),
        torch.nn.ELU(),
        torch.nn.Linear(128, 128),
        torch.nn.ELU(),
        torch.nn.Linear(128, 12),
    )
    mlp.load_state_dict(actor_sd, strict=True)
    mlp.to(device).eval()
    print(f"[INFO] Loaded actor MLP from {checkpoint_path}")
    return mlp
```

- [ ] **Step 2: Call `load_policy` in `main()` (no inference yet — just verify it loads)**

Modify `main()`:

```python
def main():
    sim_cfg = sim_utils.SimulationCfg(dt=0.005, device=args_cli.device)
    sim = sim_utils.SimulationContext(sim_cfg)
    sim.set_camera_view(eye=(2.5, 2.5, 2.5), target=(0.0, 0.0, 0.0))
    robot, env_origins = design_scene(args_cli.num_robots)
    env_origins = env_origins.to(sim.device)
    # Load the policy BEFORE sim.reset() to fail fast on bad checkpoint paths.
    policy = load_policy(args_cli.checkpoint, torch.device(sim.device))
    sim.reset()
    print(f"[INFO] {args_cli.num_robots} robots, policy loaded. Stepping sim...")
    sim_dt = sim.get_physics_dt()
    while simulation_app.is_running():
        sim.step()
        robot.update(sim_dt)
```

- [ ] **Step 3: Push and smoke test with a real checkpoint**

We have a checkpoint from the earlier `train.py` run: `/workspace/isaaclab/logs/rsl_rl/anymal_d_flat/2026-04-25_02-25-24/model_4.pt`. If it's been GC'd, run train again first:

```bash
cd /workspace/isaaclab
./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/train.py --task Isaac-Velocity-Flat-Anymal-D-v0 --num_envs 32 --max_iterations 5 --headless
# Find the latest model:
CKPT=$(ls -t /workspace/isaaclab/logs/rsl_rl/anymal_d_flat/*/model_*.pt | head -1)
echo "Using $CKPT"
```

Push the script (same as Task 2 step 3), then:

```bash
cd /workspace/isaaclab
./isaaclab.sh -p /workspace/data/replay/replay_anymal_d.py --checkpoint $CKPT --num_robots 7 --livestream 2 \
  --kit_args "--/exts/omni.kit.livestream.app/primaryStream/publicIp=$ISAACSIM_HOST --/exts/omni.kit.livestream.app/primaryStream/streamPort=$ISAACSIM_STREAM_PORT --/exts/omni.kit.livestream.app/primaryStream/signalPort=$ISAACSIM_SIGNAL_PORT"
```

**Expected:**
- `[INFO] Loaded actor MLP from /workspace/isaaclab/logs/...model_X.pt` line
- No error (actor weights load with `strict=True`)
- Same visual as Task 3 (passive ragdolls)

If `RuntimeError: No actor weights found in checkpoint`, print the keys and update the prefix list. Re-run.

- [ ] **Step 4: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add isaaclab-patches/replay_anymal_d.py
git commit -m "feat(replay): load actor MLP from rsl_rl checkpoint"
```

---

## Task 5 — `collect_obs()`: 48-dim observation matching Isaac-Velocity-Flat-Anymal-D-v0

**Files:**
- Modify: `isaac-launchable/isaaclab-patches/replay_anymal_d.py`

> **Note**: IsaacLab Newton/PhysX 백엔드는 `robot.data.X` 를 `wp.array` (Warp) 로 반환한다. `torch.cat` / 산술 연산은 torch tensor 가 필요하므로 각 access 를 `wp.to_torch()` 로 zero-copy view 변환해야 한다. 또한 `import warp as wp` 를 (다른 Kit-extension import 들과 함께) post-Kit-boot 영역에 추가한다.

- [ ] **Step 1: Add `import warp as wp` to the post-Kit-boot import block (after `import torch`)**

```python
import warp as wp
```

- [ ] **Step 2: Add `collect_obs()` after `load_policy`**

```python
def collect_obs(
    robot: Articulation,
    last_actions: torch.Tensor,
    velocity_cmd: torch.Tensor,
) -> torch.Tensor:
    """Build the 48-dim observation that matches Isaac-Velocity-Flat-Anymal-D-v0.

    IsaacLab's articulation_data exposes physical state as Warp arrays
    (``wp.array`` with vec3f / float32 dtypes). The trained policy and
    ``torch.cat`` need torch tensors, so we wrap each access with
    ``wp.to_torch()`` (zero-copy view).

    Layout (must match the trained policy's expected order):
        [0:3]   base_lin_vel    (in base frame)
        [3:6]   base_ang_vel    (in base frame)
        [6:9]   projected_gravity (in base frame; default down = (0,0,-1))
        [9:12]  velocity_commands (vx, vy, yaw_rate)
        [12:24] joint_pos - default_joint_pos    (relative)
        [24:36] joint_vel                        (absolute)
        [36:48] last_actions                     (12)
    """
    base_lin_vel = wp.to_torch(robot.data.root_lin_vel_b)               # [N, 3]
    base_ang_vel = wp.to_torch(robot.data.root_ang_vel_b)               # [N, 3]
    projected_gravity = wp.to_torch(robot.data.projected_gravity_b)     # [N, 3]
    joint_pos = wp.to_torch(robot.data.joint_pos)                       # [N, 12]
    default_joint_pos = wp.to_torch(robot.data.default_joint_pos)       # [N, 12]
    joint_pos_rel = joint_pos - default_joint_pos                       # [N, 12]
    joint_vel = wp.to_torch(robot.data.joint_vel)                       # [N, 12]
    return torch.cat(
        [
            base_lin_vel,
            base_ang_vel,
            projected_gravity,
            velocity_cmd,
            joint_pos_rel,
            joint_vel,
            last_actions,
        ],
        dim=-1,
    )
```

- [ ] **Step 2: Wire `collect_obs` in `main()` and verify shape**

Replace `main()` body with:

```python
def main():
    sim_cfg = sim_utils.SimulationCfg(dt=0.005, device=args_cli.device)
    sim = sim_utils.SimulationContext(sim_cfg)
    sim.set_camera_view(eye=(2.5, 2.5, 2.5), target=(0.0, 0.0, 0.0))
    robot, env_origins = design_scene(args_cli.num_robots)
    env_origins = env_origins.to(sim.device)
    policy = load_policy(args_cli.checkpoint, torch.device(sim.device))
    sim.reset()
    sim_dt = sim.get_physics_dt()
    N = args_cli.num_robots
    device = sim.device
    last_actions = torch.zeros(N, 12, device=device)
    velocity_cmd = torch.tensor(
        [args_cli.velocity_x, args_cli.velocity_y, args_cli.velocity_yaw],
        device=device,
    ).expand(N, 3).contiguous()
    print(f"[INFO] velocity_cmd: {velocity_cmd[0].tolist()}")
    # Sanity check obs shape on first frame
    obs = collect_obs(robot, last_actions, velocity_cmd)
    assert obs.shape == (N, 48), f"Expected obs shape ({N}, 48), got {tuple(obs.shape)}"
    print(f"[INFO] First-frame obs shape OK: {tuple(obs.shape)}")
    while simulation_app.is_running():
        # Collect obs (no inference yet)
        obs = collect_obs(robot, last_actions, velocity_cmd)
        sim.step()
        robot.update(sim_dt)
```

- [ ] **Step 3: Push and smoke test**

Same push as before, run, expect:
- `[INFO] First-frame obs shape OK: (7, 48)`
- No assertion error
- Same visual as before (passive ragdolls)

If shape is off, print individual tensor shapes and trace which one is wrong (most likely an `actions` dim mismatch — Anymal-D must have exactly 12 joints).

- [ ] **Step 4: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add isaaclab-patches/replay_anymal_d.py
git commit -m "feat(replay): collect 48-dim obs matching Velocity-Flat-Anymal-D"
```

---

## Task 6 — `apply_action()`: scaled joint position target

**Files:**
- Modify: `isaac-launchable/isaaclab-patches/replay_anymal_d.py`

> **Note (carries over from Task 5)**: `robot.data.default_joint_pos` is a `wp.array` on this build, so `.clone()` won't work directly — wrap with `wp.to_torch()` first. `robot.set_joint_position_target(target)` accepts a torch tensor directly.

- [ ] **Step 1: Add `apply_action()` after `collect_obs`**

```python
def apply_action(
    robot: Articulation,
    action: torch.Tensor,
    default_joint_pos: torch.Tensor,
    action_scale: float = 0.5,
) -> None:
    """Apply scaled action as joint position target.

    The Velocity-Flat-Anymal-D-v0 task uses
        JointPositionActionCfg(scale=0.5, use_default_offset=True)
    so the actuator target is default_joint_pos + scale * action.
    """
    target = default_joint_pos + action_scale * action
    robot.set_joint_position_target(target)
    robot.write_data_to_sim()
```

- [ ] **Step 2: Wire `apply_action` in `main()` (still no inference — pass zero action)**

Replace the loop in `main()`:

```python
    default_joint_pos = wp.to_torch(robot.data.default_joint_pos).clone()
    while simulation_app.is_running():
        obs = collect_obs(robot, last_actions, velocity_cmd)
        zero_action = torch.zeros_like(last_actions)
        apply_action(robot, zero_action, default_joint_pos)
        sim.step()
        robot.update(sim_dt)
```

(Add the `default_joint_pos = wp.to_torch(robot.data.default_joint_pos).clone()` line right after `sim.reset()` if you prefer it earlier; either order works because `sim.reset()` initializes the data buffer.)

- [ ] **Step 3: Push and smoke test**

Same push/run. Expected:
- Robots **stand** in a stable default pose (zero action + default offset = neutral standing)
- No collapse this time (the difference vs Task 5 confirms apply_action is wired)

- [ ] **Step 4: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add isaaclab-patches/replay_anymal_d.py
git commit -m "feat(replay): apply zero-action — robots stand stably"
```

---

## Task 7 — Inference loop integration: policy(obs) → action → apply

**Files:**
- Modify: `isaac-launchable/isaaclab-patches/replay_anymal_d.py`

- [ ] **Step 1: Replace the loop in `main()` to use `policy(obs)`**

```python
    default_joint_pos = robot.data.default_joint_pos.clone()
    print("[INFO] Inference loop started. Ctrl+C to exit.")
    while simulation_app.is_running():
        obs = collect_obs(robot, last_actions, velocity_cmd)
        with torch.inference_mode():
            action = policy(obs)
        apply_action(robot, action, default_joint_pos)
        sim.step()
        robot.update(sim_dt)
        last_actions = action
```

- [ ] **Step 2: Push and run with the real checkpoint**

```bash
POD=$(ssh root@10.61.3.75 "k0s kubectl -n isaac-launchable get pods -l instance=pod-0 -o jsonpath='{.items[0].metadata.name}'")
cat /Users/xiilab/git/isaac-launchable/isaaclab-patches/replay_anymal_d.py | \
  ssh root@10.61.3.75 "k0s kubectl -n isaac-launchable exec -i $POD -c vscode -- bash -c 'cat > /workspace/data/replay/replay_anymal_d.py'"
```

In pod:

```bash
cd /workspace/isaaclab
CKPT=$(ls -t /workspace/isaaclab/logs/rsl_rl/anymal_d_flat/*/model_*.pt | head -1)
./isaaclab.sh -p /workspace/data/replay/replay_anymal_d.py --checkpoint $CKPT --num_robots 7 --velocity_x 0.5 --livestream 2 \
  --kit_args "--/exts/omni.kit.livestream.app/primaryStream/publicIp=$ISAACSIM_HOST --/exts/omni.kit.livestream.app/primaryStream/streamPort=$ISAACSIM_STREAM_PORT --/exts/omni.kit.livestream.app/primaryStream/signalPort=$ISAACSIM_SIGNAL_PORT"
```

**Expected:**
- 7 Anymal-D robots in `/viewer/` attempting to walk forward (a 5-iter checkpoint will walk poorly — probably wobble forward or fall, not graceful gait)
- `chrome://webrtc-internals` `inbound-rtp (kind=video)` `framesDecoded` increments steadily (≥ 30 fps)
- The session does NOT terminate at ~51 s with `SERVER_DISCONNECTED`

This is the spec's primary acceptance criterion. If it passes, B′ is validated.

- [ ] **Step 3: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add isaaclab-patches/replay_anymal_d.py
git commit -m "feat(replay): inference loop — policy(obs) drives joint targets"
```

---

## Task 8 — Final E2E acceptance + README

**Files:**
- Modify: `isaac-launchable/isaaclab-patches/README.md` (create if missing)

- [ ] **Step 1: Run a 5-minute soak test in pod**

Same command as Task 7 step 2. Leave the viewer open in Chrome incognito for 5 minutes.

**Acceptance:**
- [ ] Viewer renders Anymal-D robots throughout
- [ ] No `SERVER_DISCONNECTED` for 5 minutes
- [ ] `chrome://webrtc-internals` `framesDecoded` keeps incrementing
- [ ] Average fps ≥ 30 (look at the `framesPerSecond` graph)
- [ ] Ctrl+C cleanly shuts down (Kit closes within 10 s)

If any acceptance fails, debug before continuing. Common issues:
- Slow fps: the policy is fine; `--num_robots` too high. Try 4.
- Disconnect at 51s: NVST didn't actually start a video track. Recheck `chrome://webrtc-internals` for `inbound-rtp (kind=video)` — if absent, the script accidentally inherited a Manager-based env path. Re-check `from isaaclab.envs ...` imports — there should be NONE.

- [ ] **Step 2: Add README entry**

Append to `isaac-launchable/isaaclab-patches/README.md` (or create the file):

```markdown
## `replay_anymal_d.py` — IsaacLab #5364 workaround

`scripts/reinforcement_learning/rsl_rl/play.py --livestream 2` cannot stream
video because `ManagerBasedRLEnv` does not bind a viewport that NVST can
capture (IsaacLab issue #5364, blocked by #5362). This script replays a
trained Anymal-D rsl_rl policy using the verified-working `quadrupeds.py`
pattern (`SimulationContext` + `sim.step()` loop) and inlines a stripped-
down inference path (just the actor MLP).

### Usage (inside the K8s pod's vscode terminal)

```bash
cd /workspace/isaaclab
CKPT=$(ls -t /workspace/isaaclab/logs/rsl_rl/anymal_d_flat/*/model_*.pt | head -1)
./isaaclab.sh -p /workspace/data/replay/replay_anymal_d.py \
  --checkpoint $CKPT \
  --num_robots 7 \
  --velocity_x 0.5 \
  --livestream 2 \
  --kit_args "--/exts/omni.kit.livestream.app/primaryStream/publicIp=$ISAACSIM_HOST --/exts/omni.kit.livestream.app/primaryStream/streamPort=$ISAACSIM_STREAM_PORT --/exts/omni.kit.livestream.app/primaryStream/signalPort=$ISAACSIM_SIGNAL_PORT"
```

Then open `http://10.61.3.125/viewer/` in Chrome incognito.

### Limitations

- Tasked only to `Isaac-Velocity-Flat-Anymal-D-v0` policies. Different
  observation orderings or action scaling (e.g. velocity-rough or non-Anymal
  robots) need parallel scripts.
- Manager-based features (curriculum, randomization, reward computation)
  are intentionally absent — this is a replay/visualization script only.
- Will become unnecessary once IsaacLab #5364 is fixed upstream.
```

- [ ] **Step 3: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add isaaclab-patches/README.md
git commit -m "docs(replay): usage + limitations for replay_anymal_d.py"
```

- [ ] **Step 4: Push to origin** (only after acceptance passes)

```bash
cd /Users/xiilab/git/isaac-launchable
git push origin main
```

---

## Self-Review

**Spec coverage:**
- Background / problem ✅ Task plan header
- Decision (B′) ✅ implicit in every task using SimulationContext directly
- Architecture diagram (1)–(5) ✅ Tasks 1, 2, 3, 4, 5–7
- Components (parse_args, design_scene, load_policy, collect_obs, apply_action, run_inference) ✅ Tasks 1, 2–3, 4, 5, 6, 7
- Data flow (single step) ✅ Tasks 5–7
- Verification criteria ✅ Task 8
- Out of scope ✅ README explicitly states limitations
- Risks (obs order, action scale, default offset) ✅ Tasks 5 and 6 — values pinned to the Velocity-Flat-Anymal-D config

**Placeholder scan:**
- No "TBD" / "TODO" / "fill in" remaining
- Every step has actual commands or actual code
- File paths are absolute or fully qualified relative to a stated cwd

**Type consistency:**
- `Articulation` (from `isaaclab.assets`) is consistent across tasks 3–7
- `last_actions: torch.Tensor[N, 12]` is consistent (Tasks 5, 7)
- `velocity_cmd: torch.Tensor[N, 3]` is consistent (Tasks 5, 7)
- `default_joint_pos: torch.Tensor[N, 12]` is consistent (Tasks 6, 7)
- `policy: torch.nn.Sequential` returning `[N, 12]` is consistent (Tasks 4, 7)

No issues — plan is ready.
