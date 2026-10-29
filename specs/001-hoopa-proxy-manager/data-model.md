# Hoopa 数据模型、状态与一致性边界

日期：2026-10-22。依据：[规格](spec.md)、[研究决策](research.md)。这是实现设计，不是迁移脚本。
字段名描述逻辑模型；SQL 列、索引和驱动在 P0 对照实验后定稿，禁止提前生成完整库表实现。
本文按[ADR007](../../docs/adr/007-p0-evidence-closure.md)同步 connector、派生恢复报告和质量/线路 planned profile；新增逻辑字段不表示当前严格 v1 已支持，旧制品与失败证据不重新贴标。

## 1. 权威、存储与通用类型

| 数据域 | 权威与存储 | 依赖阶段 |
|--------|------------|----------|
| 作者草稿 | 用户配置目录中的严格版本化 YAML；编辑不等于应用 | P1 |
| 已提交意图/活动配置 | 状态目录的不可变 bundle 与 active.json 指针 | P1，无 SQLite |
| 未完成操作与进程归属 | 持久操作日志、运行登记、私有凭据引用；持久写锁在 state，runtime 放 socket/临时资源 | P0/P1，无 SQLite |
| provider/规则资产 | 状态目录内不可变快照及摘要，下载 cache 可丢弃 | P1 |
| 计量事实和历史 | 可选本地 ledger.sqlite，单写入者，独立于基础管理 | P0 探针/P2 |
| UI 与机器输出 | 上述权威的投影，不成为另一份可写配置 | P1/P2 |

`Id` 为 1–128 字符本地稳定 token；`Revision` 是不可复用的配置提交身份，`Generation` 是完整编译快照身份。
二者不能代替核心进程的 `RunId` 或计数器的 `CounterEpoch`。源节点、连接和样本 ID 均带来源/世代命名空间。
时间用 UTC RFC3339；持续时间使用单调时钟，单位 ms。字节对外为非负十进制字符串，未知为 null 并带原因，禁止浮点 JSON 近似整数。
存储增量使用经过范围检查的整数；超出数据库/算术范围时报 INTEGRITY_ERROR，不环绕或截零。跨大量事实求和使用精确累加并检测数据库 SUM 溢出。

目录默认沿用 XDG 的 config/state/cache/runtime 分工；只有显式初始化、启动或写命令可创建它们。
active.json、操作日志和运行登记不能只放在注销/重启会清空的 runtime 目录。
Linux 文件格式、runtime 回退、私有权限、state 持久写锁及发布失败边界，按
[ADR012](../../docs/adr/012-p1-file-store.md)和[文件存储 v1](contracts/file-store-v1.md)实施。
运行意图、一次性启动票据、exec 前出生与核验后登记按
[运行记录 v1](contracts/runtime-records-v1.md)和[ADR013](../../docs/adr/013-p1-runtime-ownership.md)
实施；读取旧记录不能重新获得启动或信号许可。登记保存当时的核验快照，
当前运行状态须另行实测，不能回写历史 gate 来掩盖崩溃窗口。

## 2. 意图、节点与规则实体

### IntentDocument 与 ConfigBundle

- IntentDocument：schema_version=1、core、subscriptions、selection、groups、apps、rules、default_target，以及 P2 的 retention。
- core：Mihomo 及可选 GOST 的 path/version/sha256，不存“latest”。引用安装职责提供的二进制，不升级核心。
- 草稿允许暂缺活动来源或引用；应用必须通过语义完整性、能力、依赖及安全校验。
- ConfigBundle：generation、compiler_version、intent、全部 provider/rule/binary 摘要、能力摘要、生成配置与验证记录。
- ActivePointer：revision、generation、manifest_sha256、operation_id、desired_runtime(running/stopped)。非空 generation 只能指向已持久化完整 bundle；初始可为 null。
- revision 随已提交配置、保留策略或核心运行意图变更前进，不随遥测/查询/probe/仅下载候选前进。停止后仍保留最后核验快照，但 desired_runtime=stopped 不代表核心正在转发。
- 配置指针是发布点；用户草稿不是运行权威。成功查询同时区分草稿、已提交意图和实际运行偏差。

完整意图结构以 [intent-v1.schema.json](contracts/intent-v1.schema.json) 为准；YAML 与 JSON 是同一模型的输入表示。
YAML 解析还须拒绝重复键、危险标签、过深结构和超量 alias；JSON Schema 不替代这些解析限制。

### Subscription / ProviderSnapshot / Node

| 实体 | 关键字段 | 关系与校验 |
|------|----------|------------|
| Subscription | id、source(kind/path 或 url_secret_ref)、update_hours、last_attempt、last_success、next_due | URL 秘密来自私有引用；周期为 null 或正整数小时 |
| ProviderSnapshot | snapshot_id、subscription_id、sha256、observed_at、parser_version、node_ids、validation | 不可变；有效下载不等于运行应用；旧可用快照保留 |
| Node | subscription_id、node_id、display_name、identity_fingerprint、region_evidence、connection_secret_ref | ID 与显示名分离；同名不覆盖，歧义不自动匹配 |
| RuleAsset | asset_id、sha256、format、source、validation | 本地固定资产；编译/启动不暗中联网补齐 |

节点指纹规范化协议、端点及身份参数，排除显示名；包含秘密的指纹使用私有键处理，不输出原值。
供应商认证/身份变更按新身份处理；失去映射键或发现冲突必须显式处理，不能将固定 pin 重新绑定同名节点。
地区从本地明确映射、已声明名称规则得到；unknown 不猜为 US/JP，也不声称地区标签证明真实地理位置。

### LogicalGroup / Target / OrderedRule

- LogicalGroup：id、required、region，以及 manual/source/selected_node_id、auto/source/probe 或 follow/follows 三种互斥策略。
- source 为 active 或显式 subscription；US/JP/BEST 默认跟随活动来源，follow 指向另一个稳定逻辑目标。
- Target：group(group_id)、node(subscription_id,node_id)、explicit direct、reject 四种互斥引用。
- OrderedRule：id、match、target；数组位置即优先级。应用目的地只通过这里的 app 条件表达，profile 不再有隐含优先级覆盖。
- match：app、原始 TCP connector 的 process_instance 及 connect 时 name/path 快照、domain exact/suffix、ip_cidr，或这些条件的 all；递归深度上限 16。

验证无环/无悬空引用，必需组非空，手选属于该组已解析来源，所有 pin 的来源快照均完整。
编译显式拒绝型空组行为，不使用核心默认首节点、COMPATIBLE 或其他来源后备。default_target 必须由本地显式给出。

规则采用三值结果 true/false/unknown：首个 true 命中；当前规则 unknown 且可能影响结果时拒绝连接。
all 中存在确定 false 则整体 false；全部 true 才为 true；其余为 unknown。更早规则已确定命中的流量不受后续未知条件影响。
实例、名称、路径按字段分别声明能力，缺任一规则所需能力在配置加载时拒绝；运行时所需字段 unknown 按上述三值语义处理，不能把永久拒绝所有流量称为规则可用。路由前须验证建连快照与原 accepted socket 的关联，原生核心字段不能自动证明这种语义。

### AppProfile 与 Launch

- AppProfile：id、mode、metering、bypass；应用目标是 OrderedRule 的关联投影。metering=true 只允许 managed。
- Launch：launch_id、app_id、mode、metering、started_at、ended_at、exit_status、entry_lease_id、profile_revision、wrapper_run_id。
- managed 使用已应用 profile 的 Mihomo 命名入口；同 profile 可共享该应用上下文入口，但不能据此认定源 PID。
- 完整计量候选直接进入具备已验证事件能力的Mihomo入站；GOST链仅为兼容/累计对照。launch通过已验证的入站/认证或进程登记关联，不能推导时保留launch_unknown；新增入口不得隐式重载核心。
- 未配置可用入站时明确失败并要求显式 plan/apply。direct/inherit 可读取本地草稿 profile，无需启动管理器或核心。
- 不默认保存完整 argv、环境、prompt 或业务正文。退出码、信号和有证据的后代状态独立记录。

## 3. 操作日志、计划与配置恢复

### Plan 与 Operation

Plan 包含 schema_version、base_revision、intent、不可变输入摘要、compiler_version、capabilities_digest、impact、blockers、plan_digest。
impact 包含 runtime_action(start/replace/select/none)、受影响应用、新旧 generation、连接影响和是否需接受中断。
预览是纯计算，计划可由用户显式输出到文件，但不创建内部候选、目录、数据库或服务。
摘要使用 RFC 8785 规范化后的 JSON（排除 plan_digest 字段）再 SHA-256；它绑定内容和确认，不替代身份授权。

Operation（仅已接纳的持久变更）：operation_id、request_id、request_digest、base_revision、target_revision、previous_generation、target_generation、phase、
candidate/impact 摘要、已验证进程身份、每步结果与恢复证据。request_digest 覆盖版本、操作名及完整参数。
同 ID/同内容返回原结果；同 ID/异内容拒绝。未完成日志不得回收；压缩终态正文时保留去重 tombstone。
去重存储达到容量时拒绝新写操作并报告维护需求，不能忘记旧 ID 后悄悄重放。只读请求的 request_id 仅关联响应，不建立持久操作或审计记录。
产品 operation.recover 的调用请求保留独立去重记录，params.operation_id 指向原目标；恢复推进原目标且保留其原身份/摘要，调用结果与目标阶段分别持久化并在 T031 消歧，详见[操作日志v1](contracts/operation-journal-v1.md)。

```text
prepared → validated → applying → verifying → publishing → committed
                         │           │             │
                         └───────────┴─────────────┘
                                     ↓
                               rolling_back → rolled_back
                                     └──────→ degraded
```

prepared/validated 可以是只读计划中的计算结果；持久操作在确定接纳新写操作后才建立。
committed/rolled_back 是经实际核验的终态；degraded 表示未能恢复确定状态，禁止自动写操作，查询仍可用。
select 仅是通过额外探针后允许的优化；首期拓扑变化使用完整替换。

### 根代理确定的写入顺序与锁边界

1. 读取请求身份，若已有同内容操作则返回/查询既有阶段；不要先将合法重试判为旧 revision。
2. 新操作取得实例的写入权，复核活动 revision、全部输入/二进制摘要和能力、影响确认。
   过期返回 CONFLICT，既不创建本次持久操作，也不改文件或运行状态；不能自动合并或重新规划。
3. 形成不可变候选，校验全部来源、规则、应用入站与核心配置；写操作可执行隔离核心校验，预览不能代做。
4. 写并持久化操作身份、旧/新 bundle 和 applying 阶段，再改变核心运行态。写临时文件与 rename 必须同一文件系统，持久化文件及父目录。
5. 完整替换先关闭新流量入口；加载本地资产、还原选择、核验组/拒绝行为/所有应用入站后开放。
   旧连接可能中断，不能宣称跨连接原子迁移。若固定版本无法安全分阶段开放，此方案门槛失败，不以临时开放默认路由代替。
6. 核验实际状态后记录 publishing，再原子替换 active.json，最后持久记录 committed。
   pointer 发布与日志终态是两个可崩溃点，不宣称一个跨文件/进程事务。
7. 失败时记录 rolling_back，恢复旧完整快照并核验，随后恢复旧 pointer、记录 rolled_back。
   任何无法证明完整旧状态的情况记录 degraded；保留候选、证据和进程，不重复启动或按 PID 盲杀。

只有一个管理写入者；Linux 实例锁保护进程间所有权，服务内串行协调器保护运行变更。
网络下载/纯编译在应用临界区外，但进入写阶段必须复核身份。下载成功的候选不自动变为活动 revision。
锁顺序固定为控制写入权 → 账本写入者/短 SQL 事务；账本写入者不得持 SQL 事务反向等待控制锁。
采集提交不取得控制写入权，避免长配置替换阻断所有计数；清理批次以短维护操作取得控制写入权后提交，保证策略发布与批次边界一致。

### 重启恢复判定

| 持久记录与实际观察 | 允许的结果 |
|------------------|------------|
| 尚未改变运行态的已记录候选 | 复核 revision/输入后继续，或明确取消；不忽略已发生的并发变更 |
| pointer 指向目标、目标运行核验一致、终态日志缺失 | 补记原 operation committed，不再应用一次 |
| pointer 仍旧但候选可能运行 | 先验证当前实例；证据足够则完成原发布或恢复旧态，不能仅看文件选边 |
| rollback 阶段中断 | 继续验证旧快照；不足则 degraded，不先报告已回滚 |
| PID 被复用、端口被占、二进制/凭据身份不符 | 冲突；不认领、不 kill、不新起重复核心 |

仅显式普通 start 或 operation.recover 进入可能改变核心运行的恢复流程；control-only/前台服务启动只观察、核对和报告待恢复状态，不隐式启动/替换核心。
status/doctor/operation.get 不执行恢复动作。

runtime.stop 在杀进程前先持久记录目标 stopped 和操作阶段，核验所属进程已停止后才发布新的 revision/desired_runtime。
中途崩溃时恢复须先处理这个停止操作，不因 active 仍旧或核心消失便当作意外故障重新启动。
自动核心重启仅在服务仍运行、已提交运行意图为 running、无待恢复/降级状态且用户授权的启动范围内进行有界尝试；control-only 模式不启动核心。
runtime.start 同样在核验运行后提交 running；service.stop 不改变核心运行意图，不主动终止存活核心。

## 4. 运行身份、入口与租约

| 实体 | 字段 | 不变量 |
|------|------|--------|
| ManagedProcess | instance_id、role、run_id、binary_path/hash/version、PID、OS_start_identity、start_nonce、private_workdir、config_generation、control_identity_ref | PID/端口/API版本单独均不足以认领 |
| EntryService | service_id、app_id、launch_id、protocol、listen_ref、auth_ref、upstream_inbound_id、run_id | 入口凭据/身份随运行记录，不暴露给默认 JSON |
| EntryLease | lease_id、entry_service_id、launch_id、known_processes、connections、state、last_evidence | wrapper 消失不等于后代/连接结束；unknown 不自动回收 |
| CounterEpoch | epoch_id、service_id、run_id、source、definition_version、start_evidence | 管理器重启不生成新 epoch；来源归零需证据或缺口 |

Linux 原型验证 boot ID + 进程启动时刻 + 二进制摘要/私有启动令牌/工作目录和受保护控制端的组合。
启动前持久保存 nonce 与启动意图；spawn 后登记 PID/OS 身份。两者之间崩溃时只允许以完整证据寻找原实例；
证明不足视为冲突，不用 PID 文件缺失推断“没有进程”。P0 必须证明该窗口可处理，不能只写一个待实现 helper 便宣称可靠。

EntryLease 状态为 reserved → ready → draining → released，证据不足进入 unknown；unknown 仍占有入口直到显式检查或停止。
计量入口 ready 前须确认初始检查点能持久化且访问保护生效，再向目标应用交付入口/凭据。
失败只清理已验证属于本次的资源，返回错误；不改用不计量入口。旧租约不因开关计量而修改。

## 5. 计量账本实体与事务

### 原始证据与唯一流量事实

| 实体 | 最少字段 | 唯一性/关系 |
|------|----------|-------------|
| SourceProcessInstance | source_process_instance_id、host/boot identity、PID/PID namespace、OS_start_identity、parent_instance | 原始 TCP connect 发起实例；不是 GOST ManagedProcess.run_id |
| ConnectorEvidence（planned profile） | attribution_basis、instance_ref、connect_observed_at、name_snapshot/path_snapshot及各字段来源/定义版本/状态、原始字节与UTF-8可用性、client_socket_ref、accepted_socket_ref、backend_generation、association_status/reason、fd_transfer_observation | `connect_initiator_v1`；快照与原入口连接的唯一关联在路由前证明，旧 v1 不自动获得该口径 |
| Connection | connection_id、source/epoch/native_id、service/launch、start/end、source tuple、target_ref、connector_evidence_ref、lifecycle_state | 原生 ID 也必须带来源世代；短连接结束仍保留，epoch 结束不能填成网络结束时间 |
| TargetEvidence | original_host、normalized_host、IP/port、source、scope(connection/tunnel/request)、evidence_version | domain/IP-only/unknown 互斥，大小写/IDNA/末尾点规范化 |
| CounterCheckpoint | source、epoch、series_key、last_sample_id、raw_value、observation_time | series_key 包含完整标签；每条序列先做差 |
| ByteFact | fact_id、source/epoch/connection、sample/interval、direction、bytes、boundary、quality、data_version | 稳定样本身份去重；一个字节事实只计入全量一次 |
| AttributionRevision | fact/connection_id、revision、process/target evidence、status/reason、known_at | 补充归属不复制 bytes，不重置事实年龄 |
| Gap | gap_id、source/epoch、time range、byte/process/domain/time/route/quality dimensions、recoverability、reason | 字节未知与归属未知不同；数值未知不填 0 |
| Reconciliation（生产持久模型） | reconciliation_id、source/epoch/flow/opened_ref、fact_kind、direction、boundary、cutoff、known_detail、overhead/unallocated、status | 字节对账的 pending/not_comparable/balanced/error 与恢复报告状态区分；T073 同事务去重 |
| RecoveryReport（P0 派生投影） | source/epoch/flow/opened_ref、last_trusted_checkpoint、scan_cutoff、writer_end_evidence或缺失原因、tail_bytes=null、scan_status、history_quality、可选 reconciliation_id | 由保留原帧重复生成，不是新 source 事件或 ByteFact；pending/gap 不伪造网络终态 |

canonical facts 不分别累加“进程账”和“域名账”。未知进程与未知域名可指同一事实，不把两个桶相加成额外流量。
开销/未分配差额声明是否已包含在事实总量中；数值 gap 不进入已知总量等式。
共享 IP、当前 DNS、相似时间戳、GOST client 标签和启动上下文不能代替真实归属证据。
名称/路径按连接保存 connect 时快照及字段级证据；exec/改名不覆盖已有连接快照。同名合并是查询投影，按名称来源、定义版本和名称值分组，保留各实例与快照。
下一Linux候选按[ADR010](../../docs/adr/010-restricted-identity-profile.md)使用 `executable_path_basename_v1` 程序名及 `task_exe_file_d_path_v1` 路径来源；该能力已通过[T132固定范围原型验收](../../docs/validation/t132-restricted-acceptance.md)，source5独立承载；正式矩阵和生产消费仍待完成，旧wire不变。解释器名表示解释器，路径保留调用者视图及其已验证范围，不表示永久文件身份；保留原字节、删除后缀和UTF-8不可用状态。leader/caller comm和应用显示名使用独立字段及能力，不能补入程序名。新的来源字段与能力须走版本化契约，不升级旧v3/v4的unverified。
多线程建连的SourceProcessInstance使用进程leader TGID和leader启动身份；调用TID及调用线程comm另列，不能拼成另一份进程实例或冒充进程名。内核纳秒start与proc ticks单位分开，PID namespace坐标明确；leader名称读取/并发改名未验证时仅该字段unknown，路径不由comm推导。具体候选及未验证条件见[ADR008](../../docs/adr/008-remaining-evidence-execution.md)。
默认 connector 口径对外显示 `basis=connect_initiator_v1`，模型证据字段为 `attribution_basis`：子进程自己 connect 归子，父 connect 后子写归有真实证据的父 connector；FD 转交/fork 共享不改变连接归属，不表示逐字节 writer。未监测转交时 `fd_transfer_observation=unverified`，不能声称没有转交。connector 缺失、事件丢失、generation/namespace 错配或关联多解才保留相应未知原因；不以 profile/launcher/GOST 或默认父身份代填。晚到补证不改历史路由决定。

### 事件来源与接收位置（ADR 005）

TelemetrySource：source_instance/epoch、核心OS身份/hash、capability/definition、原生source_seq|null、durable census/spool及覆盖。Receipt：source_epoch/event_id、receipt_seq、原payload摘要、持久确认状态。receipt_seq不能填进source_seq；同源同ID异内容拒绝。当前实验journal只保存已送达原文，不是生产spool/账本。

FlowOpened/Closed与Route/Setup/Bytes沿用同一flow/attempt/generation；不能用消失的活动快照推定零。source journal、接收ACK和SQLite是不同提交边界；源spool保留/回放/空间控制必须另验。新mihomo_proxy_stream_v1与旧gost_ingress_wire_v1分边界/来源保留，旧数据不改写。契约见[telemetry-events](contracts/telemetry-events.md)。

### P0 原帧重放与独立恢复报告

权威仍是不可变 manifest、原始 opened/checkpoint/closed/gap 帧及 source_seq/event_id；重放保留原文、原 ID、原 seq。纯 wire/epoch parser 已共享于 source 重开、发送快照、receiver 和 reader，校验字段/重复键、精确整数、身份/generation、序列、生命周期、单调累计和 marker。核心补丁携带同源文本副本及摘要，不链接两个进程或合并 WAL/receipt/SQL 提交边界。T133明文本地最小闭环已完成，具体实现及局部验收见[来源传输验收](../../docs/validation/source-transport.md)；正式G02/生产SQL仍另行验收。

| 原始事实与证据 | RecoveryReport 结果 |
| --- | --- |
| opened 缺 final，没有 writer 结束证明 | pending，保留最后可信 checkpoint，未知 tail=null |
| opened 缺 final，显式启动/恢复持受管 source 目录独占 writer 锁，完成稳定快照全扫描并证明旧 source epoch 结束 | gap，reason=epoch_ended_without_final；checkpoint 保留，tail=null；不填精确网络结束时间 |
| 完整可信 closed（含尚未投递的 closed） | 原 ID/原文精确幂等重放，终值只结算一次，不额外制造 missing-final gap |
| 已有维度 gap 且有精确 closed | 字节保留精确值，历史维度缺口继续显示 |
| 损坏、缺 manifest 或无法取得稳定可核验快照 | integrity_error/unavailable，保留原字节并拒绝新完整计量，不自动修尾或换空日志 |

writer 锁只证明目录 writer 互斥，不证明核心或网络连接结束；只读 reader 没有结束证明时保持 pending，不取得写锁修复或落报告。完整 Write 后 Sync 不确定的帧，只在重开严格校验及 Sync 成功后进入已确认重放集合；新目录项另须持久化目录。sender 只读已确认截止点，receiver Write+Sync 后 ACK，历史重放不等待 SQL 提交。

可选 `reconciliation_id` 由固定命名空间与原 source/epoch/flow/opened ID、缺 final 事实 kind 确定，不含本次 boot/nonce、时间或 cutoff。同 ID 同事实幂等，内容冲突拒绝；报告不推进 source_seq/receipt_seq，不新增 ByteFact。P0 可反复从保留日志生成报告，无须新增来源恢复事件、v2 恢复引用图或为所有旧流预留新事件空间；生产持久 Gap/Reconciliation 与回收分别由 T073/T138 实现。 生产引入GC前，T138须确保接收端已有可恢复的manifest、原帧，以及（恢复判断必需的来源结束证据或等价已持久稳定恢复事实）；不能仅凭字节帧ACK删除唯一推导材料。转运格式另在T138冻结，P0不提前实现。

pending 不发布不可恢复缺口事实。持久事实仅在旧 epoch 已结束且稳定全扫描后生成；同 ID 冲突比较只覆盖不可变事实字段（旧来源引用、最后可信 checkpoint、边界及未知尾部），不比较每次报告的扫描/验证时刻等观测元数据。重复报告可携带新的观测元数据，不能因此新建事实或报事实冲突。

历史 history_quality/gap、扫描 scan_status 与当前 recording_status 分开。永久未知旧尾部不阻止已验证的新记录；新 epoch manifest/日志、终末及 marker 预留、opened 持久化能力未确认、完整性错误或容量不足才拒绝新完整计量。启动扫描在新 flow 开放前；运行期保持 flow→source 锁顺序，不持 source 锁等 flow 锁或网络 ACK。P0 不回收，实际容量/epoch 上限与重开扫描成本须按固定候选记录，不承诺无限历史。

### 一次样本提交

1. 在事务外取得并校验来源身份、epoch、完整 series、样本身份、计数边界和精度。
2. 开始短事务，检查该样本/序号是否已存在，读取原 checkpoint；重放返回既有结果。
3. 同一 epoch 的有效非负增量写唯一 ByteFact；同一事务写新的 checkpoint、必要 gap/reconciliation 状态和 data_version。
4. commit 成功才推进内存确认位置。commit 返回不确定时重新按样本身份查询，不盲加一次。
5. 缺失 series 不当作 0。无解释下降保留异常/缺口并停止该序列差量推进；不能截成 0 或自行跨进程接续。

入口与连接采集各自提交，不假设二者同一时刻。对账只在方向、边界、epoch、截止点对齐后执行。
首个样本只建立有证据的初始位置；未受控运行在纳管前的字节不追溯虚构。已存在持久 checkpoint 的接管恢复必须沿用旧世代。
采集/保存故障保留当前转发，不静默改路；有界队列溢出或不可重放尾段记录缺口，新计量启动不能确认记录能力时失败。

## 6. 质量样本与实际路由

以下身份逻辑字段仍属T132 planned profile；质量/线路已按[严格v2契约](contracts/telemetry-quality-v2.md)实现并有[局部验收](../../docs/validation/quality-ready.md)。v1不支持这些新增字段（包括cancelled结果），仍保留原文及解码；每epoch固定版本。v2不升级身份证据，不新增恢复事件；以下逻辑模型不代替v2精确wire字段。

QualityDefinition：type、version、起止定义、source、protocol、ready_kind、target_scope、unit；按 definition/ready_kind/protocol/目标范围分组，不混为一个排名。

T134的协议完成证明附着到实际Conn及其actual_connection_ref，同进程单调ready完成点独立于finish/落盘时刻；未知外层不能透传内层TCP证明。[ADR008](../../docs/adr/008-remaining-evidence-execution.md)定义实现约束；006候选已在所列三类协议/inline线路范围验证，正式T129仍待执行。
QualitySample：sample_id、source/epoch/flow/attempt、actual_connection_ref|null、definition_ref、observed_at、monotonic_start/end、duration_ms|null、observed_wait_ms|null、outcome、failure_stage、route_evidence_ref、coverage/reason。
outcome 区分 success/failure/timeout/cancelled/unknown；失败/超时/取消保留可证等待及阶段，未校准/无法观测 ready 时 ready_kind=ready_unproven、reason=ready_unknown、duration_ms=null；只有结果也不可判定时 outcome=unknown，已证失败/超时/取消不得被覆盖。observed_wait_ms 保存同边界可证等待，不进入成功耗时分布。起止须来自同进程单调时钟及同一边界，UTC 只作展示；flow 从 target_known 到协议 ready 包含身份关联等待、DNS、选路与重试，attempt 只覆盖本次尝试。

| ready_kind | 可证成功边界 |
| --- | --- |
| tcp_connected | DIRECT 目标 TCP 建立，不代表 TLS/业务成功 |
| http_connect_accepted | 上游 CONNECT 成功响应完整解析；通用协议为 2xx，固定候选只接受并拟校准 200 |
| socks5_connect_accepted | VER/RSV/REP 与完整地址校验成功；仅地址可解析不成立 |
| ready_unproven | 无法观测/未校准协议，reason=ready_unknown、duration_ms=null，结果另按证据分类 |

RouteEvidence：source/epoch/flow/attempt、actual_connection_ref、tracker_ref、config_generation、rule_id、logical_group、stable_node_id/provider_id、ordered_chain、evidence_source/time/completeness。实际 Conn 产生时绑定原 attempt/generation 与 tracker；Dial 未取得 Conn 的失败只能保留 selection_only/unavailable，不从选择意图推导实际链。节点显示名不是稳定 ID，改名/切组不改已有历史；缺证据保持未知。

生成bundle以持久node/provider/group引用作为core内部名称，展示别名单列；adapter随机实例UUID不充当跨重开稳定ID。需在配置加载核验映射后，才能将实际chain解释为稳定节点引用；手工配置未有此证明时稳定字段不可用。

ConnectionSegmentBytes：segment_id、source/epoch/flow/attempt、actual_connection_ref、route_evidence_ref、boundary_ref、direction、actual_n、sample/interval、quality。预读、partial Write 与 relay 按所属段实际 n 记录，不按请求长度；相同边界/方向/截止点满足 `flow总量 = 可证连接段总量 + unallocated`，视图不再相加。只能恢复 flow 累计而无可信分段时，不把它归给最后一条线路，节点维度保留缺口。

forward_setup 定义见 [research.md](research.md#9-统计质量与自动保留r08)；HTTPS/复用无法观察请求起止时为 N/A。lazy 握手未证明确认也保持 ready_kind=ready_unproven、reason=ready_unknown，Dial nil 或 NeedHandshake=false 不自动证明 ready。HTTP CONNECT成功头与首payload同包保留/不重计已有005定向修复及006真实回环证据；不能推广到未测协议。
延迟分位数从同定义原始样本或误差可说明的兼容直方图重算，不平均 p95；单位窗口量与速率分别返回。

## 7. 查询快照与保留清理

QuerySnapshot：data_version、attribution_version、retention_generation、cutoff、filters_digest、sort/grouping、cursor_position。
第一页固定这些值，后续页保持同一逻辑视图；排序使用稳定 ID 作最终 tie-break。
连接生命周期和归属修订也按版本取值，不能只固定字节表却让其他维度实时漂移。
默认总字节降序；缺值/不可比较值排已知值之后，原状态保留。
结果包含全量/已展示/隐藏/未知总计、口径和覆盖；Top N 的小计不能冒充全量。

旧快照遇到数据已清理、版本不能再重放或游标过期，返回 SNAPSHOT_EXPIRED，要求显式新查询；不从变化后的排名继续拼页。
只读打开条件不足返回 READ_UNAVAILABLE，不创建 WAL/SHM、不恢复或迁移。

RetentionPolicy：policy_revision、enabled、days(default 30)、confirmed_impact、effective_at；初始不自动删除，启用需确认。
CleanupRun：cleanup_id、policy_revision、cutoff、batch_position、state、error；状态 pending/running/completed/failed。
ExpiredRange：from/to、删除范围/质量、observed_deleted_at、policy_revision，不保存不需要的业务内容。

已确认策略启用后自动过期清理；服务暂停期间标待清理，恢复后限量执行，不偷偷创建系统任务。
每个短批次先核对当前授权策略，删除早于 T−days×24h 的记录；边界等于起点保留。记录删除范围并推进 retention_generation，使不再可服务的游标明确失效。
保留仍需使用的 checkpoint、活跃连接/租约、引用中的进程及必要 gap/删除范围；删除无引用维度须符合外键一致性。
原始事实观察时间决定年龄，归属修订/迟到导入不续期。跨边界无法精确分段则保留原区间与不确定性，不比例拆字节。
延长只扩大后续保留范围，已删除区间显示不可用；不用每日总量或零冒充被删明细。

## 8. 能力、支持与验收记录

CapabilityRecord：核心版本/hash、平台/内核/namespace/地址族范围、事件 profile 版本/计数边界、可用规则/入站/只读端点/计数定义、证据 run_id、限制。identity_instance、identity_name、identity_path 分字段记录来源、backend generation、捕获/入口关联证据；setup 按 protocol/ready_kind/definition 单独声明，actual_route 和 segment_bytes 另验。模型字段存在不等于能力通过，旧 v1 不重新贴标 connect_initiator_v1。
ResourceEvidence：固定后端/profile、helper RSS/CPU、kernel map 内存/容量、每连接事件量、入口关联等待、重开扫描耗时/峰值内存、日志上限/epoch/未终结 flow 测试规模。helper 与内核资源计入整套预算，不能只测管理器 RSS。
能力记录只适用于其绑定的核心/协议与平台范围。T138修改核心或协议后须追加新manifest/CapabilityRecord，由主代理按真实diff列出受影响门槛、完成必要复验并解释未受影响证据的引用；不得将旧hash的passed直接赋给新hash。T072只消费已重新确认的当前来源，T077在ready前核对实际path/hash、平台、协议/边界及能力记录；不匹配或缺证据时拒绝新计量，不因查询自动复验或变更核心。

T135的[受保护实验契约](contracts/protected-core-v1.md)已实现局部配置准入：generation与一份严格校验的配置原字节hash绑定，启用trace还须核对同generation、全部inline节点映射及来源可写状态，再开放入口。status返回的三个identity字段均为false，所有进程规则加载期拒绝；此响应不是已通过正式矩阵的生产CapabilityRecord。排空按已接受TCP保留旧generation，完整replace以独立新进程/端口验证，跨进程唯一generation分配、持久Operation和恢复仍属T136/后续管理器。
PlatformEvidence：platform/arch、构建/回环/真实应用/SSH或注销/休眠/恢复/性能各项的 passed/failed/unverified 与证据引用。
MigrationPlan：输入摘要、差异/冲突、备份与恢复目标、影响确认、执行 operation；正式执行前复核输入，旧工具变化返回冲突。

所有支持声明由上述证据产生。模型中存在字段、Schema 可验证及 mock 通过均不能生成真实平台/身份能力的 passed 记录。
