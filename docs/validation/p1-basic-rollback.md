# T030/T031：失败候选终止与自动完整回滚

2026-10-27。固定 basic 核心上的受控入口开放失败，已经走通实际自动回滚：确认旧实例
退出、终止失败候选、使用完整旧 bundle 在新目录启动新实例、核验后恢复旧指针。
这是有限组件与实际场景验收；T030/T031 的后续状态见[任务表](../../specs/001-hoopa-proxy-manager/tasks.md)，
产品生命周期矩阵仍待 T034。

实现见 [rollback.go](../../internal/control/rollback.go)、
[basic_rollback_linux.go](../../internal/control/basic_rollback_linux.go)、
[候选终止](../../internal/runtime/terminate_candidate_linux.go) 和
[新运行目录](../../internal/config/prepare_workdir_linux.go)。机器索引为
p1-basic-rollback-validation.json（本地原件 `../../experiments/gost-mihomo/p1-basic-rollback-validation.json`，不随Git上传）。

## 实际组合

使用批准的 Linux/amd64 核心 `2921c2468cf1e62099ac40a1872fbb58cc0048696a91c9dd920dc9790f27fabb`。
最终 actual-02 中，旧实例 PID 1922821、候选 PID 1922864、恢复实例 PID 1922905
具有不同启动身份和 RunID。旧配置只有一个应用入口；候选增加第二入口。候选闭态
完成核验与登记后，测试自有 `SO_REUSEADDR` socket 抢先监听第二入口，实际 activate
返回 failed，状态为 degraded。测试没有改核心或放宽凭据约束。

自动回滚先保存意图，再通过完整配置、失败回执、原始登记与实际进程身份核验候选，
使用原 pidfd 发送 TERM 并观察退出。之后逐字使用旧 bundle，创建新的随机私有目录、
RunID 和启动身份，完成闭态核验、登记、activate 和开放状态复验。最终恢复旧
revision/generation/manifest，active.OperationID 指向本次实际完成恢复的操作。

旧实例和失败候选分别有预先保留的独立 pidfd 退出观察及 /proc 消失证据；恢复实例
最终也按强身份清理。旧 bundle、旧 config 和 launcher.log 的字节与元数据保持原样。
前后共两次本地授权 HTTP echo 成功，一次无凭据请求被拒绝；竞争监听器仍存活。
同请求经公开 Apply 重放，没有再次调用 source、新增运行或改变状态树。

这一场景的首启通过公开 Apply；失败替换使用内部已接纳工作流及既有私有测试切点，
以确定抢占发生在闭态登记之后。公开入口不接受该切点。专项是 1 个真实三实例场景，
另有 1 个空 launcher helper；race/vet 通过，0 fail/skip，编译输入前后相同。
它不是实际管理器 SIGKILL、VM/BPF、磁盘断电或完整多应用矩阵。

首轮 actual-01 因测试新增入口复用凭据，被核心在启动时拒绝，没有到达目标失败点。
原诊断与日志摘要保留；当时未独立留存候选 PID/退出观察，不能补写成已证明清理。
修正为预先定义的独立凭据后运行全新 actual-02，不从核心响应反推预期快照。

## 分层验证

| 范围 | 结果 | 证明边界 |
| --- | --- | --- |
| 候选终止 runtime 回归 | 78 pass，0 fail/skip；vet 通过 | 实际自有 Go 子进程，合成控制响应；不替代 Mihomo |
| 专门的终止 Inspector | 35 pass，0 fail/skip；vet 通过 | 合成 HTTP，完整快照、状态/回执与注册绑定 |
| 实际 failed/degraded 候选终止 | 1 实际场景 + 1 空 helper 通过 | 固定核心、独立 pidfd 与 child Wait、竞争入口仍存活 |
| 新运行目录 | 33 pass，0 fail/skip；vet 通过 | 实际私有文件、容量和注入故障，不声称原生 fsync 故障 |
| control/config 回归 | control 302 pass/1 skip；config 442 pass | 合成运行顺序与实际存储；唯一 skip 为未提供固定 ELF 的材料专项 |
| 实际自动回滚 | 1 实际三实例场景 + 1 空 helper 通过 | 上述完整旧态恢复与转发；独立于回归分母 |

回归使用 `-skip '^(TestBasicApplyCore|TestBasicRollbackCore)'` 排除实际核心专项，未把排除
项计为通过。744 个 pass 和 1 个 skip 的生产源码包含最终 API 响应修正：回滚返回
`APPLY_FAILED`、`retryable:false`、outcome `rolled_back`；中间 rolling_back 的 outcome
为 pending；公开 MutationReceipt 不包含内部 run_id。后续实际夹具修正另行冻结，
不把这些记录称为同一轮测试。更早 680-pass 首启/替换记录保持原版本范围。

后续完整 Response schema 核对还发现失败响应必须为 data:null；该修正把操作关联移到
error.details.operation_id/reason，内部 phase/evidence 不变。上述已封存实际与 744-pass
记录早于这一响应修正；新结果在显式恢复阶段做定向验证，不倒填历史源码摘要。

普通 Observe/Terminate 仍拒绝 degraded。专用候选终止要求本操作的原始 closed
登记及精确匹配的激活回执，不能用任意 PID、进程名或端口占用替代归属证明。
自动回滚仅用于同一执行流程中已实际观察旧 pidfd 退出的情形。任何日志写失败停止
后续效果；持久化不确定性使 writer 失效，保留原件等待显式恢复。

## 后续边界

后续[显式恢复](p1-explicit-recovery.md)已完成独立有限验证。它分别持久化原操作和
恢复调用，处理两份记录之间的中断；管理器重启后没有 birth、进程不在或观察失败，均不能解释为已停止。
`stopped` 历史与受控重启仍由 T032、生产服务/CLI 由 T033、完整故障矩阵由 T034 验收。

主代理决定并实现事务顺序；executor 实际为 gpt-6.1-sol / medium，负责限定实现与
真实组合测试；scout 实际为 gpt-6-luna / high，独立核对固定源码、分母与证据引用。
