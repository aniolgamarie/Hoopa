# 名称钩子覆盖条件的隔离反例

2026-10-24，接续名称原子指令可行性探针。固定源码显示 ftrace 全局开关及通用分发可能跳过钩子而不增加 BPF recursion_misses；本实验只验证其中可控制的全局开关反例，保存实际调用点信息，不证明完整覆盖。

在新的无网卡/无宿主共享 VM 内，复用相同 atomic BPF 对象、固定 kernel/libbpf 和专用 PID1。挂接两个名称 tracing 程序和一个 connect4 程序；仅操作该 guest 的 `/proc/sys/kernel/ftrace_enabled`，先确认值为 1。挂载 guest tracefs，只读保存 `enabled_functions` 中目标 `__set_task_comm` 的原始行，不能依据猜测的输出格式认证 sole-direct。

预登记顺序：两次自有 PR_SET_NAME 及一次向无 listener 的 loopback connect，得到 entry=exit=2、seq=4、active=poison=0、connects=1、ECONNREFUSED。将 guest ftrace 开关置 0 并读回；第三次改名必须实际成功，但状态完全不变，三个原程序的递归漏报计数仍为 0。恢复开关为 1 并读回；第四次改名必须使 entry=exit=3、seq=6，其他状态不变，程序 ID 保持，漏报计数仍为 0。各阶段保存原始程序信息、fdinfo、开关值和调用点行。

无论成功或失败都尝试恢复 guest 开关原值、逆序销毁自有 links、关闭 map/program/cgroup FD 并恢复自有 comm。使用独立 virtio 证据通道及普通 UART 内核日志，失败材料完整保留。运行前必须通过默认拒绝、状态/阶段篡改、真实 BTF 独立解析和资源清理的离线检查；不从宿主执行探针或写其 sysctl。

成功只证明这一反例：已创建 link、最终仍持有同一 program、且 recursion_misses=0，仍可能存在漏观测的真实改名。它不验证 generic-list 的递归反例、不证明名称快照一致性、不接入核心，也不提升 identity capability 或正式 G01 样本。后续设计必须另行解决覆盖前提及其持续有效性，不能仅靠在最终时刻读取一个零计数。
