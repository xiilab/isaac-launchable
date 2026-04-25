"""Replay a trained Isaac-Ant-Direct rsl_rl policy via Isaac Sim livestream.

Standalone usage:
    cd /workspace/isaaclab
    ./isaaclab.sh -p /workspace/data/replay/replay_ant.py \
        --checkpoint /workspace/isaaclab/logs/rsl_rl/ant_direct/<run>/model_999.pt \
        --num_robots 64 \
        --livestream 2 \
        --kit_args "--/exts/omni.kit.livestream.app/primaryStream/publicIp=$ISAACSIM_HOST --/exts/omni.kit.livestream.app/primaryStream/streamPort=$ISAACSIM_STREAM_PORT --/exts/omni.kit.livestream.app/primaryStream/signalPort=$ISAACSIM_SIGNAL_PORT"

For tutorial-style invocation (`--task Isaac-Ant-Direct-v0`), use
`play_livestream.py` which dispatches to the registered `AntAdapter` below.

Bypasses IsaacLab #5364: every `gym.make` path leaves the NVST capture viewport
black; SimulationContext + manual prim_utils.create_prim does not. See
replay_common.py docstring for the full isolation matrix.
"""
from __future__ import annotations

import argparse
from typing import Tuple

import torch
import warp as wp

from isaaclab.app import AppLauncher

import replay_common
from replay_common import TaskAdapter, register_adapter


# ---------------------------------------------------------------------------
# Constants from Isaac-Ant-Direct-v0 (ant_env_cfg.py + locomotion_env.py +
# rsl_rl_ppo_cfg.py — verified at IsaacLab 1.5/2.0).
# ---------------------------------------------------------------------------

JOINT_GEARS = [15.0] * 8
ACTION_SCALE = 0.5
ANGULAR_VELOCITY_SCALE = 1.0
DOF_VEL_SCALE = 0.2
TERMINATION_HEIGHT = 0.31


# ---------------------------------------------------------------------------
# TaskAdapter
# ---------------------------------------------------------------------------


@register_adapter("Isaac-Ant-Direct-v0")
class AntAdapter(TaskAdapter):
    name = "Isaac-Ant-Direct-v0"
    obs_dim = 36
    action_dim = 8
    hidden_dims = [400, 200, 100]
    activation = "elu"
    sim_dt = 1.0 / 120.0
    decimation = 2
    spacing = 4.0
    cam_alpha = 0.02

    def build_robot_cfg(self):
        from isaaclab_assets.robots.ant import ANT_CFG
        return ANT_CFG

    def setup(self, robot, env_origins: torch.Tensor) -> None:
        from isaaclab.utils.math import quat_conjugate

        device = self.device
        N = self.num_robots
        # LocomotionEnv basis vectors and start-rotation conjugate.
        start_rot = torch.tensor([0.0, 0.0, 0.0, 1.0], device=device)
        self._inv_start_rot = quat_conjugate(start_rot).repeat(N, 1)
        self._basis_vec0 = torch.tensor([1.0, 0.0, 0.0], device=device).repeat(N, 1)
        self._basis_vec1 = torch.tensor([0.0, 0.0, 1.0], device=device).repeat(N, 1)
        # Far-away forward target so heading/angle observations point +X.
        self._targets = (
            torch.tensor([1000.0, 0.0, 0.0], device=device).repeat(N, 1) + env_origins
        )
        self._env_origins = env_origins

        # Joint indices for effort target. find_joints(".*") = all.
        self._joint_dof_idx, _ = robot.find_joints(".*")
        self._joint_gears = torch.tensor(JOINT_GEARS, dtype=torch.float32, device=device)

        # Cached defaults for auto-reset.
        self._default_joint_pos = wp.to_torch(robot.data.default_joint_pos).clone()
        self._default_joint_vel = wp.to_torch(robot.data.default_joint_vel).clone()
        self._default_root_pose = wp.to_torch(robot.data.default_root_pose).clone()
        self._default_root_vel = wp.to_torch(robot.data.default_root_vel).clone()

        # Soft joint limits (constant across envs for an articulation).
        soft_limits = wp.to_torch(robot.data.soft_joint_pos_limits)
        self._dof_lower = soft_limits[0, :, 0]
        self._dof_upper = soft_limits[0, :, 1]

    def compute_obs(self, robot, last_actions: torch.Tensor) -> torch.Tensor:
        """Build the 36-dim observation matching LocomotionEnv._get_observations.

        Layout:
            [0]      torso z (height)
            [1:4]    vel_loc (lin vel in torso frame)
            [4:7]    angvel_loc * 1.0
            [7]      yaw (normalized)
            [8]      roll (normalized)
            [9]      angle_to_target (normalized)
            [10]     up_proj
            [11]     heading_proj
            [12:20]  dof_pos_scaled (8 joints in [-1,1])
            [20:28]  dof_vel * 0.2
            [28:36]  last_actions (8)
        """
        from isaaclab.utils.math import (
            euler_xyz_from_quat,
            normalize,
            quat_apply,
            quat_apply_inverse,
            quat_mul,
            scale_transform,
        )

        N = last_actions.shape[0]
        torso_pos = wp.to_torch(robot.data.root_pos_w)
        torso_rot = wp.to_torch(robot.data.root_quat_w)
        lin_vel_w = wp.to_torch(robot.data.root_lin_vel_w)
        ang_vel_w = wp.to_torch(robot.data.root_ang_vel_w)
        dof_pos = wp.to_torch(robot.data.joint_pos)
        dof_vel = wp.to_torch(robot.data.joint_vel)

        to_target = self._targets - torso_pos
        to_target[:, 2] = 0.0
        target_dirs = normalize(to_target)

        torso_quat = quat_mul(torso_rot, self._inv_start_rot)
        up_vec = quat_apply(torso_quat, self._basis_vec1).view(N, 3)
        heading_vec = quat_apply(torso_quat, self._basis_vec0).view(N, 3)
        up_proj = up_vec[:, 2]
        heading_proj = torch.bmm(
            heading_vec.view(N, 1, 3), target_dirs.view(N, 3, 1)
        ).view(N)

        vel_loc = quat_apply_inverse(torso_quat, lin_vel_w)
        angvel_loc = quat_apply_inverse(torso_quat, ang_vel_w)
        roll, _pitch, yaw = euler_xyz_from_quat(torso_quat)
        walk_target_angle = torch.atan2(
            self._targets[:, 1] - torso_pos[:, 1],
            self._targets[:, 0] - torso_pos[:, 0],
        )
        angle_to_target = walk_target_angle - yaw

        dof_pos_scaled = scale_transform(dof_pos, self._dof_lower, self._dof_upper)

        return torch.cat(
            [
                torso_pos[:, 2:3],
                vel_loc,
                angvel_loc * ANGULAR_VELOCITY_SCALE,
                _normalize_angle(yaw).unsqueeze(-1),
                _normalize_angle(roll).unsqueeze(-1),
                _normalize_angle(angle_to_target).unsqueeze(-1),
                up_proj.unsqueeze(-1),
                heading_proj.unsqueeze(-1),
                dof_pos_scaled,
                dof_vel * DOF_VEL_SCALE,
                last_actions,
            ],
            dim=-1,
        )

    def apply_action(self, robot, action: torch.Tensor) -> None:
        """Effort target as in LocomotionEnv._apply_action."""
        forces = ACTION_SCALE * self._joint_gears * torch.clamp(action, -1.0, 1.0)
        robot.set_joint_effort_target_index(target=forces, joint_ids=self._joint_dof_idx)
        robot.write_data_to_sim()

    def maybe_reset(self, robot, last_actions: torch.Tensor) -> torch.Tensor:
        """Auto-reset ants whose torso fell below TERMINATION_HEIGHT."""
        torso_z = wp.to_torch(robot.data.root_pos_w)[:, 2]
        fallen = torso_z < TERMINATION_HEIGHT
        if not fallen.any():
            return last_actions
        env_ids = torch.nonzero(fallen, as_tuple=False).flatten()
        pose = self._default_root_pose[env_ids].clone()
        pose[:, :3] += self._env_origins[env_ids]
        robot.write_root_pose_to_sim_index(root_pose=pose, env_ids=env_ids)
        robot.write_root_velocity_to_sim_index(
            root_velocity=self._default_root_vel[env_ids], env_ids=env_ids
        )
        robot.write_joint_position_to_sim_index(
            position=self._default_joint_pos[env_ids], env_ids=env_ids
        )
        robot.write_joint_velocity_to_sim_index(
            velocity=self._default_joint_vel[env_ids], env_ids=env_ids
        )
        last_actions[env_ids] = 0.0
        return last_actions


def _normalize_angle(x: torch.Tensor) -> torch.Tensor:
    return torch.atan2(torch.sin(x), torch.cos(x))


# ---------------------------------------------------------------------------
# Standalone entry (the script can be invoked directly OR via
# play_livestream.py — this guard runs only in the standalone case).
# ---------------------------------------------------------------------------


def _main_standalone():
    parser = argparse.ArgumentParser(description="Replay a trained Ant rsl_rl policy.")
    replay_common.add_common_args(parser)
    AppLauncher.add_app_launcher_args(parser)
    parser.set_defaults(visualizer=["kit"])
    args_cli = parser.parse_args()

    app_launcher = AppLauncher(args_cli)
    simulation_app = app_launcher.app

    N = replay_common.resolve_num_robots(args_cli)
    adapter = AntAdapter(num_robots=N, device=args_cli.device)
    replay_common.run_replay(args_cli, simulation_app, adapter)
    simulation_app.close()


if __name__ == "__main__":
    _main_standalone()
