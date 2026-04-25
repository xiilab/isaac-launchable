"""Replay a trained Anymal-D rsl_rl policy via Isaac Sim livestream.

Standalone usage:
    cd /workspace/isaaclab
    ./isaaclab.sh -p /workspace/data/replay/replay_anymal_d.py \
        --checkpoint /workspace/isaaclab/logs/rsl_rl/anymal_d_flat/<run>/model_X.pt \
        --num_robots 7 \
        --velocity_x 0.5 \
        --livestream 2 \
        --kit_args "--/exts/omni.kit.livestream.app/primaryStream/publicIp=$ISAACSIM_HOST --/exts/omni.kit.livestream.app/primaryStream/streamPort=$ISAACSIM_STREAM_PORT --/exts/omni.kit.livestream.app/primaryStream/signalPort=$ISAACSIM_SIGNAL_PORT"

For tutorial-style invocation (`--task Isaac-Velocity-Flat-Anymal-D-v0`), use
`play_livestream.py` which dispatches to the registered `AnymalDAdapter`.

Bypasses IsaacLab #5364 the same way replay_ant.py does — see
replay_common.py docstring.
"""
from __future__ import annotations

import argparse

import torch
import warp as wp

from isaaclab.app import AppLauncher

import replay_common
from replay_common import TaskAdapter, register_adapter


# ---------------------------------------------------------------------------
# Constants from Isaac-Velocity-Flat-Anymal-D-v0
#  (anymal_d_flat_env_cfg.py + anymal_d_rsl_rl_cfg.py — verified at IsaacLab
#  1.5/2.0; trained with sim 200Hz / control 50Hz / decimation=4).
# ---------------------------------------------------------------------------

ACTION_SCALE = 0.5  # JointPositionActionCfg(scale=0.5, use_default_offset=True)


# ---------------------------------------------------------------------------
# TaskAdapter
# ---------------------------------------------------------------------------


@register_adapter("Isaac-Velocity-Flat-Anymal-D-v0")
class AnymalDAdapter(TaskAdapter):
    name = "Isaac-Velocity-Flat-Anymal-D-v0"
    experiment_name = "anymal_d_flat"
    obs_dim = 48
    action_dim = 12
    hidden_dims = [128, 128, 128]
    activation = "elu"
    sim_dt = 1.0 / 200.0
    decimation = 4
    spacing = 2.0
    cam_alpha = 0.02

    def __init__(self, num_robots: int, device: str,
                 velocity_x: float = 0.5,
                 velocity_y: float = 0.0,
                 velocity_yaw: float = 0.0):
        super().__init__(num_robots, device)
        self._cmd = (velocity_x, velocity_y, velocity_yaw)

    def build_robot_cfg(self):
        from isaaclab_assets.robots.anymal import ANYMAL_D_CFG  # isort:skip
        return ANYMAL_D_CFG

    def setup(self, robot, env_origins: torch.Tensor) -> None:
        N = self.num_robots
        # Velocity command broadcast to per-env tensor.
        self._velocity_cmd = torch.tensor(
            list(self._cmd), device=self.device,
        ).expand(N, 3).contiguous()
        print(f"[INFO] velocity_cmd: {self._velocity_cmd[0].tolist()}")

        # Snapshot default joint pose for action offsetting.
        self._default_joint_pos = wp.to_torch(robot.data.default_joint_pos).clone()

    def compute_obs(self, robot, last_actions: torch.Tensor) -> torch.Tensor:
        """Build the 48-dim observation matching Isaac-Velocity-Flat-Anymal-D-v0.

        Layout:
            [0:3]   base_lin_vel (in base frame)
            [3:6]   base_ang_vel (in base frame)
            [6:9]   projected_gravity (in base frame; default down = (0,0,-1))
            [9:12]  velocity_commands (vx, vy, yaw_rate)
            [12:24] joint_pos - default_joint_pos (relative)
            [24:36] joint_vel (absolute)
            [36:48] last_actions (12)
        """
        base_lin_vel = wp.to_torch(robot.data.root_lin_vel_b)
        base_ang_vel = wp.to_torch(robot.data.root_ang_vel_b)
        projected_gravity = wp.to_torch(robot.data.projected_gravity_b)
        joint_pos = wp.to_torch(robot.data.joint_pos)
        default_joint_pos = wp.to_torch(robot.data.default_joint_pos)
        joint_pos_rel = joint_pos - default_joint_pos
        joint_vel = wp.to_torch(robot.data.joint_vel)
        return torch.cat(
            [
                base_lin_vel,
                base_ang_vel,
                projected_gravity,
                self._velocity_cmd,
                joint_pos_rel,
                joint_vel,
                last_actions,
            ],
            dim=-1,
        )

    def apply_action(self, robot, action: torch.Tensor) -> None:
        """Joint position target: target = default_joint_pos + scale * action.

        The Velocity-Flat-Anymal-D-v0 task uses
            JointPositionActionCfg(scale=0.5, use_default_offset=True)
        so the actuator target is default_joint_pos + scale * action.
        """
        target = self._default_joint_pos + ACTION_SCALE * action
        robot.set_joint_position_target(target)
        robot.write_data_to_sim()


# ---------------------------------------------------------------------------
# Standalone entry — preserves --velocity_{x,y,yaw} flags users have been
# typing for months. play_livestream.py uses defaults.
# ---------------------------------------------------------------------------


def _main_standalone():
    parser = argparse.ArgumentParser(description="Replay a trained Anymal-D rsl_rl policy.")
    replay_common.add_common_args(parser)
    parser.add_argument("--velocity_x", type=float, default=0.5,
                        help="Forward velocity command (m/s)")
    parser.add_argument("--velocity_y", type=float, default=0.0,
                        help="Lateral velocity command (m/s)")
    parser.add_argument("--velocity_yaw", type=float, default=0.0,
                        help="Yaw rate command (rad/s)")
    AppLauncher.add_app_launcher_args(parser)
    parser.set_defaults(visualizer=["kit"])
    args_cli = parser.parse_args()

    replay_common.inject_livestream_kit_args(args_cli)
    app_launcher = AppLauncher(args_cli)
    simulation_app = app_launcher.app

    N = replay_common.resolve_num_robots(args_cli)
    adapter = AnymalDAdapter(
        num_robots=N, device=args_cli.device,
        velocity_x=args_cli.velocity_x,
        velocity_y=args_cli.velocity_y,
        velocity_yaw=args_cli.velocity_yaw,
    )
    replay_common.run_replay(args_cli, simulation_app, adapter)
    simulation_app.close()


if __name__ == "__main__":
    _main_standalone()
