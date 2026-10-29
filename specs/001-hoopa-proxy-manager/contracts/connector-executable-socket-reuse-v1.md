# 同一 socket 重试与旧可执行文件记录反例

2026-10-24，T132 可执行文件补充研究。前两轮仅验证全新 cookie；不得由此推断同一个 socket 重新 connect 时一定产生新快照。本轮保持同一 BPF 对象不变，主动验证旧记录能否在 LSM 已解除后被再次用于准入。目标是定位缺口，预期反例成立也不表示方案可发布。

独立无网卡/宿主共享 VM，唯一 PID1 进程，模式 `executable-socket-reuse`。对象配置、程序、map/FD 属性、BTF、通道及清理沿用先前严格检查。只操作当前测试 Python 文件和自有 socket。原始路径 `/bin/python3`，目标 `127.0.0.1:18993` 无 listener。

1. 挂接 gate 与 LSM，创建一个 AF_INET/SOCK_STREAM socket，保存 cookie。第一次普通 connect 达到 ECONNREFUSED，保存唯一 `/bin/python3` 快照，状态 `(captures,connects,allowed,refused,fault)=(1,1,1,0,0)`。
2. 解除 LSM link，保留 gate。将当前二进制 rename 为 `/bin/hoopa-exe-retry`，以 `/proc/self/exe` 的 readlink 与 stat 核对新路径和同一 dev/inode，comm 不变。
3. 在原 FD 上通过 libc `connect` 传入 16 字节 sockaddr、family=AF_UNSPEC、其余清零，重置 TCP 状态。保存返回值/errno、SO_COOKIE、SO_ERROR 和 TCP_INFO state；要求返回 0、同 cookie、TCP_CLOSE，BPF 状态及记录不变。固定源码 af_inet.c 的 AF_UNSPEC 分支调用 disconnect 后直接返回，在 pre_connect 之前；这不是目标连接，不能把它计作 gate 运行。
4. 仍使用原 FD，再次普通 connect 至同一目标。预期当前对象再次放行、结果 ECONNREFUSED、状态 `(1,2,2,0,0)`，但唯一记录字节与第一次相同，路径仍是旧 `/bin/python3`，独立 readlink 则为新路径。若预期未出现，保留实际结果并分析，不能换用新 socket 或删去失败阶段。
5. 再创建一个全新 socket，cookie 必须不同，LSM 仍解除，普通 connect 应 EPERM，状态 `(1,3,2,1,0)`，新 cookie 无记录。这样区分“新 cookie 缺证据拒绝”与“同 cookie 复用旧证据”。

将本轮分类为候选设计反例；它不改变原先限定全新 socket 的通过记录，也不能计为完整快照方案通过。保存原 FD 的所有 cookie、目标、时序、raw 记录、文件操作和返回值。没有数据发送或代理转发。关闭两个 socket、销毁 gate、关闭对象/FD，恢复自有文件名/comm；所有文件操作仅在隔离 guest 内，失败也必须导出。

如果反例成立，下一实现必须明确每次 connect 的证据消费与 socket 重试策略，至少阻止已被准入使用过的记录再次放行；这项最小修复仍不能独自证明“之前捕获但未到达 gate 的未消费记录”或所有旁路调用安全，相关边界需继续单独决策。主代理负责消费/并发语义，执行代理只准备此固定原对象的实验，不擅自修改核心或 BPF。
