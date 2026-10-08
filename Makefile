.DEFAULT_GOAL := up

DJONEHUB_PORT ?= 7575
export DJONEHUB_PORT
export DJONEHUB_USB_ID

DOCKER_SCRIPT := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))scripts/docker.sh

.PHONY: up start stop down logs status doctor help

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

help:
	@printf '%s\n' \
	  'make / make up       重新构建、连接 USB 并启动' \
	  'make stop            停止并归还 USB' \
	  'make logs            查看实时日志' \
	  'make status          查看运行状态' \
	  'make doctor          检查 Docker 和 USB' \
	  'make up DJONEHUB_PORT=7576  使用其他端口' \
	  'Webhook 配置：复制 .env.example 为 .env，填写地址后 make up'
