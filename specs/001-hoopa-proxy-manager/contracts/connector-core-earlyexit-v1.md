# 原建连进程早退后的归属保留

2026-10-24，T132 有限原型的下一批预登记，固定现有 001–009 核心、BPF/libbpf 和隔离 Linux 6.18.54.1。准备与真实执行分列，禁止用存活 writer 替换已退出 connector 的历史身份。

每流由一个独立父客户端持有 socketpair，使用自有 Popen 启动 connector 子进程；只显式移交一端 Unix 控制 FD，其余 FD 关闭。子进程继承专用 source leaf，在初始命名空间中自行建立原 TCP 连接，记录原 PID/PPID、leader 启动 ticks、BOOT 建连区间、cookie 和端点，原子保存 oracle。随后仅通过 SCM_RIGHTS 把原 TCP FD 交给父进程，关闭自身 TCP 并正常退出，不执行代理协议握手或应用负载读写。

父进程必须只接收一个完整 FD，拒绝截断、多 FD 或未知控制消息；仅清理本进程拥有的 FD。核对接收 socket 与原 oracle 的 cookie/端点一致，再 wait/reap 自己的子进程，确认退出码 0 和 `/proc/<childpid>` 消失，然后才开始代理握手。分别执行 HTTP/alpha.test/node:a 与 SOCKS5/beta.test/node:b 两流，各 33 字节、SHUT_WR、精确回显和 EOF。每条实际 node/target 的负载用预登记独立 token 核对，不能只比较字节总数。

最终严格解码 v3 原始事件，确认原已退出子进程的 PID、启动实例、cookie 与同 flow 的实际线路、ready、连接段和整数终末一致；父进程作为当前 writer 单列。此实验只证明 connector 在协议/负载开始前退出，不宣称核心在准入查询前才看到其退出，也不证明任意 namespace、FD 共享或并发调度。

输入、两流 token、有限超时和结果判据在运行前固定。默认无显式执行开关时不创建网络连接；只在无网卡、无宿主共享的私有 VM 执行，失败保留、不改变旧候选或正式 G01 分母。测试 oracle 不是核心 resolver 输入，字段 capability 不因有限实验自动升级。
