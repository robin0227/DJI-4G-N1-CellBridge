# 更新日志

## v1.1.0 — 2026-11-04

### 通话质量
- 上行（手机 → 蜂窝）改用按 RTP 序号排序的抖动缓冲：预缓冲 40 ms、上限 120 ms；丢弃重复和迟到包；单个丢包以上一帧 1/2、1/4 增益补偿后转为静音，不再直接插入 20 ms 静音。
- 每通电话结束时 `voice bridge stopping` 记录上行统计：`uplink_received`、`uplink_lost`、`uplink_late`、`uplink_duplicate`、`uplink_concealed`、`uplink_underruns`、`uplink_overflowed`、`uplink_resets`。

### 稳定性
- USB 模块重新枚举后，空闲串口读取连续失败（EIO/ENODEV/挂断）约 200 ms 即判定串口失效，记录 `modem serial port lost; restarting gateway` 并以非零状态退出，由 procd 重跑 runner、重建语音路由并打开新串口。以前进程会一直握着已删除的设备，健康接口仍显示 `ok`。
- 网关主流程改为返回退出码，非零退出时仍执行 SIP、音频和数据库的清理。

### 日志
- 模块温度只在变化 ≥3 °C 或每小时记录一次（≥60 °C 每次告警）。以前每 30 秒一条，几小时就把 OpenWrt 系统日志里的通话记录挤掉。

### 部署脚本
- `run-staging.sh` 同步 N1 实际使用的版本：可选读取私有的 `yak-push-token`（64 位十六进制，权限 600）；版本号从安装目录的 `VERSION` 文件读取。
- `doctor.sh` 统计系统日志中 USB 串口失效重启次数。
- `.gitignore` 排除 `yak-push-token`。

### 验证
- `go test ./...`、`go vet ./...`、相关包 `go test -race`、Python 回归测试、`audit-release.py` 通过。
- 已部署到 N1（OpenWrt 24.10.3，`6.18.12-flippy-94+`）：健康检查、SIP 自检通过；实测一通约 105 秒的双向通话正常，上行 5244 帧、丢包 0、乱序/重复 0、缓冲取空 15 次、突发超限丢弃 41 帧。
- USB 串口失效自动重启只做了单元测试，未在实体设备上拔插验证。

## v1.0.0 — 2026-09-30

首个公开版本，对应 2026-09-30 已使用的 N1 / OpenWrt / QDC507 部署。
