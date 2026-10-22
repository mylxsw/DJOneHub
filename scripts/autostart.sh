#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
STATE_DIR="$ROOT_DIR/dist/docker"
LABEL=in.djonehub.usb-recovery
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
DOMAIN="gui/$(id -u)"
LOG="$STATE_DIR/autostart.log"

[[ $(uname -s) == Darwin ]] || { printf '自动恢复安装目前适用于 macOS + OrbStack。\n' >&2; exit 1; }

case ${1:-status} in
  install)
    command -v python3 >/dev/null || { printf '生成启动配置需要 python3。\n' >&2; exit 1; }
    command -v orb >/dev/null
    docker --context orbstack image inspect djonehub:local >/dev/null 2>&1 \
      || { printf '请先执行 make up 完成首次构建。\n' >&2; exit 1; }
    launchctl print "$DOMAIN" >/dev/null 2>&1 \
      || { printf '需要在 macOS 用户登录会话中安装。\n' >&2; exit 1; }
    mkdir -p "$(dirname "$PLIST")" "$STATE_DIR"
    # plistlib safely handles spaces, XML characters, and custom settings.
    python3 - "$PLIST" "$ROOT_DIR" "$LOG" "$LABEL" "$(command -v python3)" <<'PY'
import os, plistlib, shutil, sys
path, root, log, label, python = sys.argv[1:]
tool_dirs = [os.path.dirname(shutil.which(tool)) for tool in ("orb", "docker")]
tool_dirs += [os.path.dirname(python), os.path.expanduser("~/.orbstack/bin"),
              "/usr/local/bin", "/opt/homebrew/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"]
environment = {
    "PATH": ":".join(dict.fromkeys(tool_dirs)),
    "DOCKER_CONTEXT": "orbstack",
    "DJONEHUB_PORT": os.environ.get("DJONEHUB_PORT", "7575"),
    "DJONEHUB_USB_ID": os.environ.get("DJONEHUB_USB_ID", ""),
}
job = {
    "Label": label,
    "ProgramArguments": [python, root + "/scripts/recover_service.py"],
    "WorkingDirectory": root,
    "EnvironmentVariables": environment,
    "RunAtLoad": True,
    "StartInterval": 30,
    "StandardOutPath": log,
    "StandardErrorPath": log,
    "ProcessType": "Background",
}
with open(path, "wb") as f:
    plistlib.dump(job, f)
os.chmod(path, 0o600)
PY
    plutil -lint "$PLIST"
    launchctl bootout "$DOMAIN/$LABEL" 2>/dev/null || true
    rm -f "$STATE_DIR/paused"
    launchctl enable "$DOMAIN/$LABEL"
    launchctl bootstrap "$DOMAIN" "$PLIST"
    printf '已启用自动恢复，每 30 秒检查一次。\n配置：%s\n日志：%s\n' "$PLIST" "$LOG"
    printf '要实现无人值守重启，macOS 必须自动登录此用户；本命令不会修改登录设置。\n'
    ;;
  uninstall)
    launchctl bootout "$DOMAIN/$LABEL" 2>/dev/null || true
    rm -f "$PLIST"
    printf '已移除自动恢复任务，当前容器继续运行。\n'
    ;;
  status)
    if [[ ! -f "$PLIST" ]]; then
      printf '自动恢复任务尚未安装。\n'
      exit 0
    fi
    printf '配置：%s\n' "$PLIST"
    launchctl print "$DOMAIN/$LABEL" | awk 'substr($0, 1, 2) != "\t\t" && /state =|last exit code =|runs =|run interval =/ { print }'
    printf 'not running 表示两轮检查之间空闲，任务仍按 30 秒间隔运行。\n'
    if [[ -f "$STATE_DIR/paused" ]]; then printf '\n自动恢复已暂停；make up 恢复。\n'; fi
    printf '\n日志：%s\n' "$LOG"
    ;;
  *) printf '用法：./scripts/autostart.sh [install|uninstall|status]\n' >&2; exit 1 ;;
esac
