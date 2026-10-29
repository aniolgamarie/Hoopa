# Hoopa CLI 与 JSON v1 契约

状态：整体规划契约，T033 已接入 Linux/amd64 的 start、stop、service run/stop、status、operation get/recover 与对应六个 JSON 操作；验收见服务生命周期记录。其余命令仍未交付。本地单用户使用；这不是公开 HTTP 服务。
依据：[规格](../spec.md)、[研究](../research.md)、[数据模型](../data-model.md)。

## 人类命令

| 命令 | 作用与边界 |
|---|---|
| `hoopa init --mihomo PATH [--gost PATH]` | 显式本地初始化；消费已有二进制，不下载、不启动核心、不创建账本 |
| `hoopa start --control-only` | 显式启动管理服务以便配置，不启动核心 |
| `hoopa start` | 启动管理服务及最后已提交、已核验的核心快照；没有可用快照时失败 |
| `hoopa service run` | 前台运行管理服务，不隐含启动转发 |
| `hoopa stop --accept-disruption` | 校验实例身份后停止所属核心及计量入口；管理服务退出另用 `service stop` |
| `hoopa service stop` | 停止管理服务；存活核心不因服务退出被主动杀死，采集停止明确显示 |
| `hoopa status` / `hoopa doctor` | 只读状态与诊断，不主动探测网络或修复 |
| `hoopa config show` | 显示脱敏意图及 revision |
| `hoopa config plan --input PATH --output PATH` | 从本地 YAML 意图纯本地预览；只写调用方指定输出文件，不创建内部状态 |
| `hoopa config apply --plan PATH --confirm PLAN_DIGEST [--accept-disruption]` | 应用原计划；不重新下载、不自动重规划；按影响披露启动或替换核心 |
| `hoopa subscription list` / `hoopa group list` | 只读当前已知来源、组与新鲜度 |
| `hoopa subscription update ID` | 显式下载到不可变来源快照；下载成功不表示运行态已应用 |
| `hoopa app inspect ID [--mode managed\|inherit\|direct]` | 只读子进程接入预览，包括入口、代理环境差异、NO_PROXY、计量及失败条件 |
| `hoopa route explain` | 只读解释；机器参数见下表，区分规则推断、未知条件与连接实际证据 |
| `hoopa connections list` | 只读活动连接，不以活动快照承诺历史完整 |
| `hoopa probe NODE_ID --subscription ID --url URL` | 显式联网；不改选择，不调用有清除固定选择副作用的组级 delay 接口 |
| `hoopa run --app ID [--mode managed\|inherit\|direct] [--meter] -- ARGV...` | 参数数组启动应用，保留正常终端、信号与退出状态；不属于 JSON 管理协议 |

T033 的服务发现、退出收据、单次重启和只读查询数据见[服务生命周期 v1](service-lifecycle-v1.md)。
`service run --restart-once` 是显式一次重启授权；普通后台服务默认没有重启预算。
`operation get ID` 与 `operation recover ID` 分别查询和显式恢复，变更命令可提供
`--request-id` / `--expected-revision`；精确重试须保留原身份及完整正文。
目前核心 stop 不涉及尚未实现的计量入口；T139 已交付限定 config plan/apply 与只读 doctor，见[最小CLI契约](minimal-cli-v1.md)及[实际验收](../../../docs/validation/minimal-cli.md)。完整 init、app run 和 US6 查询扩展仍待后续任务。

P2 才交付 usage、连接历史/导出与 TUI；保留策略通过 config plan/apply 修改。
未交付操作返回 `CAPABILITY_UNSUPPORTED`，不返回空成功。`status` 披露实际 capabilities。
只读命令不创建目录、缓存、数据库或 WAL/SHM，不更新 last_seen，不启动、下载、测速、选组、迁移、恢复或清理。
服务不可用时仅允许安全读取已有文件/账本；不能安全只读打开则 `READ_UNAVAILABLE`，不通过写连接恢复 WAL。

## 机器请求

`hoopa api v1 OPERATION` 从 stdin 读取一个有界 JSON 请求，stdout 输出且仅输出一个 JSON 响应。
stderr 仅脱敏诊断；不等待提示，不混入进度与子进程输出。请求正文 `operation` 必须与 argv 一致。
拒绝未知主版本、操作/参数字段、重复 JSON key、尾随 JSON 和超限正文。
具体请求上限为实现阶段固定、公开并测试的资源参数，不能使用无限读取。

```json
{"api_version":"v1","request_id":"client-unique-001","operation":"status.get","params":{}}
```

`request_id` 为不含秘密的调用方唯一不透明字符串，长度为 1–128 个字符。只读请求用它关联响应，不为去重创建持久日志。
以下去重/重放保证针对已接纳的持久变更操作：相同 ID 与相同操作/正文查询并重放已知结果；
同 ID 不同正文返回 `REQUEST_ID_REUSED`。重试先查已有操作，再校验候选 revision；
成功重试不会因当前 revision 前进而被误拒。未终结操作日志不得清理；压缩终态日志后保留防重放 tombstone，
没有默认重试过期窗口。调用方丢失响应时应先 `operation.get` 或以原 ID/正文重试，不能盲目创建新操作。

| operation | params（除标为可选外均必填） | 边界 |
|---|---|---|
| `status.get` / `doctor.get` / `config.get` / `group.list` | `{}` | 只读 |
| `subscription.list` | `{}`；可选 `cursor` | 只读；每页至多 32 来源，完整 total，后续页绑定同一 view_id，变化返回 SNAPSHOT_EXPIRED |
| `operation.get` | `operation_id` | 只读，未知身份 `OPERATION_NOT_FOUND` |
| `app.inspect` | `app_id`, `mode?` | 只读，模式 managed/inherit/direct |
| `route.explain` | `app_id?`, `process_instance_id?`, `domain?`, `destination_ip?` | 只读；信息不足保留未知，不造实际路径 |
| `connections.query` | `scope`=active/history；`from?`, `to?`, `filters?`, `page_size?`, `cursor?` | 只读；历史属于 P2 |
| `usage.query` | `view`, `from`, `to`, `filters?`, `sort?`, `direction?`, `page_size?`, `cursor?`, `top_n?`, `window_seconds?`, `quality_definition_id?`, `quality_target_id?` | 只读，P2；view=process/name/domain/process_domain/launch_tree |
| `config.plan` | `intent`（严格 v1 意图） | **只读**纯编译，返回普通 Plan 对象，不内部保存候选 |
| `config.apply` | `plan`, `confirmed_plan_digest`, `accept_disruption` | 显式写；revision 取 Plan.base_revision |
| `subscription.update` | `subscription_id`, `expected_revision`；可选 `intent`, `region_rules` | 显式联网/写来源快照，不应用运行态；返回持久预约，使用 operation.get 查询完成状态 |
| `runtime.start` | `expected_revision`, `control_only` | 显式启动，非 control-only 必须有已提交可用快照 |
| `runtime.stop` | `expected_revision`, `accept_disruption`=true | 显式停止已验证所属实例 |
| `service.stop` | `expected_revision` | 显式停止管理服务 |
| `operation.recover` | `operation_id`, `expected_revision` | 显式恢复既有操作 |
| `probe.run` | `subscription_id`, `node_id`, `url`, `timeout_ms` | 显式网络测量；不修改配置选择 |

节点 ID 仅在所属 subscription 命名空间内唯一；probe 必须同时指定来源和节点，不按显示名或活动来源猜测。

所有新控制变更均检查 expected revision 或 Plan.base_revision。拿到写入权后，过期候选须在本次持久或运行态写入前返回 `CONFLICT`；
不自动合并、不自动重规划。global revision 随已提交配置、retention 或核心运行意图变更前进，不随查询、probe 或仅下载候选前进；字段默认值与限制以 Schema 和下节语义为准。
runtime.stop 在终止所属进程前持久记录 desired_runtime=stopped，核验停止后提交新 revision，崩溃恢复不能把显式 stop 误当故障再启动。
control-only 不触发核心自动恢复；service.stop 不改变核心 desired_runtime。

## 响应与错误

```json
{"api_version":"v1","request_id":"client-unique-001","ok":false,"data":null,"error":{"code":"NOT_RUNNING","message":"管理服务未运行","retryable":false},"warnings":[]}
```

成功响应 `ok=true`、`data` 为对象、`error=null`；失败为 `ok=false`、`data=null`、`error` 对象。
无法解析或可信提取 request_id 时错误响应的 request_id 可为 null。警告不表示操作已提交。
变更结果包含 `operation_id`、`phase`、`base_revision`、`target_revision` 与 `outcome`；
非终结成功仅为收据，须轮询 operation.get，只有实际运行核验并提交后才能报告 committed。
失败回滚必须核验完整旧快照；状态不明或回滚失败返回 degraded/recovery required，并保留证据。

| 退出码 | error.code |
|---|---|
| 0 | 成功（含明确的未终结收据） |
| 2 | INVALID_ARGUMENT、UNSUPPORTED_VERSION、CAPABILITY_UNSUPPORTED |
| 3 | CONFLICT、REQUEST_ID_REUSED |
| 4 | NOT_RUNNING、DEPENDENCY_UNAVAILABLE、READ_UNAVAILABLE、SNAPSHOT_EXPIRED、OPERATION_NOT_FOUND、OPERATION_CAPACITY_EXHAUSTED、SUBSCRIPTION_UPDATE_FAILED |
| 5 | APPLY_FAILED、RECOVERY_REQUIRED、LEDGER_UNAVAILABLE、INTEGRITY_ERROR |
| 6 | AUTHORIZATION_DENIED |
| 7 | CANCELLED |

`retryable` 是是否适合重试同一操作的建议，不允许绕过确认、revision 或恢复条件。
所有输出、错误、警告及诊断均不得暴露订阅 URL 中的秘密、认证值、controller secret、应用正文或完整环境。
单用户本地访问仍须保护私有目录和 IPC；同用户 loopback 不构成 OS 安全隔离。

## Plan 与查询的语义校验

Plan 使用 [RFC 8785 JCS](https://www.rfc-editor.org/rfc/rfc8785.html) 的 UTF-8 canonical JSON，
移除且仅移除顶层 plan_digest 后计算 SHA-256，输出 64 位小写十六进制。拒绝重复 key、无效 Unicode、NaN/Infinity；
数字须可由 IEEE754 double 表达，整数另限安全范围 ±(2^53−1)，大字节计数使用十进制字符串。
按 JCS 排序/序列化，不使用普通 json.dumps(sort_keys=True) 代替，也不额外 Unicode normalization。
T028 已有限实现并通过 RFC/独立 Node 向量验证，见[Plan 组件验收](../../../docs/validation/p1-plan-envelope.md)；完整命令和 Intent 编译仍待后续阶段。
Plan 的 canonical digest 不等同授权。apply 须同时满足：无 blockers、确认摘要一致、base revision 和所有不可变输入仍一致、
compiler/capabilities 可用，以及 requires_disruption 时明确 accept_disruption。未知影响必须 requires_disruption=true。
impact.runtime_action 明确 start/replace/select/none；P1 完整 config.apply 仅支持 start/replace，
不以 offline draft 写入冒充发布 active；其他动作须另有能力证明。
固定节点引用绑定来源和稳定 node ID，不能按名称或活动来源重绑定。规则按显式数组顺序首个确定匹配；
当前规则需要而缺少源身份时拒绝连接，不能当作不匹配穿透。实现细节见[数据模型](../data-model.md)。

usage/历史查询统一使用 UTC 时间范围、完整过滤与稳定排序，末级以稳定行 ID 排序。
filters 为 process_instance_id、process_name、domain、app_id、launch_id、node_id、subscription_id、pid、entry_id、protocol、quality_state 的精确筛选，多个字段取交集；
pid 必须为正整数且同时给出 process_instance_id，不能跨 PID 复用混查；node_id 必须同时给出 subscription_id。
protocol 允许 tcp/udp/quic，尚未支持的协议返回明确 CAPABILITY_UNSUPPORTED，不能伪造零流量。
quality_state 允许 complete/partial/unknown/stale。
process_name 区分大小写，domain 是有证据的规范化完整主机名，不按 IP 猜补。from/to 为闭区间且 from≤to。
省略 page_size 为 100，允许 1–1000；direction 默认 desc，sort 默认 total_bytes；未知量排序放末尾。
这些是可调工程默认值，不是吞吐、支持规模或响应时间的测量承诺。
sort 允许 total_bytes、upload_bytes、download_bytes、upload_rate_bytes_per_second、download_rate_bytes_per_second、connection_count、name、pid、last_active_at、latency_p50_ms、latency_p95_ms、failure_rate。
latency_p50_ms/latency_p95_ms/failure_rate 排序必须同时给出 quality_definition_id 与 quality_target_id，按同测量定义、同目标范围比较；
速率排序必须给出 window_seconds，先汇总同窗口字节再算速率。定义/目标标识须语义校验，不能只因字段存在就混合不可比样本。
Top N 截断后仍保留全量、可见、隐藏与未知归属总量，不能将各维度总量相加。

分页 cursor 绑定 data_version、attribution_version、retention_generation、cutoff、view、完整过滤、排序与窗口；
后续页须提交相同查询（cursor 除外），快照被清理时 `SNAPSHOT_EXPIRED`，不能无提示刷新。
字节为无前导零的十进制非负整数字符串；不可知值为 null 并带 reason/coverage，不能填 0。
入口已观测量、未知字节、归属未知、缺口和协议开销分开。
Amount.value=null 时 coverage 只能 partial/unknown；coverage 为 partial/unknown 时必须有非空 reason，
已知非空值也可带 partial/unknown 及缺口说明，不能把“已观测到部分值”当作完整。
reconciliation 固定为 pending/not_comparable/balanced/error：分别表示尚待对齐、边界不可比、同口径已平衡、同口径存在无法解释的异常差额；不能将异常截零或改成平衡。
PID 必须与进程实例/启动身份关联；名称合并只是投影，不销毁实例事实。
保留策略启用/缩短先预览确认，应用后自动清理；查询只显示 pending 状态，不能触发删除。

## 查询 data 的最小规范字段

以下是未被响应业务 Schema 全覆盖的规范字段；空对象不符合这些成功结果。
具体嵌套结构须在实现交付前补齐版本化 Schema 与 fixtures，不能据当前信封校验宣称接口已实现。

| operation | 成功 data 最小内容 |
|---|---|
| status.get | revision、desired_runtime、service/core/collector 状态、degraded 原因、实际 capabilities 与新鲜度 |
| doctor.get | 检查项列表，每项含结果、证据类型、不可读/缺失原因和建议；不执行建议 |
| config.get | revision、committed intent 或 null、draft intent 或 null、draft 摘要/状态；draft 非 active revision |
| subscription.list | 来源 ID、不可变快照摘要、更新状态/时间/错误与是否待应用；next_due 保留原尝试预约，effective_next_due 表示当前 committed 周期下的有效到期时间；不含真实 URL 或认证值 |
| group.list | 逻辑 ID、来源范围、策略、跟随关系、当前选择及新鲜度；静态意图和实际选择区分 |
| app.inspect | 应用 ID、模式、已配置入口是否可用、子环境差异、bypass、计量与失败原因 |
| route.explain | 输入条件、按顺序规则判定、未知原因、意图目标、实际连接证据（有则附来源/时间） |
| connections.query | scope、快照/截止点、查询条件、连接记录、next_cursor；每条保留实例/世代、目标与实际链证据，不可见字段有原因 |
| operation.get | operation_id、phase、base_revision、target_revision、outcome 及错误/恢复证据；不得把 pending 当 committed |
| probe.run | subscription_id、node_id、目标 URL 的脱敏表示、开始/结束时间、测量定义/单位、结果、耗时或 N/A 原因；非业务延迟 |

应用目的地只在有序 rules 中表达，apps 不含 route；便利编辑入口修改对应规则并预览位置，不建立隐式优先级。
P1 managed run 只使用已应用 profile 的命名 Mihomo 入站，同 profile 多 launch 可共享应用上下文；
此上下文不证明真实 PID 或 launch 链。没有可用已配置入站时明确失败并要求 plan/apply，run 不隐式重载核心。
direct/inherit 可读取显式本地草稿 profile，无需启动服务，也不改变草稿或活动 revision。
计量时每 launch 的 GOST 入口可转发到该 profile 既有入站；真正跨跳身份仍待 P0 证明。

保留配置缺省为 disabled、days=30；enabled=true 或缩短天数必须经 plan/apply 预览确认。
禁用表示不自动删除，不承诺无限容量；容量不足须明确失败/缺口。延长策略不恢复已删除事实。

## Schema 覆盖声明

[api-v1.schema.json](api-v1.schema.json) 的入口支持请求与响应信封，参数按 operation 严格区分；
新规划使用 [plan-v2.schema.json](plan-v2.schema.json) 的完整影响预览；[plan-v1.schema.json](plan-v1.schema.json) 保留历史读取／精确请求重放。两者的意图引用仍为 Intent v1。
Schema 不能验证 duplicate keys、正文大小、argv 一致性、摘要计算、引用存在性、时序、权限、运行效果或分页快照真实性；实现须单独验证。
[service-lifecycle-v1.schema.json](service-lifecycle-v1.schema.json) 补齐 T033 status/operation/control-only 的数据形状。
响应 data 的覆盖边界在 API Schema 中逐项说明；不将一般 object 宣称为完整业务验证。
[usage-v1.schema.json](usage-v1.schema.json) 仅为 P2 流量聚合页的代表结构，不涵盖完整连接历史、延迟分布和节点分布。
后者仍受[统计设计](../../../docs/traffic-statistics.md)及规格约束，交付前须补齐相应版本化结构与测试。

身份槽位已满时，新 ID 返回 `OPERATION_CAPACITY_EXHAUSTED`（退出码 4，`retryable:false`），不隐式删除或清理历史。已接纳 ID 仍先精确重放原回执；同 ID 异内容仍返回 `REQUEST_ID_REUSED`。
