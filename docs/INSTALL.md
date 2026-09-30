# N1 / OpenWrt 首次安装

本指南针对已验证的 N1 自定义 OpenWrt 环境，不执行刷机、分区、格式化或全局网络重配。不要在正在通话时部署、重启或重挂模块语音路由。

## 1. 只读检查依赖

```sh
uname -m
uname -r
cat /etc/openwrt_release
cat /proc/asound/cards
ls -l /dev/ttyUSB* /dev/snd/
df -h / /mnt/mmcblk2p4
command -v python3
command -v arecord
command -v aplay
command -v curl
command -v timeout
```

本部署使用已挂载的 `/mnt/mmcblk2p4`，安装目标 `/mnt/mmcblk2p4/cellbridge`；路径不存在或未挂载时不要继续。默认 runner 使用 `/dev/ttyUSB2` 和 `hw:CARD=EG25GQDC507,DEV=0`，并等待声卡 0 的 PCM 节点。**这些是硬件相关设置，不是通用串口发现算法。** 必须核对 sysfs 接口；设备不同则先审查修改 runner，不能仅凭编号猜测。

用户态依赖：Python 3（sqlite3、ctypes、urllib 等标准库）、libusb-1.0、alsa-utils（arecord/aplay）、curl、timeout、logger、sha256sum。PyUSB 源码已随项目提供。

内核依赖：USB option 串口驱动、USB audio / ALSA。已验证的自定义内核为 `6.18.12-flippy-94+`，但 opkg 中 kmod 元数据可能写着另一个内核版本。**不要为凑版本强制安装 kmod，也不要自动替换 N1 内核。** 先确认当前模块能枚举出串口和 UAC 音频。

Tailscale 由用户自行安装/登录，本安装器不保存 auth key、不创建 exit node、不改 Passwall、不启停 ZeroTier。使用适配当前 OpenWrt 的软件包，而不是把 Debian/systemd 安装命令复制到 OpenWrt。

## 2. 在其他机器编译

需要 Go 1.25+，Python 3，tar。ARM64 网关使用 `CGO_ENABLED=0` 静态构建；ALSA 通过 N1 自己的 arecord/aplay 提供。

```sh
sh tools/build.sh
```

产物：`dist/cellbridge-gateway-linux-arm64`、校验文件以及部署压缩包。第一次发布记录的实体运行二进制 SHA-256 见 `docs/PROVENANCE.md`；不同 Go 版本或构建参数可能产生不同哈希，不能拿旧哈希当成所有未来版本的预期值。

通过自己已验证的 SSH/SCP 连接上传部署包。较老 OpenWrt 没有 SFTP 时可以使用 `scp -O`。SSH 密码不写进脚本；先核对主机公钥。

## 3. 准备运行时与密码

模块必须已经具有兼容的 UAC 和 ADB root。模块内核需要匹配 helper 的 `KERNEL_RELEASE = '3.18.44'`，它与 N1 主机内核是两回事。

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

在 N1 上、包含 `openwrt/` 和二进制的目录里执行：

```sh
sh openwrt/install.sh \
  --binary ./cellbridge-gateway-linux-arm64 \
  --secrets /tmp/cellbridge.secrets.env
```

安装器检查架构、挂载点、空间、依赖、运行时校验和密码占位符；拒绝已存在的部署或 init 服务。只复制到新安装目录，不升级既有实例，不安装 kmod，不改 firewall/UCI/cron，也不启动通话。

确认环境无误、没有正在进行的通话后，再单独安装服务并启动：

```sh
cp /mnt/mmcblk2p4/cellbridge/openwrt-init /etc/init.d/cellbridge
chmod 755 /etc/init.d/cellbridge
/etc/init.d/cellbridge enable
/etc/init.d/cellbridge start
curl -fsS --max-time 3 http://127.0.0.1:8787/api/v1/health
```

启用服务会开机启动。runner 会加载现有 option 驱动、通过 ADB 部署校验过的运行时并重建模块语音路由；这是有状态操作，仅在用户主动启动时执行。procd 在设备缺席/启动失败时按间隔重试，不会重启整个 N1。

## 5. 注册手机和只读自检

```sh
tailscale ip -4
/mnt/mmcblk2p4/cellbridge/doctor.sh
python3 /mnt/mmcblk2p4/cellbridge/check-n1-sip.py
```

YakPhone 使用 N1 的实际 Tailscale 地址、UDP 5060、生成的账号密码、PCMU 20 ms。注册只确认 SIP 信令，仍需用户自行做拨出、拨入和双向音频测试。测试可能产生话费，脚本不会自动代拨。

`check-n1-sip.py` 在回环发送 OPTIONS、Digest 注册查询与空体 MESSAGE；不更换手机绑定、不触发真实短信，也不拨电话。`doctor.sh` 只读检查，不打印密码、号码、SMS 或完整 SIP 报文。

## 6. 空间维护（可选）

```sh
/mnt/mmcblk2p4/cellbridge/cellbridge-maintenance.sh --check
```

如需自动维护，由用户查看并编辑现有 crontab，保留全部其他任务，只添加一行：

```cron
23 4 * * * /mnt/mmcblk2p4/cellbridge/cellbridge-maintenance.sh
```

维护使用 `wal_checkpoint(PASSIVE)`，不等待写锁、不删除历史、不在线 VACUUM、不删除 WAL/SHM。只对无人使用且大于 256 KiB 的旧 launcher.log 保留末尾 128 KiB，状态报告覆盖同一个文件。原始日志可能仍含号码，分享前需脱敏。

## 7. 停止、升级和回退

```sh
# 会中断正在进行的通话，只在确认空闲后使用。
/etc/init.d/cellbridge stop
```

既有部署不要运行首次安装器。升级前保存可恢复的二进制、runner、init 和校验文件，**不要把 secrets、data 或整个系统目录打成公开 Release**。先校验候选版本，并通过已经确认的备用 AT 接口检查空闲；脚本默认 `/dev/ttyUSB3`，编号不符时不能强行执行。

回退应只替换相应版本的程序和服务文件，保留现有密码与数据库，再启动并检查健康、实际运行二进制和手机注册。仓库保留的是当前源代码和运行脚本，不包含绑定私人设备/旧哈希的临时升级脚本。
