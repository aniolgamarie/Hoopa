# T133：来源生命周期的独立补全

日期：2026-10-23。状态：**部分实现，T133保持未勾选，G02未通过**。这轮是在[T130诊断](p0-prerequisites.md)/[ADR006裁决](../adr/006-source-identity-proof.md)后进行的独立P0实验；不依赖正向进程身份，不改变既有100流失败分母，也不开放P1。

## 已实现的行为

新的隔离核心副本加入1秒累计checkpoint，沿用13字段精确uint64字符串计数。checkpoint不是新增流量，closed也不是需要再加一次的增量。每流的终末及维度gap额度与epoch marker额度分别预留；中间事件耗尽时，已知终值仍可保存，窗口检查点另标缺口。

正常SIGTERM/SIGINT的退出钩子阻止新计量、停止并等待checkpoint worker，再持久化epoch_closed。Mihomo原退出路径没有先静止所有活动relay，因此活动流只能保存`source_shutdown_before_final`、null字节尾部与incomplete marker；不会把此刻内存快照伪造成精确closed。raw close/SIGKILL不生成正常退出marker；Write/Sync结果不确定即停止该source写入，不推进已确认seq/epochClosed。

接收端增加[完整实验事件解码器](../../experiments/mihomo-events/source_event.go)和[严格接收器](../../experiments/mihomo-events/receiver_linux.go)。七种kind分别校验精确字段集合，包括嵌套ProcessEvidence、重复/转义重名键、未知/缺字段、整数、UTC、null及marker finality。只接受当前候选的计时定义和`protocol_ready_validation=unverified`；解析不能提升质量能力。固定核心的DIRECT链会携带空provider名，原样保留，不猜配供应商；实际节点链名仍须非空。

接收过程在同一receiver互斥区内检查source序列，再调用已有journal的Write+Sync，成功后才推进内存位置并返回receipt。每epoch的source_seq必须从1连续，来源实例及generation固定；receipt_seq独立分配。原ID原文重投返回同receipt，异文冲突，epoch_closed后拒新事件但接受marker本身重投。不确定写入后必须完整重开；重开对全部opaque记录重新严格解析并核对内外身份及顺序，不修剪坏尾。

[只读生命周期恢复器](../../experiments/mihomo-events/source_lifecycle.go)逐帧检查冻结快照的长度、checksum、完整事件形状、顺序、累计单调性及flow终态。旧opened无final返回pending，或保留原gap；最后可信checkpoint独立保存，未观测尾部为null。它不修改旧epoch、不生成猜测final、不发ACK，也不能替来源证明磁盘持久性。调用者必须提供停止写入的快照；生产并发读取/回收仍是后续任务。

## 实际验证

新核心SHA-256：`aad983b1e6597704fa5de84793da5869d9d6680b43b57c689a8be716292b5655`，版本`hoopa-t133-lifecycle`，固定Go1.27.3/Linux amd64；50,454,174字节属于外部实验核心，不计作Hoopa管理器预算实测。[新manifest](../../experiments/mihomo-events/lifecycle-candidate-manifest.json)及[追加补丁](../../experiments/mihomo-events/patches/0002-source-lifecycle.patch)与旧制品分开；本轮不是T137的完整候选冻结或CapabilityRecord。

| 真实实验 | 观察结果 | 证据边界 |
|---|---|---|
| CONNECT、CONNECT同包预读、SOCKS5域名，各一条受控回环流 | 各1个checkpoint、1个closed，双向累计各257字节；3流关闭后complete marker，0gap | 单attempt DIRECT TCP；不是通用UDP/QUIC或所有协议校准 |
| 保持活动流后SIGTERM | 1个null-tail gap、incomplete marker，唯一opened与gap同flow/epoch且顺序正确 | 不宣称活动尾部精确可恢复 |
| SIGKILL后同source目录重启，以新登记请求触发扫描 | 旧frames与ID/seq原样保持；新请求属于新epoch | 旧流没有final，未制造终末；核心尚未自动写恢复gap |
| 独立Go reader读取以上4份停止写入的frames | normal closed3/complete；SIGTERM gap1/incomplete；旧崩溃epoch pending1/unverified，新epoch closed1/complete | 不是仅凭Python摘要代替完整wire解析；旧尾部仍未知 |
| 来源HTTP接收首投503 | 重投同一已持久事件原文/ID/seq，显式回环race通过 | 单事件测试，不是完整G02 |
| 接收副本Write+Sync后断开连接，不发送ACK；再重开来源 | 来源重投及重开后仍发送原epoch原文；race通过 | 与真实receiver落盘重开/去重测试分别取证，不宣称source/receiver/SQL共同事务 |

Hoopa新增事件/接收/恢复的定向race为16项顶层、8项子用例通过，0skip/FAIL；Python两个探针共13项单测通过。来源生命周期定向race为19项通过、1项默认关闭的网络测试skip；该网络测试随后显式开启并实际通过，另新增ACK丢失/重开测试通过。wrapper的5项partial n、buffer/unwrap、关闭时序回归race通过。相关vet、gofmt及隔离核心构建通过。没有把默认skip或过滤器未选中用例的exit0算作网络验收通过。

真实实验原始证据在私有`/tmp/hoopa-lifecycle-6s7bfs3u`，包括原`summary.private.json`、4份frames和后追加的`reader-validation.json`。原摘要的`reader_pending_assertion_executed=false`保留为当时事实，独立Go验证另行记录。代码审查补强active flow关联断言后，仅在原停止写入快照上复验，不重复启动核心；补充文件为`active-shutdown-link-validation.json`。这些临时原文可能被清理，公开机器摘要（本地原件 `source-lifecycle.summary.json`，不随Git上传）保存摘要及验证范围。

独立制品复核从干净固定baseline重新复制，实际先应用001、再应用002，分别与旧/新源码整树及权限一致；旧16份/新6份摘要、原二进制/001补丁未变，新binary版本/摘要/体积和模块锁一致。没有重新构建二进制，不能把源码一致性检查称为可复现构建证明。

## 尚未完成

- 核心重开来源文件目前仍主要校验帧/序号/epoch；完整事件族严格校验已在Hoopa接收/reader实现，尚未全部接回核心重开与发送路径。
- 旧opened的pending重建已实测；来源自动生成恢复gap、完整满盘/Sync/损坏/背压及所有G02故障组合仍未完成。
- CONNECT/SOCKS5及预读有真实结果，partial-write有定向wrapper测试；lazy-handshake边界及逐attempt字节归属仍未验收，`mihomo_proxy_stream_v1`未成为完整通过能力。
- T132仍缺少获批、可证明的原socket唯一owner方案及正向环境；旧正式G01身份0/100保留。T134的真实ready/两线路和T135的加载期能力拒绝/入站保护尚未实现验收，T137/T127–T129不放行。
- SQL账本、生产有界回收、SSH/注销/休眠、其他平台及性能预算均未由本轮通过。

不因接收器和局部恢复测试存在，就将T133、G02或整个项目标为完成；当前任务总数139，已完成18（本轮关闭T130/T131）。
