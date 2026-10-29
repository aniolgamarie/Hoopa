# T132 有限核心准入、v3 同源记录与半关闭修复

2026-10-24。本轮将上一阶段 token 回显服务中的准入机制接到固定 Mihomo 原 accepted socket，并取得真实代理转发证据。它仍是受控原型：**T132/T137 未完成，139 项任务完成 21 项，P0/P1 未放行；旧 G01 0/100 保留，新增正式样本 0**。本轮使用独立 Python 测试客户端，不是 Codex 正式 100 流验收。

完整输入、来源文件摘要、运行目录和测试记录见机器验收（本地原件 `../../experiments/sourceidentity/core-validation.json`，不随Git上传）及[候选 manifest](../../experiments/mihomo-events/core-connector-candidate-manifest.json)。运行时二进制、镜像和原始日志均在忽略的 `private/t132-core-identity/`，不进入 Git。固定上游仍为 Mihomo v1.19.26、commit `fc8c5a24b16991f98cd736950c17d1aa306a5041`，没有重新查询或升级上游版本。

## 已实现的边界

[移交契约](../../specs/001-hoopa-proxy-manager/contracts/connector-core-handoff-v1.md)规定一次性 Python 加载器完成 load/freeze/attach 后，通过同进程 `execve` 向核心移交 6 个 map、3 个 program、2 个 link FD。核心接管前核对 schema、世代、实际 namespace/cgroup、内核对象 ABI/引用、冻结配置和初始状态，再恢复 CLOEXEC；没有逐连接 helper RPC，也没有把客户端 oracle 当作身份输入。FD exec 使用已哈希的同一个可执行文件 FD。

原 `Accept` 返回后、协议 worker 启动前，核心直接从原 socket 读取双 cookie、两端点并核验 C→A→P 绑定。失败使后续准入撤销并关闭该新连接；已有成功准入连接保留原值。身份通过每份 metadata 的独立值副本进入同 flow 的 `opened` 原帧，不回退到 `/proc` 当前 holder 查询。

[事件 v3](../../specs/001-hoopa-proxy-manager/contracts/source-connector-v3.md)把 connector 的原 ABI、PID/TID、leader 启动 BOOTTIME、boot/PID namespace、cookie 和世代明确保存。uint64 仍为规范字符串；严格拒绝未知/缺失/重复字段及 raw ABI 不一致。其余质量、实际连接、连接段累计、终末与恢复规则沿用 v2，同一来源世代不能混合版本。传统 `process` 仍为 `unknown:connector_experiment_unverified`，名称是非原子 unverified 快照，路径 unknown，三个正式 identity capability 均 false，PROCESS/UID 配置继续拒绝。恢复投影暂不增加身份展示，身份可从保留的 opened 原帧读取。

新增[0008](../../experiments/mihomo-events/patches/0008-core-connector-admission.patch)固定上述接线；独立[0009](../../experiments/mihomo-events/patches/0009-metered-tcp-half-close.patch)修复真实测试暴露的计量半关闭问题。两者不覆盖旧 001–007 或旧候选。

## 真实运行与失败保留

运行环境是无网卡、无宿主共享和磁盘的私有 KVM 来宾，内核 `6.18.54.1-microsoft-standard-WSL2`，两 CPU、1 GiB。BPF 对象仍为上一阶段以宿主 5.10 BTF 编译、在来宾 CO-RE 加载的固定对象；本轮不在宿主加载 BPF 或修改宿主代理/cgroup。

总计 16 次 VM，14 次满足其有限判据，2 次失败保留；有效配对共 12 条。最终候选 SHA256 为 `452effa120f0f0beea122169c9e442b6338389bc4768111c4ed7ffb8d5f44f9b`，单独复验 9 个场景，7 条有效配对、48 帧来源事件：

| 最终候选场景 | 真实观察与边界 |
| --- | --- |
| 正常 HTTP CONNECT + SOCKS5 | 分别访问 alpha.test/beta.test，经 node:a/node:b；每条双向各 33 B，与独立客户端 PID、启动 ticks、TID、cookie、两端点及目标回显一致 |
| 两协议发送后立即 SHUT_WR | 修复后仍收到完整 33 B 响应和 EOF，来源终末双向各 33 B；不再因上传 EOF 提前结束下载 |
| drain 旧连接续传 | 既有 HTTP 连接先交换 12 B，drain 后再交换 33 B，终末双向各 45 B；本场景重点是旧流续传，关闭新入口的旧 v2 真实回归另列 |
| 核心重启并重开来源目录 | 旧核心正常结束后，新 PID/新来源世代继续第二条流；旧来源两文件逐字摘要不变，两个世代的 14 帧均能严格读取和恢复 |
| source scope 外客户端 | connect 失败，目标零访问、opened 零条；只有正常源关闭标记 |
| 核心 SIGKILL 后再连接 | 新 connect 失败，目标零访问；来源帧为空，没有伪造 epoch_closed。此项不代表管理器退出后的转发存活 |
| 错 core_generation | 在 load 后、exec 前仅改移交世代，核心退出，入口/控制端口均拒绝、来源文件零创建 |
| 缺失 map FD | 关闭本测试显式拥有的一个导出 FD，再 exec；核心接管拒绝，未开放入口或创建来源 |
| program FD 指向 map | 用本测试拥有的 map FD 替换 program FD，实际内核类型核验拒绝，未开放入口或创建来源 |

这些启动故障由单独的私有 guest 注入脚本完成，未给公开 bootstrap 或产品控制接口增加故障开关。每次运行先写入登记，保留实际 image、运行脚本、输入/输出摘要和有界串口归档。检查器逐原始帧验证 SHA256、严格 wire/schema/状态转换，再用独立客户端观察匹配 connector、flow、actual connection、线路及终末。完整 sourceinspect 恢复与 raw 解码分开，不把恢复投影缺少身份字段误判为身份原帧丢失。

受控线路的真实 `http_connect_accepted` ready 点与 finish 分列；最终正常两流记录 13/31 ms，提前半关闭两流为 12/30 ms。上游测试节点有人工等待，这些是转发建立耗时样本，不是 RTT、应用请求延迟或性能预算验收。BOOT 身份时钟不与进程单调质量时钟相减；启动 ticks 只在本次 100 Hz、time namespace 两项偏移均为零条件下核对。

保留的失败：

1. 首轮核心已 running，但采样器扫描到刚关闭的普通控制 FD，实验提前中止。改为只读取明确移交的 11 个资源 FD 后重跑；首轮没有客户端样本，不计成功。
2. 第二轮两客户端上传 33 B 后立即关闭写端，目标各收发 33 B，但客户端没有完整收到回显，来源终末如实记录上行 33/下行 0。`common.Cast` 可以穿过普通 tracker 的 `Upstream`，但 Hoopa 计量包装为防绕计数而隐藏它，又未转发 `CloseWrite`，导致 relay 退化为 full Close。0009 只增加半关闭控制转发：支持时计入 active 操作，不提前关闭 counter；不支持时在 begin 前走原 full Close，避免等待自身。它不暴露读写 fast path。旧错误帧保留，新的原始早半关闭方式复验通过。此外，该轮发现全局质量开关遗漏 profile3，0008 已补齐并增加独立进程初始化测试。

## 构建与自动化验证

从新私有 pristine 副本依次 `git apply --check`/apply 001–009，完整比较路径、内容、模式和 symlink；构建前后与已审查目标树一致，1155 条目、961 个文件。固定 Go 1.27.3，`GOPROXY=off`，`-p=2 -trimpath -buildvcs=false`，go.mod/go.sum 未改。公开 coreadmission 与核心副本、共同生产 wire 文件逐字一致。

本实验构建未加版本 ldflags，以便核对相同源码的二进制字节；`-v` 显示上游源码默认的 1.10.0/unknown time，**不能用该展示值代替固定源码 tag/commit 或宣称 Hoopa 发布版本**。完整可复现输入以 manifest 为准；T137 正式候选版本仍需另行冻结。

| 验证 | 观察结果 |
| --- | --- |
| bootstrap | 新单测 25 项、相关 online 回归 26 项通过；假接口/文件操作，不加载 BPF |
| 001–008 核心相关 5 包 | 63 个顶层、含子测试 203 项 race 通过、0 skip；包括 admission 119 项和核心 v3 接线等测试；vet 通过 |
| 半关闭包装 | 9 个顶层定向测试 race 通过，验证下行继续、最终累计、fallback 无自等待和并发完整关闭；vet 通过 |
| Hoopa wire/接收器默认回归 | 65 个顶层、含子测试 406 项通过，7 个需显式启用的网络实验 skip，0 fail；不把 skip 记为通过 |
| 最终候选旧受保护入口真实回归 | 独立显式 loopback 运行 2 个顶层、含子测试 49 项通过，0 skip/0 fail，覆盖认证、配置拒绝、旧 v2 来源、drain 等 |

不同测试批次存在重叠，不相加为唯一测试总数。早期 Go 1.20 语言级别不支持新内置函数、包初始化解析测试 flags、受限沙箱 loopback 等执行失败均保留，分别做等价兼容修正、仅测试初始化及按授权运行；不是环境失败记通过。稳定补丁只读审查未发现指定路径中的具体 critical/high 问题，不据此保证所有故障情形正确。

## 仍在进行的工作

本轮完成的是原核心入口接线、有限资源移交与持久事件链。核心上的同名主/子短流、早退、并发/复用、exec/改名、跨 namespace、缺失/冲突/重启等完整矩阵仍待独立覆盖，不能把上一阶段 token 服务的样本直接算到这个核心 hash。insert-only maps 的 1024 容量、长期回收/切代、生产权限部署和覆盖前提也未验收；名称原子性、路径能力不提升。

这里真实重开的是已完成旧流；活动 v3 来源被杀后的缺口、重放和完整故障矩阵仍需单独验证。管理器退出仍转发、接管原核心及配置切换归 T136/G04，不由核心 SIGKILL 结果替代。资源记录只是 ready 时单点 status/stat/fdinfo，首选 Linux 6.6、其他平台、实际 SSH/注销/休眠及 CPU/RSS 预算未验。
