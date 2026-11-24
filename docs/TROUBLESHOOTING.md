# 排障与已知限制

先区分信令、IP 音频、主机 USB 音频和模块蜂窝通话这几层，避免对所有问题统一增加缓存或重启整台路由器。

## 能注册但不能通话

确认手机与 OpenWrt 设备（如 NanoPi R76S / N1 等）在同一 Tailscale 网络、YakPhone 服务器使用该设备的实际地址且协议为 UDP。关闭另一台手机上的同名账号，避免最后一次 REGISTER 覆盖接听目标。检查当前注册状态，但不要公开日志中的 Contact、号码或凭据。

SIP `5060` 可达不意味着动态 RTP UDP 端口也可达。不要只开放一个猜测的固定 RTP 端口；根据自己的可信网络接口与 ACL 审核规则。不要把整个 INPUT 改成 ACCEPT，也不要创建公网 SIP 转发。

若网关刚重启过且手机尚未刷新注册，v1.2.0 起已知账号直接拨出时会收到 `401 Unauthorized` Digest 质询并在鉴权通过后自动恢复绑定，不再直接返回 `403` 卡死；若长时间无来电，打开一次 YakPhone 刷新注册即可。

## USB 换线后没声音 / 串口变化

`/dev/ttyUSB2` 存在，不能证明服务仍握着当前那个设备。运行 `<INSTALL_DIR>/doctor.sh` 或查看 gateway PID 的 `/proc/<pid>/fd/`；若串口链接标有 `(deleted)`，说明旧 FD 已失效。

网关的空闲串口读取由 Linux `poll` 事件驱动，连续失败（EIO/ENODEV/挂断）约 200 ms 后会记录 `modem serial port lost; restarting gateway` 并以非零状态退出，procd 约 **5 秒** 后重跑 runner。v1.2.0 起 runner 会通过 sysfs 自动重新定位 QDC507 的 AT 串口（`bInterfaceNumber=02`）与 ALSA 声卡（`EG25GQDC507`），并自动关闭该 USB 口的 autosuspend。如果因供电不足或线材接触不良导致反复掉线重启，先排查 USB 供电/换线；只在无通话时单独重启 CellBridge，不要无差别复位整条 USB 总线，也不要重启整台路由器/Passwall/Tailscale。

## 自己听得清楚，对方听自己卡顿

关注 iPhone → OpenWrt 设备的上行链路，不只看下载测速或 VPN 注册状态。PCMU/20 ms 预期约 50 帧/秒，日志中稳定的 250 帧窗口约 5 秒；窗口明显更长表示网关收到的有效音频帧不足，但日志本身不能算出精确 RTP 丢包率。

只读抓包如需执行，应只保留包到达时间、序号、时间戳、载荷长度等元数据，不保存语音或 SIP Authorization。RTP 序号跳跃还需结合时间戳、乱序、重复及抓包程序自身丢包判断。参考 [RFC 3550](https://www.rfc-editor.org/rfc/rfc3550.html)。

Wi-Fi 正常而某个蜂窝连接卡顿时，做单变量对照：同一手机换运营商、同一 SIM 换手机、或连接另一运营商的外网热点。一次同时换手机和 SIM 的改善不能单独证明运营商或手机哪一方有问题。VPN 也不能恢复在上游已经丢失的语音。

当前音频接收端接受 RTP `PT=0`（PCMU，160 字节载荷）以及 `PT=101`（RFC 4733 DTMF 事件，4 字节载荷）。上行有按序号排序的抖动缓冲（预缓冲 2 帧 / 40 ms，突发上限 15 帧 / 300 ms；队列超过 80 ms 时在静音帧里逐帧排空）和简单丢包补偿（上一帧 1/2、1/4 增益，之后静音），不是自适应缓冲，网络质量差时仍会卡顿。

每通电话结束时，`voice bridge stopping` 一行会带上行统计：`uplink_lost`（序号缺口）、`uplink_late` / `uplink_duplicate`（乱序/重复）、`uplink_underruns`（手机流中断、缓冲取空）、`uplink_overflowed`（突发超过 300 ms 上限被丢弃）、`uplink_drained`（为降低延迟跳过的静音帧，正常）。`uplink_lost` 高说明路上丢包；`uplink_underruns` 高而 `uplink_lost` 低，说明包是成批晚到。

## 通话结束或看似突然挂断

按同一 Call-ID 看事件先后，而不是把不同通话混在一起：

- `sip bye received method=BYE`：客户端发起结束；可能是正常挂断、误触或 App/iOS 结束，网关日志不能证明用户点了哪个按钮。
- `sip call ended by modem raw="NO CARRIER"`：模块先报告结束，网关再通知手机；仅凭这一行不能区分对方挂断、蜂窝网络释放或模块问题。
- `sip await bridge failed` / `sip inbound ring timeout`：查看是否尚未接通以及具体错误。响铃超时与已接通会话不是同一回事。

模块温度只在变化 ≥3 °C 或每小时记录一次（≥60 °C 每次告警），并采用自适应采样频率（<55 °C 每 60 s，≥55 °C 每 30 s），系统日志能保留更久的通话记录。

procd 输出可能全标为 `daemon.err`，但应用字段 `level=INFO` 仍是信息日志；不要把标签当成故障。

## 通话中自己的声音一断一断（麦克风像被间歇静音）

先看通话结束时的 `voice bridge stopping`：

- `uplink_overflowed` 很高：网关丢掉了成批晚到的语音。v1.1.1 起上限为 300 ms，正常情况下应接近 0。
- `uplink_lost` 很高：网络上真的丢了包（常见于手机走蜂窝流量 + VPN），网关补不回来。
- 两者都低，但 `voice client stats` 的 `nonzero` 在对方说话时很低（远小于 250）：说明手机发来的就是数字静音。这是手机端回声消除/降噪在对方说话时压低麦克风，属于手机侧行为。对方声音越大、削顶越严重（`voice cellular stats` 的 `peak_max` 接近 32767），压得越明显。可以尝试在 `<INSTALL_DIR>/cellbridge.env` 中设置 `CELLBRIDGE_DOWNLINK_GAIN_DB=-3`（或 `-6`）适度衰减下行音量，或降低手机通话音量、改用耳机、在 YakPhone 里调整回声消除/降噪设置做对照。

## 拨打客服时按键（DTMF）无反应

v1.2.0 起网关同时支持 **RFC 4733（`telephone-event/8000`，`PT=101`）** 与 **SIP `INFO`（`application/dtmf-relay` / `application/dtmf`）**，并转发为模块 `AT+VTS`。如果对方语音菜单不识别按键，先确认：

- 网关版本为 v1.2.0 及以上（`curl -s http://127.0.0.1:8787/api/v1/health`）；
- SIP 客户端未把 DTMF 设为“仅带内音频（In-band）”，优先选择 RFC 2833/4733 或 SIP INFO。

## 点静音后麦克风自己开开关关 / 通话中改状态没反应

在客户端 SIP 日志里找接通后的 `TX INVITE`（re-INVITE）及其响应。v1.1.1 起网关日志会有 `sip in-dialog offer answered`；如果客户端只收到 `180` 而没有 `200`，或挂断时出现对这个 re-INVITE 的 `CANCEL`，说明网关版本过旧。

## 后台 / 锁屏不响

前台 SIP 注册与 iOS PushKit 后台唤醒是两条链路。公开仓库不包含任何私人推送凭据；Pro 解锁、服务器可达和存在 Token 都不能单独证明来电推送成功。不要提交 Token 或私有推送密钥到仓库。

## 空间越来越大

根分区主要放系统依赖，不在上面放 Go 缓存；运行时的明文 `config.yaml` 已迁至内存 `/tmp/cellbridge`，减少闪存写入。应用、数据库和运行时位于数据盘。固定大小状态报告、系统日志缓冲和有界旧日志维护能减少无意义累积，不能阻止真实短信/通话历史增长。

维护警告不等于自动删除权限。未来需要清理历史时，先确定保留期限、同步语义和备份，再设计单独流程，不直接删 SQLite WAL/SHM。
