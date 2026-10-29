# 建连进程名一致性的可行性与剩余条件

2026-10-24，名称所需的挂接与原子指令已在隔离 Linux 6.18.54.1/x86_64 VM 通过有限验证；**尚未形成可认证的进程名快照方案**。现有 connector 的 PID/leader 启动实例与 comm 字段能力仍分开，名称不用于已认证的进程规则。见[探针契约](../../specs/001-hoopa-proxy-manager/contracts/connector-name-atomic-feasibility-v1.md)与机器记录（本地原件 `../../experiments/sourceidentity/name-atomic-validation.json`，不随Git上传）。

75 项离线检查后，真实 VM `vm-name-atomic-byp2ft31` 从实际内核 BTF 解析 `__set_task_comm`：FUNC 30836、PROTO 30835，参数为 tsk/buf/exec。两个 fentry/fexit 和一个 connect4 程序均加载、挂接并产生 JIT 代码，长度分别为 1134、1094、254 字节。两次自有改名得到 entry=exit=2、sequence=4、active=poison=0；一次无 listener 的 loopback connect 得到 ECONNREFUSED、connects=1。三个程序的递归漏报计数均为 0。逆序销毁 links 后分别确认 FD 关闭，关闭对象后确认五个 map/program FD 为 EBADF，关闭 cgroup FD 并恢复自有 comm；BTF 只记录 free 调用，不声称证明了内存回收。导出材料与内核日志均通过检查。

拟研究的读写保护使用一个 64 位原子字段组合 sequence/active，进入及退出改名都更新 sequence，读取名称前后比较同一字段，同时检查持续故障标志。这样可避免分开读取两个计数时把不一致状态拼在一起。纯有限模型枚举六组共 125,216 个顺序一致交错、16 项单元检查：完整观察的一/二 writer 情形没有接受不稳定快照；分离计数的简化方案和完整遗漏 entry/exit 的方案都有实际反例。两 writer 可能留下稳定但混合的内核字节图像；模型不承诺它一定等于某一个调用者期望设置的名称。

模型中的补充漏报查询假定能读到所有 CPU 上、在名称写入前已经累计的精确计数。这只是强假设，当前内核 API 不能直接等同于它。固定内核源码提供了以下限制：

- 该版本 `__set_task_comm` 的名称复制没有供外部 BPF 读取者共同持有的锁；单次读取或两次相同读取不能自行证明一致。tracing BPF 不能使用 `bpf_spin_lock`，所以没有采用该锁作为名称方案。见[固定 exec 源码](https://github.com/microsoft/WSL2-Linux-Kernel/blob/d504d40ab83839cb61942a812a99d183c42227b9/fs/exec.c)及[固定 verifier](https://github.com/microsoft/WSL2-Linux-Kernel/blob/d504d40ab83839cb61942a812a99d183c42227b9/kernel/bpf/verifier.c)。
- x86 JIT 将 64 位 cmpxchg 降为带 LOCK 的指令；本次实测只证明可加载和有限执行，没有完成名称复制、记录发布与后续查询的完整内存序论证。见[固定 x86 JIT](https://github.com/microsoft/WSL2-Linux-Kernel/blob/d504d40ab83839cb61942a812a99d183c42227b9/arch/x86/net/bpf_jit_comp.c)。
- 普通成功加载程序具有 per-CPU stats，递归漏报累计不依赖运行耗时统计开关；但 x86_64 的 `u64_stats` 重试包装是 no-op，各 CPU 的计数读取不是全局原子快照。早期私有笔记中“每 CPU tuple 会重试到一致”的表述已据固定源码更正。见[stats 分配](https://github.com/microsoft/WSL2-Linux-Kernel/blob/d504d40ab83839cb61942a812a99d183c42227b9/kernel/bpf/core.c)和[64 位 stats 定义](https://github.com/microsoft/WSL2-Linux-Kernel/blob/d504d40ab83839cb61942a812a99d183c42227b9/include/linux/u64_stats_sync.h)。
- ftrace 通用列表分发还可能在 BPF 回调之前跳过调用，不增加 BPF recursion_misses；全局 ftrace 关闭时也可能存在成功注册但调用点未启用。sole-direct、已完成挂接、全局启用等条件仍需要运行证据和持续性约束，不能只看某一时刻计数为零。见[固定 ftrace](https://github.com/microsoft/WSL2-Linux-Kernel/blob/d504d40ab83839cb61942a812a99d183c42227b9/kernel/trace/ftrace.c)和[递归过滤](https://github.com/microsoft/WSL2-Linux-Kernel/blob/d504d40ab83839cb61942a812a99d183c42227b9/include/linux/trace_recursion.h)。

[隔离开关反例](../../specs/001-hoopa-proxy-manager/contracts/connector-name-coverage-control-v1.md)现已完成 102 项离线检查及一轮实际 VM，见独立机器记录（本地原件 `../../experiments/sourceidentity/name-coverage-validation.json`，不随Git上传）：

| 阶段 | guest ftrace | 实际 comm | entry / exit | sequence | recursion_misses |
| --- | --- | --- | --- | --- | --- |
| 初始两次改名后 | 1 | hoopa-atom-b | 2 / 2 | 4 | 0 |
| 关闭后第三次改名 | 0 | hoopa-atom-c | 2 / 2 | 4 | 0 |
| 恢复后第四次改名 | 1 | hoopa-atom-d | 3 / 3 | 6 | 0 |

三个阶段保持相同 program ID 5/6/7，active/poison 始终为 0。enabled_functions 在初始/恢复时显示目标 `(1) R D M` 及 direct trampoline 行，关闭期间为空；完整原文分别保留，没有把端点快照解释为全过程唯一直接调用证明。实验最终恢复 guest 开关为 1，原有清理检查全部通过，六份导出文件摘要复核。**漏改名期间既没有 poison，也没有递归 miss**；因此后续设计不能仅靠最后读取零计数，或开关前后值相同，来推断覆盖连续。该实验没有改变宿主 ftrace，也没有实际演示通用列表递归跳过。

固定提交的官方完整源码现已另行下载并提取 C/H/S/Rust 文件用于所有写路径审查，档案摘要与提取清单保存在私有研究目录；源码版本一致仍不等于已证明该二进制可复现构建。完整名称方案还需处理内核相关写入路径、挂接前正在执行的 setter、启动/撤销边界、溢出及 CAS 耗尽、并发改名和覆盖丢失；当前指令探针、顺序模型和反例不能关闭这些条件。未对现有核心或 online BPF 添加名称认证逻辑，T132/T137、139/21、正式 G01 新样本 0 和 P0/P1 门槛保持。
