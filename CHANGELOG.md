# 更新日志

## v1.1.1 — 2026-11-04

### 修复
- **来电接听后对方听到“正忙”**：网关发给 SIP 客户端的 INVITE 请求地址写的是 `sip:<账号>@<网关>`，而 YakPhone 注册的是带随机后缀的 Contact（如 `sip:iphone-xxxx@…`），于是回 `404 Not Found`，网关随即挂断蜂窝来电。现在 INVITE 发往客户端注册的 Contact URI（RFC 3261 §10.3）。
- **来电推送补充 `call_id`**：YakPhone 推送接口用它把推送唤醒的 CallKit 来电和直连 INVITE 识别为同一通电话；取值为该 INVITE 的 SIP Call-ID。
- **通话中点静音后麦克风反复开关**：YakPhone（baresip）在来电接通约 50 ms 后会发一个通话中的 re-INVITE，网关把它当成未完成呼叫的重传，只回了 `180 Ringing`、始终没有最终响应，客户端整通电话都卡在未完成的媒体协商里。现在对话内的 re-INVITE / UPDATE 回 `200 OK` 并带 SDP 应答（按 offer 镜像 sendonly/recvonly/inactive），同时更新 RTP 目标；通话中的 `INFO` 回 `200 OK`；其余不支持的请求回 `501 Not Implemented`，不再不回复。
- **通话中自己的声音断断续续**：iPhone 在 Wi-Fi 省电模式下成批发送语音包，原来 120 ms 的缓冲上限会整批丢弃（一通 85 秒来电丢了 172 帧，约 3.4 秒）。上限放宽到 300 ms，突发后多出的延迟改为在说话停顿处逐帧排空；新增 `uplink_drained` 统计。
- **每通电话开头积压约 2 秒旧语音**：手机一接听就开始发 RTP，但网关要等模块接通蜂窝来电后（约 2.3 秒）才开始读取，期间约 115 个包积压在 socket 里，接通瞬间一次性涌入：约 100 帧溢出丢弃，剩余部分让通话开头多出 300 ms 延迟。现在开始接收时先丢弃 socket 中已积压的旧包（日志 `rtp stale packets discarded`）。
- **上行播放比手机发送慢 3%–4%**：模块播放循环用 `time.Ticker`，写 aplay 偶尔阻塞超过 20 ms 时定时器会直接丢拍，实际只播约 48 帧/秒，每通电话因此丢约 100 帧。改为按绝对时间排程并补齐错过的帧（落后 200 ms 以上则重新对齐）。播放写入改用独立的锁，阻塞的 aplay 写入不再卡住 RTP 接收，避免人为制造突发。实测模块声卡播放速率准确（10 s 标称用时 10.09 s，含 aplay 启动）。


### 已知问题（客户端）
- YakPhone 1.7 在通话中点“静音”后，麦克风会在不操作的情况下自动开、关。期间没有任何 SIP 信令，网关收到的 RTP 显示手机自己恢复了发送麦克风音频，属于客户端问题，网关无法修复。

### 验证
- `go test ./...`、`go vet ./...`、相关包 `go test -race`、Python 回归测试、发布前隐私扫描通过；新增测试覆盖随机 Contact、re-INVITE/UPDATE 应答、501、突发吸收与排空、播放节拍（旧实现在同样条件下仅 41 帧/秒）、旧 RTP 丢弃。
- 实体 N1 + YakPhone 1.7（baresip 4.12.0）：来电接听正常、双向语音正常；最后一通约 63 秒来电接通时丢弃 120 个积压旧包，上行 `uplink_overflowed` 由每通约 100 降为 0，丢包 0、乱序/重复 0，首个统计窗口 4992 ms。

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
