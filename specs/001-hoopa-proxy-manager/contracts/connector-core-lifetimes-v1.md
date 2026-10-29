# 同名父子并发与 PID 复用的有限核心矩阵

2026-10-24，T132 下一批预登记。使用已固定 001–009 核心、原 BPF/libbpf 与隔离 Linux 6.18.54.1，准备和实际执行分列；不修改旧运行、正式分母或产品能力。

## 同名父子独立连接

父客户端在专用 source leaf 建立自己的 HTTP CONNECT 流，访问 `alpha.test` / `node:a`；随后用持有所有权的 Popen 启动同名 `python3` 子进程。`close_fds=True`，原 TCP FD 为 CLOEXEC，子进程独立建立 SOCKS5 流访问 `beta.test` / `node:b`。双方记录 PID、PPID、leader 启动 ticks、实际 socket cookie/namespace/端点；子 connector 必须是子 PID，不能继承父 flow 的归属。

两个客户端完成各自握手后在私有 marker 屏障等待。guest 只读、有界核验两个预登记 cookie 对应的完整来源 `setup_finished` 成功帧均已可见，且尚无 closed，才释放屏障。活动文件可见不是 fsync 证明；宿主最终严格解码状态机并要求两个 ready 的 source_seq 均小于第一条 closed，证明实际连接重叠。每流各收发 33 字节并完成 SHUT_WR、精确回显与对端 EOF，独立 node/target payload 按各自 token 对齐。

## 同 PID 的不同实例

另开单独 VM，预登记初始 PID namespace 中目标 PID 为 30000。仅在该隔离 guest、确认目标 `/proc` 目录不存在后，将 `ns_last_pid` 设为 29999，再启动自己持有的客户端。必须实际观察 Popen PID 为 30000；抢占、权限或接口不满足均使该 run 失败，不能静默换 PID 或重试来改变分母。

首个 HTTP/alpha 流完成精确终末、客户端 wait/reap 且目标 `/proc` 目录消失后，至少间隔 20 ms，再按相同规则启动第二个 SOCKS5/beta 流。两流处于同一核心、同一来源 epoch 和同一 capture generation；客户端 PID 相同，但启动 ticks、BPF leader_start_boottime_ns 和两端 socket cookie 必须证明是新实例/新连接。原实例历史保持，不能按当前 PID 查找回填旧 flow。

所有预期、模式、token、固定 PID 和输入摘要在实际运行前登记；没有显式实验开关时不创建网络连接，不操作宿主 sysctl、PID 或 cgroup。Popen 清理只作用于尚未 wait/reap 的自有对象，不依据历史数字 PID 发送信号。新测试 oracle 不输入核心 resolver。

每场景仅两条预登记流，不能称为压力测试或覆盖所有 PID/tuple/namespace 生命周期。实例、名称和路径能力仍分字段保留当前状态；并发改名一致性、其他复用/缺失/范围故障继续按 T132 原要求验证。
