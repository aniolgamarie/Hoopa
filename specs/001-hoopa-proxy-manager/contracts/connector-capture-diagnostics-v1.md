# 原型采集诊断 v1

本契约针对固定 Linux/amd64 内核与 executable-online handoff v2 原型。它补充已观察到的事实：在 connect 阶段被 BPF 拒绝的请求不会到达核心 admission；现有 status 的 `running`、`rejected=0`、`revoked=false` 不能代表采集仍然可用。此契约不是正式身份能力验收，也不提升 T132/T137 或 G01。

## 只读观测

`coreadmission.Owner.Diagnostics()` 返回独立的 `sourceidentity-capture-diagnostics-v1` 对象。包含原始 `handoff_schema`、十进制字符串 `generation`、`core_generation`，以及 `observation=sequential_reads`。Owner 锁只保护用户态 FD 生命周期和 admission/close 操作；内核写入者可在读取之间运行，结果不是原子快照。

四项计数分别为 `capture_faults`、`executable_fault_bits`、`raw_enter_recursion_misses`、`raw_exit_recursion_misses`。每项都有 `status` 和 `value`：

- `observed`：value 是完整 uint64 的规范十进制字符串，包括真正读到的 `"0"`。
- `unavailable`：value 为 JSON null，reason 为 `read_failed` 或 `owner_closed`。
- `not_applicable`：value 为 JSON null；仅用于仍打开的旧 handoff v1 的后三项。

`state` 由实际观测决定：任何非零计数为 `fault_observed`；没有非零但至少一次必要读取失败为 `unavailable`；所有适用读取成功且为零才是 `no_fault_observed`。独立的 `incomplete` 在任一必要读取失败时为 true，因此可以同时返回 `fault_observed` 与 `incomplete=true`。关闭的 Owner 不执行任何 FD 操作，所有计数 unavailable。

诊断不修改 map、不关闭 FD、不调用 stop、不撤销 generation、不改变 admission 计数，也不自动恢复或更新。反复读取不得改变这些状态。它不替换现有 status schema。

受保护核心通过独立 GET `/hoopa/capture-health` 提供该对象，沿用精确 loopback 来源和单一 Bearer 认证。拒绝非空 query、非零或未知 body 长度、Transfer-Encoding；其他方法不执行诊断。没有 identity Owner 时返回 503，不构造零值健康对象。有 Owner 时返回 200 和上述对象，包括故障或不可用状态。只复制 Owner 引用时持 runtime 锁，释放后由 Owner 锁保护读取；关闭后的对象仍可返回 unavailable，但不读已释放 FD。此接口为原型诊断，并非完整 hook 健康证明。

## 固定 ABI 和证据边界

`capture_faults` 为已有 8 字节值。`exe_state` 中 fault bits 为 64 字节值偏移 32 的 u64。两个 raw tracepoint 程序从 `bpf_prog_info` 读取偏移 208 的 `recursion_misses`；长度至少 216，并复核程序 type=17、原持有 program ID 和确切名称。读取失败、短值、对象身份不符均是 unavailable，不能解释成零。

固定内核源码表明 raw tracepoint 的递归跳过会增加这个计数，无需启用全局 `bpf_stats_enabled`。**LSM MAC 程序没有同样的递归跳过计数保证**，因此不提供 `capture_exe_recursion_misses`，也不从 raw 程序计数推断 LSM 完整执行。零计数不能证明所有 hook 持续存在或执行，不能消除两个 syscall hook 同时丢失等未验证风险。

## admission 的保守拒绝

handoff v2 初始接管和每次 admission 已有的两次 fault 检查，同步要求两个 raw recursion 计数可读且为零。非零或不可读按既有拒绝路径处理，初始接管失败释放已有 FD；运行期拒绝不发布身份，并使 admission revoked。此操作发生在接管/接纳路径，不能由只读诊断触发。v1 不要求新计数。

## 验收

离线检查须覆盖：零、每一项非零、完整 uint64、各读取失败/短值、错误程序 type/ID/name、部分故障加部分缺失、v1、close 后不再读 FD、重复调用不更新状态、并发诊断与 close、初始接管拒绝和 admission 前后检查拒绝。fake 的错误注入只证明错误处理，不能算真实内核递归跳过样本。

新核心集成必须使用新 patch、源码/二进制摘要和新 VM，不能借用 0012 的实际通过记录。实际负例至少证明：正常请求后观测零；scope 拒绝后读取到故障，同时旧 admission status 保持其原本含义；诊断读取本身不修复该 generation。尚未完成实际集成与运行验收时须记录为未验证。

### 本次有限 VM 登记

2026-10-24：固定 001–013 新核心 `476d57884b810e96d8b44367afbf91001bc63b6b516930a66b89d6e8dc05467b`、原组合对象 `1bf999df3affd167b06c923b7dde3ef27ec521e7e0afaf2accb3f447a6059500`，沿用 6.18.54.1 隔离内核、20FD 移交、独立无网卡/无宿主共享 VM。实验目录 `private/t132-capture-diagnostics-vm`；仅登记 `outside-source`，不能将夹具保留的其他模式算作运行样本。

一个 source 叶内 HTTP 基线正常完成 33/33 字节及提前 SHUT_WR；根 cgroup 新进程尝试目标入口 connect，预期 EPERM；随后 source 叶内新的正常进程/不同 socket 再尝试，预期 EPERM。沿用原 scope 三方 oracle、真实路径/节点/ready、7 帧完整 epoch/tail=0 和 SIGTERM 退出校验。先前 scope VM 只用于夹具参考，不计入本新核心结果。

在 ready、基线结束、负例结束、followup 结束四个稳定阶段，每次先读旧 status，读取两次 `/hoopa/capture-health`，再读旧 status。两次诊断应相等；前后 status 应相等且保留 accepted-only 原义。前两阶段四计数均 observed/"0"，后两阶段 `capture_faults` 为可解析非零累计字符串且不下降，state 为 fault_observed；本有限场景其他三计数仍 observed/"0"。不强行预定捕获故障的确切次数，也不把 raw recursion 零读数当成真实递归错误注入成功。任何不可读或阶段不符均保留失败，不放宽为通过。
