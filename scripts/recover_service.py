"""Bound each launchd recovery attempt; leave retries to StartInterval."""

import os
from pathlib import Path
import signal
import subprocess

root = Path(__file__).resolve().parent.parent
log = root / "dist/docker/autostart.log"
if log.exists() and log.stat().st_size > 5 * 1024 * 1024:
    log.replace(log.with_suffix(".log.1"))
    log.touch(mode=0o600)

process = subprocess.Popen(
    ["/bin/bash", str(root / "scripts/docker.sh"), "recover"],
    start_new_session=True,
)


def stop_process_group():
    if process.poll() is not None:
        return
    os.killpg(process.pid, signal.SIGTERM)
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGKILL)
        process.wait()


def shutdown(signum, _frame):
    stop_process_group()
    raise SystemExit(128 + signum)


signal.signal(signal.SIGTERM, shutdown)
signal.signal(signal.SIGINT, shutdown)
try:
    raise SystemExit(process.wait(timeout=60))
except subprocess.TimeoutExpired:
    print("自动恢复超过 60 秒，本次停止，下一轮重试。", flush=True)
    stop_process_group()
    raise SystemExit(1)
