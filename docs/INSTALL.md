# OpenWrt / iStoreOS ARM 设备首次安装指南

本指南适用于带 USB 接口的 **OpenWrt / iStoreOS / ImmortalWrt ARM 设备**（已实机验证 **NanoPi R76S** 与 **斐讯 N1**，同样适用于 NanoPi R2S/R4S/R5S/R6S、香橙派、树莓派、ARM64 软路由/电视盒子等设备）。本流程不执行刷机、分区、格式化或全局网络重配。**不要在正在通话时部署、重启或重挂模块语音路由。**

## 1. 只读检查依赖

```sh
uname -m
uname -r
cat /etc/openwrt_release
cat /proc/asound/cards
ls -l /dev/ttyUSB* /dev/snd/
df -h
command -v python3
command -v arecord
command -v aplay
command -v curl
command -v timeout
```

### 常见设备的数据盘路径参考

| 设备 / 系统 | 常见安装目录（`<INSTALL_DIR>`） |
| --- | --- |
| NanoPi R76S / iStoreOS | `/mnt/data/cellbridge` |
| 斐讯 N1 / flippy OpenWrt | `/mnt/mmcblk2p4/cellbridge` |
| 其他 NanoPi / 树莓派 / 香橙派 / 软路由 | `/mnt/data/cellbridge`、外接存储挂载点或 `--target /opt/cellbridge`（建议至少 512 MiB 可用空间） |

runner 启动脚本（`run-staging.sh`）会通过 Linux sysfs **自动发现** QDC507 模块（`2c7c:0125` 或 `2ca3:4006`）的 AT 串口（`bInterfaceNumber=02`）与 ALSA 声卡（`EG25GQDC507`），并自动关闭该 USB 设备的 autosuspend，不再依赖固定 `/dev/ttyUSB2` 或 `card0` 编号。如多模块共存需要手动指定，可在 `<INSTALL_DIR>/cellbridge.env` 中覆盖 `CELLBRIDGE_MODEM_TTY` / `CELLBRIDGE_ALSA_CARD`。

用户态依赖：Python 3（sqlite3、ctypes、urllib 等标准库）、libusb-1.0、alsa-utils（`arecord`/`aplay`）、`curl`、`timeout`、`logger`、`sha256sum`。PyUSB 源码已随项目提供。

内核依赖：USB `option` 串口驱动、USB audio / ALSA（`snd-usb-audio`）。例如实测的 NanoPi R76S（iStoreOS `6.12.112`）与斐讯 N1（flippy `6.18.12-flippy-94+`）均已自带或内置这些驱动，但 opkg 源中的 kmod 元数据可能与自定义内核不一致。**不要为凑版本强制 `opkg --force-depends` 安装 kmod，也不要自动替换内核。** 先确认插入模块后能枚举出串口与 `EG25GQDC507` 声卡。

Tailscale 由用户自行安装/登录，本安装器不保存 auth key、不创建 exit node、不改 Passwall、不启停 ZeroTier。使用适配当前 OpenWrt 的软件包，而不是把 Debian/systemd 安装命令复制到 OpenWrt。

## 2. 在其他机器编译

需要 Go 1.25+、Python 3、`tar`。默认静态构建 Linux ARM64（`aarch64`，`CGO_ENABLED=0`）；ALSA 通过路由器本机的 `arecord`/`aplay` 提供。

```sh
# 绝大多数 ARM64 设备（NanoPi R76S / N1 / R2S / R4S / R5S / R6S / 树莓派 4/5 等）：
sh tools/build.sh

# 若目标设备为 32 位 ARMv7（uname -m 显示 armv7l）：
GOARCH=arm GOARM=7 sh tools/build.sh
```

产物：`dist/cellbridge-gateway-linux-arm64`（或 `-arm`）、`SHA256SUMS` 以及部署压缩包 `dist/cellbridge-openwrt-arm64.tar.gz`。历次实机运行参考哈希见 `docs/PROVENANCE.md`；不同 Go 版本或构建参数可能产生不同哈希，请以本次生成的 `SHA256SUMS` 为准。

通过已验证的 SSH/SCP 连接上传部署包。较老 OpenWrt 没有 SFTP 时可使用 `scp -O`。SSH 密码不写进脚本；先核对主机公钥。

## 3. 准备运行时与密码

模块必须已经具有兼容的 UAC 和 ADB root。模块内部内核需要匹配 helper 的 `KERNEL_RELEASE = '3.18.44'`，它与 OpenWrt 主机内核是两回事。

自行提供下列文件到 `openwrt/module-tools/voice-runtime/`：

| 文件 | 校验来源 |
| --- | --- |
| `qdc507_aprv3.ko` | `openwrt/module-tools/voice_runtime.py` 中的 SHA-256 |
| `qdc507_voice.ko` | 同上 |
| `mavo-pcm-bridge.armv7` | 同上 |

不默认在线下载这些二进制，不自动刷机或绕过模块认证。请向模块提供者取得合法兼容的运行时。已有 helper 的 `provision_runtime()` 是上游保留的下载能力，只有理解源、许可和校验要求后才可主动调用，服务启动不会自动调用它。

```sh
python3 openwrt/init-secrets.py --output /tmp/cellbridge.secrets.env
chmod 600 /tmp/cellbridge.secrets.env
```

模板只用于解释字段，禁止把 `REPLACE_...` 当成真实口令。实际文件包含两个不同的 SIP 账号；同一模块仍只支持一通蜂窝通话。可仅在手机使用第一个账号，第二个账号用于不同客户端的独立注册。

## 4. 首次安装：默认不启动

在 OpenWrt ARM 设备上、包含 `openwrt/` 和二进制的目录里执行：

```sh
# 自动选择 /mnt/data/cellbridge（R76S/iStoreOS）或 /mnt/mmcblk2p4/cellbridge（N1）：
sh openwrt/install.sh \
  --binary ./cellbridge-gateway-linux-arm64 \
  --secrets /tmp/cellbridge.secrets.env

# 或手动指定任意安装路径（如 --target /mnt/data/cellbridge 或 --target /opt/cellbridge）：
sh openwrt/install.sh \
  --binary ./cellbridge-gateway-linux-arm64 \
  --secrets /tmp/cellbridge.secrets.env \
  --target /mnt/data/cellbridge
```

安装器会检查架构、目标目录与空间、用户态依赖、运行时 SHA-256、模块 USB/AT/ALSA 节点以及密码占位符，并自动将目标路径写入安装目录的 `openwrt-init`；拒绝覆盖已存在的部署或 init 服务，不安装 kmod，不改 firewall/UCI/cron，也不启动通话。

确认环境无误、没有正在进行的通话后，再单独安装服务并启动（下例以 `<INSTALL_DIR>` 代替你的实际安装目录，如 `/mnt/data/cellbridge`）：

```sh
cp <INSTALL_DIR>/openwrt-init /etc/init.d/cellbridge
chmod 755 /etc/init.d/cellbridge
/etc/init.d/cellbridge enable
/etc/init.d/cellbridge start
curl -fsS --max-time 3 http://127.0.0.1:8787/api/v1/health
```

启用服务会开机启动。runner 会按需加载 `option` 驱动、关闭该 USB 口自动休眠、通过 ADB 快速校验/热切模块语音路由、在内存 `/tmp/cellbridge/config.yaml`（权限 `600`）生成运行配置，并以较高调度优先级（`nice -10`）启动网关。procd 在设备缺席或启动失败时每 5 秒重试，不会重启整台路由器。

### 可选本地调优（`cellbridge.env`）

如需微调运行参数，可在 `<INSTALL_DIR>/cellbridge.env`（权限 `chmod 600`，非符号链接）中按需配置：

```sh
# 已有 +CMTI 即时收信唤醒，可将兜底轮询调长至 60s（默认 15s）
CELLBRIDGE_SMS_POLL_INTERVAL=60s
# ALSA 采集周期帧数（默认 320=40ms，若追求更低延迟可尝试 160=20ms）
CELLBRIDGE_ALSA_CAPTURE_PERIOD=320
# 若对方音量偏大导致削波或触发手机回声抑制，可衰减下行音量（单位 dB，默认 0）
CELLBRIDGE_DOWNLINK_GAIN_DB=0
# 多设备时可手动锁定串口或声卡
# CELLBRIDGE_MODEM_TTY=/dev/ttyUSB2
# CELLBRIDGE_ALSA_CARD=EG25GQDC507
```

## 5. 注册手机和只读自检

```sh
tailscale ip -4
<INSTALL_DIR>/doctor.sh
python3 <INSTALL_DIR>/check-n1-sip.py
```

YakPhone 使用 OpenWrt 设备的实际 Tailscale 地址、UDP `5060`、生成的账号密码、PCMU 20 ms。注册只确认 SIP 信令，仍需用户自行做拨出、拨入、DTMF 按键和双向音频测试。测试可能产生话费，脚本不会自动代拨。

`check-n1-sip.py` 与 `check-n1-idle.py` 保留了早期文件名以兼容既有脚本，但已通用化，支持任意 OpenWrt ARM 安装路径、`/tmp/cellbridge/config.yaml` 以及 sysfs 自动发现备用 AT 口和声卡。`check-n1-sip.py` 在回环发送 OPTIONS、Digest 注册查询与空体 MESSAGE，不更换手机绑定、不触发真实短信、不拨电话。`doctor.sh` 为只读检查，不打印密码、号码、SMS 或完整 SIP 报文。

## 6. 空间维护（可选）

```sh
<INSTALL_DIR>/cellbridge-maintenance.sh --check
```

如需自动维护，由用户查看并编辑现有 crontab，保留全部其他任务，只添加一行：

```cron
23 4 * * * <INSTALL_DIR>/cellbridge-maintenance.sh
```

维护脚本自动定位自身所在目录，使用 `wal_checkpoint(PASSIVE)`，不等待写锁、不删除历史、不在线 VACUUM、不删除 WAL/SHM。只对无人使用且大于 256 KiB 的旧 `launcher.log` 保留末尾 128 KiB，状态报告覆盖同一个文件。原始日志可能仍含号码，分享前需脱敏。

## 7. 停止、升级和回退

```sh
# 会中断正在进行的通话，只在确认空闲后使用。
/etc/init.d/cellbridge stop
```

既有部署不要运行首次安装器。升级前先通过 `python3 <INSTALL_DIR>/check-n1-idle.py` 确认无活跃语音通话，并备份可恢复的旧二进制、runner、init 和校验文件，**不要把 secrets、data 或整个系统目录打成公开 Release**。

回退应只替换相应版本的程序和服务文件，保留现有密码与数据库，再启动并检查 `/api/v1/health`、实际运行二进制和手机注册。仓库保留的是当前源代码和运行脚本，不包含绑定私人设备/旧哈希的临时升级脚本。
