# executable-online 原始进程生命周期矩阵 v1

2026-10-24，运行前登记。固定 001–013 核心 `476d57884b810e96d8b44367afbf91001bc63b6b516930a66b89d6e8dc05467b`、组合对象 `1bf999df3affd167b06c923b7dde3ef27ec521e7e0afaf2accb3f447a6059500` 和隔离 6.18.54.1/amd64 guest。每模式独立、无网卡/宿主共享、有界 VM；不修改生产代码或原运行。迁移[旧生命周期判据](connector-core-lifetimes-v1.md)，并加强[旧早退实验](connector-core-earlyexit-v1.md)没有证明的 accept/Admit 顺序。

共同判据：20FD/v2 原接管对象、原 PID/TID/leader 启动实例、socket cookie/namespace/端点、296B executable 原始记录、同次 native connect token/BOOT 区间、独立 `/proc/self/exe` oracle 均核对。每模式预登记两个独立 flow：HTTP/alpha.test/node:a 和 SOCKS5/beta.test/node:b；每流 33/33 字节，SHUT_WR 后完整回显/EOF，精确 closed/tail=0。真实 ready/实际线路和目标字节按 token 核验；同一 core、capture generation、source epoch，最终 admitted=2/rejected=0/revoked=false，核心 SIGTERM/自有客户端 reap 均成功。只读捕获诊断在允许读取的阶段为四个 observed 零；零值不是连续性证明。严格 v4 解码，不用 oracle 给 resolver 填身份。

## family-concurrent

source 叶内同名父子分别独立 native connect。父 HTTP 流保留至子 SOCKS 流 ready；Popen/close_fds 和原 TCP CLOEXEC 保证子流自己建连。独立 PID/PPID、leader 启动实例、cookie、call token 和原 exe 快照不能相互串用。只读有界等待两个完整、摘要正确的来源 setup_finished 成功帧且没有 closed，再释放双方应用屏障。最终两个 ready 的 source_seq 都小于第一 closed；双方精确终末。此处证明连接重叠，不将顺序建连说成两个内核回调同时执行，不是压力测试。

## pid-reuse

独立 guest 初始 PID namespace 中预登记 PID 30000。确认其 `/proc` 不存在后，仅 guest 写 ns_last_pid=29999；Popen 实际 PID 必须等于 30000，抢占或接口失败均保留失败，不悄悄换 PID。第一流终末完整、wait/reap、`/proc/30000` 消失，并间隔至少 20ms 后才重复第二次。两流同 PID 而 start ticks、leader_start_boottime_ns、cookie 和 token 均不同；历史 connector/executable 原文不改，不以当前 PID 回填旧实例。只对仍持有且未 reap 的 Popen 清理，不对历史数字 PID 发信号。

## early-exit-before-admit

每流建连前，guest PID1 对自己仍持有的核心 Popen 发 SIGSTOP；有界 waitpid(WUNTRACED|WNOHANG) 实际得到 WIFSTOPPED/SIGSTOP，并记录核心 start identity 与所有现存 task 的停止状态。没有停止确认不得启动客户端。此时监听 socket/BPF 引用仍由核心持有，内核可完成普通 TCP connect，但 Go 不执行 accept/Admit。

另起 source 叶内父 writer；其子 connector 独立 native connect，记录原始 exe/socket/启动实例 oracle，经一个 SCM_RIGHTS 转交原 socket，关闭自己的副本并 exit0。父只接收 FD、核对同 cookie，不发代理或应用数据；必须 wait/reap 子、记录原 `/proc` 消失，原子写出等待恢复 marker 后继续等待。guest 核验该 marker、原 child 的实际 wait 状态和进程消失、core 仍停止、来源前缀没有此连接 opened；随后才 SIGCONT 核心并释放 writer。顺序必须有 CLOCK_BOOTTIME 时间和真实 wait/进程状态证据，不能依赖固定 sleep 推断。

恢复后父经同一 socket 完成握手和 33/33；opened 中 connector 必须为已退出的原 child 实例，不能改为 writer/core 或从存活 `/proc` 回填。原 child exit/reap 早于 core resume，原 accept/Admit 只能发生在 resume 后。两个协议各执行一次该顺序。所有异常路径先恢复自有 stopped core，再执行有界 SIGTERM/必要的自有进程清理；不能留下停止进程或把普通超时当通过。

## 只读资源记录

ready 和两流完成但 core 退出前，各保存自有核心 `/proc/<pid>/stat`、`status` 原文、CLK_TCK 和 CLOCK_BOOTTIME 采样前后，核对同一 PID/start；保存两阶段固定 20FD 中 map 的 fdinfo。短窗口 CPU 只按实际 ticks 和区间解释，RSS/HWM 是观察点，early-exit 窗口含 SIGSTOP，不能称空闲预算通过。bootstrap 已 exec 为核心，不制造一个并不存在的常驻 helper 用量。fdinfo 的 map 容量和 memlock 是内核给定的类型用量值，后者不等于包含动态 task-storage 的全部物理内存。不得为此遍历不支持 GET_NEXT_KEY 的 task-storage 或修改 map；本批不要求容量饱和。真实 Admit 内部等待没有量测接口时明确 unavailable，不以协议 ready 耗时替代。

本实验不证明 comm 原子真实性、路径长久不变、所有 callback 交错、恶意特权篡改或跨平台支持。名称/路径/正式身份能力仍 false/unverified；不改变正式 G01 新样本 0、旧 0/100，T132/T137 继续按完整任务要求判断。准备、实际执行、失败待决策、环境不足分别记录，旧材料不可改写。
