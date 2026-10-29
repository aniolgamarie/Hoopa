# Tasks: Hoopa 整体代理管理产品（P0–P3）

**输入**：[spec.md](spec.md)、[plan.md](plan.md)、[research.md](research.md)、[data-model.md](data-model.md)、[contracts/cli.md](contracts/cli.md)、[候选事件契约](contracts/telemetry-events.md)、[ADR 005](../../docs/adr/005-telemetry-replan.md)、[quickstart.md](quickstart.md)、[宪章](../../.specify/memory/constitution.md)。活跃模板为 `.specify/templates/tasks-template.md`。

当前总计140项、已完成72项。T022/T023 生命周期与应用恢复测试定义已完成（该阶段不代表实际产品验收；T034 已完成所需场景的分批真实覆盖，见 US5 验收），T024 Linux 文件存储已有限验收，T025 进程归属/IPC 已完成[模块有限验收](../../docs/validation/p1-runtime-ownership.md)，T026–T032 已按各自模块边界有限验收，T031 的[真实管理器死亡补验](../../docs/validation/p1-manager-crash-recovery.md)已完成，T032 的[运行意图/停止/有界重启](../../docs/validation/p1-runtime-lifecycle.md)已完成有限模块验收，T033 的[实际服务/CLI 接线](../../docs/validation/p1-service-lifecycle.md)已有限验收，T034 的[基础生命周期完整覆盖](../../docs/validation/us5-lifecycle.md)已验收，T035/T036 来源、逻辑组与调度测试定义已完成（语法通过、实现前预期 RED，非语义验收），T037 [严格意图解析](../../docs/validation/us3-intent.md)已有限模块验收，T038/T039 [来源模块](../../docs/validation/us3-provider-modules.md)已验收，T040 [逻辑组](../../docs/validation/us3-groups.md)已有限模块验收，T041 [来源完整编译与受保护自动组](../../docs/validation/us3-compiler.md)已有限验收，T042 [有界更新调度](../../docs/validation/us3-schedule.md)已完成内存模块验收，T043 [持久更新与命令接线](../../docs/validation/us3-source-update.md)已按有限profile验收，T044 [双来源故障与线路矩阵](../../docs/validation/us3-sources.md)已完成限定验收，T045规则测试定义已完成（预期缺API RED，非语义验收），T046应用入口测试定义亦完成（预期RED），T047 [纯规则求值](../../docs/validation/us4-routing.md)已有限模块验收；T048 [私有入口与完整 Plan 输入](../../docs/validation/us4-app-inputs.md)已按有限模块边界完成；T049 纯静态解释（本地原件 `../../experiments/gost-mihomo/us4-rule-explanation-validation.json`，不随Git上传）亦完成；T050 [完整影响预览](../../docs/validation/us4-impact.md)已有限验收；T051 实际应用故障矩阵（本地原件 `../../experiments/gost-mihomo/us4-app-runtime-validation.json`，不随Git上传）已有限验收；T139 [最小CLI与诊断](../../docs/validation/minimal-cli.md)已有限验收，T052 子环境测试定义（本地原件 `../../experiments/gost-mihomo/us2-environment-test-definition.json`，不随Git上传）已完成（预期缺API RED），T053 真实run测试定义（本地原件 `../../experiments/gost-mihomo/us2-launch-test-definition.json`，不随Git上传）已完成（预期缺API RED），T054 [显式本地初始化](../../docs/validation/us2-local-init.md)已有限验收，T055 纯子环境（本地原件 `../../experiments/gost-mihomo/us2-environment-validation.json`，不随Git上传）已验收，T056/T057[应用启动与CLI](../../docs/validation/us2-application-launch.md)已有限验收，继续 T058 首次旅程与真实Agent。T132受限原型、T137历史0015冻结、T136最小控制harness及T140有限程序规则校准已完成；固定0017的[T127/T012身份验收](../../docs/validation/source5-formal-identity.md)、[T128/T013恢复验收](../../docs/validation/source5-formal-recovery.md)及[T129/T014质量线路验收](../../docs/validation/source5-formal-quality.md)已完成。质量新登记6VM/21槽全部通过，241导出/194source原件独立复核，最终只读入口race1通过0失败0跳过。[T015生命周期/G07最小保护](../../docs/validation/g04-lifecycle.md)已受限完成：11VM/22应用尝试、14控制/14文件探针，最终只读入口race1通过0失败0跳过；T016已受限完成：16组/39VM/127尝试/124来源流/59epoch，正式汇总race通过且0跳过；T017已完成同源事件可行性裁决，旧hash运行与当前增量分列；T018已接受限定可行性，允许进入DB-free P1实现；旧0/100、60/100及其他失败保留，P0整体及P1产品尚未完成。历史阶段计数保持其当时范围。

本清单现进入实施阶段；仅已完成并取得对应证据的任务标为 `[X]`，其余保持未勾选。任务中的源文件、测试和证据路径是实施产物目标，文件存在不代表运行门槛通过。根代理此前已运行setup-tasks，本次不重复生成任务；按依赖从最小工程与P0开始实施。

格式为 `- [ ] TNNN [P?] [USn?] 描述；依赖 TNNN；FR/SC`。`[P]` 仅在下文明确波次中、列出的全部前置完成后可并行，最多两个子任务，单文件单写入者；不是可任意提前启动。故事同优先级按真实依赖排列 US1→US5→US3→US4→US2→US6；随后P2/P3。US5先消费P0固定核验bundle，US3才接入完整来源与编译器，因此不存在生产订阅与生命周期循环依赖。

宪章I–IX共同适用：P0先真证据；基础管理无GOST/SQL/TUI；policy不导入UI/GOST/SQL；不建插件/事件总线/多内核框架。不得默认TUN/系统代理/自启动/系统任务/特权/业务解密，不能自研协议内核；明确授权前不进行上述范围扩张。测试是规格要求的契约、身份、故障和真实工作流，不为可逆文档变更机械TDD。测试定义先于对应实现，测试运行依赖实现完成，环境不足与失败单独记录。

## Phase 1: Setup — 最小工程输入

**目标**：只建立单 module 与开发约定，不构建完整 CLI/账本。

**独立验收**：仓库只新增最小构建输入、忽略规则和版本说明；没有应用启动、下载或生成运行状态。

**阶段入口**：无；本清单生成不执行任何任务。测试定义先于对应实现；真实执行在实现与明确依赖就绪后进行。

- [X] T001 在 `go.mod` 固定 Go 1.27.3 构建候选与 `module hoopa`，外部正式地址确定后再变更；此时不引入 Cobra/TUI/生产 SQL 驱动；依赖 阶段入口；FR-001、FR-010。
- [X] T002 [P] 在 `.gitignore` 排除核心可执行文件、发行包、真实配置、账本和秘密，保留合成 fixtures 与源码；依赖 T001；FR-004、FR-030。
- [X] T003 [P] 在 `docs/development.md` 记录 Go 校验命令与固定模块摘要流程、外部核心安装职责及禁止默认系统改动，不安装或自动升级核心；依赖 T001；FR-005、FR-010。

## Phase 2: Foundational — 最小 P0 前提

**目标**：仅建立证据与标量/隔离边界；不预建完整命令树、生产 ledger 或迁移框架。

**独立验收**：合成 fixture 与事件格式可校验；私有 run 沙箱不会认领其他实例，未启动真实转发也不生成运行成功声明。

**阶段入口**：T002、T003 完成。测试定义先于对应实现；真实执行在实现与明确依赖就绪后进行。

- [X] T004 [P] 在 `internal/config/types.go` 定义分离的 Id/Revision/Generation/RunId/CounterEpoch 与精确字节/时间类型，逐字遵循“`Id` 为 1–128 字符本地稳定 token”“二者不能代替核心进程的 `RunId` 或计数器的 `CounterEpoch`”“时间用 UTC RFC3339；持续时间使用单调时钟，单位 ms”；Id pattern=`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`；依赖 T002、T003；FR-004、FR-033。
- [X] T005 [P] 在 `experiments/gost-mihomo/sandbox.go` 建立每 run 私有目录、独立端口/凭据和只操作核验身份的清理入口；实验数据不写用户既有状态，不设置系统代理/TUN；依赖 T002、T003；FR-005、FR-030。
- [X] T006 在 `experiments/gost-mihomo/evidence.go` 与 `experiments/gost-mihomo/faults.go` 定义 manifest/events/原始采样/断言结果格式及按持久边界注入故障的钩子，保留 passed/failed/unverified 和输入摘要；依赖 T004、T005；FR-003、FR-004。
- [X] T007 在 `testdata/providers/two-sources.yaml`、`testdata/providers/invalid-cases.yaml` 与 `testdata/traffic/truth-set.json` 建立两来源、重名/缺地区/无效YAML、两同名进程/两域名/子孙/PID复用/100短流的预登记真值，不含真实凭据；依赖 T006；FR-003、FR-013、FR-034、FR-035、SC-008。

## Phase 3: US1 — P0 组合证据（Priority P1，MVP）

**目标**：证明真实 Agent、计量、路由与恢复可行，再裁决生产边界。

**独立验收**：至少一个真实 Agent 工作流及受控校准；G01–G07 各自有结果与 ADR，关键失败未解决则不交付依赖功能。curl 只能校准。

**阶段入口**：T007 完成；P0 不实现完整 CLI/TUI/生产库表。测试定义先于对应实现；真实执行在实现与明确依赖就绪后进行。

**取证完成与门槛通过分开**：T011–T016 的完成条件是已尝试相应探针，并以固定输入记录可复现的 passed/failed/unverified 结果；完成取证任务不代表断言通过。前提不足时记录无法执行的原因与缺失输入，禁止伪造后续结果。整改分支在T010可用后即可启动，不要求失败断言先通过，也不必等待其余探针结束。已执行的原生探针与T122–T126保留为历史/子断言；T017的完成评审依赖T129，不能把启动条件误当完成条件。T127–T129须先满足R2中的明确能力开发/最小验证和候选冻结前置，T012–T014消费相同run证据形成门槛记录，不重复运行或改分母；每次失败追加证据与主代理裁决。只有 T018 在全部关键探针及最小访问保护实际通过、无关键失败或未验证项时才可完成，任何 failed/unverified 都不放行下游P1；T021还须全部P0门槛通过。

- [X] T008 [P] [US1] 在 `experiments/gost-mihomo/identity_test.go` 定义原口径 G01 真值、原始 socket 同步进程证据、共享归属拒绝、100短连接终末记录断言（历史任务及旧运行保留；新connector预期由T132/T127登记），覆盖 F20、F21、F22，不允许事后剔除可识别样本；依赖 T007；FR-003、FR-034、FR-036、SC-001、SC-008。
- [X] T009 [P] [US1] 在 `experiments/gost-mihomo/flow_test.go` 定义 G02、G03 的 CONNECT/SOCKS5、长连接、大数、失败/超时、计时/跨跳线路断言，覆盖 F16、F24、F26、F27、F28；依赖 T007；FR-003、FR-033、FR-038、FR-046、FR-048、SC-001。
- [X] T010 [US1] 在 `experiments/gost-mihomo/main.go`、`experiments/gost-mihomo/mihomo.yaml`、`experiments/gost-mihomo/gost.yaml` 实现最小固定版本 harness，记录核心路径/version/tag/sha256和真实Agent版本，分别保护controller、metrics与数据入口，不臆造认证字段；依赖 T008、T009；FR-003、FR-004、FR-010、FR-030。
- [X] T011 [US1] 运行 `experiments/gost-mihomo/flow_test.go` 的固定大小收发与真实Agent正常/持续工作流，在 `docs/validation/p0-workflow.md` 保存脱敏证据引用；分别证明代理变量、NO_PROXY、出口与应用信号，不以启动进程等同联网；依赖 T010；FR-001、FR-003、FR-009、SC-001。
- [X] T012 [US1] 复核T127在 `experiments/mihomo-events/identity_test.go` 产生的同源身份/终末证据，保留 `experiments/gost-mihomo/identity_test.go` 的历史oracle，使用T132/T127预登记的connect_initiator_v1新oracle（不照搬旧共享拒绝或重评旧run），保存 `docs/validation/g01-identity.md`；覆盖G01/F20/F21/F22，证明connect_initiator_v1的PID/OS启动实例及建连快照经原始入口socket关联，100预登记可识别短流归属/终末100%正确；保留旧GOST路径反例，权限skip不算通过，同一run不重复采样；依赖 T011、T127；FR-003、FR-034、FR-035、FR-036、SC-008。
- [X] T013 [US1] 复核T128在 `experiments/mihomo-events/recovery_test.go` 产生的G02/F05/F06/F16/F24证据，保存 `docs/validation/g02-counters.md`；核对同源source/epoch/flow、准确累计/终末delta、未送达与已送达重放、缺口及边界；source_seq与receipt_seq分开；旧GOST完整标签series仅作独立边界对照，浮点不精确样本不得精确对账；消费既有run而非重跑；依赖 T012、T128；FR-003、FR-033、FR-037、FR-038、FR-039、SC-001。
- [X] T014 [US1] 复核T129在 `experiments/mihomo-events/quality_route_test.go` 产生的G03/F26/F27/F28证据，保存 `docs/validation/g03-quality.md`；核验同一flow/attempt/generation下DNS到真实ready的起止、失败/超时及两节点历史链，connection_id须有无歧义映射，不要求猜配GOST SID；不用连接duration或当前now回填，消费既有run而非重跑；依赖 T013、T129；FR-003、FR-045、FR-046、FR-047、FR-048、SC-012、SC-013。
- [X] T015 [US1] 在 `experiments/mihomo-events/lifecycle_test.go` 定义并执行G04的F01/F02/F03/F04/F14，消费T136最小harness，覆盖同源候选spawn登记间隙、管理器/采集器退出、SSH/注销/休眠及PID/端口外部占用，在 `docs/validation/g04-lifecycle.md` 分记转发、源事件产生/持久化与消费连续性；执行G07最小管理IPC/controller/数据入口及事件journal访问保护的授权/未授权矩阵；若启用兼容GOST入口，另验证metrics独立认证，不以loopback/controller认证替代各端保护；依赖 T014、T136；FR-003、FR-024、FR-030、SC-001、SC-005、SC-018。
- [X] T016 [US1] 在 `experiments/mihomo-events/routing_test.go` 定义并执行G05的F08/F09/F10/F11/F12/F13/F29/F30/F31/F32/F33及C01，在 `docs/validation/g05-routing.md` 证明无GOST应用入口、同源路由前进程证据、有序三值规则、分阶段开放、完整回滚及旧候选零写冲突；原组合只作历史对照，不用其活动快照代替新候选；依赖 T015；FR-003、FR-018、FR-019、FR-020、FR-022、FR-023、FR-025、FR-050、SC-004、SC-005。
- [X] T017 [US1] 在 `docs/adr/001-event-source.md` 保留原生recorder候选的历史结论，按 `docs/adr/005-telemetry-replan.md` 及T122–T129完成整改评审；确认来源身份/终末恢复/计数开销/路由前能力与G01/G02/G03/G05同源证据，接收20事件重放不替代源完整；禁止魔法adapter、默认特权或未经许可核心fork；本任务只汇总已有取证与裁决，不再次叠加GOST扩展；依赖 T010、T129；FR-003、FR-004、FR-005、FR-034、FR-050、FR-051。
- [X] T018 [US1] 在 `docs/adr/002-p0-core-gates.md` 按ADR 005与事件契约逐项裁决G01/G02/G03/G04/G05、G07最小保护及托管/身份/编译/入口模型；绑定实际候选path/hash/能力，保留stock/兼容链差异；仅关键前提有真实证据且无未解决阻断时放行DB-free P1，不把取证完成、接收恢复、权限skip或未来上游接口当门槛通过；依赖 T012、T013、T014、T015、T016、T017；FR-003、FR-004、SC-001、SC-017。
- [ ] T019 [US1] 在 `experiments/gost-mihomo/sqlite_modernc_probe.go`、`experiments/gost-mihomo/sqlite_mattn_probe.go` 用build tags隔离两候选，在临时最小schema上先定义再执行同一组事务故障断言，执行 G06、F07、F17 的同一最小checkpoint事务/commit不确定/磁盘满/busy/WAL-SHM权限/只读与三平台构建比较，写 `docs/adr/003-sqlite-driver.md` 固定唯一驱动/版本/参数；实验不预建生产双驱动层；依赖 T018；FR-003、FR-004、FR-026、FR-033、FR-039、FR-054。
- [ ] T020 [US1] 在 `experiments/gost-mihomo/scale_security_test.go` 执行 G07、F17、C02 的最小权限/0、1、5入口/短清理批次及10k、100k、1m历史梯度，在 `docs/adr/004-capacity-security.md` 记录RSS、体积、积压、冷/热查询p50/p95、磁盘成本与有界规模预算；不足项保持未验证不阻塞无关P1；依赖 T019；FR-003、FR-004、FR-030、FR-040、SC-010、SC-016、SC-018。
- [ ] T021 [US1] 在 `docs/validation/p0-result.md` 汇总真实工作流、G01、G02、G03、G04、G05、G06、G07 证据与全部失败/未验证项，只有相关门槛已关闭才报告P0完成；MVP至此仅组合证据，不是基础产品；依赖 T018、T019、T020；FR-001、FR-002、FR-003、FR-004、SC-001、SC-017。

## P0 整改分支 R1：同源事件与接收持久化（T122–T129）

用户2026-10-22授权修复或重规划后追加；原任务ID/已完成记录不重编号。此分支从T010启动，属于P0，先于下游阶段；并非跳过T018。路线和新旧计数边界见[ADR 005](../../docs/adr/005-telemetry-replan.md)。保持源身份、终末完整性、质量/线路与最小权限要求，原型接收通过不等于来源通过。未授权核心fork不执行，T126先准备审查材料再处理明确选择。

T126候选记录：用户看到具体提案与授权问题后再次调用implement，已在私有/tmp隔离实施并构建候选；[候选manifest](../../experiments/mihomo-events/candidate-manifest.json)绑定固定commit、实际补丁、二进制与能力限制，单流回环已证明原生opened/终末和来源身份绑定。T126完成只表示可运行实验候选与审查材料已取得，完整进程归属、源故障矩阵、协议ready、实际两线路与配置级能力拒绝仍未通过，必须由后续探针验证；T018仍阻断P1。

- [X] T122 [US1] 在 `experiments/telemetry/journal_linux.go`、`journal_linux_test.go` 验证G02已送达事件的Write+Sync确认、epoch/event_id去重、原整数保真、单writer锁、结果不确定poison/重开消歧、损坏拒绝不截尾；文档明确不是源完整性/生产SQL；依赖 T010；FR-003、FR-033、FR-039。
- [X] T123 [US1] 在 `experiments/sourceidentity/resolve_linux.go`、`resolve_linux_test.go` 定义并尝试原入口100连接、同名源、共享FD与权限探针，记录同UID/同netns/IPv4限定和不可见owner集合的unverified；只完成实验取证，不代表G01通过；依赖 T010；FR-003、FR-034、FR-050、SC-008。
- [X] T124 [US1] 在 `experiments/gost-mihomo/observer_probe.go` 与平台journal桥接中绑定真实GOST世代、SID/服务白名单，Append+Sync后ACK；运行固定两核注入落盘后ACK503并关闭/重开核对，保存 `docs/validation/telemetry-replan.md` 与脱敏摘要，作为G02接收恢复子断言；不修复从未送达事件；依赖 T122；FR-003、FR-033、FR-039。
- [X] T125 [US1] 在 `docs/adr/005-telemetry-replan.md`、`contracts/telemetry-events.md` 及spec/plan/research/data-model记录同一Mihomo数据面事件候选、receipt/source序号区别、新旧边界及能力矩阵，并同步G03/G05入口、质量与选路设计；不宣称stock支持或下游放行；依赖 T123、T124；FR-003、FR-004、FR-034、FR-046、FR-048、FR-051。
- [X] T126 [US1] 在 `experiments/mihomo-events/README.md` 与可审查补丁提案中核实固定源码的process证据、opened/final、单flow/attempt、setup、actual chain及有界源持久输出；先完成接口/许可/维护成本审查，再取得可运行候选并记录path/hash/版本/能力。优先上游原生接口；若需实验fork，具体改动备妥且明确授权后才构建，不改用户核心；仅有提案或无授权/可运行候选时保持未完成，后续探针不得启动；依赖 T125；FR-003、FR-004、FR-005、FR-010、FR-034、FR-050、FR-051。
- [X] T127 [US1] 在 `experiments/mihomo-events/identity_test.go` 按connect_initiator_v1新预登记oracle运行G01/F20–F22及G05源身份三值子矩阵：两同名、子孙独立建连、父connect/子写与FD共享转交正例、PID复用/缺证据/权限、100预登记短流、源socket在路由前绑定；先完成T140独立source5规则消费器和能力小样本，再消费T132真实最小通过及T140新固定候选，正式登记前复核同一运行拓扑/可见性证据，条件变化先返回T130，不消耗正式分母；已登记后未知仍为失败，原0/100保留，不删除或重分类失败样本；依赖 T126、T132、T137、T140；FR-003、FR-034、FR-035、FR-036、FR-050、SC-008、SC-011。
- [X] T128 [US1] 在 `experiments/mihomo-events/recovery_test.go` 重跑G02/F05/F06/F16/F24，消费T133补全和T137固定候选，核对来源spool/census、未送达与ACK丢失、重放冲突、采集器/核心退出、满盘/背压及buffer/lazy-handshake边界，显式gap且无重复；本任务验收来源与接收journal，SQL驱动及事务由T019和T069/T071/T073后续分别验证，不作为本任务前置，也不由接收恢复代替；依赖 T127、T133、T137；FR-003、FR-033、FR-037、FR-038、FR-039、SC-001。
- [X] T129 [US1] 在 `experiments/mihomo-events/quality_route_test.go` 重跑G03/G05与F26–F28/F30，消费T134/T135补全和T137固定候选：同flow/attempt的DNS/ready/失败/超时/取消及ready_unknown，按冻结profile逐字段核对actual_connection_ref、同源monotonic_start/end、独立observed_wait_ms、definition/ready_kind/protocol及ready_unknown不覆盖已证结果；两线路/切组前后/改名/多节点链、连接段实际n守恒及无Conn不推实际链、未知身份拒绝且管理器离线继续策略，更新g01/g02/g03/g05记录；满足后才提交T017/T018评审；依赖 T128、T134、T135、T137；FR-003、FR-046、FR-047、FR-048、FR-049、FR-050、FR-051、SC-011、SC-012、SC-013。

  T129按[ADR011](../../docs/adr/011-source5-quality-acceptance.md)分层验收已完成：六个新formal VM/21槽全部通过，原运行/独立复核/受限决定与真实只读入口（本地原件 `../../experiments/mihomo-events/source5-quality-gate-validation.json`，不随Git上传）均齐全。source5注入分类/段守恒、adapter取消/未知边界单列，不计入真实网络分母。切组历史按既定独立core/入口/端口完整替换验证；原在线selector写入仍拒绝。原准备保持formal0，原件未重命名/重评；同hash身份/策略和恢复引用已接受T127/T128。客户端断开取消dial、真实lazy/TFO及同端口无缝替换不获能力声明。T014消费同run形成[G03记录](../../docs/validation/g03-quality.md)，不重复采样；T017/T018及P0/P1仍待各自评审。

## P0 整改分支 R2：前提诊断、能力补全与候选冻结（T130–T137）

以下2026-10-23叙述保留当时前提；当前完成状态以本页开头、任务勾选和文末最新记录为准。

2026-10-23全计划复核追加，保留全部原ID和复核时16项已完成标记。T126不回退为未完成；新任务承担后续修订和验收输入，不把先前部分候选提升为完整能力。复核后的T130/T131现已完成负向诊断/裁决，T132前提仍阻断、T133独立部分实现中；禁止直接无变化重跑T127。

T130/T131是诊断/设计任务，结论为不满足也可完成取证，但不能生成passed能力。T132–T137按各自明确验收完成；前置证据不满足保持未勾选。T133–T135可在T130/T131完成后进行不依赖正向身份的独立P0子实验，单写入者串行修改同一核心；T137要求全部能力最小验证和身份成功后才冻结。运行默认串行，未登记新的并行波次；不改变T018门槛。仅当与失败直接相关的环境、方案或实现输入发生可观察变化，且记录明确因果预期时才重试；无关改动、文档修改或仅重编相同源码不构成理由，旧run及正式样本分母保留。

- [X] T130 [US1] 在 `experiments/sourceidentity/preflight_linux.go`、`preflight_linux_test.go` 和 `docs/validation/p0-prerequisites.md` 定义并执行显式无特权前提诊断：同候选UID/netns/PIDns/父子关系和原socket持有方式下枚举owner所需范围，区分不存在/退出/权限/多持有者，不以只读自身FD通过；同时登记loopback、私有持久目录/锁/Sync、固定工具链/核心来源及真实SSH/休眠、schema、CGO/免费CI资源可用性，各项独立passed/failed/unverified。仅完成诊断，不启动正式100矩阵、不改变权限或系统；对应G01；依赖 T126；FR-003、FR-004、FR-005、FR-034、FR-054、SC-008、SC-015。
- [X] T131 [US1] 在 `docs/adr/006-source-identity-proof.md` 根据T130及旧0/100，裁决具体Linux原socket证据算法和可支持环境：定义唯一owner证明所需集合、持有/路由时点、PID/start前后复核、共享/转交竞态、exec/改名、IPv4/IPv6和namespace边界及unknown/reject；说明当前宿主阻断原因的已证范围和最小可观察重试条件。候选环境仍须正向验证，不接受“首owner/启动器/凭据即真实PID”、笼统换环境或未授权特权；方案不可证明则保留架构阻断，不宣称修复；对应F30/G01/G05；依赖 T130；FR-003、FR-004、FR-034、FR-049、FR-050、FR-051、SC-008、SC-011。
- [X] T132 [US1] 在 `experiments/sourceidentity/`、`experiments/mihomo-events/identity_preflight_test.go` 及隔离补丁的身份文件按[ADR007](../../docs/adr/007-p0-evidence-closure.md)先做connect捕获和原accepted socket关联两个有停止门槛的原型。采用用户已批准的connect_initiator_v1：原始客户端TCP建连实例、建连名称/路径快照；父connect/子写、fork共享、FD转交仍归已证connector，子独立connect归子，不要求逐字节writer/唯一当前holder。先冻结模型/契约/fixtures及PID坐标、start单位、namespace、后端generation和字段来源，进程TGID必须配leader启动身份，调用TID/线程comm单列，不能冒充进程名；ns/ticks和PID namespace坐标分开，字段读取一致性缺证据为unknown，部署前提及工作线程反例见[ADR008](../../docs/adr/008-remaining-evidence-execution.md)。再按ADR007的受限connect→active/passive→accepted SO_COOKIE候选具体化生命周期与冲突规则/匹配键；cookie不能直接相等，时间/tuple猜配不够。实测同名主/子短流、早退、同tuple复用、并发、exec/改名、跨namespace、事件丢失/重启及原connector缺失/关联多解；实例/名称/路径分字段能力，缺路径能力拒绝路径配置，未监测FD转交显unverified。没有唯一关联算法/真实正例或连续性证明则停在research/spike，不能先建完整helper、全unknown通过或重评旧0/100。内核候选仅在另有加载授权及前提时运行，选择口径不等于赋权；记录helper RSS/CPU、kernel map容量/内存及关联等待；对应F20/F21/F22/F30/G01/G05；依赖 T131；FR-003、FR-034、FR-036、FR-049、FR-050、SC-008、SC-011。
- [X] T133 [US1] 在 `protocol/telemetrywire/`、隔离补丁的 `component/hoopatrace/`、`experiments/mihomo-events/source_lifecycle_test.go`、`transport_integration_test.go` 及既有只读恢复器完成[ADR007](../../docs/adr/007-p0-evidence-closure.md)最小闭环：同源纯wire/epoch完整形状/生命周期校验接入source重开、发送快照、receiver及reader，现有v1/旧原帧保真。原opened/CP/closed/gap按原ID/seq重放；恢复报告是派生数据，缺final且无source writer结束证据为pending；仅显式启动/恢复持独占writer锁并核验稳定完整来源后可报epoch_ended_without_final gap，CP保留、尾部null，锁不证明网络结束。closed精确幂等不重加、不造gap；必要持久reconciliation_id只依原source/epoch/flow/opened及事实kind生成，同事实去重/冲突拒绝，不占source/receipt序或新增ByteFact；pending不发布不可恢复事实，同ID冲突只比较稳定事实payload而非本次扫描时间等元数据。撤回强制v2 recovery_gap/跨epoch引用图及历史全量恢复事件预留；历史gap与当前recording_status分开。验证opened/CP后退出、closed未投递、完整/短Write、Sync不确定、损坏/缺manifest、反复恢复中断、多epoch、503/ACK丢失、ENOSPC/配额/collector离线及背压；坏尾保留并拒绝新完整计量，不自动截断，接收ACK不冒充SQL。冻结CONNECT/SOCKS5/预读/partial-write/lazy-handshake实际n边界；在实际8MiB/32epoch等上限内登记规模/容差并测重开扫描耗时、峰值内存/未终结flow数。生产持久Gap/Reconciliation与传输/回收由T073/T138完成；对应F05/F06/F24/G02；依赖 T126、T130、T131；FR-003、FR-033、FR-037、FR-038、FR-039、SC-001、SC-008。
- [X] T134 [US1] 在隔离补丁的outbound协议/inbound/tunnel/statistic观测点、`protocol/telemetrywire/` 与 `experiments/mihomo-events/setup_probe_test.go` 按[ADR007](../../docs/adr/007-p0-evidence-closure.md)先冻结data-model/telemetry事件profile和正反fixtures，再实现target_known→协议已证ready的flow及独立attempt；字段含ready_kind/protocol/definition、success/failure/timeout/cancelled/unknown、不可见null及reason、独立observed_wait_ms；ready未证不覆盖已知失败分类，严格v1无法承载时明确新profile、保留旧解码。先校准DIRECT TCP、固定候选HTTP CONNECT 200（通用协议为2xx）及SOCKS5完整成功响应，补VER/REP/RSV拒绝反例；不用Dial nil/NeedHandshake=false统一推断ready；按[ADR008](../../docs/adr/008-remaining-evidence-execution.md)把完成证明/单调ready点绑定实际Conn，未知外层遮断内层TCP证明，ready点与finish/落盘时刻分开。覆盖HTTP成功回复头与首段隧道payload同包的预读保留/实际n不丢不重、慢DNS、延迟握手、partial/失败/超时/取消及lazy无确认；其他协议为ready_unproven/null。生成bundle以持久node/provider/group引用作为core内部名称、展示别名单列并验证映射，不能用随机adapter UUID冒充跨重开稳定ID；两条受控真实节点在具体Conn产生处保存稳定node/provider身份、链及generation，tracker绑定同flow/attempt；无Conn只保留selection_only/unavailable，不推导实际链，旧连接切组/改名后历史不变。字节按实际n归读写连接段，同边界flow总量=已证段+unallocated，不挂最后attempt或重复叠加。可见起止与受控同单调时钟参考及预登记容差一致，业务TTFB不可见为N/A；对应F26/F27/F28/G03；依赖 T126、T130、T131；FR-003、FR-045、FR-046、FR-047、FR-048、SC-012、SC-013。
- [X] T135 [US1] 在隔离补丁配置校验/规则/入站位置与 `experiments/mihomo-events/capability_test.go` 实现配置加载期的能力拒绝和入站保护：不支持的进程实例/条件组合/环境在开放端口前拒绝，合法配置仍有正向用例；配置不支持区别于单连接身份unknown，保留首匹配/AND三值；具体验证认证、命名入站不绕规则、generation只绑定已验证配置、旧入口排空与失败不提前开放。未实现热重载时明确只允许完整replace，不伪造热更新；对应F10/F11/F30/G05/G07；依赖 T126、T130、T131；FR-003、FR-018、FR-019、FR-022、FR-030、FR-032、FR-049、FR-050、SC-004、SC-011、SC-018。
- [X] T136 [US1] 在 `experiments/mihomo-events/control_probe.go`、`control_probe_test.go` 实现G04/G05所需最小控制harness：私有受保护IPC、控制进程与核心分离、spawn前nonce/意图及身份登记、stopped/running、不可变旧/新bundle、串行分阶段应用、C01旧候选零写冲突/同ID重试和故障钩子；小样本先验证管理器退出仍转发、同核心接管、入口关闭/开放顺序及失败保留旧态。仅实验运行器，不搭完整CLI/账本；真实SSH/注销/休眠由T015在登记环境另验；对应F06/F10/F11/F12/F13/F14/F33/G07；依赖 T137；FR-003、FR-022、FR-023、FR-024、FR-025、FR-030、SC-005、SC-018。
- [X] T137 [US1] 在 `experiments/mihomo-events/patches/` 及 `experiments/mihomo-events/candidates/<binary-sha>.json` 冻结新候选：T132–T135最小正负真实/定向断言符合各自范围，新的同拓扑身份smoke必须成功；记录commit/补丁/二进制/工具链/依赖/配置/能力摘要，干净副本实际apply、构建和版本校验。旧manifest/summary/run不可覆盖；新配置或核心改变须说明哪些已有证据失效并重验，不直接复用旧hash的正式结果；对应G01/G03/G05；依赖 T132、T133、T134、T135；FR-003、FR-004、FR-005、FR-010、FR-034、FR-046、FR-048、FR-050、SC-001、SC-008。

## P0 补全分支 R3：source5程序规则消费（T140）

T136后续审查确认：既有identity_test仍面向host/legacy process，source5 intentionally保留旧process unknown；字段available不等于PROCESS规则可用。正式矩阵不承担首次能力开发。按[新实验契约](contracts/connector-program-rules-v1.md)先完成明确consumer，再由T127预登记新oracle；旧0/100不重评、不覆盖，T132/T137/T136历史完成不撤回。

- [X] T140 [US1] 在 `experiments/mihomo-events/capabilityprofile/program_rules.go`、`program_rules_test.go`、隔离核心的 `component/hoopaguard/profile/`、`component/hoopatrace/`、`rules/common/process.go` 与 `hoopa_guard_linux.go` 实现显式 `connect_initiator_exact_program_v1` consumer：独立新配置校验入口，旧Validate/旧身份入口及0016helper继续拒绝新配置；仅启用source5原connector的精确区分大小写NAME/PATH与单层AND，所用字段必须在required_fields中；按值冻结profile/原exe快照，不填旧process/comm，不提供PID/UID/instance/regex/wildcard规则。维持false支配unknown、首true及关键unknown拒绝，新增认证只读program-rules声明、旧status不变。先定向race与旧入口回归，再形成干净追加补丁/独立manifest/新hash；实际guest小样本验证NAME/PATH真/假、大小写、真实非UTF8文本unknown、AND两个顺序、后续节点/无dial拒绝与完整字节，不能用admission失败代替rule-time unknown。新候选与CapabilityRecord说明受影响G01/G03/G05/G07最小保护；旧hash结果不复制，校准不计正式100分母。由T127随后实现新版oracle及100槽位预登记。依赖 T132、T135、T136、T137；FR-003、FR-034、FR-035、FR-036、FR-050；SC-008、SC-011。

## Phase 4: US5 — P1 生命周期、精确应用与恢复（Priority P1）

**目标**：用P0已核验的固定bundle建立文件控制状态和核心所有权，不等待完整来源编译器。

**独立验收**：固定可信bundle场景可独立启动/停止/接管/失败回滚；崩溃各边界、旧计划与原ID重试结果一致，无GOST/SQL可工作。

**阶段入口**：T018 核心门槛完成；T019、T020 可继续但不阻塞本故事。测试定义先于对应实现；真实执行在实现与明确依赖就绪后进行。

- [X] T022 [P] [US5] 在 `tests/integration/lifecycle_test.go` 定义 F01、F02、F03、F04、F06、F14、F18：用 `testdata/runtime/verified-bundle.json` 固定P0核验产物，断言强身份、spawn间隙、显式停止不被重启及control-only不启动核心；依赖 T018；FR-002、FR-022、FR-024、SC-005。
- [X] T023 [P] [US5] 在 `tests/integration/apply_recovery_test.go` 定义 F10、F11、F12、F13 和 C01：prepared/应用/开放/publishing/active/终态边界失败、完整旧态核验、同ID先查与异内容拒绝，明确文件/核心非共同事务；依赖 T018；FR-022、FR-023、FR-025、SC-005。
- [X] T024 [US5] 在 `internal/config/store.go`、`internal/config/atomic.go` 实现私有XDG config/state/cache/runtime分工与不可变bundle/active pointer，逐字遵循“active.json、操作日志和运行登记不能只放在注销/重启会清空的 runtime 目录”“写临时文件与 rename 必须同一文件系统，持久化文件及父目录”；依赖 T022、T023；FR-022、FR-025、FR-030。
- [X] T025 [US5] 在 `internal/platform/process_linux.go`、`internal/runtime/ownership.go` 及 `internal/platform/ipc_linux.go` 落地G04 ADR组合身份与spawn前nonce/意图、spawn后登记，以及T033服务开放前的私有Unix socket/目录权限与peer身份校验；逐字遵循“PID/端口/API版本单独均不足以认领”“证明不足视为冲突，不用 PID 文件缺失推断‘没有进程’”，不杀外部进程；依赖 T024；FR-024、FR-025、FR-030、SC-005、SC-018。
- [X] T026 [US5] 在 `internal/mihomo/controller.go`、`internal/mihomo/capabilities.go` 消费固定路径/version/sha256和G05许可能力，最小允许端点保护controller，核验完整组/选择/入口；按[ADR013](../../docs/adr/013-p1-runtime-ownership.md)补齐真实closed-start/认证activate，当前立即开放核心不能冒充具备此能力。先固定版本化控制/幂等失败契约，再冻结新候选及能力，实际验证G04/G05/G07受影响路径并裁决G01–G03增量，保留旧hash全部历史结果，基础输入验收后新增独立完整basic fixture并让P1生命周期选择它，保留旧完整身份fixture及全部规则/原件，不仅换旧hash或删进程规则冒充通过；完整身份模式按T138/T077另验；启动离线固定资产，不下载/升级；依赖 T025；FR-010、FR-015、FR-022、FR-030。
- [X] T027 [US5] 在 `internal/control/operation.go`、`internal/control/replay.go` 实现“同 ID/同内容返回原结果；同 ID/异内容拒绝”“未完成日志不得回收；压缩终态正文时保留去重 tombstone”，request_digest覆盖版本/操作/完整参数，容量满拒绝新写、不设重试遗忘窗口；依赖 T026；FR-011、FR-025。
- [X] T028 [US5] 在 `internal/control/plan.go`、`internal/config/canonical.go` 实现Plan绑定revision/编译器/能力/provider/binary/rule摘要与RFC8785 JCS摘要（仅排除顶层plan_digest），拒绝重复key/无效Unicode/NaN/Infinity及超安全整数；本地plan纯编译、仅显式输出文件，未知影响requires_disruption=true；按[Plan信封v1](contracts/plan-envelope-v1.md)使用必需纯Intent语义验证器，缺失即拒绝，固定输入不冒充完整来源编译，T037–T041补真实语义实现；依赖 T027；FR-021、FR-025、FR-026。
- [X] T029 [US5] 按[协调接纳v1](contracts/coordinator-admission-v1.md)在 `internal/control/coordinator.go` 落地单写入者及“过期返回 CONFLICT，既不创建本次持久操作，也不改文件或运行状态”；先解析已接纳请求重放再查旧revision，确认摘要/中断/全部输入后接纳，不自动合并或重规划；依赖 T028；FR-011、FR-022、FR-025、SC-005。
- [X] T030 [US5] 在 `internal/control/apply.go` 实现“prepared → validated → applying → verifying → publishing → committed”及P1 start/replace完整替换；关闭新入口、固定资产/选择/规则/所有应用入站核验再开放，实际核验后先publishing再active再committed；依赖 T029；FR-021、FR-022、FR-025、SC-005。
- [X] T031 [US5] 在 `internal/control/recover.go` 实现“rolling_back → rolled_back”和失败“degraded”，逐字遵循“committed/rolled_back 是经实际核验的终态”“degraded 表示未能恢复确定状态，禁止自动写操作，查询仍可用”；pointer/实际核验/终态缺失分别消歧，不盲重放；按[操作日志v1](contracts/operation-journal-v1.md)补config显式临时残留检查/维护API，恢复调用独立去重记录与原目标操作分别持久化/核验关联和中断，不更换原操作身份；依赖 T030；FR-023、FR-025、SC-005。
- [X] T032 [US5] 按[运行生命周期契约](contracts/runtime-lifecycle-v1.md)在 `internal/runtime/lifecycle.go` 与 `internal/control/lifecycle*.go` 实现运行意图running/stopped：停止前持久stopped及阶段，核验后发布revision；“revision 随已提交配置、保留策略或核心运行意图变更前进，不随遥测/查询/probe/仅下载候选前进”，control-only只观察、service.stop不杀核心；授权running且无待恢复/degraded才有限重启；依赖 T031；FR-022、FR-024、FR-025、FR-029。
- [X] T033 [US5] 按[服务传输](contracts/service-transport-v1.md)与[已运行接管](contracts/managed-start-v1.md)约束，在 `cmd/hoopa/main.go`、`internal/cli/service.go` 接入显式start、start --control-only、service run/stop、stop --accept-disruption与operation恢复用例，添加Cobra v1.10.2及摘要；无已提交可用快照普通start失败，不构建P2空命令；依赖 T032；FR-001、FR-002、FR-012、FR-024。
- [x] T034 [US5] 执行 `tests/integration/lifecycle_test.go` 和 `tests/integration/apply_recovery_test.go`，在 `docs/validation/us5-lifecycle.md` 保存F01、F02、F03、F04、F06、F10、F11、F12、F13、F14、F18、C01重现结果与零误接管/重复启动证据；使用T026独立完整basic fixture，不依赖BPF或把入口归属当源PID，缺能力的进程规则明确拒绝；旧identity fixture保留给P2对应验收；依赖 T033；FR-022、FR-023、FR-024、FR-025、SC-005。

## Phase 5: US3 — P1 来源、稳定组与编译（Priority P1）

**目标**：将受限订阅/稳定节点/组策略编译接入US5完整bundle，更新只产生候选。

**独立验收**：两个来源含重名/改名/地区缺失时US/JP/BEST稳定；错过3周期每来源只补一次，失败旧快照可用，未确认中断候选待应用。

**阶段入口**：T034 生命周期验收通过；不需要SQLite。测试定义先于对应实现；真实执行在实现与明确依赖就绪后进行。

- [X] T035 [P] [US3] 在 `internal/subscription/provider_test.go` 定义F08、F09、F32：重名/改名/认证变化/身份键丢失/未知地区/跨源pin/恶意字段和超量输入拒绝，固定节点不重绑定；依赖 T034；FR-013、FR-015、FR-020、FR-030、SC-002。
- [X] T036 [P] [US3] 在 `internal/policy/groups_test.go`、`internal/schedule/update_test.go` 定义手选/自动/跟随无环与SC-006错过3周期、失败保留、每来源/全局并发和F08、F09的待应用状态断言；依赖 T034；FR-014、FR-016、FR-017、SC-002、SC-006。
- [X] T037 [US3] 在 `internal/config/intent.go` 实现严格schema_version=1的core/subscriptions/selection/groups/apps/rules/default_target；拒绝未知字段/重复键/危险tag/深度与alias超限，core二进制path/version为非空字符串、sha256为64位小写十六进制且全部必填、不存latest；所有对象additionalProperties=false；draft可active_subscription=null，apply须完整；source严格https(url_secret_ref私有路径)或file(path非空)，不内联URL秘密；依赖 T035、T036；FR-006、FR-010、FR-013、FR-030。
- [X] T038 [US3] 在 `internal/subscription/fetch.go`、`internal/subscription/parse.go` 实现节点字段allowlist、https/file显式读取与不可变ProviderSnapshot；单次30秒/解压10MiB/10000节点/深度64为可调限值，越界保留旧态，不执行脚本/在线转换；依赖 T037；FR-013、FR-015、FR-016、FR-030。
- [X] T039 [US3] 在 `internal/subscription/identity.go` 通过私有键规范化协议/端点/身份参数维护来源内稳定ID；逐字遵循“ID 与显示名分离；同名不覆盖，歧义不自动匹配”“供应商认证/身份变更按新身份处理”；缺键/冲突显式阻断固定pin；依赖 T038；FR-013、FR-020、SC-002。
- [X] T040 [US3] 在 `internal/policy/groups.go` 实现“manual/source/selected_node_id、auto/source/probe 或 follow/follows 三种互斥策略”，source active或subscription，required为boolean、region可省略或非空字符串/null，验证无环/无悬空、必需组非空/手选属源；自动probe url为合法的非空http/https字符串，interval_seconds/timeout_ms为正整数、tolerance_ms为非负整数且全部有限，只在确认候选内；依赖 T039；FR-014、FR-015、FR-028、SC-002。
- [X] T041 [US3] 在 `internal/policy/compile.go`、`internal/config/bundle.go` 将已验证所有引用来源/规则资产/二进制及能力写完整bundle交US5；先补版本化受保护核心/快照对远端节点与auto URL-test的声明和实测，保留basic-v1兼容与旧证据，自动组策略快照不冻结实时选中节点、未成功探测/全失败候选不得默认首选；“不使用核心默认首节点、COMPATIBLE 或其他来源后备”，自动探测不等同业务最优，地区unknown不猜补；依赖 T040；FR-013、FR-014、FR-015、FR-020、FR-022。
- [X] T042 [US3] 在 `internal/schedule/update.go` 实现“周期为 null 或正整数小时”、启用才调度、全局最多2来源且每来源1个；错过周期每来源最多补一次，不创建系统任务；下载/有效/待应用/已生效分态，不用新候选擅自中断；依赖 T041；FR-016、FR-017、SC-006。
- [x] T043 [US3] 在 `internal/control/source_updater_linux.go`、`source_apply_linux.go`、`source_schedule_linux.go` 与 `internal/cli/subscription_linux.go` 接入显式update、list、选择/跟随配置的plan/apply和单写入复核；下载在临界区外、入写前复核身份，成功下载不推进global revision；依赖 T042；FR-012、FR-014、FR-016、FR-017、FR-025。
- [X] T044 [US3] 执行 `internal/subscription/provider_test.go`、`internal/policy/groups_test.go`、`internal/schedule/update_test.go`、`internal/control/source_matrix_linux_test.go` 与固定核心对照，在 `docs/validation/us3-sources.md` 记录两来源US/JP/BEST、F08、F09、F32及SC-002/SC-006全矩阵，无静默后备/解除pin；依赖 T043；FR-013、FR-014、FR-015、FR-016、FR-017、SC-002、SC-006。

## Phase 6: US4 — P1 有序应用选路（Priority P1）

**目标**：无计量也能为应用指向组或来源固定节点，按统一有序规则解释与执行。

**独立验收**：两个应用/两个组/一个pin及域名IP重叠矩阵实际路径符合首匹配，部分入口绑定失败完整回滚。

**阶段入口**：T044 完成来源与编译集成。测试定义先于对应实现；真实执行在实现与明确依赖就绪后进行。

- [X] T045 [P] [US4] 在 `internal/policy/rules_test.go` 定义F31、F32的顺序/遮蔽/失效pin及三值all真值表；match仅app/process_instance/process_name/process_path/domain exact或suffix/ip_cidr/all，all非空且最大深度16，unsupported过程条件P1拒绝；依赖 T044；FR-018、FR-020、FR-027、FR-050、SC-004。
- [X] T046 [P] [US4] 在 `tests/integration/app_routing_test.go` 定义F29、F33：无GOST/SQLite双profile、组/节点实际路径、部分绑定失败和管理器死亡仍按已发布策略转发；依赖 T044；FR-002、FR-019、FR-022、FR-051、SC-004。
- [X] T047 [US4] 在 `internal/policy/rules.go` 实现“数组位置即优先级。应用目的地只通过这里的 app 条件表达，profile 不再有隐含优先级覆盖”“Target：group(group_id)、node(subscription_id,node_id)、explicit direct、reject 四种互斥引用”，default_target显式；逐字遵循“all 中存在确定 false 则整体 false；全部 true 才为 true；其余为 unknown”；依赖 T045、T046；FR-018、FR-019、FR-020、FR-050。
- [X] T048 [US4] 按[named-app-entry-v1](contracts/named-app-entry-v1.md)先以受控解析失败校准IP未知与all假值支配、补齐并冻结独立应用选路核心候选/能力（旧来源核心证据不外推），再在 `internal/policy/app_entries.go`、`internal/config/app_binding_store_linux.go`、`internal/control/app_entry_initialize_linux.go` 与现有 `internal/mihomo/source_protocol_linux.go` 适配路径及协调器捕获中实现显式不可变入口初始化、完整Plan身份绑定，按G05能力编译命名profile入站和同一规则顺序，不用listener直接proxy绕过前置域名/IP；“managed 使用已应用 profile 的 Mihomo 命名入口”，profile上下文不冒充源PID；依赖 T047；FR-018、FR-019、FR-034、SC-004。
- [X] T049 [US4] 在 `internal/policy/explain.go` 实现纯静态顺序、已知遮蔽、缺目标与未知条件解释，区分意图/实际证据，不调用controller探测或修改选择；依赖 T048；FR-018、FR-021、FR-026、FR-027。
- [X] T050 [US4] 在 `internal/control/impact.go` 披露受影响节点/组/规则/应用/存量与新连接、runtime_action(start/replace/select/none)、connection_effect(preserve_old/interrupt/unknown)、previous_generation可null及target_generation必填、blockers与回滚目标；“未知影响必须 requires_disruption=true”，P1仅start/replace，确认后交US5完整替换；依赖 T049；FR-019、FR-021、FR-022、SC-004。
- [X] T051 [US4] 执行 `internal/policy/rules_test.go`、`tests/integration/app_routing_test.go` 的F29、F31、F32、F33，在 `docs/validation/us4-routing.md` 核对每条真实路径、遮蔽及中断预览，缺目标/不可达不换源直连；依赖 T050；FR-018、FR-019、FR-020、FR-021、SC-004。

## Phase 7: US2 — P1 首次配置与子进程接入（Priority P1）

**目标**：完成init、inspect与三模式真实Agent启动，父环境和既有系统配置不变。

**独立验收**：首次缺核心/地区清楚报错；无GOST/DB/TUI时完成managed；inherit/direct与信号/退出/后台后代一致，managed失败不降级。

**阶段入口**：T051 应用入站与影响验收通过。测试定义先于对应实现；真实执行在实现与明确依赖就绪后进行。

- [X] T139 [US2] 在 `tests/integration/onboarding_entry_test.go` 先定义真实CLI首次旅程前置，再在 `internal/cli/config.go`、`internal/cli/status.go`、`internal/control/query.go`、`internal/control/doctor.go` 接入最小人类config plan/apply和只读status/doctor；复用已实现的US5/US3/US4用例、T025访问保护，不新建第二套变更或恢复状态机。覆盖缺实例/有草稿/已提交及失败回滚入口，命令执行不依赖未来T065；完整JSON契约及诊断字段由US6扩展，不把最小入口冒充全API交付；对应F17；依赖 T051；FR-001、FR-006、FR-012、FR-021、FR-026、FR-029、SC-003、SC-007。
- [X] T052 [P] [US2] 在 `internal/launch/environment_test.go` 定义F18、F19的大小写代理冲突/NO_PROXY/bypass、三模式与计量兼容断言，基线父shell与系统代理不得变化；依赖 T139；FR-002、FR-007、FR-008、SC-003。
- [X] T053 [P] [US2] 在 `tests/integration/launch_test.go` 定义F15、F19的argv数组/TTY/信号/退出码/后台后代与不存在入口失败，既有launch不因新run或计量开关被重载/迁移；依赖 T139；FR-007、FR-008、FR-009、FR-032、SC-003。
- [X] T054 [US2] 在 `internal/control/init_linux.go`、`internal/cli/init.go` 实现显式路径本地init与依赖/缺失项指南，首次Store创建仅限显式初始化/既有T033人类control-only写打开；draft/launch历史空间由init显式准备，query/run不创建；不联网/建账本/启动，缺version/hash生成可解释draft或报告缺输入，不发布已应用generation或running意图；依赖 T052、T053；FR-006、FR-010、FR-026。
- [X] T055 [US2] 在 `internal/launch/environment.go` 实现managed清理冲突大小写变量、仅profile生成代理/NO_PROXY且默认bypass=[]；inherit原样继承、direct清理声明变量不承诺绕过TUN；“metering=true 只允许 managed”，父环境不改；依赖 T054；FR-007、FR-008、SC-003。
- [X] T056 [US2] 在 `internal/launch/run_linux.go`、`internal/launch/process_linux.go`、`internal/launch/terminal_linux.go` 实现参数数组/TTY/中断/退出码与launch身份，有证据后代状态单列；managed仅使用已提交profile既有入站，无入口要求显式plan/apply，“不因每次 run 隐式重载核心”，direct/inherit可读draft不启动服务；依赖 T055；FR-007、FR-008、FR-009、FR-019、FR-032。
- [X] T057 [US2] 在 `internal/cli/run_linux.go`、`internal/cli/app_linux.go` 接入inspect/run三模式预览与正常应用IO，JSON管理无任意子进程操作；显示入口/子环境变化/绕过/计量、运行成功不等于访问成功，不保存完整敏感argv环境；依赖 T056；FR-006、FR-007、FR-009、FR-012、FR-030。
- [ ] T058 [US2] 执行 `internal/launch/environment_test.go`、`tests/integration/launch_test.go` 并真实Agent访问，在 `docs/validation/us2-onboarding.md` 记录F15、F18、F19与SC-003，零父环境/系统修改、未知后代不假结束、无计量基础旅程完整；依赖 T057；FR-001、FR-002、FR-006、FR-007、FR-008、FR-009、SC-003。

## Phase 8: US6 — P1 只读、JSON v1 与访问保护（Priority P1）

**目标**：人类/机器共用用例，读取无副作用，主动下载/探测有明确边界。

**独立验收**：未运行/正常/degraded下所有只读调用零创建/恢复/网络；错误可解析、stdout无应用输出，未授权读写及敏感回显为零。

**阶段入口**：T058 首次配置/启动旅程通过；P2导出增强另在US7/US9补齐。测试定义先于对应实现；真实执行在实现与明确依赖就绪后进行。

- [ ] T059 [P] [US6] 在 `tests/contract/api_v1_test.go` 定义版本、request_id长度1–128、未知字段/重复JSONkey/尾随/超限、argv操作一致、成功ok/data/error互斥、退出码2–7与非终态收据轮询正反例，schema校验不能代替解析/时序检查；依赖 T058；FR-011、FR-012、FR-025、SC-007。
- [ ] T060 [P] [US6] 在 `tests/integration/readonly_security_test.go` 定义F17与授权/未授权读写矩阵，对照树/hash/mtime/进程集合，atime/无关后台写入单列，诊断/错误/导出秘密泄漏为零；依赖 T058；FR-026、FR-029、FR-030、SC-007、SC-018。
- [ ] T061 [US6] 在 `api/v1/query-response.schema.json`、`api/v1/query-response.fixtures.json` 补全当前仅信封覆盖的status/doctor/config/subscription/group/app/route/active-connections/operation/probe完整版本化业务结构；status含desired_runtime及各组件freshness，config区分committed/draft/实际偏差，使用固定兼容2020-12验证器做meta-schema和正反例；依赖 T059、T060；FR-011、FR-012、FR-027、FR-029。
- [ ] T062 [US6] 在 `internal/cli/api.go`、`internal/cli/json.go` 实现有界单JSON请求与唯一响应信封、strict params/版本/重复key/argv一致性，request_id≤128且坏请求可null；机器stdout不混进度/提示/应用IO，仅allowlist脱敏stderr，不回显拒绝正文；依赖 T061；FR-011、FR-012、FR-030、SC-007。
- [ ] T063 [US6] 在 `internal/platform/ipc_linux.go`、`internal/control/access.go` 扩展T025已启用的私有Unix socket/peer身份校验到完整API访问矩阵，controller/metrics分别保护，明确loopback不等于同用户OS隔离；未授权读取前即拒绝，不建立持久读审计，不将首次保护延后至本任务；依赖 T062；FR-005、FR-030、SC-018。
- [ ] T064 [US6] 在 `internal/control/query.go`、`internal/control/doctor.go` 扩展T139最小只读用例至status/doctor/config/list/app/route完整版本结构，逐字遵循“只读请求的 request_id 仅关联响应，不建立持久操作或审计记录”；缺能力明确unsupported，缺事实null+原因而非零；依赖 T063；FR-026、FR-027、FR-029、SC-007。
- [ ] T065 [US6] 在 `internal/cli/status.go`、`internal/cli/config.go`、`internal/cli/route.go` 扩展T139已有入口并将人类query、config plan/apply与API接同一用例，回归T058真实首次旅程；预览纯本地不运行隔离核心校验，不因诊断修复/恢复/更新last_seen；依赖 T064；FR-012、FR-021、FR-026、FR-027。
- [ ] T066 [US6] 在 `internal/control/probe.go`、`internal/cli/probe.go` 实现显式`probe NODE_ID --subscription ID --url URL`及API subscription_id+node_id命名空间、timeout与范围/结果/时间；排除组级有副作用delay端点，probe不改选择/revision，不冒业务延迟；依赖 T065；FR-020、FR-028、FR-029。
- [ ] T067 [US6] 在 `internal/control/diagnostics.go`、`internal/cli/diagnostics.go` 实现脱敏排障导出预览/明确执行，区分管理/转发/采集故障及来源范围，正文/认证/完整环境不出包；完整计量历史/质量导出留US7/US9；依赖 T066；FR-030、FR-053、SC-018。
- [ ] T068 [US6] 执行 `tests/contract/api_v1_test.go`、`tests/integration/readonly_security_test.go` 的F17与非交互旅程，在 `docs/validation/us6-api-readonly.md` 留SC-007/SC-018及人类/机器同一恢复结果证据；交付P1仍不得宣称P2或全产品完成；依赖 T067；FR-011、FR-012、FR-026、FR-027、FR-028、FR-029、FR-030、SC-007、SC-018。

## Phase 9: US7 — P2 同源账本、历史查询与保留（Priority P2）

**目标**：唯一字节事实驱动进程/域名投影，具备精确提交、真实归属、固定版本查询、基础窗口字节/速率及确认后自动清理。

**独立验收**：预登记两同名进程/两域名/子孙/PID复用/100短流100%正确；重放零重计；单行分页/Top1总计一致；确认前零删、确认后自动清理且不把缺口填零；同cutoff/window基础bytes、上传/下载B/s、同名聚合与速率排序参考一致，粒度或精度不足明确不可用。

**阶段入口**：T019唯一驱动ADR、T020相关规模/权限/清理门槛、T068 P1 API与G01/G02均满足；不合格前不得生产账本定稿。测试定义先于对应实现；真实执行在实现与明确依赖就绪后进行。

生产事件传输另由T138实现；它在T071最小存储建立后执行，先于T072消费来源。接收持久ACK与SQL消费提交分开，端到端回收安全由T073实际事务及T088故障矩阵最终证明；不把P0不回收日志直接搬进生产。

- [ ] T138 [US7] 在外部核心补丁的 `component/hoopatrace/`、`internal/metering/source_runtime.go`、`internal/ledger/receipt.go` 及 `tests/integration/source_transport_test.go` 实现有界生产source spool→持久接收journal的认证、严格事件解析、稳定重放/游标、段/索引上限和回收；来源只在有可恢复接收副本的持久ACK后回收，接收journal只在对应SQL消费位置已持久提交后回收，接收ACK不冒充SQL提交；GC前须确保对应manifest、原帧，以及（推导pending/gap必需的来源结束证据或等价已持久稳定恢复事实）均已有可恢复接收副本，不能只凭字节帧ACK删除唯一推导材料；具体转运格式在本任务设计并冻结。此任务先验证来源/接收两端及消费提交接口的正负故障，SQL联动由T073/T088补验；确认不确定时保留最后副本，满盘拒绝新计量、旧流不改路，不清空坏尾。核心单writer负责来源段回收，Hoopa不直接改写/删除核心活动日志；以版本化持久ACK协调，接收journal由自身writer回收。若生成新核心/协议制品，在本任务内追加不可变manifest/CapabilityRecord，由主代理按真实diff裁决G01/G02/G03/G05/G07最小保护的受影响范围、完成必要回归并解释未受影响证据的引用；旧hash的passed不得直接赋给新hash，缺少本次制品/能力结论时本任务不得完成，不反向依赖或重勾旧T137。明确旧世代/保留检查点/reader与writer/段回收锁顺序，复杂语义由主代理裁决；对应F05/F06/F24/G02/G07；完整身份/来源模式的当前核心必须承接T026同等closed-start、认证activate/drain和完整snapshot契约，不能直接沿用立即开放的旧guard；冻结bootstrap/BPF/FD handoff闭包及所需平台前提，核心与该链的受影响验证在本任务完成，产品运行接线由T077负责；依赖 T071、T017、T020；FR-004、FR-010、FR-030、FR-031、FR-033、FR-037、FR-039、SC-008、SC-018。

- [ ] T069 [P] [US7] 在 `internal/ledger/commit_test.go` 定义F05/F06/F07/F16：同epoch/event_id去重、source_seq|null与receipt_seq独立位置、累计checkpoint算delta及final不重复计入、checkpoint/delta/gap同进退、commit不确定重查、epoch下降/兼容series消失/磁盘失败/溢出；超出数据库/算术范围时报INTEGRITY_ERROR，不环绕或截零；依赖 T068、T019、T020；FR-033、FR-037、FR-039、SC-008。
- [ ] T070 [P] [US7] 在 `tests/integration/history_test.go`、`tests/integration/retention_test.go`、`internal/ledger/window_test.go`、`tests/contract/usage_v1_test.go` 定义F15、F20、F21、F22、F23、F24、F25、C02与SC-008/SC-009/SC-010真值/分页/UTC边界/清理竞争矩阵，按connect_initiator_v1登记建连快照及父connect/子写、FD共享/转交与原connector未知真值，旧失败不重评；测试样本不随失败调整；包含新核心仍引用旧hash能力或缺少复验记录时拒绝接流的反例；先定义usage.query及历史connections.query与CLI在同一快照/过滤下的结果和错误一致性；再定义基础窗口同cutoff/window字节与上传/下载B/s、同名先汇总同窗字节、缺口/粒度或精度不足不可用及速率排序参考断言，含F27窗口场景；依赖 T068、T019、T020；FR-011、FR-012、FR-032、FR-034、FR-035、FR-036、FR-038、FR-040、FR-043、FR-044、FR-045、SC-008、SC-009、SC-010、SC-012。
- [ ] T071 [US7] 在 `internal/ledger/open.go`、`internal/ledger/schema.sql` 实现G06选定的唯一驱动与最小实际表/索引/显式版本迁移，单写入者、本地WAL/外键/同步参数按ADR；不把网络/核心启动/fsync放SQL长事务，DB可选不污染基础管理；依赖 T069、T070；FR-002、FR-033、FR-039。
- [ ] T072 [US7] 在 `internal/metering/source.go`、`internal/metering/normalize.go` 仅消费G01/G02与ADR 005已验收且明确版本的 `contracts/telemetry-events.md` 来源profile（旧v1不自动升级为connector/ready能力），且当前制品须经T138重新冻结/必要复验并取得匹配path/hash、平台、协议/边界的能力记录；缺失/不符明确拒绝，经T138持久接收路径保留source实例/epoch、flow/attempt、event_id、source_seq|null及独立receipt_seq，不能用接收位置补来源位置；精确整数/定义/原始证据不失真；mihomo_proxy_stream_v1与旧gost_ingress_wire_v1分源分边界，完整标签series仅用于兼容对照；管理器重启不生成新epoch，归零需证据或gap，首样本仅立已证位置；依赖 T071、T017、T138；FR-031、FR-033、FR-037。
- [ ] T073 [US7] 在 `internal/ledger/commit.go` 实现同一事务提交checkpoint、delta、gap/reconciliation与data_version；派生恢复事实按原source/epoch/flow/opened及事实kind的稳定reconciliation_id幂等，同ID只比较稳定事实payload、冲突拒绝，不将重扫观测时刻差异当冲突，不造来源/接收序号或额外字节；source_seq与receipt_seq独立存储，按同源累计checkpoint计算delta，closed/final不额外叠加；commit成功才推进消费确认位置，不确定时按稳定epoch/event_id查询既有结果；接通T138消费提交接口，实际故障验证SQL未提交/提交不确定时接收journal不回收，重开按原ID恢复后才确认回收资格；兼容series缺失不当0，无解释下降停止推进，精确求和检测SUM溢出；依赖 T072；FR-033、FR-037、FR-039。
- [ ] T074 [US7] 在 `internal/metering/process.go`、`internal/ledger/attribution.go` 保存connect_initiator_v1的SourceProcessInstance含boot/PID/start、建连name/path/user/parent快照、字段来源/状态和backend_generation；已连接FD共享/转交不改connector，未监测转交标unverified，exec/改名不覆盖建连快照；补充归属不复制 bytes、不重置事实年龄，不以GOST/client标签/launch猜源；依赖 T073；FR-034、FR-036、FR-041、SC-008。
- [ ] T075 [US7] 在 `internal/metering/target.go`、`internal/ledger/connections.go` 保留短连接终末生命周期与原生ID来源世代，TargetEvidence保留scope(connection/tunnel/request)和evidence_version，遵循“domain/IP-only/unknown 互斥，大小写/IDNA/末尾点规范化”，共享IP/DNS/近时间不猜配、不复制不可分段流量；依赖 T074；FR-035、FR-036、SC-008。
- [ ] T076 [US7] 在 `internal/ledger/reconcile.go`、`internal/metering/gaps.go` 分离字节未知/进程未知/域名未知/开销/缺口，状态“pending/not_comparable/balanced/error 分开”，仅同方向/边界/epoch/截止点对账，gap不塞已知等式、不比例摊分或负差截零；依赖 T075；FR-037、FR-038、FR-039、SC-009。
- [ ] T077 [US7] 在 `internal/runtime/entry.go`、`internal/runtime/lease.go` 实现ADR 005已验收的原生计量入口/认证或进程登记关联，不要求GOST必经；保持reserved→ready→draining→released及证据不足unknown，unknown仍占入口；ready前再次核对实际path/hash、平台、协议/边界及T138确认的能力记录，缺失/不符拒绝新计量，不继承旧hash的passed；完整身份/计量模式须在本任务接入已固定Python/bootstrap、BPF/libbpf与scope/FD handoff，在最外层先持久birth并关闭继承目录，同PID启动后以原pidfd/同连接证明完成登记，再认证activate；缺环境/能力不得回退basic或立即开放。实际验证新mode的管理器退出、同实例接管、启动部分失败/清理和未知拒绝，并重用T034受影响断言，基本模式不因此依赖BPF。再确认访问保护、初始checkpoint持久，且来源能在接流前持久记录opened/durable census；共享profile不推导launch，不能证明则launch_unknown；不隐式重载或降级成未计量，新完整计量记录能力不足明确失败；依赖 T076；FR-008、FR-031、FR-032、FR-039。
- [ ] T078 [US7] 在 `internal/control/metering.go` 接入计量只影响后续启动，旧lease/run/连接保持；采集故障转发继续且显stale/gap，有界队列溢出/不可重放尾段分维度缺口，不先推进位置；依赖 T077；FR-032、FR-037、FR-039。
- [ ] T079 [US7] 在 `api/v1/usage-response.schema.json`、`api/v1/history-response.schema.json` 补全代表usage未覆盖的路径/用户、连接数口径（当前活动/窗口新建/历史去重明确区分）、五视图、双向下钻/历史与导出业务字段，以及基础窗口的cutoff/window、上传/下载窗口bytes、upload_rate_bytes_per_second/download_rate_bytes_per_second（B/s）、覆盖/新鲜度和不可用原因，字节pattern=`^(0|[1-9][0-9]*)$`；“未知为 null 并带原因”，null不得complete，partial/unknown必有reason；固定验证器检验meta-schema与实例；依赖 T078；FR-011、FR-036、FR-041、FR-042、FR-043、FR-044、FR-045、FR-053、SC-012。
- [ ] T080 [US7] 在 `internal/ledger/snapshot.go` 实现“第一页固定这些值，后续页保持同一逻辑视图；排序使用稳定 ID 作最终 tie-break”，固定data_version/attribution_version/retention_generation/cutoff/filter/sort/grouping，连接生命周期/归属也按版本取；过期SNAPSHOT_EXPIRED不静默续页；依赖 T079；FR-043、FR-044、SC-009。
- [ ] T081 [US7] 在 `internal/ledger/query.go`、`internal/ledger/views.go`、`internal/ledger/window.go` 实现process/name/domain/process_domain/launch_tree与路径用户展开，同名“仅影响展示”不改源事实，未知/退出默认保留；11filters交集，PID须process_instance_id、node_id须subscription_id，protocol tcp/udp/quic未支持明确capability；在同一查询快照/cutoff/window下实现基础窗口bytes与上传/下载B/s，先精确汇总同窗字节再除窗口，注明单位/覆盖/新鲜度；缺口不填0、不插值，粒度或精度不足明确不可用，不平均行速率，必须先于T082速率排序；依赖 T080；FR-034、FR-035、FR-041、FR-042、FR-044、FR-045、SC-009、SC-012。
- [ ] T082 [US7] 在 `internal/ledger/order.go` 支持total/upload/download、upload_rate_bytes_per_second/download_rate_bytes_per_second、connection_count/name/pid/last_active_at/latency_p50_ms/latency_p95_ms/failure_rate稳定升降序，缺值末尾；速率需window_seconds、质量排序需definition+target（质量实现前unsupported），page_size默认100且1–1000，全量/visible/hidden/unknown与TopN保留；依赖 T081；FR-042、FR-043、FR-044、FR-045、SC-009。
- [ ] T083 [US7] 在 `internal/ledger/readonly.go` 实现服务在时无持久副作用读取，否则只安全打开已有账本；“只读打开条件不足返回 READ_UNAVAILABLE，不创建 WAL/SHM、不恢复或迁移”，不得活跃WAL immutable=1或坏库换空库；依赖 T082；FR-026、FR-039、FR-044、SC-007。
- [ ] T084 [US7] 在 `internal/control/retention.go` 接入retention默认enabled=false/days=30，正整数days；“初始不自动删除，启用需确认”，启用/缩短plan/apply披露丢失范围后前进global revision；禁用不承诺无限容量，延长不恢复；依赖 T083；FR-021、FR-025、FR-040、SC-010。
- [ ] T085 [US7] 在 `internal/ledger/cleanup.go` 实现CleanupRun pending/running/completed/failed及ExpiredRange from/to、删除范围/质量、observed_deleted_at、policy_revision，按当前已确认days计算UTC闭区间`[T−days×24h,T]`，30天仅是默认参数；遵循“边界等于起点保留”“原始事实观察时间决定年龄，归属修订/迟到导入不续期”；短批核对policy_revision，保留checkpoint/活跃引用/gap/ExpiredRange，推进retention_generation；跨界不比例分配；依赖 T084；FR-040、FR-044、SC-010。
- [ ] T086 [US7] 在 `internal/control/maintenance.go` 按“控制写入权 → 账本写入者/短 SQL 事务”“账本写入者不得持 SQL 事务反向等待控制锁”执行已确认自动清理；采集提交不拿控制锁，查询不清理，服务暂停pending/恢复有界补删，不安装系统任务；依赖 T085；FR-005、FR-026、FR-039、FR-040、SC-010。
- [ ] T087 [US7] 在 `internal/cli/usage.go`、`internal/cli/connections.go`、`internal/ledger/export.go`、`internal/control/usage.go`、`internal/control/history.go` 和 `internal/cli/api.go` 接入五视图/历史/导出及usage.query、历史connections.query版本化JSON分发；CLI/API调用同一查询用例，并实际返回T081的基础窗口bytes、上传/下载B/s、cutoff/window、覆盖/新鲜度与不可用原因；包含筛选窗口/计数口径/版本/覆盖/总计与已删不可用区间；导出脱敏且不因查询启动采集/修复；依赖 T086；FR-011、FR-012、FR-031、FR-041、FR-042、FR-043、FR-044、FR-045、FR-053、SC-012。
- [ ] T088 [US7] 执行 `internal/ledger/commit_test.go`、`internal/ledger/window_test.go`、`tests/contract/usage_v1_test.go`、`tests/integration/source_transport_test.go`、`tests/integration/history_test.go`、`tests/integration/retention_test.go` 的F05、F06、F07、F15、F16、F17、F20、F21、F22、F23、F24、F25、F27、C02，在 `docs/validation/us7-history.md` 核验100短流、单行分页/Top1、查询零副作用、确认前零删及失败恢复，并实际调用JSON API和CLI，核对同一快照/过滤/分页/TopN的字段、全量总计和错误一致性；核对新hash缺证据或不匹配时拒绝新计量；在US7验收同cutoff/window基础bytes、B/s、同名聚合、覆盖/新鲜度、缺口/粒度或精度不足不可用和速率排序参考结果；依赖 T087；FR-011、FR-012、FR-031、FR-032、FR-033、FR-034、FR-035、FR-036、FR-037、FR-038、FR-039、FR-040、FR-041、FR-042、FR-043、FR-044、FR-045、SC-008、SC-009、SC-010、SC-012。

## Phase 10: US8 — P2 路由前真实进程规则（Priority P2）

**目标**：在已验收Mihomo数据面的原始入口和规则执行位置使用源进程证据，事后归属不倒写；不新增逐连接依赖管理器的外部决策服务。

**独立验收**：实例/精确名称/路径与应用域名IP共序；父connect/子写及FD共享仍按已证connector，缺证据/关联多解和字段能力缺失拒绝正确，管理器死亡已发布规则仍执行。

**阶段入口**：T088 历史身份验收及T018/G01/G05/事件来源ADR成立；不能靠新增空adapter跳过门槛。测试定义先于对应实现；真实执行在实现与明确依赖就绪后进行。

- [ ] T089 [P] [US8] 在 `internal/policy/process_test.go` 定义F30、F31的实例/精确名称/路径、早匹配与后未知、父connect/子写及FD共享/转交保持connector的正例，原connector缺失/关联多解和分字段unsupported配置拒绝、不绑定裸PID断言；明确断言“all 中存在确定 false 则整体 false；全部 true 才为 true；其余为 unknown”“当前规则 unknown 且可能影响结果时拒绝连接”，更早确定匹配不受后续未知影响；依赖 T088；FR-049、FR-050、SC-011。
- [ ] T090 [P] [US8] 在 `tests/integration/process_routing_test.go` 定义F21、F30、F33路由前connector及建连名称/路径快照、FD共享/转交不重选和新连接exec/改名快照、管理器死后连续执行、迟到补全不改历史决策及direct/inherit未进入入口不强捕获断言；依赖 T088；FR-034、FR-049、FR-050、FR-051、SC-011。
- [ ] T091 [US8] 在 `internal/mihomo/process_policy.go` 实现G05/事件来源ADR已经裁决的编译与数据组件接入；若能力前提变化先回主代理ADR，不引入未批准特权/内核；不得每连接调用管理器RPC或ledger SQL，缺所需身份拒绝，能力不支持拒绝配置；依赖 T089、T090；FR-005、FR-049、FR-050、FR-051。
- [ ] T092 [US8] 在 `internal/metering/route_decision.go` 保存当时可证身份/命中规则/配置世代与执行结果，区别后续AttributionRevision；长期条件仅实例/精确建连名称/路径，父只有真实connect证据才可作为connector，不以父关系/转发器代填，不倒写过去；依赖 T091；FR-049、FR-050、FR-051、SC-011。
- [ ] T093 [US8] 在 `internal/control/process_rule.go` 通过同一plan/apply展示规则顺序、受影响入口/连接与unsupported原因；实际路由规则存有序rules，不新增profile隐式覆盖或全机接管；依赖 T092；FR-018、FR-021、FR-049、FR-050、FR-051。
- [ ] T094 [US8] 执行 `internal/policy/process_test.go`、`tests/integration/process_routing_test.go`，在 `docs/validation/us8-process-routing.md` 记录F21、F30、F31、F33完整矩阵，零冒用/隐式后备/事后倒写；依赖 T093；FR-049、FR-050、FR-051、SC-011。

## Phase 11: US9 — P2 质量、窗口与实际线路（Priority P2）

**目标**：提供可比较的测量、重算分位数与原连接真实链，复用US7已实现的基础窗口/速率完成质量与线路联查。

**独立验收**：受控慢请求/响应/超时/复用/HTTPS与两线路矩阵参考一致，无平均p95、缺值零填或恢复累计逐秒插值；联查与US7基础窗口的快照、过滤、cutoff/window和覆盖一致。

**阶段入口**：T094真实进程规则验收、T129及T014的G03同源flow/attempt计时/线路证据成立。测试定义先于对应实现；真实执行在实现与明确依赖就绪后进行。

- [ ] T095 [P] [US9] 在 `internal/ledger/quality_test.go`、`tests/contract/quality_v1_test.go` 定义F26、F27的可观察起止、成功/失败/超时/取消、定义不兼容及聚合参考样本，并校验QualitySample稳定sample_id、source_epoch、connection/attempt/request_id关联、observed_at、duration_ms|null、observed_wait_ms|null、actual_connection_ref|null、monotonic_start/end、definition_ref、ready_kind/protocol、failure_stage、route_evidence_ref、coverage/reason及outcome枚举；“outcome 区分 success/failure/timeout/cancelled/unknown”，不以连接成功代替业务成功；补充质量与T081既有窗口结果联查时同快照/过滤/cutoff/window一致的回归断言，不重写基础窗口计算，并定义usage.query/connections.query质量扩展与CLI同快照一致性；依赖 T094；FR-011、FR-012、FR-045、FR-046、FR-047、SC-012。
- [ ] T096 [P] [US9] 在 `tests/integration/route_history_test.go` 定义F28的两线路/切组前存量与新连接/改名/一进程多节点，保留原connection与generation关联及稳定node/provider身份，按ConnectionSegmentBytes/actual_n与同边界unallocated核对flow守恒、无Conn只selection_only/unavailable，now不能回填；依赖 T094；FR-048、SC-013。
- [ ] T097 [US9] 在 `internal/metering/quality.go` 保存QualityDefinition type/version/起止/source/protocol/ready_kind/target_scope/unit，逐字落实“QualitySample：sample_id、source_epoch、connection/attempt/request_id、definition_ref、observed_at、duration_ms|null、observed_wait_ms|null、actual_connection_ref|null、monotonic_start/end、ready_kind/protocol、outcome、failure_stage、route_evidence_ref、coverage/reason”；校验稳定样本/来源世代及原连接关联、observed_at为UTC RFC3339、持续时间单调时钟ms，outcome为success/failure/timeout/cancelled/unknown，缺失或不可见duration为null并保留reason，ready未证不抹已知失败/超时/取消，observed_wait独立于成功duration，所有分母明确；forward_setup从完整代理请求已解析目标已知到通道ready、含DNS，HTTPS/复用不可见为N/A；依赖 T095、T096；FR-046、FR-047。
- [ ] T098 [US9] 在 `internal/ledger/quality.go` 从同定义/ready_kind/protocol/目标的原样本或兼容可说明误差直方图重算p50/p95/min/max/count及各outcome分母、新鲜度，“不平均 p95”；质量排序明确definition+ready_kind+protocol+target，不可比保留原因；依赖 T097；FR-043、FR-046、FR-047、SC-012。
- [ ] T099 [US9] 在 `internal/control/quality.go` 复用T081已实现的 `internal/ledger/window.go` 基础窗口bytes及upload_rate_bytes_per_second/download_rate_bytes_per_second，完成与质量结果联查的快照/过滤/cutoff/window、覆盖/新鲜度和单位一致性；为后续T100/T102实际线路联查沿用同一查询上下文，不重新实现基础速率，不平均行速率、不把恢复累计补逐秒曲线；依赖 T098、T081；FR-043、FR-044、FR-045、SC-012。
- [ ] T100 [US9] 在 `internal/ledger/routes.go` 保存ConnectionSegmentBytes（segment_id、actual_connection_ref、flow/attempt、route_ref、direction/boundary、actual_n/interval、quality）并关联唯一ByteFact，分配不复制字节；保存原source/epoch、flow_id、attempt_id、connection_id、config_generation、rule_id、稳定actual_node_id/provider_id及显示名快照、logical_group、ordered_chain、evidence_source/time/completeness；connection_id须无歧义映射到原flow/attempt，重试各attempt的实际Conn/路线分开，无Conn仅selection_only/unavailable；同边界flow字节=已证连接段+unallocated，恢复总量无分段不挂最后节点；不可关联未知，两节点分布可展开，与当前now分开，观测吞吐不冒节点带宽上限；依赖 T099；FR-048、SC-013。
- [ ] T101 [US9] 在 `api/v1/quality-response.schema.json`、`api/v1/quality-response.fixtures.json` 补齐完整质量/速率/节点分布与导出结构并接usage/history响应，固定验证器跑正反例；不沿用代表usage结构声称已覆盖全部，单位显式bytes_per_second；依赖 T100；FR-011、FR-043、FR-045、FR-046、FR-047、FR-048、FR-053。
- [ ] T102 [US9] 在 `internal/control/quality.go`、`internal/cli/quality.go` 接入质量筛选/排序/线路下钻/导出，并扩展既有usage.query/connections.query的API校验与分发，共用control查询用例，快照绑定定义/目标/窗口与全部查询参数；查询不触发probe、自动组选优或改路；依赖 T101；FR-011、FR-012、FR-026、FR-042、FR-043、FR-045、FR-046、FR-047、FR-048、FR-053。
- [ ] T103 [US9] 执行 `internal/ledger/window_test.go`、`internal/ledger/quality_test.go`、`tests/contract/quality_v1_test.go`、`tests/integration/route_history_test.go` 的F26、F27、F28，在 `docs/validation/us9-quality.md` 核验同口径参考与两个实际链/分布，保留不可见N/A和失败分母，不美化排名，实际核对JSON API与CLI质量/线路字段及错误一致性，并回归质量/实际线路联查复用US7基础窗口后的同快照、cutoff/window与覆盖一致性；依赖 T102；FR-011、FR-012、FR-045、FR-046、FR-047、FR-048、SC-012、SC-013。

## Phase 12: US10 — P2 按需交互（Priority P2）

**目标**：TUI仅投影共享查询/计划/变更用例，不托管核心。

**独立验收**：相同版本/参数下CLI/API/TUI结果、影响与恢复一致；正常关闭或崩溃面板停止转发为零。

**阶段入口**：T103质量与所有共享用例稳定。测试定义先于对应实现；真实执行在实现与明确依赖就绪后进行。

- [ ] T104 [P] [US10] 在 `internal/tui/model_test.go` 定义共享快照/空缺/过期/degraded视图和键盘操作计划确认流，不建立第二份配置、查询不主动网络；依赖 T103；FR-026、FR-052、SC-014。
- [ ] T105 [P] [US10] 在 `tests/integration/tui_test.go` 定义F01、F02面板正常/异常关闭与CLI/API对照，错误/中断/恢复相同且长连接继续；依赖 T103；FR-012、FR-024、FR-052、SC-014。
- [ ] T106 [US10] 在 `internal/tui/model.go`、`internal/tui/update.go` 按需加入research固定Bubble Tea v2.0.8/Bubbles v2.2.1并核对go.mod；Model/Update调用共享control，用真实需要的小接口，不建事件总线；依赖 T104、T105；FR-002、FR-010、FR-052。
- [ ] T107 [US10] 在 `internal/tui/view.go`、`internal/cli/tui.go` 提供概览/订阅节点/规则/应用/用量/日志，保留unknown/stale/pending/degraded，变更必须同plan/apply确认；退出只结束界面；依赖 T106；FR-029、FR-052、SC-014。
- [ ] T108 [US10] 执行 `internal/tui/model_test.go`、`tests/integration/tui_test.go`，在 `docs/validation/us10-tui.md` 保存F01、F02与SC-014对照证据，基础旅程再次无TUI运行；依赖 T107；FR-002、FR-012、FR-024、FR-052、SC-014。

## Phase 13: US11 — P3 平台证据与可回退迁移（Priority P3）

**目标**：适用平台能力按证据声明，迁移先预览备份再显式执行。

**独立验收**：各正式支持项都有构建/回环/真实应用/退出休眠恢复/性能证据；预览零破坏，旧输入变化冲突，执行失败能核验旧态恢复，额外付费零。

**阶段入口**：T108交互与全基础功能验收；真实平台资源不足保留unverified不伪造。测试定义先于对应实现；真实执行在实现与明确依赖就绪后进行。

- [ ] T109 [P] [US11] 在 `tests/integration/platform_test.go` 定义Linux/macOS/Windows路径/IPC/锁/身份/信号/ACL与F01、F02、F03、F04、F14、F17、F19、F33适用矩阵，分别记passed/failed/unverified；依赖 T108；FR-024、FR-030、FR-054、FR-055、SC-015、SC-018。
- [ ] T110 [P] [US11] 在 `tests/integration/migration_test.go` 定义备份/差异/冲突/失败回退与旧输入被编辑场景，预览不写旧文件/抢端口/改系统设置、执行复核输入摘要，覆盖C01式旧输入拒绝；依赖 T108；FR-025、FR-056、SC-015。
- [ ] T111 [US11] 在 `internal/platform/process_darwin.go`、`internal/platform/process_windows.go`、`internal/platform/ipc_darwin.go`、`internal/platform/ipc_windows.go` 适配实际所需身份/ACL/信号/路径、Unix peer身份及接管边界；共享Unix实现时明确build tags和平台peer校验差异。同步验证外部计量核心/来源适配的各平台构建与实际能力；Linux-only来源保持不支持，禁止用launcher PID或空stub冒充能力。验证Windows Job不默认kill-on-close，不暗装系统服务/linger/登录任务；依赖 T109、T110；FR-005、FR-024、FR-030、FR-034、FR-054、SC-015、SC-018。
- [ ] T112 [US11] 在 `.github/workflows/ci.yml` 配置标准托管构建/单元/适用回环与Go格式/vet/test/适用race，固定依赖校验与最小并发/制品保留；仅现有免费额度，无额度标未验证，不用额外付费runner；依赖 T111；FR-010、FR-054、FR-055、SC-015。
- [ ] T113 [US11] 在 `internal/control/migration.go`、`api/v1/migration-plan.schema.json` 实现MigrationPlan输入摘要/差异/冲突/备份/恢复目标/影响确认/operation；预览严格只读、旧工具可继续运行，未知或敏感字段脱敏呈现；依赖 T112；FR-026、FR-030、FR-056、SC-015。
- [ ] T114 [US11] 在 `internal/control/migration_apply.go`、`internal/cli/migrate.go` 接入显式执行前旧输入复核，冲突零本次写入；备份完成才替换，失败恢复核验旧配置，沿用US5日志/确认/恢复，不另建跨资源原子事务；依赖 T113；FR-025、FR-056、SC-015。
- [ ] T115 [US11] 在 `docs/validation/platform-evidence.md` 执行真实Linux/macOS/Windows Agent、SSH/注销/休眠/恢复、身份与0/1/5入口性能矩阵，F01、F02、F03、F04、F14、F33分平台记录，不从CI/GOOS交叉编译推出正式支持；依赖 T114；FR-054、FR-055、SC-015、SC-016。
- [ ] T116 [US11] 执行 `tests/integration/platform_test.go`、`tests/integration/migration_test.go`，在 `docs/validation/us11-migration.md` 记录授权/无授权与预览/失败恢复/旧输入冲突及免费资源使用证据，未验证项目只可实验性声明；依赖 T115；FR-030、FR-054、FR-055、FR-056、SC-015、SC-018。

## Phase 14: Polish — 稳定交付与全产品验收

**目标**：汇总真实证据、性能、许可和首次用户可用性，不扩建架构。

**独立验收**：所有FR/SC及33类F和C01/C02有可重现结果，无关键失败；环境不足逐项保留，不能记全产品通过。

**阶段入口**：T116平台/迁移及此前故事完成，所有适用门槛已满足。测试定义先于对应实现；真实执行在实现与明确依赖就绪后进行。

- [ ] T117 [P] 在 `tests/integration/performance_test.go` 与 `docs/validation/performance.md` 对非空规则/节点、0/1/5入口、历史梯度实测管理器RSS≤40MiB、10分钟平均CPU<单核0.5%、整套≤200MiB、压缩包≤10MiB/程序≤25MiB及外部核心分列；按ADR查询规模预算报告，不丢明细达标；依赖 T116；FR-004、FR-039、FR-054、SC-016。
- [ ] T118 [P] 在 `THIRD_PARTY_NOTICES.md`、`docs/release.md` 核对源码MIT与固定模块/外部核心许可、版权与分发义务；不直接复制未核对上游实现，包中不含真实配置/账本/秘密，正式module地址只用真实已定地址；依赖 T116；FR-005、FR-010、FR-030。
- [ ] T119 在 `docs/validation/first-user.md` 请至少一名未参与实现验收者仅按帮助完成首次managed、切组预览和只读排障，修正文案于 `docs/usage.md`，不得要求编辑生成文件/内部状态；记录SC-017真实观察；依赖 T117、T118；FR-001、FR-006、FR-012、FR-026、SC-017。
- [ ] T120 执行 `tests/integration/full_acceptance_test.go` 的全矩阵归档到 `docs/validation/acceptance.md`，逐项审阅F01、F02、F03、F04、F05、F06、F07、F08、F09、F10、F11、F12、F13、F14、F15、F16、F17、F18、F19、F20、F21、F22、F23、F24、F25、F26、F27、F28、F29、F30、F31、F32、F33、C01、C02及全部SC；先组装已有真实测试入口再运行，不用mock替代身份/事务/平台证据；依赖 T119；FR-003、FR-004、SC-001、SC-002、SC-003、SC-004、SC-005、SC-006、SC-007、SC-008、SC-009、SC-010、SC-011、SC-012、SC-013、SC-014、SC-015、SC-016、SC-017、SC-018。
- [ ] T121 在 `README.md`、`specs/001-hoopa-proxy-manager/quickstart.md` 更新真实可用命令、兼容/限制、证据链接及未验证平台；执行go fmt/vet/test和适用race及固定schema验证器/链接检查，以 `docs/validation/release-checks.md` 记录真实命令结果，失败/未运行不得称通过；依赖 T120；FR-001、FR-004、FR-011、FR-054、SC-017、SC-018。

## Dependencies & Execution Order

|阶段|入口与退出|说明|
|---|---|---|
|Setup — 最小工程输入|T001 → T003|按各任务inline依赖；阶段入口见本节前对应phase|
|Foundational — 最小 P0 前提|T004 → T007|按各任务inline依赖；阶段入口见本节前对应phase|
|US1 — P0 组合证据（Priority P1，MVP）|T008 → T021，含下行整改分支|原组合历史/子断言保留；按新候选证据关闭门槛|
|P0整改R1 — 同源事件与接收持久化|T010 → T122–T126；新候选正式矩阵T127 → T128 → T129|T127之前须满足下行R2；T129后T012–T014/T017复核，再由T018裁决|
|P0整改R2 — 前提与能力补全|T126 → T130 → T131 → T132–T135 → T137；T136消费T137；T140消费T132/T135/T136/T137，T127在T140之后|T132必须有正向身份；T133–T135仅独立P0子实验可分开；T136先于T015/T016|
|US5 — P1 生命周期、精确应用与恢复（Priority P1）|T022 → T034|按各任务inline依赖；阶段入口见本节前对应phase|
|US3 — P1 来源、稳定组与编译（Priority P1）|T035 → T044|按各任务inline依赖；阶段入口见本节前对应phase|
|US4 — P1 有序应用选路（Priority P1）|T045 → T051|按各任务inline依赖；阶段入口见本节前对应phase|
|US2 — P1 首次配置与子进程接入（Priority P1）|T051 → T139 → T052–T058|按各任务inline依赖；阶段入口见本节前对应phase|
|US6 — P1 只读、JSON v1 与访问保护（Priority P1）|T059 → T068|按各任务inline依赖；阶段入口见本节前对应phase|
|US7 — P2 同源账本、历史查询与保留（Priority P2）|T069/T070 → T071 → T138 → T072–T088|按各任务inline依赖；阶段入口见本节前对应phase|
|US8 — P2 路由前真实进程规则（Priority P2）|T089 → T094|按各任务inline依赖；阶段入口见本节前对应phase|
|US9 — P2 质量、窗口与实际线路（Priority P2）|T095 → T103|按各任务inline依赖；阶段入口见本节前对应phase|
|US10 — P2 按需交互（Priority P2）|T104 → T108|按各任务inline依赖；阶段入口见本节前对应phase|
|US11 — P3 平台证据与可回退迁移（Priority P3）|T109 → T116|按各任务inline依赖；阶段入口见本节前对应phase|
|Polish — 稳定交付与全产品验收|T117 → T121|按各任务inline依赖；阶段入口见本节前对应phase|

核心DAG：Setup/Foundational → T010/整改R1 → T130/T131前提裁决 → T132–T135最小能力 → T137冻结候选/T136控制harness → T140 source5程序规则consumer → T127–T129正式矩阵及T012–T017证据/安全裁决 → US1的T018（G01–G05关键可行性及T015的G07最小保护）→ US5 → US3 → US4 → US2 → US6 → US7 → US8 → US9 → US10 → US11 → Polish。
US1的T019（驱动）和T020（规模/清理/权限）可在DB-free P1期间继续；它们不阻塞US5，却明确阻塞US7入口。US1全部完成才是完整P0/MVP完成；不能因为P1可推进就把G06/G07勾为通过。G07的基础权限/入口安全在T015真实验证并由T018裁决，不能延后到P1；只有T019驱动定稿与T020历史规模/多入口预算细化可继续。P2门槛明确为T019+T020+T068及相关来源ADR通过。

核心门槛或adapter前提失败：先暂停受影响任务，主代理修具体ADR/边界并重跑受影响G验证，更新依赖证据；不能将字段存在、mock或全部unknown当作可行，也不能擅自引入权限/核心fork。只有已满足自身阶段前置的独立工作可继续；T018未通过时不得据此启动下游DB-free P1。复杂事务/锁/恢复变更必须由主代理裁决。

## Parallel Examples（真实文件所有权与波次）

每一行两项任务只在其相同前置全部完成后启动，分别独占各任务列出的文件；运行E2E实验不包括在这些并行编辑波次内。真实实验默认串行，若另行批准并行，必须各run私有沙箱、端口、状态和证据目录，不能共享进程/账本。

|波次|任务对|前置|文件独占|
|---|---|---|---|
|Setup|T002 ∥ T003|T001|依各任务明确文件集合分配两个执行者，不交换共享文件写权|
|Foundational|T004 ∥ T005|T002、T003|依各任务明确文件集合分配两个执行者，不交换共享文件写权|
|US1|T008 ∥ T009|T007|依各任务明确文件集合分配两个执行者，不交换共享文件写权|
|US5|T022 ∥ T023|T018|依各任务明确文件集合分配两个执行者，不交换共享文件写权|
|US3|T035 ∥ T036|T034|依各任务明确文件集合分配两个执行者，不交换共享文件写权|
|US4|T045 ∥ T046|T044|依各任务明确文件集合分配两个执行者，不交换共享文件写权|
|US2|T052 ∥ T053|T139|依各任务明确文件集合分配两个执行者，不交换共享文件写权|
|US6|T059 ∥ T060|T058|依各任务明确文件集合分配两个执行者，不交换共享文件写权|
|US7|T069 ∥ T070|T068、T019、T020|依各任务明确文件集合分配两个执行者，不交换共享文件写权|
|US8|T089 ∥ T090|T088|依各任务明确文件集合分配两个执行者，不交换共享文件写权|
|US9|T095 ∥ T096|T094|依各任务明确文件集合分配两个执行者，不交换共享文件写权|
|US10|T104 ∥ T105|T103|依各任务明确文件集合分配两个执行者，不交换共享文件写权|
|US11|T109 ∥ T110|T108|依各任务明确文件集合分配两个执行者，不交换共享文件写权|
|Polish|T117 ∥ T118|T116|依各任务明确文件集合分配两个执行者，不交换共享文件写权|

## Requirement Coverage Index

每行任务ID来自具体清单中的FR/SC引用；不是仅用下表宣称覆盖。边界测试与真实证据均须完成，需求主责不被最终汇总任务代替。

|需求|任务|
|---|---|
|FR-001|T001、T011、T021、T033、T058、T119、T121、T139|
|FR-002|T021、T022、T033、T046、T052、T058、T071、T106、T108|
|FR-003|T006、T007、T008、T009、T010、T011、T012、T013、T014、T015、T016、T017、T018、T019、T020、T021、T120、T122、T123、T124、T125、T126、T127、T128、T129、T130、T131、T132、T133、T134、T135、T136、T137、T140|
|FR-004|T002、T004、T006、T010、T017、T018、T019、T020、T021、T117、T120、T121、T125、T126、T130、T131、T137、T138|
|FR-005|T003、T005、T017、T063、T086、T091、T111、T118、T126、T130、T137|
|FR-006|T037、T054、T057、T058、T119、T139|
|FR-007|T052、T053、T055、T056、T057、T058|
|FR-008|T052、T053、T055、T056、T058、T077|
|FR-009|T011、T053、T056、T057、T058|
|FR-010|T001、T003、T010、T026、T037、T054、T106、T112、T118、T126、T137、T138|
|FR-011|T027、T029、T059、T061、T062、T068、T070、T079、T087、T088、T095、T101、T102、T103、T121|
|FR-012|T033、T043、T057、T059、T061、T062、T065、T068、T070、T087、T088、T095、T102、T103、T105、T108、T119、T139|
|FR-013|T007、T035、T037、T038、T039、T041、T044|
|FR-014|T036、T040、T041、T043、T044|
|FR-015|T026、T035、T038、T040、T041、T044|
|FR-016|T036、T038、T042、T043、T044|
|FR-017|T036、T042、T043、T044|
|FR-018|T016、T045、T047、T048、T049、T051、T093、T135|
|FR-019|T016、T046、T047、T048、T050、T051、T056、T135|
|FR-020|T016、T035、T039、T041、T045、T047、T051、T066|
|FR-021|T028、T030、T049、T050、T051、T065、T084、T093、T139|
|FR-022|T016、T022、T023、T024、T026、T029、T030、T032、T034、T041、T046、T050、T135、T136|
|FR-023|T016、T023、T031、T034、T136|
|FR-024|T015、T022、T025、T032、T033、T034、T105、T108、T109、T111、T136|
|FR-025|T016、T023、T024、T025、T027、T028、T029、T030、T031、T032、T034、T043、T059、T084、T110、T114、T136|
|FR-026|T019、T028、T049、T054、T060、T064、T065、T068、T083、T086、T102、T104、T113、T119、T139|
|FR-027|T045、T049、T061、T064、T065、T068|
|FR-028|T040、T066、T068|
|FR-029|T032、T060、T061、T064、T066、T068、T107、T139|
|FR-030|T002、T005、T010、T015、T020、T024、T025、T026、T035、T037、T038、T057、T060、T062、T063、T067、T068、T109、T111、T113、T116、T118、T135、T136、T138|
|FR-031|T072、T077、T087、T088、T138|
|FR-032|T053、T056、T070、T077、T078、T088、T135|
|FR-033|T004、T009、T013、T019、T069、T071、T072、T073、T088、T122、T124、T128、T133、T138|
|FR-034|T007、T008、T012、T017、T048、T070、T074、T081、T088、T090、T111、T123、T125、T126、T127、T130、T131、T132、T137、T140|
|FR-035|T007、T012、T070、T075、T081、T088、T127、T140|
|FR-036|T008、T012、T070、T074、T075、T079、T088、T127、T132、T140|
|FR-037|T013、T069、T072、T073、T076、T078、T088、T128、T133、T138|
|FR-038|T009、T013、T070、T076、T088、T128、T133|
|FR-039|T013、T019、T069、T071、T073、T076、T077、T078、T083、T086、T088、T117、T122、T124、T128、T133、T138|
|FR-040|T020、T070、T084、T085、T086、T088|
|FR-041|T074、T079、T081、T087、T088|
|FR-042|T079、T081、T082、T087、T088、T102|
|FR-043|T070、T079、T080、T082、T087、T088、T098、T099、T101、T102|
|FR-044|T070、T079、T080、T081、T082、T083、T085、T087、T088、T099|
|FR-045|T014、T070、T079、T081、T082、T087、T088、T095、T099、T101、T102、T103、T134|
|FR-046|T009、T014、T095、T097、T098、T101、T102、T103、T125、T129、T134、T137|
|FR-047|T014、T095、T097、T098、T101、T102、T103、T129、T134|
|FR-048|T009、T014、T096、T100、T101、T102、T103、T125、T129、T134、T137|
|FR-049|T089、T090、T091、T092、T093、T094、T129、T131、T132、T135|
|FR-050|T016、T017、T045、T047、T089、T090、T091、T092、T093、T094、T123、T126、T127、T129、T131、T132、T135、T137、T140|
|FR-051|T017、T046、T090、T091、T092、T093、T094、T125、T126、T129、T131|
|FR-052|T104、T105、T106、T107、T108|
|FR-053|T067、T079、T087、T101、T102|
|FR-054|T019、T109、T111、T112、T115、T116、T117、T121、T130|
|FR-055|T109、T112、T115、T116|
|FR-056|T110、T113、T114、T116|
|SC-001|T008、T009、T011、T013、T015、T018、T021、T120、T128、T133、T137|
|SC-002|T035、T036、T039、T040、T044、T120|
|SC-003|T052、T053、T055、T058、T120、T139|
|SC-004|T016、T045、T046、T048、T050、T051、T120、T135|
|SC-005|T015、T016、T022、T023、T025、T029、T030、T031、T034、T120、T136|
|SC-006|T036、T042、T044、T120|
|SC-007|T059、T060、T062、T064、T068、T083、T120、T139|
|SC-008|T007、T008、T012、T069、T070、T074、T075、T088、T120、T123、T127、T130、T131、T132、T133、T137、T138|
|SC-009|T070、T076、T080、T081、T082、T088、T120|
|SC-010|T020、T070、T084、T085、T086、T088、T120|
|SC-011|T089、T090、T092、T094、T120、T127、T129、T131、T132、T135|
|SC-012|T014、T070、T079、T081、T087、T088、T095、T098、T099、T103、T120、T129、T134|
|SC-013|T014、T096、T100、T103、T120、T129、T134|
|SC-014|T104、T105、T107、T108、T120|
|SC-015|T109、T110、T111、T112、T113、T114、T115、T116、T120、T130|
|SC-016|T020、T115、T117、T120|
|SC-017|T018、T021、T119、T120、T121|
|SC-018|T015、T020、T025、T060、T063、T067、T068、T109、T111、T116、T120、T121、T135、T136、T138|

## Fault / Gate / Clarification Coverage

本表完整列出各具体任务行中该编号的实际引用，统一排除仅汇总证据的T021和T120，其他定义、实现、执行和门槛任务均按相同规则收录；F01–F33 原定义保持 [prototype.md](../../docs/prototype.md)，G定义保持research，C01/C02保持plan。最终汇总不取代专门矩阵任务。

|编号|具体定义/执行任务|
|---|---|
|F01|T015、T022、T034、T105、T108、T109、T115|
|F02|T015、T022、T034、T105、T108、T109、T115|
|F03|T015、T022、T034、T109、T115|
|F04|T015、T022、T034、T109、T115|
|F05|T013、T069、T088、T128、T133、T138|
|F06|T013、T022、T034、T069、T088、T128、T133、T136、T138|
|F07|T019、T069、T088|
|F08|T016、T035、T036、T044|
|F09|T016、T035、T036、T044|
|F10|T016、T023、T034、T135、T136|
|F11|T016、T023、T034、T135、T136|
|F12|T016、T023、T034、T136|
|F13|T016、T023、T034、T136|
|F14|T015、T022、T034、T109、T115、T136|
|F15|T053、T058、T070、T088|
|F16|T009、T013、T069、T088、T128|
|F17|T019、T020、T060、T068、T088、T109、T139|
|F18|T022、T034、T052、T058|
|F19|T052、T053、T058、T109|
|F20|T008、T012、T070、T088、T127、T132|
|F21|T008、T012、T070、T088、T090、T094、T127、T132|
|F22|T008、T012、T070、T088、T127、T132|
|F23|T070、T088|
|F24|T009、T013、T070、T088、T128、T133、T138|
|F25|T070、T088|
|F26|T009、T014、T095、T103、T129、T134|
|F27|T009、T014、T070、T088、T095、T103、T129、T134|
|F28|T009、T014、T096、T103、T129、T134|
|F29|T016、T046、T051|
|F30|T016、T089、T090、T094、T129、T131、T132、T135|
|F31|T016、T045、T051、T089、T094|
|F32|T016、T035、T044、T045、T051|
|F33|T016、T046、T051、T090、T094、T109、T115、T136|
|G01|T008、T012、T017、T018、T072、T123、T127、T130、T131、T132、T137、T138|
|G02|T009、T013、T017、T018、T072、T122、T124、T128、T133、T138|
|G03|T009、T014、T017、T018、T125、T129、T134、T137、T138|
|G04|T015、T018、T025、T136|
|G05|T016、T017、T018、T026、T048、T091、T125、T127、T129、T131、T132、T135、T136、T137、T138|
|G06|T019、T071|
|G07|T015、T018、T020、T135、T136、T138|
|C01|T016、T023、T034、T110、T136|
|C02|T020、T070、T088|

## Implementation Strategy

1. MVP = Setup + 最小Foundational + US1全部P0证据与裁决；不宣称已交付完整管理器，也不自动部署/发布。
2. 按同优先级真实依赖完成US5→US3→US4→US2→US6，在每故事独立fixture/usecase或真实旅程验收后集成。独立可验收不等于没有前置、可以同时实施。
3. P1完成仅为无DB基础管理。G06/G07及相关来源能力明确通过后才能US7生产账本；再按US8→US9→US10完成P2，最后US11/P3及全产品验收。
4. 每任务实施后只做相关验证与常规修复；新增证据才扩大检查。同一失败两种有效尝试仍不解、前提变化、范围扩大或事务/恢复不清时回主代理。
5. 计划约束与最终验收必须一起保留；不以提前勾选、弱化测试、删真值样本、未知当零或代表Schema冒充完整交付。

## Story Counts & Acceptance Summary

各故事独立验收标准见对应phase；总计包含Setup/Foundational/Polish，不把最终汇总任务重复计数。

|类别|任务数|
|---|---|
|US1|30|
|US5|13|
|US3|10|
|US4|7|
|US2|8|
|US6|10|
|US7|21|
|US8|6|
|US9|9|
|US10|5|
|US11|8|
|Setup/Foundational/Polish|12|
|总计|139|

## Notes / 本次任务记录

- 已完成：实施 T001–T011，包含最小工程、精确标量、私有沙箱、证据 writer/故障钩子、合成真值、身份/流量 oracle、固定两核 harness 与真实 Codex 正常/持续工作流；[运行证据](../../docs/validation/p0-workflow.md)与机器摘要（本地原件 `../../docs/validation/p0-2026-10-22.summary.json`，不随Git上传）已记录。应用响应/exit0不代表wrapper信号或SSH/休眠验收。
- 任务同步：保留原129个ID，追加T130–T139至139项；T012–T016改为同源候选取证/复核并接T127–T129，T017区分分支启动与完成，T069/T072/T073/T077/T100明确事件序号、计数边界、durable opened和flow/attempt；未重新生成清单。
- 追加完成：T122–T126，含接收journal、限定身份探针取证、真实recorder持久重开、同源路线/契约及隔离可运行候选。T126仅证明候选及审查材料已取得，不代表G01–G05通过。截至该阶段总任务139，已完成18；新增T130/T131只关闭诊断/裁决，不提升身份能力。
- 已完成（此前文档）：全计划复核、发现修订及静态检查。T127已执行100预登记短流，身份断言失败，完整身份矩阵未完成；T012–T017仅取得部分证据，保持未勾选。T128/T129依赖T127而未启动，其余未勾选任务仍未完成，没有后台继续实施P1。
- 本次实施历史：T130/T131取证/裁决已完成；当时T132缺正向唯一owner证明，不重跑原100（后续按本节末已确认connector口径推进）。T133已补checkpoint/正常marker、`source_event.go`/`receiver_linux.go`严格接收、`source_lifecycle.go`及`cmd/sourceinspect/`只读pending恢复、两个显式Python探针和独立002补丁；真实CONNECT/预读/SOCKS5、SIGTERM/SIGKILL、503与ACK丢失重投有部分通过证据。核心重开完整形状校验/来源结束证据与派生恢复报告/lazy-handshake与完整故障仍未完，T133不勾选，详见[实际结果](../../docs/validation/source-lifecycle.md)。
- 较早收敛方案（恢复/归属设计已由本节末最新决定取代）：按用户要求思考并解决三项剩余问题，新增[ADR007](../../docs/adr/007-p0-evidence-closure.md)，定向修订T132/T133/T134，不改FR/SC和完成标记；内核身份/v2恢复/按协议ready均不得凭设计放行。实际新增003最小SOCKS5回复头校验补丁，27合成流用例修复前11失败/16通过、修复后全通过，race/vet/格式/新副本apply及整树核对通过；无新binary或真实两线路，T134不勾选，详见机器证据（本地原件 `../../experiments/mihomo-events/socks5-reply-validation.json`，不随Git上传）。
- 失败待决策：严格身份Go断言实际exit1，原始PID/名称/start为0/100，原因均为owner_visibility；终末字节/目标域名为100/100，不能替代身份。原stock候选仍受[ADR 001](../../docs/adr/001-event-source.md)否决，增强候选尚未证明完整源恢复及质量/实际链关联，[ADR 002](../../docs/adr/002-p0-core-gates.md)不放行T018及依赖阶段。未降低规格或默认提权。
- 环境不足未验证：原proc完整owner集合仍不可证明，新connector后端及原入口关联未实证；PID复用/共享或转交FD/后代、完整生命周期/路由/故障矩阵、Schema元校验/实例、SSH/注销/休眠、驱动、规模/性能和其他平台仍未完成验收。来源未知保留，不将实际失败改为skip。
- 验证证据：此前Hoopa全量race46通过/2跳过、Observer定向race8通过及原固定核心21校准/9认证/20事件回放均保留原范围。2026-10-22隔离候选23项定向race通过，0skip/FAIL/竞态，vet/format/构建通过；Hoopa计数codec定向race为5顶层/111子用例通过。另行启用的100流身份集成实际失败，不宣称全部测试通过；详见[隔离运行记录](../../docs/validation/native-candidate.md)。
- 模型分工：此前文档审查与实施分工保留于任务日志；本次主代理负责身份/锁/日志/恢复裁决、严格解码/reader及最终验收，executor按角色配置gpt-6.1-sol / medium独占各次明确分配的诊断库、隔离生命周期/接收器/真实探针及制品复核，scout按gpt-6-luna / high独立只读取证和稳定代码审查。最多两个子任务、未再派生；实际模型和测试结果分列于[任务日志](../../docs/task-log.md)。
- 此前全范围修订同步plan/tasks及相关状态/执行指南，详见[复核记录](../../docs/validation/plan-task-review.md)；本次未修改spec、宪章、模板、检查清单与旧运行证据，setup-tasks不重复执行；新核心仅为独立/tmp副本，未替换用户/旧候选核心或安装依赖。
- 任务执行记录应持续使用已完成、进行中、失败待决策、环境不足未验证四种状态；未执行不代表通过。最终审查针对写入完成的稳定diff，单文件一位写入者，同时最多两个子任务。

- 2026-10-23可行性复核后的最新决定：用户选择connect_initiator_v1，FR-034/049/050/051与相关用例/SC、模型/契约/产品文档同步；父connect/子写和FD共享/转交不改已证connector。T132先证connect→原accepted socket唯一关联，尚无已实证算法；T133撤回未实现的强制v2恢复引用图，改原帧重放+派生pending/gap报告/稳定幂等事实，历史gap与当前ready拆分；T134先冻结字段profile再按协议校准，并补HTTP回复/payload同包边界。139任务/18完成保持，原0/100及001/002/003证据不变，本轮仅文档，不新增运行通过结论。
- 2026-10-23随后implement：已实现`protocol/telemetrywire/`共同严格v1/manifest/epoch校验与纯恢复，接入source重开/confirmed prefix发送/receiver/reader；持锁完整稳定扫描后派生gap，read-only缺final仍pending/null，稳定事实不占新seq。004/005在干净upstream新副本实际apply并构建，HTTP同包payload丢失反例已先失败后修复，lazy首次Write有8个actual n定向例；未安装依赖或改用户核心。详见[最新实现及证据](../../docs/validation/source-recovery-closure.md)与机器摘要（本地原件 `../../experiments/mihomo-events/closure-validation.json`，不随Git上传）。
- 已完成（独立子工作，未勾整任务）：局部Hoopa race 18+25+2通过/身份1skip；核心恢复反例、32活跃上限、32epoch/精确8MiB扫描及两项显式网络race通过；扫描2.81s、额外heap约48.7MB为合成样本而非产品预算。全量vet与16项Python测试通过；仓库全量race在默认沙箱96通过/2skip/3因回环绑定权限失败，未把失败当通过。
- 失败待决策：新binary首次真实组合normal通过、active `RuntimeError`且trace空失败、crash未执行，原记录保留。阶段码诊断未复现，确切原因未知。另已修正有源码依据的探针启动前提：独立非计量入站实际CONNECT+回显后再开始主入站测量；修后三小场景和四对只读真实快照通过，不擦除首次失败、不宣称已证明其根因，旧G01的0/100也不重跑/改判。
- 当时进行中（004/005历史阶段）：T133完整collector离线/背压、实际lazy协议及完整故障闭环仍缺；T134协议ready profile/真实两线路未交付，T132仍缺正向connector唯一关联。T132/T133/T134/T137保持未勾选，139任务/18完成，T018/P1继续未放行。环境不足未验证：未获得/验证BPF加载条件，未加载BPF；真实宿主断电/满盘、SSH/休眠、SQL/其他平台和预算保持原范围。

- 2026-10-23本轮收尾：T133明文本地最小闭环验收完成，勾选T133；139项/19完成。新增真实collector离线（独占bind未listen）/ACK停滞/SIGKILL与receiver+core重开两项race均通过，原帧与receipt去重稳定；配对reader预算反例先失败后修复、精确上限接受/超限拒绝，3项相关race通过。详见[验收](../../docs/validation/source-transport.md)及机器证据（本地原件 `../../experiments/mihomo-events/transport-validation.json`，不随Git上传）。T128正式G02依赖未满足，没有据此勾选T128/T132/T134/T137或放行P1。原0/100与首次active失败保持；历史宽泛“完整矩阵”状态不能替代具体T133验收条款，也不能把本次局部通过提升为G02所有组合通过。

- 2026-10-23 T134阶段收尾（历史）：T134按明文范围局部验收完成，截至该阶段139项/20完成。严格v2保留旧v1，实际Conn协议证明/单调ready与finish分离、连接段actual n、稳定inline node/group映射均实现；最终001–006固定候选真实8流/11attempts、每流各方向257B通过，DNS/取消/超时/lazy与partial另有分层定向证据。见[验收报告](../../docs/validation/quality-ready.md)、[新manifest](../../experiments/mihomo-events/quality-candidate-manifest.json)、机器验证（本地原件 `../../experiments/mihomo-events/quality-validation.json`，不随Git上传）。真实run的provider为空/null，未声明命名订阅provider、完整核心DNS E2E或未测协议。T132/T135/T136/T137/T127/T128/T129/T018仍未勾选，P1不放行；原0/100、首次active异常及本轮修前失败均保留。

- 2026-10-23最新收尾：T135本地能力拒绝与受保护实验入口已完成并勾选，当前139项/21完成。最终001–007已在干净上游副本实际check/apply、整树内容/路径/权限一致并离线构建；版本`hoopa-t135-guard-final`，binary SHA256 `7c022d3263cf5c16d72fd68834041ae2bfb15e748283b6aa75d529c1fdf4440f`。最终真实入口/trace矩阵2顶层、含子测试49项pass，0fail/skip，2.839s；harness race、core普通构建。严格JSON、来源预校验、独立认证、原序两线路、只读控制、不可变generation/SIGHUP、drain旧流续传与独立replace有分层证据，所有进程规则仍加载期拒绝，AND unknown仅既有单测，不代替身份。详见[新契约](contracts/protected-core-v1.md)、[验收报告](../../docs/validation/protected-entry.md)、[manifest](../../experiments/mihomo-events/capability-candidate-manifest.json)和机器验证（本地原件 `../../experiments/mihomo-events/capability-validation.json`，不随Git上传）。T132/T127/T128/T129/T136/T137/T018保持未勾选，P0/P1未放行；18/19/20完成计数、原0/100与各次修前/环境失败保留其历史范围。

- 2026-10-23 T132原型准备：用户确认暂时没有隔离内核环境，先完成准备。新增 `experiments/sourceidentity/bpf/connect_snapshot.bpf.c`、`prepare.py`/`test_prepare.py` 及[ABI/范围契约](contracts/connector-capture-spike-v1.md)。准备器12项测试和真实BTF离线编译通过；C/BTF私有快照、工具前后摘要、ELF结构和命令/日志归档，runtime/verifier/association仍unverified。只完成connect观察子步骤，A的active传递、B的accepted关联、loader、核心接线与真实故障矩阵未交付；T132保持未勾选，139/21及正式门槛不变。[验收记录](../../docs/validation/connector-preparation.md)区分运行环境缺失及本轮/tmp满盘导致的工具启动失败，旧身份失败和旧候选不改。

- 2026-10-23 T132继续准备：新增 `bpf/lifecycle_snapshot.bpf.c` 与[生命周期契约](contracts/connector-lifecycle-spike-v1.md)，同对象共享原connect maps，active按cookie查捕获观察，passive和TCP_CLOSE另存不可变记录，不建立tuple索引/归属查询。准备器新增固定lifecycle profile和双源码快照，16项测试及真实BTF离线编译通过。固定v5.10 sockops缺netns helper、SO_NETNS_COOKIE缺席及非原子核验删除竞态已修订ADR007/008；后续关联实验优先核验固定v6.6，但不升级宿主或宣称平台支持。详见[新准备记录](../../docs/validation/connector-lifecycle-preparation.md)。verifier/attach/完整关联未验证，T132仍未勾选；139/21、旧0/100和001–007制品证据不变。

- 2026-10-23 T132对象/读取准备：新增 `inspect_object.py` 与 `observations.py`，在固定摘要的输入上经现有libbpf运行库作真实open/read/close和profile声明核对，解码器只读调用方bytes并保留uint64/comm未知状态。8项假库测试、10项纯bytes测试通过；固定最终源码实际检查两个旧对象成功、截断ELF拒绝，strace未见BPF syscall或namespace/mount修改。默认沙箱ptrace失败保留，经自动审批仅跟踪自有离线子进程；未load/attach、改cgroup或下载依赖。详见[验收](../../docs/validation/connector-object-inspection.md)。完整loader与原accepted关联仍未交付，T132及139/21不变。

- 2026-10-23 T132有限loader准备：新增 `native_loader.py`、`loader_session.py`、`run_observer.py`，完成配置readback/freeze、共享map与直接link info、全部挂接后观测、单次逆序清理和显式开关；47项fake接口测试、18项受影响接口回归通过，未执行真实BPF。贯通测试发现结果map ID被内部close清空，已修复并保留修前失败；详见[验收](../../docs/validation/connector-loader-session.md)。停止前提明确修正：close link不保证RCU回调结束，freeze不阻止BPF写；当前只有diagnostic_snapshot，quiescence/completeness/association仍unverified。T132下一步须证停止同步/稳定cutoff与范围，再完成原accepted唯一关联及核心接线；T132/T137/正式矩阵保持未放行，139/21和旧0/100不变。

- 2026-10-23 T132停止候选准备：按[停止协议v1](contracts/connector-stop-spike-v1.md)新增`gated_snapshot.bpf.c`与三profile支持，单锁admit/stop/exit、用户态LOCK读及不attach的TEST_RUN控制程序接入清理。1/2/3回调模型检查及两错误协议反例、101项测试、三个profile真实离线构建/解析通过；默认三段指令与旧对象相同。见[新验收](../../docs/validation/connector-stop-preparation.md)。源码/模型不代替实际gate/范围/连续性证明；没有load/attach/TEST_RUN，下一步仍须隔离小样本验证，再完成原accepted唯一关联。停止稳定不等于完整采集，timeout不硬中断内核调用；T132/T137、139/21、旧0/100和旧运行证据保持。

- 2026-10-23 T132有限关联模型：新增 `association_model.py`/`test_association_model.py` 与[关联契约](contracts/connector-association-model-v1.md)，14项定向测试通过。C不预知客户端临时端口，原FD查询不使用oracle正确cookie；完整覆盖1/2/3流交错没有错误候选，三类反例揭示忽略fault、漏active、错真实FD的错配，后两类可零fault。仅研究模型，无新BPF profile/ABI/在线resolver。按契约A–E补真实范围/连续性、版本化netns观察、原FD来源与正反例，再审查在线边界及核心接线；[验收](../../docs/validation/connector-association-model.md)保留初版及修前失败，runtime/verifier/identity_capability均unverified。T132/T137及139/21、旧0/100与全部旧运行证据不变，不新开正式分母。

- 2026-10-23 T132原accepted见证准备：新增 `experiments/sourceidentity/acceptedwitness/` 五个Go文件和[契约](contracts/accepted-socket-witness-v1.md)，包内AcceptTCP、单Control完整读取、私有Conn/值副本及失败清理。fake race最终4顶层/含子测试38通过，vet/gofmt及Darwin/Windows stub离线编译通过（无平台运行支持）。本机普通Go Close的SO_COOKIE窗口实测通过；SO_NETNS_COOKIE(71) ENOPROTOOPT使完整见证正例skip/unverified，拒绝/清理/listener保留有实际局部证据，最终helper改动重验同一拒绝路径。见[验收](../../docs/validation/accepted-socket-witness.md)。无BPF/核心接线/正式身份新样本，T132/T137及139/21、旧0/100与旧JSON不变；真实范围/连续性、双cookie及原FD对passive、独立oracle、在线与核心兼容仍待验。

- 2026-10-24 T132连续准备：新增namespaced established 56B/schema2、受控Go client/serve fixture及离线raw-map/token核对器，[执行契约](contracts/connector-calibration-v1.md)将预登记、挂接、外部放置、真实传输与停止后核对串联。129项共享回归、12项checker定向测试、fixture fake race/vet/build通过；三个受影响profile真实离线构建/解析通过，旧指令段相同。独立审查发现缺listening校验已修并保留开发日志；见[验收](../../docs/validation/connector-calibration.md)。无socket/BPF/cgroup操作，无正式身份新分母；完整真实scope/连续性、启动/名称与在线核心接线仍缺，T132/T137/T136及139/21、原0/100与旧证据保持。


- 2026-10-24 T132驱动与材料准备：新增有限三进程driver、单一owner子进程管理和完整运行目录checker；observer marker绑定input SHA/boot/单调时间，namespaced记录time namespace。首spawn重验14秒余量，材料核对PID/argv/exit/EOF、先listening后client与外部scope对observer结果的摘要绑定。58项定向测试通过，真实子进程仅无网络Python程序；driver/scope及BPF记录使用假接口/合成材料，见[契约](contracts/fixture-run-v1.md)、[验收](../../docs/validation/fixture-run.md)。本轮无真实fixture/socket/BPF/cgroup操作；T132真实范围、挂接连续性、双cookie关联、启动/名称与在线核心接线仍未验证，T132/T137/T136及139/21、原0/100与旧证据保持。

- 2026-10-24 T132首次隔离内核实测：用户后续授权下实际创建/启动私有KVM/QEMU来宾，官方WSL6.18.54.1具备BTF/cgroup2/time namespace/dual-cookie。修复私有root cgroup预检、libbpf0.6目标BTF失败（私有1.6.2）、TEST_RUN opts接线及map object flags与FD访问模式混淆；旧失败/原对象/旧JSON/正式0/100保留。最终58项loader相关测试+20项checker测试通过；真实两客户端PID83/84、原accepted cookie核对一致，stop35/35 active0、两个link关闭、driver/observer exit0，完整材料consistent_unverified。5个从真实材料派生的离线变异与2个CLI参数反例均拒绝，不是真实故障矩阵或正式新分母。见[验收](../../docs/validation/isolated-linux-lab.md)与机器摘要（本地原件 `../../experiments/sourceidentity/isolated-lab-validation.json`，不随Git上传）。环境已可用于后续实验，不能再把整个T132归为“无隔离环境”；真实停止交错/范围/复用/FD转交负例、启动身份/名称、在线关联及核心接线仍未齐，T132/T137/139/21与P0/P1门槛不变。

- 同轮追加实际负例：wrong-driver-scope按预登记在fixture socket前拒绝；SIGTERM原预期失配（实际observer/driver均1、result为interrupted且零fixture），保留原登记，不改为通过；另登记SIGKILL后观察到observer-9/无result、driver和fixture成功通信但整轮拒绝。此反例要求T132优先冻结在线/核心转发前的来源失联拒绝，单纯marker、事后checker或瞬时进程存活检查不足；不把实验driver直接投入生产选路。私有原始材料及[验收边界](../../docs/validation/isolated-linux-lab.md)已记录，正式分母和139/21不变。


- 2026-10-24 T132有限在线准入：新增 `bpf/online_snapshot.bpf.c`、`online_admission.py` 与独立 `online_test_client.py`，冻结[在线契约](contracts/connector-online-spike-v1.md)。同一实验进程持有listener/link/map/原accepted socket，C→A→P同步发布不可变绑定，Q在任何应用读写前核对；guest根挂接与source叶scope分开。profile/受影响回归130项、准入9项、client19项通过（共158项，17项新profile测试已包含在130项内），实际BPF与socket证据另列。见[验收](../../docs/validation/connector-online.md)及机器摘要（本地原件 `../../experiments/sourceidentity/online-validation.json`，不随Git上传）。
- 已完成的T132子步骤：13次独立VM中12个有效场景，14条允许配对、七种拒绝条件；线程TID与TGID区分，100Hz/零time偏移下leader启动ns对proc ticks吻合；父connect/子写与SCM_RIGHTS仍归原connector；同tuple第二次实际connect后在应用读写前拒绝。服务SIGKILL后新client connect refused，修复了独立observer退出后该新实验入口仍可无证通信的形态；服务不是Hoopa管理器，不能替代T136的管理器离线转发验收。
- 失败及修正保留：首次thread client在回显后因私有镜像缺libgcc_s.so.1而-6，整场仍failed；只补guest库并另行预登记重跑成功。V1范围外断言原本未限制失败阶段，host检查追加要求原始connect EPERM，未改旧guest/登记。旧SIGTERM失配、observer SIGKILL后仍通信、原G01 0/100与历史active根因未明均保留。停止/link撤销只覆盖明确accept/Q边界，不冒充任意并发故障覆盖。
- 当前进行中：T132须完成Mihomo原入口接线及核心/加载端资源所有权、重启/长期回收、完整scope与连续性、跨namespace、exec/改名及字段能力矩阵；单点RSS/CPU ticks/fdinfo不是预算验收。T137固定完整候选仍依赖T132，不放行T136/正式G01–G03/P0/P1。新增正式身份样本0，139/21不变；隔离6.18环境已可用，首选6.6、其他平台及实际SSH/休眠仍未验证。

- 2026-10-24 T132有限核心接线：按[资源移交](contracts/connector-core-handoff-v1.md)/[事件v3](contracts/source-connector-v3.md)新增bootstrap与Go coreadmission，0008在原accepted socket、协议worker之前核验，值副本进入同flow原始opened；0009修复Hoopa计量包装遗漏CloseWrite导致早EOF丢下行的真实回归。新pristine实际check/apply001–009并构建，最终hash `452effa120f0f0beea122169c9e442b6338389bc4768111c4ed7ffb8d5f44f9b`；旧补丁/候选保持。见[验收](../../docs/validation/connector-core.md)、机器摘要（本地原件 `../../experiments/sourceidentity/core-validation.json`，不随Git上传）、[候选manifest](../../experiments/mihomo-events/core-connector-candidate-manifest.json)。
- 本轮已完成子步骤：16次VM保留2次已诊断失败，14次满足有限判据；最终hash单独9场景、7条有效配对/48来源帧，涵盖HTTP/SOCKS两线路、原提前SHUT_WR、drain续传、核心正常重开旧来源不变、scope外/core kill拒绝及三类移交启动拒绝。核心相关5包203项含子测试race通过，bootstrap25+26、半关闭9顶层、默认wire/receiver406pass与7显式skip、真实旧protected/v2矩阵49pass0skip；不同批次有重叠，不合计为唯一测试数。startup/name/path/formalcap仍按字段区分，comm非原子、pathunknown、三个identity能力false；未把受控Python客户端当Codex正式100流。
- 当前进行中：继续T132在固定核心上的线程、父connect/子写、SCM转交、exec/改名等独立矩阵，以及活动来源崩溃gap、完整scope/namespace/缺失冲突、map长期回收与生产权限部署；旧token服务的已验样本不直接算到新corehash。T132/T137不勾，139/21、P0/P1未放行、G01新增正式样本0/旧0/100不变，T136管理器离线与接管另验；首选6.6/其他平台/真实SSH休眠仍未验证。

- 2026-10-24 继续完成上述五类核心边界：新 client 22 fake、旧 client 19 回归、harness 73 断言通过；最终同一核心5次真实VM/10条流/65帧，thread、父connect/子写、SCM、改名、exec分别完成两协议同flow身份/实际线路/ready/整数终末核对。见[独立验收](../../docs/validation/connector-core-edges.md)与机器记录（本地原件 `../../experiments/sourceidentity/core-edges-validation.json`，不随Git上传）。本批无实际失败，旧失败保留，正式能力不升级。
- 当前继续项：按[活动来源崩溃契约](contracts/connector-core-crash-v1.md)验证旧CP/未知尾部与同来源重开；核心内部writer恢复报告和只读pending边界不能混淆。T132剩余同名主/子独立短流、并发/早退/PID与tuple复用、namespace/缺失冲突/连续性及名称快照一致性仍待闭合。139/21、T132/T137未勾选、P0/P1未放行和正式G01新分母0保持。

- 2026-10-24 活动来源异常退出有限回归完成：私有60断言后实际1VM/2流/13帧，旧CP12/12无终末、核心SIGKILL后客户端断流，新世代独立对象与新来源33/33；只读pending/tail=null。测试overlay调用固定核心实际writer，对真实原帧副本三次重开得到稳定gap及逐字重放，race1顶层通过/0skip；不能冒充原VM已导出恢复报告，T138/T073传输与账本责任保持。见[验收](../../docs/validation/connector-core-crash.md)/机器摘要（本地原件 `../../experiments/sourceidentity/core-crash-validation.json`，不随Git上传）。139/21、T132/T137未勾、正式G01新增0/旧0/100与P0/P1门槛保持；继续同名父子独立并发流、PID复用及名称/namespace/连续性矩阵。

- 2026-10-24 T132 同名父子并发/PID复用两项有限子步骤完成：104离线断言，2真实VM/4流/26帧；父81子83独立且两ready先于closed，另一VM两次实际PID30000由不同leader start/socket区分，全部33/33终末与两线路核对一致。见[验收](../../docs/validation/connector-core-lifetimes.md)/机器记录（本地原件 `../../experiments/sourceidentity/core-lifetimes-validation.json`，不随Git上传）。继续原connector早退、名称并发一致性及其余namespace/tuple/连续性；本批无失败、正式能力不升级，139/21和既有门槛不变。

- 2026-10-24 原建连进程早退子步骤完成：85离线检查，1真实VM/2流/14帧。子83/87在父82/86握手前已exit0/reap，SCM原socket继续通信，connector仍为子实例，33/33整数终末与实际两线路一致；不声称Admit发生于退出后。见[验收](../../docs/validation/connector-core-earlyexit.md)/机器记录（本地原件 `../../experiments/sourceidentity/core-earlyexit-validation.json`，不随Git上传）。下一步namespace拒绝及名称/tuple/覆盖矩阵，139/21及既有正式门槛不变。

- 2026-10-24 T132 PID/net namespace范围拒绝完成两项有限子步骤：[验收](../../docs/validation/connector-core-namespaces.md)/机器记录（本地原件 `../../experiments/sourceidentity/core-namespaces-validation.json`，不随Git上传）。保留首轮UART导出损坏及soft lockup失败，V2独立virtio通道117离线检查后2VM严格通过；原connect EPERM、0应用字节/flow/实际线路，实际BTF摘要核对。不宣称跨namespace支持；继续名称一致性、核心tuple复用及覆盖/缺失/冲突矩阵，139/21与正式门槛不变。

- 2026-10-24 名称可行性子步骤：[真实原子/挂接探针](../../docs/validation/connector-name-feasibility.md)75离线检查+1VM通过，仅确认实际BTF、fentry/fexit/connect4原子指令有限执行与清理。125216个SC交错的有限模型不替代内核证明；固定源码确认ftrace存在BPF miss之外的跳过路径，名称一致性继续研究，未升级字段。后续已登记[覆盖开关反例](contracts/connector-name-coverage-control-v1.md)及[固定核心tuple复用拒绝](contracts/connector-core-tuple-reuse-v1.md)，既有门槛不变。

- 2026-10-24 名称覆盖反例（本地原件 `../../experiments/sourceidentity/name-coverage-validation.json`，不随Git上传）已实测：guest ftrace关闭期间真实改名未被同一钩子观察，miss/poison仍0；恢复后正常计数。102离线检查+1VM符合负向判据，不能升级名称能力；连续覆盖机制仍需设计，端点开关/计数轮询不足。继续核心tuple负向及完整固定源码审查，139/21不变。

- 2026-10-24 固定核心tuple复用负例完成：V2 96离线检查+1VM/1完整流/7帧，PID83/start441/cookie4097与PID86/start583/cookie2同四元组connect成功；第二连接EOF且零协议/应用写，admitted1/rejected1/revoked，第一33/33原帧保持。见[验收](../../docs/validation/connector-core-tuple-reuse.md)。首轮夹具将当前连接gauge误作累计值而超时，第二客户端未启动，原失败/契约保留；新契约仅修正状态解释/诊断。当前捕获世代随复用失效，未解决长期回收/可用性。继续exe快照独立spike与完整连续性/字段能力；139/21、T132/T137/P0/P1及正式新增0不变。

- 2026-10-24 exe_file独立spike完成：108准备检查+1实际VM，2条带引用路径与原cookie匹配，挂接前/解除后新连接EPERM；comm改名后exe路径保持。主机checker两处元数据解释错误已保留，独立86检查后的修订checker复核同一run通过，未重跑/增加样本。见[验收](../../docs/validation/connector-executable-snapshot.md)。此独立字段未替代comm或接入核心，继续路径改名/超长/删除和关联连续性；139/21、T132/T137/P0/P1及正式新增0不变。

- 2026-10-24 [可执行文件路径边界](../../docs/validation/connector-executable-path-boundaries.md)有限实测完成：173离线检查后1VM/6新socket/5记录，同一dev/inode下改名更新新快照、282B路径-ENAMETOOLONG并EPERM、移回恢复、删除原文保留，历史不变。本批无实际失败；继续同socket重试/消费反例和sockops连续性核查，不将新cookie验证推广到重试。139/21、T132/T137/P0/P1及正式新增0保持。

- 2026-10-24 T132可执行文件消费边界：[旧对象1VM](../../docs/validation/connector-executable-socket-reuse.md)实际复现解除采集后同cookie使用旧路径；[新对象2VM](../../docs/validation/connector-executable-consumption.md)拒绝已消费cookie重试且保留新cookie正常准入。45/53分别是两批准备检查，不当作真实流数；未消费旧记录仍待处理。TFO早期passive回调与TCP_INFO标志另验，不从普通握手推断。T132/T137、139/21、正式新样本0和旧失败保持。

- 2026-10-24 T132 [TFO标志1VM](../../docs/validation/connector-tfo-boundary.md)独立观察完成；0010原FD检查与固定release限制已build，新核心运行验收仍进行中。普通半关闭不按state误拒，TFO/普通源码次序分开；不是T137完成或正式身份新样本。

- 2026-10-24 T132 [0010新核心正向1VM/2流](../../docs/validation/connector-core-tfo-positive.md)通过，两个协议均提前SHUT_WR后收足33/33，13帧/21导出复核；TFO拒绝另验。未消费记录实验首轮在AF_UNSPEC错误口径停止，已登记[v2](contracts/connector-executable-unconsumed-v2.md)保留失败，不将首轮记成完整反例通过。139/21及全部正式门槛保持。

- 2026-10-24 T132 [未消费旧快照反例](../../docs/validation/connector-executable-unconsumed.md)在v2新VM完整成立，证明CAS不能单独解决freshness；首轮在断开错误预期处中止并保留。下一步核查同一次系统调用token的可行性与拒绝旁路，不将预期反例通过等同身份能力通过，正式门槛保持。

- 2026-10-24 T132 [0010核心TFO实际拒绝](../../docs/validation/connector-core-tfo-negative.md)完成：49准备检查后1VM，首普通流33/33完整保留；第二同tuple确有181B MSG_FASTOPEN发送和SYN_DATA32，reset且零回应/额外flow。24导出摘要核对，sysctl恢复，未声称复现旧错配或独立识别内部拒绝分支。下一子步骤已登记[connect调用token契约](contracts/connector-executable-call-token-v1.md)，新296B记录与raw entry/exit候选仅编译及源码审查，实际verifier/运行尚未验证；不修改T132/T137勾选及正式分母。

- 2026-10-24 T132 [调用编号有限实测](../../docs/validation/connector-executable-call-token.md)完成：38项准备检查、1次隔离VM、10次native connect（含AF_UNSPEC）、9个token；旧EISCONN未消费记录、TFO与缺entry均拒绝，恢复普通新socket可达ECONNREFUSED。4map/4program、6次link创建（包含重挂）清理核对；18项后续离线变异不增加VM样本。新[source v4](contracts/source-connector-v4.md)解码38顶层/298含子测试race通过，旧v3严格性保留；目前组合BPF仅编译/离线解析，20FD核心接线进行中。139/21、T132/T137和正式新增0保持。

- 2026-10-24 T132组合接线准备：严格20FD v2移交、原accepted→client-cookie→296B exe值副本及v4 writer完成；Python相关126 fake、public coreadmission 28顶层/144含子测试race通过，核心wire/writer/guard最终批375项含子测试通过、2个显式网络skip；随后guard profile4定向5项通过。首次镜像测试源的JSON fixture路径缺失保留并修正。新[001–011 manifest](../../experiments/mihomo-events/core-executable-candidate-manifest.json)固定干净apply/tree/build，组合BPF实际verifier/转发待[独立契约](contracts/connector-executable-online-v1.md)实测；不把独立call-token通过借给组合对象，不改变任务勾选/正式分母。

- 2026-10-24 组合首VM整轮失败保留：两流33/33、v4 exe token1/3已出现，但constant中的实际线路/ready捕获开关只识别profile2/3，导致profile4实际hop类型unverified/稳定ID空。主机checker按原标准拒绝，未勾T132；独立0012修复与新制品/VM回归进行中，不能在旧候选上补写通过。

- 2026-10-24 T132 [组合核心正向实测](../../docs/validation/connector-executable-core-positive.md)在独立001–012新制品通过：76准备检查后1VM/2流/13帧，20FD接管、296B原exe快照、原PID/TID/cookie、token1/3、HTTP/SOCKS两实际节点与12/31ms ready、提前SHUT_WR后33/33及完整epoch均核对。0011首轮线路捕获失败原21项证据保持不变；0012只补profile4质量开关并增加真实子进程初始化回归。继续线程/转交/路径变化在新组合上的独立矩阵；T132/T137未勾、139/21、正式新增0及P0/P1门槛保持。

- 2026-10-24 T132新组合追加[五类归属边界](../../docs/validation/connector-executable-core-edges.md)：191离线检查后5VM/10flow/65帧，线程PID/TID、父子写入、SCM、comm改名、换路径exec均保持原connect快照；exec后不同path/inode不改历史。[范围负例](../../docs/validation/connector-executable-scope-negative.md)81离线检查后3VM/3基线flow/21帧，6次trigger/followup目标connect均EPERM，73项导出核对；owner状态只计accepted，不能代表BPF捕获健康。两批独立运行复核无critical/high矛盾，本批8VM无运行失败，准备问题及旧失败保留。继续[tuple/TFO负向](contracts/connector-executable-connection-negative-v1.md)、捕获健康/连续性与新组合恢复；T132/T137、139/21、正式新增0/P0/P1门槛不变。

- 2026-10-24 T132 [组合连接负例](../../docs/validation/connector-executable-connection-negative.md)完成131离线准备后3VM/3基线流/21帧/83导出，tuple复用进入accepted后拒绝，两个TFO syscall为EPERM，各自普通followup均拒绝。独立证据复核符合，原prefix和33/33终末保持；未识别内部拒绝分支、未观察原BPF counter。新增[只读诊断契约](contracts/connector-capture-diagnostics-v1.md)和0013独立候选，整包race233项及集成100项通过，实际新VM准备中。T132/T137、139/21、正式新增0/P0/P1不变。

- 2026-10-24 [0013只读采集诊断实际验收](../../docs/validation/connector-capture-diagnostics.md)：56离线准备后1VM/1基线流/7帧/23导出。4阶段各2读取，capture_faults为0/0/1/1，旧status前后不变；scope拒绝后正常followup仍EPERM，raw recursion/exe计数实际读为0但未注入真实skip。VM/extract/check均0。继续[新v4活动核心崩溃与重开](contracts/connector-executable-core-crash-v1.md)和hook连续性，不能继承旧v3矩阵；T132/T137及139/21、正式新增0/P0/P1不变。

- 2026-10-24 [0013/v4活动流崩溃与重开](../../docs/validation/connector-executable-core-crash.md)105离线检查后1VM/2core/2flow/13帧/31导出通过。旧HTTP PID85检查点12/12后SIGKILL核心77，明确断流；新核心88以全新20FD/世代承接SOCKS PID95终末33/33。原材料不变，旧只读pending/tail=null。独立test-only overlay race1项/3reopen生成6内部报告，旧gap fact稳定且原v4+exe重放逐字相同，非VM API gap导出。继续[固定link操作边界](contracts/connector-capture-link-operations-v1.md)与并发/早退等身份矩阵；T132/T137、139/21及正式门槛保持。

- 2026-10-24 [采集link操作边界](../../docs/validation/connector-capture-link-operations.md)134离线检查后1VM/2flow/13帧/23导出通过。自有core的六个副本经pidfd取得并核对；3次detach=EOPNOTSUPP、3次update=EINVAL，前后对象/status/四零诊断一致，HTTP在操作前、SOCKS在操作后均33/33完整。未成功撤销挂钩或证明完整连续性；rawTP目标名probe只读长度的范围已明确。继续[生命周期新矩阵](contracts/connector-executable-lifetimes-v1.md)，其中早退以实际SIGSTOP确认→child connect/SCM/exit/reap→core resume保证接纳先后。T132/T137及139/21、正式门槛保持。

- 2026-10-24 T132新生命周期实际完成：[三模式](../../docs/validation/connector-executable-lifetimes.md)162离线检查后3成功VM/6流/39帧/77导出，均0；另保留1个核心启动前失败VM。实际STOP/子退出/reap/CONT顺序补足旧早退限制，PID30000两个启动身份与同名父子独立流均核对。短资源与Admit unavailable分记；完整连续性/部署前提尚待闭合，任务勾选及139/21不变。

- 2026-10-24 T132 [cgroup实际卸载](../../docs/validation/connector-cgroup-hook-loss.md)两VM/extract/check均0：先33/33基线，再实际detach sockops/connect4；新普通connect0后零写EOF、准入1/1/revoked、旧六帧不变、最终7帧完整。共56导出重验，95本包离线检查与root73分计；sockops四零/connect4 capture_faults2不等同连续性证明。继续受限部署前提和[原Admit调用耗时](contracts/connector-admit-measurement-v1.md)，正式任务状态不变。

- 2026-10-25 T132 [原Admit整段计时](../../docs/validation/connector-admit-measurement.md)实际1VM/2流/13帧/26导出均通过，161824ns和89686ns分别绑定原连接；88准备检查及测量overlay34顶层/233含子race分计。原0013树/制品不变；整段含mutex/同步核验，纯等待仍不可拆分。按[ADR010](../../docs/adr/010-restricted-identity-profile.md)选定区分程序名/comm的工程方向并声明可信profile条件，下一步[非特权客户端](contracts/connector-unprivileged-client-v1.md)验证权限边界；不凭文档提升正式capability或勾选任务。

- 2026-10-25 T132 [降权客户端实际验证](../../docs/validation/connector-unprivileged-client.md)：138离线检查后v2 VM/extract/check均0，2流/13帧/38导出；PID84/89均65534、五组capability零、NNP1，实际cgroup打开EACCES、原核心FD复制及BPF link获取EPERM，两线路33/33终末完整。v1祖先目录断言失败在spawn前发生并保留，具体旧mode未保存不猜测；新镜像显式目录权限及先保存layout后通过。继续[逐连接user/mount范围守卫](contracts/connector-namespace-guard-v1.md)，不能将启动点采样升级为动态namespace能力；正式任务勾选/139/21/G01/P0/P1状态保持。

- 2026-10-25 T132 [建连时namespace守卫](../../docs/validation/connector-namespace-guard.md)完成227离线检查及3实际VM/extract/check0：正常2流、两个负例各1基线，共4流/27帧/138导出。实际unshare仅改变指定user/mount后原connect及initial followup均EPERM/零写；capture_faults0→1→2与Owner1/0未revoked分记，原六帧及完整33/33终末保持。新0014/21FD/raw24实际加载核验，独立scout原始证据复核无重大矛盾。继续[严格v5字段能力与preflight](contracts/source-connector-v5.md)，旧v4/正式139/21/T132/T137/G01/P0/P1状态保持。

### 2026-10-25：受限字段原型与固定候选完成

T132按[主代理裁决](../../docs/validation/t132-restricted-acceptance.md)完成；T137追加独立SHA命名候选，不覆盖旧manifest。0015干净应用/构建、实际3种preflight拒绝、新VM两流13帧38导出均通过；source5实例/程序名/路径在固定范围可用，comm未验证，旧PROCESS仍拒绝。139项完成23项。新正式G01分母0，旧0/100保留。继续T136控制harness与后续正式矩阵，不宣称P0/P1完成。

- 2026-10-25 T136本范围完成：0016仅修复受保护socket SO_REUSEADDR，CGO1干净001–016构建；新helper Control race56/200、vet通过，guest两socket回归和五场VM/extract/check均0。9流、64活动帧+1失败目标终末、167导出；完整旧bundle回滚、新实例替换、身份拒绝和两同核心接管验收。新增不可变SHA候选，0015旧失败保留；未验证混合版本立即重绑/SSH/休眠/其他平台。139/24，正式G01新增0、旧0/100，P0/P1未放行。

- 2026-10-25追加T140：修复字段能力与实际规则消费的已证缺口，不放宽正式G01/G05要求。140项/24完成；T140进行中，T127等待新consumer能力而不空跑旧host oracle；非法UTF8文本路径拟作实际rule-time unknown前提，当前只有源码/单测依据，尚无该真实样本。

- 2026-10-25 T140实现阶段：[记录](../../docs/validation/connector-program-rules.md)包含显式配置入口、source5按值消费、只读声明与0017干净构建。配置15/143、集成28/62、主入口4/43、初始化1/11定向race均通过/无skip；有效修前重复route请求2失败已修，首次cwd setup失败保留。新hash5693d4ac…ac1b967尚无实际VM验收；14类校准夹具进行中，T140未勾，140/24及正式新增0/原0/100不变。

- 2026-10-25 T140有限验收完成：0017固定候选14场VM/extract/check均0，28流（20条33/33转发、8条0/0拒绝）、166帧、462导出、14完整epoch；原始程序文本、NAME/PATH及AND两顺序核对通过。机器记录与SHA候选已冻结，主代理据独立稳定复核勾选T140，140/25。共享拒绝payload和零accept边界见[验收记录](../../docs/validation/connector-program-rules.md)。T127新版oracle准备中；正式新增0、原0/100和P0/P1未放行保持。

- 2026-10-25 T127实现准备：按[正式oracle契约](contracts/source5-formal-oracle-v1.md)实现五类各20的固定正例组成、每connect前原socket/进程独立登记与fsync/release屏障；父/子writer及线程TID分开记录。先同冻结镜像五类各2校准，后正式100登记，负例/边界另列。客户端40项、guest登记45项纯/fake准备检查通过，不算真实场景；镜像/host checker准备中，正式新增仍0，T127未勾，140/25不变。

- 2026-10-25 T127首轮正式记录：v3五类10条准备通过后完整登记100；首轮冻结检查器结果60/100，fork/scm各20因误将合法周期事件名写为checkpoint而拒绝（实际名bytes_checkpoint）。五场VM和规范提取均成功，失败原run/claim/登记/检查结果不修改，不事后改判。主代理已在独立checkpoints lab修正检查器并补针对性回归，准备新一轮登记；不改核心/来源协议，也不放宽身份/终末/100分母要求。T127仍未勾，140/25，P0/P1未放行。

- 2026-10-25 T127新独立批次正向100/100通过：先同镜像10条准备，后五类各20，612帧/210导出/5完整epoch；独立冻结复核及真实公共只读Go入口1通过0跳过。见[有限验收](../../docs/validation/source5-formal-identity.md)。旧60/100和0/100保留，7个周期检查点本批均0/0；不宣称非零中间进度或writer-end保证。PID复用/缺凭据/权限/G05三值边界进行中；T127仍未勾，140/25，P0/P1未放行。

- 2026-10-26 T127 PID复用正式边界2/2通过，准备2条另列；同PID30000、start469/499、cookie1/4097，13帧/25导出/完整epoch，两条33/33及独立线路。见[边界验收](../../docs/validation/source5-formal-identity.md)。不增加正向100分母；缺凭据/权限/三值边界进行中，T127未勾，140/25。

- 2026-10-26 T127权限正式单例1/1通过：实际降权及有效MAP_CREATE→EPERM，7导出独立核对；只接受预登记BPF管理权限范围，不推source/data-plane/G07。准备单列、正向100不变。T127缺凭据正式验证及三值矩阵继续，140/25。

- 2026-10-26 T127缺凭据正式边界2/2通过并独立复核：基线33/33；停止核心后删除原cookie4097的exe key，ENOENT且connect112不变，恢复后degraded/revoked/rejected，无负例opened及来源账（null）。7帧/28导出；计划SOCKS/beta标签不算实际请求。原准备shadow0/2保留。三值正式矩阵及整体门槛继续，T127未勾140/25。


- 2026-10-26 T127受限正式验收及T012同源评审完成：三值14/14（7VM/75帧/168导出）和此前独立正向100、PID复用2、缺凭据2、权限1形成完整限定矩阵；总只读门槛708文件核验，真实race1通过0失败0跳过。旧报告taskfalse与旧失败保持原义，主代理独立acceptance记录当前结论。140/27，T128准备开始；P0/P1、完整G05、生产权限部署和平台范围不放行。

- 2026-10-27 T030 部分实现：真实固定 basic core 公开 Apply 首启/同端口替换通过，2 本地授权转发/1 拒绝/同请求无重复run；control/config 最终race680pass，实际专项另计1场景+1空helper。见[部分验收](../../docs/validation/p1-basic-apply.md)及[顺序契约](contracts/basic-apply-v1.md)。失败候选终止、rollback/recover/stopped历史仍待，不勾选T030/T031，43/140；组件顺序测试不替代真实故障矩阵。

- 2026-10-27 T030/T031 继续：候选failed/degraded真实终止、完整旧bundle三实例自动回滚通过独立实际场景；control/config回归744pass/1明确材料opt-in skip，分版本证据见[自动回滚](../../docs/validation/p1-basic-rollback.md)。新目录容量/持久边界33pass，操作残留显式维护26pass。显式Recover已实现，正在验证独立调用去重、两记录中断和新鲜原件/核心证明；T030/T031暂未勾选，不把历史通过当作新增恢复通过。

- 2026-10-27 T030模块完成：公开首启/replace与完整快照门控、按publishing→active→committed顺序发布已由实际固定核心和最终control/config/runtime/mihomo回归1037pass验证。T030勾选，44/140。T031自动回滚、有限已open实例显式恢复和独立调用记录已有3实际场景/5core证据；实际manager SIGKILL补验进行中，不提前勾选。历史stopped与受控重启仍由T032处理。

- 2026-10-27 T031模块完成：自动完整旧态回滚/失败degraded、显式临时残留维护、恢复调用独立去重和两记录中断已由前述实际/定向证据验收；最终manager-crash-04补足1个真实SIGKILL场景，原core继续转发，新Coordinator公开Recover完成原目标和调用，零重复run。3顶层pass包含2空dispatch，0fail/skip；195冻结引用独审一致，生产相对1037-pass版本未变。见[死亡恢复补验](../../docs/validation/p1-manager-crash-recovery.md)。T031勾选，45/140；T032停止/重启、T033服务/CLI、T034完整产品矩阵继续未勾选，closed/未证退出窗口仍degraded。

- 2026-10-27 T032模块完成：停止意图先持久再原pidfd TERM，停止后从最后bundle闭态启动并发布新revision；公开Recover可完成原活核stop及既有closed runtime.start，不重复spawn。授权watch每Coordinator一次、每watch一次预算，重启保留revision/generation。三实际场景/六core/九echo按两批源码分列，最终受影响两包race480pass/0fail/skip、vet与46文件格式检查通过，独审无阻断。见[验收](../../docs/validation/p1-runtime-lifecycle.md)。46/140；T033服务/CLI、T034矩阵仍未完成，未证出生/退出窗口保持degraded。

- 2026-10-27 T033子模块推进：[有界IPC/依赖准备](../../docs/validation/p1-service-preparation.md)已通过；[已运行实例普通start接管](../../docs/validation/p1-managed-start.md)真实一场/一core/四echo，保持原身份/配置且无新运行。最终V2 control race436pass/0fail/skip、vet及独审通过，实际V1正常路径与V2无效证据检查分列。T033仍未勾，服务发现/就绪/stop事务和Cobra产品命令待接线；任务46/140。

- 2026-10-27 T033完成有限服务/CLI交付：实际Cobra命令、writer/实例发现、就绪、原始stop收据、只读状态/操作与显式一次重启接线通过。[验收](../../docs/validation/p1-service-lifecycle.md)实际两场/四core/九echo；最终race633pass/0fail/1skip（该轮未传可选核心变量），114顶层/520子项，vet退出0；22正例/3反例数据形状验证，134冻结源码/152证据引用独审一致。actual01–04失败及版本差异保留，未把未完成核心操作或离线只读激活当已确认运行。已完成：T033；进行中：T034真实产品harness与矩阵；失败待决策：此模块无；环境不足未验证：实际会话/休眠、其他平台与预算。47/140，T034仍未勾，P1与整体项目未完成。

- 2026-10-27 T034继续推进：[准备验收](../../docs/validation/p1-integration-preparation.md)完成真实CLI有界子进程传输（6顶层20子项，普通/race通过，两个真实只读失败原件）及F14相关恢复接纳前核验。旧顺序先记录再发现原归属冲突已由失败回归证明并修订；接纳后证明继续保留。最终实际缺注册原件一场/两core/四echo，输入未变；新control race507pass/0fail/1skip，未变service102pass、CLI55pass分别沿用同生产版本的前轮，最终vet通过，未伪装单次全套通过。旧证明次数断言失败已保留并改为终态接纳前后共四次，原矩阵断言未删。已完成：运输seam与有限恢复修订；进行中：完整双入口fixture转换/产品harness/故障证据；失败待决策：无新未裁决项；环境不足未验证：实际会话/休眠及其他平台。T034仍未勾，仍47/140。

- 2026-10-27 T034固定双入口增量：[有限转换与实际连续性](../../docs/validation/p1-finite-dual-entry.md)完成。原两套1248B配置及独立预期snapshot精确保留；公开Apply→Stop明确setup，真实CLI普通start、管理器SIGKILL后held/new转发、重开不写状态及显式Recover同一原核心，再完整替换改走另一实际节点。最终普通/race各2顶层pass（各含1空dispatch），各3core/2Source/9echo；race只覆盖测试监督进程/BasicApplier/网络辅助，CLI和固定core普通构建。Loader27节点普通/race通过，vet通过；三个核心均原pidfd退出及proc消失，孤儿精确回收。已完成：该有限场景与转换；进行中：完整factory、事件时刻trace和故障命中持久见证；失败待决策：无未决，vet辅助锁复制已修正并保留旧失败；环境不足未验证：会话/休眠、断电及其他平台。T034仍未勾，47/140不变。

- 2026-10-27 T034下一接线前置：已定位 `assertOrdering` 将所有持久操作都视作新revision发布的假设，与已实现的adopt-running、operation.recover、service.stop不符；授权自动restart也保留revision但会发布新的运行权威。下一工作包按操作类型核对目标revision是否存在及实际发布/保留语义，保留全部原始操作和身份/副作用断言，不伪造target revision、不漏报操作来适配测试。本条是待实施契约修订，不是已修复或矩阵通过；本轮固定场景源码及验收记录保持不变。

- 2026-10-27 T034进程外见证增量：[五个真实故障场景](../../docs/validation/p1-traced-faults.md)普通/race各7 PASS节点、0fail/skip，各8core/4次管理器SIGKILL，完整退出且无清理补信号。实际入口占用触发完整回滚，Stop三个窗口及Apply发布后显式恢复通过；完整factory仍未实现。已完成：事件运输/监督、按类型顺序与真实历史目标绑定、V1上述有限场景；进行中：重启trace和剩余矩阵/适配器；失败待决策：无；环境不足未验证：会话/休眠、断电及其他平台。默认六包回归普通/race各1320pass/12可选core跳过、vet通过；旧runner与CLI错误预期失败保留。T034未勾，47/140不变。

- 2026-10-27 T034 V2继续：[六场景及pidfd EINTR修复](../../docs/validation/p1-traced-faults.md)普通/race各8PASS节点、0fail/skip，各11core/4manager SIGKILL/2core SIGKILL，无cleanup补信号，169冻结输入一致，独审原件6405/6396引用通过。平台115、上层runtime/control/service695节点分别普通/race通过，上层12可选core跳过；不拼接为单次全套。原生信号实验确认EINTR机制，修复仅同原pidfd最多16次重试、每次检查ctx，其它errno/事件仍拒绝；历史restart/race02与06未捕获errno，不事后断言完整归因。pre-spawn gap测试依既有契约收紧为degraded诊断、禁止重建ticket，未冒称该矩阵已运行。进行中：只读bundle/operation投影和完整factory；T034未勾，47/140。

- 2026-10-27 T034只读接线增量：[七操作真实回执与owner观察](../../docs/validation/p1-traced-faults.md)普通/race各1场景、2core、7操作/7原始回执、2新鲜owner收据、4组只读树快照一致，原pidfd退出且无cleanup补信号；172冻结输入、每轮1625证据引用独审一致。bundle/operation组件18节点、owner组件7节点分别normal/race通过，taggedvet通过；新鲜Observe不冒称完整managed activation。完整历史读取另完成473事件/47真实历史日志/7操作的离线关联；候选与rollback运行角色的顺序策略修订及完整factory仍进行中，长连接真实入口归属接线并行。已完成：该有限接线场景；进行中：上述适配器/矩阵；失败待决策：无须用户决定项，旧失败保留；环境不足未验证：实际SSH/logout/休眠及其他平台。T034未勾，47/140。

- 2026-10-27 T034统一适配器继续：[流量/回执/容量组件](../../docs/validation/p1-traced-faults.md)各自冻结版本普通/race分别10/95/40 PASS、vet通过。流量保留原连接和实际节点见证，回执区分独立调用ID、wire结果与目标/恢复调用逻辑结果，容量拒绝允许旧ID精确回放。第一轮真实统一F02管理器SIGKILL后同核心held/new流量通过（19.38秒，181输入未变），发现并加固测试KillManager对已提前退出进程的误报路径；后续组合验证进行中，此单轮不替代完整验收。已完成：上述有限组件；进行中：统一factory和剩余故障矩阵；失败待决策：无用户决策项，历史失败保留；环境不足未验证：实际SSH/logout/休眠及其他平台。T034未勾，47/140。

- 2026-10-27 T034统一factory四场景：[普通/race各4PASS](../../docs/validation/p1-traced-faults.md)，同184冻结输入，11core均原pidfd退出/proc消失/无cleanup补信号，各5528唯一原件独审一致，vet通过。覆盖真实入口冲突回滚、授权一次重启、Stop落盘崩溃恢复、无授权不重启。旧Apply退出状态仅执行时核验的归档限制已说明，新版增加独立wait/stdout回执。已完成：该四场景；进行中：六个F11发布切点及其余矩阵；失败待决策：publication01候选分支误要求RecoverCall已修订并复验中，其余五分支通过但整轮失败保留，无用户决策项；环境不足未验证：真实会话/休眠和其他平台。T034未勾，47/140。

- 2026-10-27 T034发布/启动窗口增量：[六发布切点普通/race各7PASS](../../docs/validation/p1-traced-faults.md)，各18实际核心全退出，degraded夹具收尾各2次原pidfd信号如实记录；四active I/O切点普通5PASS、两启动窗口普通3PASS，race待补。prelaunch只声明未调用Start，postlaunch独立closed-owner/birth证明后显式恢复同一实例，均不造注册/退出事实。已完成：上述限定批次；进行中：runtime切点复验、回滚故障和其他矩阵；失败待决策：两个脚本收据错误已修正并保留，entry-opened证明角色已定向修正、实际复验中，无人工决策阻塞；环境不足未验证：真实会话/休眠及其他平台。T034未勾，47/140。

- 2026-10-27 T034运行时/回滚增量：十运行时切点普通11PASS、F13两回滚故障普通3PASS、degraded发布普通2PASS，各自冻结输入未变，race待补。已完成：上述限定真实批次与容量元数据来源/顺序组件（normal/race各56PASS、vet通过）；进行中：C01真实容量重放、其余race、归属/可选依赖/会话矩阵；失败待决策：无人工决策项，历史失败保留；环境不足未验证：真实会话/休眠及其他平台。回滚持久化失败保留rolling_back/pending，不能伪造degraded落盘。T034未勾，47/140。证据见 docs/validation/p1-traced-faults.md 与增量机器索引。

- 2026-10-28 T034继续：四I/O、两启动窗口、两回滚失败、外来监听器分别补齐冻结版本race；容量normal02真实6PASS，race02在300秒fixture预算结束前未启动末尾replay worker，失败保留、扩大测试预算后复验中。F14三个元数据场景normal4PASS，外来监听器normal/race各2PASS并证明子进程仍活；不冒称实际PID复用。F18固定build/launch依赖边界normal1PASS。实际专用VM S3休眠2PASS，197pins、1402非空原件独审一致，3payload交换同Run/原长连接、2core全退出无清理补信号；不含SSH/logout/OSshutdown。已完成：上述有限批次；进行中：剩余race、SSH/logout真实拓扑、实际PID复用与最终稳定审查；失败待决策：无人工决策阻塞，历史失败保留；环境不足未验证：其他平台，宿主KVM已确认且S3实测通过。T034未勾，47/140，见增量与会话机器索引。

- 2026-10-28 T034增量：已完成运行时十切点/degraded发布/F18/C01各自冻结版本race，11/2/1/6 PASS；C01 normal195输入与race197输入分列，原timeout失败保留。当前产品真实PID复用VM 2PASS，PID132/start1352→2066，Recover拒绝且新监听者仍活；197pins/1251原件独审通过，host PID namespace未改。进行中：F14元数据race、SSH/logout真实拓扑与最终稳定矩阵审查。失败待决策：无人工决策阻塞，历史失败不改标。环境不足未验证：其他平台与性能预算；Linux KVM可用，S3/PID复用均已实际验收。主代理负责锁/恢复语义；executor实际gpt-6.1-sol/medium、scout实际gpt-6-luna/high，无再派生。T034仍未勾，47/140。

- 2026-10-28 补记：F14 三个元数据场景 race 4PASS、六实际核心原 pidfd 退出/proc 消失；对应已启动批次的 race 已完成。SSH/logout 与完整覆盖/稳定审查仍进行中，T034未勾；无新增人工阻塞。

- 2026-10-28 生命周期补齐：F01前端两种退出、F03显式恢复及control-only空/运行状态，同197冻结输入normal/race各7PASS、九core全退出且无清理补信号。该批不含F02，下一步SSH/logout实际VM与稳定共享路径F02检查。T034未勾，47/140；无人工阻塞。

- 2026-10-28 SSH首轮保留失败：两个真实会话事件/原manager pidfd退出均完成；F04旧断言将公开只读unknown误解为独立Core必须不可观测，故两个子项及父项失败。root仅修订断言，仍保留activation_requires_management，不提升管理权威；须证明原完整身份/owner/ready并完成held/new转发，且全分支noMutation。201pin新快照仅lifecycle_test.go改变，实际复验中。该首轮只有断开前payload，不计连续性通过；无人工阻塞。

- 2026-10-28 T034完成：所需F01/F02/F03/F04/F06/F10/F11/F12/F13/F14/F18/C01及补充窗口场景均有接受的真实批次。SSH02普通VM 3PASS、201pins、2875导出/2734非空原件，七空引用仍不可用；4core全退出无补信号，6次4096B实际流量；checker16反例/旧失败拒绝、3834原文件未变、独审通过。最终201输入与live相同、taggedvet通过；S3新增无修改要求由既有原件离线五维比较验证，非重跑。已完成：[US5汇总](../../docs/validation/us5-lifecycle.md)，T034勾选，48/140；进行中：T035/T036测试定义。失败待决策：无，旧失败均保留；环境不足未验证：其他平台、PAM/logind差异、断电与性能。分批版本不合并声称一次全树执行，P1/项目未完成。root负责语义与最终验收，executor实际gpt-6.1-sol/medium、scout实际gpt-6-luna/high，无再派生。

- 2026-10-28 T035/T036定义完成：26个顶层测试覆盖来源身份/pin/恶意输入、逻辑组与有界调度；固定Go语法/格式通过，因未来生产符号缺失的RED明确保留，不计语义PASS。独立scout复核无定义矛盾，T035当前check-04新增来源重标记反例，accepted-pin.json为本次权威，旧final-pin.json/check-03留作历史。已完成50/140；进行中T037；失败待决策无；环境不足未验证仍为其他平台与既有边界。executor实际gpt-6.1-sol/medium负责先前定义，scout实际gpt-6-luna/high复核，root作最终验收。

- 2026-10-28 T037完成：严格v1意图+有界JSON/YAML纯解析，root复核后补数字literal128字节上限与Probe主机/端口边界；最终check-02 document普通/race各20PASS、Intent各39PASS、vet通过、7输入hash相符。旧收据保留，未触碰运行/store事务。已完成51/140；进行中T038/T039；失败待决策无；环境不足未验证其他平台/完整来源运行矩阵。executor为gpt-6.1-sol/medium，scout为gpt-6-luna/high；root最终验收。

- 2026-10-28 T038/T039完成：Linux显式Fetch普通/race各16PASS，有限解析/身份各157PASS、vet通过；固定core34组原始/归一化68次-t通过且binary未变。root修正native ALPN/SNI与WS ed隐含参数后重验受影响范围，旧146PASS与草案保留。完成53/140；进行中T040；失败待决策无；环境不足未验证其他平台/远端协议握手/T044真实来源线路，构造校验不充当转发。root方案/Fetch/core验证，executor gpt-6.1-sol/medium、scout gpt-6-luna/high。见us3-provider-modules及验证索引。

- 2026-10-28 T040完成：policy普通/race各73PASS、0FAIL/0SKIP，15顶层含T036六组，vet通过；source元数据按需校验、20k链/深环和独立指针验证，root稳定审查/hash通过。完成54/140。进行中T041，新增发现basic-v1明确只接受loopback HTTP/SOCKS/Selector且URLTest存在默认首选，已补入同一任务的版本化核心/策略快照/成功探测前拒绝前提，未扩大旧capability或勾选T041。失败待决策无；其他平台/新核心实际矩阵仍未验证。executor实际gpt-6.1-sol/medium；scout实际gpt-6-luna/high提供源码证据。

- 2026-10-28 T041进行中：新增sourcecontrol v2纯codec候选及sourceprofile严格生成配置/独立预期快照；Mihomo来源核心为1011文件固定基线的独立副本，旧二进制与旧证据保留。受保护原子探测模块normal/race各41PASS、0FAIL/0SKIP、vet通过；实际HEAD/503/超时/cancel已验证，0ms与溢出选择为synthetic，非远端协议握手。v1控制回归已通过；v2核心动态切换/重开及产品controller/compiler接线进行中。失败待决策无（已知hosts别名回绕缺陷已修，等待最终profile验收）；环境未验证：外部真实代理握手、其他平台，新核心尚未获approved capability。保持54/140，T041未勾选。executor实际gpt-6.1-sol/medium；scout实际gpt-6-luna/high提供独立wire/核心阶段审查。

- 2026-10-28 T041失败待修订记录：直接exec的dynamic build-01虽通过native13/race13有限核心场景，但真实US5 Apply在入操作前被OpenExecutable以PT_INTERP拒绝（READ_UNAVAILABLE、未创建product run）。root撤回该hash的能力登记，保留原approval/失败证据，显式CGO_ENABLED=0重建static候选并补ELF预检；不放宽旧launcher。编译器纯模块normal/race policy34+bundle4通过，Controller新54/旧126通过；当前T041仍进行中、54/140不变。

- 2026-10-28 T041有限模块完成：[来源编译验收](../../docs/validation/us3-compiler.md)绑定static c90d82候选。最终native13PASS、实际产品normal/race各1场（各2core/4转发/两公开Stop、原pidfd退出且无清理补信号），准入normal/race各49PASS；受影响宿主race186PASS/5旧core opt-in跳过、注册表race4PASS、vet通过。动态ELF拒绝及sandbox私有目录失败保留；独立静态命令/env/前后hash闭合，scout最终审查通过。已完成55/140；进行中T042；失败待决策无；环境不足未验证：新二进制完整故障矩阵/远端七协议握手/其他平台与预算。T043/T044、P1和完整项目未完成。root负责关键语义和验收，executor gpt-6.1-sol/medium、scout gpt-6-luna/high。

- 2026-10-28 T042模块完成：固定截止批次/实际开始预约、全局2worker/每来源1个、取消join及下载/验证/待应用分态，normal/race各20顶层42PASS/0fail/0skip，vet与最终独审通过；原T036测试hash未变。已完成56/140；进行中T043，[更新契约](contracts/subscription-update-v1.md)明确持久预约/不可变资产/原journal复用与无自动Apply。失败待决策无；未验证：跨崩溃调度与真实服务更新、其他平台。executor gpt-6.1-sol/medium，scout gpt-6-luna/high，root最终验收。

- 2026-10-28 T043基础模块增量：[来源持久化准备](../../docs/validation/us3-source-storage.md)覆盖原key重解析56PASS、资产33PASS、opaque设置29PASS、typed设置76PASS、初始化31PASS及inventory19PASS（各自normal/race），相关vet与独审通过；root新增source-assets/Runtime隔离，最终Paths+assets race115PASS。实际64MiB单文件，512MiB只测sparse metadata；不冒断电或真实update事务。早期地区kind/禁用Compact夹具失败保留。已完成仍56/140；进行中冻结下载输入与T043持久预约/来源复核/命令接线；失败待决策无；环境不足未验证完整更新故障矩阵/其他平台。

- 2026-10-28 T043 更新增量：[集成记录](../../docs/validation/us3-source-update.md)。已完成冻结Fetch normal/race各31PASS、context各112PASS、request各6PASS、result各78PASS及相关vet。进行中：异步Journal/worker/恢复与服务接线；实际文件/TLS六场普通与race、真实SIGKILL中断后不重投下载、实际Unix socket更新/轮询/失败/离线重放已分批通过，仍待稳定完整矩阵。首轮worker空config.ID导致Prepared编码失败（1PASS/5FAIL）已修，原件保留；独审发现阶段标志矛盾已修。失败待决策无人工阻塞；环境不足未验证仍为其他平台/预算。来源list、CLI、编译Apply与持久调度未完成，T043不勾，56/140。root负责事务与验收，executor实际gpt-6.1-sol/medium、scout实际gpt-6-luna/high。

- 2026-10-28 T043 有限更新/恢复/列表模块验收：冻结46输入，control normal/race各140PASS/36顶层、service完整race106PASS/34顶层、0FAIL/0SKIP，vet通过；包含真实HTTPS子管理器SIGKILL后只重放/显式interrupted、不重投，Publishing/依赖损坏/live/压缩恢复31项子矩阵，32+1完整分页与view变化过期、实际Unix socket列表和离线只读。原空SnapshotID编码失败保留。已完成上述限定模块；进行中CLI、来源编译Apply与持久调度；失败待决策无人类阻塞；其他平台/性能未验证。T043仍未勾选、56/140；查询Applied夹具为synthetic stopped bundle，不冒称真实Apply，参见us3-source-update.md及机器索引。

- 2026-10-28 T043 继续：CLI有限normal/race各42PASS、Plan reader各22PASS/9顶层、vet及独审完成。source Plan/API与原BasicApplier桥接、确认后工作目录准备及live source历史例外已接线；旧BuildPlan夹具/不安全/data祖先失败保留，修正后材料/历史/Coordinator race174PASS/23顶层0skip。持久pulse复用同一Journal预约，补齐manual-null-period的effective_next_due与status复制；调度9场race、service/CLI/schedule race112/59/42PASS，离线schema19组，最终control普通249PASS/63顶层及vet通过。已完成上述有限模块；进行中实际核心首次/替换/下载重叠Apply、最终组合race与独审、T044矩阵准备；失败待决策无人工阻塞；环境不足未验证其他平台/预算。synthetic调度bundle不充当实际Apply；T043仍不勾、56/140。root负责锁/事务/调度裁决，executor实际gpt-6.1-sol/medium、scout实际gpt-6-luna/high；详见us3-source-update.md。

- 2026-10-28 T043有限完成：post-F08真实SourceApply normal/race各1个多阶段场景（每场2个固定core、3次实际HTTP转发、1次held HTTPS跨Apply后input_changed，公共替换/Stop原pidfd退出且无cleanup信号），24pins与独审通过。F08空provider原成功语义已收紧为validation_failed保留旧Latest；历史空候选Plan/list/recovery拒绝，受影响control normal/race各71PASS、service/CLI race113/59、vet与10pins/独审通过。历史249组合与旧空fixture日志保留限定范围。总验收索引（本地原件 `../../experiments/gost-mihomo/us3-source-control-validation.json`，不随Git上传）不叠加重合测试。已完成57/140；进行中T044实际双来源US/JP/BEST、F08/F09/F32/SC-006及FR-015最小具体原因（发现泛化INVALID_ARGUMENT尚不足，已安排修正）；失败待决策无人工阻塞；环境不足未验证其他平台/资源预算/物理休眠。本阶段只支持首次start/运行中replace，no-op与stopped新配置显式unsupported，T139/T054及整个P1/项目未完成。root负责裁决与验收，executor gpt-6.1-sol/medium、scout gpt-6-luna/high。

- 2026-10-28 T044继续：[双来源矩阵记录](../../docs/validation/us3-sources.md)已建立。FR-015首阻断诊断完成定向normal/race：policy140/29顶层、control42/13顶层、实际Unix API13/2顶层，0fail/skip，vet通过；成功编译完整golden未变。provider/schedule normal/race各243/42、vet通过，只分别证明模块与内存调度，不替代核心线路。已完成上述有限检查；进行中独立诊断复核及真实双源US/JP/BEST/pin/F08/F09/F32/SC006矩阵；失败待决策无人工阻塞，旧夹具/断言失败原件保留；环境不足未验证其他平台/预算/物理休眠。仍57/140，T044不勾选。root负责关键判断；executor实际gpt-6.1-sol/medium、scout实际gpt-6-luna/high。

- 2026-10-28 T044矩阵复核发现并保留check-03真实FAIL：旧两轮67次origin GET仅有66条成功marker，新增逐请求token证明fixed-down在测试client 1s超时后、原端点恢复时经原A-US晚到；无跨源证据，但旧负例不能证明核心终末失败。root已裁决收紧负例等待明确代理失败，client deadline不得充当通过，逐origin GET校核；不改生产、不承诺取消传播已修复。已完成诊断独审；进行中矩阵增量；失败待决策已由root作上述裁决，无人类阻塞；环境不足仍其他平台/预算/物理休眠。T044继续未勾，57/140。

- 2026-10-28 T044完成限定验收：[双源证据](../../docs/validation/us3-sources.md)及索引（本地原件 `../../experiments/gost-mihomo/us3-sources-validation.json`，不随Git上传）。check04真实normal/race各1多阶段场景，每场3次Apply、66逐token origin GET/66线路marker、0nonapp/0direct、8明确拒绝+1终末poll、两源4h追赶各一次；公开Stop及原pidfd/proc结束，无cleanup补信号。27pins/独审闭合，旧check03 client1s超时后原pin晚到FAIL与旧67/66日志保留，未改生产取消语义。当前模块/诊断normal/race及vet见各自收据，不叠加重合计数。已完成58/140；进行中T045/T046规则/应用入口测试定义；失败待决策无人工阻塞；环境不足未验证其他平台/预算/物理休眠，HTTP CONNECT之外协议与应用直接node规则仍相应后续任务。root最终验收；executor实际gpt-6.1-sol/medium、scout实际gpt-6-luna/high。

- 2026-10-28 T045测试定义完成：规则定义索引（本地原件 `../../experiments/gost-mihomo/us4-rule-definition-validation.json`，不随Git上传）记录13顶层、gofmt/AST通过，因NewRuleSet等未来API缺失而预期编译RED，不称语义通过。独审补齐false先行后续true的索引/ID/目标与invalid-known事实不可被false兄弟遮盖，10pins/增量审查通过。已完成59/140；进行中T046实际app入口测试定义，T047须待T046定义就绪；失败待决策无人工阻塞，预期RED/旧语法工具误用记录保留；环境不足未验证其他平台/预算。named-app-entry契约已冻结独立不可变private输入、无隐式分端口/换凭据、完整Plan identity，实际绑定/回滚待T048–T051。root负责契约/验收；executor实际gpt-6.1-sol/medium、scout实际gpt-6-luna/high。

- 2026-10-28 T046测试定义完成：应用定义索引（本地原件 `../../experiments/gost-mihomo/us4-app-definition-validation.json`，不随Git上传）含4场景+2子进程dispatch、独立us4integration tag、gofmt/AST通过，未来app接口/专属批准工厂缺失的预期RED，未执行网络/core/kill。16pins/最终独审通过；初审负例误判已按空hop集合→origin violation→finish验证链纠正并保留说明。已完成60/140；进行中T047纯规则实现；T048另需解决源码证实DNS/IP unknown被原core化为false的风险、独立新app候选资格，不外推旧c90/T044。失败待决策无人工阻塞，旧RED与contract漂移原件保留；环境不足未验证其他平台/预算。root负责契约/准入和验收；executor实际gpt-6.1-sol/medium、scout实际gpt-6-luna/high。

- 2026-10-28 T047纯规则模块验收：索引（本地原件 `../../experiments/gost-mihomo/us4-rules-validation.json`，不随Git上传），全policy normal/race各42顶层/236含子测试PASS（规则13/96），0失败/跳过，vet通过；15 scoped pins/最终独审闭合。实现全部引用/失效pin前验、owned immutable规则、all false压制unknown但不隐藏invalid-known、首个决定性unknown阻断、明确default和输出克隆；不授予进程事实或core能力。已完成61/140；进行中T048核心资格/私有绑定。失败待决策：vm-01证明初版候选虽helper测试绿但无FlowID应用未进三值分派，root已修正启动模式接线，尚待新ELF重验；无人工阻塞。环境不足未验证其他平台/预算，旧RED/VM失败原件保留。executor实际gpt-6.1-sol/medium、scout实际gpt-6-luna/high，root最终验收。

- 2026-10-29 T048原生核心前置资格完成但整项未勾选：[有限验收](../../docs/validation/us4-app-core.md)，新6ec6静态ELF、1028源码/patch/rebuild/FD校验闭合；最终VM03候选8PASS、4终末拒绝0CONNECT/GET，旧c90同场3unknown失败保留。controller/source native normal/race与实际mode/hash验证通过；当前继续产品registry的Mode/Capabilities精确身份绑定、private app codec/Store/Plan集成。已完成仍61/140。失败待决策：vm01初版分派缺口已复现并修复验收；/tmp容量故障已用私有构建临时目录解决；无人工阻塞。环境不足未验证其他平台/预算，T051产品故障矩阵尚未执行。

- 2026-10-29 T048模块增量：registry 精确 mode/capabilities 身份 normal/race各244PASS，纯app compiler各278PASS，独立codec各333PASS，Store新19/既有45PASS、初始化39PASS各自有限normal/race与独审通过，所有旧失败保留。root继续完整Plan/capture/material接线，并复用初始化校验函数做后续增量验证（原初始化源码与审查另存，不把旧hash冒称当前）。已完成任务仍61/140；进行中T048/后续T049、T051准备；失败待决策无人工项；其他平台/预算未验证，T051仅tagged静态编译成功，未执行产品矩阵。来源设置v1不接受嵌入app entries；实际独立输入仍经显式初始化。

- 2026-10-29 T048有限模块验收：绑定与Plan索引（本地原件 `../../experiments/gost-mihomo/us4-app-input-validation.json`，不随Git上传），当前control normal/race各40顶层327PASS/0fail/0skip、vet0、22pins与最终独审闭合。完整canonical app record/实际parent settings/编译版本纳入generation，Apply重捕获在effects前；缺失/历史改写/设置丢失实际进入Apply后零写拒绝，不冒称成功runtime Apply。原initializer抽取前源码与收据保留，当前批重验。已完成62/140，进行中T049解释/T050影响；T051只有静态tagged编译准备，真实四场矩阵尚未执行。失败待决策无；/tmp容量有限采用repo构建与后续既有隔离VM，不放宽guard；其他平台/预算未验证。executor实际gpt-6.1-sol/medium实现codec/compiler与准备，scout实际gpt-6-luna/high独审，root负责锁/历史/准入/最终验收。

- 2026-10-29 T049纯静态解释完成：索引（本地原件 `../../experiments/gost-mihomo/us4-rule-explanation-validation.json`，不随Git上传）。完整policy normal/race各55顶层311PASS/0fail/0skip、vet0、16pins与独审通过；不改变原首匹配/unknown规则，完整authority/target前验共享；仅duplicate/exact-AND-subset遮蔽证明、4096pair/65536atom预算，缺引用invalid诊断与malformed零错误分开，实际证据固定not_observed。旧测试局部变量编译失败与早期RED保留。已完成63/140，进行中T050 Plan v2影响（保留精确v1读取/重放）、T051隔离VM准备；失败待决策无；环境不足其他平台/预算，未执行T051产品四场。executor实际gpt-6.1-sol/medium，scout实际gpt-6-luna/high，root裁决/最终验收。

- 2026-10-29 T050有限验收：影响索引（本地原件 `../../experiments/gost-mihomo/us4-impact-validation.json`，不随Git上传）。当前host组合normal/race各62顶层589PASS/0fail/0skip、vet0、24pins与独审闭合；隔离VM实际c90 ELF/Plan各12顶层37PASS，2567编译输入相同，guest/QEMUexit0且无强制收尾，VM独审通过。精确v1读取/旧请求重放保留，新source Apply v2 gate在replay之后；上一代取actual active bundle，完整并集/移除app/准确回滚identity，unknown不冒空元数据。只打开ELF不执行core，prior运行态为标明的metadata fixture，T051未执行。已完成64/140；进行中T051证据保留与真实产品矩阵。失败待决策无；旧Unicode oracle失败保留，/tmp容量通过隔离VM解决，其他平台/预算未验证。root负责capture/准入/最终验收，executor gpt-6.1-sol/medium，scout gpt-6-luna/high。

- 2026-10-29 T051真实normal-01失败（完整私有现场保留）：4场中2通过、2失败；规则顺序与管理器SIGKILL持续转发通过，正常manager退出后的测试子进程回收遗漏，及候选在closed登记前退出缺少安全自动回滚证明。已完成仍64/140，T051进行中；失败待决策已由root裁决按同次直接子进程Wait+原始intent/birth证明补齐，不改核心批准hash或放宽信号准入。修复和复验尚未完成，race未运行。环境不足其他平台/预算；当前无人工阻塞。另/tmp容量由旧缓存逐项SHA校验迁移恢复，非删除失败证据；原normal-01与2581源码快照保留。

- 2026-10-29 T051完成：有限真实验收（本地原件 `../../experiments/gost-mihomo/us4-app-runtime-validation.json`，不随Git上传），fixed normal/race各4实际场+2helpers PASS，35 origin token/0non-app，各342项完整导出，2584输入不变、无race/应急清理错误；独审闭合。端口占用在closed登记前退出由同次直接子进程Wait+intent/birth持久证明安全回滚，重开manager后公共recover不增run；旧registered信号准入不变，未完整开放rollback前崩溃仍degraded。fixture回收及OperationID契约预期修正有独审，旧2PASS/2FAIL现场完整保留。control普通/race各172PASS、runtime各49PASS、vet0；policy16pins未变沿用T049311PASS。已完成65/140，进行中T139测试定义/最小CLI与只读doctor；失败待决策无，环境不足其他平台/资源预算，T051无计量app上下文不冒充原进程身份或整个项目完成。

- 2026-10-29 T139进行中：最小CLI契约冻结；root实现只读doctor及统一query ID仅关联语义，control普通/race各4PASS、service各23顶层62PASS与vet通过；离线诊断Schema3meta/32合成向量通过。Plan输出独立原子新文件模块普通/race通过，CLI参数接线和真实旅程仍待完成；执行者正在先定义真实CLI缺实例/草稿/提交/回滚场景。已完成仍65/140。失败待决策无；缺API预期RED及私有schema checker未启用日期校验的失败保留，均非运行产品失败；环境不足其他平台/资源预算。

- 2026-10-29 T139完成：[最小CLI实际验收](../../docs/validation/minimal-cli.md)，normal/race各3真实场PASS、53次CLI调用、266完整证据项，公共Plan/Apply、只读doctor/status、离线精确重放及端口占用完整回滚通过，独审闭合。最终2768构建输入无漂移，初2750漏race专用列举的18项补充有原件记录；真实30份doctor响应Schema通过。control/service/CLI/output有限模块普通/race与vet通过（集合不累加）。已完成66/140，进行中T052环境测试定义与T053生命周期边界；失败待决策无，环境不足其他平台/资源预算。显式手工前置不冒充T054 init、T058真实应用接入或P1完成。root最终验收，executor gpt-6.1-sol/medium、scout gpt-6-luna/high。

- 2026-10-29 T052定义完成：有限测试索引（本地原件 `../../experiments/gost-mihomo/us2-environment-test-definition.json`，不随Git上传），root冻结child-environment-v1有限变量/bypass/隐私接口，executor定义8顶层行为测试，gofmt与显式AST通过，真实编译因未来API缺失预期RED且保留；scout稳定独审无阻塞。已完成67/140，进行中T053 argv/PTY/信号/退出/后台后代边界；失败待决策无（RED非产品失败），环境不足其他平台及真实应用接入尚未执行。未写环境实现，不把定义当T055/T058语义验收，也不声称系统全局代理审计。

- 2026-10-29 T053进行中：实际PTY/信号私有原型normal03/race01各4场观察通过，但独审发现Cmd.Wait先reap再按数字PGID恢复终端的复用窗口；root已裁决改为原pidfd观察退出、原child保留zombie期间核对会话/foreground并恢复，然后唯一Wait/close，正在独立批次补验。旧普通/race结果与两次环境/驱动解析失败原件均保留，不能冒充正式T053/T058或完整job-control验收。已完成仍67/140；失败待决策无（修订方案已确定），环境不足其他平台/资源预算，完整launch记录与派发屏障仍待实现。

- 2026-10-29 T053机制补验完成但任务仍进行中：修订TTY spike normal04/race02各4场PASS、独审闭合；四个实际TTY样本在restore前后均为原PID/start/PPID/stateZ，事件顺序poll未reap→核对session/foreground→restore→唯一Wait→close。rawpidfd TERM不广播同组sentinel、背景stdio不阻wrapper退出均复核。只接受普通协作session有限原型，不承诺跨进程原子TTY CAS或产品持久派发。正式真实CLI测试定义正在编写，完成计数仍67/140；失败待决策无，其他平台/资源预算未验证。

- 2026-10-29 T054准备口径澄清（未勾选/未实现）：保留T033已公开的人类control-only空Store初始化，file-store既有“显式初始化/写打开”边界不变；新增init负责draft/应用输入/launch历史空间，空control-only不等于配置就绪，query/run不得建缺失空间。task“不发布active”细化为不发布已应用generation或running意图，与已有空stopped初始权威一致。撤销私有准备笔记中拟删除冷control-only行为的提案，避免无需求的生命周期行为变更；仍67/140。

- 2026-10-29 T053定义完成：真实run测试索引（本地原件 `../../experiments/gost-mihomo/us2-launch-test-definition.json`，不随Git上传），5类实际CLI定义、独立应用/PTY夹具冻结；AST/gofmt与fixture argv/IO smoke通过，完整tagged编译为未来launch元数据API缺失的预期RED。两处独审缺口已补精确拒绝诊断与exec-format126/持久原因/无shellfallback，最终增量独审闭合；真实Exec失败来源仍需T056源码/运行证据，不仅靠黑盒输出。独立TTY修订原型normal04/race02各4场有限通过，不能替代T058。已完成68/140；进行中T054 init/launch历史安装，失败待决策无，环境不足其他平台/资源预算及真实run产品尚未执行。

- 2026-10-29 T054完成：[本地init验收](../../docs/validation/us2-local-init.md)普通/race各13/131存储、15/46控制、6纯输入、3CLI模块PASS；真实cmd/hoopa各8调用符合预期，338冻结输入未变、vet0、最终独审闭合。修正umask发布前实际权限校验、已有launch历史预检前置及本地null成功报告退出码；旧cwd/fixture、CLI失败与修前审查保留。已完成69/140；进行中T055子环境，之后T056持久派发/TTY与T058实际run。失败待决策无；环境不足未验证其他平台/资源预算，未冒称init启动了代理或完成应用产品。

- 2026-10-29 T055完成：纯子环境模块（本地原件 `../../experiments/gost-mihomo/us2-environment-validation.json`，不随Git上传）普通/race各10顶层44含子PASS、vet0、原T052八顶层测试未改、独审通过。新增有效空env显式[]避免exec隐式继承；opaque私有指针+Format阻止普通fmt异常verb回退泄露，旧RED/失败保留。已完成70/140；进行中T056阶段原件/独立锁/匿名字节载荷/派发屏障与TTY，失败待决策无；真实子进程与客户端代理效果尚待T058，其他平台和资源预算未验证。

- 2026-10-29 T056/T057完成：[应用启动与CLI有限验收](../../docs/validation/us2-application-launch.md)，固定真实cmd/hoopa普通/race各5场PASS、0FAIL/SKIP；每mode27管理调用/11终末/5实际GET，原core/entry与旧连接不被新run重载。阶段原件/字节载荷/原pidfd/延后TTY handoff及只读inspect均有组件与实际证据，稳定独审通过。两次Python观察器自身SIGHUP失败及修订原件保留，最终完整矩阵另列。已完成72/140；T058受控矩阵已通过，完整首次配置补验中；真实Agent请求未执行，自动审批拒绝使用现有认证向外部服务请求，待用户具体确认。其他平台/资源预算未验证，P1和全项目未完成。root最终验收，executor gpt-6.1-sol/medium、scout gpt-6-luna/high。

- 2026-10-29 T058局部进展：[首次配置验收进度](../../docs/validation/us2-onboarding.md)，普通normal02/race01完整纯本地CLI旅程通过，包含真实init及managed HTTP，旧driver收尾失败保留。真实Agent外发被自动审批拒绝、尚未执行，等待具体用户确认；T058未勾、仍72/140。其他平台/预算未验证。
