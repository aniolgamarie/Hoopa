# 核心 connector 的线程、转交与 exec 边界验收

2026-10-24。固定核心上的五个隔离 VM 场景全部满足本轮有限判据：10 条真实代理流、65 个原始来源帧，每流上、下行各 33 字节。它们延续[核心接线验收](connector-core.md)，不把旧 token 服务的样本移记到实际核心，也不将本轮结果提升为正式 G01 或产品进程能力。完整摘要、每次运行及原始材料 SHA256 见机器记录（本地原件 `../../experiments/sourceidentity/core-edges-validation.json`，不随Git上传），判据见[运行前契约](../../specs/001-hoopa-proxy-manager/contracts/connector-core-edges-v1.md)。

## 固定输入与真实结果

核心保持 001–009 候选，SHA256 为 `452effa120f0f0beea122169c9e442b6338389bc4768111c4ed7ffb8d5f44f9b`；BPF、libbpf、QEMU 和 Linux 6.18.54.1 来宾均与前一已验运行一致。新镜像 SHA256 为 `f0c74a44c6a90c0003d153c4f76dcacdf313e035ff2a14f789f0fc4ad45ad964`。每次运行独立保存镜像、预登记、串口原文、导出材料和宿主检查结果；无网卡或宿主文件共享，没有在宿主加载 BPF 或修改 cgroup。

| 场景 | 独立观察与核心记录 | HTTP / SOCKS5 流数 |
| --- | --- | --- |
| 工作线程建连 | 实际 TID 与 TGID 不同，核心使用进程 TGID 与 leader 启动身份，调用 TID 单列 | 1 / 1 |
| 父建连、子写入 | writer PID 与 connector PID 不同，原始身份保持父进程 | 1 / 1 |
| SCM_RIGHTS 转交 | 子进程先关闭继承的 TCP FD；接收前对该已知 FD 的一次只读检查得到 EBADF，随后接收并核对同一 socket cookie/namespace/端点；归属保持原 connector | 1 / 1 |
| 建连后改名 | 建连观察为 `hoopa-before`，发送代理请求前改为 `hoopa-after`；原 opened 中保留建连观察 | 1 / 1 |
| 原 socket 跨 exec | 仅原 TCP FD 临时可继承；同 PID、启动 ticks 和双端点/cookie 在 exec 后复核一致，进程名已变为 `python3`，原 opened 保留 `hoopa-before` | 1 / 1 |

SCM 接收后的数字 FD 可以恰好复用旧数字；本轮依据是接收前已关闭的检查和接收后的 socket 身份，不要求数字不同。原 connector、writer 和调用线程分别记录，不能据此声称逐字节 writer 归属已被核心监测。

每个场景包含一个认证 HTTP CONNECT 客户端和一个认证 SOCKS5 客户端，分别访问 `alpha.test` / `beta.test`，实际经 `node:a` / `node:b`。客户端发送预登记 token 后执行 SHUT_WR，要求精确 33 字节回显与对端 EOF。独立 node 和 target 记录每方向的受限 payload；宿主按 token 对齐客户端、节点与目标，而非只比较总量。

宿主严格 v3 解码、epoch 状态机和只读恢复同时核对：原 socket 与 PID/TID/leader 启动 ticks、建连 BOOTTIME 区间、opened 的目标与端点、同 flow/attempt 的实际 Conn 引用、单节点链、已证 `http_connect_accepted` ready、连接段守恒及精确 closed。两类客户端的上游节点均是 HTTP 代理，因此 ready protocol 都是 `http_connect`；这里不能按入站协议误标为 SOCKS ready。观察到的 attempt ready 耗时为 node:a 11–12 ms、node:b 31–33 ms，仅是受控延迟案例，不是公共节点性能排名。

## 实现、检查与限制

新增[有限客户端](../../experiments/sourceidentity/core_test_client.py)和[定向测试](../../experiments/sourceidentity/test_core_test_client.py)，复用既有有限子进程与 FD 接收 helper。默认没有显式开关时不创建 socket；实际运行还要求隔离 guest 标记、精确 source cgroup 和显式目标。exec oracle 为私有 0600 文件，严格解析完整注册与进程身份后才接管声明的 FD；错误输入不关闭任意数字 FD。oracle 仅供独立测试核对，不作为核心 resolver 输入。

22 项新 fake 检查、19 项相关 client 回归及 73 项私有 harness 断言通过；这三组计数分别报告。准备期间曾发现 oracle 缺少 port 字段，完整 capture→oracle→resume 往返检查定位后修正，修前日志保留。上述无网络检查和只读代码审查不替代随后实际执行的五次 VM。稳定源码审查未发现指定路径的具体 critical/high 问题，不意味着任意部署或并发行为已获证明。

本轮实际 VM 无失败；前一阶段两次失败和正式旧 G01 0/100 原记录继续保留。名称仍是受控顺序下的非原子观察，路径仍 unknown，传统 process 字段和实例/名称/路径能力未升级；原帧保存身份也不等于已完成查询投影、进程聚合、排序或进程规则。

T132 仍需同名主/子独立短流、并发、早退、PID/tuple 复用、namespace/缺失冲突及持续覆盖等剩余矩阵，名称一致性须另有可证明方案。新准备的[活动来源崩溃契约](../../specs/001-hoopa-proxy-manager/contracts/connector-core-crash-v1.md)单列只读 pending 与核心 writer 恢复报告的边界。生产长期 map 回收和部署权限不由本次有限运行推导。T132/T137 保持未勾选，139 项 / 21 完成、P0/P1 未放行，新增正式 G01 样本为 0。

已有六次最终核心 ready 快照的只读资源整理显示：六个 map 的 `fdinfo memlock` 合计均为 430,472 字节，三个 program 合计 12,288 字节，核心单点 RSS 为 28,376–28,804 KiB。原始快照和派生记录（本地原件 `../../private/t132-core-resource-review/observations.json`，不随Git上传）保留；这些是内核报告字段和单点观察，不是完整 kernel 分配账、独立 manager RSS 或 10 分钟 CPU 预算验收。Linux 6.6、其他平台和实际 SSH/注销/休眠仍未验证。

本阶段由主代理负责语义取舍、真实 VM 与材料验收；executor 实际配置 gpt-6.1-sol/medium 负责客户端和私有 harness，scout 实际配置 gpt-6-luna/high 做限定只读审查。最多两个子任务同时执行，没有再派生子代理或声称自动切换主模型。
