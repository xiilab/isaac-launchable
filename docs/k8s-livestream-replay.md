# Isaac Lab K8s Livestream Quick Reference

Tutorial-compatible commands for training and replaying RL policies in an `isaac-launchable` pod with browser livestream. The shipping wrapper at `isaaclab-patches/play_livestream.py` (deployed under `/workspace/data/replay/` in the pod) sidesteps two upstream IsaacLab bugs that block `play.py --livestream 2`. See `isaaclab-patches/README.md` for the wrapper's design notes and instructions for adding new tasks.

## TL;DR

```bash
# 1) Train (standard IsaacLab CLI, headless for speed)
./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/train.py \
    --task Isaac-Ant-Direct-v0 \
    --headless \
    --max_iterations 1000

# 2) Replay with livestream (drop-in replacement for play.py)
./isaaclab.sh -p /workspace/data/replay/play_livestream.py \
    --task Isaac-Ant-Direct-v0 \
    --num_envs 64 \
    --livestream 2
```

Plain Isaac Sim (no Isaac Lab, just the standalone Kit viewer):

```bash
ACCEPT_EULA=y /isaac-sim/runheadless.sh
```

Open the browser viewer at `http://<viewer-host>/viewer/` — see [Browser](#browser) below for how to resolve `<viewer-host>` for this deployment.

## Why `play_livestream.py` instead of `play.py`

Two upstream IsaacLab bugs block the official tutorial flow on `--livestream 2`:

| Issue | Symptom | Our handling |
|------|---------|--------------|
| [#5363](https://github.com/isaac-sim/IsaacLab/issues/5363) | `play.py` hangs in `OnPolicyRunner.__init__` with rsl-rl 4.x because `obs_groups` is missing | `play_livestream.py` loads the actor MLP weights directly — the broken `OnPolicyRunner` path is not used at all |
| [#5364](https://github.com/isaac-sim/IsaacLab/issues/5364) | `play.py --livestream 2` produces black viewport: WebRTC connects but no video track | `play_livestream.py` constructs the scene via `SimulationContext` + manual `prim_utils.create_prim` (the pattern `quadrupeds.py` uses), bypassing the `gym.make` → `DirectRLEnv`/`ManagerBasedRLEnv` path that doesn't reach NVST |

Both bypasses are confirmed to produce a healthy `inbound-rtp (kind=video, H264)` track in `chrome://webrtc-internals`.

## Auto-fills (CLI parity with `play.py`)

| Flag | Behavior when omitted |
|------|-----------------------|
| `--checkpoint` | Resolves to `logs/rsl_rl/<adapter.experiment_name>/<latest_run>/model_<biggest_N>.pt` (matches `play.py`'s default) |
| `--kit_args`   | Auto-prepends `--/exts/omni.kit.livestream.app/primaryStream/{publicIp,streamPort,signalPort}` from `ISAACSIM_HOST` / `ISAACSIM_STREAM_PORT` / `ISAACSIM_SIGNAL_PORT` pod env vars when `--livestream > 0` |

User-provided values still win.

## Optional flags

| Flag | Effect |
|------|--------|
| `--cam_zoom 0.5` | Camera 50% closer to swarm |
| `--cam_zoom 2.0` | Camera 2x farther |
| `--spacing 4.0`  | Override grid spacing between robots |
| `--num_envs N`   | Number of robots (alias of `--num_robots`) |

Anymal-D adapter additionally accepts `--velocity_x / --velocity_y / --velocity_yaw` when run as standalone `replay_anymal_d.py`.

## Registered tasks

| `--task` | Robot | Source |
|----------|-------|--------|
| `Isaac-Ant-Direct-v0` | 8-DoF MuJoCo Ant | `replay_ant.py` |
| `Isaac-Velocity-Flat-Anymal-D-v0` | 12-DoF Anymal-D quadruped | `replay_anymal_d.py` |

Adding a new task: ~150 LOC `TaskAdapter` subclass + `import` line in `play_livestream.py`. See `/workspace/data/replay/README.md` (in `isaac-launchable` repo: `isaaclab-patches/README.md`) for the recipe.

## HAMi vGPU partition

This pod is allocated `nvidia.com/gpumem: 10k` (10 GB) / `nvidia.com/gpucores: 50` (50 % SMs) by HAMi. The Vulkan layer is activated via `HAMI_VULKAN_ENABLE=1` env injected by the HAMi webhook (annotation `hami.io/vulkan: "true"` on the deployment). Verify partitioning with:

```bash
./isaaclab.sh -p /workspace/data/replay/play_livestream.py --task Isaac-Ant-Direct-v0 --num_envs 64 --livestream 2 2>&1 | grep "GPU Memory"
# Expected: |  0  | NVIDIA RTX 6000 Ada Generation | Yes: 0 | | 10000   MB | ...
# (Without the layer, this would show ~46068 MB = full GPU minus ECC overhead)
```

## Browser

The viewer URL is `http://<viewer-host>/viewer/`, where `<viewer-host>` depends on the deployment. Resolve it with `kubectl` (run on the host, not inside the pod):

```bash
# LoadBalancer external IP, if exposed that way
kubectl -n isaac-launchable get svc -l app=isaac-launchable -o \
    jsonpath='{range .items[?(@.spec.type=="LoadBalancer")]}{.metadata.name}{"\t"}{.status.loadBalancer.ingress[0].ip}{"\n"}{end}'

# NodePort fallback
kubectl -n isaac-launchable get svc -l app=isaac-launchable
# then http://<any-node-ip>:<nodeport>/viewer/
```

Or use whatever ingress / hostname your cluster maps to the `isaac-launchable-*-lb` service. Refresh the page after Kit boots — wait for `[INFO] Inference loop started ...` in the script's stdout before connecting.

## Where things live in this pod

| Path | Persistence |
|------|-------------|
| `/workspace/data/replay/`   | PVC (`isaac-workspace-pvc-0`) — survives pod recreation |
| `/workspace/data/logs_backup/` | PVC — manual backup directory used during the HAMi rollout |
| `/workspace/isaaclab/`      | Container overlay — **lost on pod recreation**, including `logs/rsl_rl/` checkpoints |
| `/etc/vulkan/implicit_layer.d/hami.json` | Bind-mounted by device-plugin |
| `/usr/local/vgpu/libvgpu.so` | Bind-mounted by device-plugin (`LD_PRELOAD`) |

If you produce checkpoints worth keeping, copy them to `/workspace/data/` before any pod restart. Or open a follow-up to mount `/workspace/isaaclab/logs` as a PVC subPath.
