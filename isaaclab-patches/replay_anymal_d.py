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
