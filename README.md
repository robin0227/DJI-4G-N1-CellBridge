# DJI 4G 模块 × OpenWrt ARM 设备 × CellBridge

把带 VoLTE SIM 卡的 DJI 一代 / BAIWANG QDC507 模块，通过 USB 接入任意 **OpenWrt / iStoreOS ARM 设备**（如 **NanoPi R76S、斐讯 N1、NanoPi R2S/R4S/R5S/R6S、香橙派、树莓派及其他 ARM 软路由/开发板**），作为 iPhone 的 SIP 蜂窝语音与短信网关。客户端使用 YakPhone，异地连接可使用 Tailscale。

> 仓库名因早期在斐讯 N1 上起步而保留了 `N1`，现已全面重构为 **OpenWrt / iStoreOS ARM 通用部署方案**：自动识别 QDC507 的 USB 串口、ALSA 声卡与数据分区，不限于 N1。

这是基于 [CellBridge](https://github.com/mccding/CellBridge) 和 [CellBridge-mac](https://github.com/easonjoo/cellbridge-mac) 的社区移植与优化整理，采用 MIT 许可。保留上游署名，不是 DJI 官方产品。**模块侧闭源运行时、密码、VPN 身份和用户数据不随仓库分发。**

## 适用设备（对号入座）

只要你的设备运行 OpenWrt / iStoreOS / ImmortalWrt、具备稳定供电的 USB 接口，并满足以下 3 个前提，即可直接套用本仓库方案：

1. **架构**：Linux ARM64（`aarch64`，默认预编译产物直接运行），或 32 位 ARMv7（执行 `GOARCH=arm GOARM=7 sh tools/build.sh` 自行交叉编译）；
2. **内核驱动**：内核已内置或可加载 `option`（USB 串口）与 `snd-usb-audio`（USB UAC 音频）；
3. **存储与运行库**：数据分区至少 512 MiB 可用空间，系统具备 `python3`（含 `sqlite3`）、`libusb-1.0`、`alsa-utils`（`arecord`/`aplay`）。

| 设备类别 | 典型型号（供对号入座） | 架构 / 系统 | 常见安装路径 | 状态 |
| --- | --- | --- | --- | --- |
| **友善 NanoPi RK3576** | **NanoPi R76S** | ARM64 / iStoreOS 25.12.6（Linux `6.12.112`） | `/mnt/data/cellbridge` | **已实机验证（v1.2.0 主力环境）** |
| **晶晨 S905D 盒子** | **斐讯 N1** | ARM64 / OpenWrt 24.10.3（flippy `6.18.12`） | `/mnt/mmcblk2p4/cellbridge` | **已实机验证（v1.0.0–v1.1.1）** |
| **友善 NanoPi 系列** | R2S / R2C / R3S / R4S / R4SE / R5S / R5C / R6S / R6C | ARM64 / OpenWrt / iStoreOS / ImmortalWrt | `/mnt/data/cellbridge` 或 `--target` 指定 | 架构与脚本通用兼容 |
| **ARM64 盒子 / 轻 NAS** | 晶晨 S905X3 / S922X、瑞芯微 H68K / H69K / H88K、贝壳云 / 我家云等 | ARM64 / flippy OpenWrt / iStoreOS | `/mnt/mmcblk*` 或 `--target` 指定 | 架构与脚本通用兼容 |
| **单板计算机（SBC）** | Raspberry Pi 3B+/4B/5、Orange Pi R1 Plus / Zero 2W / 3B / 5、Radxa Rock 系列 | ARM64 / OpenWrt | `--target <数据目录>/cellbridge` | 架构与脚本通用兼容 |
| **ARM 硬路由（带 USB）** | GL.iNet MT6000 (Flint 2) 等 MT7981/7986/7988（Filogic）、IPQ60xx/807x 路由器 | ARM64（或 ARMv7 自行编译）/ OpenWrt | `--target <外接存储>/cellbridge` | 需确认 USB 驱动与空间 |

> 这不是“任何 OpenWrt 镜像一键刷入”的固件包。**自定义内核与 opkg 软件源的 kmod 版本可能不一致，禁止照抄 `opkg --force-depends` 安装内核模块。** 先确认现有 USB 串口和 UAC 音频驱动可用。

## 已验证范围与核心特性

- **实机验证**：
  - **NanoPi R76S**（2026-11-24）：iStoreOS 25.12.6 / Linux `6.12.112` / `aarch64`，USB ID `2c7c:0125`，ALSA 声卡 `EG25GQDC507`，运行 v1.2.0（`r76s-20261124`），详见 [更新日志](CHANGELOG.md)。
  - **斐讯 N1**（2026-09-30 至 2026-11-04）：OpenWrt 24.10.3 / flippy 内核 `6.18.12-flippy-94+` / `aarch64`，长期实测前台拨出、来电接听与长通话。
- **低延迟通话与按键**：来电响铃并行预热模块语音路由 + 单条热切（启动耗时由 ~2.2 s 降至 ~0.7–1.0 s）；ALSA 默认采集窗口 40 ms；按序号排序的上行抖动缓冲与单包补偿；支持 **RFC 4733 与 SIP INFO DTMF**（可直接操作银行/运营商语音菜单）。
- **实时短信与低后台占用**：Linux `unix.Poll` 事件驱动串口读取（无 100 Hz 轮询空转）+ `AT+CNMI` / `+CMTI` 毫秒级唤醒收信；事件驱动僵尸呼叫清理、自适应温控采集与 SQLite WAL 优化。
- **硬件自适应与安全隔离**：启动脚本通过 sysfs 自动发现 QDC507 的 AT 串口和 ALSA 声卡、自动禁用该 USB 口自动休眠；运行时明文配置仅生成在内存 `/tmp/cellbridge`（`chmod 600`），HTTP 管理接口仅监听 `127.0.0.1:8787`，录音默认关闭，空间维护不删历史记录。
- Tailscale 与 ZeroTier 都做过连接测试；默认优先选择 `tailscale*` 网卡地址，避免误选运营商 CGNAT IP。

## 通话链路

```text
iPhone / YakPhone
    │ SIP UDP 5060 (Digest 认证 / DTMF) + RTP PCMU 8 kHz / 20 ms
    │ 局域网或 Tailscale 加密隧道
    ▼
OpenWrt / iStoreOS ARM 设备（如 NanoPi R76S / 斐讯 N1 等） / CellBridge
    ├─ sysfs 自动发现 /dev/ttyUSB*：AT 拨号、+CMTI 实时短信、来电与 DTMF
    ├─ ALSA arecord / aplay（EG25GQDC507）：双向 USB 音频
    └─ PyUSB ADB helper：模块侧快速语音路由热切
    │ USB（自动关闭 autosuspend）
    ▼
QDC507 / DJI 4G 模块 + SIM → VoLTE → 对方电话
```

## 仓库内容

| 目录 | 内容 |
| --- | --- |
| `gateway/` | 完整 Go 网关源码、单元测试、SQLite schema/migrations |
| `openwrt/` | OpenWrt ARM 通用运行脚本、安装器、procd 服务、路由 helper、只读检查、空间维护 |
| `openwrt/module-tools/` | Python ADB shim 和运行时校验/快速热切逻辑，不含闭源二进制运行时 |
| `openwrt/vendor/usb/` | PyUSB 1.3.1 源码，保留 BSD 许可 |
| `tools/` | 交叉编译（默认 ARM64，支持 `GOARCH=arm GOARM=7`）、依赖许可收集、发布前隐私审计 |
| `docs/` | 首次安装、排障与已知限制、版本来源与校验 |
| `CHANGELOG.md` | 各版本更新说明 |

## 快速开始

详细步骤见 [OpenWrt ARM 设备安装指南](docs/INSTALL.md)。先准备已经开启 UAC、ADB root 的兼容模块；本项目不会自动刷机、修改 IMEI、重启路由器或调整全局网络。

在编译机（不建议在路由器根分区编译）上：

```sh
git clone https://github.com/robin0227/DJI-4G-N1-CellBridge.git
cd DJI-4G-N1-CellBridge
sh tools/build.sh
# 若目标设备为 32 位 ARMv7 OpenWrt，请改为：
# GOARCH=arm GOARM=7 sh tools/build.sh
```

在 OpenWrt ARM 设备上，确认数据分区和依赖后，生成独立随机 SIP 密码：

```sh
python3 openwrt/init-secrets.py --output /tmp/cellbridge.secrets.env
# 仅本地查看此文件，把第一个 SIP 账号填入手机；不要上传或贴到公开 Issue。
```

用户自行提供三个合法取得、且 SHA-256 与 helper 匹配的模块运行时文件，放到 `openwrt/module-tools/voice-runtime/`。首次安装使用 `openwrt/install.sh`（自动识别 `/mnt/data` 或 `/mnt/mmcblk2p4`，也可用 `--target` 指定任意数据目录；拒绝覆盖既有部署，默认不启动服务）。

YakPhone 参数：

| 项目 | 值 |
| --- | --- |
| SIP 服务器 | OpenWrt 设备自己的 Tailscale IPv4（`tailscale ip -4`），不是作者的地址 |
| 端口 / 传输 | `5060` / UDP |
| 账号 / 密码 | 本机生成的 `cellbridge.secrets.env` 中的值 |
| 编解码 / 封包 | PCMU / G.711 μ-law，8 kHz，20 ms（160 字节载荷） |

同一个账号只在一台手机启用；换手机测试时先停用另一台的该账号，避免注册覆盖。

## 重要限制

- **锁屏推送需自行配置与验证。** 前台注册成功不等于后台一定能收到来电，不把 YakPhone Pro 或 Token 当成已经配置好的推送服务。
- USB 拔插或模块重新枚举后，网关发现串口已失效会主动退出，procd 约 5 秒后重跑 runner（通过 sysfs 重新发现 AT 串口和 ALSA 声卡、重建语音路由并启动网关）。若硬件异常导致反复重启，详见 [排障指南](docs/TROUBLESHOOTING.md)。
- 上行（手机 → 蜂窝）使用按 RTP 序号排序的抖动缓冲：预缓冲 40 ms、突发上限 300 ms（突发后的多余延迟在说话停顿时逐帧排空），丢弃重复/迟到包，单个丢包用上一帧衰减补偿。它不是自适应缓冲，网络上已经丢失的语音也补不回来；音频接收器只接受 PCMU/20 ms（以及 RFC 4733 DTMF 事件）。
- 日志受系统日志缓冲约束，但历史数据库保留全部记录，所以数据库仍会随真实使用增长。维护不是“空间永远不变”。
- 短信支持与 `+CMTI` 即时收信代码均包含在仓库并有单元测试覆盖；公开发布不会自动触发产生话费的短信或拨号。

## 测试与构建

```sh
cd gateway
go test ./...
go vet ./...
cd ..
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s openwrt -p 'test_*.py' -v
python3 tools/audit-release.py
sh tools/build.sh
```

GitHub Actions 仅做隐私审计、测试和 Linux ARM64 静态编译，不连接用户设备，不需要路由器、SIM、SIP 或 VPN 凭据，不自动改写仓库。构建包包含依赖许可证，不包含模块闭源运行时。

## 安全和许可

不要把 SIP/RTP 端口直接转发到公网。SIP 服务监听所有本地接口，**不等于 OpenWrt 防火墙已限制到 Tailscale**；必须按自己的网络检查 INPUT 规则和接口权限。Tailscale 隧道以外的 SIP/UDP 与 RTP 没有额外 TLS/SRTP 加密。

提交前阅读 [安全说明](SECURITY.md) 与 [第三方声明](NOTICE)。源码 MIT 许可见 [LICENSE](LICENSE)；PyUSB 和其他依赖保留各自许可证。厂商二进制的许可不因本仓库开源而改变。

## 赞赏支持

如果这个项目帮你省了时间，欢迎扫码请作者喝杯咖啡。完全自愿，不影响任何功能的使用；收款人是本仓库作者个人，与 DJI、CellBridge / CellBridge-mac 上游作者无关。

<img src="docs/images/wechat-pay.png" alt="微信赞赏码" width="220">
