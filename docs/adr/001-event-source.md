# ADR 001：接受受限同源事件路线，保留原生快照否决

2026-10-26：**T017 整改评审完成**，接受限定 Linux 原型中的同源事件路线。
[机器裁决](../../experiments/mihomo-events/source5-event-source-decision.json)绑定候选、
已接受实验及最终证据索引（本地原件 `../../private/t017-final-source-index-preparation/draft-index.json`，不随Git上传）。
索引校验 5,593 份引用；索引本身保留 draft，接受来自本次独立 root 裁决。
T018 仍须完成托管与访问保护的版本来源评审；P0/P1 尚未放行。

## 采用的证据与能力

| 问题 | 已接受的可行性证据 | 保留边界 |
| --- | --- | --- |
| 原始进程归属 | 固定 0017 的 G01 正向 100/100，另有 PID 复用、缺证明、权限和三值规则独立分母；原 TCP connector 的 PID/启动身份/执行文件快照关联原 socket | FD 共享或转交不改历史；不把入口、GOST 或 launcher 当源 PID |
| 来源终末与恢复 | 同一数据面产生 source epoch/seq、flow 与整数计数；8 VM/16 槽验证来源、接收重放、核心重开和明确 gap | receipt ACK 不等于来源完整，更不等于 SQL 提交；没有终末的流保留 pending/null 与缺口 |
| 延迟和实际线路 | 6 VM/21 槽验证同 flow/attempt 的单调计时、协议 ready、失败及实际连接/节点关联 | setup 不是业务 RTT；注入/adapter/真实网络分列，未知阶段不能补测量值 |
| 当前多入口增量 | T016 16 组/39 VM/127 尝试/124 来源流；证明两入口原 connector、规则顺序、管理器缺席转发、完整回滚与缺证明恢复后持续拒绝 | 四 owner 仅合成覆盖；三个接纳前拒绝无来源流/终末/字节，均为 null |
| 计数开销 | 旧/当前外部核心文件分别 51,339,954 / 51,336,161 字节；两 owner 原件有 42 个 typed FD | 文件体积和 FD 数不代表 RSS、CPU 或增量内存；十分钟空闲与规模预算留 T020 实测 |

0017 完整二进制 SHA 为 `5693d4ac2bda5fcae7e5ac037d23db1b2a48049f681b8c825b8b4db12ac1b967`，
当前多入口核心为 `5e934bdc28dd07e7b7f8cf372d1d4f7a553a3aa3bd835de24f982dfed011a388`。
旧 100/100、8 VM、6 VM 结果保持旧 hash，没有移植成新版运行。既有源码独审
核对 1,002 份基线文件；增量限定为 owner 集合校验/接管、入口绑定、不可变来源选择
与只读多 owner 状态。Owner.Admit、BPF/证明 ABI、writer/reader、序号/字节、
wire、ready 和规则算法未变。root 以这些实现的原证据、当前定向测试和实际受影响
路径验收共同作出判断；来源选择文件本身有改动，不称整文件相同。

受限 profile 为 Linux amd64、初始 namespace、IPv4 回环、专用 cgroup 和受控
trusted-root guest。NAME/PATH 与有限 AND 有明确能力；其他进程条件仍按能力拒绝。
真实 SSH 断开、login-shell logout 和 guest S3 已有 T015 的限定环境证据，
不能推广为宿主休眠、完整 PAM/logind 或所有生产环境保证。

## 实施后果

基础管理继续不依赖 GOST、SQLite、TUI。完整统计使用经过验证的外部 Mihomo
事件能力；stock 和 GOST 兼容链保留各自能力记录，不借本裁决获得完整统计声明。
Hoopa 不嵌入代理内核；已授权隔离实验不能变成默认特权安装或自动替换用户核心。

保持原 reader 的 `recording_status=unverified`、`writer_end_evidence=null`。
正常来源帧、故障恢复和派生 gap 各有证据，单次只读报告不额外声称来源 fsync。
生产来源回收/积压、SQL 事务、历史视图/速率、默认部署、性能、UDP/QUIC、其他平台
以及同端口无缝迁移仍由后续任务实现和验收。原 0/100、60/100、宿主检查器失败及
其他失败均保留。下面文字是 2026-10-22 的历史决定，不描述当前授权或进度。

## 2026-10-22原生组合历史决定


日期：2026-10-22。历史裁决：当前候选被否决。替代路线现由 [ADR 005](005-telemetry-replan.md)确定，完整事件源尚未实现。决策人：主代理。没有降低规格或宪章，也不改变基础代理管理不依赖 GOST 的原则。

## 背景与实际证据

本地固定 Mihomo 1.19.26、GOST 3.3.1 的收发、认证和真实 Codex 路径成立，见 [P0 工作流](../validation/p0-workflow.md)。但 Mihomo 经 GOST 后识别的是 GOST 进程；活动快照没有保证短流终末记录。只用入口 metrics 无法构造真实 PID/域名/原连接链。

按 T017 仅实验了官方已有的 [GOST recorder HTTP plugin](https://v3.gost.run/en/concepts/recorder/)，没有新建代理协议、核心 fork、特权采集或 TLS 解密。追加 `--recorder-probe --recorder-reject-first` 可复现固定配置；20 条终末记录具有 SID 和整数 input/output，但无源 PID/name/start、seq/epoch 或可证明的 forward_setup 起点。首投失败后本次有界窗口内未见相同 SID 重投。字段事实及限制见 [G01](../validation/g01-identity.md)、[G02](../validation/g02-counters.md)、[G03](../validation/g03-quality.md)。

本次源码/发布资产的来源仍未核验。没有宣称所有 GOST 机制永远不可能提供这些能力；结论限于当前固定运行候选和已实验扩展。

## 决定

1. 不把 `metrics + /connections` 或本次 recorder HTTP callback 组合标为完整、可恢复的进程事件源；不据此开始生产账本和进程路由。
2. 保留当前 harness 作为入口转发、认证与受控负载对照；保留真实结果，不改测试分母、口径或 unknown 规则。
3. 后续数据源设计必须先提供下列可执行证据，再重跑 G01/G02/G03/G05；在此之前 T018 不放行下游阶段。

## 替代候选必须证明的契约

| 缺口 | 必须由何处给出证据 | 后续最小实验 |
| --- | --- | --- |
| 源身份 | 原应用到首个代理入口的 socket，在选路前关联真实 PID/name/启动身份/boot；共享归属可拒绝 | 两同名父进程、子孙、共享 socket、PID 复用与预登记 100 短流；100% 可识别样本正确 |
| 终末与恢复 | 事件产生方的稳定身份/世代、稳定事件 ID、明确整数边界及持久化/重放保证；无法恢复需明确 gap | callback 拒绝、采集器退出、核心崩溃、重复投递；核对最终字节及 gap，不仅检查 HTTP 200 |
| 跨跳关联 | 原入口连接与核心当时实际 rule/provider/node/ordered chain 的可核验关联 | 两线路、切换前后并存、改名；禁止 now 回填 |
| 计时 | 明确已解析目标到通道 ready 的起止、DNS/失败/超时定义与事件身份 | 慢 DNS/建链、失败、HTTPS/复用 N/A，与客户端 RTT 分开 |
| 路由前执行 | 已发布数据路径自行使用当时可信源身份执行有序三值规则 | 管理器停机仍执行，未知身份拒绝；不依赖每连接管理器 RPC 或 ledger SQL |

这些是候选的验收输入，不是对一个尚不存在的 adapter 的能力声明。ADR 005修正一项过强技术假设：接收持久序号可以由Hoopa提供，不能据此宣称源事件完整；不可恢复尾部仍按产品要求标gap。来源连续性与接收连续性分开验收。需要核心 fork、默认特权或放宽 MUST 时，必须另行取得明确范围决定，当前没有此授权。

## 被排除的替代做法与后果

静态服务名、launch/run ID、父进程或 GOST PID 不能代替真实源进程。提高轮询频率不能证明短连接终末完整；callback 收到过若干事件不能证明持久 replay。服务端 duration 不能直接命名为业务延迟或 forward_setup；PROXY header 支持也没有在当前 Mihomo 入站上得到验证。

可退化的“应用入口累计字节”本身有价值，但擅自只实现它会缩小已确认的整体规格。当前保持规格，停止依赖未证能力的实现；由后续事件源方案或显式范围修订解除阻断。没有选择双驱动层、插件框架、特权抓包或自研代理内核来掩盖缺口。
