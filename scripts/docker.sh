#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
STATE_DIR="$ROOT_DIR/dist/docker"
USB_STATE="$STATE_DIR/usb-id"
cd "$ROOT_DIR"

compose() { docker compose -f "$ROOT_DIR/compose.yaml" "$@"; }
fail() { printf '%s\n' "$*" >&2; exit 1; }

check_engine() {
  command -v docker >/dev/null || fail '请先安装并启动 OrbStack（Mac）或 Docker Engine（Linux）。'
  docker info >/dev/null 2>&1 || fail 'Docker 尚未运行，请先启动它。'
  docker compose version >/dev/null || fail '需要 Docker Compose v2 或更新版本。'
  if [[ $(uname -s) == Darwin ]]; then
    command -v orb >/dev/null || fail 'Mac USB 运行需要支持 USB 直通的 OrbStack。'
    [[ $(docker context show) == orbstack ]] || fail '请先执行 docker context use orbstack，再运行此命令。'
    orb usb list >/dev/null || fail '当前 OrbStack 不支持 USB 直通，请更新 OrbStack。'
  elif [[ $(uname -s) != Linux ]]; then
    fail '当前启动脚本支持 macOS + OrbStack，或 Linux + 本地 Docker。'
  fi
}

choose_usb() {
  local devices count
  devices=$(orb usb list | awk '$2 == "2ca3:4006" { print $1 }')
  if [[ -n ${DJONEHUB_USB_ID:-} ]]; then
    printf '%s\n' "$devices" | awk -v id="$DJONEHUB_USB_ID" '$0 == id { found=1 } END { exit !found }' \
      || fail "未找到指定的大疆 USB 设备：$DJONEHUB_USB_ID"
    USB_ID=$DJONEHUB_USB_ID
  else
    count=$(printf '%s\n' "$devices" | awk 'NF { n++ } END { print n+0 }')
    [[ $count -gt 0 ]] || fail '未找到大疆设备 2ca3:4006，请检查 USB 线缆和连接。'
    [[ $count -eq 1 ]] || fail '检测到多块大疆设备，请用 make up DJONEHUB_USB_ID=设备ID 指定一块。'
    USB_ID=$devices
  fi
}

attach_usb() {
  mkdir -p "$STATE_DIR"
  local details
  details=$(orb usb info "$USB_ID")
  if printf '%s\n' "$details" | usb_is_detached; then
    printf '正在把 USB %s 交给容器使用（停止后归还 macOS）…\n' "$USB_ID"
    orb usb attach "$USB_ID"
    printf '%s\n' "$USB_ID" > "$USB_STATE"
  else
    printf 'USB %s 已交给 OrbStack，继续启动。\n' "$USB_ID"
  fi
}

# Use standard awk instead of requiring ripgrep on the user's machine.
usb_is_detached() { awk '/^Machine: Not attached$/ { found=1 } END { exit !found }'; }

return_usb() {
  [[ -f "$USB_STATE" ]] || return 0
  local id
  id=$(cat "$USB_STATE")
  if orb usb list | awk -v id="$id" '$1 == id { found=1 } END { exit !found }'; then
    if orb usb info "$id" | usb_is_detached; then
      rm -f "$USB_STATE"
    else
      orb usb detach "$id"
      rm -f "$USB_STATE"
    fi
  else
    rm -f "$USB_STATE"
    printf 'USB 已拔出；再次插入后如仍被占用，可在 OrbStack 的 Devices 中选择 Detach。\n'
  fi
}

case ${1:-up} in
  up|start)
    check_engine
    if [[ $(uname -s) == Darwin ]]; then choose_usb; fi
    # Build first: a compilation failure leaves the existing service and USB alone.
    compose build
    if [[ $(uname -s) == Darwin ]]; then attach_usb; fi
    if ! compose up -d --force-recreate --wait --wait-timeout 60; then
      compose logs --tail=30 || true
      compose down
      if [[ $(uname -s) == Darwin && -f "$USB_STATE" ]]; then
        printf 'USB 连接暂未恢复，正在重新连接后重试…\n'
        return_usb
        attach_usb
        if ! compose up -d --force-recreate --wait --wait-timeout 60; then
          compose logs --tail=30 || true
          compose down
          return_usb
          fail '启动失败，请根据上面的日志检查设备和端口。'
        fi
      else
        fail '启动失败，请根据上面的日志检查设备和端口。'
      fi
    fi
    # HTTP health and hardware readiness are different checks.
    health=$(compose exec -T djonehub curl --noproxy '*' -fsS --max-time 8 http://127.0.0.1:7575/api/health)
    if ! printf '%s\n' "$health" | awk '/"discovery_error":""/ && /"vendor_id":"2ca3"/ { found=1 } END { exit !found }'; then
      compose logs --tail=20
      fail '网页已启动，但 USB AT 尚未连接。修复设备连接后再次执行 make up。'
    fi
    printf '\nDJOneHub 已启动，USB AT 已连接，端口绑定到 0.0.0.0:%s：\n本机：http://127.0.0.1:%s\n局域网：http://这台电脑的局域网IP:%s\n' \
      "${DJONEHUB_PORT:-7575}" "${DJONEHUB_PORT:-7575}" "${DJONEHUB_PORT:-7575}"
    ;;
  stop|down)
    check_engine
    compose down
    if [[ $(uname -s) == Darwin ]]; then return_usb; fi
    printf '已停止，保存的本地资料保留在 Docker 数据卷中。\n'
    ;;
  logs)
    check_engine
    compose logs -f --tail=100
    ;;
  status)
    check_engine
    compose ps
    compose exec -T djonehub curl --noproxy '*' -fsS --max-time 8 http://127.0.0.1:7575/api/health
    ;;
  doctor)
    check_engine
    if [[ $(uname -s) == Darwin ]]; then orb usb list; fi
    compose ps
    ;;
  *)
    fail '用法：./scripts/docker.sh [up|stop|logs|status|doctor]'
    ;;
esac
