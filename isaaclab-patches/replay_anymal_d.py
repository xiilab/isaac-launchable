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

"""Rest everything follows."""

import math
from typing import Tuple

import numpy as np
import torch

import isaaclab.sim as sim_utils
from isaaclab.assets import Articulation
from isaaclab_assets.robots.anymal import ANYMAL_D_CFG  # isort:skip

import isaacsim.core.utils.prims as prim_utils


def prim_utils_create_xform(prim_path: str, translation):
    """Create an Xform prim if it doesn't exist (Isaac Sim 6.0 helper)."""
    prim_utils.create_prim(prim_path, "Xform", translation=tuple(translation))


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


if __name__ == "__main__":
    main()
    simulation_app.close()
