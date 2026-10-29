# Telemetry events：候选契约 v1

状态：P0 候选；不是 stock Mihomo/GOST 已有 API，也不是 agentcfg 直接读取的接口。依据 [ADR 005](../../../docs/adr/005-telemetry-replan.md)，已有隔离部分原型，完整来源能力尚未验收。

2026-10-23最新状态：本文保留严格v1及设计历史；T134已另行冻结并实现[质量/线路v2契约](telemetry-quality-v2.md)，见[局部验收](../../../docs/validation/quality-ready.md)。新候选逐epoch固定v1或v2，v1原文与解码保留。v2只增加质量、实际线路和连接段字段，不升级ProcessEvidence为已证connector，也不新增恢复事件。本文下方planned质量描述由新契约的精确字段取代；T132身份profile仍待实现/验收。T135已按[protected-core-v1](protected-core-v1.md)完成加载期能力拒绝与受保护入口的局部验收，身份能力仍为false。

## 身份与版本

| 字段 | 语义 |
| --- | --- |
| schema_version | 主版本1，未知主版本拒绝 |
| source_instance_id / source_epoch | 绑定核心路径/hash、boot/PID/start/nonce及计数连续性；管理器重启不更换来源世代 |
| event_id | 来源epoch内稳定唯一，重投不变，同ID异内容冲突 |
| source_seq | 来源产生并持久化的顺序；legacy未提供则null，不填接收端序号 |
| flow_id / attempt_id | 解析目标时即有flow，失败亦可关联；每次建链尝试独立ID |
| observed_at / config_generation | UTC RFC3339及事件当时配置；不能从当前active补填 |
| boundary_ref | 计数定义及版本；方向/世代/边界/cutoff一致才可对账 |
| receipt_seq | 接收journal持久确认后分配，独立于source_seq |

ProcessEvidence 的原 v1 字段和证据类别按固定候选原样保留；其 PID、启动身份、boot/PID namespace、名称/来源、原 socket、观测时点及可信范围不自动证明新 connector 口径。comm 名不是 executable path，launcher/GOST/launch 不能代替源身份。下文 planned profile 分字段描述实例、名称及路径能力，当前严格 v1 不接受其新增字段。

目标来自实际代理请求或已证核心metadata，host/ip_only/unknown互斥；隧道目标不等于加密复用内每个业务域名。禁止IP反查、时间近似或当前DNS猜配。

## 事件与数值

事件kind为opened、route_decided、setup_finished、bytes_checkpoint、closed、gap、epoch_closed。流事件沿用原flow/attempt/generation；epoch_closed属于source scope，flow_id/attempt_id为null，不能编造一个连接ID。route_decided保留当时身份及规则；actual route另存provider/node/ordered_chain，不能用当前now覆盖。进程规则在数据面执行，不逐连接调用管理器/SQL。

字节用十进制uint64字符串，检查范围。checkpoint/closed是双向累计，不得把final当新增字节再累加；SQLite消费者按稳定样本算delta并同事务提交checkpoint/delta/gap/version。原型read/write、缓冲预读、lazy handshake和失败写入口径冻结后才能启用 `mihomo_proxy_stream_v1`。旧 `gost_ingress_wire_v1` 分源分边界保留，不改历史或合并数字。

setup保存definition/target/protocol、可证明的单调起止、duration_ms|null、outcome及failure_stage。目标已解析到实际通道ready包含DNS；重试按attempt区分。HTTPS正文/复用内业务TTFB为N/A，probe不代替业务质量，失败/超时不当成功耗时。

## 确认与故障

1. 校验来源/世代/版本/事件ID/大小/字段后写journal并Sync，成功才ACK。写入结果不确定则停写，重开按原ID消歧。
2. 同ID同原文去重、异原文拒绝。实验按原始字节相等；生产需保证重投字节稳定或版本化规范化规则。事件契约解析须拒绝重复字段，通用journal只验证opaque JSON对象，不代替契约解析。
3. 接收重放只恢复已送达事件。未送达不可由receipt序号证明没有丢失；来源必须提供可恢复spool或durable opened census与显式gap。
4. 完整计量接流前持久 opened；缺 final 且无 source writer 结束证明时为 pending，只有显式启动/恢复持 writer 锁并完成稳定快照核验、证明旧 source epoch 结束，才派生 epoch_ended_without_final gap，tail=null，不冒充网络结束。周期 checkpoint 保留且只缩小故障窗口，不承诺零损失，也不插值逐秒历史。
5. 坏帧不自动丢尾或换空库；满盘/背压拒绝新的完整计量，已有转发保持线路。源日志/ACK/SQLite不构成共同事务。
6. 实验journal的单writer内存Replay不代表生产spool的并发读取、空间上限、消费确认和保留已经实现。

默认输出不含凭据、完整argv/环境、URL query、请求正文或模型内容；探针原文只存私有目录。

## 能力与验收

basic_proxy、activity_snapshot、cumulative_ingress、source_process_evidence、durable_lifecycle、setup_timing、actual_route_events、process_rule_tristate分别记录平台、版本/hash及证据。process/path字段、/connections或duration的存在均不能自动置完整能力为true。

缺能力拒绝对应配置。完整计量仍需100预登记真短流、共享/权限/PID复用、collector/core退出、未投递/重复/ACK丢失及两线路历史矩阵；journal测试不替代G01–G05。

## T126 计数子集准备

已实现的[实验解码器](../../../experiments/mihomo-events/README.md)仅覆盖原生 `bytes_checkpoint` / `closed` 的13字段扁平计数帧。
schema_version仅接受数字token `1`；source_seq为非零uint64十进制字符串，upload_bytes/download_bytes为uint64十进制字符串，
observed_at使用UTC `Z`；拒绝未知/缺失/重复字段、null、额外JSON、非法ID和非精确数字。
该子集要求原生source_seq；无此字段的legacy GOST回调继续留在独立接收记录，不用receipt_seq补填。
隔离补丁已提供opened/身份查询/选路/候选计时/closed/gap的原型，尚无周期checkpoint、epoch_closed及完整生产来源/消费者；
解析通过不证明source_seq跨帧连续、身份真实、边界已验证或终末已持久化。
route_decided中phase=connection_binding保存原flow/attempt与tracker UUID映射，不能替换更早的规则决策事件。
closed目前是flow累计，attempt_id保留其终末关联；重试字节逐attempt拆分尚未验收，不能将整个flow累计归到最后一条线路。
来源故障导致route/setup缺口时，原型保留可证明的closed字节并另记gap维度；字节本身不确定则不产生精确closed，保留gap/census。
上游观测点与范围见[具体提案](../../../experiments/mihomo-events/upstream-proposal.md)，实际状态见[验证记录](../../../docs/validation/native-candidate.md)：
T126已取得可运行候选；T127的100流身份断言失败，T128/T129未执行，完整门槛未通过。

## 后续能力开发与验收顺序

2026-10-23复核后，T130/T131先诊断同拓扑前提并裁决具体身份证据方案，T132取得最小正负真实身份；T133/T134/T135分别补全生命周期/严格事件、协议ready/实际线路、配置加载期能力拒绝及入站保护，T137冻结新候选后再运行T127–T129。原manifest和失败run不可覆盖。T136负责控制/恢复最小harness，不能等生产管理器完成才证明P0前提。

P0源日志不回收的实验限制不能作为生产容量保证。P2的T138另实现source→持久接收journal的认证、严格解析、回放及有界回收；source收到持久接收ACK后才有回收资格，接收journal须等SQL消费位置与对应事实同事务提交后才有回收资格。提交不确定保留最后可恢复副本，端到端SQL联动由T073/T088验证，不将两次提交宣称为同一事务。
来源段由核心单writer按确认协议回收，Hoopa不直接修改核心活动日志；接收journal由自身writer回收。源码边界分别在外部核心补丁和Hoopa接收/消费端，不通过链接核心库或共享数据库合并提交边界。
T138产生新核心/协议制品时须追加不可变manifest/CapabilityRecord，按真实diff由主代理裁决受影响门槛并完成必要复验；旧hash通过记录不自动适用于新hash。T072消费和T077开放计量前必须核对当前核心path/hash、平台、协议/边界及能力记录，缺失/不匹配则拒绝新计量；本步骤不要求回退依赖已经完成的T137，也不由只读查询触发。

## T133局部实验profile与实现状态

2026-10-23：002隔离补丁已提供1秒累计checkpoint及正常epoch_closed；[完整形状解码、严格接收和只读恢复](../../../docs/validation/source-lifecycle.md)已有定向/真实部分证据，T133/G02仍未通过。原T126段落描述旧001 hash，旧结果不回填。

epoch_closed的额外字段为`reason=core_shutdown`、canonical uint64字符串`unfinished_flows`及`finality=complete|incomplete`；0对应complete，非0对应incomplete。此处complete只表示当前epoch所有流均已终结登记，不是每个维度/字节都已完整：之前保存的维度gap必须继续显示。活动relay未静止时，来源保存`source_shutdown_before_final` gap、双向字节null及incomplete marker；不生成精确closed，marker后不能再写流事件。

当前gap额外字段为reason、非空去重dimensions、upload_bytes/download_bytes=null、connection_id字符串（未知为空串）。dimensions只接受bytes、route_or_setup、window_checkpoints；已知closed与其后的非字节维度gap可以并存，不将该gap改成0，也不抹去已知终值。当前核心DIRECT的provider_chain会保留空字符串元素，表示该位置没有提供命名provider证据；原文保留，不猜配，ordered_chain节点名仍须非空。

所有七种kind在Hoopa接收/reader逐种校验字段，重复、缺失、未知字段、非精确整数及误导finality拒绝。receiver检查每epoch来源序列后才Write+Sync返回receipt，重开严格解析全部opaque payload。只读reader恢复旧opened的pending/null尾部并保留最后可信checkpoint，不能改写旧epoch为精确final。随后004已将共同校验接回核心重开/发送，并提供独立派生报告，见[局部实现记录](../../../docs/validation/source-recovery-closure.md)；各新测试的通过范围独立记录，不自动启用完整能力。P0 不再要求核心为重开新增来源恢复事件。

## P0 共同校验与派生恢复报告

[ADR007](../../../docs/adr/007-p0-evidence-closure.md)为当前收敛输入；当前 v1 解析器、001/002/003 补丁的范围和既有结果不变。严格 v1 不静默兼容新字段，旧 v1/旧 proc 记录不重新标为 connect_initiator_v1。

P0 保留原 manifest 与 opened/bytes_checkpoint/closed/gap/epoch_closed 原帧及其 source_seq/event_id，不改旧 epoch 原文。共同纯 wire/epoch parser 已接入 source 重开、发送快照、receiver 和 reader：统一校验版本/完整字段/重复键、精确整数、source/epoch/instance/generation、事件 ID/seq、opened/attempt/终态顺序、累计单调与 marker 一致性。核心补丁使用同源文本副本和摘要；WAL、receipt 与 SQL 仍分属各自提交边界。只读配对检查同时限制manifest+frames总字节，不因每帧各自合法就接受超配额来源。

RecoveryReport 是保留来源事实的 derived projection，不是 event kind，不消耗 source_seq/receipt_seq，不产生新的 ByteFact：

| 事实与证明 | 报告 |
| --- | --- |
| opened 缺 final，没有 source writer 结束证明 | pending，保留最后可信 checkpoint，tail=null |
| opened 缺 final，显式启动/恢复取得受管 source 目录独占 writer 锁，核验稳定快照并完成全扫描，证明旧 source epoch 结束 | gap，reason=epoch_ended_without_final；checkpoint 不丢，tail=null，不生成网络精确结束时刻 |
| 可信 closed，包括原来未投递的完整 closed | 按原 seq/ID/原文重放，精确累计幂等结算一次，不额外制造 missing-final gap |
| 非字节维度 gap 与精确 closed 并存 | 保留已知字节与历史维度缺口 |
| 损坏、缺 manifest、快照不稳定或不能核验 | integrity_error/unavailable，保留原字节并拒绝新完整计量，不修尾或换空日志 |

报告包含原 source/epoch/flow/opened ID、最后可信 checkpoint、扫描截止点、writer 结束证明或缺失原因、tail=null、scan_status、历史质量。锁只证明该 source 目录 writer 互斥，不证明核心退出或网络终止；只读 reader 无结束证明时保持 pending，不顺手封存/修复/写报告。

若持久保存结论，可选 reconciliation_id 由固定命名空间 + 原 source/epoch/flow/opened ID + 缺 final 事实 kind 确定，不含本次 boot/启动 nonce、时间或 cutoff。同 ID 同事实幂等，同 ID 内容冲突拒绝。P0 可由保留原日志反复生成报告，无须新来源恢复事件、v2 恢复引用图或逐旧流新事件预留。生产持久 Gap/Reconciliation 在 T073 同事务去重，生产传输与 GC 在 T138；本段不新增具体字节公式或跨端前缀引用要求。 生产引入GC前，T138须确保接收端已有可恢复的manifest、原帧，以及（恢复判断必需的来源结束证据或等价已持久稳定恢复事实）；不能仅凭字节帧ACK删除唯一推导材料。转运格式另在T138冻结，P0不提前实现。

pending 不发布不可恢复缺口事实。持久事实仅在旧 epoch 已结束且稳定全扫描后生成；同 ID 冲突比较只覆盖不可变事实字段（旧来源引用、最后可信 checkpoint、边界及未知尾部），不比较每次报告的扫描/验证时刻等观测元数据。重复报告可携带新的观测元数据，不能因此新建事实或报事实冲突。

完整 Write 后 Sync 不确定时，本实例不推进确认；重开全校验并 Sync 成功后按原 ID 消歧，新建目录项须持久化目录。sender 只发送已确认截止点的原帧；receiver Write+Sync 后 ACK，SQL 消费提交另行确认。启动扫描发生在新 flow 开放前；运行保持 flow→source 锁顺序，不持 source 锁等 flow 锁或网络 ACK。

历史 gap/history_quality、scan_status 与当前 recording_status 分开；永久未知历史尾部不阻止已验证的新记录。新 epoch manifest/源文件、终末/marker 预留和 opened 持久能力未确认、完整性错误或容量不足才拒绝新完整计量。P0 不回收原日志，固定容量/epoch 上限与扫描成本须实际验收，不承诺无限运行。

## 身份 planned profile 与质量/线路设计历史

下列为T132身份设计及T134早期逻辑字段。身份部分仍为planned；T134质量/线路已按上方独立v2契约实现并在固定新候选验证，不能将下方概念字段名当作实际wire形状。v1没有cancelled结果且仍保持原严格解码；v2不承担P0恢复报告，不新增来源恢复事件或跨epoch引用图。每个hash/profile独立取得能力证据。

### Connector 身份

- attribution_basis=connect_initiator_v1，面向用户输出 basis=connect_initiator_v1；表示连接到已验证原入口的客户端 TCP connect 发起实例，不表示后续逐字节 writer 或当前 FD owner。
- instance_ref 包含 host/boot、PID/TGID 坐标系、OS start identity 及 PID/net namespace；name_snapshot/path_snapshot 及各自来源、状态/reason 保存 connect 时观察，comm 名不能冒称完整路径。
- 多线程建连的实例使用进程leader TGID及leader启动身份，调用TID单列；不能把TGID配调用线程start。纳秒start与proc ticks单位明确分开。进程名若定义为leader comm，不能用调用线程comm冒充；读取一致性、改名/exec及namespace坐标须有证据，否则该字段unknown。active PID namespace不由pid_ns_for_children替代。具体实施边界见[ADR008](../../../docs/adr/008-remaining-evidence-execution.md)。
- client_socket_ref、原 accepted_socket_ref、connect_observed_at、backend_generation 和关联证据绑定；两端 socket cookie 不假定相等，路由前须证明唯一关联。实例、名称、路径分别声明能力；某所需能力未支持在配置加载拒绝，运行时所需字段 unknown 按三值拒绝。
- 子进程自己 connect 归子；父 connect 后子写归有真实建连证据的父 connector。FD 转交、fork 共享、后续 exec/改名不改变该连接及其建连快照。未监测转交时 fd_transfer_observation=unverified，始终展示口径、观测时点和限制，不声称已识别全部转交。
- connector 缺失、事件丢失/捕获连续性不明、来源 generation/namespace 错配或原入口关联多解才为相应字段 unknown；共享本身不把已证 connector 改 unknown。不以 profile、launcher、GOST 或默认父身份代填；晚到补证可修订统计，不倒写过去路由。

### Ready 与计时

setup 的 planned 字段包括 definition/version、protocol、ready_kind、source/epoch/flow/attempt、actual_connection_ref|null、同进程 monotonic 起止、duration_ms|null、observed_wait_ms|null、outcome、failure_stage、observed_at、coverage/reason。flow 从 target_known 到协议 ready，包含身份关联等待、DNS、选择与重试；attempt 只计本次尝试，不能混用起点或把 UTC 跨进程相减。按 definition/ready_kind/protocol/目标范围分别统计。

| ready_kind | 已证明时的成功边界 | 未证明限制 |
| --- | --- | --- |
| tcp_connected | DIRECT 目标 TCP 建立 | 不是 TLS/业务成功 |
| http_connect_accepted | 上游 HTTP CONNECT 成功回复完整解析 | 通用协议 2xx；固定候选只接受且拟校准 200，不扩大实现声明 |
| socks5_connect_accepted | VER/RSV/REP 和完整地址均校验成功 | 仅地址可解析不能证明成功；003 仅有局部合成测试，无新运行 binary/真实校准 |
| ready_unproven | 未校准或无法观察的协议 | reason=ready_unknown、duration_ms=null，结果另按证据分类，不进入成功耗时分布 |

outcome 区分 success/failure/timeout/cancelled/unknown；失败/超时/取消保存 observed_wait_ms、结果与阶段，不能伪装为成功耗时；ready 未证明不覆盖已证失败/超时/取消，只有结果也不可判定时 outcome=unknown。lazy 握手无明确确认仍为 ready_kind=ready_unproven、reason=ready_unknown，不凭 Dial nil/NeedHandshake=false 判 ready。HTTPS/复用内业务 TTFB 保持 N/A。
HTTP CONNECT 成功回复头与首段隧道 payload 同包必须另测预读保留、不丢字节和不重计；目前为待验边界，不是已证新运行 bug。

实现按[ADR008](../../../docs/adr/008-remaining-evidence-execution.md)在实际Conn附着协议完成证明与单调完成点，后续tracker只绑定该引用。未知外层协议必须遮断内层TCP证明，不可沿unwrap找到DIRECT就判整个代理ready。flow/attempt完成点与finish落盘时刻分开；本段规定拟实现方法，尚无新profile运行通过结论。HTTP预读本身已有005反例修复证据，但不代表ready证明已接线。

### 实际 Conn、线路与连接段字节

在 actual Conn 产生处绑定 source/epoch/flow/attempt/config_generation、tracker_ref、rule/logical target、稳定 node_id/provider_id 与 ordered_chain、观测时间/来源/完整性。Dial 未取得 Conn 的失败只能记录 selection_only/unavailable，不以选择意图推导实际链；改名/切组不覆盖原连接历史。

Hoopa生成bundle将持久node/provider/group引用用作core内部名称，展示别名另存；实例随机adapter UUID不是跨重开稳定ID。加载时须核验引用/类型/提供者映射，Conn冻结实际链；无已验证映射的手工配置只能提供原链，不能宣称稳定节点能力。T134已验证inline节点和group稳定映射，实际provider为空/null；命名订阅provider仍待验收。T135在受限loopback profile内验证端口开放前的配置能力、trace generation/映射/来源可写准入，详见[受保护入口验收](../../../docs/validation/protected-entry.md)；该结果不证明通用配置、身份后端或正式门槛。

连接段记录 segment_id、actual_connection_ref、flow/attempt、route_evidence_ref、boundary、direction、actual_n、sample/interval 和质量。预读、partial Write、relay 使用所属段实际 n，不用请求长度替代；同方向、边界和截止点下 `flow总量 = 可证连接段总量 + unallocated`，这些视图不重复累计。若只能恢复 flow 累计而无法可信拆分到连接段，节点维度保留缺口，不能全归给最后 attempt。

能力记录按实例/名称/路径、后端generation/平台范围、protocol/ready_kind/definition、actual route/segment bytes分项绑定候选hash/profile及run_id。额外记录helper RSS/CPU、kernel map内存/容量、每连接事件量、入口关联等待与重开扫描成本；不把内核/helper成本隐藏在管理器RSS外。T134局部质量证据见上方v2记录；T132身份仍缺真实固定候选验收，原严格0/100不变。T133的v1本地最小闭环见[来源传输验收](../../../docs/validation/source-transport.md)，不能代替正式G02、身份或生产CapabilityRecord。
