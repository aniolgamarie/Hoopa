# Implementation Plan: Hoopa 整体代理管理产品（P0–P3）

**Branch**: Git 实际分支 `main`；Spec Kit 特性标识 `001-hoopa-proxy-manager`，本次不创建/切换分支

**Date**: 2026-10-22 | **Spec**: [spec.md](spec.md) | **Constitution**: [v1.0.0](../../.specify/memory/constitution.md)

**Input**: `specs/001-hoopa-proxy-manager/spec.md`，含并发冲突、自动保留策略及原始 TCP 连接发起者归属口径澄清。

本次输出限于规划阶段 Phase 0 研究和 Phase 1 设计；产品阶段仍称 P0/P1/P2/P3，二者不是同一编号。
以上描述规划阶段的产物范围。规划时尚无 Go 实现；现已按 [tasks.md](tasks.md) 进入 P0，具备最小 module 与原型，执行状态以[任务记录](../../docs/task-log.md)为准，正式平台验收尚未完成。

2026-10-27当前进展：140项完成47项。T132受限原型、T137历史冻结、T136控制harness、T140程序规则以及T127/T012身份、T128/T013恢复、T129/T014质量、T015生命周期/最小保护均已完成各自有限验收。T016当前多入口候选的16组/39VM/127应用尝试/124来源流/59epoch及最终只读race入口已通过；T017同源事件裁决与T018核心可行性裁决完成，允许DB-free P1实现。旧0017的100/100、8VM/16槽、6VM/21槽保留旧hash；当前增量按未变算法来源、定向测试与实际受影响路径证据评审，旧失败均保留。[ADR002](../../docs/adr/002-p0-core-gates.md)明确工程入口不等于P0整体/P1产品交付，G06驱动事务与规模/资源仍由T019–T021完成。T019共同事务探针已有双驱动实际 ENOSPC 与 fsync/fdatasync 注入证据，选型仍未关闭。T022/T023产品验收定义已完成，实际适配器未实现；已按[文件存储契约](contracts/file-store-v1.md)完成T024 Linux有限验收，T025 运行归属模块、T026 基础核心组合、T027 持久操作日志、T028 Plan 信封与 T029 串行接纳均已有限验收，T030 完整替换已完成有限模块验收，T031 自动回滚、独立显式恢复及[实际管理器死亡补验](../../docs/validation/p1-manager-crash-recovery.md)已完成有限模块验收，T032 已按[运行生命周期契约](contracts/runtime-lifecycle-v1.md)完成[有限模块验收](../../docs/validation/p1-runtime-lifecycle.md)，T033 已完成[实际服务/CLI有限验收](../../docs/validation/p1-service-lifecycle.md)；T034 已完成[运输与恢复前置核验准备](../../docs/validation/p1-integration-preparation.md)及[固定双入口实际连续性](../../docs/validation/p1-finite-dual-entry.md)，并已推进[进程外故障见证及五个真实场景](../../docs/validation/p1-traced-faults.md)；完整产品矩阵仍待执行，实际状态以任务记录为准。

T025 已按[运行记录 v1](contracts/runtime-records-v1.md)完成[运行归属模块有限验收](../../docs/validation/p1-runtime-ownership.md)，测试进程的真实出生/exec/接管不等于 Mihomo 产品矩阵。按[ADR013](../../docs/adr/013-p1-runtime-ownership.md)，T026 必须为当前缺失的 closed-start/activate 能力制作新固定候选并验收受影响路径；不把凭据尚未交付当作入口关闭，也不把旧核心的门槛结果赋给新 hash。

T129按[ADR011](../../docs/adr/011-source5-quality-acceptance.md)区分固定核心真实网络、source5受控事件语义及适配器边界。[正式质量契约](contracts/source5-formal-quality-v1.md)所定六VM/21槽已重新登记并执行，HTTP2、SOCKS2、DIRECT慢DNS2、完整替换4、失败/超时7及nested/retry4全部通过；241导出/194来源及接收原件独立复核，最终公共入口race1通过0失败0跳过。原六类准备保持formal0，不转贴旧结果。替换使用独立进程/端口，取消/unknown注入不证明客户端断开取消dial或真实lazy协议；FR/SC的产品界面、分位数/窗口统计以及T017/T018前置不变。

## Summary

单仓库、单 Go module、一个 `hoopa` 程序提供 CLI、本地管理服务与按需 TUI；Mihomo 独立承担代理与已发布路由，
完整计量改为同一 Mihomo 数据面的事件来源候选，GOST 保留可选入口/对照；SQLite 保存历史事实。基础管理、应用选路和不计量接入不依赖 GOST、数据库或 TUI。
管理器与数据路径分开，关闭界面不停止核心。

先以最小真实应用组合证明计数、源身份、短连接、计时、实际链、规则和恢复边界，再交付完整管理器。
stock GOST/Mihomo 接口尚不足以证明完整进程账本；按 [ADR 005](../../docs/adr/005-telemetry-replan.md)验证同源事件、接收journal及必要上游观测能力，并通过 [research.md](research.md) 的 G01–G07 实验；
不能将一个尚未实现的 adapter 当作能力保证。P1 交付应用路由，P2 交付真实进程规则、历史与质量视图。

## 2026-10-23 全计划复核与当时执行入口

本节保留当时的失败与前置条件；当前状态以上方2026-10-26进展及tasks勾选为准。

当前固定候选已构建，但G01正式100流身份断言为0/100、全部owner_visibility；终末字节/域名100/100不关闭身份门槛。T126完成仅说明取得可运行实验候选，T127及T018仍未完成。原始失败及摘要保持不变，详见[真实结果](../../docs/validation/native-candidate.md)。

实施按T130环境诊断、T131具体身份方案裁决、T132最小身份验证推进。T130/T131现已完成负向取证/裁决：真实回环运行环境存在同UID FD不可读，沙箱内自身扫描不能移用于该环境；具体结果见[前提诊断](../../docs/validation/p0-prerequisites.md)与[ADR006](../../docs/adr/006-source-identity-proof.md)。该宿主上下文的T132前提仍不满足；2026-10-24另在独立VM取得两流实验正例，详见末节真实校准记录，完整身份证明仍未完成。诊断必须核对与候选的UID、namespace、父子关系及权限，观察者可执行文件不同的安全上下文局限另记，不能仅检查自身/helper就断言完整owner集合。核心归属身份、launch上下文与业务socket源身份不能相互代填。诊断是显式实验，不由status/doctor/预览触发，不更改权限、系统或用户核心。

T130可在得到明确阻断结论后完成取证；T132只有最小正负真实样本满足本次用户确认的连接发起者口径才能完成。T131已保留proc方案的负向裁决；后续按ADR007证明原始connect事实、原accepted socket关联、建连快照、可见范围及无法证明时的拒绝，不能只写“换一个环境”或“增加adapter”。若当前环境不满足，记录缺少的权限/拓扑条件及下一次允许重试的可观察变化；不自动提权或无限重试。原100流失败不能改为skip或重新分类。

T133–T135已分别完成来源生命周期、协议ready/实际线路、配置加载期能力拒绝与入口保护的本地验收；当前139项任务完成21项。这些是不依赖正向身份结果的独立P0子实验，不能代替G01或启动P1。只有T132最小身份证明、各能力的最小正负验证和T137新候选冻结均通过，才重新预登记并执行T127–T129正式矩阵。T136提供G04/G05所需最小控制/托管/分阶段应用harness；不等待生产管理器实现才验证其恢复前提。

T133现已完成明文本地最小闭环：独立checkpoint/正常marker、同源v1校验四处接线、持writer锁稳定扫描后的派生恢复、confirmed prefix发送、真实CONNECT/SOCKS5/崩溃重开及collector离线/ACK停滞/receiver重开；partial-write/lazy首次Write actual n已有定向注入。只读配对恢复限制总字节预算，精确上限接受、超限拒绝。首次真实组合失败保留，新的预检和故障run各自记录，详见[本地验收](../../docs/validation/source-transport.md)。正式G02的T128仍未完成；T134另有下段独立局部验收，不把本地子任务提升为T137或生产能力。

后续收敛按[ADR007](../../docs/adr/007-p0-evidence-closure.md)：用户已选定 `connect_initiator_v1`，源进程为向原入口建连的实例及其建连名称/路径快照；父connect后子写仍归父connector，FD共享/转交不改写历史。T132先捕获connect事实，再给出可审查的客户端socket→原accepted socket唯一关联算法并实测，缺算法/正例就停止；实例、名称、路径能力分开验证。内核后端已有独立VM两流实验正例，完整身份能力仍未验收，不默认加载或赋权。旧0/100不按新语义重评。

T133先将现有v1同源纯wire/epoch校验接入来源重开/发送/接收/reader；保留原帧/原序重放，恢复报告是派生投影：缺final且无writer结束证据为pending，显式恢复持writer锁并核验稳定来源后可标epoch_ended_without_final gap，尾部null。完整closed幂等恢复；损坏或缺manifest拒绝新完整计量。撤回强制v2 recovery_gap、跨epoch引用图和为全部历史恢复事件预留空间的P0要求；历史gap与当前记录ready分开，生产持久对账/回收仍归T073/T138。

T134已冻结并实现[质量/线路v2契约](contracts/telemetry-quality-v2.md)及正反fixtures，保留v1原文/解码，不新增恢复事件。DIRECT/HTTP CONNECT/SOCKS5 ready证明、实际Conn引用、原段actual n及稳定node/group映射已完成局部验收：最终新候选真实五流和三种失败重试通过，慢DNS、超时/取消及未知wrapper另有定向证据，见[完整范围](../../docs/validation/quality-ready.md)。未知协议ready仍为null；命名订阅provider、完整核心DNS E2E及正式T129未由本轮证明。T133/T134独立于T132完成局部实现，修改同一核心保持单写入者；G01–G03正式门槛仍未关闭。

T135已实现[受保护实验契约](contracts/protected-core-v1.md)：严格有界JSON预校验、来源可写/容量准入、全部端点先bind后listen、独立入口认证及每请求HTTP重验；控制面仅认证status/drain，原序规则经受控A/B两线路验证。generation/hash固定，SIGHUP不热重载，drain保留旧TCP，完整replace用独立新进程；进程规则仍在加载期全部拒绝。最终001–007候选`hoopa-t135-guard-final`已干净应用、整树一致并真实复验，见[验收报告](../../docs/validation/protected-entry.md)、[manifest](../../experiments/mihomo-events/capability-candidate-manifest.json)与机器验证（本地原件 `../../experiments/mihomo-events/capability-validation.json`，不随Git上传）。本地T135完成不关闭T132/T127/T128/T129/T136/T137/T018，P0/P1未放行。

[ADR008](../../docs/adr/008-remaining-evidence-execution.md)进一步确定T132使用进程leader启动身份、线程字段单列并要求受控loader/cgroup部署；T134在实际Conn附着协议完成证明/单调ready点，未知外层遮断内层证明，稳定内部node/provider/group引用与展示别名分离。T132已有受控VM有限在线准入、真实正反例及有限Mihomo核心接线实测，完整身份与生产支持仍未认证；T134已按上述范围实现，不能以普通proc holder或随机adapter UUID代替未证能力。

T132历史connect准备阶段仅完成[connect捕获准备契约](contracts/connector-capture-spike-v1.md)、有限观察源码和离线准备器：真实BTF编译与12项准备器测试通过，详见[准备验收](../../docs/validation/connector-preparation.md)。当时用户确认暂时没有隔离运行环境，该准备阶段未建立loader或执行load/attach。初次源码只接受初始PID namespace、一个精确cgroup和netns，comm始终unverified；A的connect→active、B的原accepted cookie关联及正式矩阵仍未交付，不减少T132验收条款或改变139/21计数。

随后继续准备新增[lifecycle观测目标](contracts/connector-lifecycle-spike-v1.md)：同对象的active回调查原connect cookie，passive/TCP_CLOSE只保存观察；两C文件提前快照、16项准备器测试及真实BTF编译通过。没有tuple索引、namespace/connector推断或清理。v5.10的sockops/netns接口缺口及非原子cookie核验删除已在ADR008修正；唯一关联实验优先用固定v6.6核验接口，未声明产品最低内核版本。loader/关联/内核矩阵仍待完成，T132及139/21不变；见[新准备记录](../../docs/validation/connector-lifecycle-preparation.md)。

本轮又补齐加载前的[对象检查与单条ABI解码](../../docs/validation/connector-object-inspection.md)：现有libbpf运行库实际open/read/close两个对象，严格核对声明；不是完整loader。单条解码不访问内核、不做跨记录关联，uint64以字符串保真，所有运行能力仍unverified。8项假库与10项纯bytes测试、两个实际对象及畸形对象拒绝分层记录，未改变T132/T137及阶段依赖。

历史loader准备阶段增加[有限loader/会话控制](../../docs/validation/connector-loader-session.md)：共享map/info、配置freeze、直接BPF_LINK_CREATE与失败清理、显式实验命令经47项假接口测试，受影响接口18项回归通过；该准备阶段未执行真实load/attach。停止方案修正为：关闭link不证明RCU在途回调结束，MAP_FREEZE也不能阻止BPF写；当前导出仅diagnostic_snapshot，quiescence/completeness始终unverified。T132下一步仍须先证停止/范围与稳定cutoff，再证原accepted唯一关联，不能由准备代码关闭T132或启动T137。详细语义见[契约第8节](contracts/connector-lifecycle-spike-v1.md#8-有限-loader-准备与停止边界)。

后续已将停止问题实现为独立[`gated`候选](contracts/connector-stop-spike-v1.md)：同锁admission/active/closed协议覆盖全部观察body，非attach控制程序关闭准入，LOCK读取排空后再扫描。错误停止方法有模型反例，101项定向测试及三个profile实际离线编译/解析通过，见[新验收](../../docs/validation/connector-stop-preparation.md)。该准备阶段仍未执行内核实测；停止后的写稳定与RCU退出/事件完备/身份分别验收，原lifecycle默认保留。下一步需隔离环境的小规模gate交错、范围/连续性证据，再推进accepted关联；T132及139/21不变。

accepted 关联进一步冻结为[有限条件模型](contracts/connector-association-model-v1.md)：C 不预知客户端临时端口，A 按自身 socket 发布只插入的 tuple 索引，P 按自身 cookie 绑定，Q 仅查询原 FD witness；不发布在线身份。14 项定向测试通过，并复现忽略 fault、漏采 active、取错另一真实 FD 三类错配，后两者可以零 fault。不能把覆盖与原 FD 来源作为未验声明传给 resolver；必须独立证明。普通 accept queue 发布早于状态处理，FD 交付另有 child 锁，模型不把两者混同。按契约 A–E 先验范围/连续性，再版本化带 netns 的观察与原 FD 见证，最后审查在线一致边界及核心接线；[本轮验收](../../docs/validation/connector-association-model.md)不改变 T132/T137、139/21 或正式样本分母。

每次运行绑定不可变环境/方案/源码/补丁/二进制/配置/真值摘要；任何相关输入改变，受影响证据重新验证，并保留旧run。仅当与失败直接相关的环境、方案或实现输入发生可观察变化，且记录明确因果预期时，才重试该失败路径；无关改动、文档修改或仅重编相同源码不构成重试理由，正式样本分母不变。其他独立P0工作仅在自身依赖满足时继续；T018之前仍不交付P1。

原accepted见证已有[独立准备](contracts/accepted-socket-witness-v1.md)：只在自身AcceptTCP入口、同次Control内读取两个cookie/端点并私有绑定，失败严格关闭新连接。fake race 4顶层/38含子测试、vet/gofmt及两个平台stub编译通过；本机普通Go Close窗口有SO_COOKIE运行正例，但SO_NETNS_COOKIE为ENOPROTOOPT，完整见证skip/unverified。见[验收](../../docs/validation/accepted-socket-witness.md)。不因此改变内核ABI/核心patch或放行T132；原FD对passive与独立oracle、范围/连续性、在线/半关闭兼容仍按关联契约验收。

## Technical Context

**Language/Version**: Go 1.27.3 为固定构建候选。本地 module 可先为 `hoopa`；正式地址确定后改为真实 module path，不虚构仓库组织名。

**Primary Dependencies**: Cobra v1.10.2；P2 才加入 `charm.land/bubbletea/v2 v2.0.8` 和 `charm.land/bubbles/v2 v2.2.1`。
Mihomo、GOST v3 由外部安装职责提供，P0 manifest 固定实际版本/tag/commit/摘要，不使用运行时自动 latest。
JSON Schema 2020-12 定义契约，计划摘要遵循 RFC 8785；YAML 为受限意图/节点输入，不提供脚本扩展。

**Storage**: 私有文件中的意图、不可变 provider/bundle、active pointer、操作日志和进程登记为 P1 权威，无需 SQLite。
P2 使用可选 SQLite；P0 对照 modernc.org/sqlite 与 mattn/go-sqlite3 后通过 ADR 固定一个驱动，不预建生产双驱动框架。
本地 WAL、短事务、checkpoint/增量同提交；只读查询不修复、迁移或创建 sidecar。

**Testing**: Go testing、合成配置/JSON fixtures、契约正反例、固定核心适配集成、故障注入、Linux 真应用和性能测量。
实现后执行 gofmt、go vet、go test ./... 和适用 race 检查；macOS/Windows 标准托管 CI 做适用验证，真实生命周期另验。
规划阶段仅做文档/契约静态验证；实施阶段的 Go 与原型运行结果单独保存于[任务记录](../../docs/task-log.md)，不由规划文档推定通过。

**Target Platform**: Linux 优先实际验收；macOS/Windows 为待验证目标，路径、锁、进程身份、信号和 IPC 分别适配。
最低 OS 版本、退出/休眠能力及正式支持须由 P3 证据表确定。

**Project Type**: 单用户本地 CLI-first 代理管理器，无 Web/远程控制平台。

**Performance Goals**: 管理器空闲 RSS ≤40 MiB、10 分钟平均 CPU <单核 0.5%、整套空闲内存 ≤200 MiB；
Hoopa 压缩包 ≤10 MiB、程序 ≤25 MiB，外部核心分列。均为待测目标。
报告声明平台、构建、规则/节点/历史基数、0/1/5 个计量入口及采集状态，不以空配置代表实用负载。

**Constraints**: 不自研代理协议内核；不默认 TUN、系统代理、自启动、系统任务、特权采集或业务解密。
基础管理与账本解耦，查询无隐藏写入/主动联网，固定 pin 不隐式后备，逐连接执行不依赖管理器在线。
跨资源变更可恢复但不是原子事务；关键经验前提失败须停止相应交付并具体调整。

**Scale/Scope**: 一个本地用户、单实例写入者、多订阅、多 profile/launch；覆盖 11 用户故事、56 FR、18 SC。
下载具备正文/节点/解析深度/并发限制，见研究 R03。历史以 10k/100k/1m 连接梯度测量后确定 P2 支持规模与查询预算，非已支持承诺。
首期计量限已验收 TCP，不承诺全部 UDP/QUIC、整机流量、模型 token 或供应商账单。

## Constitution Check

规划前与设计后均审查以下门槛，无需宪章例外。PASS 表示设计遵循；所有运行门槛另记，不能由文档审查代替。

| 原则 | 规划前 | 设计后 | 设计证据/运行门槛 |
|------|--------|--------|------------------|
| I CLI 与独立转发 | PASS | PASS | 共享用例、核心独立进程；G04/F01–F04 实测后才承诺存活 |
| II 本地确定性规则 | PASS | PASS | 本地 provider、统一顺序、三值判断、固定来源；G05/F29–F33 |
| III 恢复与身份 | PASS | PASS | 日志/完整候选/实际核验/旧计划冲突；F10–F14、C01 |
| IV 局部接入/版本集成 | PASS | PASS | 子进程环境、参数数组、JSON v1、管理/应用输出分离；F19 |
| V 同源完整计量 | PASS | PASS | 唯一 ByteFact、独立 epoch、checkpoint 同提交；G01/G02/F20–F25 |
| VI 质量/只读 | PASS | PASS | 测量定义/历史线路/固定窗口、readonly 路径；G03/F17/F26–F28 |
| VII 原型优先 | PASS | PASS | P0 先行、门槛有证据；P1 不等于项目完成 |
| VIII 权限/复现 | PASS | PASS | 私有 IPC/独立 metrics 保护、秘密引用、固定版本/许可、零额外收费 CI；G07 |
| IX 简洁/协作 | PASS | PASS | 按需建包、小接口、独占写入、稳定内容最终审查；执行记录见文末 |

规划时 G01–G07 尚未执行；现已有部分原型运行证据，完整门槛仍未关闭，后续失败仍按宪章阻断/调整，不因规划或代码编译完成自动放行。

## Project Structure

### Documentation (this feature)

```text
specs/001-hoopa-proxy-manager/
├── spec.md
├── checklists/requirements.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
└── contracts/
    ├── cli.md
    ├── intent-v1.schema.json
    ├── plan-v1.schema.json  # 历史读取／重放
    ├── plan-v2.schema.json  # 新规划完整影响
    ├── api-v1.schema.json
    └── usage-v1.schema.json
```

tasks.md 已由 `$speckit-tasks` 生成并用于当前实施，任务完成状态以其标记和对应证据为准。规划阶段未改规格与宪章，实施也不自动放宽约束。
运行证据留私有目录，只有经核对的合成/脱敏材料进入 Git。

### Source Code (repository root)

以下为随阶段建立的目标路径；当前仅按 P0 创建必要源码，不预建下游空包。

```text
cmd/hoopa/main.go               # 依赖组装、退出码、版本
internal/
  cli/                         # Cobra 与管理 JSON 编解码
  control/                     # 用例、写入协调、计划/操作与恢复
  config/                      # 严格意图、原子文件、不可变 bundle
  subscription/                # 有界抓取、节点身份、provider 快照
  policy/                      # 有序谓词/组跟随图、纯校验与编译
  runtime/                     # 所有权、核心生命周期、入口与租约
  mihomo/                      # 外部核心配置/能力/controller 适配
  launch/                      # 环境、参数数组、信号与退出状态
  schedule/                    # 有界到期工作、时钟、恢复补跑
  metering/                    # P2；已验证事件源/epoch/差量/质量，GOST仅可选对照
  ledger/                      # P2；唯一 SQLite 适配、事务与历史投影
  tui/                         # P2；共享用例的按需 Model/Update/View
  platform/                    # OS 路径、锁、身份和 IPC 必要封装
api/v1/                        # 实现期契约、兼容 fixtures
protocol/telemetrywire/         # T133已提炼的无IO纯事件/epoch/恢复校验，核心携带同源文本及摘要
experiments/gost-mihomo/        # P0 最小 harness/配置/证据格式
experiments/sourceidentity/     # P0 原socket身份及运行拓扑前提
experiments/mihomo-events/      # P0 隔离补丁、能力预检、正式同源矩阵
experiments/telemetry/          # P0 接收journal，不能代替来源完整性
testdata/                      # 合成订阅、指标/事件、黄金文件
tests/integration/             # 固定核心、故障与平台回环
docs/                          # 既有设计及后续 ADR/验收记录
.github/workflows/             # 后续标准托管适用验证
```

**Structure Decision**: 单仓库、单 module、单 hoopa 产物，CLI/服务/TUI 为不同运行入口。
依赖方向为 cli/tui → control → policy/subscription/runtime/launch；policy 不导入 UI、GOST 或 SQL。
ledger/metering 只在需要时装配，没有数据库也能运行基础管理。接口放在消费方，仅为实际使用的时钟、核心、来源、证据和账本定义小接口。
不建立通用事件总线、多内核层、插件框架或 utils 杂物包。P0 先在实验目录形成证据，再提炼真实边界。
实现期将契约引入 api/v1 并以兼容 fixtures 防漂移；外部调用方不读写内部运行文件/表。
telemetrywire不导入internal/Mihomo/SQL；核心补丁的文本副本须与该包及同一组fixtures核对摘要，不通过链接两个进程或手工维护两套校验器共享实现。来源WAL、接收journal与SQL仍各自拥有写入/确认边界。该包现已实现并用于四处接线；001–005候选含六份生产文本，006增至八份且与仓库逐字节一致；T133/T134验收各自记录。
Git 保存源码、文档、合成 fixtures 和构建定义；程序、发行包、真实配置/账本和凭据加入实现期忽略规则。

## Complexity Tracking

无宪章违规或申请例外。必要复杂度是进程/历史身份与可恢复变更；采用私有文件日志和一个 SQLite 写入者，不引入分布式事务或消息中间件。
GOST 原生事件不足时先做具体探针/ADR，不能预建未定义的数据面框架。

## Runtime and State Design

不计量应用 → 经固定能力与启动门控验证的 Mihomo 受保护入口；完整计量候选为应用 → 已验证事件能力的 Mihomo 命名入站 → 本地规则/实际节点。
同一数据面输出身份、计数、计时及实际链，journal保存已送达事件，SQLite另行消费。GOST入口链仅保留兼容/累计对照。
同profile共享入站只证明应用上下文；launch须经可验证入站/认证或进程登记关联，无法区分时保留launch_unknown。新增入口的应用/排空行为仍须P0验证，不因每次launch隐式重载核心。业务转发不经过管理服务。

控制服务由显式 start/service run 启动；首次可用 start --control-only 配置而不启动核心。
init 只做本地设置，subscription update 明确下载，config plan 只读本地材料，apply 按确认计划执行已披露动作。
普通 start 没有可用已提交快照则失败。stop 中断本实例；service stop 只退出管理器，不暗杀代理。
核心 desired_runtime 与实际状态分开持久记录；明确 stop 的恢复不能被误当成崩溃后自动重启，control-only 也不启动核心。

managed 使用 committed profile/路由，清理冲突变量并采用显式 bypass，默认 bypass 为空。
inherit/direct 可读本地 profile 而不依赖核心，父 shell/系统/应用持久配置不变。
run 与 JSON 管理协议分离，API 不提供执行任意子进程操作。
T054/T056 的应用生命周期记录按[独立 launch 空间写权](contracts/application-launch-store-v1.md)实施：配置/核心状态仍由唯一 Store writer 管理，wrapper 仅追加自己新建的 launch 目录，不能借此修改配置或重放历史执行。显式 init 安装该空间，query/run 不重建；该扩展尚待实现和验收。

Linux 管理 IPC 首选私有 Unix socket/用户身份核对；controller 与 metrics 分别保护，数据入口按固定版本能力认证。
T025先实现服务开放所需的最小IPC权限与peer身份保护，T063再扩展完整API授权矩阵；不能让T033已开放的服务等待后续安全任务才受保护。
loopback/profile 不构成同用户隔离。Windows ACL/Job 单独验证，不把 chmod 当作跨平台保护保证。
只读请求不落持久审计、last_seen、迁移或自动恢复；probe/update 与写操作明确分离。
错误与诊断仅由允许字段构造，拒绝请求时不回显原始敏感输入；速率字段明确使用 bytes_per_second，避免与 bit/s 混淆。

变更、锁、进程登记和 SQL 提交的顺序以 [data-model.md](data-model.md) 为准；
机器交互和错误以 [CLI/API 契约](contracts/cli.md) 为准，不在多个适配层重复恢复状态机。

## Delivery Sequence and Gates

### P0 — 组合原型与工程裁决

1. 固定 Linux、核心/真实应用版本和摘要、两个来源与受控目标，建立私有 manifest。
2. 跑 Mihomo 对照和 GOST 链路、独立 metrics 保护、已知上传/下载、失败重试及长连接；真 Agent 与 curl 校准分别验收。
3. G01–G03：T122–T126旧证据保留；先按T130–T132证明身份前提和最小真实身份，T133–T135已完成来源、ready/线路及配置能力的局部验收；在T132正向身份成立后由T137冻结完整新候选，T136之后由T140补齐独立source5程序规则消费和真实三值小样本并冻结新候选，再预登记两个同名进程/子进程/域名真值及100短连接并执行T127–T129。正式矩阵不得替代能力开发或前提诊断；journal接收恢复与源端未送达分开，不假装stock核心已有完整事件。
4. G04/G05：先由T136构建最小控制/恢复harness，之后T015/T016实测管理器死亡、身份接管、分阶段开放、规则顺序、未知拒绝、旧候选冲突，以及持久边界失败/回滚。预先登记真实SSH/注销/休眠所需环境；缺少环境保留阻断，不对当前工作主机自动执行破坏会话的操作。
5. G06/G07：两驱动同故障/只读测试、0/1/5 入口、规模梯度与权限矩阵；验证自动清理的最小关键语义。
6. 形成 ADR：托管/身份、规则编译、事件来源、入口模型、驱动/只读路径、支持规模/资源基线。

退出须有关键可行性真证据且无未解决关键阻断。身份/事件/规则失败先调整并重验，不能直接堆 P1 界面。
驱动选择和 P2 性能细化可与不依赖数据库的 P1 工作并行，但不因此关闭未完成门槛或宣称 P2 可用。

### P1 — 独立基础管理

按任务依赖依次交付 US5→US3→US4→US2→US6：US5 先消费 T026 单独实测的完整 basic 固定 bundle 建立核心所有权、生命周期、变更日志与恢复；
US3 再接入严格意图、来源、稳定组、完整快照编译与有界调度；随后 US4 应用有序选路、US2 首次设置与三模式 launch、US6 只读诊断与 JSON v1。
P0 核心身份、路由、恢复与最小访问保护门槛仍是 P1 前置；驱动定稿和规模细化仅可与不依赖数据库的工作并行。
基础P1使用无BPF受保护模式；`testdata/runtime/basic-verified-bundle.json`独立记录完整应用/域名顺序配置。旧`verified-bundle.json`完整身份原件不删规则、不换hash、不改判。进程规则缺能力在P1拒绝，真实进程能力仍属P2。
应用选组/节点本阶段可用，不等有账本才做；手选/自动/跟随、固定来源、未知地区/缺目标均有明确结果。
更新先产生候选，需要中断时等待显式应用或既定策略；核心和规则资产支持已验证的离线启动。
US2的T139在首次旅程T058之前接入最小config plan/apply、status/doctor，复用既有用例；US6的T064/T065扩展完整输出和JSON接线。首次旅程不能依赖排在其后的命令实现。

退出：无 GOST、无 SQLite、无 TUI 完成基础旅程；F01–F19、F29–F33 的本阶段场景与 C01 通过；
机器输出、父环境、只读副作用、未授权调用均可核验。P1 完成仅表示基础管理可用。

### P2 — 可靠账本、真实进程规则与交互

US7 先落地已通过 P0 的来源/身份适配和唯一驱动，再做 checkpoint/事实/归属修订/缺口/对账、固定版本查询与确认后的清理。
T138负责当前完整身份/来源核心同等closed-start、认证activate/drain和完整snapshot契约，以及bootstrap/BPF/FD handoff闭包冻结与受影响实测；T077在产品运行层接通最外层birth、同PID启动、原pidfd/同连接登记后开放，验证退出/接管/部分失败。缺能力或特权环境不能自动降级为basic；T091随后接真实进程规则，不把旧P0 helper或basic通过当生产身份启动链。
P0的8MiB/32epoch不回收日志仅是有界实验。T138须先完成生产来源spool/接收journal的严格事件校验、稳定重放、消费确认、容量与回收协议；接收ACK和SQL提交分别定义，只有可证明不会丢掉最后可恢复副本的记录才可回收。来源段由核心单writer回收，管理器通过版本化持久ACK协调，不直接改写或删除核心活动日志；接收journal由自身writer回收。无法确认时保留并拒绝新计量，不能静默清空或把内存Replay提升为生产来源。
T138若生成不同于P0已验收制品的核心/协议，须在本任务内重新冻结不可变manifest和CapabilityRecord，由主代理根据真实diff及输入变化裁决G01/G02/G03/G05/G07最小保护的受影响范围、执行必要回归并签明结论。旧hash的passed不能直接挂到新hash；未受影响证据的引用须说明依据，新制品仍须通过本次来源/接收正负故障验证。T138完成及T072消费以这些证据就绪为前提，T077在入口ready前再次核对当前path/hash、平台、协议/边界及能力记录；不反向依赖或重勾旧T137，不改变已完成P0的历史裁决。
基础窗口字节与上传/下载 B/s 在 US7 查询实现中完成：同 cutoff/window 先汇总字节再算速率，覆盖/新鲜度和粒度或精度不足原因可见；
其后才接速率排序、CLI/API/导出及独立验收。US8 接真实进程规则，US9 复用 US7 窗口计算联查质量/实际线路，最后 US10 加 TUI。
真实进程规则使用路由前证据，事后补全不倒写决策；采集断开不改路，新计量启动不能记录时拒绝。
进程/域名/交叉视图、同名合并、PID/路径/用户展开、排序分页/Top N、固定窗口和错误分布共用事实及版本。
T087明确将usage.query及历史connections.query接入共享control查询，T088实际对照CLI/API相同快照与错误；T102/T103同样实现和验收质量/线路扩展，Schema本身不代替API分发实现。

退出：F20–F28、F30 的完整历史场景及 C02 通过；预登记可识别样本正确率 100%、短连接终末值可核验，不得事后剔除失败样本。
确认后的策略自动清理，查询只读，已清理区间不填零；CLI/API/TUI 一致，关闭面板不影响转发。

### P3 — 平台证据与迁移

标准托管 CI 做构建/单元/适用回环，按免费额度限制并发、制品保留和预算，不启用额外付费 runner。
实际 macOS/Windows 环境验收真应用、身份、终端退出/注销/休眠、恢复与性能；不能由 CI 或 Linux 结果推导支持。
T111包含macOS IPC/peer身份适配及外部来源核心的各平台能力检查；仅适配Hoopa进程封装不能推导Linux-only计量补丁可在其他平台工作。缺失能力在配置阶段拒绝，支持表按基础管理/计量/进程规则分别列证据，正式声明仅覆盖实际通过的项目。
迁移先预览差异、输入摘要、备份与回退，正式执行前复核旧输入，防止覆盖预览后的新修改。

退出：正式支持逐项有证据，失败迁移可恢复，未验证平台保持实验性。整体完成须满足全部 SC、原 33 类故障及 C01/C02。

## Validation and Traceability

| 用户故事/阶段 | 主要边界 | 功能需求 | 成功标准 |
|---------------|----------|----------|----------|
| US1/P0 | experiments、证据/ADR | FR-001–005 | SC-001、SC-017 |
| US2/P1 | config/launch/runtime | FR-006–010 | SC-003 |
| US3/P1 | subscription/policy/schedule | FR-013–017 | SC-002、SC-006 |
| US4/P1 | policy/mihomo/预览 | FR-018–021 | SC-004 |
| US5/P0/P1 | control/runtime/platform | FR-022–025 | SC-005 |
| US6/P1/P2 | readonly/API/保护/probe/导出 | FR-011–012、FR-026–030、FR-053 | SC-007、SC-018 |
| US7/P2 | metering/ledger/基础窗口与速率/清理 | FR-031–045 | SC-008–010、SC-012（窗口/速率子要求） |
| US8/P2 | 原始身份/规则能力 | FR-049–051 | SC-011 |
| US9/P2 | quality/route/复用窗口联查 | FR-045–048 | SC-012（质量与窗口联查子要求）、SC-013 |
| US10/P2 | tui 与共享用例 | FR-052 | SC-014 |
| US11/P3 | platform/CI/migration | FR-054–056 | SC-015 |
| 跨阶段 | 资源与首次使用 | 性能假设/非功能范围 | SC-016–017 |

原 [F01–F33 故障矩阵](../../docs/prototype.md)之外追加两项澄清场景，不改原编号：

- **C01**：两个调用方同 revision 计划，先应用一个；旧计划冲突且零本次状态写入。同一已接纳写请求重试返回原操作，不再执行。
- **C02**：启用/缩短策略确认前不按新策略删除，确认后自动过期；故障 pending、恢复有界、边界保留、延长不恢复；查询不清理，分页过期明确报错。

验证层次为纯策略/差量 → 契约/黄金文件 → 固定核心适配 → 实际进程/持久化故障 → 真应用/平台/性能。
保存每层输入、版本、结果和未验证项，mock 不替代真实身份/计数/重载证据。操作和预期见 [quickstart.md](quickstart.md)。

## Risks and Decision Boundaries

| 风险 | 处置与禁止绕过 |
|------|----------------|
| stock 缺完整 PID/终末事件/计时/跨跳 ID | G01–G03 阻断；最小扩展探针后作具体 ADR，不默认特权/新内核 |
| 旧proc完整owner集合不可见，新connector已有有限在线正反例 | T130/T131保留负向事实，T132按已确认口径先验证connect与原accepted socket关联；无正例停止，不重评或无变化重跑旧0/100 |
| T133–T135局部已验，身份与正式矩阵未齐 | 按ADR007/008继续T132真实connector证明，T137冻结后再做正式矩阵；配置加载期拒绝不作为正向身份或完整能力证明 |
| 实验spool到达上限后永久拒绝新流 | P0记录容量边界，T138定义并验证生产确认/回收；不可丢掉未恢复来源换取空间 |
| 多 GOST 进程超预算 | 先测 0/1/5，再比较共享 service；不丢身份/epoch/租约 |
| 固定核心不能安全开放严格候选 | 保持关闭、回滚或 degraded，不把混合状态报成功 |
| WAL/SHM 只读条件不足 | READ_UNAVAILABLE，显式恢复，不因查询建库或修复 |
| 历史/查询规模未测 | 梯度后定范围，过载显式缺口/拒绝新计量，不声称无限可用 |
| 自动清理与配置/分页竞争 | 控制协调、短事务和保留世代；旧游标明确过期 |
| 平台退出/注销不同 | 分平台证据，用户态脱离不等于系统托管 |
| 上游版本/许可变化 | 固定来源/摘要/版本/声明，升级单独验证，不复制未核对实现 |

无等待用户回答的剩余产品问题；经验事实仍待运行，研究完成不表示可行性全部通过。

## Planning Execution Record（原规划阶段历史）

- 已完成：宪章/需求核对、资料研究、选型实验设计、数据模型、CLI/JSON 契约、验证指南及稳定内容的定向审查。
- 进行中：无。
- 失败待决策：无；审查发现的查询字段、缺口原因、命名空间和运行意图问题已修正。
- 验证通过：JSON 解析、本地引用与文档链接、编号/阶段一致性、占位符/空白及本轮写入范围检查。
- 环境不足/阶段未验证：本地 Python/Node 均无适用 JSON Schema 校验器，未执行元 Schema 与实例正反例验证；未安装依赖。
  真应用、G01–G07、F01–F33、性能/平台、Go 构建及 SQLite 选型均未执行。
- 分工：主代理沿用会话模型/effort，负责架构、事务/锁/恢复与验收；两位 scout 按 gpt-6-luna / high 研究和只读审查；一位 executor 按 gpt-6.1-sol / medium 独占 contracts/ 与 quickstart.md。无子代理再派生或共享文件并写。

原规划阶段止于设计交付；任务清单现已生成，仍按 P0 门槛排依赖，不能先做完整 CLI/TUI。

## 本次一致性修订记录

- 已完成：按授权修订 P1 顺序与任务一致，US7 基础窗口/速率前置并同步 SC-012 子要求映射；未更改需求、门槛或事务/锁/恢复边界。
- 进行中：无；修订文件稳定后交根代理重新执行只读 `$speckit-analyze`。
- 失败待决策：无。
- 环境未验证：本次只检查文档格式、索引、依赖、链接、空白与并行文件范围；Go、Schema 实例、真实运行及性能未执行。
- 分工：主代理沿用当前会话配置决定修订方案并最终验收；executor 按 gpt-6.1-sol / medium 独占 plan.md/tasks.md 修订，不派生；之后根代理组织只读复核。

上述为此前修订记录。2026-10-23全范围复核由主代理独占文档修订，executor/scout只读核查；本轮不实现核心、不运行Go或代理矩阵，实际失败仍保留。最新问题、修订和检查范围见[复核记录](../../docs/validation/plan-task-review.md)。

## T132 校准准备历史进度（2026-10-24）

已将namespaced内核观察、原accepted FD见证、独立client/serve fixture与离线checker串为[小规模执行契约](contracts/connector-calibration-v1.md)。共享Python回归129项、checker12项、fixture fake race/vet/build及三个受影响profile离线构建/解析通过，详见[验收](../../docs/validation/connector-calibration.md)。候选只消费raw BPF，实际传输token只参与后验核对；缺失/重复/矛盾一律拒绝，诊断一致仍unverified。

该准备阶段的真实隔离环境、scope/连续attach、双cookie、启动实例/名称及在线核心接线尚未验证；后续真实VM结果见下文。按既定依赖先运行两个独立client的小样本，再决定内核关联/核心实现；不能以离线最终拒绝保护已经发生的路由。未改变FR/SC、任务粒度或P0门槛，139项/21完成、T132未勾选与原0/100保持。


## T132 驱动与材料闭合历史进度（2026-10-24）

在既有校准准备链上新增[有限三进程驱动和材料契约](contracts/fixture-run-v1.md)。外部在挂接后将驱动放入既有目标cgroup，再由其按listening顺序启动两个客户端；输入/fixture快照、所有子进程退出与输出、单调观察时间和observer结果绑定到独立检查器。首spawn前重验14秒余量，PID/time namespace和time_for_children一致；scope仍需外部声明，点采样不证明连续隔离。该历史阶段58项驱动定向测试通过，见[验收](../../docs/validation/fixture-run.md)；当时真实执行仅无网络Python子进程，作用域/driver为假接口。该阶段无内核运行、正式矩阵或身份能力放行，T132未勾选，139/21不变。

## T132 隔离 VM 真实两流校准进度（2026-10-24）

已在独立 QEMU 9.1.2/KVM VM 完成小规模真实实验，使用官方 WSL Linux 6.18.54.1、固定 BTF 与 cgroup2，无网卡或 host 共享。成功 run 为 `private/t132-kvm-lab/vm-calibration-64kp6nzk`：真实 load/attach、双 cookie、两个独立客户端 PID 83/84 到原 accepted cookie 的后验一致核对通过；observer/driver 均 exit 0，停止 admitted/completed=35/35、active=0。详见[隔离实验验收](../../docs/validation/isolated-linux-lab.md)与机器记录（本地原件 `../../experiments/sourceidentity/isolated-lab-validation.json`，不随Git上传）。首选固定 6.6 验证基线尚未验收，这次 6.18.54.1 实验不定义产品最低内核或正式 Linux/Windows 支持。

本次修复私有 libbpf 0.6 对新内核 BTF 的读取失败（保留旧库，私有构建 1.6.2），将 TEST_RUN 接至 opts ABI，并分别严格核对 map 永久标志与 FD 只读访问模式；本次受影响 loader 58 项、checker 20 项测试通过，不能与上一节历史驱动准备的 58 项混为同次验收。跨 VM 材料保持原 receipt/hash，仅显式声明原目录用于三条 argv 文本核对；完整材料检查在附明确 operator 范围声明后为 `consistent_unverified`。声明与成功退出不证明连续隔离、采集完整性或正式身份能力。

5 个离线材料变异和 2 个真实 CLI 参数反例均拒绝；它们没有注入真实停止交错、漏挂、范围越界或复用故障。后续三个真实负例进一步明确了下节的在线前置 guard 优先级；启动实例/名称仍未验证，离线拒绝不能保护已经发生的路由。原始失败、旧 manifest 与正式 G01 **0/100** 全部保留，新两流不计入正式分母；T132 未完成、139 项/21 完成及 T137/P0/P1 未放行状态不变。二进制与原始日志留私有目录，不提交 Git。

## T132 真实负例与在线前置保护边界（2026-10-24）

在两流正例之外另行预登记并运行了三个真实 VM 场景，见[隔离实验验收](../../docs/validation/isolated-linux-lab.md)与机器记录（本地原件 `../../experiments/sourceidentity/isolated-lab-validation.json`，不随Git上传）：

| 场景与私有 run | 实际观察 | 验收分类 |
| --- | --- | --- |
| wrong-driver-scope / `bh_ookb1` | 不执行 preexec 放置；driver 1、observer 0，scope 预检拒绝且零 fixture | 原登记的预期拒绝命中 |
| observer-terminated / `1r5ztgz3` | SIGTERM 被 handler 处理；observer 1 且有 interrupted result，driver 1、零 fixture | 与原 driver 0 / observer -15 / 缺 result 预期失配，原登记不重写、不改判通过 |
| 独立登记 observer-killed / `cliyp3q8` | SIGKILL 后 observer -9、无 result；driver 0，三个 fixture 全 exit 0，最终 calibration failed | 仅命中新登记的缺失材料后验拒绝判据 |

SIGKILL 发生在 driver 启动前，但后续两客户端仍完成实际通信。这说明当前 driver 只在后验材料不全时拒绝校准，尚无在线身份/挂接失联前置 guard；它不能用于生产路由，也不能把这次有限负例通过写成 fail-closed 路由能力。该历史缺口随后由有限在线准入实验验证部分边界；该在线阶段的后续优先项是核心原 socket 接线及 FD/link/map 所有权；其后有限核心实测见末节，长期回收、重启、跨 namespace、exec/名称与完整停止/范围/复用矩阵仍待验。三组原材料、SIGTERM 预期失配及独立 SIGKILL 登记全部保留，不覆盖两流正例；正式 G01 0/100 与分母、T132 未完成、139/21 及 T137/P0/P1 不放行状态均不变。


## T132 有限在线准入两轮真实矩阵（2026-10-24）

[在线验收](../../docs/validation/connector-online.md)记录同一 owner 同时持有 listener、link/maps 和原 accepted socket 的受控原型：只按原 socket 见证在 Q 查询同步只插入绑定，grant 前不读取 token 或执行应用 I/O。v1 八场景已观察 10 条允许配对和六种拒绝条件，包括范围外 connect 拒绝、Q 前关闭 gate/link 或配置不符时零 I/O，以及服务进程终止后的 connect refused。服务终止不是管理器离线转发验收，八客户端批次也不证明全部并发交错。

v2 四个有效场景观察实际 native thread connect、父 connect 后 fork 子写、SCM_RIGHTS 转交及真实 tuple 复用；前三项连同 tuple 首次交换新增 4 条允许配对。tuple 第二次确实 connect 成功，四元组相同、PID/socket cookie 不同，随后无 identity/grant、零应用读写且 fault 非零，新增一种拒绝条件。总计 **13 次 VM、12 个有效场景、14 条有限允许配对、7 种拒绝条件**。首次 thread 因 image 缺运行时 `libgcc_s.so.1` 在回显后退出 -6，仍为失败；补私有 image 依赖并新登记的重跑单列通过，原登记及失败不覆盖、不混算其成功字节。

独立 host 检查核对每轮源/镜像/导出哈希、raw ABI、client oracle 与 scope、grant-before-I/O。启动 ticks 仅在 100 Hz 与零 time namespace 偏移条件下核对，comm 采样匹配保持 unverified，rename/path 未验收；oracle 不成为 resolver 输入。在该在线阶段，下一步是核心原 socket 接线及 FD/link/map 所有权；后续实测见末节，长期回收/重启/跨 namespace/exec 与名称矩阵、完整故障交错及正式 100 流仍未完成。旧 SIGTERM 预期失配、observer SIGKILL 后验拒绝与所有原始失败保留；T132/T137 未勾选、139 项完成 21 项、G01 新增 0、旧 0/100 与正式分母均不变，P0/P1 不放行，也不升级平台支持声明。

## 2026-10-24 有限核心接线进度（不改变依赖与门槛）

T132接续[核心准入移交契约](contracts/connector-core-handoff-v1.md)，已在真实Mihomo实验核心接通原Accept→Admit→不可变metadata值→同flow的严格[v3 opened](contracts/source-connector-v3.md)。`exec_core`同一PID exec移交11个FD，由Go核验并恢复CLOEXEC；原process保持unknown、正式identity能力false，不开放PROCESS/UID规则。原帧保存connector，Flow/RecoveryFlow派生报告不新增身份展示。完整scope/namespace、exec/改名、非原子名称、路径unknown、长期map回收及生产权限部署仍须按既有要求验证，有限接线不改规格条款。

001–008的真实原early-half-close请求发现Hoopa计量wrapper隐藏Upstream同时漏传CloseWrite；common.Cast可穿generic tracker，缺口位于Hoopa wrapper。独立0009显式转发半关闭控制，不暴露读写fastpath，原失败与修后同请求分开保留。最终001–009候选的两协议baseline及原early-half-close均核对双向各33B、来源重开旧hash不变与两世代，以及错generation/缺map FD/错program FD的零source/零listener拒绝；其他场景按[新验收](../../docs/validation/connector-core.md)与机器结果（本地原件 `../../experiments/sourceidentity/core-validation.json`，不随Git上传）记录，不在计划中预报完成总数。

同一固定核心的后续[边界矩阵](../../docs/validation/connector-core-edges.md)已经实际覆盖线程、父connect/子写、SCM转交、受控改名及同socket跨exec，各两协议共10流，原始connector与同flow线路/ready/终末一致。只关闭这些有限子步骤，名称原子一致性及任务剩余矩阵不因此消失。活动来源CP后SIGKILL/重开遵守[独立契约](contracts/connector-core-crash-v1.md)，只读pending不能伪装为已有writer结束证明的gap；T132/T137、139/21与正式门槛保持。

最终candidate SHA256 `452effa120f0f0beea122169c9e442b6338389bc4768111c4ed7ffb8d5f44f9b`，源码固定v1.19.26 / `fc8c5a24b16991f98cd736950c17d1aa306a5041`；[manifest](../../experiments/mihomo-events/core-connector-candidate-manifest.json)记录从干净上游依序apply、完整树/权限相等及无ldflags离线构建。打印默认版本1.10.0不是源码版本。旧patch、JSON、失败与正式G01分母不覆盖；T132/T137仍未勾选、139项完成21项、正式G01新增0/旧0/100、T127–T129与T018依赖保持。T136的管理器退出/接管须另验，不以核心SIGKILL代替，P0/P1未放行。

活动来源崩溃[定向回归](../../docs/validation/connector-core-crash.md)已完成：真实旧CP12/12后核心SIGKILL，新capture/source世代接续33/33，外部只读保留pending/未知尾部；另以测试overlay调用固定核心实际writer，对真实原帧副本三次重开验证稳定缺口事实与原帧重放。该结果不提前实现T138/T073生产恢复报告传输，不改变T136管理器生命周期验收或T132/T137依赖。

同名父子同时建连及同PID不同实例已完成[两次有限实测](../../docs/validation/connector-core-lifetimes.md)，按各自启动身份/socket关联原flow；不把进程名称当唯一实例键，也不回查当前PID改写旧账。后续早退FD使用、名称一致性与namespace/覆盖矩阵仍属于T132，依赖门槛保持。

固定核心已完成[PID/net namespace范围拒绝](../../docs/validation/connector-core-namespaces.md)：两轮新VM均在原connect拒绝，证据通道首轮失败保留。该结果不提供跨namespace支持；名称一致性、tuple复用及覆盖/缺失/冲突矩阵继续按T132执行。

名称一致性新增[有限可行性记录](../../docs/validation/connector-name-feasibility.md)：采用原子组合计数的研究方向已通过指令/挂接小样本；全钩子覆盖与内存序仍未成立，零BPF recursion_misses不能代替覆盖证明。继续限定反例和并发/生命周期验证，不提前将comm标为已证或用于进程名规则。

- 2026-10-24 固定核心[四元组复用拒绝](../../docs/validation/connector-core-tuple-reuse.md)有限实证完成，第一历史不变、第二身份未误配；一轮夹具失败与一轮有效负例分别保留。status的accepted_tcp_connections已明确为当前连接数，不能作为累计accept。表不回收导致整个捕获世代拒绝的可用性限制仍未解决；下一步独立验证带引用exe_file/d_path快照，不替换comm含义或提前提升名称/路径能力。139/21/T132/T137及正式门槛保持。

- 2026-10-24 [exe_file快照最小验证](../../docs/validation/connector-executable-snapshot.md)在实际guest加载并完成同socket-cookie关联及缺证据拒绝。仅独立字段可行性，路径可变且并非comm；先按新契约验证rename/超长/删除，再裁决产品来源，不从一次成功升级正式能力。初版checker ABI错误与同材料修订复核分记；139/21及全部门槛保持。

- 2026-10-24 [路径边界](../../docs/validation/connector-executable-path-boundaries.md)有限实测完成；[ADR009候选字段方案](../../docs/adr/009-process-label-sources.md)参照固定Mihomo的exe basename来源，将程序名/路径、leader/caller comm和应用显示名分开。该工程候选未改变用户澄清、现有v3字段或正式name能力；继续同socket重试/证据消费及关联连续性后再冻结新profile。

- 2026-10-24 快照新鲜性按反例拆分：[旧对象同socket重用](../../docs/validation/connector-executable-socket-reuse.md)已实际复现；[CAS一次消费](../../docs/validation/connector-executable-consumption.md)通过2VM，范围仅限已消费记录重用，不足以认证已捕获未消费记录。固定内核TFO会在SYN创建child时执行passive回调，普通tuple拒绝结果不能覆盖该时序；先独立观察TCP_INFO TFO_CHILD及普通半关闭，再决定核心拒绝实现，不以ESTABLISHED-only误拒合法half-close。正式字段/门槛保持。

- 2026-10-24 TFO候选采用固定内核TCP_INFO的TFO_CHILD拒绝，release限制与launcher镜像hash分别检查；[四项真实标志观察](../../docs/validation/connector-tfo-boundary.md)完成，0010已build但需新核心普通/earlySHUT_WR与TFO实际拒绝回归。未认证TFO身份，也不宣称早期错配竞态已实证。旧候选和其范围内证据保留。

- 2026-10-24 已实际观察[已捕获未消费记录被后续connect使用](../../docs/validation/connector-executable-unconsumed.md)。停止把单CAS作为完整新鲜性方案，转为研究每次原生connect的调用token、LSM记录与cgroup检查三者一致性；raw sys_enter/sys_exit覆盖、递归/线程生命周期与非普通路径均须先取证，不预报实现或能力通过。

- 2026-10-24 0010核心的[真实TFO负向回归](../../docs/validation/connector-core-tfo-negative.md)完成，和此前两协议半关闭正向结果共同约束该guard的有限范围。名称/路径新鲜性按[调用token原型](contracts/connector-executable-call-token-v1.md)继续：raw syscall tracepoint、当前task storage、饱和编号分配、LSM同调用快照与gate一次消费；原生ABI与TFO option另行限制。首轮单线程、可信loader独占隔离VM，不将零miss/link存在当作全部连续性证明，不将未运行的候选写为passed，也不提前扩展生产权限和平台范围。

- 2026-10-24 T132 [调用编号有限实测](../../docs/validation/connector-executable-call-token.md)完成：38项准备检查、1次隔离VM、10次native connect（含AF_UNSPEC）、9个token；旧EISCONN未消费记录、TFO与缺entry均拒绝，恢复普通新socket可达ECONNREFUSED。4map/4program、6次link创建（包含重挂）清理核对；18项后续离线变异不增加VM样本。新[source v4](contracts/source-connector-v4.md)解码38顶层/298含子测试race通过，旧v3严格性保留；目前组合BPF仅编译/离线解析，20FD核心接线进行中。139/21、T132/T137和正式新增0保持。

- 2026-10-24 T132 [组合核心正向实测](../../docs/validation/connector-executable-core-positive.md)在独立001–012新制品通过：76准备检查后1VM/2流/13帧，20FD接管、296B原exe快照、原PID/TID/cookie、token1/3、HTTP/SOCKS两实际节点与12/31ms ready、提前SHUT_WR后33/33及完整epoch均核对。0011首轮线路捕获失败原21项证据保持不变；0012只补profile4质量开关并增加真实子进程初始化回归。继续线程/转交/路径变化在新组合上的独立矩阵；T132/T137未勾、139/21、正式新增0及P0/P1门槛保持。

- 2026-10-24 T132新组合追加[五类归属边界](../../docs/validation/connector-executable-core-edges.md)：191离线检查后5VM/10flow/65帧，线程PID/TID、父子写入、SCM、comm改名、换路径exec均保持原connect快照；exec后不同path/inode不改历史。[范围负例](../../docs/validation/connector-executable-scope-negative.md)81离线检查后3VM/3基线flow/21帧，6次trigger/followup目标connect均EPERM，73项导出核对；owner状态只计accepted，不能代表BPF捕获健康。两批独立运行复核无critical/high矛盾，本批8VM无运行失败，准备问题及旧失败保留。继续[tuple/TFO负向](contracts/connector-executable-connection-negative-v1.md)、捕获健康/连续性与新组合恢复；T132/T137、139/21、正式新增0/P0/P1门槛不变。

2026-10-24补充：[001–012连接负例](../../docs/validation/connector-executable-connection-negative.md)三场实际完成，tuple/TFO及正常followup的不同拒绝阶段分别保存，不将accepted-only状态当采集健康。新增[采集诊断v1](contracts/connector-capture-diagnostics-v1.md)：独立只读接口、unknown/null与零区分、关闭后不读FD、两raw程序递归计数按固定ABI读取且接管/接纳保守拒绝。0013为独立制品，源码/测试/构建已完成，实际观测另验；零计数不能证明LSM或hook连续性。

2026-10-24进展：[0013实际诊断](../../docs/validation/connector-capture-diagnostics.md)已完成1VM，正常0、范围拒绝后1，四阶段重复只读观测与旧status不变；原33/33基线完整。后续按[新v4崩溃契约](contracts/connector-executable-core-crash-v1.md)验证两代20FD、12检查点/未知尾部null及新33终末，旧v3结果单独保留。

2026-10-24进展：[新v4崩溃/来源重开](../../docs/validation/connector-executable-core-crash.md)完成1VM、全新两代20FD和原始材料保真；旧12检查点pending/null、新33精确终末。另在测试私有副本调用实际openSource，三次writer生成稳定gap并原帧重放，生产源码/二进制未改；不得当作实际接口已导出gap。下一[link操作负例](contracts/connector-capture-link-operations-v1.md)验证固定rawTP/LSM不支持detach/update的实际API边界，特权测试父进程临时复制FD的前提单列，不宣称一般特权抗干预。

## T140：正式矩阵前补齐source5规则消费（2026-10-25）

T136已通过新0016五场控制矩阵；没有把此阶段等同项目完成。下一处实际缺口是旧matcher只读legacy process，而source5刻意将该字段保留unknown，protected profile仍拒绝所有PROCESS条件。追加T140建立显式opt-in、独立新校验入口和按值source5程序NAME/PATH消费，旧接口/旧0016helper继续拒绝新配置；完整语法、文本unknown与source初始化约束见[契约](contracts/connector-program-rules-v1.md)。不复用旧process字段，不把comm/应用产品名混入程序名。

实际非UTF8文件名保留合法raw connector但文本valid=false，可作为拟议rule-time unknown输入；必须先用新guest观测admission/opened，再验证有规则时的拒绝及AND false支配unknown。该路径目前只有源码/单测证据，不提前写实际通过。实例归属仍按connect initiator，FD转交不改历史。程序规则consumer仅为P0可行性补全，P2规则CRUD与UI仍按原顺序。

T140先在受限新候选校准后冻结，再由T127写source5-aware oracle：host固定全部100逻辑正例槽位及pins，guest每次connect前独立登记实际PID/start/executable/cookie；登记后unknown/缺失/超时均留在分母。预期拒绝/边界案例另列，不混成100正例身份通过。旧truth-set/host测试及0/100保留。T128/T129继续依赖T127，未更改P0/P1放行条件。

2026-10-25 T140后续验收：上述拟议非UTF8输入已在固定0017实际验证，14类/28流/14完整epoch通过，已勾选T140。新规则消费不等同生产控制helper集成或正式100正例通过。T127继续按connect前持久登记和完整分母执行。

2026-10-25 T127正向进展：[新独立登记100/100](../../docs/validation/source5-formal-identity.md)通过，固定0017、完整原连接oracle、5VM/612帧/210导出，真实只读入口通过。旧60/100及0/100保留。下一执行独立边界矩阵，不能把本次正向结果等同T127/G01或P0/P1放行。

2026-10-26：T127独立PID复用正式边界2/2通过，同PID30000不同start469/499及cookie1/4097，13帧/25导出；两个边界不计正向100。按[边界契约](contracts/source5-formal-boundaries-v1.md)继续缺凭据/权限/三值验证，T127仍未完成。

2026-10-26继续：权限正式单例1/1与缺凭据正式2/2均已独立复核并有限验收。权限只证实预登记BPF管理拒绝；缺凭据没有admitted来源账，bytes/final保留null。正式G01还需三值14槽与整体证据门槛，T127不提前勾选。

### US4 implementation boundary update (2026-10-28)

T044 has completed its bounded two-source matrix; its input archive and limitations remain in docs/validation/us3-sources.md. T045/T047 use the pure [ordered rule contract](contracts/ordered-rules-v1.md), separating supplied tri-state facts from any core capability. T046/T048 follow the [named managed entry contract](contracts/named-app-entry-v1.md): retain Intent App and immutable SourceSettings v1, prepare separate private immutable app-binding inputs explicitly, include every binding in Plan/generation identity, and route by ordered IN-NAME predicates. Initialization is local input preparation with no active/runtime effect; actual multi-entry forwarding/partial bind rollback/manager death require T051 evidence. Existing sources-v2 grammar is an implementation basis, not an automatic runtime acceptance claim. Unsupported original-process predicates remain refused.

The US4 native-rule inspection found an additional qualification dependency: original sources-v2 IP-CIDR returns false after failed DNS, while the ordered model requires potentially decisive unknown to reject. T048 must resolve and test that core rule boundary before app capability approval; grammar support alone is insufficient. Keep the original source candidate/receipts unchanged and use a separately frozen qualified app candidate via ApprovedAppRoutingCapability. This does not block the independent pure T047 rule calculation. Controlled DNS-failure and both false/unknown all orders must be covered before T051 completion.
