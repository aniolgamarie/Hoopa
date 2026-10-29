# 基础运行生命周期 v1：T032 接线约束

2026-10-27，root。此契约定义模块行为；实际通过范围另由验收记录说明。

## 分层与请求

`internal/runtime/lifecycle.go` 只观察强身份核验时保留的原 pidfd 退出；不重开 PID、
发信号、回收子进程或运行重启策略。控制层的 `BasicApplier.Stop/Start/Recover` 使用
Coordinator 同一控制锁，负责操作身份、运行意图和 active 发布。runtime 不反向导入 control。

所有生命周期变更先解析并查找已接纳身份。相同 request_id/内容返回持久结果，即使
调用 context 已失效；不同内容复用 ID 拒绝。新 stop 参数精确为 expected_revision 与
accept_disruption=true，新 start 参数精确为 expected_revision 与 control_only=false。
过期、未知历史、未完成/degraded、容量或暂存冲突，在本次接纳前拒绝。

control-only 不进入 Start 变更路径，由 T033 的服务层只观察。Coordinator.Close
等待当前控制调用结束并释放 writer，不主动终止核心；service.stop 的请求/退出接线
由 T033 完成，不能在持有控制锁时递归 Close 或等待需要该锁的 worker。

## 显式停止

从 running active 的发布操作、generation、manifest 找到恰一已登记运行；PID 缺失
不是已经停止。新操作记录目标 revision，但不立即改变 active。依次：

1. prepared；validated/stop-intent-durable 保存 desired_runtime=stopped、完整 base、
   SourceRunID 和本 stop 操作身份。未完成 stop 阻止任何自动重启。
2. applying/stop-original 持久后，新鲜核验原实例并保存本 stop 的 open registration，
   再通过原 pidfd TERM 与退出观察；不按数字 PID 发送信号。
3. 验证源 intent/birth/registration 原件与唯一关联，保存 SourceStopped=true。
   verifying → publishing → 新 revision/stopped active → 再核验退出原件 → committed。

停止保留 generation、manifest 和不可变配置。保存退出事实后不能再通过缺进程查询
重新推断它；事实须始终配套完整身份原件。观察到退出但尚未持久事实时崩溃，恢复
没有原句柄且不能重证活核，则 degraded，不凭 PID 消失提交 stopped。

## 停止后启动

首次配置由 Apply 完成；空状态不能启动。显式 Start 消费 active 指向的已提交 stop，
完整核对其停止事实和原件，读取最后已提交 bundle，不重新下载、编译订阅或调用
材料 Source。新 running 意图产生新的 revision，generation 和配置内容不变。

先持久新 launch-48hex 目录名，再独占准备工作目录；生成新 nonce/RunID，持久
RunIntent 后仅消费内存中的一次性票据。闭态启动、完整核验、注册、记录 activate
意图、认证开放及开放核验后，按 publishing → active → 再核验 → committed 发布。
不复用旧目录/日志，也不从磁盘重建已丢失的启动许可。

运行 active 的 OperationID 是该运行实际发布操作：可为 config.apply、runtime.start
或下面的受控 restart。停止 active 的 OperationID 则指向停止操作，不把它当成运行
创建操作。生命周期 evidence 显式关联 SourceRunID、退出证明操作和 TargetRunID。

## 显式恢复

沿用独立 operation.recover 请求与原操作分别持久的规则。恢复调用先记录 applying，
再推进目标；完成原目标后才分别完成本次与先前准确关联的恢复调用。

- runtime.stop 若仍能新鲜证明原活核，可在原 stopped 意图下完成停止；若已有持久
  退出事实，则复核原件并补 active/终态。没有原句柄且退出未证则 degraded。
- runtime.start/restart 若已产生可证明的原实例，只接管该实例。closed 情况先保存
  恢复阶段，再核对完整身份/快照、登记并 activate；open 情况核验原注册与精确激活
  回执。两次开放证明一致后才完成原发布/终态，恢复过程中不 spawn。
- RunIntent 已持久但 TargetRunID 尚未写入步骤、出生证明缺失、启动/退出未知等
  窗口，保持 degraded。不重建票据，不把未找到进程解释为可再次启动。
- config.apply 的已有恢复分支仍只收敛已开放目标/回滚；不能借本次 runtime.start
  的正例宣称所有 Apply 崩溃窗口都已支持。完整产品矩阵在 T034 验证。

runtime.stop/start 的正文暂不允许 Journal.Compact；后续启动及恢复依赖其原始步骤。
普通 apply 的终态压缩契约不变。未建立引用保留策略前不删除生命周期证据，压缩也
不会释放去重身份容量；容量满仍拒绝新请求。

## 一次授权重启

`AuthorizeRestart` 是内部服务显式授权入口，不是新的 JSON 操作，也不从查询、
control-only、Recover 或管理器初始化自动调用。必须先强核验当前已开放原核。
每个 Coordinator 生命周期最多授予一次 watch；关闭 watch、显式 stop/start、预算
耗尽、复制 Coordinator/applier 都不能重新获得授权。新管理器不从磁盘重建 watch；
只有新的显式用户授权范围才允许由服务再次调用此入口。

watch 的值副本共享句柄、串行执行锁和一次预算。Attempt 不持控制锁等待原 pidfd
退出，因此 Stop 可以先完成。退出后进行一次 100 ms 有界退避，再取得控制锁，
确认 active 完整指针仍等于授权时的 running base、所有历史/运行来源完整且无
pending/degraded；任何差异拒绝。预算在可能持久的接纳之前消费，失败不补额度。

重启使用原 bundle、新目录、新 nonce/RunID，与显式 Start 共用完整门控路径。
记录为内部生成独立请求身份的 runtime.start，evidence.mode=restart，TargetRevision
为 null：未改变用户运行意图或配置，因此 revision/generation 不前进；active 的
发布操作改为这次重启。记录用于重放和显式恢复，不会恢复内存重启预算。

成功后 watch 保留新核的强核验句柄。再次退出只观察并返回 RESTART_BUDGET_EXHAUSTED，
不写记录、不创建更多运行。ExitObserved 只表示该具体 run 的历史退出事实，不是有
其他 pending 候选时的全局“核心已停止”结论。

Close 只关闭观察句柄，不杀核心。它能唤醒正在等待退出的 Attempt；若已进入串行
启动阶段，则等待该有界启动调用结束，不假装已经撤销运行效果。服务退出应先取消
worker context、在控制锁外 Close watch，再关闭 Coordinator，不建立锁顺序倒置。
