# Source5 正式恢复验收

2026-10-26，固定0017候选的 **T128受限验收通过**：先登记全部六组、八场VM、16个原应用槽，再逐场执行，16/16通过。273份规范导出经独立复核；两项实际来源私有副本恢复各race 1通过、0失败、0跳过。最终只读Go总入口也是1通过、0失败、0跳过。正式报告（本地原件 `../../experiments/mihomo-events/source5-recovery-formal-validation.json`，不随Git上传）、[主代理验收](../../experiments/mihomo-events/source5-recovery-acceptance.json)与总入口执行记录（本地原件 `../../experiments/mihomo-events/source5-recovery-gate-validation.json`，不随Git上传）分别保存。T013消费同一批证据完成[G02评审](g02-counters.md)，项目140项完成29项；T129继续，P0/P1未放行。

核心SHA256为`5693d4ac2bda5fcae7e5ac037d23db1b2a48049f681b8c825b8b4db12ac1b967`，与T127身份验收相同。拓扑仍限已验证的Linux amd64、受控loopback及专用cgroup；所有BPF、ptrace、挂载及故障操作都发生在无NIC、无宿主共享的隔离VM。每场最多2CPU/1GiB/150秒。原失败、准备样本及其分母保持原义。

| 正式场景 | 原应用槽 | 实际结果 |
| --- | ---: | --- |
| 离线后首次closed持久接收、丢ACK | 2/2 | 两流各33/33；同原文重投沿用receipt，不重复累计 |
| 离线后首次closed在接收前503 | 2/2 | 首投无receipt；后续原文重投完整接收 |
| opened持久接收后阻塞ACK | 2/2 | 未释放ACK时转发继续；同opened重投后沿用receipt |
| 采集器和核心依次SIGKILL、重开 | 2/2 | 旧流可信12/12、无final；六条旧事件保留receipt1–6，新流独立33/33 |
| source真实ENOSPC | 2/2 | 实际write912→92，再write820→ENOSPC；原连接实际17/17，可信来源仍12/12；新流被拒绝且无source flow |
| 全局逻辑预留200000字节 | 3/3 | 第一流12/12保持时拒绝第二流，第一终末仍准确；释放后第三流33/33 |
| 活动连接正常TERM | 1/1 | 12/12检查点及receipt后TERM；实际closed12/12、complete且unfinished0；客户端仅recv并观察EOF |
| 节点额外前缀及同ID异原文冲突 | 2/2 | 每流33up/41down，target33/33，节点额外8字节；异原文409无receipt，原文204沿用receipt，journal不变 |

主代理另对九份来源文件的93条完整原帧核验长度、SHA256、连续source_seq、累计单调及连接段守恒。14条实际source flow中，12条有精确closed，2条无final；另外两个被拒绝的应用槽没有source flow，不能填成两条零流量记录。TERM及配额第一流的终末减最后检查点均为0，重复终末也不能新增增量。此核验是原始数据审计，不是SQL事务实现。

重启场景的只读原件仍显示pending12/12、final/tail为null。固定源码在该实际输入的私有副本上持独占writer锁、完整扫描并创建新私有epoch，三次得到相同`epoch_ended_without_final`缺口标识和fact hash；已closed的新流一直保持33/33。原始文件没有写入，也没有把派生恢复报告伪装成来源事件。

ENOSPC原件共8192字节，确认前缀8100字节、partial尾92字节；接收journal仅包含六条已确认原文。对该实际原件的私有副本，三次`openSource`均返回`ErrSource`，不截断、不生成新epoch。实际complete尾的成功重开未在本次VM观察，也没有自动修复保证。客户端发送成功不等于代理已转发：被拒绝的SOCKS连接完成协议回复后曾尝试发送33字节，但没有node:b或来源流。

固定源码的20项race边界检查另列：5项来源/注入故障，15项预读、initial write及未知外层证明。覆盖大于2^53的整数、窗口配额耗尽、partial/full Write与Sync不确定、实际n保留和lazy未知；这些不是额外正式VM样本。实际buffer客户端预读字段均为空，不能声称命中了非空预读分支。实际TERM选择closed分支，另一shutdown gap分支仍只在合成输入覆盖。各场退出marker本次收到，不等于通用退出交付保证。

前两场transport driver退出2表示该场2/2已通过、同组尚有未运行槽；最后一场完成后组汇总为6/6。第一场root结果曾引用变化中的组汇总，原字节已保存为独立快照；原结果、快照绑定和退出码均保留。准备阶段的夹具/打包失败及日志覆盖缺口、overlay登记诊断遗漏帧摘要、源码树排序比较差异也已记录，未重跑或改判正式样本。

源码/探针与overlay执行由executor（gpt-6.1-sol / medium）承担，稳定原件独立复核由scout（gpt-6-luna / high）承担；主代理负责登记、VM、恢复语义和最终接受。SQL驱动及事务、生产gap/GC、质量线路T129、SSH/休眠、其他平台与性能预算仍按后续任务验收。
