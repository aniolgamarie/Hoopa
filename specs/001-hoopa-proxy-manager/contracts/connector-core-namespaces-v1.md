# 范围外命名空间的原连接拒绝

2026-10-24，T132 现有初始命名空间有限候选的预登记。固定 001–009 核心与原 BPF/libbpf，不扩大已支持范围。两个场景各用独立、无网卡/无宿主共享的 Linux 6.18.54.1 VM。

- `pid-namespace`：guest 自有客户端 wrapper 先进入准确 source leaf，再通过自有 `unshare --pid --fork` 子进程在新 PID namespace 尝试原 TCP。socket 仍属于初始 network namespace；记录 `/proc/self/ns/pid`、NSpid、子进程可见 PID 与相同 netns cookie，证明确实超出初始 PID namespace。BPF 应在实际 connect 阶段返回 EPERM，未发送代理协议或负载，核心无 admitted、无 opened/flow，目标与两节点无连接。
- `net-namespace`：guest 自有 wrapper 在准确 source leaf，以 `unshare --net` 子进程创建新 network namespace，只在该 guest 子命名空间启用 loopback。PID namespace 保持初始，socket 的 netns cookie 和 namespace inode 必须不同于核心。对同一 127.0.0.1:18993 的实际 connect 必须为 EPERM，不能把没有 listener 的 ECONNREFUSED 或超时算成 BPF 范围拒绝；同样要求无代理/负载、无 admitted/flow/目标连接。

只由 guest 的 Popen 对象负责启动和回收自己的 wrapper/子进程，不按未知或历史数字 PID 发送信号。工具不可在宿主执行 unshare、mount、sysctl、BPF 或 cgroup 修改；没有显式隔离实验开关时不创建网络连接。输入、模式、namespace 条件、errno 和零流判据在 VM 启动前固定。内核不支持 unshare 或权限不足是未完成的实际失败，不静默换方法后覆盖本次结果。

这两项只验证当前候选对上述范围外原连接的拒绝，既不提供跨 namespace 身份支持，也不替代 user/time namespace、配置缺失、覆盖连续性、长期回收或任意并发矩阵。完整来源严格解码与只读恢复仍需校验，旧候选/旧证据/正式 G01 分母不改。
