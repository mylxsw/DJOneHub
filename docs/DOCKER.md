# 用 Docker 开发和运行 DJOneHub

## Mac 上一条命令运行

当前方案让整个 DJOneHub 程序在 Linux 容器中运行，USB 由 OrbStack 直接交给 Linux，Mac 上无需编译或启动 Go 程序。

需要：

- 支持 `orb usb` 的新版 OrbStack（本机已验证 2.2.3）。
- Docker 当前使用 `orbstack` context。
- 已连接 USB `2ca3:4006` 的大疆第一代 4G 模块。

在项目目录执行：

```sh
make up
```

直接执行 `make` 也会重新构建并启动。

服务和 Docker 对外端口都绑定到 `0.0.0.0:7575`。本机打开 <http://127.0.0.1:7575>，局域网中的其他设备打开 `http://这台电脑的局域网IP:7575`。`0.0.0.0` 是监听地址，访问时使用本机地址或实际 IP。

这条命令会按顺序检查 Docker 和 USB、构建最新源码、连接 USB、替换旧容器，并检查网页服务和 USB AT 管理接口。第一次要下载 Go、Debian 和依赖；之后使用 Docker 构建缓存。修改 Go、网页 JS/CSS/HTML 后，再次执行同一条命令即可。构建失败时不会停止正在运行的旧版本。

如果容器启动超时，Mac 上的脚本会对本次接管的 USB 重新连接并重试一次。

日常操作：

```sh
make stop    # 停止容器，并把脚本接管的 USB 归还 macOS
make logs    # 看实时日志；Ctrl+C 只退出日志
make status  # 看容器和 USB 连接状态
make doctor  # 列出 OrbStack USB 和容器
```

容器在后台运行，关闭终端不会停止。停止命令保留 Docker 数据卷。`make help` 查看命令；Makefile 调用原来的 `scripts/docker.sh`，脚本入口仍可直接使用。

端口冲突时可以改端口；后续操作请使用相同设置：

```sh
make up DJONEHUB_PORT=7576
```

多块相同设备时，先执行 `orb usb list` 查看第一列 ID：

```sh
make up DJONEHUB_USB_ID=03100000
```

脚本需要一块目标设备。当前程序的 libusb 后端按 VID/PID 打开第一块匹配设备；使用多块同型号设备时，请确保只有目标模块交给 Linux。

## Mac 无屏服务器：重启后自动恢复

Docker 的 `restart: unless-stopped` 只恢复容器，不会执行 Mac 上的 USB 连接命令。电脑重启后即使网页能打开，也可能报 `DJI USB device is not connected`。

首次成功执行 `make up` 后，安装自动恢复任务：

```sh
make autostart
make autostart-status
```

任务使用 macOS LaunchAgent，在当前用户登录时自动加载，每 30 秒检查一次：

- OrbStack 未就绪时尝试 `orb start`，下一轮继续重试。
- 按 VID/PID `2ca3:4006` 查找当前 USB ID，并连接尚未交给 Linux 的设备。
- 容器停止或不存在时，使用已构建的本地镜像启动，不重新编译、不下载镜像。
- 健康状态正常时不重建或重启容器。USB 已连接但管理接口连续三次未恢复时，只重启 DJOneHub 容器。
- 每轮最多等待约 65 秒，超时结束本轮，后续继续检查。日志超过 5 MiB 时保留一份轮转文件。

`make stop` 会暂停自动恢复，暂停状态重启后仍保留；`make up` 恢复运行和自动检查。`make autostart-stop` 仅卸载后台任务，当前容器继续运行。

**无人值守重启还需要 macOS 自动登录安装任务的用户。** 用户任务在登录后运行，OrbStack 目前不支持直接从系统级 LaunchDaemon 在未登录状态下启动。仅安装此任务不能绕过登录要求。

一次性通过“系统设置 → 用户与群组 → 自动登录为”选择运行服务的账号，并在系统设置中输入账号密码。FileVault 开启时无法自动登录；本脚本不会更改自动登录或 FileVault 设置。设置完成后，普通重启应自动登录、启动 OrbStack、连接 USB，并恢复服务；真正的整机重启验收需要在方便中断服务时进行。

自定义设置需在安装时一起传入；修改后重新安装：

```sh
make autostart DJONEHUB_PORT=7576
# 多块同型号设备时指定目标；只有一块时保持自动查找，以适应拔插后 ID 变化。
make autostart DJONEHUB_USB_ID=03100000
```

安装和运行需要 `python3`，后台任务使用安装时记录的 Python 路径。任务、日志和暂停标记：

```text
~/Library/LaunchAgents/in.djonehub.usb-recovery.plist
项目目录/dist/docker/autostart.log
项目目录/dist/docker/paused
```

项目目录不能移动；移动后在新目录重新执行 `make autostart`。后台任务不会自动拉取代码，修改代码仍执行 `make up`。`.env` 保持在项目根目录，后台任务不会将 Webhook 地址或 Token 写入启动配置。

当前 Mac 使用市电的系统睡眠设为 1 分钟，断电恢复后自动开机未开启；长期服务器还应在一次性系统配置时处理。这些设置需要管理员权限，例如：

```sh
make server-power
```

它执行 `sudo pmset -c sleep 0 autorestart 1`，关闭市电供电时的系统自动睡眠，并开启断电恢复后的自动开机。需要在终端输入管理员密码。自动登录后可锁屏，避免手动注销用户会话。

参考：[OrbStack 未登录启动的官方说明](https://github.com/orbstack/orbstack/issues/1767)、[Apple 自动登录设置](https://support.apple.com/102316)、[Apple LaunchAgent 生命周期](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html)。

自动恢复脚本的隔离测试（不会操作真实 USB 或容器）：

```sh
python3 scripts/test_docker_recovery.py
```

## USB 与上网模式的区别

本次 Docker 配置以短信、eSIM 和设备状态管理为用途，保留模块当前模式，不自动切换到上网模式。

OrbStack 的专用 USB 直通会让模块暂时从 macOS 移走。短信、AT、SIM 状态和兼容 eUICC 的管理都在容器中完成；USB 网卡也由 Linux 接管。这个过程不会让 Mac 自动通过容器共享 4G 网络。

默认 Compose 使用独立网络。网络页面展示容器的网络接口、路由和流量，不能把 Docker 的虚拟 `eth0` 当成大疆网卡或 Mac 的 4G 流量。模块处于模式 0 时，没有 USB 上网接口是正常的。

如果需要让 Mac 自身通过大疆网卡上网，停止 Docker 并归还设备，再使用原有 macOS 启动方式。Linux 容器中的网卡启用、IP 分配和网络共享是独立的配置步骤。

切换 USB 模式或拔插后，如果页面没有重新连接，重新执行 `make up`。脚本会按当前 USB 清单重新连接；容器挂载整个 `/dev/bus/usb`，允许设备地址变化。停止后如设备仍归 Linux 所有，在 OrbStack 的 Devices 页面选择该设备并点 Detach，也可执行：

```sh
orb usb list
orb usb detach 03100000  # 使用当前清单里的 ID
```

不要同时运行原生 DJOneHub 和 Docker 版，以免争用同一个管理接口。

## Linux 主机

在设备直接插在本机的 Linux 上：

```sh
make up
# 或：
docker compose up -d --build --wait
```

USB 通过 `/dev/bus/usb` 和设备权限规则访问，不需要 `--privileged`。启动脚本不会自动配置蜂窝拨号或 DHCP。默认容器网络与宿主网络相互独立。

## 数据和演示

收到短信后可以通过 Webhook 转发：复制 `.env.example` 为 `.env`，填写 `DJONEHUB_WEBHOOK_URL`（可选 `DJONEHUB_WEBHOOK_TOKEN`），执行 `make up`。请求格式、重试和去重说明见 [短信 Webhook](WEBHOOK.md)。

Profile 的本地备注保存在 `djonehub_djonehub-data` 数据卷。短信收件箱仍遵循原项目的内存缓存方式，容器重建会清空本次收件箱。Docker 版默认关闭导入短信后的模块自动清理，保留模块里的短信以便重启后重新读取。手动清理按钮仍可使用。

无硬件演示（先停止真实运行，避免占用相同端口）：

```sh
docker build -t djonehub:local .
docker run --rm -p 0.0.0.0:7575:7575 djonehub:local -listen 0.0.0.0:7575 -demo
```

`/api/health` 的 `ok` 代表网页服务运行；应同时检查 `usb_device` 和 `discovery_error`。启动脚本会额外检查 USB AT 是否成功打开，不把演示模式视为硬件验证。

## 构建与测试

无需在 Mac 安装 Go/libusb 即可测试 Linux 构建：

```sh
docker build --target build -t djonehub:build .
docker run --rm djonehub:build go test ./...
```

原生 macOS 的 cgo/libusb 构建仍使用 `./scripts/build-macos.sh`，发行打包仍使用原来的脚本。

参考：[OrbStack USB 直通](https://docs.orbstack.dev/features/usb)。Docker Desktop 的 [USB/IP 支持](https://docs.docker.com/desktop/features/usbip/) 使用不同方式，本脚本的 Mac 自动连接流程仅适用于 OrbStack。
