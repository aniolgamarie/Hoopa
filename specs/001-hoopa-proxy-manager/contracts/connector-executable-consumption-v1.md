# 可执行文件快照只消费一次的有限修复

2026-10-24，研究候选。原 `a2692e…acff1` 对象和同 socket 重试反例保持不变。主代理另制新对象，在同为 288B 的记录中，将 offset20 的保留 u32 明确定义为 `consumed`，初始 0；gate 完成 cookie、PID/TID、路径返回与 fault 检查后，使用一次原子 CAS 将 0 改为 1。只有成功从 0 改为 1 的执行可以计入 allowed；已消费记录计入 refused。文件身份/路径 bytes 不被更新，也不由当前 proc 回填。

该修复只解决已到达 gate 的成功快照被重复使用，不能据此声称覆盖“捕获完成但此前未到达 gate”的记录、任意 LSM 失联或所有旁路调用。它仍是独立有限研究对象，不接核心，不认证正式名称/路径能力。

实际验收分两轮、各自新 VM 和预登记，不覆盖原反例：

- **consumed-retry**：完整复现[原重试契约](connector-executable-socket-reuse-v1.md)的相同 FD/cookie、解除 LSM、同对象改名和 AF_UNSPEC 重置；三次目标 connect 应为 ECONNREFUSED/EPERM/EPERM。状态为 `(1,1,1,0,0)`、`(1,2,1,1,0)`、`(1,3,1,2,0)`；唯一记录 `consumed=1` 且其余值和历史原文不变，新 cookie 无记录。文件名、comm、两个 socket 和程序/map/link FD 均清理。
- **consumed-fresh**：复现[最小四阶段契约](connector-executable-snapshot-spike-v1.md)，每次新 cookie；结果仍为 EPERM/ECONNREFUSED/ECONNREFUSED/EPERM，原四组累计状态不变。两条成功记录均 `consumed=1`，改 comm 前后 exe 路径匹配独立 oracle。这样避免把永久拒绝一切当成修复。

使用新对象的严格摘要和 ABI/profile 标签，旧记录的 reserved=0 不得被新检查器当成 consumed=1。运行前只补受影响消费、计数、errno、记录篡改与两模式默认拒绝检查，并复用原严格 transport/BTF/map/FD/清理验证。全部来源、准备与实际失败分别记录，主代理审查稳定 diff 并执行 VM。该 CAS 的编译成功不是实际 verifier/JIT 或并发证明；正式接入仍需冻结全路径证据时序。
