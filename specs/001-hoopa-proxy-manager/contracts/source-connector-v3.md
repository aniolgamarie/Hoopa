# connector 同源实验事件 profile v3

2026-10-24 实现契约。接续[质量与线路 v2](telemetry-quality-v2.md)、[核心准入与 exec 移交](connector-core-handoff-v1.md)及 [ADR008](../../../docs/adr/008-remaining-evidence-execution.md)。本 profile 将原 accepted socket 的实验 connector 值放入同一 flow 的 `opened` 原帧；不是新的身份认证或正式能力声明。实际运行、候选摘要、故障与性能结果另行记录，本契约不放行 T132、G01–G03、T137 或 P0/P1。

## 版本与严格形状

每个 v3 事件的 `schema_version` 必须是字面 JSON 数字 `3`；`3.0`、`3e0`、字符串 `"3"` 均拒绝。保留 v1/v2 原有形状及解码行为，不为旧事件回填 connector。

通用字段精确为：

`schema_version source_instance_id source_epoch event_id source_seq flow_id attempt_id observed_at config_generation boundary_ref kind`

沿用既有通用字段类型与边界：`source_seq` 为非零规范 uint64 十进制字符串，ID 使用既有 ID 语法，`observed_at` 为合法且以 `Z` 结尾的 UTC 时间，`boundary_ref` 固定 `mihomo_proxy_stream_v1`。只有 `epoch_closed` 的 flow/attempt 是 null。

事件必须是有效 UTF-8 JSON，整帧上限仍为 8192 B。所有精确对象都拒绝缺失、未知和重复字段，包括转义后同名的重复键；不忽略额外尾部输入。整数不经 float64，不接受溢出或非规范十进制。下文的 uint64 字符串范围为 0–18446744073709551615；非零字段从 1 开始，不带符号、前导零、小数点或指数。

## opened

在全部通用字段之外，精确增加以下八个字段：

`process source_addr entry_addr target_kind target identity_scope flow_start_ns connector`

| 字段 | 类型与约束 |
| --- | --- |
| process | 精确为 `{"status":"unknown","reason":"connector_experiment_unverified"}`；不得升级成旧 `identified` 形状 |
| identity_scope | 固定 `connect_initiator_v1_initial_ns_ipv4_loopback_experimental` |
| source_addr / entry_addr | 沿用 v2 的可解析 AddrPort 字符串；本 profile 还分别与 connector 的 remote/local endpoint 核对，因而入口两端均为 `127.0.0.1` 且端口非零 |
| target_kind / target | 沿用 v2：`unknown` 对应空字符串，`ip_only` 对应合法 IP 字符串，`host` 对应非空字符串；入口 scope 不把目标强制改为 loopback |
| flow_start_ns | 规范 uint64 字符串，允许 0；含义与 v2 完全相同 |
| connector | 下述非 null 精确对象；只出现在 opened，不追加到后续事件 |

`connector` 与核心准入 `Identity` 保持相同的值字段和 JSON 名称，解码类型为 `ConnectorEvidence`。它不是来自代理请求头、认证用户名、目标、客户端 token 或 proc holder 扫描的回填字段。

## connector 值字段

connector 精确包含下表全部字段，不允许另加字段或将对象替换为 null。

| 字段 | 类型与约束 |
| --- | --- |
| schema | 固定 `sourceidentity-core-admission-v1` |
| identity_capability | 固定 `unverified` |
| process_path | 固定 `unknown`，不携带可执行文件路径 |
| boot_id | 规范小写 UUID 文本，形状为 `8-4-4-4-12` 个十六进制字符 |
| core_generation | 与本事件 `config_generation` 完全相同的字符串；与下行数值 generation 分列 |
| generation | 捕获配置的非零规范 uint64 字符串 |
| source_cgroup_id / root_cgroup_id | 非零规范 uint64 字符串，二者不同 |
| accepted_cookie / client_cookie | 非零规范 uint64 字符串，二者不同；前者属于原 accepted socket，后者属于 connect 发起 socket |
| socket_netns_cookie | 非零规范 uint64 字符串 |
| tgid_initial / leader_pid_initial | JSON 整数，1–2147483647，二者相等；初始 PID namespace 的进程 TGID 与对应 leader PID |
| caller_tid_initial | JSON 整数，1–2147483647；单独保留实际调用线程 TID，不代替 TGID |
| leader_start_boottime_ns | 非零规范 uint64 字符串，对应进程 leader 的 BOOT 纳秒启动身份，不使用 caller 的线程启动时间 |
| observed_boot_ns | 非零规范 uint64 字符串，connect 捕获时的 BOOT 纳秒坐标，必须不小于 leader_start_boottime_ns |
| active_pidns_inum | 非零 uint32 JSON 整数，1–4294967295；当前 active PID namespace inode，不是 pid_ns_for_children |
| leader_comm / caller_comm | 各为下述 comm 精确对象，不能相互代用 |
| local_endpoint / remote_endpoint | 各精确包含 `ip port`；ip 固定 `127.0.0.1`，port 为 1–65535 的 JSON 整数；分别等于 opened 的 entry_addr/source_addr |
| raw | 精确包含 `binding connect gate` 三个小写十六进制字符串，长度及 ABI 见下文 |

`boot_id`、root/source cgroup 等配置坐标由已核验的移交与原 socket 准入提供。wire 解码器检查可表达的一致性，不能仅凭调用者提交了一组自洽 JSON/原始 bytes 就认证其来源。

### comm

每个 comm 精确包含 `raw_hex text text_valid status`：

- `raw_hex` 是完整 16 B 的小写十六进制，最后一个 byte 必须为 NUL；首个 NUL 之前的 bytes 才是文本。首个 NUL 之后的原始 bytes 保留，不要求全部为零。
- `text_valid` 是 JSON 布尔值，必须等于该文本前缀的 UTF-8 有效性。有效时 `text` 精确等于该前缀，包括合法空文本；无效时 `text` 必须为空字符串。
- `status` 仅允许 `unavailable` 或 `unverified`，分别对应 C 记录中的状态整数 0、1。状态不会把 bytes 提升为已验证名称，`unavailable` 也不单独要求原始文本为空。
- raw_hex 必须与 C 记录中对应 caller/leader 的 16 B 完全相同，状态也必须匹配；不能只核对展示文本。

两份 comm 仍是独立、非原子的名称快照；不证明与其他字段具有统一读取时点，不证明 exec/改名一致性，不由 caller comm 猜 leader 名称。有效 UTF-8 和成功读取均不等于正式进程名能力。

## 原始 ABI 与一致性

raw.binding、raw.connect、raw.gate 分别必须解码为精确的 56、112、40 B，不接受大写 hex、非 hex 或长短不符。下表所有整数均为 little-endian；offset 从 0 开始，以 byte 计。能对应公开字段的每一项都必须相等，schema、状态及保留位同样检查。

### binding：56 B

| offset | 宽度 | 字段与核对 |
| --- | --- | --- |
| 0 | u64 | generation |
| 8 | u64 | socket_netns_cookie |
| 16 | u64 | accepted_cookie |
| 24 | u64 | client_cookie |
| 32 | u32 | 客户端 IPv4，值 `0x0100007f`，原始 bytes `7f000001` |
| 36 | u32 | 入口 IPv4，同上 |
| 40 | u32 | 客户端端口，等于 remote_endpoint.port |
| 44 | u32 | 入口端口，等于 local_endpoint.port |
| 48 | u32 | schema，固定 1 |
| 52 | u32 | reserved，固定 0 |

端口是该 ABI 的 u32 值，与公开 uint16 范围核对；不是将这四个 bytes 重新解释成网络端序端口。

### connect：112 B

| offset | 宽度 | 字段与核对 |
| --- | --- | --- |
| 0 | u64 | generation |
| 8 | u64 | observed_boot_ns |
| 16 | u64 | client_cookie |
| 24 | u64 | socket_netns_cookie |
| 32 | u64 | source_cgroup_id |
| 40 | u64 | leader_start_boottime_ns |
| 48 | u32 | schema，固定 1 |
| 52 | u32 | tgid_initial |
| 56 | u32 | caller_tid_initial |
| 60 | u32 | active_pidns_inum |
| 64 | u32 | leader_pid_initial |
| 68 | u32 | caller comm 状态，0 或 1 |
| 72 | u32 | leader comm 状态，0 或 1 |
| 76 | u32 | reserved，固定 0 |
| 80 | 16 B | caller_comm 原始 bytes |
| 96 | 16 B | leader_comm 原始 bytes |

### gate：40 B

| offset | 宽度 | 字段与约束 |
| --- | --- | --- |
| 0 | u32 | BPF_F_LOCK 读取返回的 lock，必须为 0 |
| 4 | u32 | closed，准入证据必须为 0 |
| 8 | u64 | active |
| 16 | u64 | admitted |
| 24 | u64 | completed |
| 32 | u32 | poison，必须为 0 |
| 36 | u32 | reserved，必须为 0 |

必须满足 `completed <= admitted` 且 `active = admitted - completed`。三项计数允许为零；它们是捕获 gate 的诊断计数，不是流字节、SourceReceiver receipt 或管理端统计。成功准入携带查询 binding/connect 后再次核验的 gate bytes；这一原始值不代表之后永不撤销，也不把跨 map 查询变成原子全局快照。

## 两类时间坐标

connector 的 `leader_start_boottime_ns`、`observed_boot_ns` 使用内核 BOOT 纳秒坐标，来源与 boot_id 一起解释。它们不是 proc USER_HZ ticks，不作静默单位转换；该 pair 只按本契约核对先后关系。

v2/v3 质量字段 `flow_start_ns`、`attempt_start_ns`、`ready_ns`、`finish_ns` 使用核心进程内捕获 origin 的 elapsed monotonic 坐标。质量时长继续由这些坐标按 v2 规则计算。BOOT 字段不能与质量坐标相减、对齐或替代 ready；UTC observed_at 也不参与时长计算。不同 boot、进程或 epoch 不因此获得跨域可比时间轴。

## 其他事件与 epoch

除 opened 外，v3 使用 v2 的精确解码与状态规则；不是回退到 v1 的宽化分支：

- `route_decided` 保留旧 rule-decision 形状及 actual_connection/connection_binding 两种 phase。核心旧 process 字段继续 unknown；connector 不开启旧 proc 路由归属或 PROCESS/UID 规则。
- `setup_finished` 保持 `forward_setup_v2` 的协议完成证明、独立 ready/finish、时长和 outcome 约束。
- `bytes_checkpoint`、`closed` 保持累计 flow/segment/unallocated 的守恒、单调与溢出检查，CounterEvent 投影保留 schema_version 3。
- `gap`、`epoch_closed` 保持既有字段和终态规则。未注册连接、错误 flow/attempt、退步时间或计数、重复 setup/binding 均不因版本 3 绕过检查。

同一 source_epoch 的 source_instance_id、config_generation 及 schema_version 在第一帧固定；source_seq 连续且事件 ID 唯一，epoch 终结后不再接新帧。1、2、3 不能混在同一 epoch。活跃 flow 上限仍为 32，v2 的每 flow 连接段及 chain 上限保持。source manifest 仍为 schema 1，没有新增 profile 字段；其旧 scope 不构成 connector 能力凭据。事件版本通过全 epoch 重放校验。

## 原 socket、接线与持久重放

受保护入口在原 TCP Accept 后、协议 worker 读取请求前取得准入纯值。`WithHoopaAdmission` 捕获该值，每个协议 metadata 分别复制；后续请求、其他 metadata 或之后被撤销的 owner 不得改写既有成功值。它经既有 Addition→Metadata→tunnel→Begin 路径到达同一 flow，opened 序列化其中的值；不在包装后的 socket 上另找 holder，也不向客户端索取补充归属。

启用 tracing 的 profile3 Begin 必须得到且只得到一个非 nil connector；缺失、多个或无效证据拒绝，不 fallback 到旧 proc evidence。profile1/2 拒绝非 nil connector，保留旧行为。opened 写前校验失败不得留下未写出的 flow/reservation；不能为了继续转发而降级写 v1/v2 opened。

受保护 tracing 的 `HOOPA_TRACE_PROFILE=3` 与 `HOOPA_IDENTITY_HANDOFF` 的存在绑定，并核对 tracing inbound、generation 与绝对 route-map 路径；无 handoff 的受保护 tracing 仍要求 profile2。handoff 变量即使是空字符串也强制进入 protected 路径，空值不是关闭身份模式；语法/FD 验证仍须通过。`-t` 不能替代运行时 FD adoption。质量证据开关必须同时识别 profile2 和 profile3，不能只改事件 schema 而让真实 Conn 失去 route/ready 证据。完整资源移交及失败关闭顺序以[移交契约](connector-core-handoff-v1.md)为准。

原 source 与 SourceReceiver 的持久性顺序不变：source 严格解码、校验状态、写带长度/hash 的原 JSON 帧并 Sync，随后提交状态；receiver 在原始字节去重和状态校验后持久 append，再提交接收状态。相同事件 ID/相同原帧重试沿用原 receipt，不同原帧拒绝冲突；来源序号与 receipt 序号仍分开。重开须重新严格解码每帧并重放同一 State，坏 connector 不能因 journal 是 opaque payload 而绕过校验，也不自动修复坏帧。

身份保存在 durable opened 原帧及 DecodeSourceEvent 返回的 ConnectorEvidence。当前 `Flow`/`RecoveryFlow` 派生投影不新增身份字段，ProjectRecovery 不展示 connector、comm 或路径，不从旁路观察或当前进程状态补回历史归属。没有 final 的旧流仍按既有 pending/gap 规则处理；connector 不制造恢复事件、新 byte fact 或额外结算。

## 能力边界

profile3 及 raw ABI 的自洽性检查不构成外部认证。信任边界仍包含同一隔离运行中固定的加载器、内核对象、核心、移交与原 accepted socket 操作链。身份 capability 保持 unverified，旧正式 identity_instance/name/path 能力不因此变为 true；名称为非原子快照，路径 unknown，PROCESS/UID 规则保持拒绝。

本契约沿用限定实验环境、普通 IPv4 loopback TCP 与初始 namespace 的边界，不扩展到其他 namespace、NAT、TFO、任意未知外层协议、生产加载权限、长期回收、崩溃接管或性能预算。协议解码通过、假接口测试和有限实际运行分别记录，均不能由本契约文字替代正式验收。
