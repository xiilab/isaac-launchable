"""Common scene/loop helpers for K8s livestream-friendly RL policy replay.

Why this exists: IsaacLab #5364 — `play.py --livestream 2` produces no video
frames for any task that goes through `gym.make` → `DirectRLEnv` /
`ManagerBasedRLEnv` → `InteractiveScene._setup_scene`. The viewport binds and
USD prims are created (verified up to 2432 prims for 64-env Ant), but the
fabric-clone transforms never reach the RTX/NVST capture path. We isolated the
issue down to single-env runs (38 USD prims, same black screen) and across
every config knob we have (`clone_in_fabric`, `replicate_physics`,
`readTransformsFromFabricInRenderDelegate`) — none reproduce a working stream.

The pattern below — `AppLauncher` → `SimulationContext` directly + manual
`prim_utils.create_prim` per env + `Articulation` regex prim_path — DOES
produce frames in NVST. quadrupeds.py and our `replay_*.py` scripts have used
it successfully for months. Anything in this module is shared boilerplate so
each new task is just a `TaskAdapter` subclass.

Per-task contract is in `TaskAdapter` below. Existing adapters live in
`replay_*.py` modules (one per task family); register them in `ADAPTERS` so
`play_livestream.py` can dispatch by `--task`.
"""
from __future__ import annotations

import argparse
import glob
import math
import os
import re
from typing import Callable, Dict, List, Optional, Tuple, Type

import torch
import warp as wp


# ---------------------------------------------------------------------------
# CLI / boot
# ---------------------------------------------------------------------------


def add_common_args(parser: argparse.ArgumentParser) -> None:
    """Add the CLI args every replay script (and play_livestream.py) shares.

    Mirrors IsaacLab `play.py` field names where possible so users can paste
    tutorial commands with minimal edits.
    """
    parser.add_argument("--checkpoint", type=str, default=None,
                        help="Path to rsl_rl checkpoint (.pt). If omitted, "
                             "auto-resolves to the latest model_*.pt under "
                             "logs/rsl_rl/<experiment>/<latest_run>/, matching "
                             "play.py's auto-load behavior.")
    parser.add_argument("--num_envs", type=int, default=None,
                        help="Number of envs/robots in the scene "
                             "(alias of --num_robots; takes precedence)")
    parser.add_argument("--num_robots", type=int, default=64,
                        help="Number of robots (used if --num_envs unset)")
    parser.add_argument("--spacing", type=float, default=None,
                        help="Grid spacing between robots (m). Defaults to "
                             "the task adapter's preferred spacing.")
    parser.add_argument("--seed", type=int, default=None,
                        help="(Accepted for play.py CLI parity; ignored.)")
    parser.add_argument("--real-time", action="store_true", default=False,
                        help="(Accepted for play.py CLI parity; ignored — "
                             "the loop already runs at sim_dt rate.)")


def resolve_num_robots(args_cli: argparse.Namespace) -> int:
    """`--num_envs` wins for play.py-style invocations; falls back to
    `--num_robots`."""
    return args_cli.num_envs if args_cli.num_envs is not None else args_cli.num_robots


def inject_livestream_kit_args(args_cli: argparse.Namespace) -> None:
    """Auto-prepend NVST publicIp / streamPort / signalPort kit args.

    The deployment sets `ISAACSIM_HOST`, `ISAACSIM_STREAM_PORT`,
    `ISAACSIM_SIGNAL_PORT` in the pod environment. Without these flags Kit
    advertises wrong endpoints in WebRTC offer SDP and the browser fails
    ICE — every replay invocation needs them. We inject so users don't
    have to copy-paste the long --kit_args string.

    User-supplied --kit_args (if any) wins over auto-injected values
    because it appears later on the command line.
    """
    livestream = getattr(args_cli, "livestream", 0) or 0
    if int(livestream) <= 0:
        return
    host = os.environ.get("ISAACSIM_HOST")
    stream_port = os.environ.get("ISAACSIM_STREAM_PORT")
    signal_port = os.environ.get("ISAACSIM_SIGNAL_PORT")
    if not (host and stream_port and signal_port):
        print("[INFO] livestream auto-inject skipped: ISAACSIM_HOST / "
              "ISAACSIM_STREAM_PORT / ISAACSIM_SIGNAL_PORT not all set.")
        return
    auto = (
        f"--/exts/omni.kit.livestream.app/primaryStream/publicIp={host} "
        f"--/exts/omni.kit.livestream.app/primaryStream/streamPort={stream_port} "
        f"--/exts/omni.kit.livestream.app/primaryStream/signalPort={signal_port}"
    )
    existing = getattr(args_cli, "kit_args", None)
    # AppLauncher accepts kit_args as either a list or a single string
    # depending on IsaacLab version; handle both.
    if isinstance(existing, list):
        args_cli.kit_args = auto.split() + existing
    else:
        existing_str = (existing or "").strip()
        args_cli.kit_args = f"{auto} {existing_str}".strip() if existing_str else auto
    print(f"[INFO] auto-injected livestream kit_args (publicIp={host}, "
          f"streamPort={stream_port}, signalPort={signal_port})")


def resolve_checkpoint(args_cli: argparse.Namespace, experiment_name: str) -> str:
    """Resolve `--checkpoint` value, auto-finding the latest run if absent.

    Matches IsaacLab `play.py`'s default: when no checkpoint is passed,
    pick `logs/rsl_rl/<experiment>/<latest_run>/model_<biggest_N>.pt`. Run
    dirs are timestamped (YYYY-MM-DD_HH-MM-SS) so sorted = chronological.
    """
    if args_cli.checkpoint:
        return args_cli.checkpoint
    log_root = os.path.abspath(os.path.join("logs", "rsl_rl", experiment_name))
    if not os.path.isdir(log_root):
        raise FileNotFoundError(
            f"--checkpoint not given and log root does not exist: {log_root}. "
            f"Train first with: ./isaaclab.sh -p scripts/reinforcement_learning/"
            f"rsl_rl/train.py --task <task> --headless"
        )
    runs = sorted(d for d in os.listdir(log_root)
                  if os.path.isdir(os.path.join(log_root, d)))
    if not runs:
        raise FileNotFoundError(f"No run directories under {log_root}.")
    run_dir = os.path.join(log_root, runs[-1])
    pts = glob.glob(os.path.join(run_dir, "model_*.pt"))
    if not pts:
        raise FileNotFoundError(f"No model_*.pt under {run_dir}.")
    # Sort by iteration number (model_<N>.pt) so model_999 > model_50.
    def _iter(p: str) -> int:
        m = re.search(r"model_(\d+)\.pt$", p)
        return int(m.group(1)) if m else -1
    pts.sort(key=_iter)
    chosen = pts[-1]
    print(f"[INFO] --checkpoint not given; auto-resolved {chosen}")
    return chosen


# ---------------------------------------------------------------------------
# Network / checkpoint
# ---------------------------------------------------------------------------


def load_actor_mlp(
    checkpoint_path: str,
    device: torch.device,
    obs_dim: int,
    action_dim: int,
    hidden_dims: List[int],
    activation: str = "elu",
) -> torch.nn.Module:
    """Load the actor MLP from an rsl_rl checkpoint.

    rsl_rl 4.x (IsaacLab default) stores the actor under `actor_state_dict`
    with bare keys like `mlp.0.weight`, plus a non-MLP `distribution.std_param`
    for the action Gaussian. Older runners use `model_state_dict` with
    `actor.mlp.*` / `policy.mlp.*` prefixes. This handles both.
    """
    state = torch.load(checkpoint_path, map_location=device, weights_only=False)
    if "actor_state_dict" in state:
        actor_sd_raw = state["actor_state_dict"]
    elif "model_state_dict" in state:
        actor_sd_raw = state["model_state_dict"]
    else:
        raise RuntimeError(
            f"No actor state_dict found in checkpoint. Top-level keys: "
            f"{list(state.keys())}"
        )
    actor_sd: Dict[str, torch.Tensor] = {}
    for k, v in actor_sd_raw.items():
        if not any(k.startswith(p) for p in ("actor.mlp.", "policy.mlp.", "actor.", "mlp.")):
            continue
        for prefix in ("actor.mlp.", "policy.mlp.", "actor.", "mlp."):
            if k.startswith(prefix):
                actor_sd[k[len(prefix):]] = v
                break
    if not actor_sd:
        raise RuntimeError(
            f"No actor MLP weights found. Sample raw keys: "
            f"{list(actor_sd_raw.keys())[:10]}..."
        )
    if activation.lower() == "elu":
        act = torch.nn.ELU
    elif activation.lower() == "tanh":
        act = torch.nn.Tanh
    elif activation.lower() == "relu":
        act = torch.nn.ReLU
    else:
        raise ValueError(f"Unsupported activation: {activation}")
    layers: List[torch.nn.Module] = []
    prev = obs_dim
    for h in hidden_dims:
        layers += [torch.nn.Linear(prev, h), act()]
        prev = h
    layers.append(torch.nn.Linear(prev, action_dim))
    mlp = torch.nn.Sequential(*layers)
    mlp.load_state_dict(actor_sd, strict=True)
    mlp.to(device).eval()
    print(f"[INFO] Loaded actor MLP ({obs_dim}->{hidden_dims}->{action_dim}, "
          f"{activation}) from {checkpoint_path}")
    return mlp


# ---------------------------------------------------------------------------
# Scene
# ---------------------------------------------------------------------------


def define_origins(num: int, spacing: float) -> torch.Tensor:
    """Centered grid of XY origins on Z=0 (same pattern as quadrupeds.py)."""
    out = torch.zeros(num, 3)
    cols = int(math.floor(math.sqrt(num)))
    rows = int(math.ceil(num / cols))
    xx, yy = torch.meshgrid(torch.arange(rows), torch.arange(cols), indexing="xy")
    out[:, 0] = spacing * xx.flatten()[:num] - spacing * (rows - 1) / 2.0
    out[:, 1] = spacing * yy.flatten()[:num] - spacing * (cols - 1) / 2.0
    return out


def build_scene(num_robots: int, spacing: float, robot_cfg, device: str):
    """Place ground + dome light + N env Xforms + Articulation.

    Returns (robot, env_origins_on_device).
    """
    import isaaclab.sim as sim_utils
    from isaaclab.assets import Articulation
    import isaacsim.core.utils.prims as prim_utils

    # Default ground is 100x100m — too small once trained policies walk
    # forward unboundedly. 1km square covers any reasonable replay duration.
    ground = sim_utils.GroundPlaneCfg(size=(1000.0, 1000.0))
    ground.func("/World/defaultGroundPlane", ground)
    light = sim_utils.DomeLightCfg(intensity=2000.0, color=(0.75, 0.75, 0.75))
    light.func("/World/Light", light)

    env_origins = define_origins(num_robots, spacing)
    for i, origin in enumerate(env_origins.tolist()):
        prim_utils.create_prim(f"/World/envs/env_{i}", "Xform",
                               translation=tuple(origin))
    robot_cfg = robot_cfg.replace(prim_path="/World/envs/env_.*/Robot")
    robot = Articulation(cfg=robot_cfg)
    return robot, env_origins.to(device)


# ---------------------------------------------------------------------------
# Camera follower
# ---------------------------------------------------------------------------


class CameraFollower:
    """Per-step exponential lerp toward fleet centroid + auto-zoom.

    Quadrupeds.py and IsaacLab's standalone demos use a static camera; replay
    needs follow because the trained policy moves robots unboundedly. Lerping
    every sim step (instead of teleporting every N steps) avoids the NVST
    keyframe spikes that look like buffering jitter.

    Auto-zoom: every tick we measure the max XY distance from the centroid
    (swarm extent) and lerp the camera offset to keep everyone in frame. This
    way Ant 64-bot crowds auto-pull-back and 7-bot Anymal-D stays close.
    """

    def __init__(self, num_robots: int, spacing: float, device: str,
                 alpha: float = 0.02, zoom_alpha: float = 0.02):
        # Initial offset tuned for the starting grid; auto-zoom takes over.
        grid_extent = math.sqrt(max(num_robots, 1)) * spacing
        self._min_xy = max(2.5, grid_extent * 0.6)
        self._min_z = max(2.0, grid_extent * 0.4)
        self._off_xy = self._min_xy
        self._off_z = self._min_z

        self.alpha = alpha
        self.zoom_alpha = zoom_alpha
        self.device = device
        self._target = torch.zeros(3, device=device)
        self._initialized = False

    def tick(self, robot, sim) -> None:
        pos_w = wp.to_torch(robot.data.root_pos_w)
        centroid = pos_w.mean(dim=0)
        if not self._initialized:
            self._target = centroid.clone()
            self._initialized = True
        else:
            self._target = self._target + self.alpha * (centroid - self._target)

        # Auto-zoom: max XY distance from centroid -> required radius.
        # 1.4x extent leaves comfortable margin; floor at the initial values
        # so the camera never zooms in tighter than the starting framing.
        deltas_xy = pos_w[:, :2] - centroid[:2]
        extent = deltas_xy.norm(dim=1).max().item()
        target_xy = max(self._min_xy, extent * 1.4 + 2.0)
        target_z = max(self._min_z, extent * 0.7 + 2.0)
        self._off_xy += self.zoom_alpha * (target_xy - self._off_xy)
        self._off_z += self.zoom_alpha * (target_z - self._off_z)

        t = self._target.cpu().tolist()
        sim.set_camera_view(
            eye=(t[0] + self._off_xy, t[1] + self._off_xy, t[2] + self._off_z),
            target=(t[0], t[1], t[2]),
        )


# ---------------------------------------------------------------------------
# TaskAdapter contract + registry
# ---------------------------------------------------------------------------


class TaskAdapter:
    """Per-task spec for the replay loop.

    Subclass per task and register in `ADAPTERS`. The common loop drives:
      1. Build robot cfg (replace prim_path is done in build_scene).
      2. Instantiate adapter, call setup() once after sim.reset() + warmup.
      3. Per decimation tick: compute_obs() -> policy -> apply_action().
      4. Per N steps: maybe_reset() to recycle fallen agents.
    """

    # ----- per-task constants (override in subclass) -----
    name: str = "<override>"
    # `experiment_name` matches the rsl_rl PPO runner cfg; used to
    # auto-resolve the latest checkpoint when --checkpoint is omitted.
    experiment_name: str = "<override>"
    obs_dim: int = 0
    action_dim: int = 0
    hidden_dims: List[int] = []
    activation: str = "elu"
    sim_dt: float = 1.0 / 200.0
    decimation: int = 4
    spacing: float = 2.0
    cam_alpha: float = 0.02

    def __init__(self, num_robots: int, device: str):
        self.num_robots = num_robots
        self.device = device

    # ----- robot config -----

    def build_robot_cfg(self):
        """Return ArticulationCfg (prim_path will be replaced by build_scene)."""
        raise NotImplementedError

    # ----- one-time setup after sim.reset() + warmup step -----

    def setup(self, robot, env_origins: torch.Tensor) -> None:
        """Cache references that don't change per step (default joint pose,
        target vectors, etc.). Default: no-op."""

    # ----- per-step API -----

    def compute_obs(self, robot, last_actions: torch.Tensor) -> torch.Tensor:
        """Return [N, obs_dim] observation tensor matching the trained
        policy's expected layout."""
        raise NotImplementedError

    def apply_action(self, robot, action: torch.Tensor) -> None:
        """Set joint target on the articulation and write to sim."""
        raise NotImplementedError

    # ----- optional auto-reset -----

    def maybe_reset(self, robot, last_actions: torch.Tensor) -> torch.Tensor:
        """Recycle agents that fell / drifted out. Return updated last_actions
        (zeroed for reset envs). Default: no reset."""
        return last_actions


ADAPTERS: Dict[str, Type[TaskAdapter]] = {}


def register_adapter(name: str):
    """Decorator: register a TaskAdapter subclass under the given task name."""
    def _wrap(cls: Type[TaskAdapter]) -> Type[TaskAdapter]:
        ADAPTERS[name] = cls
        return cls
    return _wrap


# ---------------------------------------------------------------------------
# Loop driver
# ---------------------------------------------------------------------------


def run_replay(args_cli: argparse.Namespace, simulation_app, adapter: TaskAdapter) -> None:
    """Common boot + loop driver. Writes one render per sim step so NVST has a
    fresh frame, lerps the camera, and recycles fallen agents periodically."""
    import isaaclab.sim as sim_utils

    sim_cfg = sim_utils.SimulationCfg(dt=adapter.sim_dt, device=args_cli.device)
    sim = sim_utils.SimulationContext(sim_cfg)
    # Wide initial shot; CameraFollower takes over after first frame.
    sim.set_camera_view(eye=(15.0, 15.0, 12.0), target=(0.0, 0.0, 0.0))

    spacing = args_cli.spacing if args_cli.spacing is not None else adapter.spacing
    N = adapter.num_robots
    robot, env_origins = build_scene(N, spacing, adapter.build_robot_cfg(),
                                     sim.device)

    checkpoint_path = resolve_checkpoint(args_cli, adapter.experiment_name)
    policy = load_actor_mlp(
        checkpoint_path, torch.device(sim.device),
        adapter.obs_dim, adapter.action_dim, adapter.hidden_dims,
        adapter.activation,
    )

    sim.reset()
    sim_dt = sim.get_physics_dt()
    # Warmup: populate Warp buffer timestamps before reading.
    sim.step()
    robot.update(sim_dt)

    adapter.setup(robot, env_origins)

    last_actions = torch.zeros(N, adapter.action_dim, device=sim.device)
    obs = adapter.compute_obs(robot, last_actions)
    assert obs.shape == (N, adapter.obs_dim), \
        f"[{adapter.name}] expected ({N}, {adapter.obs_dim}), got {tuple(obs.shape)}"
    print(f"[INFO] First-frame obs OK: {tuple(obs.shape)}")

    cam = CameraFollower(N, spacing, sim.device, alpha=adapter.cam_alpha)
    action = torch.zeros(N, adapter.action_dim, device=sim.device)
    step_count = 0
    print(f"[INFO] Inference loop started "
          f"(task={adapter.name}, N={N}, decimation={adapter.decimation}). "
          f"Ctrl+C to exit.")
    while simulation_app.is_running():
        if step_count % adapter.decimation == 0:
            obs = adapter.compute_obs(robot, last_actions)
            with torch.inference_mode():
                action = policy(obs)
            # Clone out of inference_mode so maybe_reset() can do
            # `last_actions[env_ids] = 0.0` without "Inplace update to
            # inference tensor outside InferenceMode" RuntimeError.
            last_actions = action.clone()
        adapter.apply_action(robot, action)
        sim.step()
        robot.update(sim_dt)
        step_count += 1
        # Recycle fallen agents every 30 sim steps (~6Hz @ 200Hz / 4Hz @ 120Hz).
        if step_count % 30 == 0:
            last_actions = adapter.maybe_reset(robot, last_actions)
        cam.tick(robot, sim)
