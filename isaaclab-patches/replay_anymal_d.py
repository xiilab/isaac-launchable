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


if __name__ == "__main__":
    main()
    simulation_app.close()
