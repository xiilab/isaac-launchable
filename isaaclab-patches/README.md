# IsaacLab patches & K8s livestream replay

This directory contains:

| File | Purpose |
|------|---------|
| `train.py` | IsaacLab `scripts/reinforcement_learning/rsl_rl/train.py` with isaac-launchable patches. |
| `play.py` | IsaacLab `scripts/reinforcement_learning/rsl_rl/play.py` with isaac-launchable patches. **Does not stream video in livestream mode** (IsaacLab #5364). |
| `replay_common.py` | Shared scene/loop/network helpers + `TaskAdapter` contract. |
| `replay_ant.py` | `TaskAdapter` for `Isaac-Ant-Direct-v0`. Standalone-runnable. |
| `replay_anymal_d.py` | `TaskAdapter` for `Isaac-Velocity-Flat-Anymal-D-v0`. Standalone-runnable. |
| `play_livestream.py` | Drop-in replacement for `play.py` that dispatches to a `TaskAdapter`. **This is what you run in livestream.** |

## TL;DR for tutorial users

The official tutorial flow:
```bash
./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/train.py --task X ...
./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/play.py  --task X --livestream 2 ...   # ❌ black screen in K8s
```
Replace the second command with:
```bash
./isaaclab.sh -p /workspace/data/replay/play_livestream.py     --task X --livestream 2 ...   # ✅
```
All other flags (`--task`, `--num_envs`, `--checkpoint`, `--livestream`, `--kit_args`) are identical.

## Why `play.py` is broken in livestream

IsaacLab #5364 (we filed this). Anything that goes through `gym.make` →
`DirectRLEnv` / `ManagerBasedRLEnv` → `InteractiveScene._setup_scene` produces
zero video frames in NVST. Verified isolation:

| Configuration | Result |
|---|---|
| `play.py`, default | viewport binds, USD prims exist, RTX warning about fabric→Hydra dynamic transforms — **black screen** |
| `play.py --num_envs 1` | 38 USD prims, viewport active — **black screen** |
| `play.py` + `clone_in_fabric=False` | 2432 USD prims, but simulation thrashes — NVST keepalive timeout |
| `play.py` + `replicate_physics=False` | thrashes — same as above |
| `play.py` + `--/rtx/hydra/readTransformsFromFabricInRenderDelegate=false` | thrashes — same as above |
| `play.py` + explicit `env.unwrapped.sim.render()` per loop step | **black screen** |
| `replay_*.py` (`SimulationContext` directly + manual `prim_utils.create_prim`) | ✅ frames stream |

The `gym.make` path binds RTX/Hydra to a render product that fabric clone
transforms never reach. None of the public config knobs we tried route around
it. The pattern in `replay_*.py` (no `gym.make`, no `InteractiveScene` clone) is
the only one that works.

## Adding a new task

`replay_*.py` for each task family is ~150 LOC. Steps:

1. Read `<task>_env.py` (or the locomotion base) to learn the obs layout, action
   space, action_scale, decimation, sim dt, network arch, termination height.
2. Copy `replay_ant.py` to `replay_<task>.py`.
3. Replace the `AntAdapter` class:
   - set class constants (`obs_dim`, `action_dim`, `hidden_dims`, `sim_dt`,
     `decimation`, `spacing`),
   - implement `build_robot_cfg()` (return the asset cfg from
     `isaaclab_assets.robots.<robot>`),
   - implement `setup()` to cache references the obs computation needs,
   - implement `compute_obs()` mirroring the env's `_get_observations`,
   - implement `apply_action()` mirroring the env's `_apply_action`,
   - optionally implement `maybe_reset()` if the task has a termination
     condition you want to recycle (Ant: torso height; Anymal-D: never falls,
     no reset needed).
4. Decorate the class with `@register_adapter("Isaac-<Task>-v0")`.
5. Add `import replay_<task>` to `play_livestream.py` so registration fires.
6. Smoke-test with the standalone entry first (`./isaaclab.sh -p
   replay_<task>.py --checkpoint ... --livestream 2 ...`), then via
   `play_livestream.py --task Isaac-<Task>-v0`.

## Standalone vs wrapper

`replay_ant.py` and `replay_anymal_d.py` keep their original standalone CLIs
(including `--velocity_x` for Anymal-D) so existing scripts and muscle memory
keep working. `play_livestream.py` is the thin wrapper that takes `--task`
like `play.py` and looks up the right adapter.

## Pod workspace layout

By convention everything lives in `/workspace/data/replay/`:

```
/workspace/data/replay/
├── replay_common.py       # shared helpers
├── replay_ant.py          # Ant adapter + standalone
├── replay_anymal_d.py     # Anymal-D adapter + standalone
└── play_livestream.py     # tutorial-compatible entry point
```

`./isaaclab.sh -p` adds the script's directory to `sys.path`, so the imports
between these files just work as long as they're co-located.

## Required `--kit_args`

For NVST to advertise the right public IP and ports in K8s, every livestream
invocation needs:

```text
--/exts/omni.kit.livestream.app/primaryStream/publicIp=$ISAACSIM_HOST
--/exts/omni.kit.livestream.app/primaryStream/streamPort=$ISAACSIM_STREAM_PORT
--/exts/omni.kit.livestream.app/primaryStream/signalPort=$ISAACSIM_SIGNAL_PORT
```

The pod env vars (`ISAACSIM_HOST`, `ISAACSIM_STREAM_PORT`,
`ISAACSIM_SIGNAL_PORT`) are set by the deployment and resolved at shell
expansion. Don't quote them away.
