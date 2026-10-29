# 组合核心的原连接归属边界 v1

2026-10-24，运行前登记，T132 的有限子步骤。使用[001–012固定候选](../../../experiments/mihomo-events/core-executable-quality-candidate-manifest.json)、组合对象 SHA256 `1bf999df3affd167b06c923b7dde3ef27ec521e7e0afaf2accb3f447a6059500` 和固定6.18.54.1隔离guest。沿用[组合契约](connector-executable-online-v1.md)的20FD资源接管、v4字段、可信独占loader、世代拒绝策略与能力限制。每个场景新VM、新资源世代，不修改宿主内核、cgroup或sysctl。

默认归属始终为发起原始TCP connect的进程实例。应用协议读写、FD传递和后续exec不得改写原始PID、启动身份、调用TID、socket cookie及建连时可执行文件快照。独立oracle仅用于事后核验，不作为核心准入或选路输入。

| 场景 | 实际操作与必需断言 |
| --- | --- |
| `thread` | 原connect在native worker线程执行；TGID/PID为leader，caller TID必须不同；296B exe记录同样绑定该TID，writer PID仍为原进程。 |
| `fork-writer` | 父进程完成connect及oracle，再fork让子进程握手/写入；writer PID不同，opened保留父进程坐标。 |
| `scm-writer` | 父进程完成connect及oracle，再经SCM_RIGHTS传递；子进程先关闭继承的原TCP FD才recvmsg，收到的cookie/netns/endpoint与原socket一致；opened仍归父进程。 |
| `rename-after-connect` | connect时comm为`hoopa-before`，随后PR_SET_NAME改为`hoopa-after`；原comm观察不改，可执行文件仍为`/bin/python3`。此操作不是文件路径改名，也不是comm原子性证明。 |
| `exec-fd` | connect后保留原socket，exec到`/work/python3-resume`再握手/写入；该文件是`/bin/python3`的逐字节副本，路径及inode不同。PID/start/cookie保持，单独记录`executable_resume`，opened必须仍是原`/bin/python3`快照；exec后的信息不能覆盖原oracle。 |

每次原connect之前、成功返回之后均由发起方读取`/proc/self/exe`原始路径字节及stat dev/inode，记录BOOT和monotonic窗口。两次oracle的对象/path/basename必须相同，296B记录中的exe采样时间必须落入原connect BOOT窗口，且满足leader_start≤exe_boot≤connect_boot。原raw cookie/TGID/TID、consumed=1、正整数call_token、路径/NUL及独立UTF-8字段严格核验；不同已准入连接必须具有不同token。thread在worker中完成这组采集；fork/SCM在传递前固定；exec在替换镜像前持久保存原oracle。

exec另存resume时的路径/dev/inode与时刻，必须位于原connect之后，路径为新副本且inode不同；原镜像及副本SHA256相同只说明运行同版本解释器，不表示没有exec。只有已校验的原socket FD在exec期间继承，resume恢复CLOEXEC、核对原PID/start与socket身份。禁止以当前writer/进程表的exe回填历史。

每个场景两个独立客户端，HTTP→alpha.test/node:a，SOCKS5→beta.test/node:b；每流实际传输双向33字节，记录真实上游ready、同attempt/actual_connection_ref与精确终末，完整epoch期望13帧、无gap。真实代理与目标记录必须独立匹配token/目标/节点；统计结果要求admitted=2/rejected=0/revoked=false、连接gauge归零。普通流延续现有受控fixture收发顺序；提前半关闭已有同制品正向单独验收，不伪称本批全部覆盖提前半关闭。

各VM必须完成核心SIGTERM0、v4严格解码与只读重放（两个closed流、tail=0）、原始导出摘要检查。准备测试涵盖模式分支、错误原PID/TID/cookie、exe错路径/时间/token/消费、转交标记、resume信息覆盖、实际线路与终末缺失的拒绝；准备/fake数量与实际VM/flow分别计数。源码或夹具更改后新建image/run，旧失败和原始材料不覆盖。

这五项不证明所有线程并发、任意FD共享时序、路径并发rename/unlink、PID/tuple复用、跨namespace、TFO、缺钩子、长期回收或生产权限部署。旧001–009/0010的相应结果不能借给本制品。正式identity/name/path能力仍false/unverified，PROCESS规则继续拒绝，T132/T137保持未完成，正式G01新增0、旧0/100与P0/P1门槛保持。
