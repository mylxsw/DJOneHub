.DEFAULT_GOAL := up

DJONEHUB_PORT ?= 7575
export DJONEHUB_PORT
export DJONEHUB_USB_ID
export PATH

DOCKER_SCRIPT := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))scripts/docker.sh

.PHONY: up start stop down logs status doctor help autostart autostart-stop autostart-status server-power

up start:
	@"$(DOCKER_SCRIPT)" up

stop down:
	@"$(DOCKER_SCRIPT)" stop

logs:
	@"$(DOCKER_SCRIPT)" logs

status:
	@"$(DOCKER_SCRIPT)" status

doctor:
	@"$(DOCKER_SCRIPT)" doctor

autostart:
	@/bin/bash "$(dir $(DOCKER_SCRIPT))autostart.sh" install

autostart-stop:
	@/bin/bash "$(dir $(DOCKER_SCRIPT))autostart.sh" uninstall

autostart-status:
	@/bin/bash "$(dir $(DOCKER_SCRIPT))autostart.sh" status

server-power:
	@test "$$(uname -s)" = Darwin || { echo '此电源配置仅适用于 Mac。'; exit 1; }
	@sudo /usr/bin/pmset -c sleep 0 autorestart 1

help:
	@printf '%s\n' \
	  'make / make up       重新构建、连接 USB 并启动' \
	  'make stop            停止并归还 USB' \
	  'make logs            查看实时日志' \
	  'make status          查看运行状态' \
	  'make doctor          检查 Docker 和 USB' \
	  'make autostart       安装 Mac 自动恢复任务（需用户登录，支持自动登录）' \
	  'make autostart-stop  移除自动恢复任务' \
	  'make autostart-status  查看自动恢复任务' \
	  'make server-power    配置市电不休眠、断电恢复后开机（需管理员认证）' \
	  'make up DJONEHUB_PORT=7576  使用其他端口' \
	  'Webhook 配置：复制 .env.example 为 .env，填写地址后 make up'
