# DJI 4G 模块 × N1 OpenWrt × CellBridge

把带 VoLTE SIM 卡的 DJI 一代 / BAIWANG QDC507 模块，通过 USB 接到 N1 的 OpenWrt，作为 iPhone 的 SIP 蜂窝语音网关。客户端使用 YakPhone，异地连接可使用 Tailscale。

这是基于 [CellBridge](https://github.com/mccding/CellBridge) 和 [CellBridge-mac](https://github.com/easonjoo/cellbridge-mac) 的社区移植与部署整理，采用 MIT 许可。保留上游署名，不是 DJI 官方产品。**模块侧闭源运行时、密码、VPN 身份和用户数据不随仓库分发。**

## 已验证范围

2026-09-30 的实体部署：N1 / aarch64，OpenWrt 24.10.3、flippy 自定义内核 `6.18.12-flippy-94+`，USB ID `2c7c:0125`，ALSA 声卡 `EG25GQDC507`，Go 网关版本 `n1-20260930`。

- 前台 YakPhone 的拨出、来电接听和双向语音已实际使用。
- SIP Digest 认证、来电事务处理、通话结束清理和长通话修复包含在完整 `gateway/` 源码中。
- OpenWrt 原生 procd 启动，数据放在已挂载的数据分区，HTTP 管理接口仅监听回环地址。
- 录音关闭；空间维护不删除短信或通话记录。
- Tailscale 与 ZeroTier 都做过连接测试。本部署最终使用 Tailscale，不承诺某种 VPN 或运营商总是更稳定。

这不是“任何 OpenWrt 镜像一键通用”的固件包。**自定义内核与 opkg 软件源的 kmod 版本可能不一致，禁止照抄 `opkg --force-depends` 安装内核模块。** 先确认现有 USB 串口和音频驱动可用。

## 通话链路

```text
iPhone / YakPhone
    │ SIP UDP 5060 + RTP PCMU 8 kHz / 20 ms
    │ 局域网或 Tailscale 加密隧道
    ▼
N1 / OpenWrt / CellBridge
    ├─ /dev/ttyUSB2：AT 拨号、短信、来电事件
    ├─ ALSA arecord / aplay：双向 USB 音频
    └─ PyUSB ADB helper：模块侧语音路由
    │ USB
    ▼
QDC507 / DJI 4G 模块 + SIM → VoLTE → 对方电话
```

## 仓库内容

| 目录 | 内容 |
| --- | --- |
| `gateway/` | 完整 Go 网关源码、单元测试、SQLite schema/migrations |
| `openwrt/` | 已使用的运行脚本、procd 服务、路由 helper、只读检查、空间维护 |
| `openwrt/module-tools/` | Python ADB shim 和运行时校验/部署逻辑，不含二进制运行时 |
| `openwrt/vendor/usb/` | PyUSB 1.3.1 源码，保留 BSD 许可 |
| `tools/` | 编译、依赖许可收集、发布前检查 |
| `docs/` | 安装、安全、排障、已知限制和版本来源 |

## 快速开始

详细步骤见 [N1 安装指南](docs/INSTALL.md)。先准备已经开启 UAC、ADB root 的兼容模块；本项目不会自动刷机、修改 IMEI、重启路由器或调整全局网络。

在编译机（不建议在 N1 根分区编译）上：

```sh
git clone https://github.com/robin0227/DJI-4G-N1-CellBridge.git
cd DJI-4G-N1-CellBridge
sh tools/build.sh
```

在 N1 上，确认数据盘和依赖后，生成独立随机 SIP 密码：

```sh
python3 openwrt/init-secrets.py --output /tmp/cellbridge.secrets.env
# 仅本地查看此文件，把第一个 SIP 账号填入手机；不要上传或贴到公开 Issue。
```

用户自行提供三个合法取得、且 SHA-256 与 helper 匹配的模块运行时文件，放到 `openwrt/module-tools/voice-runtime/`。完整的首次安装命令见安装指南；安装器拒绝覆盖既有部署，默认不启动服务。

YakPhone 参数：

| 项目 | 值 |
| --- | --- |
| SIP 服务器 | N1 自己的 Tailscale IPv4（`tailscale ip -4`），不是作者的地址 |
| 端口 / 传输 | `5060` / UDP |
| 账号 / 密码 | 本机生成的 `cellbridge.secrets.env` 中的值 |
| 编解码 / 封包 | PCMU / G.711 μ-law，8 kHz，20 ms（160 字节载荷） |

同一个账号只在一台手机启用；换手机测试时先停用另一台的该账号，避免注册覆盖。

## 重要限制

- **锁屏推送尚未在此 N1 部署完成配置和验证。** 前台注册成功不等于后台一定能收到来电，不把 YakPhone Pro 或 Token 当成已经配置好的推送服务。
- USB 拔插可能改变串口编号，或让进程握着已删除的旧设备；健康 API 仍可能返回正常。当前没有承诺自动热插拔恢复，详见 [排障](docs/TROUBLESHOOTING.md)。
- 网络上行缺包不会因加长音频缓存就消失；当前接收器主要面向 PCMU/20 ms，不包含完善的自适应抖动缓冲、乱序修复或丢包隐藏。
- 日志受系统日志缓冲约束，但历史数据库保留全部记录，所以数据库仍会随真实使用增长。维护不是“空间永远不变”。
- 短信支持代码包含在仓库；本次公开发布没有新增收费的短信或拨号实测。

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

GitHub Actions 仅做检查、测试和 Linux ARM64 编译，不连接用户设备，不需要 N1、SIM、SIP 或 VPN 凭据，不自动改写仓库。构建包包含依赖许可证，不包含模块闭源运行时。

## 安全和许可

不要把 SIP/RTP 端口直接转发到公网。SIP 服务监听所有本地接口，**不等于 OpenWrt 防火墙已限制到 Tailscale**；必须按自己的网络检查 INPUT 规则和接口权限。Tailscale 隧道以外的 SIP/UDP 与 RTP 没有额外 TLS/SRTP 加密。

提交前阅读 [安全说明](SECURITY.md) 与 [第三方声明](NOTICE)。源码 MIT 许可见 [LICENSE](LICENSE)；PyUSB 和其他依赖保留各自许可证。厂商二进制的许可不因本仓库开源而改变。
