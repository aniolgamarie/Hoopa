# 有限在线原始连接准入验收

日期：2026-10-24。状态：在线准入及四个身份边界场景已取得真实允许与拒绝证据，**不是生产核心集成或 T132 整体完成**。接续[隔离两流校准](isolated-linux-lab.md)，实现边界见[运行前冻结的在线实验契约](../../specs/001-hoopa-proxy-manager/contracts/connector-online-spike-v1.md)，结果汇总见机器摘要（本地原件 `../../experiments/sourceidentity/online-validation.json`，不随Git上传）。原 G01 的 0/100、历史失败及正式分母不变；139 项任务仍完成 21 项，T132/T137 与 P0/P1 不放行。

## 改变的行为与依据

上一轮独立 observer 被 SIGKILL 后，诊断 fixture 仍可通信，最后才拒绝材料。本次新增单进程实验服务：它同时持有 listener、原 accepted socket、两个 BPF link 和 map。授权前不读取客户端 token；只按自己接受的原 socket 的 cookie/netns/端点查询同步绑定。正常退出先撤销新准入、关闭 listener，再停止 writer 和释放 link/map。SIGKILL 直接终止该入口，没有另一个依赖遗留 marker 的转发进程。

新 `online` profile 将两条 link 挂到**独立 guest 的 cgroup2 根**，配置 source cgroup 则为专用叶目录。C 阶段在目标 connect 完成前检查实际调用进程 cgroup、socket namespace 和捕获结果；范围外目标连接返回拒绝。A 以自身 cookie 核验 C 后发布 tuple 索引，P 以原 accepted cookie 发布绑定。新增 map 只插入，冲突和缺失拒绝后续准入，不做删除、覆盖、过期复用或猜配。旧 capture/lifecycle/gated/namespaced profile 和诊断 observer 不获得在线授权能力。

这一设计依赖固定普通 IPv4 TCP 路径：active 回调同步先于最终 ACK；服务端队列可以先出现 child，但 `inet_accept` 返回 FD 前须取得 child socket 锁，普通 fresh-child 路径在 passive 回调后才释放该锁。hash 新条目在完整填充后发布，NOEXIST 在 bucket 锁下判冲突；这些源码事实不等同于任意环境的持续挂接证明。[固定内核 active 路径](https://github.com/microsoft/WSL2-Linux-Kernel/blob/linux-msft-wsl-6.18.54.1/net/ipv4/tcp_input.c#L6528-L6544)、[accepted FD 交付](https://github.com/microsoft/WSL2-Linux-Kernel/blob/linux-msft-wsl-6.18.54.1/net/ipv4/af_inet.c#L776-L790)、[child 状态处理](https://github.com/microsoft/WSL2-Linux-Kernel/blob/linux-msft-wsl-6.18.54.1/net/ipv4/tcp_minisocks.c#L988-L1014)、[hash 发布](https://github.com/microsoft/WSL2-Linux-Kernel/blob/linux-msft-wsl-6.18.54.1/kernel/bpf/hashtab.c#L1137-L1185)。

同一所有者只按既定串行路径撤销；外部特权程序强制 detach、原始 TCP 注入、TFO、TCP_REPAIR、其他 namespace 和未知 BPF 程序不在本次条件内。root 挂接不表示产品将采用该权限拓扑，更没有在宿主加载程序。生产核心与特权 helper 的 FD 交接、崩溃接管及长期 map 回收仍待设计和验收。实验服务不是 Hoopa 管理器；其 SIGKILL 拒绝行为不能替代“管理器退出、独立代理仍转发”的 T136/G04 验收。

## 固定输入与首批真实矩阵

沿用此前私有 QEMU 9.1.2/KVM、WSL Linux `6.18.54.1-microsoft-standard-WSL2` 和 libbpf 1.6.2；每个 VM 无网卡、host share 或宿主磁盘，输出仅经 serial 导出。新对象由宿主 5.10 BTF 离线编译，实际在 6.18 guest 通过 CO-RE 加载，不能称为“使用 6.18 BTF 编译”。对象 SHA256 为 `980e1bac835d1671a893a5a71ff0a5b0648bdc5258f84b0e062ef030e967e1a9`，程序/map ABI、实际 map FD 权限、程序引用 map ID、配置冻结和根 link ID 均由运行时检查。

八场景在运行前登记于 `private/t132-online/registration-v1.json`。每轮保留独立 image、guest/build/run 脚本、完整导出和 exit status。QEMU exit 0 只表示 VM 退出；场景结论另由 guest 结果、原始记录及 `verify_matrix_v1.py` 核对。

| 场景及独立 run 后缀 | 观察到的事实 |
| --- | --- |
| baseline / `rf04cif3` | 两客户端均成功；PID 72/73、client cookie 2/4097、accepted cookie 3/4098 分别吻合；应用各读写 33 B，grant 先于首次读写 |
| 8 客户端批次 / `y050gbvi` | 8 条独立 PID/cookie/namespace/端点逐条吻合，各读写 33 B；不声称八条同时活跃或覆盖所有并发交错 |
| outside-scope / `1bz7nnwj` | 客户端实际 `connect: operation not permitted`；source fault 非零，accepted 记录为零 |
| stop-before-query / `7q27p_z2` | 实际 accept 后执行停止 BPF 程序；Q 因 gate 关闭拒绝，应用读写为零 |
| close-connect-before-query / `78odqci4` | 实际 accept 后关闭自有 connect link；Q 因 link 缺失拒绝，应用读写为零 |
| close-lifecycle-before-query / `k5wftto7` | 实际 accept 后关闭自有 lifecycle link；Q 因 link 缺失拒绝，应用读写为零 |
| wrong-config-before-query / `bmm9rnzk` | 实际 accept 后用错误 generation 查询；配置 readback 不符，应用读写为零 |
| server-killed / `yx8rxvn9` | 自有服务进程实际 SIGKILL/-9；无最终服务报告，无连接记录；后启动客户端实际 connect refused |

首批共 10 条允许配对及六种拒绝场景，**正式 G01 新样本为零**。故障注入是在明确的 accept/Q 边界执行，不等于任意时点并发 detach 或在途回调故障矩阵。SIGKILL 的最终报告缺失按预期保留，没有补造清理成功。

独立复核发现 guest 对 outside-scope 原本只断言非零客户端退出、零 accepted 和 fault>0，弱于登记的“connect 报错”。原始 stderr 确实为 connect EPERM；host 材料检查额外要求该错误阶段。旧登记和 guest 原文保留，不把较弱断言静默改写成已覆盖所有失败阶段。

## 线程、FD 转交与 tuple 复用

四个新增场景在 `registration-v2.json` 冻结；新 client 的 `/proc` 和 socket 自读字段仅作验收 oracle，服务端不读取客户端声明来生成身份。测试脚本同时核对 client 的真实退出码和服务端记录，不能只看 `exchanged` 行。

| 场景及 run 后缀 | 原始记录及结论 |
| --- | --- |
| thread 首次 / `vmq_hgco` | TGID 73、TID 74 和回显记录吻合，但 client 最终 -6；stderr 为缺少 `libgcc_s.so.1` 的 pthread unwind 错误，整场失败保留 |
| thread 重跑 / `177xj0e7` | 仅向私有镜像加入本机运行库，依据 `registration-v3-thread.json` 重跑；client/server 均 exit 0，TGID 73、TID 74、writer PID 73，授权后各读写 33 B |
| fork-writer / `05_wiudq` | parent PID 72 发起连接，child PID 73 使用继承 FD 写入；BPF/绑定仍记录 parent 72，client/server exit 0 |
| scm-writer / `m93rf0fz` | parent PID 74 发起；child PID 75 先关闭继承 TCP FD，再通过 SCM_RIGHTS 接收并核对同一 socket，写入后仍归 parent 74 |
| tuple-reuse / `6tzc1gwk` | PID 74/cookie 4097 的首连接成功；PID 75/cookie 2 再次实际连接同一 `127.0.0.1:31001→18993`；tuple 冲突令 fault=1，第二 accepted 在 grant 前拒绝，应用读写均 0、首次读写时间戳为空 |

tuple 复用只有第二次 connect 确实成功才满足判据；`bind`/`connect` 失败不能冒充在线拒绝。拒绝路径不读取 token，因此第二 accepted 的配对依赖这个封闭、先后两客户端场景和事件顺序；不声称已从拒绝记录识别出第二客户端身份。

在这三个身份场景及 tuple 的首条成功连接中，实测 `SC_CLK_TCK=100`、初始 time namespace 两个 offset 都为零，才将 BPF leader `start_boottime` 纳秒整除 10,000,000，与 `/proc/<pid>/stat` field 22 ticks 比较。该换算来自固定内核的 [proc start-time 输出](https://github.com/microsoft/WSL2-Linux-Kernel/blob/linux-msft-wsl-6.18.54.1/fs/proc/array.c)与 [nsec_to_clock_t](https://github.com/microsoft/WSL2-Linux-Kernel/blob/linux-msft-wsl-6.18.54.1/kernel/time/time.c)；不推广到不同 USER_HZ/time namespace。建连前、建连后和完成时的受控 leader comm 均吻合，但非原子采样未证明 rename/exec 一致性，路径仍 unknown。

独立 `verify_matrix_v2.py` 核对实际 initramfs 内容、登记源码、全部导出文件摘要、raw binding/C ABI、原 socket、PID/TID、启动 ticks、名称样本和 grant-before-I/O。原 v2 矩阵保持 failed；替换线程 run 与其新登记单列后，四个有效场景满足有限判据。累计 13 次 VM 启停中，12 个有效场景提供 **14 条允许配对、七种拒绝条件**，另保留一次线程失败；正式 G01 新样本仍为零。本地摘要和时间戳是可复查操作记录，不是外部签名的来源认证。

新增四个有效 run 各在 ready 后、client 前采一次实验服务 `/proc` status/stat 和 11 项带 BPF ID 的 fdinfo，采样 error 为空。它们只给出单点 RSS/CPU ticks/资源信息；没有 10 分钟 CPU 均值、kernel map 内存实测总账或整套 idle 预算结论。具体单点值保存在机器摘要中，不赋给正式管理器性能指标。

## 代码验收与限制

新增 profile 的 17 项测试及受影响旧路径回归共 130 项通过；服务端准入/清理另有 9 项、独立 client 另有 19 项定向测试通过，合计 158 项。17 项包含在 130 项内，不重复累加。这些测试不加载 BPF、不创建真实网络 socket；client 测试包含无网络原生线程。真实加载、fork/SCM 和 socket 行为由上述 VM 材料单列证明。审查发现的 create-link 后追加列表可能漏登记 FD 已修复为提前预留两个所有权槽，并验证第二次获取失败时仍关闭第一条 link。旧核心 001–007 补丁、原 BPF 对象与历史 JSON 未覆盖。

`/tmp` 曾满盘导致子代理的沙箱在命令执行前失败。只清理核验为 Hoopa 专用、可重建的 `/tmp/hoopa-mihomo-events-buildcache`，释放约 2.7 GiB；源码、固定二进制、依赖缓存及实验日志保留。恢复后继续复核，未把环境失败记为测试通过。

下一步先确定核心与特权加载端的 listener/link/map/原 FD 所有权及撤销顺序，再在受控核心 `serveEntry` 的原 accept 后、worker/`mixed.HandleHoopaConn` 前接线，分别验证真实允许和来源失效拒绝。核心/helper 分离不能重新引入“observer 死亡而入口继续无证准入”的缺口，也不能把管理器与代理生命周期绑在一起。完整重启/跨 namespace/exec 与改名竞态、路径能力、长期 map 回收、性能及正式 100 流仍未完成；局部 token 回显不能称为实际 Mihomo 转发验收。

主代理负责协议、C、准入服务、真实 VM 与最终裁决；executor（gpt-6.1-sol/medium）实现已定 profile 接口并独立复核稳定代码，scout（gpt-6-luna/high）核验固定内核源码及原始材料。最多两个子任务并行，没有子代理再派生。
