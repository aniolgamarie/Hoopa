# T132 connect 捕获准备契约 v1

状态：仅 Linux/x86_64、IPv4 loopback 的离线可编译捕获源码及准备器。不是 telemetry wire v3，不是生产身份接口；verifier、attach、运行捕获、名称一致性、connect 结果和 accepted socket 关联均未验证。用户本轮确认“暂时没有，先完成原型准备”，因此没有加载、挂接或建立特权 loader。

本契约细化 [ADR008](../../../docs/adr/008-remaining-evidence-execution.md) 的捕获步骤，不改变 connect_initiator_v1，也不完成 T132。后续 A 的 connect→active 传递和 B 的 active→passive→原入口 cookie 关联仍须单独实现及实测。

## 1. 离线边界与固定输入

[prepare.py](../../../experiments/sourceidentity/prepare.py) 只按固定 argv 查询 clang/bpftool 版本、将已有 BTF 转为 C 头文件、编译仓库捕获源码。它不执行 BPF syscall、feature probe、load/attach、sudo、mount 或下载。每次创建独立 0700 目录，输入快照/日志/对象/manifest 为 0600；C 输入最多 1 MiB，BTF 与生成头最多 64 MiB，普通日志每流最多 8 MiB，每工具步骤最多 30 秒。

C/BTF 在任何工具运行前复制到私有快照，后续命令只读取快照；保存原路径、快照路径及 SHA256。工具真实路径与开始/结束 SHA256 另记，不一致则失败；这不是针对同 UID 恶意修改者的强隔离。ELF 检查只确认 ELF64 little-endian、EM_BPF、ET_REL、有界 section table、非空必需 sections 和指令段长度，不解析为 verifier 通过。

`status=prepared` 只表示上述离线步骤通过。`runtime`、`verifier`、`association` 始终是 `unverified`；失败返回非零并保留当次 manifest/日志。没有运行结果时不能生成 passed CapabilityRecord，也不能将该对象替换为 Mihomo 制品。

## 2. 捕获输入与原始记录 ABI

[connect_snapshot.bpf.c](../../../experiments/sourceidentity/bpf/connect_snapshot.bpf.c) 的 `cgroup/connect4` 程序只观察调用，所有返回路径均为 1，不修改地址、端口或连接策略。源码声明 `Dual MIT/GPL`，文件按 MIT 或 GPL-2.0-only 双许可；没有引入 libbpf 源码或新增项目依赖。

所有标量为 BPFEL 的 little-endian 原生布局。编译期断言 config 为 32 字节、record 为 112 字节；它们是有限实验的 map ABI，不能直接作为对外 JSON。

| Map | 键/值与限制 |
| --- | --- |
| capture_config | ARRAY，唯一键 uint32(0)，值依次为 uint64 generation/cgroup/netns、uint32 destination_port/reserved；reserved=0，前三值非零，port 为主机序 1–65535。BPF 程序只读 |
| connect_records | HASH，1024 项，uint64 客户端 cookie → connect_observation，用户态只读；仅插入不存在的键，不覆盖、不 LRU 驱逐、不超时回收 |
| capture_faults | ARRAY，唯一键 uint32(0)，uint64 故障计数，用户态只读；捕获异常原子累加，没有重置接口 |

`connect_observation` 的精确顺序：

| 偏移 | 字段 | 来源/语义 |
| --- | --- | --- |
| 0–47 | 六个 uint64：generation、observed_boot_ns、client_cookie、socket_netns_cookie、cgroup、leader_start_boottime_ns | 冻结配置、boot 单调时间、原客户端 socket、socket 所属 netns、调用者 cgroup、group_leader 的原始 start_boottime 纳秒 |
| 48–79 | 八个 uint32：schema_version、tgid_initial、caller_tid_initial、active_pidns_inum、leader_pid_initial、caller_comm_status、leader_comm_status、reserved | schema=1；TGID 和调用 TID 明确分开；leader pid/tgid 须与 TGID 一致；reserved=0 |
| 80–95 | caller_comm[16] | 调用线程名原始观察，末字节 NUL；不能替代进程名 |
| 96–111 | leader_comm[16] | leader comm 原始观察，末字节 NUL；并发改名一致性尚未证明 |

两个 comm status 均只有 `0=unavailable`、`1=unverified observation`，没有 verified 值。路径不在此 ABI 中，路径能力仍不支持。boot ID、读取端观测 PID/net namespace、object/BTF/kernel hash、配置与后端 generation 必须在未来运行 manifest 中独立登记；离线构建 manifest 不是该运行 manifest。

## 3. 明确的范围限制

- 目标为单个 `127.0.0.1:port` TCP endpoint；地址/端口按 sock_addr UAPI 网络序比较。它没有最终源端口、握手成功或 accepted cookie。
- 捕获侧调用者必须处于一个精确 cgroup ID，socket netns cookie 必须匹配配置。当前没有子 cgroup 自动覆盖；未来 A/B 环境应将测试客户端和核心放入预登记的受控范围，不能把单个 ID 说成自动覆盖整个层级。
- 初次捕获源码只接受 leader 的 `thread_pid.level=0`，从 `numbers[0].ns` 取 active PID namespace；不使用 `pid_ns_for_children`。非初始 PID namespace 目标样本导致捕获失败，不能用这个限制免除后续跨 namespace 否例。
- TGID 与启动身份均取对应 group leader，调用线程仅作为独立诊断字段；原始纳秒 start 不转换或等同 proc USER_HZ ticks。上述读取路径已静态检查，但尚无运行一致性证据。
- 此时只知道 connect 调用。失败建连也可能有记录；同一 socket 再次 connect 会因 cookie 重复使本轮失效。没有真实握手结果时不能把记录称作“成功连接”。

## 4. 故障与未来 loader 的停止条件

配置缺失/无效、目标范围错配、实例读取失败、namespace 不支持、cookie 重复及 map 满等路径增加故障计数，仍允许原 connect。任何故障使本轮全部观察不可作为通过证据，旧记录保留用于诊断；不把故障数减去或删除后继续报告成功。

该计数是程序的故障报告机制，不自行撤销 map 中的旧记录，也不能单独证明 hook 连续在线。若故障 map lookup 不可用，程序无法报告该次失败；未来 loader/验收器必须核对 map 类型/键值大小/容量/权限、只使用本次 FD，并用明确故障注入验证计数路径。故障 map 读取失败、配置未 freeze、attach 丢失或覆盖连续性无法证明时整轮拒绝，不能把读取错误按零处理。

未来 loader 须在 attach 前设置并 `BPF_MAP_FREEZE` 配置，固定作用 cgroup 与端口；新 generation 使用新对象/map。有限 spike A 不回收 map：关闭本次客户端、停止新的发起、detach 并确认观测静止后，再核对稳定记录和故障计数。不能在并发写入时宣布零缺口或永久完备。map/程序/attach FD 的关闭顺序和真实失败路径仍待实现与验证，默认不 pin、不安装服务，普通核心不持加载权限。

现有参考内核路径对 task/kernel 字段读取有 `bpf_capable` 与 `perfmon_capable` 检查；网络程序加载条件还须单独核验。仅工具/BTF 或 CAP_BPF 的存在不保证能读取这些字段。[固定 v5.10 helper 分派](https://github.com/torvalds/linux/blob/v5.10/kernel/bpf/helpers.c#L662-L701)、[socket-address helper 分派](https://github.com/torvalds/linux/blob/v5.10/net/core/filter.c#L6534-L6605)。这是上游源码判断，不冒充当前厂商内核的 verifier 实测；本机安装的是 headers/devel 材料，未取得完整厂商运行源码。

## 5. 真实运行前要登记的验收用例

以下均为待执行断言，不能由编译或假工具测试计为通过：

| 用例 | 预登记与判定 |
| --- | --- |
| 同名主进程、子进程、工作线程 | 每个样本在放行 connect 前登记 PID/TID、leader 启动身份、名称真值及原 socket；核对工作线程不得带自己的 start 冒充 leader |
| 父 connect / 子写 / FD 共享转交 | 捕获仍归原 connector；后续写入不改历史；另有子独立 connect 的区分正例 |
| 失败 connect、短流早退、重复 cookie | 区分调用与成功连接；重复键保留首条并产生故障，不静默更新 |
| 满容量与读取失败 | map 不驱逐，故障可见；无法读故障状态或来源输入时判为未通过 |
| 改名、exec、PID 重用 | 已存观察不被后来的 proc/name 替换；名称一致性仍缺证明时保持该字段 unverified，不能标路径支持 |
| cgroup/netns/PID namespace 错配 | 目标调用明确记录失败/不可证明；非目标调用只放行，不能误归属 |
| attach 丢失、后端重启 | 旧 generation 不被新后端复用；记录数量/真值及覆盖不足均不能通过 |
| A/B 后续关联 | 实现 connect→active cookie 传递及 passive→accepted cookie 查询；证明两侧 cookie 分别使用、生命周期唯一性和 tuple 复用处理后才可接原核心路由 |

真实运行还必须记录 helper RSS/CPU、map 实际容量/内存与等待成本。T132 所有明文断言及 T137 前置保持不变；本准备契约没有减少正式样本或变更旧 G01 0/100。
