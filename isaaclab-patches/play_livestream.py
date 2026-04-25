"""Drop-in replacement for IsaacLab `play.py` that works in K8s livestream.

The official `scripts/reinforcement_learning/rsl_rl/play.py` constructs the
env via `gym.make` → `DirectRLEnv` / `ManagerBasedRLEnv` →
`InteractiveScene._setup_scene`, which doesn't reach the NVST capture path
in livestream mode (IsaacLab #5364). USD prims are created and the viewport
binds, but the Hydra-side dynamic transforms never produce frames — verified
across single-env runs, every `clone_in_fabric` / `replicate_physics` /
`readTransformsFromFabricInRenderDelegate` combination, and explicit
`env.unwrapped.sim.render()` per loop iteration.

This wrapper instead dispatches to a per-task `TaskAdapter` (registered via
`@replay_common.register_adapter`) that constructs the scene the way
`quadrupeds.py` does — manual `prim_utils.create_prim` + `Articulation`
regex + `SimulationContext` directly. NVST captures fine on this path.

Usage matches `play.py` so tutorial commands work with minimal edits:

    ./isaaclab.sh -p /workspace/data/replay/play_livestream.py \
        --task Isaac-Ant-Direct-v0 \
        --num_envs 64 \
        --checkpoint /workspace/isaaclab/logs/rsl_rl/ant_direct/<run>/model_999.pt \
        --livestream 2 \
        --kit_args "--/exts/omni.kit.livestream.app/primaryStream/publicIp=$ISAACSIM_HOST --/exts/omni.kit.livestream.app/primaryStream/streamPort=$ISAACSIM_STREAM_PORT --/exts/omni.kit.livestream.app/primaryStream/signalPort=$ISAACSIM_SIGNAL_PORT"

Adding a new task: write `TaskAdapter` subclass (~150 LOC, mirrors
`replay_ant.py`) defining obs/action/network and decorate it with
`@register_adapter("Isaac-<Task>-v0")`. Import it below so registration
fires.
"""
from __future__ import annotations

import argparse
import sys

from isaaclab.app import AppLauncher

import replay_common

# Importing each adapter module triggers `@register_adapter` decorators.
# Add a new task by importing its module here.
import replay_ant  # noqa: F401  -> Isaac-Ant-Direct-v0
import replay_anymal_d  # noqa: F401  -> Isaac-Velocity-Flat-Anymal-D-v0


def _build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Tutorial-compatible replay of a trained rsl_rl policy "
                    "with Isaac Sim livestream (K8s-friendly; bypasses #5364)."
    )
    parser.add_argument("--task", type=str, required=True,
                        help="Task name, e.g. Isaac-Ant-Direct-v0. Must be "
                             "registered in replay_common.ADAPTERS.")
    # play.py CLI parity (all accepted; some are no-ops here).
    parser.add_argument("--video", action="store_true", default=False,
                        help="(Accepted for play.py CLI parity; ignored.)")
    parser.add_argument("--video_length", type=int, default=200,
                        help="(Accepted for play.py CLI parity; ignored.)")
    parser.add_argument("--disable_fabric", action="store_true", default=False,
                        help="(Accepted for play.py CLI parity; ignored — "
                             "this script never goes through fabric clones.)")
    parser.add_argument("--agent", type=str, default="rsl_rl_cfg_entry_point",
                        help="(Accepted for play.py CLI parity; ignored.)")
    parser.add_argument("--use_pretrained_checkpoint", action="store_true",
                        help="(Accepted for play.py CLI parity; ignored.)")
    replay_common.add_common_args(parser)
    AppLauncher.add_app_launcher_args(parser)
    parser.set_defaults(visualizer=["kit"])
    return parser


def main() -> None:
    parser = _build_parser()
    args_cli = parser.parse_args()

    if args_cli.task not in replay_common.ADAPTERS:
        registered = sorted(replay_common.ADAPTERS.keys())
        print(f"[ERROR] task '{args_cli.task}' is not registered.")
        print(f"[ERROR] Registered tasks: {registered}")
        print("[ERROR] To add a new task, write a TaskAdapter subclass and "
              "import it from play_livestream.py. See replay_ant.py.")
        sys.exit(2)

    adapter_cls = replay_common.ADAPTERS[args_cli.task]
    app_launcher = AppLauncher(args_cli)
    simulation_app = app_launcher.app

    N = replay_common.resolve_num_robots(args_cli)
    adapter = adapter_cls(num_robots=N, device=args_cli.device)
    replay_common.run_replay(args_cli, simulation_app, adapter)
    simulation_app.close()


if __name__ == "__main__":
    main()
