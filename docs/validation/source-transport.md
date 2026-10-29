# 来源真实传输、重开去重与只读配额验证

日期：2026-10-23。根代理验收裁决：**T133任务正文明确的本地实现与验证要求已覆盖，已完成并勾选T133，139项/19完成**。这不是正式G02，也不关闭T128；T128仍须满足T127、T133、T137等自身依赖后执行。T132/T134及T137未完成，P0、SQL账本和平台支持未因此放行。

本记录接续[来源恢复局部补全](source-recovery-closure.md)。最终范围和日志摘要见传输机器验证（本地原件 `../../experiments/mihomo-events/transport-validation.json`，不随Git上传）；closure阶段机器记录（本地原件 `../../experiments/mihomo-events/closure-validation.json`，不随Git上传）保留其冻结时的阶段状态，不回改旧结论。任务边界依据[ADR008](../adr/008-remaining-evidence-execution.md)，这里记录实际执行，不重复实施方案全文。

## 固定输入没有变化

本轮没有重编或替换核心。仍使用001–005的`hoopa-t133-closure`，SHA-256为`bfc1df3a99871f2b1257e4b28b58b64a97b6289840b6cd63a50c6b86ec75c6d2`，50,550,333字节；路径为`/tmp/hoopa-t133-closure-artifact-l35s9_fn/mihomo-closure`。其干净固定上游、五补丁实际apply、整树内容/权限一致性及构建记录仍由[closure manifest](../../experiments/mihomo-events/closure-candidate-manifest.json)固定。旧补丁、制品和摘要未覆盖。

新增[真实传输测试](../../experiments/mihomo-events/transport_integration_test.go)最终源码SHA-256为`697914b2df38234f239e9b6a27c89354e736907946c7e4297ae1df20bbfb40fd`；另收紧Hoopa[只读恢复器](../../experiments/mihomo-events/source_recovery.go)的manifest文件对配额并补反例。这些是Hoopa侧测试/reader约束，没有改变上述核心字节、v1事件形状或来源序号协议。

测试明确绑定候选路径/SHA，并核对固定manifest；每个场景复制私有0500可执行快照，目录0700，其余文件0600。stock运行缓存预建为0600，未引入SQL账本。所有端点为127.0.0.1；独立命名的非计量健康入口必须完成CONNECT加echo，TCP accept不能算ready，也不消耗被测flow分母。每场景预登记双向257字节及30秒固定deadline，不探测整机流量或身份覆盖率。

## 最终真实race：2通过、0失败、0跳过

最终原始摘要为`/tmp/hoopa-closure-transport-results-v2.json`，执行日志为`/tmp/hoopa-closure-transport-bound-offline-race.jsonl`，日志SHA-256为`5e551015ff300171459de46250f74ba25290315dbc5051527bfc39a00e95da33`。本地回环执行经过明确授权；默认未设置gate时两项均skip，不能算通过。最终vet和gofmt通过。

```sh
HOOPA_CLOSURE_NETWORK_TEST=1 \
HOOPA_CLOSURE_CANDIDATE=/tmp/hoopa-t133-closure-artifact-l35s9_fn/mihomo-closure \
HOOPA_CLOSURE_CANDIDATE_SHA256=bfc1df3a99871f2b1257e4b28b58b64a97b6289840b6cd63a50c6b86ec75c6d2 \
GOTOOLCHAIN=local GOPROXY=off GOCACHE=/tmp/hoopa-go-cache \
/tmp/hoopa-go-1.27.3-cb6ry1lq/go/bin/go test -race -count=1 -json \
  ./experiments/mihomo-events -run '^TestHoopaClosureTransport'
```

| 场景 | 实际观察与边界 |
| --- | --- |
| collector离线、恢复上线、closed ACK丢失及重开 | 以AF_INET、SOCK_STREAM、SOCK_CLOEXEC创建socket，仅bind 127.0.0.1:0，不listen、不启用SO_REUSEADDR，并持有fd直到主CONNECT双向257字节、source checkpoint及closed已记录。确认SO_ACCEPTCONN=0且connect返回ECONNREFUSED，再关闭占位socket启动实际接收器；端口交接若被抢占则Listen失败，不放宽离线断言。该阶段closed尚未送达 |
| 实际接收closed后扣ACK | 上线HTTP端点调用已有SourceReceiver.Accept，经过journal Write+Sync才进入屏障，故此时closed已持久接收但发送方没有取得ACK，不能称从未送达。明确验证自有core以SIGKILL退出，停止HTTP端点并关闭/重开实际receiver journal；旧原文直接重投返回原receipt，记录数不增加 |
| core重开与新epoch | 同source目录重开核心并使新epoch完成另一条双向257字节流。旧manifest/frames在子进程退出及重开后逐字节不变，接收raw与旧来源原文一致；重放closed的ID/seq/raw/receipt保持，两个epoch各只有一个累计257字节的closed，不把checkpoint与final相加 |
| opened ACK停滞不阻塞已有转发 | 实际receiver持久Accept opened后扣住HTTP ACK；在显式屏障之后，已有主连接仍完成双向257字节，source checkpoint/closed继续产生。解除屏障后完整接收，累计终值唯一。所有2秒HTTP超时产生的重试共享同一屏障，不靠盲等假定请求仍受阻 |

两个独立场景的私有证据目录分别为`/tmp/hoopa-closure-transport-3253735116`（离线/重开）和`/tmp/hoopa-closure-transport-2322544171`（ACK停滞）。三个自有core子进程均已Wait回收；只终止本测试启动的child。失败清理也释放所有HTTP屏障及bound socket，且不持receiver锁等ACK。源活跃时可能短帧，观察在固定deadline内重试严格reader，不修改或放宽codec。

前两次真实pass的结果`/tmp/hoopa-closure-transport-results.json`与`/tmp/hoopa-closure-transport-explicit-first-race.jsonl`、`/tmp/hoopa-closure-transport-explicit-final-race.jsonl`均保留；最终离线证明使用v2的`bound_not_listening`，不将旧端口前提追溯升级为新证明。更早默认skip、沙箱socket权限失败及获授权的局部重投通过仍分别保留在closure记录中。

## 只读恢复器的文件对配额

原反例证明：即使事件形状和生命周期均合法，`ReadSourceRecovery`也会接受超过manifest声明的文件对字节预算。`/tmp/hoopa-source-recovery-budget-before.jsonl`记录该测试实际失败；修复后`/tmp/hoopa-source-recovery-budget-final.jsonl`记录3项race通过、0失败/skip，包含配额、只读pending与配对/损坏拒绝。

预算是`len(raw_manifest) + frames_bytes <= max_source_bytes`。reader至多消费剩余frames预算加1字节的溢出哨兵；恰好达到整个文件对limit的合法快照仍接受，超限拒绝且不返回部分成功报告。单帧合法或独立8MiB读取上限不能代替这个整体约束。此次没有取得writer锁、执行Sync、生成来源事件或发布缺口事实；缺final仍是pending，未知尾部为null。

## T133正文覆盖与证据映射

下表覆盖[T133任务正文](../../specs/001-hoopa-proxy-manager/tasks.md)的本地要求；并列不同阶段证据，不把测试数量相加成一次正式矩阵。

| 正文要求 | 覆盖证据及限制 |
| --- | --- |
| 同源完整wire/epoch校验接四处；保留v1原帧 | closure记录中的六个纯生产文件字节一致；source重开/发送、receiver、reader校验七kind、精确整数/重复字段、身份/seq、opened/attempt/终态与marker、累计单调。旧DIRECT空provider保留 |
| pending/派生gap、checkpoint/closed与稳定恢复身份 | closure恢复测试覆盖无结束证明保持pending、持writer锁稳定全扫描后派生epoch_ended_without_final、checkpoint及null尾部、closed精确幂等、维度gap与已知字节并存；重复报告不新建source/receipt/ByteFact，观测时间不进入稳定事实冲突比较 |
| 完整/短Write、Sync不确定及确认前缀 | closure故障注入验证不确定写入poison且不推进seq/confirmedSize，sender仅发送已确认前缀；重开以原ID消歧。预留终末/marker额度，容量满保留重放。注入不是宿主真实断电/磁盘填满证明 |
| 损坏/缺manifest、身份错配、反复恢复/多epoch | closure反例拒绝畸形manifest/帧、坏checksum、重复键、错误generation与孤立事件，保留坏尾不修剪；32并发pending与合法终结历史区分，真实32前缀追加第33帧时拒绝。新reader文件对配额补足整体有界读取 |
| closed未投递、collector离线/背压、503/ACK丢失 | v2真实场景补足持有非listen端口的离线、已接收但未ACK、停滞ACK期间真实转发继续、实际journal重开去重；closure原503重投测试保留。新测试只通过既有Accept持久确认，不另写一套codec/SQL |
| 实际n计数、CONNECT/SOCKS5/预读/partial/lazy首次Write | 001–005的受控流及合成回归、HTTP预读先失败后通过、首次Write的8个边界子例覆盖actual n与原error。lazy计数单测没有证明T134协议ready、实际Conn质量profile或两条线路 |
| 实际上限内规模/容差、扫描成本与未终结flow | closure机器记录的32epoch/精确8MiB、0未终结flow扫描，与32pending样本独立；预登记规模与回归护栏并记录扫描时间、Go heap抽样和进程生命周期MaxRSS。它们不代表产品闲置RSS/CPU预算通过 |
| 生产持久Gap/Reconciliation、传输与GC边界 | 保持T073/T138责任，不将来源Sync、接收ACK、SQL提交或回收混成共同事务；没有以本轮完成生产SQL/GC为T133本地完成的替代声明 |

## 身份环境与剩余门槛

T132运行前提的新只读快照`/tmp/hoopa-t132-runtime-prerequisites.json`对应**与获授权回环核心相同的执行上下文**，不是整台宿主，也不同于默认沙箱：

| 项目 | 实际记录 |
| --- | --- |
| effective/permitted/ambient capabilities | 均为0；bounding set为`000001ffffffffff`，不是0 |
| NoNewPrivs / Seccomp | 都为0，不能沿用默认沙箱数值描述核心上下文 |
| bpffs | 挂载rw，但root所有01700目录对当前EUID 1002不可写 |
| cgroup / 非特权BPF策略 | cgroup挂载只读，没有已委派可写cgroup2；unprivileged_bpf_disabled=2 |
| load / attach | 均未尝试；BTF存在不等于加载或字段能力通过 |

当前仍无T132正向connector唯一关联结果，不能用普通proc扫描、bounding set或rw挂载推导已有BPF能力；也不据此声称宿主管理员没有能力。具体受控部署和T134协议完成证明方案见ADR008，未在本轮加载BPF、改变权限或实现质量新profile。

原正式G01身份0/100保持失败，首次真实组合探针`/tmp/hoopa-lifecycle-edy6mctq`的active RuntimeError/空trace仍保持失败、当次crash未执行；后续新前提下的pass与孤立诊断不回填旧结果或补造确定根因。T134的protocol-ready/lazy确认、两条独立线路和稳定节点历史仍未完成。正式G02/T128、SQL/GC、真实宿主断电/满盘、SSH/休眠、其他平台及产品预算均不由本轮放行，T137及P1继续等待各自门槛。
