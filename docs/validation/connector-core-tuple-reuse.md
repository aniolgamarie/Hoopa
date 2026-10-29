# 固定核心的顺序四元组复用拒绝

2026-10-24，按[修订契约 V2](../../specs/001-hoopa-proxy-manager/contracts/connector-core-tuple-reuse-v2.md)完成 96 项离线检查与一次有效隔离 VM 验证。核心及 online BPF 摘要保持原值；本轮成功只验证复用时拒绝错误归属，尚未实现长期映射回收。详见机器记录（本地原件 `../../experiments/sourceidentity/core-tuple-reuse-validation.json`，不随Git上传）。

首轮 `vm-tuple-reuse-w6s07ie4` 失败保留：第一条流已完成双向 33B，但夹具把 `accepted_tcp_connections` 当成累计 accept 次数，等待值 1 超时，第二客户端尚未启动。固定核心实际返回 `len(r.conns)`，是当前持有的连接数。V1 未保存失败轮询的实际状态值，不能事后补造。V2 仅修正判据和有界诊断，使用新目录、新预登记与新 VM；原契约、镜像和日志均未覆盖。

有效运行 `vm-tuple-reuse-p9ikb42q` 中，同一核心 PID75、同一来源世代，两个客户端依次连接 `127.0.0.1:31001 → 127.0.0.1:18993`：

| 客户端 | PID / leader start ticks | socket cookie | 结果 |
| --- | --- | --- | --- |
| 第一进程 | 83 / 441 | 4097 | HTTP → node:a → alpha.test，双向 33B、EOF、完整 closed |
| 第二进程 | 86 / 583 | 2 | connect 成功，未发送协议或应用数据，收到 EOF |

第一进程正常退出并 reap，确认 `/proc` 消失后等待至少 1.2 秒再复用。三个静稳阶段的当前连接数均为 0；累计 admitted 为 0/1/1，rejected 为 0/0/1，最终 revoked=true、state=degraded。两次成功 connect 和同一 owner 的准入计数共同证明这两次准入调用；不能用连接数 gauge 推断累计 accept。第二连接无 opened、ready、closed 或实际节点/目标记录。第一连接原身份及来源前缀逐字节保持，正常关闭后只读重开得到一条完整 33/33 流，共 7 帧。

原 Accept→Admit 代码路径和核心拒绝状态共同支持协议读取前拒绝，不能只凭客户端没有发送来作结论。23 份导出文件均重新核对摘要，专用 virtio 通道完整，内核日志未发现本检查器关注的事故。状态等待成功/失败路径均保存最后实际状态、采样次数、耗时和期望，分别有 5 秒及 501 次上界。

本次实际读取 guest TCP 配置：fastopen=1、syncookies=1、tw_reuse=2、tw_reuse_delay=1000；两个 socket 的 TCP_FASTOPEN_CONNECT 均为 0。使用普通 connect API，不能将这些值解释为系统全面禁用 TFO。固定源码审查表明普通 active 回调与最终 ACK、passive 回调及 accept 的锁序可支持当前顺序路径；TFO 存在提前排队/accept 和不同回调路径，未被本轮覆盖。相关审查保存在私有 `tcp-callback-order.md`，不把源码推论提升为全部握手和并发交错实证。

当前表只插入、不覆盖、不回收，复用触发持续故障，整个捕获世代停止放行新身份。此行为避免旧 PID/cookie 被套用，但仍需要容量、回收和运行连续性设计。名称/路径能力、T132/T137、139 项完成 21 项、P0/P1 门槛不变；正式 G01 新样本 0，旧 0/100 保留。
