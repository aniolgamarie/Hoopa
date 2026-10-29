# 项目任务记录

日期：2026-10-21。范围：基于用户约束、当前仓库与上游资料制定结构、设计、规范和原型验收方案；本次不执行真实代理组合原型。

任务状态统一使用：**已完成、进行中、失败待决策、环境不足未验证**。后续尚未开展的产品阶段另见路线图，不冒充进行中或验证通过。

| 工作 | 状态 | 证据或后续动作 |
| --- | --- | --- |
| 仓库基线检查 | 已完成 | 初始只有 README.md 与 MIT LICENSE，无代码、go.mod、既有详细设计；起点提交 4083208 |
| Mihomo / Clash Verge Rev 资料核对 | 已完成 | 独立 scout 核对官方文档与源码，事实、推论与待实测项记录在 references.md |
| GOST 计量与配置资料核对 | 已完成 | 独立 scout 核对指标方向、标签、计数包装与精度边界，未将可变源码当作固定 release |
| 架构、账本、原型与开发规范 | 已完成 | 主代理编写设计、计量、原型及资料文档；executor 完成 development.md；README 提供导航 |
| 最终内容审查 | 已完成 | 两个 scout 审查稳定正文；补齐拒绝回退、provider 探测及启动门槛，修正 series 汇总歧义并复核闭合；主代理验收 |
| 文档静态检查 | 已完成 | 7 个 Markdown 文件、19 个本地链接通过；代码围栏、文件结尾、已有 diff 和新增文件空白检查通过 |
| 默认沙箱中的最后一次复核 | 环境不足未验证 | 沙箱建立临时挂载目标时报告 ENOSPC，检查程序未启动；只读检查确认根分区可用空间为 0，改用不创建该挂载的只读执行路径复核 |
| 真实应用 GOST + Mihomo 组合原型 | 环境不足未验证 | 本次未准备固定版本核心、测试订阅和目标应用验收材料；交付原型方案，不将资料阅读算作运行验证 |
| macOS / Windows 生命周期与休眠验收 | 环境不足未验证 | 本地为 Linux，尚无真实平台执行记录或托管 CI 结果 |

当前没有“失败待决策”项。SQLite 驱动、进程托管和热重载等尚待实验裁决的选择列在 design.md，不是本次发生的执行失败。

## 执行分工

- 主代理：沿用会话模型与 effort，负责方案取舍、事务与恢复语义、文档写入和最终验收；未切换模型。
- 两个独立 scout：工具配置为 `gpt-6-luna / high`，分别搜集 Mihomo / Clash Verge Rev 与 GOST 证据，不写文件、不再委派。
- 一个 executor：工具配置为 `gpt-6.1-sol / medium`，独占编写 development.md 并验证其链接与空白；在资料搜集完成后执行，未超过两个同时运行的子任务。
- 运行原型、Go 编译和性能基线尚未执行；仓库当前没有可运行实现。

## 验收证据

- 主代理执行 `git diff --check`，并对全部 Markdown 逐文件执行 `git diff --no-index --check -- /dev/null <file>`，已有与新增内容均无空白错误。
- 主代理的 Python 静态检查确认 7 个 Markdown 文件、19 个本地相对链接有效，代码围栏配对且文件以换行结束。
- executor 对 development.md 的 6 个内部链接及空白独立验收；执行日志保存于 `/tmp/hoopa-development-check.log` 与 `/tmp/hoopa-development-whitespace.log`。
- 最终修改范围：更新 README，新增六份 docs 文档；未添加实现、依赖、CI 工作流或运行结果。

## 功能规划补充

用户认可八个功能模块与使用流程建议后，授权记录到仓库文档。范围包含新功能规划、关联文档同步，以及“构建产物不提交 Git”的说明；仍不实施 CLI 或运行代理原型。

| 工作 | 状态 | 证据或后续动作 |
| --- | --- | --- |
| 功能规划新文档 | 已完成 | executor 编写 features.md，覆盖八个模块、四项 P1 流程、状态与 TUI 边界；主代理按审查结果澄清 init 的显式更新边界 |
| 导航与设计同步 | 已完成 | README、design、prototype、development、references 同步入口、P1 验收和源码/产物边界 |
| 稳定正文审查 | 已完成 | scout 对写完正文检查约束；仅发现 init 更新授权有歧义，主代理修正后验收其与设计的一致性 |
| 文档静态检查 | 已完成 | 8 个 Markdown 文件、27 个本地链接，代码围栏、文件结尾、已有 diff 与新增文件空白检查通过 |
| 真实应用、性能与跨平台验证 | 环境不足未验证 | 本轮仅记录产品规划，无新的运行验收结果；既有 P0/P3 未验证状态保留 |

本轮无失败待决策项。主代理沿用会话模型与 effort；executor 使用角色配置 `gpt-6.1-sol / medium`，只读审查的 scout 使用角色配置 `gpt-6-luna / high`。文档工作各有独占写入范围，原有设计正文仅补充本次相关内容。根分区现已恢复可用空间，本轮可正常执行静态检查；上一轮沙箱失败记录保留。

executor 独立检查新文档模块数、4 个相对链接和空白，日志为 `/tmp/hoopa-features-check.log`。主代理复核稳定正文并对全部 Markdown 执行本地链接、围栏、文件结尾和逐文件 `git diff --no-index --check` 检查，同时执行 `git diff --check`；未运行不存在的 Go 工程测试。

## 进程与域名流量需求补充

用户明确要求进程级（PID、进程名）、域名级查看、同名进程合并、排序与完整统计。本轮记录为 P2 必需能力，并将源身份、短连接及对账可行性加入 P0 门槛；保留 Hoopa 计量入口与已验证 TCP 的范围。

| 工作 | 状态 | 证据或后续动作 |
| --- | --- | --- |
| 上游可见性核对 | 已完成 | scout 核对 Mihomo API/metadata/进程查询及 GOST 转发上下文；活动快照、源 PID 和转发后身份存在能力边界 |
| 功能要求更新 | 已完成 | executor 更新 features.md，补进程/域名、合并、排序、筛选、下钻及未知项要求 |
| 计量模型与验收同步 | 已完成 | 主代理新增 traffic-statistics.md，更新入口/源进程分层、逐 launch 入口、事实/对账/缺口和 P0/P2 验收 |
| 稳定正文审查 | 已完成 | scout 对本轮稳定正文未发现阻断；主代理验收源进程身份、socket 共享、域名互斥分桶、同名聚合和对账语义 |
| 文档静态检查 | 已完成 | 9 个 Markdown 文件、39 个本地链接，围栏、文件结尾、已有 diff 及逐文件空白检查均通过 |
| 连接级采集与真实进程/域名归属 | 环境不足未验证 | 无新的固定版本原型执行；不能把 metrics 或活动快照当成已验证的完整维度账本 |

主代理沿用会话配置，负责归属、事务与对账取舍；executor 使用 `gpt-6.1-sol / medium` 独占更新 features.md，scout 使用 `gpt-6-luna / high` 只读核对来源；子代理未再派生。本轮没有执行失败待决策项。采集适配方式尚待 P0 裁决，不等于已实现或已验证。

executor 的 features.md 需求锚点检查通过，日志为 `/tmp/hoopa-features-traffic-check.log`；主代理执行全部文档的相对链接、围栏、换行和 `git diff --no-index --check`，并执行 `git diff --check`。故障矩阵新增 F20–F25，共 25 项；这些是待执行验收，未记录为运行测试通过。

## 访问质量与应用进程路由补充

日期：2026-10-22。用户先要求进程访问延迟、单位时间流量与连接节点，再要求按应用/进程选择节点或 group；两项合并处理，保留已有流量完整性和基础管理独立要求。

| 工作 | 状态 | 证据或后续动作 |
| --- | --- | --- |
| 上游计时与路由构件核对 | 已完成 | 两个 scout 分别核对延迟/实际链、命名入站/进程规则；官方字段不等于已通过组合验证 |
| 用户功能要求更新 | 已完成 | executor 独占更新 features.md，明确 P1 应用定向、P2 原进程规则和访问质量 |
| 质量与路由设计同步 | 已完成 | 主代理扩展 traffic-statistics.md、新增 routing.md，同步入口、提交、规则、事实和阶段验收 |
| 稳定正文审查 | 已完成 | 两个 scout 分别审查质量与路由；主代理补清转发计时起点/DNS边界和固定 provider+node pin 不随活动订阅重绑定，并验收最终正文 |
| 文档静态检查 | 已完成 | 10 个 Markdown 文件、54 个本地链接通过；围栏、文件结尾、已有 diff 与逐文件空白检查均无错误 |
| 实际质量采集与应用/进程选路 | 环境不足未验证 | 未运行固定版本原型，计时事件、历史链、身份三态与多个入站的启动恢复仍须实测 |

本轮主代理沿用会话模型与 effort，负责计时、规则顺序、身份缺失及提交恢复决策；executor 配置 `gpt-6.1-sol / medium`，两名 scout 配置 `gpt-6-luna / high`，各自独立任务均未再派生。并行阶段最多两个子任务，写入文件有独占范围。当前无执行失败待决策项；未验证能力不能记录为通过。

executor 对 features.md 的 17 项需求锚点与空白检查通过，日志为 `/tmp/hoopa-features-routing-check.log`。主代理验证全部本地文档链接，执行 `git diff --check` 及逐文件 `git diff --no-index --check`；新增质量/路由验收 F26–F33，故障矩阵共 33 项，均为待执行场景，没有记录为实际测试通过。

## P0 实施启动

日期：2026-10-22。用户明确调用 `$speckit-implement`，按 [任务清单](../specs/001-hoopa-proxy-manager/tasks.md) 执行；后续阶段受 P0 门槛约束。

| 工作 | 状态 | 证据或后续动作 |
| --- | --- | --- |
| 实施前置与需求清单 | 已完成 | 前置脚本定位当前特性；requirements.md 16/16 已勾选，只读核对，未改检查清单 |
| T001–T003 最小工程基础 | 已完成 | module 精确结构、63 个忽略/可见路径示例、7 个本地链接及空白检查通过；未把空 module 记成构建通过 |
| 当前工具链、固定核心与真实应用材料 | 已完成 | 已在 /tmp 核验并运行官方 Go 1.27.3；实际使用 Mihomo 1.19.26、GOST 3.3.1、Codex 0.163.0。未修改系统工具链或旧代理，核心官方资产来源尚未核验 |
| T004 标量与边界测试 | 已完成 | 固定 Go 1.27.3 下 test/vet/fmt 通过；精确 2^53+1、范围溢出、身份/时间边界和失败原值不变有测试 |
| T005 私有实验沙箱 | 已完成 | 4 项测试通过：目录/权限/独立端口、外部身份拒绝、替换目录拒绝、symlink 目标保护及幂等清理；vet 通过 |
| T006–T007 证据与真值基础 | 已完成 | 9 项 Evidence/Fault/Fixture 测试、vet/fmt及可见性校验通过；parent directory Sync失败不报告完成，事件不确定后poison；root补齐publishing持久边界并定向通过 |
| T008–T010 最小组合原型 | 已完成 | 身份/流量定义先行；真实两核21项校准与9项认证通过；已核验Agent版本。G01–G07不由此放行 |
| T011 真实 Codex 正常/持续工作流 | 已完成 | exit0、应用完成响应、GOST计数增长和实际UPSTREAM连接成立；子进程NO_PROXY为空，父代理变量前后不变，按强身份+pidfd清理两核 |
| T012–T017 部分探针与数据源整改 | 进行中 | 严格oracle通过；原生recorder收到20条终末整数事件，首投503窗口内未重见该SID，但缺源身份/持久序号/计时起点。首次ADR已记录，完整矩阵、替代设计与重跑未完成 |
| 当前事件源候选 | 失败待决策 | [ADR 001](adr/001-event-source.md)否决其完整事件源用途；[ADR 002](adr/002-p0-core-gates.md)不放行P1。不能把GOST PID当原应用，不能把callback视为持久replay |
| 未运行或环境不足项 | 环境不足未验证 | SSH/注销/休眠、完整100短流/回滚/路由、IPC保护、Schema元校验、SQL驱动、性能规模与macOS/Windows；未启动P1/P2/P3 |
| 可恢复执行问题 | 已完成 | 官方Go入口TLS失败改官方直达并校验摘要；默认sandbox禁socket后获准回环执行。真实Agent取证脚本Decimal/空数组处理错误已修正重跑；跨工具会话保留核心未存活原因仍待隔离 |
| 稳定源码最终验证 | 已完成 | Go1.27.3：gofmt无输出、vet通过；全量race32顶层=31通过+1默认集成skip，无竞争；另显式固定核心集成1通过/0skip，仍断言所有G gate未关闭 |

主代理沿用会话配置，负责进程身份、事务/锁/恢复取舍及最终验收；executor 角色配置为 gpt-6.1-sol / medium，scout 为 gpt-6-luna / high。子代理不再派生，最多两个子任务；tasks.md 与本记录由主代理独占维护。

工具链来源为 [Go 官方下载](https://go.dev/dl/)与官方 dl.google.com 直达下载；Go 1.27.3 linux-amd64 归档 SHA-256 为 `65efbc80fdc1fa2cd2631cad5b481777a68e8725f34e89f50fc6ac714e379b40`。本地只使用临时目录中的工具链，GOTOOLCHAIN=local、模块代理关闭；尚未引入第三方 Go 模块。

最终测试日志：`/tmp/hoopa-final-{fmt,vet,race,integration}.log`。实际工作流、私有目录与原始证据摘要见[P0工作流](validation/p0-workflow.md)和脱敏JSON（本地原件 `validation/p0-2026-10-22.summary.json`，不随Git上传）。公共材料不包含真实目标、原始标签值、凭据或Agent内容；原始文件留私有目录，可能随临时目录清理而不可用。

本轮主代理沿用会话模型与effort，未声称切换模型；executor实际按角色配置gpt-6.1-sol / medium编写限定源码并运行最终验证；scout按gpt-6-luna / high只读审查稳定源码及运行证据。scout首次计数提取遗漏一条series，主代理用result总量发现不一致后定向要求按完整标签重算，已更正，未将差异误报为GOST故障。所有Go写入停止后完成最终race/集成，后续只更新证据文档与任务状态。

最终独立文档审查通过，未发现实际结果、任务勾选和门槛裁决冲突。主代理核对19份主文档的122个本地链接、围栏/换行、所有当前Go文件空白、121任务中仅T001–T011勾选，以及公共摘要字段边界；均通过。随后澄清实验README的沙箱测试需要loopback权限，并同步规划时态，按受影响文档复核；未改Go源码。after_implement检查确认 `.specify/extensions.yml` 不存在，无注册后置hook。全部原先保留核心的PID/starttime核验均显示原进程已不存在。

## 事件源修复与重规划

日期：2026-10-22。用户明确要求修复三个阻断或重新规划。技术路线按[ADR 005](adr/005-telemetry-replan.md)改为同一Mihomo数据面产生身份/计量/计时/实际链；保留完整进程/域名要求、计数边界和P0门槛，未修改宪章/检查清单，未建立核心fork。

| 工作 | 状态 | 证据或后续动作 |
| --- | --- | --- |
| 接收事件journal（T122） | 已完成 | 9项定向race测试通过，原文整数不经float；完整Write+Sync才确认，不确定后poison/重开按原ID消歧，坏帧不截尾，单writer锁 |
| 原始入口身份探针取证（T123） | 已完成 | 原入口tuple/inode/PID/start/comm/boot限定实现；共享FD和越界拒绝通过；取证完成不代表身份能力通过 |
| 100同名来源连接断言 | 环境不足未验证 | 初次实测失败于同UID进程FD目录权限，保留原因；不跳过不可见进程来认定唯一。现在此条件明确skip/unverified；跨UID/FD转移/IPv6亦未证明 |
| 原生recorder→journal（T124） | 已完成 | 真实固定两核21校准/9认证通过；20条已送达事件落盘并重开一致，1次落盘后ACK503；未观察到原生重投，去重由受控单测证明 |
| 同源架构/契约/任务同步（T125） | 已完成 | specFR033区分source/receipt，plan/research/data-model及设计同步；追加T122–T129，129任务中完成15，不变更旧任务ID |
| 完整来源候选 | 失败待决策 | stock接口缺源PID/start、durable lifecycle及setup事件；后续T126先准备具体观测点/改动和许可审查，核心fork/发行未获自动授权 |
| 下一原型与重跑（T126–T129） | 进行中 | 下一执行包已列出：上游能力/观测点、G01身份、G02源恢复、G03/G05同连接线路/质量；当前没有后台启动生产CLI/TUI |
| 代码验证 | 已完成 | 固定Go1.27.3：gofmt/vet通过；全量race顶层46通过、2跳过、无FAIL/竞争；sourceidentity/telemetry/config缓存复用，gost-mihomo本轮执行 |

完整进程/域名、原生source spool、setup/chain能力和G01–G05仍未完成；接收20记录恢复不冒充源端零损失或生产账本。现有GOST结果继续按旧边界保存，不与新Mihomo代理流候选混算；新边界read/write/预读/lazy-handshake开销尚待冻结。日志与私有证据索引见[重规划验收](validation/telemetry-replan.md)。

主代理沿用会话配置负责身份、锁/日志/恢复与方案裁决；executor实际角色配置gpt-6.1-sol / medium实现限定journal和接线，scout为gpt-6-luna / high核对固定源码并审查稳定内容，子代理未派生。最终源码审查提出epoch不要混用当前collector run，按核心自身持久身份取hash修订；observer整体unverified与journal子passed有意区分来源观察和持久化，不提升G门槛。

最终core身份hash修订后，Observer定向race8项通过、vet/build/fmt通过；新版真实固定核心再次21校准/9认证通过，20已送达事件重开一致，1次落盘后ACK503，未保留运行核心。最终原始目录为 `/tmp/hoopa-p0-3253506149`，脱敏摘要已更新；未因单路径修订重复全量测试。源码写入结束后完成30份Markdown/196本地链接、所有实验Go空白、JSON解析以及129任务/15完成/依赖无环检查；P1与SQL前置T018仍保留。scout对最终稳定ADR/契约/规格与任务的定向复核通过：没有把候选增强写成stock已有能力，没有以接收恢复替代源完整或绕过授权；主代理完成最终验收。

## 重规划后的任务修订与只读分析

日期：2026-10-22。用户要求修订tasks后重新执行 `$speckit-analyze`。主代理修订任务、计划中的对应顺序及本记录，保留129个任务ID和15项已完成标记；T012–T016改用同源候选并消费T127–T129既有run，T017区分启动与完成条件，T126明确提案不等于可运行候选；补齐T069/T072/T073/T077/T100的source/receipt位置、final去重、接流前durable opened及flow/attempt线路绑定，更新R1执行顺序。未改源码、宪章或检查清单。

已完成：任务修订；进行中：冻结文档后的只读一致性分析；失败待决策：原生完整事件源能力仍未实现，非本轮Go测试失败；环境不足未验证：沿用原100来源权限限制和未运行故障/平台项。本轮不重复Go或网络测试，按文档依赖、需求覆盖、链接及空白核验。scout（角色配置gpt-6-luna / high）独立核对US7–US9遗漏及计划顺序，主代理判断和写入；子代理未派生。只读分析发现plan把T127–T129写成预登记前置、故障索引未列新探针，以及T128的SQL阶段表述含混；已在用户授权的修订阶段分别改为T122–T126准备后预登记并执行T127–T129、补齐故障/门槛引用索引、明确SQL由T019及P2事务任务后续验收。随后对稳定正文重新开始只读分析；分析过程不写文件，最终结论直接回报。

## T126 同源事件候选准备

日期：2026-10-22。用户再次调用 `$speckit-implement`，requirements.md只读门槛16/16通过；按任务依赖从T126继续，未修改检查清单或宪章。

| 工作 | 状态 | 证据或后续动作 |
| --- | --- | --- |
| 固定源码、二进制身份与观测点 | 已完成 | 官方v1.19.26→fc8c5a24b16991f98cd736950c17d1aa306a5041；16份已审查文件（含LICENSE/go.mod）SHA核对及干净源码状态通过。现有二进制revision一致但dirty，等同干净标签源码未验证；原运行结果仍绑定原binary SHA |
| 原生计数帧解析准备 | 已完成 | experiments/mihomo-events/counter_event.go严格13字段，拒绝缺失/重复/转义重名/未知/null/额外JSON和非精确数字；source_seq与receipt_seq分开，不声明跨帧连续或完整来源 |
| 提案与许可/维护范围 | 已完成 | source-review/source.lock及upstream-proposal明确原socket证据、三值规则、逐attempt计时/实际链、buffer/peek边界与有界源日志顺序；核心GPL version3与Hoopa外部进程边界分开，不构建或发行fork |
| T126完整候选 | 进行中 | 没有可应用核心补丁或可运行增强候选，T126保持未勾选；下一执行需决定是否授权按具体提案实施并构建隔离实验核心 |
| stock完整来源候选 | 失败待决策 | 原生字段/快照缺口仍存在；不能借codec、首个owner或当前group填补；继续保留ADR001/002不放行结论。隔离核心的具体选择待用户决定，不记为本轮测试失败 |
| 100来源/完整故障与平台矩阵 | 环境不足未验证 | 未重跑原权限受限来源探针；T127–T129未启动，T018未通过，P1/P2/P3未启动 |
| 定向代码验证 | 已完成 | 固定Go1.27.3，executor按先测试后实现执行race：5顶层+111子测试通过、0skip/FAIL/竞态；vet/gofmt通过，无新增模块、Mihomo库导入或核心运行 |
| 最终稳定内容复核 | 已完成 | scout只读复核codec/契约/源码事实/提案，未发现确定问题；主代理核对来源与接收分离、候选能力边界、任务状态、16源码摘要、本地链接及空白 |

源码和候选说明见[T126准备](../experiments/mihomo-events/README.md)与[具体提案](../experiments/mihomo-events/upstream-proposal.md)。当前129任务/15已完成保持；未把准备子项等同T126完成。严格counter profile只是候选计数子集，尚未接线源端或receiver，不增加生产API保证。

主代理沿用会话模型与effort，负责身份、持久化/恢复顺序、范围与最终验收；executor角色配置gpt-6.1-sol / medium独占counter_event.go及测试，scout角色配置gpt-6-luna / high独立取证及稳定内容审查，最多两个子任务且未再派生。源码审计首稿将match段误写成tracker行号，主代理根据提案上下文发现后要求scout定向更正为tunnel.go:640–674，最终公开材料已用正确位置。验证日志为 `/tmp/hoopa-counter-event-{race,vet,fmt}.log`，临时日志可能被清理。

## 隔离原生候选实施与 G01 阻断

日期：2026-10-22。用户在上一轮具体提案与隔离实验授权问题后再次调用 `$speckit-implement`，按已提交隔离范围继续实施/构建，无重复确认；requirements清单只读16/16通过。原用户核心、父shell/系统设置未修改，无默认特权、TUN或服务，未发行fork。

| 工作 | 状态 | 证据或后续动作 |
| --- | --- | --- |
| T126可运行候选及审查材料 | 已完成 | 干净fc8c5a24b16991f98cd736950c17d1aa306a5041的私有副本，16文件可应用补丁；固定Go1.27.3构建独立版本hoopa-p0-fc8c5a2，来源/补丁/binary/能力manifest及一条真实smoke。完成不代表完整能力通过 |
| 来源WAL与计数/规则钩子 | 已完成 | Write+Sync后source_seq，独立终末及维度gap容量、opened写前失败回滚、IO不确定poison；sourceepoch绑定core身份；只读回放模式保持旧日志可消费。wrapper禁绕过、Bind先于Join、计数终止/final尝试先于Leave；原stock路径保留 |
| 最终源码定向验证 | 已完成 | 新来源/规则/包装/TCP钩子23顶层race通过，0skip/FAIL/竞态；vet/format/构建成功。发送端503重投原ID/原文；没有把此断言当完整G02。Hoopa计数codec的5顶层/111子用例定向race与vet另通过 |
| T127身份/短流矩阵 | 进行中 | 已定义显式native identity集成测试并执行100预登记短流；其余PID复用/共享/转交/子孙矩阵未完成，任务保持未勾选 |
| G01严格身份断言 | 失败待决策 | 原始PID+名称+start为0/100，100条owner_visibility；两同名进程/两域名/100truth、opened、closed均在，终末字节和域名100/100。实际Go测试exit1，不改分母、不改成skip/通过 |
| 当前环境可见性 | 环境不足未验证 | 当前普通权限无法证明完整owner集合，源字段明确unknown；未改变系统权限。跨平台/SSH/休眠及原未验证矩阵保留 |

T128/T129因T127失败未启动，T012–T018未关闭，P1/P2/P3未推进；不将这些未执行任务记为测试失败或环境验收通过。

最终二进制SHA为76e0e114ac9a669ec0eb131f35ead972d3e24b2dceeb270e72a3e879f5b9c32e，补丁SHA为e871aea91fdad62647d8f81ba146e799279616c3d64b53d0768b52457faed0d3；16文件补丁apply check成功。23项仅为本次相关断言，不是上游全量或整个产品验收。外部核心体积50,429,542字节单列，管理器性能未测。源段不做回收，8MiB/32epoch/32active/8KiB帧为当前实验上限；到上限只回放旧源、拒绝新计量。

源码独立审查发现中间事件耗终末额度、opened预写失败泄漏、满epoch阻碍旧回放及Decide失败未标维度缺口；主代理先加3个回归断言观察失败，再修订并通过。controller关闭先Leave问题由主代理裁决，executor增加相关纯测试修复。源Close落盘失败poison并保留census，不报告final成功；活动socket仍清理，完整故障恢复未由此通过。首次smoke helper comm多写换行，保留原文，修正预登记名称复核后再跑smoke，100正式样本不含此错误，不做事后剔除。

主代理沿用会话模型/effort，负责源身份、锁/日志/恢复及最终验收；executor角色配置gpt-6.1-sol / medium独占上游钩子/包装/三值实现与相关测试，scout角色配置gpt-6-luna / high只读取证和定向审查，最多两个子任务且未派生。原公共依赖下载受sandbox网络限制，后续限定固定公开依赖、专用/tmp缓存并保留sum校验后成功；未改系统Go或project go.mod。无.git的候选首次原生VCS读取构建失败，明确-buildvcs=false并独立记录commit/patch后成功。

实际结果见[原生候选验收](validation/native-candidate.md)与机器摘要（本地原件 `validation/native-candidate.summary.json`，不随Git上传），任务现在129项/16项完成（仅新增T126）。严格G01日志在 `/tmp/hoopa-mihomo-events-ildaobd2/g01-native-identity.log`，100流原文 `/tmp/hoopa-native-probe-2uzvik_w`；校准smoke原文单独保留，机器摘要引用各文件hash，临时原文可能被清理。当前没有保留本轮运行核心；后续需要合适的owner可见环境或具体身份方案调整，不能默认提权、降低要求或继续堆P1界面。

最终写入停止后，executor独立从固定Git对象新导出副本并实际应用补丁，23项产物一致性检查通过：16变更摘要、917文件完整树、baseline909文件不变、模块锁不变、binary版本/大小/hash以及99个有效依赖校验和一致；未重跑Go测试、构建或代理流量，不构成可复现构建证明。scout发现research/tasks旧状态摘要滞后，主代理修正后同一scout定向复核通过。主代理最终核对docs/specs/原生实验范围31份Markdown的217个本地链接、129任务/16完成及文档空白，均无错误；本轮3个受控核心PID均已不存在。after_implement复查确认`.specify/extensions.yml`不存在，无后置hook。详细产物报告为`/tmp/hoopa-native-final-artifact-review.{md,json}`。

## 失败后的整体 Plan / Tasks 复核

日期：2026-10-23。用户要求重新review整个plan/tasks并避免再次因规划问题失败。主代理先做全范围审查和授权内修订，再冻结文档进入只读一致性分析；未将静态审查表述为运行零缺陷保证。

| 工作 | 状态 | 证据或后续动作 |
| --- | --- | --- |
| 全P0–P3计划/139任务复核及修订 | 已完成 | 9类规划问题已给出具体落点；追加T130–T139，原129 ID和16完成标记保留；spec/宪章/检查清单及旧运行证据不变 |
| 项目P0整改 | 进行中 | T127保持未完成，新的T130前提诊断是下一实施入口，新增任务本轮均未执行；T018及P1仍未放行 |
| G01身份能力 | 失败待决策 | 旧100流严格FAIL、0/100 owner_visibility保留；要求同拓扑诊断、具体算法和最小正负身份通过后，冻结新候选再正式登记，不无变化重跑 |
| 宿主/真实平台/完整源故障与质量 | 环境不足未验证 | 旧可见性限制未由文档解决；SSH/休眠、Schema实例、SQL驱动、资源/平台与完整故障仍按原范围未验收 |
| 本轮静态检查 | 已完成 | 139任务/16完成、74FR/SC全映射、显式依赖无环、关键传递前置、14并行波次及故障索引检查通过；32份Markdown/232本地链接与空白/围栏检查通过，git diff --check通过 |

修订内容包括身份前提/最小证明、来源/ready/配置能力的明确实现、P0控制恢复harness、生产spool与接收日志确认/回收、首次CLI语义依赖环、历史/质量JSON接线、早期IPC保护与macOS适配；[完整记录](validation/plan-task-review.md)逐项对应任务。保留接收ACK/SQL提交不同边界及最后可恢复副本，未修改锁/事务宪章约束。

主代理沿用会话模型/effort独占文档写入并裁决身份及日志/恢复；executor按角色配置gpt-6.1-sol / medium只读检查全任务/P1–P3并发现首次CLI语义环、API遗漏和macOS IPC遗漏；scout按gpt-6-luna / high只读核查P0真实根因与前提，子代理未派生。未运行Go/代理链路/100矩阵，未安装依赖或替换核心；静态JSON在`/tmp/hoopa-plan-review-static.json`。修订后冻结正文交独立只读复核，最终分析结果由答复报告。

首次冻结复核的P0前提及P1–P3执行链通过；executor另指出F/G/C表声明字面引用，但新增任务只具语义映射。主代理退出只读分析回到授权修订阶段，补齐正文编号并按正文重建索引，明确T138来源/接收各自单writer回收及持久ACK边界；随后仅复核这些受影响内容，不重跑无关Go或全范围审查。

## 再次修订后执行 speckit-analyze

日期：2026-10-23。用户明确要求先修订再重新分析。已完成：补齐T138新核心/协议制品的manifest/能力记录、主代理影响裁决和必要复验责任，T072/T077消费/接流前核对，T070/T088增加错配/缺证据拒绝；统一重试输入为失败相关环境/方案/实现的可观察变化及因果预期，保留旧run/正式分母。对应[复核记录](validation/plan-task-review.md)的F01/F02；139任务/16完成和原依赖均保持。

进行中：项目P0整改，T130仍为下一实施入口，本次不实施这些任务；失败待决策：旧G01身份0/100 owner_visibility仍FAIL；环境不足未验证：可见性、完整源故障/ready/线路及原平台/Schema/预算项维持原范围，文档修订不代替实测。主代理沿用会话配置独占文档写入和关键语义裁决；executor角色gpt-6.1-sol / medium只读发现新hash放行责任缺口，scout角色gpt-6-luna / high确认重试措辞冲突，均未派生。本轮未写源码、安装依赖或运行Go/代理/网络测试；随后冻结本轮diff，执行只读分析并在答复报告结果。

## T130/T131实施与T133独立生命周期补全

日期：2026-10-23。用户再次调用`$speckit-implement`，按现有139任务及既有隔离授权继续；requirements清单只读16/16通过，未重复生成任务。原用户核心、旧001候选/补丁/100流失败证据未改，未安装工具或更改系统权限/全局代理/TUN/服务。

| 工作 | 状态 | 观察及边界 |
|---|---|---|
| T130真实运行拓扑诊断 | 已完成 | 有界实际EUID/FD扫描、独立输出器、两个held-source barrier；沙箱4同UID全部可读但loopback EPERM，实际回环环境64同UID/63可读/1项权限拒绝，身份仍0/2；观察者不同exe安全上下文等价性未证明，不等于G01 |
| T131原socket身份证据裁决 | 已完成 | ADR006明确所需owner集合、PID/start/UID/ns前后复核及共享/转交/线程FD边界；普通非原子proc扫描不能证明通用唯一owner，当前负向裁决不等于身份已修复 |
| T132及旧G01身份 | 失败待决策 | 正向获批方案/前提未成立，T132未启动；旧正式100流0/100 owner_visibility保留，没有无变化重跑、删样本、改skip或自动提权 |
| T133来源/接收/只读恢复 | 进行中 | 周期累计CP、独立终末/epoch额度、正常退出marker、严格七种事件接收及pending/null尾部恢复已实现；完整core重开解析/来源恢复gap/lazy-handshake/完整故障仍缺，T133保持未勾选 |
| T133小规模真实场景 | 已完成 | CONNECT/同包预读/SOCKS5每流双向257字节，CP→closed；活动SIGTERM gap/null及incomplete；SIGKILL重启旧帧不改、新epoch关联新请求。Go reader独立检查4份稳定frames，旧流pending/null，不能泛化为完整G02 |
| 本轮定向验证 | 已完成 | 诊断Go race6顶层/7子用例；新增Hoopa事件/接收/恢复race16顶层/8子用例、0skip/FAIL；来源生命周期19 PASS/1网络gate默认SKIP，503测试显式开启后另PASS，新增lostACK/source重开race PASS；wrapper5项回归PASS；Python13 tests、相关vet/format/构建PASS |
| 稳定制品一致性 | 已完成 | 新/tmp副本依次实际apply001/002，旧/新整树及权限分别一致；旧16/新6文件、两binary/补丁、module锁核对通过，无重构建证明；新hash仅局部实验，不是T137/CapabilityRecord |
| 原环境及其余验收 | 环境不足未验证 | 完整owner可见性/证明、真实SSH/注销/休眠、Schema验证器、SQL驱动、平台/CI和预算仍无完整证据；T134/T135核心ready/两线路/加载期拒绝尚未实现验收，T137/T127–T129及P1不放行 |

[前提记录](validation/p0-prerequisites.md)、[ADR006](adr/006-source-identity-proof.md)与[生命周期分项结果](validation/source-lifecycle.md)保留详细范围和机器摘要。当前139任务完成18（本轮仅新增T130/T131），不是完成基础管理或整个项目。

主代理沿用会话模型/effort，负责身份/锁/日志/终态/恢复裁决、严格事件解码/只读reader及最终验收。executor实际角色gpt-6.1-sol / medium按单写入者顺序完成诊断库、隔离生命周期、receiver、真实小规模probe及制品复核；scout实际角色gpt-6-luna / high独立只读取证和稳定代码审查，最多两个子任务，未派生。

审查发现并定向修正两项探针失败路径：hash检查后仍按原路径exec改为执行私有已验副本；malformed observer/barrier异常改为记录错误类型并EOF释放helper。根代理把严格解码接上旧真实frames后发现provider_chain=[""]的DIRECT兼容问题，先加失败反例再按无命名provider证据保留原文；不修改原帧。另补active gap必须关联唯一opened的反例和断言，原冻结frames复验通过，未因此重跑核心。所有不确定/未知数据保持原义，未为让测试通过填0。

主要日志为`/tmp/hoopa-preflight-{race,vet,fmt}.log`、`/tmp/hoopa-t133-{a,b,c}-*`、`/tmp/hoopa-t133-final-{race,vet}.log`、`/tmp/hoopa-t133-sender-explicit-race.log`与`/tmp/hoopa-t133-ack-race.log`；原真实场景`/tmp/hoopa-lifecycle-6s7bfs3u`及独立reader/关联复验另存。最初错误测试过滤器未选中source，后一次未设显式网络gate而skip；这两次exit0均没有计入网络PASS，随后确切用例显式启用才通过。一次报告写入工具使用Path.open不支持的opener报错，仅修正私有报告写入并重新只读解析，未重复网络实验。临时材料可能被清理；公共patch/manifest/机器摘要保留固定hash及范围。

## 三项剩余问题的设计收敛与协议前提修复

日期：2026-10-23。用户要求思考并解决原始进程归属、来源恢复及真实ready/两线路问题。主代理沿用karpathy-guidelines约束，先拆分可证明事实与未经验证的前提，再修订现有任务，没有重跑旧G01或降低FR/SC。

| 工作 | 状态 | 证据及边界 |
|---|---|---|
| ADR007及T132–T134定向修订 | 已完成 | 分开connect发起者/request origin/FD holder/launch；内核候选先做connect事实和原入口关联两个最小原型；T133同源纯校验/v2 recovery_gap、稳定引用去重及故障点；T134按协议完成证据/连接产生处线路/实际n字节分段 |
| 固定候选SOCKS5回复头缺陷 | 已完成 | 原实现未检查VER/REP/RSV；先27合成用例观察11失败，再最小校验修复，27叶子/3顶层race通过、0skip，vet/gofmt通过；003在fresh副本实际apply且整树/权限一致，前后日志嵌入机器证据 |
| 三项完整实现与验收 | 进行中 | 设计不是实现；T132/T133/T134保持未勾，139任务/18完成。003只关闭一项ready前提，不形成新binary或T137/CapabilityRecord |
| 身份证明路线 | 失败待决策 | 非原子proc双扫不能证明唯一FD owner，connect hook不能证明后续请求写入者/全部FD持有关系；候选须另证入口socket关联/共享及健康覆盖，不借更名字段通过G01；旧0/100保留 |
| 内核/真实矩阵 | 环境不足未验证 | 只读环境有部分BPF/BTF前提，未证明可load/attach，未加载/改权限；v2来源恢复、lazy握手、真实两线路及原平台/SQL/预算验收未执行 |

主代理负责恢复顺序、身份口径及方案取舍，独占文档/plan/tasks修改；两个scout实际角色均为gpt-6-luna / high，分别只读核对Linux证据/权限前提和固定核心ready/线路调用位置，不派生。其后executor实际角色gpt-6.1-sol / medium独占新私有副本的两份SOCKS5源/测试及仓库003补丁/证据JSON；同时最多两个子任务，不修改001/002/冻结输入或用户核心。root复核原代码及RFC1928、最小diff和错误码保真；没有重复执行已通过的测试。

[ADR007](adr/007-p0-evidence-closure.md)、[003补丁](../experiments/mihomo-events/patches/0003-socks5-reply-validation.patch)和完整测试证据（本地原件 `../experiments/mihomo-events/socks5-reply-validation.json`，不随Git上传）记录具体方案及实际结果。原始新验证材料在`/tmp/hoopa-socks5-reply-N2daun`，可能被清理；公共JSON保留日志及摘要。本轮浏览了一手内核/协议资料，没有运行代理流量探针、构建新核心、加载BPF、安装依赖或声明全项目完成。

## 可行性复核、connector澄清与P0恢复简化

日期：2026-10-23。用户要求判断三项方案是否可行、需要更新什么，并明确选择“原始TCP连接发起进程”：按建连PID/启动实例归属，FD转交不改写历史，显示共享/转交限制。选择语义不等于授权加载BPF或更改权限。

| 工作 | 状态 | 本轮结果及边界 |
|---|---|---|
| 归属语义与跨文档同步 | 已完成 | spec的FR-034/049/050/051、相关故事/SC，产品统计/路由/计量/故障表，data-model、telemetry契约、research/plan/tasks及最新入口说明同步connect_initiator_v1；名称/路径用建连快照，实例/name/path能力分别验收，旧v1不重新贴标 |
| T133/T134可行性和任务收敛 | 已完成 | 撤回未实现的强制v2 recovery_gap/跨epoch引用图；改同源parser四接线点、原帧重放与派生pending/gap报告。pending不造不可恢复事实；持久事实按稳定ID/事实payload幂等，重扫元数据不制造冲突；历史gap与当前recording_status拆分。质量先冻结profile/fixtures、按协议ready和实际Conn/连接段校准，保留失败/取消/未知及独立等待值 |
| T132/T133/T134实现及正式验收 | 进行中 | 仍未勾选，139任务/18完成；本轮没有源码实现、新binary、新runtime能力或新的运行测试通过结论；T018/P1继续不放行 |
| 身份后端的关键证明 | 失败待决策 | 原0/100及owner_visibility失败原样保留、不按新口径重评。旧proc全owner枚举不再定义connector；新connect→active/passive→accepted SO_COOKIE候选尚未实证，若cookie/启动快照或唯一关联不能成立，停止spike并由主代理重新裁决 |
| 实际环境与剩余矩阵 | 环境不足未验证 | 未验证当前宿主BPF加载/attach/受控cgroup、原入口关联/复用/提前退出/丢失、字段快照与namespace范围；完整来源故障、真实ready/两线路/HTTP回复payload同包、平台/SSH/休眠、SQL/资源预算仍待验收 |

[ADR007](adr/007-p0-evidence-closure.md)记录判断与受限关联候选：T133工程路径明确可独立推进，T134按协议可分步校准；身份仅有条件可行，两个小原型通过前不先建完整helper。旧001/002/003补丁、manifest/机器摘要、原100流结果不改；恢复简化不把未知尾部伪造为0或精确closed，也不放宽G01–G03。

主代理沿用会话模型/effort负责用户语义、锁/日志/恢复与关键取舍，写spec/ADR/research/plan/tasks及状态文档；scout实际角色gpt-6-luna / high只读核对Linux固定源码/身份前提及最终一致性；executor实际角色gpt-6.1-sol / medium按明确文件范围先同步四份产品文档，再同步data-model/telemetry契约。子任务不派生、同时最多两个。仅浏览一手固定内核/协议资料并做文档静态检查，没有加载BPF、安装依赖、运行Go/代理/网络实验或替换用户核心。

独立文档复核发现下游任务承接不足：T012仍可能照搬旧共享拒绝oracle；T095/T097漏planned质量字段、T100漏连接段字节入账。主代理已补新oracle/原入口关联、actual_connection_ref/单调起止/observed_wait/ready分类、稳定线路及ConnectionSegmentBytes与唯一ByteFact关系，并补T096守恒断言；已完成T008/T131只加历史范围说明，标记不变。复核后只重新检查这些受影响内容和最终静态结果，不重跑运行实验。

最终稳定文档复核：身份scout确认新旧oracle/connector范围修复通过；恢复scout确认质量任务/T129覆盖及T138回收必要材料一致、没有实质矛盾，主代理按其建议消除恢复事实可替代原帧的语法歧义。18份Markdown的211个本地链接、围栏/空白，74项FR/SC ID及139任务/18完成标记不变、依赖无环、001/002/003补丁SHA不变检查通过，摘要位于`/tmp/hoopa-feasibility-doc-static.json`；最终仅文字补全后再做受影响内容检查。上述均为静态证据，无新Go/代理/内核/故障运行结论。

## T133共用校验、来源重开与T134流边界子工作

日期：2026-10-23。用户再次调用`$speckit-implement`。requirements清单只读16/16通过，沿用现有139项任务；根据ADR007推进不依赖正向身份的P0独立子实验。详细证据见[来源恢复局部补全](validation/source-recovery-closure.md)、[新制品manifest](../experiments/mihomo-events/closure-candidate-manifest.json)与机器验证（本地原件 `../experiments/mihomo-events/closure-validation.json`，不随Git上传）。

| 工作 | 状态 | 本轮结果与边界 |
|---|---|---|
| 共同v1校验及恢复接线 | 已完成 | 新纯包校验七事件、manifest/epoch和生命周期，四处共用；并发活动flow最多32、历史终结不占上限；核心六份生产文本与仓库一致。完整Write+Sync后Commit，sender只发confirmed prefix，坏尾不截断，缺manifest拒绝 |
| 派生恢复和只读检查 | 已完成 | 持锁完整稳定扫描后才能派生epoch_ended_without_final；缺final的只读者保持pending，CP保留、尾部null；精确closed不重加、稳定事实ID与扫描时间分离。新binary四对真实快照只读检查通过，未造writer结束证明；生产SQL/持久reconciliation/GC未实现 |
| HTTP/首次Write边界 | 已完成 | HTTP预读payload反例修前2/4失败、修后4/4 race通过；lazy初始一次Write按actual n计数有8个定向用例。这只是流边界子工作，未把Dial成功/空Write/未知握手当ready |
| 可审查新制品 | 已完成 | 干净upstream依次check/apply001–005、整树内容/权限一致、离线构建和版本/SHA通过。新binary SHA bfc1df3a99871f2b1257e4b28b58b64a97b6289840b6cd63a50c6b86ec75c6d2，只放私有tmp，未放Git或覆盖用户核心；不是T137 |
| 定向验证 | 已完成 | Hoopa18+25+2顶层race通过，身份gate1skip；source故障/配额/重开/32活跃和32epoch8MiB扫描通过；两项网络race另获授权执行通过；全量vet及16个Python纯测试通过。测试注入不代表宿主真实满盘/断电，扫描heap/RSS不代表产品闲置预算 |
| 首次真实组合异常 | 失败待决策 | /tmp/hoopa-lifecycle-edy6mctq：normal三流257/257通过、active RuntimeError且trace空失败、crash未执行，cleanup通过；确切根因因原诊断缺失未定位，旧结果保留。孤立诊断及增加阶段码后的组合通过不能改判该run |
| 探针启动前提 | 已完成 | 固定源码listeners可先于Running开放，TCP accept/CONNECT200不能证明转发；改用独立非计量入站限时完成11字节回显，每core重新验。修后/tmp/hoopa-lifecycle-coop5rr8三小场景通过，预检不计入主flow分母，不声称定位首次异常 |
| T132/T133/T134/T137及正式门槛 | 进行中 | T133仍需完整collector离线/背压/实际lazy边界矩阵；T134仍缺新profile、协议ready及两实际线路；T132正向connector关联仍缺。139任务/18完成保持，旧G01的0/100不改，G02/G03/T137/T018和P1未放行 |
| 环境限制与其余验收 | 环境不足未验证 | 默认沙箱仓库全量race为96通过/2skip/3因socket EPERM未完成，未为得到green重跑旧身份100流。BPF加载/attach未获验证且未运行；SSH/注销/休眠、SQL、其他平台和产品预算未完成 |

主代理沿用会话模型与effort，负责锁/日志/恢复顺序、纯恢复实现、实际失败处置、启动前提修正和最终验收；executor实际角色为gpt-6.1-sol / medium，独占共同wire提炼/receiver、隔离核心接线/测试及制品，再记录两份文档；scout实际角色为gpt-6-luna / high，独立只读取证、32活跃上限与启动预检审查，未执行网络。最多两个子任务、没有子代理再派生；写入范围按阶段交接，最终针对稳定内容验收。此次没有下载依赖、加载BPF、改系统设置或发布制品。

首次manifest反例在旧实现6项失败，active33反例也先失败；修复后相关用例通过。源坏尾/Sync不确定、接收journal确认与SQL边界保持分离。新004/005及closure JSON不覆盖001/002/003/旧manifest与原100失败；当前任务未满足整体完成条件，保持未勾选。

## T133本地闭环完成与剩余身份/质量实施决定

日期：2026-10-23。用户要求继续思考并解决。已补齐T133明列的collector离线、ACK停滞和receiver/core重开真实验收，并修复配对reader预算；主代理按任务正文及独立只读覆盖复核，完成T133，当前139项/19完成。没有把T128正式G02或T134协议ready当作已交付，也没有改原0/100及首次active异常。

| 工作 | 状态 | 证据与边界 |
|---|---|---|
| T133传输故障 | 已完成 | 两项显式真实回环race通过、0fail/skip：离线时独占bind未listen且终末已落盘后才上线collector；持久closed扣ACK时SIGKILL、receiver/core重开原raw同receipt；opened ACK停滞时原flow转发及CP/closed继续。最终3个自有child全部回收，私有文件权限核验通过 |
| 恢复配对预算修复 | 已完成 | 先有超预算合法帧被接受的失败反例，再按manifest+frames总预算限制读取；精确上限接受，超限拒绝，最多读取frames余量+1；相关3项race通过，不返回部分报告 |
| T133既有能力复核 | 已完成 | 四处同源校验、确认前缀、writer结束证据/派生pending-gap、坏尾/Sync/Write/注入ENOSPC、重复恢复与active/epoch/规模边界承接原证据；actual-n lazy计数不提升为协议ready。新旧记录按各自hash/范围保存 |
| 首次异常与原身份失败 | 失败待决策 | 原G01 0/100及首个active RuntimeError保留；已修正有源码依据的启动前提，但未补造原异常确切原因；没有重新评分或重跑正式身份100流 |
| T132与T134 | 进行中 | [ADR008](adr/008-remaining-evidence-execution.md)补明确部署/字段/原入口关联与实际Conn协议证明；线程TGID/start/comm不能混配；core内部持久引用与展示名分开。新kernel后端及新quality profile/ready/两线路尚未实现验收，二任务保持未勾选 |
| T132运行环境 | 环境不足未验证 | 默认沙箱与实际回环上下文分别核对：实际Eff/Prm/Amb0但Bnd非0、NoNewPrivs0/Seccomp0；unprivileged BPF=2，cgroupro且无受控可写cgroup2，bpffs rw但root-owned01700/EUID不可写。未load/attach，不把工具/BTF存在当支持，不能直接试内核原型 |
| 正式及生产范围 | 环境不足未验证 | 正式G02/G03、SQL/生产GC、真实宿主断电/满盘、其他平台/SSH/休眠和产品预算未完成。T137/T018/P1不放行；本轮没有新核心或新平台声明 |

Root发现首轮offline fixture仅释放端口不能严格证明没有collector，要求改为持有bind未listen socket直到源closed已持久，并断言SO_REUSEADDR=0、SO_ACCEPTCONN=0及ECONNREFUSED；最终新run通过，前两次pass及其源码范围原样保留，没有替换原失败。新文档[来源传输验收](validation/source-transport.md)和机器摘要（本地原件 `../experiments/mihomo-events/transport-validation.json`，不随Git上传）绑定最终测试文本及同一001–005 binary SHA。

主代理沿用会话模型与effort，负责恢复预算实现、锁/ACK/SIGKILL审查、offline真值收紧、实际执行上下文复核、身份线程坐标/质量方案裁决和最终任务验收。executor实际角色gpt-6.1-sol / medium独占新增transport测试、定向race/vet/fmt及新验证文档；scout实际角色gpt-6-luna / high只读核实固定内核、环境前提及T133文字覆盖，未网络复测。最多两个子任务、没有再派生；源码写入完成后审查稳定文件。未下载依赖、改系统权限、加载BPF、安装服务或覆盖用户核心。

## T134连接质量与实际线路局部完成

日期：2026-10-23。用户要求继续，按T134推进协议ready和实际线路，未展开完整CLI/TUI。主代理验收后勾选T134，当前139项/20完成。详见[分层验收](validation/quality-ready.md)、[最终候选manifest](../experiments/mihomo-events/quality-candidate-manifest.json)及机器记录（本地原件 `../experiments/mihomo-events/quality-validation.json`，不随Git上传）。

| 工作 | 状态 | 结果与边界 |
| --- | --- | --- |
| 严格v2与旧v1共存 | 已完成 | wire/epoch完整形状、时间等式、实际引用唯一性、pre_route中止状态、段单调/守恒；八份纯生产文本与核心逐字一致。旧原帧可重开，不从旧总量捏造新quality/线路 |
| 协议ready与flow/attempt | 已完成 | DIRECT TCP、HTTP完整200、SOCKS5完整成功响应产生实际Conn证明；单调ready与finish/落盘分离，失败分类优先，无证明为null。真实回环及Pipe/受控DNS/flow夹具分层记录，未知外层不能借内层证明 |
| 实际线路与累计字节 | 已完成 | 持久node/group内部名、展示别名分开；具体Conn冻结chain并绑定tracker，首次Write与relay actual n留在原segment。五真实成功流覆盖切组后旧A连接、nested、改名重开；三重试各1flow/2attempts，失败无虚构actual引用，每流双向各257B且守恒 |
| 可复现局部候选 | 已完成 | 全新固定upstream依次check/apply001–006、整树内容/权限匹配、离线构建/版本核验。最终SHA256 9d52edee280d7e4893911e3c4a6ae43150cf4914421431f7ee19567505459b29，仅私有tmp；go.mod/go.sum未改，旧patch/hash/run均保留。这不是T137 |
| 最终验证 | 已完成 | 显式真实8流/11attempts，2顶层/3子例pass，0fail/skip；仓库定向race为61顶层pass/5skip（含子例371pass），fresh core quality为5顶层/12含子例pass。仓库全量vet与core定向vet通过。harness为race，启动的core普通构建；skip不记为通过 |
| 本轮失败及修正 | 已完成 | 首次harness误计旧事件重放、fake resolver分支、测试import cycle和shim再解析错误均保留失败并有独立修后结果；没有改判历史run。初次共享文本漏拷贝编译失败、归档脚本Python3.8 API不兼容亦已修正；这些不属于产品运行通过证据 |
| 原G01与首次active异常 | 失败待决策 | 原0/100和首次active RuntimeError保留；尚未取得新身份正式通过，不能由quality成功抵消；没有补造原active异常确切根因 |
| T135与完整候选 | 进行中 | 下一项可独立推进配置加载期能力拒绝/入口保护，保留合法正向用例；T132/T135未过前T137、T127–T129、T018/P1仍未放行 |
| 身份与其他范围 | 环境不足未验证 | T132真实connect→accepted socket唯一关联及受控加载环境仍缺，未load/attach BPF；命名订阅provider、完整core DNS E2E、其他协议/平台、SQL、SSH/休眠和产品预算没有本轮通过证据。曾在沙箱被拒的DIRECT socket另在已授权回环上下文运行，旧EPERM保留 |

主代理沿用会话模型与effort，负责协议证明/锁/日志顺序、核心实现、原段计数、异常裁决和最终任务验收。executor实际角色gpt-6.1-sol / medium负责wire、bundle、真实harness、相关测试与验收文档；scout实际角色gpt-6-luna / high负责固定调用链、稳定源码和制品只读核验，未运行网络。最多两个子任务，未再派生；写入后审查稳定内容。未下载依赖、改系统权限、加载BPF、安装服务或替换用户核心。

## T135 配置准入、认证与排空局部完成

日期：2026-10-23。用户要求继续，按依赖可独立推进的 T135 完成受保护实验入口。requirements 清单只读 16/16 通过；T135 已勾选，当前 139 项任务完成 21 项。最终固定候选及结果见[验收报告](validation/protected-entry.md)、[manifest](../experiments/mihomo-events/capability-candidate-manifest.json)、机器验证（本地原件 `../experiments/mihomo-events/capability-validation.json`，不随Git上传）。

| 状态 | 本次结果与边界 |
| --- | --- |
| 已完成 | T135 严格有界 JSON profile、同字节 hash/解析、端口开放前能力与可选来源准入、独立入口认证、HTTP 逐请求认证、受限只读 status、不可变 generation、排空与独立完整替换。全部进程规则加载期拒绝；非进程规则按首匹配经两条真实受控线路通过 |
| 已完成 | 007 从干净 v1.19.26 依次实际应用 001–007，13 个增量文件、整树路径/内容/模式一致；8 份 wire 与 profile 纯文件逐字相同，依赖和旧补丁不变。新 binary 只在私有 `/tmp`，未替换用户核心、加载 BPF、改变系统代理或安装依赖 |
| 已完成 | 最终固定候选真实回环：2 顶层/49 含子测试 pass、0 fail/skip；完整 trace 单变量 generation/map/权限拒绝及合法双向各 257B final。仓库定向 race 462 pass/7 skip，核心受影响包 race 91 pass/2 skip，独立真实 socket reservation race 1 pass；仓库与核心定向 vet 通过。统计是测试事件数，不是正式身份样本数；核心子进程为普通构建 |
| 已完成 | 畸形 AND 造成解析 panic、32 epoch 只读来源误准入均先复现真实失败，再修复并通过回归。默认 cache 权限、沙箱 socket EPERM、缺符号编译和 main flag parser 阻断测试分别保留，不能写成运行通过；重定位生产 socket helper 后显式测试实际通过 |
| 进行中 | T132 连接发起者身份正向关联仍缺，T137 完整候选未冻结；T136 依赖 T137，尚不能由本轮排空/独立替换原语宣布崩溃接管或分阶段回滚完成。T127–T129、T018 与 P1 保持未放行 |
| 失败待决策 | 原正式 G01 的 0/100 和首次组合生命周期 active 异常保留；本轮没有以改文档、删失败样本或重新分类来消除旧结果，也没有无变化重跑原正式矩阵 |
| 环境不足未验证 | T132 受控内核加载/cgroup 与原入口唯一关联环境仍缺，未 load/attach；其他平台、真实 SSH/注销/休眠、宿主满盘/断电、TLS/远端链路、生产 SQL 和性能预算均没有本轮通过证据 |

执行分工为 executor（GPT-6.1-sol / medium）实现纯校验器/入口 harness 和指定文档，scout（GPT-6-luna / high）提取核心调用链、复核稳定改动及归档摘要；主代理裁决配置/来源/socket 关键语义、补齐 trace 验收、处理反例并验收最终固定制品。旧 001–006、旧 manifest/run 未改；本轮 007 的通过范围单独绑定新 hash，不继承旧正式门槛结果。后续必须取得受控环境中 connect 事实与原 accepted socket 唯一关联的正反证据，再推进 T132/T137；局部任务完成不等于整个项目完成。

## T132 connect 捕获原型准备

日期：2026-10-23。用户要求继续，随后明确回复“暂时没有，先完成原型准备”，本轮不加载内核程序。requirements 清单只读16/16；139项/21完成不变，T132保持未勾选。详见[准备验收](validation/connector-preparation.md)及机器归档（本地原件 `../experiments/sourceidentity/prepare-validation.json`，不随Git上传）。

| 状态 | 事实与边界 |
| --- | --- |
| 已完成 | 有限 connect4 观察源码、32B配置/112B记录ABI及未来验收边界；TGID/leader start、线程TID、初始PID namespace和精确cgroup/netns分开，comm未验证，无路径或accepted关联能力 |
| 已完成 | 离线准备器固定argv、私有输入快照、工具前后摘要、日志/输入上限与超时、失败manifest和ELF结构检查。12项假工具测试及py_compile通过；最终真实BTF编译四步骤均exit0，生成对象不进入Git |
| 进行中 | T132的connect→active传递、active/passive→原入口关联、有限loader/读取端及核心接线尚未交付；不先建完整服务，不改变T137/T136依赖或P0/P1门槛 |
| 失败待决策 | 原G01 0/100保持。本轮工具曾因/tmp所在分区满而无法启动，首次迁移shell亦未执行；改用无shell临时文件命令将本轮两目录校验迁移至ignored private并保留原路径链接，旧任务证据未改。这不是代理满盘故障验收 |
| 环境不足未验证 | 用户暂未提供隔离Linux/受控cgroup/loader环境；本轮没有load/attach、verifier或真实身份样本；名称读取一致性、唯一入口关联、生命周期/连续性及内核成本仍待实测 |

executor（GPT-6.1-sol / medium）独占准备器与测试，scout（GPT-6-luna / high）只读盘点/复核，主代理完成捕获源码、字段/故障决策、真实离线编译及最终验收。没有修改旧Go proc探针、001–007补丁、Mihomo制品或Go依赖；没有安装工具或改变权限/系统网络。后续先在隔离环境验证有限loader与捕获，再证明原入口关联；本轮prepared状态不生成passed CapabilityRecord。

## T132 socket 生命周期观测与关联前提修正

日期：2026-10-23。用户继续，沿用“暂时没有，先完成原型准备”的范围。requirements 清单只读16/16；139项/21完成不变，T132未勾选。结果见[生命周期准备记录](validation/connector-lifecycle-preparation.md)与机器归档（本地原件 `../experiments/sourceidentity/lifecycle-prepare-validation.json`，不随Git上传）。

| 状态 | 事实与边界 |
| --- | --- |
| 已完成 | 新lifecycle对象共享原capture的三个map，追加48B established/40B TCP_CLOSE不可变观察；active按cookie查原capture，passive不推netns/PID；不建立/删除tuple索引，flags注册错误使整轮失效 |
| 已完成 | 准备器capture默认兼容，lifecycle固定双源码提前快照、额外sockops ELF检查；16项假工具测试、py_compile及真实BTF离线编译四步exit0，独立对象/日志/摘要保留。没有内核load/attach |
| 已完成 | 固定上游v5.10 helper分派、SO_COOKIE与缺失的SO_NETNS_COOKIE、端口字节序/STATE_CB/flags复核；修正非原子cookie核验删除的候选前提。固定v6.6作为后续关联接口的优先验证基线，不修改产品支持范围 |
| 进行中 | T132的有限loader、namespace核验、原accepted cookie唯一关联、复用/并发/早退规则与核心接线；新对象仅观察源码，不是完成A/B运行验收，也未放行T137/T136或正式门槛 |
| 失败待决策 | 旧G01 0/100及首次active异常保留。旧tuple核验后删除方案不能证明安全，后续权威关联仍须给出可审查算法及正反例；本次没有新的运行身份分母 |
| 环境不足未验证 | 隔离Linux与受控cgroup/loader仍未提供；verifier/attach、回调覆盖/顺序、真实故障路径、名称一致性、SO_COOKIE与passive对应、跨namespace和运行成本未验，不把编译/源码事实算作支持 |

executor按角色配置GPT-6.1-sol / medium独占准备器及测试；scout按GPT-6-luna / high独立只读核实上游与稳定C源码，未派生代理。主代理负责生命周期与停止语义、C源码、方案/任务修订、真实离线编译及最终验收。原capture C、旧prepare-validation、001–007补丁/manifest和Go依赖未改；新对象不进Git。没有安装依赖、改系统权限/网络或替换用户核心。

## T132 真实对象检查与单条ABI读取准备

日期：2026-10-23。继续“先完成原型准备”，没有另行加载授权或隔离运行环境。requirements只读16/16，139项/21完成不变；T132未勾选。[验收记录](validation/connector-object-inspection.md)及机器归档（本地原件 `../experiments/sourceidentity/object-inspection-validation.json`，不随Git上传）区分假库/纯bytes与真实libbpf离线解析。

| 状态 | 事实与边界 |
| --- | --- |
| 已完成 | inspect_object.py固定对象/运行库摘要，真实open_mem/read/close与profile声明核对，兼容旧迭代器、精确FFI及有效对象异常关闭；无load/attach/pin。非普通文件提前拒绝，O_NONBLOCK避免FIFO替换阻塞 |
| 已完成 | observations.py严格配置/单条ABI、key/generation/schema及单条可核对字段；uint64字符串保真、comm原文和未知状态保留，无集合关联、内核map读取或passed身份 |
| 已完成 | 10项纯bytes与8项假库测试通过；固定最终源码实际解析capture/lifecycle两个旧对象成功，截断ELF按预期exit1拒绝。三例strace未见bpf/mount/unshare/setns，唯一可写open flag为/dev/null；不宣称记录了全部write调用 |
| 进行中 | 完整有限loader、配置freeze/共享map/全部attach、停止后的稳定读取、namespace与唯一关联、名称一致性和核心接线仍缺；不放行T137/T136/正式矩阵或P1 |
| 失败待决策 | 旧0/100和首次active异常保留；旧tuple关联/回收缺口未因对象可解析而解决。本轮默认沙箱ptrace拒绝原样保留，随后获自动审批仅跟踪自有离线子进程；无新增运行身份分母 |
| 环境不足未验证 | 未执行CO-RE加载、verifier/attach或真实捕获；所查范围无libbpf开发文件/Go加载缓存，仅使用已有发行库，不下载依赖、不修改cgroup/宿主网络 |

executor（GPT-6.1-sol / medium）独占解码器与定向测试，scout（GPT-6-luna / high）只读核查依赖/公开API/稳定实现与trace。主代理负责FFI、输入/关闭语义、真实工具执行和最终验收；最多两个子任务，无再派生。C/准备器/旧JSON/001–007证据未改，新结果以独立摘要保存，旧对象未重编或替换。

## T132 有限 loader 与会话控制准备

日期：2026-10-23。继续“先完成原型准备”，requirements只读16/16，139项/21完成不变，T132未勾选。结果见[验收记录](validation/connector-loader-session.md)与机器归档（本地原件 `../experiments/sourceidentity/loader-session-validation.json`，不随Git上传）。

| 状态 | 事实与边界 |
| --- | --- |
| 已完成 | 新有限lifecycle后端、会话控制及默认拒绝的实验命令；配置readback/freeze、实际info/map ID集合、直接BPF_LINK_CREATE、全部挂接核验后观测、单次逆序清理和有界诊断扫描均有fake测试 |
| 已完成 | 20项fake CDLL、14项会话、13项CLI测试及18项既有对象/解码接口回归，共65项顶层通过/0失败/跳过；六文件语法检查通过。没有运行真实load/attach、cgroup变更、网络或正式100流 |
| 已完成 | 贯通测试捕获结果map ID被close清空（修前13通过/1失败），改独立副本后通过并保留旧日志。主错误被摘要复核覆盖、u64输出及FD登记失败清理边界亦已修正和定向验证；不承诺SIGKILL/全局OOM时完成报告 |
| 进行中 | T132停止同步/稳定cutoff与目标范围、attachment连续性、原accepted cookie唯一关联和核心接线；close link不证明RCU在途回调结束，freeze不阻止BPF写，diagnostic_snapshot仍unverified |
| 失败待决策 | 原G01 0/100与首次active异常保留；没有新的运行身份分母，旧tuple复用/回收缺口未由本轮代码准备解决 |
| 环境不足未验证 | 隔离Linux/受控cgroup仍缺；verifier/CO-RE、真实map共享/freeze/attach、PID/netns/cgroup坐标、回调连续性、退出资源回收、名称一致性和资源成本未实测，T137/T136及P0/P1门槛未放行 |

executor按GPT-6.1-sol / medium独占native后端与两组测试；scout按GPT-6-luna / high只读核对固定API、停止调用链和稳定代码。主代理负责控制器/CLI、信号与资源/证据顺序、集成故障修复和最终验收；最多两个子任务，无再派生。原C/准备器/对象检查/解码、旧JSON和001–007证据保持不变；没有安装依赖、修改系统权限/网络、替换用户核心或触发提权。

## T132 gated 停止写入候选

日期：2026-10-23。继续用户“先完成原型准备”的范围，requirements只读16/16，T132未勾选，139项/21完成不变。详见[停止协议](../specs/001-hoopa-proxy-manager/contracts/connector-stop-spike-v1.md)、[验收](validation/connector-stop-preparation.md)及机器归档（本地原件 `../experiments/sourceidentity/stop-preparation-validation.json`，不随Git上传）。

| 状态 | 事实与边界 |
| --- | --- |
| 已完成 | 共享锁下admit/active/closed/exit候选，原观察body所有返回由wrapper汇合；新增3程序/6map的gated profile，不attach的TC控制程序仅关闭准入，用户态BPF_F_LOCK读取，不整块覆写计数 |
| 已完成 | 1/2/3回调去重模型和两种错误方案的可重放反例；gate假接口/会话贯通及受影响接口共101项顶层Python测试通过、0失败/跳过。三profile真实离线构建各4步exit0及真实libbpf只读解析通过，无load/attach/TEST_RUN |
| 已完成 | 两原观察body逐字符相同；capture connect4和lifecycle connect4/sockops指令section与旧对象逐字相同。C wrapper及共享Python实现已更新，新ELF/hash单独归档；旧对象、旧验证JSON和001–007核心证据未覆盖 |
| 进行中 | T132真实writer停止、范围与attachment连续性、原accepted唯一关联和核心接线。writer_stop只是候选观测；不是RCU/回调全部退出、TCP结束或采集完备，T137及正式门槛未放行 |
| 失败待决策 | 旧G01 0/100及首次active异常保持；旧tuple复用/关联缺口未由gate解决。两种被否决停止法的模型反例独立记录，不产生真实身份新分母 |
| 环境不足未验证 | verifier/helper权限、真实锁与回调交错、TEST_RUN、目标坐标/残留socket/范围、内核阻塞与退出资源释放、锁竞争及CPU/RSS/map成本；默认2秒/1001次轮询不提供内核调用硬超时 |

executor实际角色GPT-6.1-sol / medium独占模型、准备器及新gate测试；scout实际角色GPT-6-luna / high只读核查固定v6.6/v0.6.0接口、锁内存序及稳定实现。主代理负责协议/BPF/loader、错误和状态边界、真实离线编译与验收；最多两个子任务，无再派生。未安装依赖、修改cgroup/系统网络/权限、替换用户核心或重跑正式100流。

## T132 有限原 accepted 关联模型

日期：2026-10-23。继续原型准备，requirements只读16/16，无扩展hook；T132未勾选，139项/21完成不变。[契约](../specs/001-hoopa-proxy-manager/contracts/connector-association-model-v1.md)、[验收](validation/connector-association-model.md)与机器归档（本地原件 `../experiments/sourceidentity/association-preparation-validation.json`，不随Git上传）绑定本轮证据。

| 状态 | 事实与边界 |
| --- | --- |
| 已完成 | 只插入的有限关联模型、1/2/3流全部抽象交错、三条错配反例与14项定向测试；C不预知客户端临时端口，Q只消费witness，oracle仅结果后核对；稳定内容审查通过 |
| 已完成 | 固定v6.6普通握手/namespace及accept锁路径核查；区分queue发布、callback和FD交付。模型未增加BPF/loader/profile或核心补丁，旧源码/制品/JSON保持 |
| 进行中 | T132真实范围/覆盖连续性、带netns的关联ABI、原FD获取/包装/关闭顺序、名称一致性和在线核心接线；按契约A–E推进，模型结果不能直接用于路由 |
| 失败待决策 | 原G01 0/100及首次active异常保留；初版模型的字段预知/oracle补配被反例否决，14项修前3失败与修后通过分别归档。新三反例不是正式身份分母 |
| 环境不足未验证 | 无隔离Linux/BPF/cgroup真实加载运行，缺覆盖/停止/原FD正反例、资源与性能证据；T137/T136、正式矩阵及P0/P1不放行 |

executor实际GPT-6.1-sol / medium独占模型和测试，scout实际GPT-6-luna / high只读证据与稳定内容审查；主代理负责协议、源顺序与oracle边界裁决、文档和最终验收。两子任务不再派生。沙箱最初因/tmp不足启动失败，自动审批只读df后恢复；未删除用户数据，新日志放项目private目录，未安装依赖或更改cgroup/系统网络/权限。

## T132 原 accepted FD 见证准备

日期：2026-10-23。按用户继续原型准备；requirements只读16/16，无hooks。[契约](../specs/001-hoopa-proxy-manager/contracts/accepted-socket-witness-v1.md)、[验收](validation/accepted-socket-witness.md)与归档（本地原件 `../experiments/sourceidentity/accepted-witness-validation.json`，不随Git上传）记录实际边界。

| 状态 | 事实与限制 |
| --- | --- |
| 已完成 | 五文件Go实验包；自己AcceptTCP、单Control内读取原socket、精确uint64字符串、私有绑定/值副本、失败关闭/双错误保留。fake race4顶层/含子测试38通过，vet/gofmt通过，两个平台stub离线编译 |
| 已完成 | 自动审批后仅自有loopback测试：普通Go Close窗口的SO_COOKIE读取稳定；本机不支持NETNS时确实拒绝并关闭新连接、保留listener。最终清理helper改动后重验局部拒绝路径 |
| 进行中 | T132完整见证、真实范围/attach连续性、带netns内核观察、原FD对passive、独立oracle、名称与在线核心接线；当前仅普通net.Conn基本方法，半关闭与包装链兼容另验 |
| 失败待决策 | 原0/100/首次active异常不改；tests-first未定义符号和沙箱socket EPERM保留。本机SO_NETNS_COOKIE明确ENOPROTOOPT，完整正例skip/unverified，未用零值或配置namespace补证 |
| 环境不足未验证 | 双cookie正例、BPF/verifier/跨socket关联、强制FD复用/转交和实际平台运行；package exit0不代表完整见证通过，无正式身份新分母 |

executor实际GPT-6.1-sol / medium独占Go实现/测试/编译；scout实际GPT-6-luna / high只读源码及稳定代码；主代理负责接口/生命周期决策、真实loopback和最终验收。初版源码及原运行结果独立保存，最终仅重验改动影响的路径。无再派生、无依赖下载/BPF/cgroup或系统网络配置变更，139项/21完成与T132未勾选保持。

## T132 小规模校准准备链

日期：2026-10-24。按用户要求连续推进，沿用 speckit-implement 与 karpathy-guidelines；requirements只读16/16，无扩展hook。见[验收](validation/connector-calibration.md)、[契约](../specs/001-hoopa-proxy-manager/contracts/connector-calibration-v1.md)及机器摘要（本地原件 `../experiments/sourceidentity/calibration-validation.json`，不随Git上传）。

| 状态 | 事实与边界 |
| --- | --- |
| 已完成 | namespaced的56B/schema2实际socket namespace观察与完整profile接线；原profile指令段兼容；三个受影响对象各4步离线构建及真实只读解析通过 |
| 已完成 | 原accepted见证+独立client/serve fixture+离线checker；共享回归129项、checker12项、fixture fake race/vet/build通过；4个合成CLI案例按预期接受/拒绝 |
| 进行中 | T132真实scope/attach连续性、双cookie/唯一关联、启动与名称一致性及在线核心接线；须先获得小规模内核实测依据，不能用离线一致放行路由 |
| 失败待决策 | 本轮无未决代码失败；审查所见缺listening校验已修正。旧G01 0/100、首次active失败及tests-first/初版记录保持，不改评、不新开正式分母 |
| 环境不足未验证 | 仍无隔离BPF/cgroup环境；当前宿主先前NETNS ENOPROTOOPT不重复尝试。未运行socket/BPF或改变cgroup，未验证内核、平台或性能，139/21与T132未勾选保持 |

executor实际gpt-6.1-sol / medium负责已定接口实现与定向验收，scout实际gpt-6-luna / high负责固定上游和稳定代码只读审查；主代理负责关键关联/C/边界裁决、真实离线工具和最终验收。最多两个子任务，不再派生。二进制和原始日志在忽略的private中，旧JSON、001–007补丁和旧对象不覆盖。后续由既有环境限制决定可执行范围，不以每个小组件完成作为再次要求用户“继续”的节点。


## T132 三进程驱动与完整材料准备

日期：2026-10-24。沿用 speckit-implement 与 karpathy-guidelines，requirements只读16/16。见[契约](../specs/001-hoopa-proxy-manager/contracts/fixture-run-v1.md)、[验收](validation/fixture-run.md)和机器记录（本地原件 `../experiments/sourceidentity/fixture-run-validation.json`，不随Git上传）。

| 状态 | 本轮事实与边界 |
| --- | --- |
| 已完成 | 单一owner管理一个server和两个client、有限输出/清理、显式执行开关、先listening再client、输入及fixture快照；observer marker绑定boot/clock/input，补time namespace核对 |
| 已完成 | 独立11文件checker核对PID/argv/exit/EOF/时序与SHA，scope声明还须绑定observer结果；58项定向测试通过，其中真实子进程仅无网络Python程序，driver/scope和BPF结果均为假接口/合成材料 |
| 进行中 | T132真实scope/attach连续性、原socket唯一关联、启动与名称及在线核心接线；T136仍依赖T137，不能绕过T132直接放行 |
| 失败待决策 | 本轮无未决代码失败；首spawn余量重核验、observer结果摘要绑定经审查补齐并定向验证。tests-first失败及旧G01 0/100、首次active RuntimeError保留 |
| 环境不足未验证 | 未提供隔离BPF/cgroup环境，本机NETNS失败不重试；无真实fixture/socket/BPF或cgroup改变，无新正式身份分母，T132未勾选且139/21不变 |

executor实际gpt-6.1-sol / medium负责已定接口子进程管理、checker、集成测试与文档；scout实际gpt-6-luna / high负责依赖和本地Python所有权证据、稳定代码增量审查；主代理负责时序/作用域语义、driver与最终验收。最多两个子任务，未再派生。旧JSON、001–007补丁、旧对象和Go fixture源码保持；产物和原始日志只在忽略的private目录。


## T132 隔离来宾真实两流与兼容修正（2026-10-24）

用户后续“授权都给你，继续吧”覆盖隔离环境准备和执行。本轮连续推进下载、真实启动、失败诊断和修正，没有要求用户反复确认；完成边界见[独立验收](validation/isolated-linux-lab.md)及机器摘要（本地原件 `../experiments/sourceidentity/isolated-lab-validation.json`，不随Git上传）。

| 工作 | 状态 | 证据或下一步 |
| --- | --- | --- |
| 无宿主系统改动的私有KVM/QEMU与官方内核 | 已完成 | KVM API/create关闭实测；官方Kata QEMU与WSL MSI私有提取，MSI官方SHA匹配；无网卡/host share，9次VM启动/退出原始日志保留 |
| 本轮实际失败诊断与修正 | 已完成 | Kata环境拒绝、root cgroup预检、旧libbpf BTF失败、map权限混淆均记录；现代opts及FD权限修正经58定向测试与真实来宾验证 |
| 原样导出checker与首组真实两客户端 | 已完成 | 20项checker测试；原收据未改，完整检查一致，PID83/84及原accepted cookie配对；5离线变异+2CLI反例拒绝，能力仍unverified |
| T132剩余真实故障、启动身份与在线核心接线 | 进行中 | 已有可用隔离环境；下一步真实停止交错、漏挂/范围/复用/转交负例，以及启动身份/名称与在线一致性；不把离线检查当路由前保护 |
| 历史首次active RuntimeError根因 | 失败待决策 | 保留原失败，根因未证实；本轮局部校准不改判，也不重评原G01 0/100 |
| 首选6.6、跨平台和实际SSH/休眠行为 | 环境不足未验证 | 本轮只实际运行上述6.18来宾，不宣称产品平台正式支持或补齐宿主生命周期验收 |

当前仍为139项/21完成；T132/T137/P0/P1未放行。主代理负责真实VM、scope判断与最终验收；executor配置gpt-6.1-sol/medium，scout配置gpt-6-luna/high，另用只读explorer映射ABI和受影响测试；最多两个子任务并行，写入范围独占，无子代理再派生。私有脚本/镜像/二进制/原始日志留在忽略的private目录，历史manifest、核心补丁和BPF对象不覆盖。

本轮继续真实负例后的更新：错误cgroup准入拒绝与另行预登记的SIGKILL事后校准拒绝均命中各自有限判据；SIGTERM原预期失配单独保留，未篡改登记。SIGKILL后fixture仍通信的事实说明生产转发前保护尚未实现，T132的进行中优先项变为在线/核心来源失联拒绝，不能只依赖marker或事后核验。新预期失配的原因与处理已裁决，不是待用户授权；原历史active根因仍未证明。

最终调用链复核：当前只有 `experiments/sourceidentity`，没有生产 `internal/sourceidentity`；BPF 尚无同步 tuple→client cookie、accepted cookie→client cookie 绑定 map，`NativeBackend.snapshot` 只允许停止排空后扫描。受控核心的接线位置在 0007 补丁的 `serveEntry` 接受连接后、启动 worker/调用 `mixed.HandleHoopaConn` 前。下一步先由主代理冻结 C→A→P 的在线发布顺序、generation/冲突规则、link/map 持有与准入失效顺序，再实现有真实允许与拒绝用例的有限后端。单独检查 PID、给离线快照加在线接口、或只写永远拒绝的占位后端，都不能关闭这项证明缺口；诊断 fixture 与原记录继续保留。


## T132 有限在线准入与身份边界实测（2026-10-24）

沿用 speckit-implement 与 karpathy-guidelines；requirements只读16/16，前后无扩展hook。用户已有隔离实验授权，继续实现、实际加载、故障诊断与重跑，无再次权限询问。见[验收](validation/connector-online.md)、[冻结契约](../specs/001-hoopa-proxy-manager/contracts/connector-online-spike-v1.md)和机器摘要（本地原件 `../experiments/sourceidentity/online-validation.json`，不随Git上传）。

| 状态 | 工作与证据 |
| --- | --- |
| 已完成 | online BPF同步tuple/accepted绑定、只插入冲突拒绝、root link/source leaf分离；单服务持有listener、原socket、link/map，Q前无应用读写；不提升旧observer能力 |
| 已完成 | 130项profile及受影响回归、9项准入、19项client测试通过；实际新对象CO-RE加载于6.18 guest，host5.10 BTF编译事实分列；无宿主BPF加载或系统代理改动 |
| 已完成 | 13次VM保留1次失败，12个有效场景取得14条允许配对、七种拒绝条件；线程、父connect/子写、SCM转交保持connector；同tuple新连接真实建立后因fault在应用I/O前拒绝 |
| 已完成 | 首次thread镜像缺libgcc导致-6，补guest依赖后新登记重跑exit0；原failed及其镜像/日志保留。V1范围外阶段断言由host材料核对补强，旧guest未覆盖；稳定源码/镜像/导出摘要复核 |
| 进行中 | T132生产核心原accepted接线与核心/加载端资源移交、持续覆盖、长期map回收、重启及完整namespace/exec/改名矩阵；名称采样非原子、路径unknown。T132/T137未勾，139项/21完成、P0/P1未放行 |
| 失败待决策 | 历史active RuntimeError根因仍未证明；原G01 0/100保留。本次缺库失败已诊断/修正，不冒充尚缺用户决策，也不以重跑抹除失败 |
| 环境不足未验证 | 首选6.6、其他平台及真实SSH/注销/休眠未验；现有隔离6.18已可执行，不再归类为缺BPF环境。四次ready资源单点不等于CPU/RSS预算或kernel map完整内存账 |

新服务退出后入口终止是来源准入实验的证据，不是“Hoopa管理器与代理同生共死”的产品设计；管理器退出仍转发由T136/G04另验。当前只回显token，未接入Mihomo代理协议；正式G01新增样本0，旧正式分母/旧core补丁001–007/历史JSON和BPF对象保留。

主代理负责同步发布与撤销协议、BPF/准入实现、KVM执行和最终裁决；executor实际配置gpt-6.1-sol/medium负责profile接口、独立client、host checker和指定文档，scout实际配置gpt-6-luna/high负责固定内核源码及稳定harness/材料证据。最多两个子任务，无子代理再派生。审查发现link创建后追加列表可能漏登记FD，已改为预分配两个所有权槽并定向验证。

本轮/tmp满盘曾使沙箱在运行命令前失败；只清理核验为Hoopa专用的可重建Go build cache，释放约2.7GiB。源码、固定二进制、依赖缓存和原始运行日志保留。未把环境失败记为测试通过；最终JSON生成脚本的Python3.8 Path API兼容错误亦保留在私有audit说明，修正后才写入摘要。

## T132 核心原入口、资源移交与 v3 来源接线（2026-10-24，进行中）

沿用已有授权持续执行。冻结[核心移交契约](../specs/001-hoopa-proxy-manager/contracts/connector-core-handoff-v1.md)：一次性加载器通过同进程 exec 将 11 个 BPF 资源 FD 移交核心；核心在原 accept 后、协议 worker 前检查 connector，并以不可变副本传入同一 flow。旧原型、旧补丁001–007与旧正式 G01 0/100 保留；本节后续实测结果另追加。

| 状态 | 当前证据与剩余工作 |
| --- | --- |
| 已完成 | 独立 Go coreadmission 的119项含子测试 race 检查；bootstrap新增25单测、26相关回归；v3 wire严格字段/原ABI/去重重开235项+receiver5项；私有核心新接线17项含子测试通过。单测不代表真实BPF完成 |
| 已完成 | 首次真实核心执行成功移交/核验11FD并进入running；采样器扫到瞬时消失的普通控制FD导致首轮中止，原VM与失败保留；采样已限定为明确移交FD |
| 进行中 | v3同flow原始事件已实录原PID/启动/cookie；发现全局质量开关仅认v2，已补3，待新候选实测/独立fresh-apply校验；受控HTTP与SOCKS分别走两条实际线路 |
| 失败待决策 | 提前SHUT_WR的试验触发现有relay结束：目标收发33B，核心入口上行33/下行0，client未收到完整响应。保留失败，独立定位其来源/支持边界；正常请求响应fixture先完成响应后EOF，不能据此抹除提前半关闭问题。历史active根因仍未证明 |
| 环境不足未验证 | 首选6.6、其他平台、真实SSH/注销/休眠；当前6.18隔离环境可用，不归类为缺内核测试环境 |

T132/T137仍未勾选，139项/21完成，P0/P1未放行。v3 connector专用字段保留unverified，传统process仍unknown；统计恢复投影暂不新增身份展示，原帧保留不等于产品界面已实现。进程规则仍明确拒绝。当前由主代理决策生命周期/锁/持久语义与执行VM，executor实际配置gpt-6.1-sol/medium负责指定实现/测试，scout实际配置gpt-6-luna/high只读提取证据；最多两个子任务，不再派生。

### 同轮核心接线阶段验收与继续项

[完整验收](validation/connector-core.md)与机器记录（本地原件 `../experiments/sourceidentity/core-validation.json`，不随Git上传）已写入；[候选manifest](../experiments/mihomo-events/core-connector-candidate-manifest.json)固定001–009及最终SHA `452effa120f0f0beea122169c9e442b6338389bc4768111c4ed7ffb8d5f44f9b`。新pristine顺序check/apply/build与审查源树完整一致；展示版本仍为上游无ldflags默认1.10.0，不冒充固定source v1.19.26或Hoopa发行版本。

| 状态 | 更新 |
| --- | --- |
| 已完成 | 核心原accepted准入、11FD同进程exec、严格v3原始同flow身份/ready/线路/终末；正常核心重开保留旧来源逐字摘要，两来源世代继续转发 |
| 已完成 | 16次VM中14次满足有限判据，2次失败原证据保留；最终固定hash独立9场景、7条有效配对、48帧。normal与early-SHUT_WR两协议各33B双向；drain旧流45B双向；wrong generation/mapFD/programFD在source/入口创建前拒绝 |
| 已完成 | 采样FD消失失败修正；半关闭根因已裁决为Hoopa wrapper阻断common.Cast递归，0009显式CloseWrite保持计数/终末锁序，原早半关闭方式真实复验通过。上表该问题不再是“失败待决策” |
| 已完成 | 核心5包203项含子测试race，半关闭9顶层、bootstrap25+26、Hoopa默认406pass/7skip和最终候选真实protected49pass/0skip；套件重叠不求和，稳定只读审查无指定路径具体critical/high |
| 进行中 | 固定核心上的FD转交/线程/exec/改名等T132矩阵、活动来源异常结束gap及覆盖连续性/namespace/长期map策略；下一工作包为独立代理协议client边界准备，不修改已冻结候选 |
| 失败待决策 | 历史active RuntimeError根因仍未证明，原正式G01 0/100保留；本轮两次失败都已诊断，不能借此擦除旧记录 |
| 环境不足未验证 | 6.6/其他平台/真实SSH与注销休眠；已有6.18实验可用。单点资源采样不等于预算验收 |

T132/T137、139/21、P0/P1与新增正式分母0保持。当前模型分工仍为主代理做正确性/验收、executor gpt-6.1-sol/medium实现与验证、scout gpt-6-luna/high只读证据；没有自动切换主模型。无用户重新授权问题，继续必要后续工作。requirements16/16未改；未发现扩展hook配置。

### 固定核心的五类身份边界（2026-10-24）

继续执行已有授权，未要求用户逐步确认。见[边界验收](validation/connector-core-edges.md)和机器记录（本地原件 `../experiments/sourceidentity/core-edges-validation.json`，不随Git上传）。

| 状态 | 事实与下一步 |
| --- | --- |
| 已完成 | 新 client 22 fake + 旧 client 19 回归、私有 harness 73 断言；完整 exec oracle roundtrip 修正缺 port，修前日志保留；稳定只读审查无指定路径具体 critical/high |
| 已完成 | 同一最终核心五次真实 VM：thread、fork-writer、SCM、rename、exec，各 HTTP/SOCKS 两流；10 条流 / 65 帧均按原 PID/start/socket、token 实际线路、ready 和双向 33B 终末核对一致；本批无失败 |
| 进行中 | 活动流 checkpoint 后 SIGKILL、同来源重开及只读 pending 边界；T132 剩余短流/复用/namespace/连续性与名称一致性，当前新矩阵不自动升级 capability |
| 失败待决策 | 历史首次 active RuntimeError 根因没有足够证据，旧 G01 0/100 与旧失败材料保留；本轮不补造根因，也不要求用户重复授权 |
| 环境不足未验证 | 首选 6.6、其他平台、真实 SSH/注销/休眠与性能预算；6.18 隔离 guest 已可执行，不再归类为缺整个实验环境 |

139/21、T132/T137 和 P0/P1 状态不变，正式 G01 新样本0。核心001–009/BPF/旧 manifest 无修改，二进制、镜像和原始运行材料只在被 Git 忽略的 private 目录。主代理持续执行后续恢复实验；executor gpt-6.1-sol/medium 与 scout gpt-6-luna/high 按指定范围工作，无子代理再派生。

### 活动来源 SIGKILL 与 v3 恢复回归（2026-10-24）

见[验收](validation/connector-core-crash.md)及机器记录（本地原件 `../experiments/sourceidentity/core-crash-validation.json`，不随Git上传）。

| 状态 | 证据与后续 |
| --- | --- |
| 已完成 | 私有 harness 60 断言；真实1VM/2流/13帧，旧CP12/12后核心-9、客户端确认断流，新捕获世代/来源/内核对象继续33/33；30导出文件摘要复核 |
| 已完成 | 仅测试Go overlay，固定核心实际openSource对真实原帧副本重开3次；race 1顶层通过、0skip，旧缺口ID/事实/检查点稳定、未知尾部null、完整重放与原文不变，独占锁实际拒绝第二writer |
| 进行中 | 同名父子独立连接/并发与PID复用已准备；T132名称一致性、完整scope/namespace/连续性及余下矩阵；T138/T073生产恢复报告传输与账本实现仍待完成 |
| 失败待决策 | 历史active RuntimeError根因未证明、旧G01 0/100保留；本批实际实验无失败。摘要提取首次误将Go JSON分片当完整报告，已拼接Output后解析，原测试成功日志未改 |
| 环境不足未验证 | 6.6/其他平台/真实SSH注销休眠/性能预算；当前隔离6.18可用 |

实际VM受保护接口未导出内部恢复报告，外部只读仍pending；独立writer探针不能冒充产品gap接口。完整生产源树与候选hash复核不变。139/21、T132/T137、P0/P1及新增正式G01样本0保持。主代理负责恢复语义/真实执行/最终验收，executor配置gpt-6.1-sol/medium负责私有工具，scout配置gpt-6-luna/high只读审查；最多两个子任务，无再派生。

### 同名父子并发与 PID 实际复用（2026-10-24）

[验收](validation/connector-core-lifetimes.md)及机器记录（本地原件 `../experiments/sourceidentity/core-lifetimes-validation.json`，不随Git上传）已保存。已完成：104 离线断言后2VM/4流/26帧，父81与子83同名独立建连且两ready先于任一closed；PID30000两次实际分配且先reap，leader启动与cookie不同，原历史保持。进行中：原connector早退后writer继续、名称一致性与其余namespace/tuple/覆盖矩阵。失败待决策：历史active根因未证明与旧G01失败保留，本批无实际失败。环境不足未验证：6.6/其他平台/真实SSH注销休眠与性能预算。

稳定工具及导出摘要复核，既有候选/BPF/旧记录未改；139/21、T132/T137、P0/P1及正式G01新增0保持。executor配置gpt-6.1-sol/medium完成独占私有工具和104检查，主代理审查稳定代码并实际build/run/严格验收；scout配置gpt-6-luna/high继续独立内核名称证据。最多两个子任务，无再派生，无新增用户授权问题。

### 原 connector 早退后的 FD 使用（2026-10-24）

见[验收](validation/connector-core-earlyexit.md)/机器记录（本地原件 `../experiments/sourceidentity/core-earlyexit-validation.json`，不随Git上传）。已完成：85项离线检查后1VM/2流/14帧，原子进程83/87均转交socket并exit0/reap，父82/86确认/proc消失后才握手；归属保留子PID/start/cookie，双向33B与两线路ready核对一致，22导出文件摘要复核。进行中：namespace拒绝矩阵、名称一致性和剩余tuple/覆盖故障。失败待决策：旧active根因未证明及G01 0/100保留，本批无实际失败。环境不足未验证：6.6/其他平台/真实SSH注销休眠/性能预算。

不声称Admit发生于退出后，未伪造死进程的新名称观察；固定核心/BPF保持，139/21/T132/T137/P0/P1/正式新增样本0不变。executor配置gpt-6.1-sol/medium准备独占私有工具，主代理稳定审查与真实VM/严格验收，scout配置gpt-6-luna/high研究固定内核名称可行性，无子代理再派生。

### PID/net namespace 范围拒绝与证据通道（2026-10-24）

见[验收](validation/connector-core-namespaces.md)/机器记录（本地原件 `../experiments/sourceidentity/core-namespaces-validation.json`，不随Git上传）。已完成：V2 117项离线检查后2VM，原始connect均EPERM、0应用字节/flow/实际线路，34导出文件和实际BTF核对。进行中：名称挂接/原子指令可行性、tuple复用、完整覆盖与故障矩阵。失败待决策：历史active根因未证明与旧G01保留；本批首轮UART导出混入内核日志并出现soft lockup，原失败保留，改用独立virtio通道的另两轮通过。环境不足未验证：6.6/其他平台/真实SSH注销休眠/性能预算。

现有核心/BPF未改、139/21、T132/T137及正式G01新增0保持。主代理执行实际VM与判据验收；executor配置gpt-6.1-sol/medium准备独占工具，scout配置gpt-6-luna/high核对固定内核源码。新增源码证据显示ftrace可能在BPF递归计数之外跳过钩子，因此零miss不能独立认证名称快照；继续研究，不伪装为通过。

### 名称原子指令的真实可行性（2026-10-24）

[验收与设计条件](validation/connector-name-feasibility.md)/机器记录（本地原件 `../experiments/sourceidentity/name-atomic-validation.json`，不随Git上传）保存：已完成75项离线检查及1VM，实际BTF、3程序JIT、2次改名/1次connect状态与FD清理符合判据；纯SC有限模型125216交错/16检查只作假设内证据。进行中：guest ftrace开关反例、完整覆盖/内存序/写路径/生命周期，固定核心tuple复用拒绝。失败待决策：历史active未明和G01旧失败保留，本次实际探针无失败；ftrace不计数漏钩子是设计缺口，不能靠重跑清除。环境不足未验证：6.6/其他平台/真实SSH休眠与预算。

主代理负责原子状态设计、原探针、实际VM与验收；executor配置gpt-6.1-sol/medium完成独占harness，scout配置gpt-6-luna/high完成固定源码取证，报告对x86_64 stats重试的早期误述已修正。未认证name、未改核心，139/21/T132/T137及正式门槛保持。

### 名称钩子覆盖反例成立（2026-10-24）

已完成：102离线检查+1隔离VM，ftrace 1→0→1时实际名字b→c→d，entry/exit 2→2→3、seq4→4→6，三程序ID不变且miss/poison一直0；最终开关/FD/comm清理正确，6导出文件核对。见[设计证据](validation/connector-name-feasibility.md)/机器记录（本地原件 `../experiments/sourceidentity/name-coverage-validation.json`，不随Git上传）。这是预期负向反例通过，不是名称方案通过。进行中：全固定源码写路径审查、TCP回调顺序与核心tuple复用负向实验。失败待决策：零miss认证前提被实际反例否定，名称仍研究；历史active根因和G01旧失败保留。环境不足未验证：6.6/其他平台/真实SSH休眠/性能预算。139/21/T132/T137/P0/P1保持，正式新增0。

### 固定核心的四元组复用拒绝（2026-10-24）

见[验收](validation/connector-core-tuple-reuse.md)/机器记录（本地原件 `../experiments/sourceidentity/core-tuple-reuse-validation.json`，不随Git上传）。已完成：V2 96离线检查后1VM，两个不同PID/start/cookie实际同tuple connect成功，首流33/33完整关闭，第二流协议前拒绝、无事件/实际线路；同core累计admitted1/rejected1/revoked，23导出文件核对。进行中：exe_file路径快照独立spike、完整连续性/字段能力与剩余故障矩阵。失败待决策：首轮误把当前连接数当累计accept导致夹具超时，第二客户端未启动；旧run保留，V2明确gauge语义且增加最后实际状态诊断。历史active根因和旧G01保留。环境不足未验证：6.6/其他平台/真实SSH休眠/预算。

主代理审查稳定差异、实际build/run和验收，executor配置gpt-6.1-sol/medium独占工具准备，scout配置gpt-6-luna/high审查TCP回调源码及前批稳定文档；前批审查未发现critical/high。无子代理再派生。139/21/T132/T137/P0/P1及正式新增0保持；容量/复用导致整个世代拒绝的新可用性限制明确保留。

### 可执行文件快照的真实最小验证（2026-10-24）

[验收](validation/connector-executable-snapshot.md)/机器记录（本地原件 `../experiments/sourceidentity/executable-snapshot-validation.json`，不随Git上传）已保存。已完成：108准备检查后1VM，LSM带引用exe_file/d_path/put与connect4同cookie关联，4阶段EPERM/ECONNREFUSED/ECONNREFUSED/EPERM，2真实路径记录，解除钩子后新cookie无证拒绝；3导出文件核对。初版主机checker混淆map对象flags/FD权限及tracing union对齐而失败，原材料全部保留；修订checker独立86检查后复核同一run通过，未重跑VM/增加样本。进行中：同对象路径改名/超长/删除矩阵、字段语义与完整关联连续性。失败待决策：名称覆盖设计缺口、历史active根因和旧G01保留，checker错误已修正。环境不足未验证：6.6/其他平台/真实SSH休眠/预算。

主代理负责kfunc引用语义/C对象、稳定差异审查/VM与验收，executor配置gpt-6.1-sol/medium负责独占私有工具和相关检查，scout配置gpt-6-luna/high只读固定源码。没有将exe路径冒充comm，未接核心，139/21/T132/T137/P0/P1及正式新增0保持。

### 可执行文件改名、超长与删除（2026-10-24）

[验收](validation/connector-executable-path-boundaries.md)/机器记录（本地原件 `../experiments/sourceidentity/executable-path-boundaries-validation.json`，不随Git上传）：已完成173准备检查后1VM，6新cookie/5记录；同dev2/inode8依次改名、282B路径返回-ENAMETOOLONG并EPERM、移回后恢复、删除保留原文、解除LSM后新cookie拒绝。旧记录字节保持，3导出文件核对及清理通过。进行中：同socket重试与证据消费、普通sockops覆盖边界及产品字段来源。失败待决策：本批实际无失败，既有名称覆盖/历史active/旧G01保留。环境不足未验证：6.6/其他平台/真实SSH休眠/预算。

主代理执行稳定审查/VM/验收，executor配置gpt-6.1-sol/medium独占准备工具，scout配置gpt-6-luna/high只读关联API与dispatch证据。SOCK_OPS没有反向TCP lookup helper，不能声称SK_STORAGE自动解决关联。未接核心或升级正式能力，139/21/T132/T137/P0/P1保持，正式新增0。

名称来源候选已记入[ADR009](adr/009-process-label-sources.md)：固定Mihomo Linux源码使用exe路径basename，不等于kernel comm。工程方案分开程序名/路径、comm、应用显示名，明确解释器/删除/编码/历史与规则来源边界；未写成用户新增澄清或正式能力通过，旧v3/G01不重新解释。

### 同 socket 旧快照反例与一次消费修复（2026-10-24）

[旧对象反例](validation/connector-executable-socket-reuse.md)与[新对象验收](validation/connector-executable-consumption.md)分别保存。已完成：旧对象45项准备检查后1VM复现解除LSM/改名/AF_UNSPEC重置后旧cookie仍放行；新对象53项准备检查后2VM，将已消费旧cookie重试改为EPERM，并保留新cookie正常路径。三轮共9份导出文件分别核对摘要；不是正式身份样本。进行中：已采集但未消费记录的新鲜性、TFO早期passive回调边界和TCP_INFO标志、完整名称/关联连续性。失败待决策：原对象的旧快照重用已实证，CAS仅关闭已消费分支；旧名称覆盖/历史active/G01失败保留。环境不足未验证：6.6、其他平台、真实SSH/休眠与性能预算。

主代理负责原子消费设计、C对象、稳定差异审查、实际VM和验收；executor角色配置gpt-6.1-sol/medium完成私有harness，scout角色配置gpt-6-luna/high核对固定源码；未再派生。TFO旧私人推论“没有passive callback所以必拒绝”已发现错误：固定源码在SYN创建child时回调，普通握手的有限实测不外推到TFO。正在准备独立观察，不把源代码推论记成实际错配。核心001–009、正式能力、139/21、T132/T137及P0/P1门槛不变。

### TFO child 标志与核心拒绝准备（2026-10-24）

[独立观察](validation/connector-tfo-boundary.md)/机器结果（本地原件 `../experiments/sourceidentity/tfo-info-validation.json`，不随Git上传）已完成：76准备检查后1VM，普通/普通半关闭options7、TFO/TFO半关闭167；四条各33/33B，两个半关闭EOF后反向可写，原sysctl恢复、12socket关闭、2导出摘要核验。新准入检查固定6.18 release，原FD读取TCP_INFO在cookie/map/协议前拒绝TFO_CHILD，普通CLOSE_WAIT保留；20顶层101子测试race通过0skip。初次测试错误地仅接受EBADF而遇sandbox EPERM，失败保留，修正只接受这两种实际读取失败。

[新构建记录](../experiments/mihomo-events/core-tfo-candidate-manifest.json)保存干净上游001–010 apply/tree一致及二进制e9dd96…d9be，旧001–009和原manifest未变。进行中：新核心真实转发/earlySHUT_WR/TFO拒绝、未消费旧快照反例。失败待决策：TFO旧source推论已撤回，尚无实际错误归属样本；既有名称/历史active/G01失败保留。环境不足未验证：6.6/其他平台/真实SSH休眠与性能预算。T132/T137、139/21、正式新样本0、P0/P1保持。

### 0010核心普通两协议的真实回归（2026-10-24）

[实际正向验收](validation/connector-core-tfo-positive.md)已完成：新hash在1VM/2flow/13帧中，HTTP84→node:a11ms、SOCKS87→node:b31ms，均send33→SHUT_WR→收33+EOF，身份/实际线路/终末与21导出hash核对。admitted2/rejected0、核心SIGTERM0。进行中：该hash的TFO拒绝，未消费快照反例v2；后者v1已实际证明EISCONN留下0记录，但错误要求已建立连接AF_UNSPEC后SO_ERROR0而停止，重连尚未执行；已按固定源码104→读取清除0登记新v2并保留原失败。失败待决策：既有名称连续性/历史active/G01保留。环境不足未验证：6.6/其他平台/真实SSH休眠/预算。

executor角色配置gpt-6.1-sol/medium曾返回内容风险拦截错误，未完成该轮修订；主代理接手未消费夹具及实际运行，后续普通正向回归准备任务执行器恢复可用并完成46项离线检查。scout角色配置gpt-6-luna/high完成固定源码有界调查，无再派生。未把准备或局部回归升级为任务完成，139/21/T132/T137/P0/P1及正式新增0保持。

### 未消费旧记录的反例完整成立（2026-10-24）

[验收](validation/connector-executable-unconsumed.md)/机器记录（本地原件 `../experiments/sourceidentity/executable-unconsumed-validation.json`，不随Git上传）：已完成，v2在45准备检查后1新VM观察EISCONN106留下consumed0，LSM解除/文件改名/AF_UNSPEC后原cookie重连111消费旧路径为1，新cookie1拒绝；3导出核对，清理通过。首VM错误预期SO_ERROR0而实际104停止在重连前，原材料保留；新v2按固定源码验证104→读取清除0，不能把首轮当完整反例。进行中：同一次connect的调用token新鲜性方案源码核验、新核心TFO拒绝。失败待决策：单靠CAS的完整新鲜性前提已被实际否定，不靠增加重复次数掩盖；既有名称覆盖/历史active/G01保持。环境不足未验证：6.6/其他平台/真实SSH休眠/性能。主代理接手修订与实际运行，scout有界核对新token可行性；139/21/T132/T137/P0/P1及正式新增0不变。

### 0010核心TFO拒绝与调用token准备（2026-10-24）

已完成：[TFO同tuple负向](validation/connector-core-tfo-negative.md)49准备检查后1VM，第二原进程181B完整TFO发送/原TCP_INFO SYN_DATA32后reset0回应，首流33/33及6517B前缀不变，7帧/24导出核对，sysctl恢复，累计admitted1/rejected1/revoked。进行中：[connect调用编号原型](../specs/001-hoopa-proxy-manager/contracts/connector-executable-call-token-v1.md)，新296B记录/64B state/task storage8B，raw entry/exit与饱和CAS编号候选已编译；实际verifier及缺钩子/旧快照/普通恢复矩阵尚未运行。失败待决策：已证CAS单独不足的新鲜性反例保留，既有名称覆盖/历史active/G01仍保留。环境不足未验证：6.6、其他平台、真实SSH/休眠、性能预算。

主代理完成TFO实际VM/验收及调用编号语义与C实现；executor配置gpt-6.1-sol/medium完成TFO准备与稳定证据文档、继续独占token harness；scout配置gpt-6-luna/high只读核验固定源码和候选指令，未再派生。首轮token实验限制单线程/可信loader，跨线程fault全序不借此次声称解决。139/21、T132/T137/P0/P1及正式新增0保持。

### 同调用编号实测与v4接线（2026-10-24）

已完成：[调用编号有限实测](validation/connector-executable-call-token.md)38准备检查后1VM，10次native connect含AF_UNSPEC、9个token；旧未消费记录不改、缺LSM/entry及两种TFO路径拒绝，恢复普通新socket到达无监听端口ECONNREFUSED。它不是代理转发成功；18项离线变异检查不增加运行样本。public C与实际私有C逐字节一致，scout独立检查原始导出、计数、清理与稳定文档无critical/high矛盾。v4严格解码38顶层/298含子测试race通过0skip，首次255B路径测试夹具覆盖NUL的失败保留，仅修夹具；纯值exe ABI定向4顶层race通过。

进行中：组合对象9maps/6programs/5links的20FD移交，原accepted校验与同flow v4输出；组合对象只完成编译/离线ELF解析，尚无verifier/运行证据。失败待决策：旧CAS未消费反例及名称覆盖/历史active/G01仍保留，不能用新局部通过覆盖。环境不足未验证：6.6、其他平台、真实SSH/休眠及性能预算。139/21、T132/T137/P0/P1与正式新增0保持。

executor角色配置gpt-6.1-sol/medium在调用编号harness再次收到工具内容风险拦截，未完成该包；主代理保留其部分文件，接手C语义、全部剩余harness、VM和18项变异。随后executor完成普通Go v4/ABI及稳定证据文档，继续Python接线；scout角色配置gpt-6-luna/high只读调查及验收，未再派生。该中断并非自动审批拒绝，也不是测试失败或已完成执行的证据。

### 组合核心构建与稳定接线审查（2026-10-24）

已完成：严格20FD handoff v2、完整rawTP目标名/LSM BTF对象内容核验，296B记录与原client cookie/PID/TID/时间关联；profile4保留原帧及终末/恢复语义。Python相关126项fake通过；public核心包28顶层/144含子测试race通过；上游wire/writer/guard最终批375项含子测试通过，两个显式网络测试skip；补充profile4 guard定向5项通过。两次上游wire测试因镜像测试缺JSON fixture失败保留，修正测试路径与复制fixture后通过，无放宽校验。第一次准备脚本工作目录错误未写目标，修正后生成测试，未隐去该执行错误。

新[manifest](../experiments/mihomo-events/core-executable-candidate-manifest.json)核对干净上游001–011 apply、目标/新树完全一致及binary `4fab2b7f44513d57c2629a7b68c3c4ee4634130a10135e59930b0f57925788bb`。scout对稳定Go/Python/核心接线未发现critical/high；实际组合verifier/接管/两协议转发仍进行中，准备记录 `private/t132-executable-online/checkpoint-pre-vm.json` 不代表运行通过。新组合一次exe拒绝会使generation持续拒绝，raw/LSM不属于原writer gate完整drain，已在[组合契约](../specs/001-hoopa-proxy-manager/contracts/connector-executable-online-v1.md)显式保留。

失败待决策与环境未验证状态同上节；历史G01 0/100、T132/T137未勾选、139/21及P0/P1门槛不变。root负责复杂所有权/故障语义、源码和核心build，executor负责已定ABI/Python/新真实夹具准备，scout负责冻结差异证据，无再派生。

### 首次组合VM发现profile4线路捕获遗漏（2026-10-24）

失败待修复：`vm-core-executable-positive-oqznnj4m` 实际加载组合对象并由新core接管20FD，两个客户端已完成33/33和EOF，v4 opened含/bin/python3、原PID/cookie及调用编号1/3；但独立host checker发现actual_connection两线路仅internal_name node:a/b，adapter_type为unverified且node_id为null，整轮未通过。21个导出由canonical extractor核对后读取，原source/frame/镜像/制品及失败日志保留。不得将传输或exe局部通过当整轮通过。

scout定位 `constant/hoopa_quality.go` 初始化开关仅profile2/3，遗漏4，导致outbound实际hop/ready捕获未启用；原v4 writer测试用人工构造的connection evidence，未覆盖这个进程启动开关。进行中：新0012仅补profile4，并增加真实子进程环境初始化回归，然后fresh构建与独立新VM重跑原完整判据。已有静态审查“未发现critical/high”仍是其范围内观察，不保证运行成功。环境未验证/正式样本与任务勾选不变。

### 0012组合核心正向运行闭合（2026-10-24）

已完成：[独立正向验收](validation/connector-executable-core-positive.md)使用新001–012 binary `4bc907d7c675ec886b1342f6b05f675f693b2d72f21c5da8557d4958f1d68d27`。76准备检查后1次实际VM/2流/13帧：严格20FD接管，原connector与296B executable记录及独立proc oracle吻合；HTTP PID84/token1/node:a/12ms、SOCKS PID87/token3/node:b/31ms，均提前SHUT_WR后收到33/33和EOF，来源完整终止、核心SIGTERM0。canonical extractor核对21项导出，v4解码及只读重放通过。scout对稳定运行独立复核未发现critical/high矛盾，同时重算0011失败运行21项checkpoint，全部保持原字节。

进行中：同一新组合的线程、父connect/子写、SCM转交、comm改名、exec及路径变化边界。0012只有质量开关一行生产修正及子进程初始化测试，constant真实子进程矩阵9项含子测试、outbound profile4 race13项含子测试通过，干净apply/build及完整树核对见[manifest](../experiments/mihomo-events/core-executable-quality-candidate-manifest.json)。这些批次各自记录，不汇总为无重复数量。

失败待决策：0011整轮失败已定位并由新制品回归关闭，原失败/源码/镜像不覆盖；旧名称覆盖、未消费快照反例、历史active及G01失败仍保留。环境不足未验证：6.6、其他平台、真实SSH/休眠与性能预算。139/21、T132/T137未勾、正式G01新增0/旧0/100及P0/P1门槛保持。主代理负责语义决策、fresh build、实际VM和最终验收，executor（角色配置gpt-6.1-sol/medium）完成最小修复、测试及证据文档，scout（gpt-6-luna/high）独立取证，无再派生。

### 新组合的归属边界与范围拒绝准备（2026-10-24）

进行中：[五类原连接边界](../specs/001-hoopa-proxy-manager/contracts/connector-executable-core-edges-v1.md)覆盖native线程、父connect/子写、SCM、comm改名及换路径exec；原exe快照固定在connect前后，exec后的不同path/inode仅单独观察。[范围拒绝契约](../specs/001-hoopa-proxy-manager/contracts/connector-executable-scope-negative-v1.md)登记每场基线→范围负例→同世代正常新socket的顺序；owner status只计accepted准入，不伪称包含内核拒绝计数。

已完成准备：范围负例81项离线断言、独立稳定源码审查及镜像构建（SHA256 `81c7704703b14f740cbc814f0a1274a012bde3962d14030f783c36669442620d`）；尚无本批VM结果。首次源码生成因原fixture词序不同触发保护断言，未生成镜像；首轮变异发现NSpid首/尾一致性检查遗漏，修正私有checker共享谓词后通过，原错误均保留。没有修改固定核心/BPF或旧原始证据。

主代理负责范围拒绝语义与该独立私有夹具，executor负责另一目录的五类边界夹具，scout只读复核，两子代理无再派生。正式能力、139/21和T132/T137/P0/P1门槛不变；其他平台、6.6、真实SSH/休眠及性能预算仍未验证。

### 新组合八个实际边界场景闭合（2026-10-24）

已完成：[五类归属边界](validation/connector-executable-core-edges.md)191离线检查后5VM/10流/65帧，105项导出重验；[范围拒绝](validation/connector-executable-scope-negative.md)81离线检查后3VM/3基线流/21帧，6次目标connect拒绝，73项导出重验。各场VM/extractor/checker均0，核心正常退出、完整来源和只读重放核对；scout对冻结源码及完成的原始运行独立复核无critical/high矛盾。exe历史未被writer/comm变化或exec后不同inode路径覆盖。

进行中：按[新连接负向契约](../specs/001-hoopa-proxy-manager/contracts/connector-executable-connection-negative-v1.md)准备tuple复用、sendto MSG_FASTOPEN和TCP_FASTOPEN_CONNECT，并检查同世代正常followup。范围实测确认当前采集世代故障后持续拒绝；现有owner计数只反映accepted，未读取BPF fault计数且不能表明采集健康，后续只读诊断/恢复必须补齐。

失败待决策：本批8次实际VM无失败；五类边界两项准备问题、范围夹具词序保护断言及NSpid变异失败均保留并修正，不改变验收口径；旧0011、名称覆盖、旧快照与G01反例不覆盖。环境不足未验证：6.6/其他平台、真实SSH/休眠及性能。主代理执行实际VM并负责范围夹具及语义，executor（gpt-6.1-sol/medium）负责边界夹具/结果文档，scout（gpt-6-luna/high）独立只读验收，无再派生。T132/T137、139/21、正式新增0及P0/P1门槛保持。

### 三连接负例完成与只读采集诊断（2026-10-24）

已完成：[tuple/TFO矩阵](validation/connector-executable-connection-negative.md)131离线准备、3实际VM、3基线流、21帧、83导出。原始调用、prefix、状态阶段、TFO guest设置恢复及完整终末独立复核符合；旧记录不改。新增诊断Owner与只读接口、raw recursion接管/准入拒绝，整包race34顶层/233含子项、集成7顶层/100含子项和对应vet通过；001–013重新apply/build得到独立binary476d5788…05467b。

进行中：[诊断契约](../specs/001-hoopa-proxy-manager/contracts/connector-capture-diagnostics-v1.md)实际VM，正常零读数与范围拒绝非零读数，前后旧status及重复读取不变；继续连续性与新组合恢复。失败待决策：三个连接VM无失败；准备继承空格检查失败保留。诊断VM准备的host帮助命令缺少guest helper导入路径，已仅给离线命令显式PYTHONPATH后56检查通过，保留失败，不算VM失败。环境不足未验证：其他内核/平台、真实休眠/SSH与性能。

本阶段主代理负责诊断语义/代码、核心集成和实际VM；executor（gpt-6.1-sol/medium）负责新诊断测试与三连接公开记录；scout（gpt-6-luna/high）负责固定内核/ABI与冻结证据复核，未再派生。正式identity/name/path仍false/unverified；T132/T137未勾、139/21、G01新增0。

### 0013诊断实际结果（2026-10-24）

已完成：[只读诊断](validation/connector-capture-diagnostics.md)独立VM/extractor/checker均0，23导出重验，4阶段各2次读取capture_faults=0/0/1/1；原status前后不变，正常基线PID83/node:a/11ms/33/33，两个负向connect均EPERM。实际读到的raw recursion/exe计数为0，不等于真实skip注入或完整hook连续性。

进行中：新0013/v4/20FD[活动流崩溃与来源重开](../specs/001-hoopa-proxy-manager/contracts/connector-executable-core-crash-v1.md)准备，以及固定内核link/程序生命周期证据；旧流应保持pending及未知尾部null，不人工制造API gap。失败待决策：本次实际VM无失败；离线host helper导入问题已经修正并保留原失败。环境不足未验证及正式任务门槛保持。

### 0013/v4崩溃与独立内部恢复闭合（2026-10-24）

已完成：[新组合crash/reopen](validation/connector-executable-core-crash.md)105离线检查后1VM/2core/2flow/13帧/31导出，所有阶段0；旧核心自有SIGKILL=-9、旧client明确断流，旧checkpoint12/12及未知tail=null，新core全新9map/6program/5link、SOCKS终末33/33。两个token1分别绑定捕获generation。独立overlay race1测试通过，3reopen/6reports，真实openSource在私有副本持writer锁生成稳定gap，原帧/生产tree/binary不变；明确不是VM API gap导出。scout准备、原始runtime及overlay源码/结果审查均无重大偏差。

进行中：[固定link操作边界](../specs/001-hoopa-proxy-manager/contracts/connector-capture-link-operations-v1.md)私有夹具，后续并发、早退等完整身份矩阵；source报告生产传输由既有后续任务承担。失败待决策：本批VM及overlay无失败。环境不足未验证：其他内核/平台、SSH/休眠及性能预算。主代理负责实际VM、恢复语义/overlay和结果；executor（gpt-6.1-sol/medium）移植crash并准备link夹具，scout（gpt-6-luna/high）独立证据复核，无再派生。T132/T137与139/21、G01新增0/P0/P1不变。

### 0013挂钩操作边界完成与生命周期新矩阵（2026-10-24）

已完成：[固定link操作](validation/connector-capture-link-operations.md)134离线准备后1VM/2flow/13帧/23导出，三阶段0。自有PID77的六个FD副本验证完整，detach三次errno95、update三次errno22，前后对象信息一致；只关闭六副本/pidfd。HTTP先完成，操作后SOCKS完成，两流33/33；诊断四零、status操作前后相同，最终2/0/false。rawTP probe只读目标名长度的限制经root明确接受，不把本次操作拒绝写成完整连续性。scout稳定源码/真实导出复核无critical/high。

进行中：[新生命周期契约](../specs/001-hoopa-proxy-manager/contracts/connector-executable-lifetimes-v1.md)及私有准备：同名父子独立重叠流、同PID新启动实例、真实停止核心后原connector建连并退出/reap再恢复，解决旧早退未证明Admit先后问题。root负责停止/恢复顺序与原始归属裁决，executor（gpt-6.1-sol/medium）只准备冻结实验，scout（gpt-6-luna/high）只读关联不变量/缺证据审查；不派生、不修改原核心/BPF。

失败待决策：本次VM/checker无失败；首次文档生成器将snapshot字段误按顶层读取而KeyError，已保留失败且只修正取值路径，原始证据未改。环境不足未验证：其他内核/平台、SSH/休眠及性能预算。T132/T137未勾、139/21、正式G01新增0/旧0/100及P0/P1未放行保持。

### 组合关联连续性的定向复核（2026-10-24）

已完成：[连续性判据](validation/connector-continuity-review.md)根据当前组合/Owner和固定内核源码收敛：单值insert-only不等于未漏active回调；普通握手的direct cgroup dispatch与TFO先passive例外分开；raw/LSM不支持detach，而cgroup link支持显式detach且cgroup_id变0。独立scout三份有界源码报告和公开说明复核无critical/high；旧在线实验实际是close link FD，其措辞已明确，不误称执行过BPF_LINK_DETACH。

进行中：executor完善新生命周期3模式，scout阶段性审查STOP/child退出/reap/CONT及cleanup（标注未冻结）；root登记[新组合cgroup hook实际移除](../specs/001-hoopa-proxy-manager/contracts/connector-cgroup-hook-loss-v1.md)，待准备后实际验收。资源采样只读stat/status/固定map fdinfo，动态task-storage内存与无接口的Admit等待不猜值。失败待决策：本次源码复核没有新运行失败；连续性前提仍需后续验收，不能记为通过。环境不足未验证和正式任务门槛保持。

### 新生命周期矩阵首轮启动失败（2026-10-24）

失败待决策：`private/t132-executable-lifetimes/vm-family-concurrent-cqu0mox7` 的VM/extract为0，严格checker为1；guest只导出readiness拒绝记录，原因是main调用未定义tcp_settings，核心尚未启动、没有flow，PID复用/早退两模式未执行。镜像v1、原始文件和matrix-lifetimes-v1全部保留。157离线检查/阶段性审查没有覆盖实际guest入口依赖，不能代替运行。

进行中：定向入口核查又确认export_btf定义也遗漏；executor补只读helper与实际main前置路径的pure/fake组合回归，旧副本先复现失败、新三模式必须走到admission入口，然后另建v2镜像。scout定向核查main调用链，root决定新输入有因果修复后再运行。不改变生产核心/BPF、判据或旧结果。已完成：cgroup移除底层探针73纯/fake检查（0实际pidfd/BPF）；该后续harness暂缓至生命周期修复冻结。环境不足未验证及139/21、正式任务门槛保持。

### 0013生命周期三模式实际完成（2026-10-24）

已完成：[并发/PID复用/接纳前早退](validation/connector-executable-lifetimes.md)在v2镜像三个独立VM均通过，VM/extract/check各0；162离线检查、6流/39帧/77导出分记。父85/子86两ready早于首closed；PID30000的start ticks452→481；原connector87/91在core78全部8task确实停止期间建连、转交、退出/reap/消失，SIGCONT后原身份仍进入opened，writer86/90未替代。各流33/33/SHUT_WR后完整EOF，实际两线路、终末、资源只读原文均核对；scout独立重验77导出和三个归档摘要。

进行中：cgroup真实hook移除两模式及连续性/能力前提收口。失败待决策：v1缺tcp_settings启动失败已通过补两个只读helper和实际main启动回归解决，新v2真实通过；原v1和未执行到的export_btf静态缺失分开记录。没有改核心/BPF或降判据。环境不足未验证：其他内核/平台、SSH/休眠、10分钟性能；Admit内部等待无接口仍unavailable，短CPU/RSS和map类型memlock不冒充完整预算。

主代理执行实际VM、决定顺序不变量并整理公开结果；executor（gpt-6.1-sol/medium）准备冻结harness，scout（gpt-6-luna/high）独立原始证据复核，不派生。139/21、T132/T137未勾、正式新G01=0/旧0/100及P0/P1未放行不变。

### 两种cgroup实际移除完成与准入计时准备（2026-10-24）

已完成：[两实际移除](validation/connector-cgroup-hook-loss.md)均VM/extract/check0，2正常基线/2负向新connect/14帧/56导出；原FD保留但link cgroup_id1→0，只有副本/pidfd被关闭。新connect0后EOF且协议/应用零写，准入1/1/revoked；sockops四诊断0、connect4 capture_faults2，其余0；旧prefix/完整来源33/33不变。独立scout复核运行，executor独立重算公开摘要/帧；未重跑原VM。

进行中：[原Admit计时契约](../specs/001-hoopa-proxy-manager/contracts/connector-admit-measurement-v1.md)与独立Go overlay测量制品，只对原Admit方法改名、wrapper前后单调计时，成功按原cookies/世代与后续opened匹配；含mutex和同步核验，不含accept前排队/内核回调及日志输出。原34顶层/233含子race与离线build0；新binary b4b9f0df…862b1808，原0013源码树与二进制不变。尚未实际测量，不更改旧unavailable。scout冻结overlay审查无重大偏差。

失败待决策：cgroup首离线build的guest pin清单问题已修正，旧记录保留；其公开生成器最初误用Python3.9 Path API，在公共写入前修为3.8相容路径并通过。root首次计时overlay误用coreadmission包名，与上游hoopaid不符，编译前失败已保留；新v2修正包名并增加前置一致性检查，race/build已通过。没有失败VM覆盖。环境不足未验证：其他内核/平台、SSH/休眠及完整性能；正式能力/139/21/T132/T137/G01/P0/P1门槛保持。

### 原准入计时实际完成与受限profile部署前提（2026-10-25）

已完成：[测量VM](validation/connector-admit-measurement.md)三阶段0，父83/子84两流13帧/26导出；原Admit整段161824ns/89686ns与两原accepted cookie、实例和opened唯一关联，ready12/31ms另记。旧0013树/制品不变，测量制品只用于本批，88离线与34顶层/233含子race分开。scout对原始运行独立核验，executor重算公开摘要/帧与原始源码树，均无重大矛盾。

已完成决策：[ADR010](adr/010-restricted-identity-profile.md)限定固定普通TCP/受控cgroup/持续hook等前提，Owner只有入口一次link核验；默认程序名取同次executable basename，comm与应用显示名独立。不伪称用户新增澄清、不改旧v3/v4。固定源码反证复核未找到前提内普通active遗漏后无故障沿用旧tuple的路径，但不冒充实际逐回调覆盖或任意root对抗。

进行中：[非特权客户端](../specs/001-hoopa-proxy-manager/contracts/connector-unprivileged-client-v1.md)准备：root wrapper先joinleaf后清空capabilities、UID/GID65534和NNP再exec，父进程真实proc核验后才放行connect；实际cgroup写打开、pidfd_getfd和BPF link取FD拒绝分别记录，原两协议仍须正确归属/计量。主代理决定语义/实际VM，executor（gpt-6.1-sol/medium）负责独立harness，scout（gpt-6-luna/high）定向源码和冻结证据审查，无再派生。

失败待决策：本次VM无失败；首次overlay包名setup失败和两个纯fixture准备失败已修复并保留，未把旧失败改为通过。环境不足未验证：其他内核/平台、SSH/休眠、完整性能、生产权限部署。T132/T137、139/21、正式G01新0/旧0/100及P0/P1未放行保持。

### 降权客户端完成与建连时namespace缺口（2026-10-25）

已完成：[非特权客户端实际验收](validation/connector-unprivileged-client.md)的v2三阶段0，2流/13帧/38导出，两个65534实例原身份/路径/实际线路/33B终末完整；父进程真实proc屏障在target socket之前，cgroup写打开EACCES、pidfd_getfd及BPF command30/4B取link均EPERM，自有pidfd关闭一次。原0013核心/BPF及20FD不变。scout独立重验原始记录和摘要，无重大矛盾。名称来源/版本按ADR010同步spec/data-model/统计/路由设计，不写成用户新增澄清或开放旧PROCESS能力。

失败待决策：v1在core ready后的spawn前祖先权限断言失败，无客户端/flow，core TERM0；具体失败祖先值未在断言前保存，不能事后猜定。旧image/source/run保留；主代理在新v2显式归档root/work/out权限并先保存layout，cpio回归及新VM通过，已关闭该启动失败。原两项纯fixture适配错误保留，不计作VM失败。首次root文档检查误把git no-index返回1当作空白错误，检查本身失败；按无诊断输出及合法0/1返回重新通过，不涉及产品行为。

进行中：[建连时namespace守卫](../specs/001-hoopa-proxy-manager/contracts/connector-namespace-guard-v1.md)：源码确认非root/NNP不能单独排除后续user namespace创建，当前BPF未逐连接检查user/mount。主代理选择新增冻结scope map及strict handoff v3，在当前调用线程的LSM捕获前后及connect路径核验，保留旧112/296B和source v4 unverified。executor（gpt-6.1-sol/medium）实现相关最小接口/定向检查，scout（gpt-6-luna/high）独立字段/契约取证与冻结运行审查，不派生；主代理负责设计、真实VM、失败处置和最终验收。

环境不足未验证：新守卫实际verifier/namespace负例、生产launcher与普通用户部署、其他平台/内核、SSH/休眠及完整性能。动态namespace不变、任意特权对抗和完整身份未由本批证明。T132/T137未勾、139/21、正式G01新增0/旧0/100及P0/P1未放行保持。

### 建连时namespace守卫实际完成与字段能力接线（2026-10-25）

已完成：[0014守卫](validation/connector-namespace-guard.md)的3个独立VM运行、提取、严格校验均0；正常模式2流，user/mount负例各1基线，共4流/27帧/138导出。实际加载新对象、21FD接管与24B冻结scope读回成立。user客户端65534成功unshare，mount仅限自有root夹具；两者实际只变指定namespace，原native connect及initial followup均EPERM且零协议/应用写。capture_faults为0/0/1/2，Owner保持1/0/false；完整原prefix、33/33终末与两线路ready保持。scout独立重算138导出和3归档，无critical/high矛盾。

进行中：[source v5字段能力契约](../specs/001-hoopa-proxy-manager/contracts/source-connector-v5.md)已登记，实施独立handoff4、受限instance/program_name/program_path与未验证comm、严格manifest2和listener前需求校验。旧source v3/v4、其unknown/unverified和所有PROCESS拒绝保持；新契约本身不是能力验收。主代理负责能力语义、恢复边界和实际VM；executor（gpt-6.1-sol/medium）负责已确定实现与定向检查，scout（gpt-6-luna/high）只读取证与稳定材料复核，不派生。

失败待决策：本批3VM无失败。准备期错误负例fixture及元数据修正前镜像v1保留，最终使用独立v2；227检查不与旧221预备结果相加。公开记录生成器首次相对路径元数据处理失败已修复并保留，不影响原VM材料。环境不足未验证：生产launcher/普通用户部署、其他内核/平台、真实SSH/休眠及完整10分钟性能；v5尚待实现/实际验收。139/21、T132/T137未勾、正式G01新增0/旧0/100及P0/P1未放行保持。

### Source v5实际完成、T132受限验收与T137冻结（2026-10-25）

已完成：[新字段实测](validation/connector-field-profile.md)在0015独立候选通过；干净001–015树与审查树一致，38顶层/180含子项定向race、vet/build通过。实际preflight三种错误需求拒绝，1顶层/4含子项、0skip。新VM `vm-positive-xuab9n1d` 的VM/extract/check各0，2流13帧38导出；原PID/start86/479及91/525、296B exe/24B scope/21FD一致，instance/program_name/program_path available而comm unverified。两入口HTTP/SOCKS5，上游均Http，attempt ready11/31ms，flow12/32ms，两个33/33终末及SHUT_WR后EOF完整。

已完成裁决：[T132](validation/t132-restricted-acceptance.md)按固定kernel/对象、普通TCP、初始namespace/受控cgroup/可信管理员范围完成。T137追加SHA命名候选，绑定新实际结果及真实diff影响，旧hash矩阵不转贴。总计139/23，正式G01新0、旧0/100，P0/P1未放行。旧status的PROCESS能力仍false，与source5字段能力分开。

进行中：T136私有控制harness的持久意图、同核心接管、分阶段应用/失败恢复；继续后续正式矩阵。主代理负责语义裁决、干净构建、实际VM与最终验收；executor（gpt-6.1-sol/medium）负责确定接口实现、定向测试和公开证据整理，scout（gpt-6-luna/high）负责冻结diff/harness及原始运行独立核验，未派生。

失败待决策：本批实际VM无失败；实施期编译/fixture/路径、准备期PYTHONPATH和旧handoff断言等错误已在冻结前修复，原日志保留。选择性单active遗漏模型反例仍存在，固定源码与可信前提内采用条件性顺序证明，没有伪造实际注入通过。环境不足未验证：生产launcher/权限部署、其他内核/平台、真实SSH/休眠及完整10分钟性能；本次没有新v5崩溃VM，完整/pending/反复重开来自定向测试，旧v4崩溃证据按原范围引用。

### T136 控制原型与两种实际接管（2026-10-25）

已完成：[控制原型](validation/control-probe.md)的持久意图/claim、受保护 Unix IPC、同 ID/revision、明确 Recover 与 pidfd 身份停止路径；稳定 helper 定向 race 56 顶层/200 含子项、vet/build 通过，cmd 包仅编译。两个固定 guest 的 VM/extract/check 各 0：管理器 SIGKILL 与成功 spawn 后父记录前退出均接管原核心，离线既有流/新流继续；4 流27帧52导出、每场41/7 B终末与应用一致。scout独立重验52导出摘要及原始顺序，不重复运行。

进行中：T136 实际完整替换、失败后回滚、身份冲突拒绝与低 UID 目录保护；只完成这两场不勾 T136。后续 T127–T129 正式矩阵仍待运行，139/23、正式 G01 新0/原0/100、P0/P1 未放行保持。

失败待决策：首次 takeover 的夹具字面请求行断言造成 EOF，checker 为1，原始请求行未导出；hosts 解析原因仅为源码推断。新 v2 明确接受登记别名并记录原头，两个实际场景通过；旧失败、镜像和0/0终末保留。环境不足未验证：真实 SSH/注销/休眠、其他内核/平台、生产部署与完整性能。

主代理负责事务/恢复/身份决策、代码关键修正及实际 VM；executor（gpt-6.1-sol/medium）实现模块和独立准备，scout（gpt-6-luna/high）只读复核，无再派生。没有宿主 BPF/cgroup/核心操作，没有把假后端或 VM 退出0单独当作通过。

### T136 完整替换与身份拒绝通过、回滚失败保留（2026-10-25）

已完成：[新增三场实际记录](validation/control-replace.md)中，完整替换与身份冲突拒绝两场 VM/规范提取/严格 checker 均为0，共3流、21帧、66导出。完整替换从原11/11 B到新实例17/17 B，PID/start/generation/epoch改变；身份冲突只改claim.start_ticks，status/stop均503/UNKNOWN，原连接继续13/13 B，恢复精确原claim后显式Recover停止，单流终末24/24 B。低UID得到0700目录边界EACCES，不算实际SO_PEERCRED不匹配覆盖。两种接管与本批结果分开登记，不重复计数。

进行中：T136仍未勾选；准备独立绑定errno诊断，核验三个可信claim对应实例实际消失后，只在自有guest记录原端口bind及SO_REUSEADDR对照。诊断尚未实际完成，不能写成根因证实或回滚通过。主代理负责故障裁决、实际VM和最终验收，executor负责已定准备及文档同步，scout只读复核；没有再派生。

失败待决策：`vm-rollback-7y2qfj4a`的VM/提取为0、checker为1，目标与恢复旧bundle的新实例均通用受保护配置拒绝，HTTP503、operation degraded/rolling-back。预置端口冲突是场景条件，原运行没有bind errno或完整非监听TCP状态；具体原因仍是待验证假设。失败9帧单独保存，不计入成功矩阵；旧候选/镜像/raw及准备失败保留。机器记录（本地原件 `../experiments/mihomo-events/control-replace-validation.json`，不随Git上传）分别列出两成功与一失败。

环境不足未验证：真实SSH/注销/休眠、其他内核和平台、生产托管/权限部署及完整性能预算未由本批验证。T136未完成；总计139项、完成23项，正式G01新增0、旧0/100，P0/P1未放行保持；不改已有T137验收与候选记录。

### T136回滚修复与五场控制矩阵完成（2026-10-25）

已完成：[0016有限验收](validation/control-rebind.md)通过。固定内核诊断记录旧入口FIN_WAIT2/CLOSE_WAIT与bind98/98；新旧socket均复用的对照成功。独立0016仅改Reserve与回归测试，保留core CGO1，修正helper精确core pin。新guest两socket回归、五场VM/extract/check均0；9流、64活动来源帧+1失败目标终末、167导出。完整旧bundle回滚后fresh PID119/start740，新应用17/17 B走原节点；两个接管保持原核心身份/世代，身份拒绝后原流继续。root验收T136，139/24；SHA命名新候选已追加，T137旧记录未改。

进行中：补齐T127之前的source5进程规则消费者与新oracle。已有字段available不等于规则能力，旧host/legacy process入口无法验证source5。主代理负责方案、关键语义、实际VM和最终验收；executor（gpt-6.1-sol/medium）负责确定实现/定向检查/文档，scout（gpt-6-luna/high）负责独立稳定证据复核，无派生。

失败待决策：本批5个新runtime无失败；0015原503/degraded与9帧保持失败，独立诊断不计成功矩阵。CGO0未运行初稿、sandbox IPC测试失败、helper缺artifact父目录准备错误分别保留，修正后独立检查通过。没有把旧hash运行结果赋给新hash，也没有宣称仅new socket设置reuse就能立刻接管0015旧socket。

环境不足未验证：真实SSH/注销/休眠、其他内核/平台、生产部署、完整性能预算。正式G01新增0、原0/100；P0/P1仍未放行。只验收T136有限harness，不把此阶段当作整个项目完成。

### T140显式程序规则消费开始（2026-10-25）

已完成：T136稳定发布审查无剩余矛盾，216摘要引用/273链接核对通过，旧T137候选未改。进行中：按[新契约](../specs/001-hoopa-proxy-manager/contracts/connector-program-rules-v1.md)新增T140，先做纯配置和匹配再做新核心guest；T127正式登记在其后。总计140/24，新增任务只补已证消费缺口，不降低门槛。失败待决策：本阶段尚无运行；旧失败保留。环境不足未验证：真实非UTF8程序路径尚未运行，源码推断不能算三值端到端通过；原平台/部署/性能边界不变。

### T140有限程序规则验收完成（2026-10-25）

已完成：[0017有限验收](validation/connector-program-rules.md)覆盖14VM/28流/166帧/462导出/14完整epoch，VM/提取/checker全0；20流33/33字节、8流已admit后0/0拒绝，原始非法UTF8及AND两顺序符合有限契约。源码/初始化/旧入口定向race与vet通过，干净构建、新SHA候选及机器记录冻结。主代理验收T140，140/25；executor观察模型gpt-6.1-sol/medium，scout为gpt-6-luna/high，无再派生。

进行中：T127 source5正式oracle和connect前持久登记100槽位；本批校准不计正式分母。失败待决策：无剩余本批运行失败，重复route初始化修前失败及准备错误均保留，历史0/100不变。环境不足未验证：其他内核/平台、SSH/注销/休眠、降权部署和完整性能；旧控制helper不支持新配置。正式G01新增0，P0/P1未放行。

### T127 source5原连接oracle实现准备（2026-10-25）

已完成：T140最终稳定发布复核无重大矛盾。新[oracle契约](../specs/001-hoopa-proxy-manager/contracts/source5-formal-oracle-v1.md)固定五类20正例，客户端40项及guest登记45项纯/fake检查通过。进行中：独立账本/严格checker/镜像准备，先做10条同拓扑校准再正式登记100；尚无新增真实正式样本。主代理负责持久顺序与分母/验收，executor为gpt-6.1-sol/medium，scout为gpt-6-luna/high。失败待决策：当前无新运行，旧0/100保持；环境不足未验证：其他平台/真实SSH/休眠/生产权限部署仍未扩展。T127未勾，140/25、P0/P1未放行保持。

### T127首轮100正式槽位：保留60/100并修正检查器（2026-10-25）

已完成：固定v3镜像五类10条准备、65帧/120导出通过独立复核，随后一次性登记100正例。正式五场VM/提取成功，冻结checker接受普通/子孙/thread共60，fork/scm各20因合法bytes_checkpoint未被识别而失败，故首轮正式结果60/100。主代理确认自己误将周期事件种类写为checkpoint；核心/来源协议未变。

进行中：独立checkpoints lab修正两个种类名并增加真实失败帧的纯回归，再做新准备/登记。失败待决策：本次原因已明确，原40失败仍属于该100分母，旧run与历史0/100均保留，不重采旧槽位或把纯复评当通过。更早v2准备cgroup命名不符在加载前失败也保留；v3只改用既有白名单前缀。环境不足未验证：完整负例/边界矩阵、其他平台/真实SSH/生产权限部署仍未完成。T127未勾，140/25，P0/P1不放行。

### T127新正向批次100/100，继续边界验证（2026-10-25）

已完成：[新批次有限验收](validation/source5-formal-identity.md)100/100、5VM/612帧/210导出/5完整epoch，独立冻结复核无重大矛盾；公共只读入口真实race 1通过0跳过、Python纯检查11通过。进行中：PID复用、缺证据、权限及三值规则独立预登记矩阵，T127未勾，140/25。失败待决策：本批无剩余失败；旧60/100与0/100、准备失败均保留原义。环境不足未验证：生产降权、其他内核/平台、真实SSH/休眠；本批7检查点0/0不证明非零进度。P0/P1未放行。观察executor gpt-6.1-sol/medium、scout gpt-6-luna/high；主代理负责分母、证据与验收。

### T127 PID复用边界通过（2026-10-26）

已完成：独立准备2条后正式2/2，samePID30000的start469/499与cookie1/4097、源身份/flow/终末分开，13帧/25导出，独立稳定复核通过。进行中：缺凭据故障夹具（13纯fake检查通过，未运行）、权限guest准备及三值规则正式边界。失败待决策：此边界无运行失败；准备代码提取漏局部count已修，历史失败保持。环境不足未验证：其他平台/部署/压力/休眠原范围不变。T127未勾，140/25，正向100不变、P0/P1未放行。

### T127缺凭据准备失败与隔离修正（2026-10-26）

已完成：权限准备真实降权、五cap清零、sysctl2及有效MAP_CREATE256→EPERM，7导出，待独立复核后正式登记。进行中：缺凭据修正版准备及三值矩阵实现。失败待决策：原缺凭据准备0/2，基线33/33且实际map重开/读取成功，但guest truth变量覆盖核心登记导致KeyError generation；删除未执行，不能当目标拒绝通过。主代理在独立fixed lab仅改truth局部变量名，原失败不动；13纯fake及2静态作用域回归通过，仍须新真实准备。环境不足未验证：生产权限部署/平台/SSH/休眠边界保持。T127未勾，140/25，P0/P1未放行。

### T127权限正式单例通过（2026-10-26）

已完成：准备及同镜像正式各1个权限负例；正式PID70/start253、五cap清零、UID/GID65534、NNP1、sysctl2未变、有效MAP_CREATE→EPERM，7导出独立复核，已接受窄BPF管理权限范围。进行中：缺凭据正式2槽和三值14槽夹具；修正版缺凭据准备2/2及独立复核通过。失败待决策：原缺凭据shadow0/2仍保留，不改判。环境不足未验证：source/data-plane权限与G07不由本单例推导；其他平台/部署限制保持。T127未勾，140/25，P0/P1未放行。

### T127缺凭据正式边界通过（2026-10-26）

已完成：fixed lab正式2/2，7帧/28导出，正常基线33/33；缺凭据cookie4097在STOP后删除/ENOENT、connect112不变、恢复拒绝且来源bytes/final为null，独立复核和机器归档完成。进行中：三值14槽夹具/准备/正式矩阵与整体门槛。失败待决策：原shadow准备0/2保留，修复后本批无失败。环境不足未验证：受控特权删除不代表普通用户权限或生产部署；无代理请求的负例计划域名不当实际访问。T127未勾，140/25，正向100分母不变、P0/P1未放行。


### T127受限验收与T012评审完成，继续T128（2026-10-26）

已完成：三值14准备另列，正式14/14、7VM/75帧/168导出，独立复核后与100正向/PID复用2/缺凭据2/权限1组成限定验收。公共只读总入口核对708文件，真实Go race1通过0失败0跳过；T127/T012勾选，140/27。历史报告taskfalse不覆盖，独立acceptance保存当前裁决。观察executor为gpt-6.1-sol/medium、scout为gpt-6-luna/high，root负责关键语义和验收，无子代理再派生。

进行中：T128准备固定0017/source5的真实collector辅助程序与恢复矩阵；旧T133绑定001–005，不继承到新候选。失败待决策：本次正式三值无失败；gate准备曾错误要求独立permission报告绑定core，v2已修正、旧准备记录保留；旧0/100、60/100及缺凭据0/2仍保留。环境不足未验证：真实文件系统满盘尚待guest实测，生产权限/SSH/注销/休眠/其他平台/产品预算仍未验证。T128/T129、P0/P1未放行。


### T128传输与满盘准备（2026-10-26）

已完成：T127/T012最终稳定发布复核无重大矛盾。新独立source5 collector helper使用现有严格receiver/journal，race9顶层/19含子测试、vet和CGO0构建通过；这些是文件/httptest检查，未开真实HTTP listener，不能算来源传输通过。进行中：按[三场景准备契约](../specs/001-hoopa-proxy-manager/contracts/source5-recovery-transport-v1.md)制作离线/503/丢ACK/hold的固定0017 guest；另用隔离2MiB tmpfs和strace小进程校准实际ENOSPC。失败待决策：本阶段暂无实际VM结果；held屏障的仅TCP等待方案在实现前因opened需代理目标解析被否决，改为上游CONNECT成功响应前等待已持久held，避免死锁。环境不足未验证：真实collector/core恢复与完整T128故障矩阵、实际满盘和ptrace仍待执行；生产SQL/GC及既有平台限制保持。140/27，P0/P1未放行。


### T128真实ENOSPC环境校准通过（2026-10-26）

已完成：隔离无core/BPF VM的2MiB tmpfs校准1/1，实际PID73/FD3 write8192→ENOSPC，释放自有filler后8192成功/fsync、12288原文正确；VM/extract/check0、5导出独立复核。准备11纯反例与两个hostguard拒绝分开记录。进行中：当前0017三个真实传输准备case与后续核心故障矩阵。失败待决策：此校准无实际失败。环境不足未验证：已有core attach、source ENOSPC恢复、断电/未插桩时序仍未验证；不从tmpfs校准推出T128通过。140/27、P0/P1不变。

### T128三个真实传输准备场景通过（2026-10-26）

已完成：[新独立准备6/6](validation/source5-recovery-preparation.md)，3VM/39来源帧/99导出、36截止事件全部逐字接收；42次delivery/39唯一receipt/41次accepted，原文重投不重复记账。三次marker实际收到但不提升为退出交付保证。首轮4/6的held变量覆盖失败保留，root仅在新lab改client_release局部名及两处使用；旧源回归复现、新30项准备与新6/6通过，独立复核无未决阻断。

进行中：source5两core/两collector真实SIGKILL/reopen夹具；固定源码恢复overlay的合成/拒绝race10项通过，实际入口1skip，等待新VM证据后验证独占全扫描和稳定gap。失败待决策：当前传输新批次无剩余失败，首轮4/6及旧0/100等不改判；预审曾遗漏sender两秒超时而误判held死锁，原报告与撤销补充同时保留。环境不足未验证：实际已有core ptrace/source满盘/完整故障矩阵及生产部署/跨平台仍待验；宿主/tmp满盘曾使沙箱启动失败，仅迁移Hoopa可再生Go缓存到private并保留原路径链接后恢复。观察executor gpt-6.1-sol/medium、scout gpt-6-luna/high，root负责故障裁决和最终验收。140/27，T128正式0样本，P0/P1未放行。

### T128注入边界通过与两项实际准备失败保留（2026-10-26）

已完成：定向边界检查（本地原件 `../experiments/mihomo-events/source5-fault-boundaries-preparation-validation.json`，不随Git上传）source5合成/注入race5项及vet通过，固定源码buffer/actual n/lazy证明race15项通过，1199源码条目保持；独立scout核对分层范围。进行中：新attach-fixed与restart-fixed准备、重新登记及实际验收。失败待决策：首次attach0/2，实际7线程附加和TERM回收已观察，但客户端旧guest marker导致preconnect前拒绝；首次restart0/2，guest完成两实例故障重开，但host checker把str传给Path读取接口而失败。root已决定各自新lab最小修正并补实际调用回归，旧镜像/注册/结果保留且不重评分。环境不足未验证：完整已有核心附加/脱离、真实source ENOSPC、成功登记的完整重开及内部gap输入检查仍待完成；平台/部署/性能原限制不变。主代理承担故障裁决与实际VM，executor为gpt-6.1-sol/medium、scout为gpt-6-luna/high，未派生子代理。140/27，T128正式0样本，P0/P1未放行。

### T128真实重开与核心附加校准通过（2026-10-26）

已完成：新重开2/2（本地原件 `../experiments/mihomo-events/source5-restart-preparation-validation.json`，不随Git上传）13帧/44导出，旧12/12检查点与六原receipt保留，新33/33闭合；新VM输入绑定后的固定源码私有副本race1通过0跳过，三次重开gap ID/fact稳定，原证据与1199源码条目不变。新附加校准2/2（本地原件 `../experiments/mihomo-events/source5-core-attach-preparation-validation.json`，不随Git上传）13帧/36导出，实际writerFD7的12次write/fsync、TERM脱离后8线程无tracer且原核心running、自有挂载卸载均通过。两项独立scout只读复核无剩余阻断。

进行中：真正来源ENOSPC两尾分支和200000字节逻辑准入预留的独立guest准备；正常退出、冲突与实际buffer场景随后执行。失败待决策：本次两个新登记无失败，原attach0/2和restart0/2不改判；restart无写诊断还修复每实例实际argv及实际recovery字段接线，旧checker缺字段get默认为null已改为严格索引。环境不足未验证：实际source满盘/准入背压和剩余完整矩阵、平台/部署/性能仍未验证。root负责语义、登记和实际VM；executor gpt-6.1-sol/medium负责两处互不重叠lab实现，scout gpt-6-luna/high做稳定证据复核，无再派生。140/27，T128正式0样本，P0/P1未放行。

### T128满盘原件恢复检查准备（2026-10-26）

已完成：新增固定源码测试overlay，合成完整/partial原件的三次独占副本重开及五类输入拒绝共race9项通过，vet通过；真实输入入口明确skip，1199源码条目保持。完整原件恢复保留12/12检查点、稳定缺口事实和null终末；partial原件三次拒绝且不截断、不生成新epoch。符号链接反例保持两文件数量，单独验证最终文件链接拒绝，避免仅由额外文件数量触发失败。

进行中：200000字节配额三槽lab已离线构建、冻结，补实际guest/client guard正向接线审查后再登记；真实source ENOSPC lab继续准备。失败待决策：本次合成检查无失败，尚无新的VM判定；旧准备失败全部保留。环境不足未验证：真实source满盘输入、配额三槽转发/终末及其余矩阵仍待实跑，不能以合成通过替代。root负责恢复语义及overlay，两个executor为gpt-6.1-sol/medium；140/27，T128正式0样本，P0/P1未放行。

### T128逻辑配额实跑通过，满盘准备入口修正（2026-10-26）

已完成：200000字节逻辑预留3/3（本地原件 `../experiments/mihomo-events/source5-quota-preparation-validation.json`，不随Git上传），单VM/31导出/14帧及唯一receipt逐字核对。第一连接保持12/12时第二连接无source flow、final=null，第一终末仍准确12/12，释放后第三33/33通过。第二实际收到SOCKS成功回复再EOF的边界已公开；固定source marker实际收到但不提升退出交付保证。新增actual client guard三槽正向及反例13/13，独立scout复核稳定原件和发布内容。

进行中：真实source ENOSPC新fixed准备、HTTP响应旁8字节前缀与collector同ID异字节冲突的独立lab。失败待决策：root在原ENOSPC登记前发现enospc_checks新复制函数缺Path import，独立静态预审也未发现；原包尚未注册、runtime0，保留冻结及原预审，以新裁决要求修复、旧失败复现和实际整函数输入反例，禁止直接boot旧包。环境不足未验证：真实source ENOSPC及剩余矩阵、正常活动流退出、平台/部署/性能仍待验。root承担关键复核/裁决/VM，executor gpt-6.1-sol/medium、scout gpt-6-luna/high，无子代理派生。140/27，T128正式0样本，P0/P1未放行。

### T128前缀及冲突实跑，满盘镜像依赖补齐（2026-10-26）

已完成：buffer/conflict新登记2/2、30导出、两flow/segment/client33up41down而target33/33；原13帧与journal逐字匹配，冲突409无receipt及原文204沿用receipt1，三份控制前后journal相同。root已形成私有发布草稿，独立runtime复核待进行，不能写作已完成独立接受。ENOSPC Path最小修复27项与独立delta预审通过。

进行中：ENOSPC新的bundle-fixed镜像闭包检查、正常活动12B连接TERM准备。失败待决策：ENOSPC fixed第一次VM登记0/2，源故障未执行，guest import缺evidence_checks/oracle_checks导致PID1退出且无archive；root决定只在新目录补原模块打包与实际cpio提取work隔离import回归，旧run/注册/镜像保留。环境不足未验证：实际来源ENOSPC、正常TERM及T128完整正式矩阵、部署/平台/性能仍待验。root负责决定和实际VM，两executor gpt-6.1-sol/medium分写镜像修正与TERM，scout gpt-6-luna/high负责稳定预审；无再派生。140/27，T128正式0样本，P0/P1未放行。

### 2026-10-26 buffer独立接受与新满盘运行

已完成：buffer/conflict准备（本地原件 `../experiments/mihomo-events/source5-buffer-preparation-validation.json`，不随Git上传）独立运行审查通过，2/2、30导出、原文重投与冲突控制均核对；明确两个实际client预读字段为空，不宣称实际预读分支已命中。新ENOSPC镜像实际cpio隔离导入回归通过，独立预审后另登记VM观察2/2。

进行中：新满盘运行的独立证据复核、固定核心对实际来源原件私有副本的恢复检查、活动连接TERM准备。失败待决策：旧ENOSPC镜像导入失败0/2保留，未重评分。环境不足未验证：实际满盘恢复尚待根校验接受，正常TERM及完整T128正式矩阵、部署/平台/性能仍待验。root负责关键恢复语义和实际执行，executor gpt-6.1-sol/medium准备TERM，scout gpt-6-luna/high独立复核，无再派生。140/27，正式T128样本0，P0/P1未放行。

### 2026-10-26 实际source满盘与partial尾接受

已完成：新满盘准备2/2（本地原件 `../experiments/mihomo-events/source5-core-enospc-preparation-validation.json`，不随Git上传）41导出与独立复核，core77/FD7实际write917→57、860→ENOSPC，old真实17/17而可信12/12/null；新SOCKS协议后发送33再reset，无node:b或source flow。原件8192B/57B partial保持，实际输入私有副本固定代码race1通过0跳过，三次ErrSource且无新epoch/修复，1199源码条目不变。

进行中：活动TERM候选14准备测试、cpio隔离导入及独立预审通过，root已另登记1槽并启动VM。失败待决策：原镜像导入0/2不改判；overlay首次只读后检查有排序差异，零内容差异确认后保存结果，实际测试未重跑。环境不足未验证：真实ENOSPC的complete尾成功重开未观察、自动修复未实现，正常TERM实际结果和正式T128矩阵仍待验；平台/部署/性能不扩大。root承担恢复判断及执行，executor gpt-6.1-sol/medium、scout gpt-6-luna/high，140/27、正式T128样本0、P0/P1未放行。

### 2026-10-26 活动TERM准备接受、正式夹具适配

已完成：活动TERM准备1/1（本地原件 `../experiments/mihomo-events/source5-term-preparation-validation.json`，不随Git上传）28导出独立复核；原连接12/12 CP+receipt→TERM/wait→仅recv授权，EOF/零后写；closed12/12+complete unfinished0，八帧/receipt原文相等，marker本次收到。

进行中：正式8VM/16原应用槽的六family夹具只做phase/命名空间/路径适配，独立目录、新镜像，全部注册须在第一正式VM前冻结。失败待决策：本次TERM无失败；历史失败不改判，未生成的便捷SHA256SUMS不当已完成证据，权威hash保存在freeze/image。环境不足未验证：TERM另一gap分支、实际满盘complete尾重开未观察；自动修复、SQL/GC、生产/跨平台/性能仍依各自后续任务。root负责判断和VM，executor gpt-6.1-sol/medium适配、scout gpt-6-luna/high独立取证；140/27、正式T128样本0、P0/P1未放行。

### 2026-10-26 T128只读总入口准备

已完成：新增recovery_test.go/source5_recovery_gate.py，仅校验root已接受的8case/16唯一slot、6正式注册、原run/镜像/完整导出和两个实际恢复overlay引用。独立审查后补canonical export归属、formal目录、镜像副本hash、root decision绑定及严格整数。私有合成图1正向/16语义变异/4哈希JSON变异均符合预期，读取前后输入不变；Go race入口全缺env明确1skip，两个部分配置各1fail且0skip，均为预期守卫证据。

进行中：六family正式适配新镜像及实际cpio闭包准备，尚无正式registration或VM。失败待决策：总gate首次合成baseline因fixture给绝对report路径被拒绝，修正fixture为约定相对路径，原日志/原fixture保留；未将其计为实际验收。环境不足未验证：正式bundle尚不存在，因此实际总入口仍未验证；T128/T013和后续任务不勾选，140/27，P0/P1不变。root负责总gate实现和关键验收，executor gpt-6.1-sol/medium做formal适配，scout gpt-6-luna/high做独立覆盖/代码审查。

### 2026-10-26 T128正式矩阵完整登记

已完成：六family正式夹具稳定冻结并经独立delta审查，385条hash核对、107既有检查/42formal检查/36help/六新镜像及实际cpio闭包；全部六份formal注册先于首VM完成，共8case/16唯一原应用槽，root matrix SHA256 9782a54605316e1c8cdd32766bbc3f0bb4e7e1bbe01cf20f82f439d15125e52d。

进行中：root每case执行前重核全部注册、保存不可覆盖admission，再逐场串行VM；首场offline-drop-closed启动，最终通过数待实际结果。失败待决策：适配早期日志文件被复用覆盖，保留ledger观察摘录并明确不完整；最终独立日志已冻结，未把早期检查写为全过。诊断调用分支/closure脚本的准备错误亦保留，不改变runtime断言。环境不足未验证：完整正式结果和两个formal实际输入恢复overlay、实际总gate仍未完成；T128/T013未勾，140/27，P0/P1未放行。

### 2026-10-26 T128正式恢复与T013评审完成

已完成：[固定0017正式恢复验收](validation/source5-formal-recovery.md)8VM/16预登记槽全部通过，273导出独立复核；九份来源93完整帧、14实际source flow中12closed/2无final，两个准入拒绝槽没有伪造零流量。正式ENOSPC实际write912→92、820→ENOSPC，8192B原件/8100B确认前缀/92B partial；原连接实际17/17而可信12/12/null。两个正式输入私有副本overlay各race1通过0失败0跳过，三次稳定gap或ErrSource拒绝、1199源码条目及输入不变；最终只读总入口race1通过0失败0跳过。T128及消费同run的T013勾选，140项完成29项，P0/P1未放行。

进行中：T129质量/线路准备。首先新增可扣住HTTP成功响应最后CRLF的受控上游夹具，再绑定source5原连接验证真实ready；旧profile2测试不直接升级为正式证据。独立映射发现0017受保护controller明确拒绝live selector PUT，旧T134脚本不能直接用于新候选；DNS、取消/超时/lazy完整核心场景也需明确执行前提，主代理正按固定能力裁决，不悄悄放宽控制入口。

失败待决策：本次正式16槽无失败；前两场driver退出2是组中其余槽尚待执行，最终transport6/6。首root结果的动态summary引用以原字节快照保留，后续改用独立snapshot；overlay登记诊断首次遗漏帧后32B摘要，在执行前修正并保留记录；源码树排序preflight失败保留，逐路径核对无实际修改。旧准备4/6、0/2、原身份0/100及60/100等不改判。T129第一轮socketpair准备因sandbox sendall EPERM失败，保留原日志并按相同无网络夹具申请执行环境复验。

环境不足未验证：真实ENOSPC complete尾成功重开、TERM另一gap及buffer非空实际预读未观察，相关合成覆盖单列；SQL事务/GC、生产/平台/性能按原后续任务，未被T128通过替代。root负责全矩阵登记、VM、原计数核验及恢复裁决；executor gpt-6.1-sol/medium执行overlay与新夹具，scout gpt-6-luna/high只读复核稳定原件。主代理纠正了独立报告将副本writer恢复误称纯decoder的表述后冻结接受，无子代理再派生。

### 2026-10-26 T129协议时序与回环DNS准备

已完成：T128发布后的稳定文档/任务/机器记录独立复核一致。新的HTTP上游helper合成AF_UNIX最终9/9、独立静态审查通过；首次sandbox EPERM保留，相同测试在授权环境复验。root新增回环UDP53单查询DNS helper，纯报文/脚本socket9/9通过、独立静态审查通过；没有真实UDP或代理结果。

进行中：[T129准备](validation/source5-quality-preparation.md)先做两原应用经HTTP A/B完整响应屏障：source两份同flow无setup原件、120ms hold、实际ready/ref/链及n核对。executor已完成适用旧28项和新8项检查、7入口help及离线镜像，实际cpio闭包/稳定预审后再登记VM。DNS将固定guest resolv.conf及唯一.test名，在真实DIRECT路径取证。

失败待决策：源码映射证实旧live selector PUT与当前protected guard冲突；保留409保护，正式场景尚未冻结，完整替换的新旧流/BPF共存须先校准，不能静默替代同核心热切换。DNS初次“无配置dns键即无法真实查询”的推断已被更细调用链修正：Mihomo SystemResolver仍可走guest回环DNS。新HTTP quality合成fixture首次带旧source_port导致失败，已改为新单场两槽派生输入，保留原日志，不放松oracle。

环境不足未验证：当前T129真实HTTP屏障、回环DNS、两代BPF共存、实际cancel/lazy正式覆盖均未取得通过记录；T129未勾，140/29、P0/P1不变。主代理负责场景/恢复门槛与DNS实现，executor gpt-6.1-sol/medium实现HTTP lab，scout gpt-6-luna/high做独立映射及冻结材料复核，无再派生。

### 2026-10-26 T129 HTTP完整响应真实准备通过

已完成：HTTP-ready准备2/2（本地原件 `../experiments/mihomo-events/source5-http-ready-preparation-validation.json`，不随Git上传），单VM/39导出/14帧/四份来源快照独立复核。两原connector分别95/start498及101/start540，HTTP A/B实际扣留124253294ns/125372428ns、ready耗时137ms/126ms；四个源时间等式、同flow/attempt/ref实际链和双向33B整数终末均核对。第二flow的两快照间只有零流量checkpoint，没有提前setup；严格recovery的recording_status仍unverified，不将可见前缀说成fsync。冻结hash、登记、一次claim、原run/日志和root受限接受记录齐全。

进行中：DIRECT+回环慢DNS新lab。独立映射发现TTL0下规则解析后DIRECT还会按Host再次lookup，root决定每slot两次真实查询、只延迟首答并保留次答，而非假设缓存；两个独立慢域/HTTP与SOCKS inbound、DIRECT实际ready和flow/attempt差额待实证。完整替换按既定独立进程/入口/端口先做双BPF共存及旧流持续校准，不将同入口跨cgroup冲突绕过或当作已支持。

失败待决策：新HTTP实际2槽无失败；历史EPERM及合成fixture旧端口输入失败保留。旧同核心selector PUT仍受保护拒绝，T129正式场景尚未冻结。环境不足未验证：真实DNS、SOCKS上游最后字节、取消/失败/超时/lazy和完整替换/多跳仍待验，正式T129样本0、T129不勾，140/29、P0/P1不变。root负责实际VM、时序/归属/计数裁决及发布；executor gpt-6.1-sol/medium实现DNS lab，scout gpt-6-luna/high独立静态/原件复核，无再派生。

### 2026-10-26 T129 SOCKS准备与固定能力再核对

已完成：root SOCKS5 helper AF_UNIX7/7及独立静态复核；新单场两槽SOCKS上游lab的适用旧28项/新9项/7help/实际cpio7模块隔离导入通过，已冻结待独立预审，没有注册或VM结果。HTTP公开记录的稳定复核通过；公开formal_t129_samples=0来自准备范围判断，旧host-check实际字段formal_t128_samples=0并未被倒写或误引。

进行中：executor完成DIRECT双查询慢DNS镜像与闭包/冻结；root并行准备SOCKS真实完整BND边界；独立不同入口双捕获器/旧连接持续的完整替换方案已记录。失败待决策：scout最初依据普通BasicOption误判protected允许tfo，root从profile shape/object精确白名单发现前置拒绝；原报告保存attempt1，修订报告保留TFO只在普通adapter可达的区别。客户端关闭不取消Background派生上游ctx，实际cancelled/lazy触发不能凭单测虚构；正式T129范围尚待能力裁决。环境不足未验证：DNS与SOCKS新VM、双root link并存/独立detach及多跳真实行为仍待执行；正式T129样本0、140/29、P0/P1不变。root实现SOCKS lab及关键可达性复核，executor gpt-6.1-sol/medium负责DNS，scout gpt-6-luna/high独立审查，无再派生。

### 2026-10-26 T129 SOCKS完整响应实际准备通过

已完成：SOCKS-ready准备2/2（本地原件 `../experiments/mihomo-events/source5-socks-ready-preparation-validation.json`，不随Git上传），39导出/14完整source帧/14逐字receipt独立复核，56个公开引用hash核对。原connector93/start472及99/start521，node:a/b建立耗时140/128ms，实际扣留124771441/126302999ns；两份无setup快照后2/9/1回复写入完成，四个源时间等式和双向33B终末/segment守恒通过。BND脚本值及source可见性边界保持，未提升为业务RTT或fsync。

进行中：DIRECT DNS修正版v2已完成16检查/7help及实际cpio完整文件、7模块隔离导入和10项非Python依赖闭包，待独立预审/登记VM；完整替换独立入口4槽lab开始实施。失败待决策：root在原DNS冻结包登记前发现缺少guest /etc/hosts，runtime0，原freeze/image保留，新目录只修正builder与实际依赖验证，14 runtime/oracle/driver逐字不变。环境不足未验证：真实DNS、双generation BPF并存/旧流持续/独立detach与失败超时多跳仍待验；取消/lazy为固定能力边界待决策，不归因于环境。正式T129样本0、140/29、P0/P1不变。root负责时序裁决/VM/发布，executor gpt-6.1-sol/medium实现隔离lab，scout gpt-6-luna/high独立复核，无再派生。

### 2026-10-26 T129 DIRECT慢DNS实际准备通过

已完成：DIRECT DNS准备2/2（本地原件 `../experiments/mihomo-events/source5-direct-dns-preparation-validation.json`，不随Git上传），40导出/13原帧与13逐字receipt经独立复核，58个公开引用hash核对。两个原connector92/start529及98/start570，各两次真实A/IN查询/TTL0/45B回复；首答扣留133856703/132458735ns，source pre-attempt136075072/133911192ns，flow ready137/134ms与独立attempt1/0ms区分正确。两份无setup快照、Direct实际chain/ref、33/33终末/segment守恒及资源回收通过。原v1缺hosts保持登记前runtime0，不重评分。

进行中：独立不同入口的完整替换4槽lab；root新增七种上游拒绝/坏响应/停答helper，10顶层pure/AF_UNIX检查通过，待独立静态审后接真实核心。失败待决策：本次DNS新登记2槽无失败；当前取消/lazy与live selector为固定保护能力限制，尚不把未可达场景记为passed。环境不足未验证：真实拒绝/超时、双generation并存/独立detach与多跳仍待实际验证。正式T129样本0、T129不勾、140/29、P0/P1不变；主代理负责关键时间等式与实际执行/发布，executor gpt-6.1-sol/medium、scout gpt-6-luna/high按独立任务工作，无再派生。

### 2026-10-26 T129失败场景预审整改与source5语义检查

已完成：root新增固定source5质量overlay，2顶层/含子测试11项race通过、0fail/skip，前后1199条候选源码不变；取消/未知/已证ready后错误分类及partial段并发CP守恒使用明确合成身份/Conn/错误/n输入，不是真实网络取消或BPF。独立范围复核待进行。完整替换lab的18主检查+3补充、7help与实际镜像闭包已完成，稳定冻结待独立预审，实际样本0。

进行中：failure-fixed新lab最小修正及完整check_delivery回归。失败待决策：旧failure冻结包的时间戳字符串字典序比较由root发现；scout又确认仍调用旧两流33/33 cutoff，与新七流0/0不兼容。root决定旧包登记前停止/runtime0，保留freeze/image，修复数字比较及精确七流零终末，并新增完整调用链正反例，不只测试下层receipt函数。旧failure helper重跑的AF_UNIX EPERM/后续2skip保留；未改变helper的root原10项实际AF_UNIX通过证据继续单列引用。环境不足未验证：实际7场失败/超时、完整替换与多跳仍待VM；取消/lazy仍属固定profile能力边界。正式T129样本0，140/29、P0/P1未放行。root负责关键语义、两个executor按gpt-6.1-sol/medium分写独立目录、scout gpt-6-luna/high串行复核，最多两个子任务活动，无再派生。

### 2026-10-26 T129 完整替换与故障准备实际运行

- 已完成：四槽完整替换新登记1VM、4/4通过并独立核对50导出；旧A流12B checkpoint及receipt之后drain，新B/A流在旧连接存活时完成，旧A续至33B，旧core退出后新B再次成功。两个owner各10 map/6 program/5 link且对象ID按类不交叉；旧来源原文不变，旧/新source及journal分别9/19帧。见source5-replacement-preparation-validation.json（本地原件 `../experiments/mihomo-events/source5-replacement-preparation-validation.json`，不随Git上传）。七槽故障修正版新登记1VM、7/7通过：五类拒绝/畸形各10 attempt，两类stall各1 timeout，共52次真实accept；七条明确closed 0/0，不用client已写33B代替转发字节。99帧及99原收据、38导出已复核，见source5-upstream-failure-preparation-validation.json（本地原件 `../experiments/mihomo-events/source5-upstream-failure-preparation-validation.json`，不随Git上传）。source5受控输入2顶层/11含子测试race通过、0fail/skip已独立复核并另行接受（本地原件 `../experiments/mihomo-events/source5-quality-injected-validation.json`，不随Git上传）。
- 进行中：独立nested/retry四槽准备（HTTP外层经SOCKS内层的两个原应用，加HTTP/SOCKS首次失败后成功各一）；只在适用check_delivery和新增质量完整语义函数验证合成正反例，原身份/恢复外部工具仍须实际VM验收。新增固定HTTP adapter net.Pipe显式取消检查1顶层/5含子测试race通过，原源码不变，独立复核进行中。T129正式场景分母及层级接受仍待范围裁决，140项/29完成、T129未勾选、formal0、P0/P1 false。
- 失败待决策：原故障验收器两个预审缺陷已有独立修正版通过，旧包和拒绝复现不改。nested离线整机合成探针先因connector原ABI不一致被strictinspect拒绝，修后strictinspect通过，sourceinspect仍拒绝合成manifest；保留探针失败，主代理停止扩展合成恢复模型，不将它记作runtime失败或完整离线通过。当前没有把未触发分支补成正样本：七槽中52/52 source attempt都有实际accept，允许的最后deadline无accept分支未出现。
- 环境不足未验证：其他kernel/平台、宿主真实休眠/掉电、SQL账本及产品界面仍未验证。固定profile的客户端断开不取消dial、tfo被拒属于已查明的能力边界，不归因环境，也不以注入取消/unknown替代。替换只证明不同端口独立core及本次旧detach隔离，不宣称同端口无缝切换或在线selector写入。

实现由executor配置gpt-6.1-sol/medium执行，独立证据复核由scout配置gpt-6-luna/high执行；主代理负责注册、VM执行、关键语义裁决和公开证据接受。子代理没有派生；两次VM均已退出回收，无后台VM遗留。

### 2026-10-26 T129 六类准备收束与正式副本准备

- 已完成：nested/retry新登记1VM4槽、实际顶层checker4/4通过，35导出/28source/28原journal payload独立复核并受限接受。两个B→A多跳源建立254/255ms，inner实际relay154/92B包含外层HTTP握手，outer/source/app33/33；HTTP403、SOCKS坏REP后成功各一，4flow/6attempt，第一次失败保留、第二次独占终末段。实际来源恢复complete/validated保留recording_status=unverified，旧synthetic探针失败独立保留。见source5-nested-retry-preparation-validation.json（本地原件 `../experiments/mihomo-events/source5-nested-retry-preparation-validation.json`，不随Git上传）。固定HTTP adapter/Pipe取消1顶层/5含子测试race已独立复核和受限接受（本地原件 `../experiments/mihomo-events/source5-http-adapter-boundary-validation.json`，不随Git上传）。
- 进行中：六准备HTTP2、SOCKS2、DNS2、replacement4、failure7、nested4已各自通过，但全部仍formal0。[ADR011](adr/011-source5-quality-acceptance.md)及[正式质量契约](../specs/001-hoopa-proxy-manager/contracts/source5-formal-quality-v1.md)明确网络/注入/适配器三个证据层、六family/mode和21逻辑槽；独立审查发现并同步了ADR过时的nested未完成状态。新formal六家庭副本、镜像、guard和实际cpio闭包仍在准备，未创建任何正式registration或VM。计数裁决另列vm_started，executed只计实际导出中已登记原客户端开始connect的唯一槽，不能用QEMU存在回填整组；passed仍靠完整host checker。
- 失败待决策：本次实际nested没有新增失败；先前合成raw ABI/sourceinspect和fake测试失败原件保留，修正范围及停止扩展合成恢复模型的决定不变。没有以报告措辞修正覆盖runtime。原GOST0/100、首正式身份60/100及其他历史失败保持。
- 环境不足未验证：正式21槽尚未执行是阶段状态，不是环境失败；客户端断开不取消dial、真实lazy/TFO及同端口切换仍属明确能力边界。其他平台/生产权限/SSH休眠/性能预算/SQL/CLI/TUI按后续任务验收。140项/29完成、T129/T014未勾选，T017/T018及P0/P1没有放行。


### 2026-10-26 T129 正式21槽统一登记与执行

- 已完成：六新formal-v2镜像、631条冻结引用经独立预审；54 guard/count检查、36完整aggregate虚拟场景、48help及实际cpio闭包均通过。root补充外部总controller，独立审查提出总matrix运行端需再次核库存/hash后，已加入seal、精确六family/case/21slot绑定及每场前全输入复核。正式总matrix SHA `7b9e2e7c84160b9ad3703e95147b54536f892fc7e7de61f80f64b1074d8ebc28` 已在首VM前登记，21 registered、0 attempted/executed/passed为登记时快照；只通过root controller运行。
- 进行中：六场按HTTP、SOCKS、DNS、完整替换、上游失败、nested/retry顺序实际执行；原件独立复核与受限接受另作记录。只读公共gate的全图正例/14语义负例/2缺失篡改负例、默认race明确1skip、partial-env预期1fail及稳定代码审查已完成；这些不计实际formal样本。
- 失败待决策：正式运行结果待实际完成；准备期guard测试假设及同步脚本错误日志保留。gate首次默认缓存只读失败保留，显式使用原离线缓存后通过；审查记录已纠正实际Python为3.8.17，无需重跑。未修改历史失败分母。
- 环境不足未验证：正式实际结果当前进行中，不记为环境失败；生产/SSH休眠/其他平台/SQL/界面仍按后续任务。140项29完成，T129/T014暂未勾，P0/P1未放行。executor配置gpt-6.1-sol/medium负责受限实现及入口检查，scout配置gpt-6-luna/high独立静态/原件复核，主代理负责总矩阵/VM及最终裁决。


### 2026-10-26 T129/T014正式质量线路验收完成

- 已完成：六个新formal VM/21原应用槽，registered/attempted/executed/passed均21，failed/pending及unobserved均0；241规范导出、七份epoch194source原帧及194接收journal/receipt经独立复核。HTTP/SOCKS的attempt与flow时长分别记载；慢DNS归规则flow前段，DIRECT attempt整数0ms有真实ready证明。failure52实际attempt/accept、七closed0/0与client33B分别保留；nested4flow/6attempt及两段hold、inner154/92B握手解释成立；replacement旧12CP/receipt、drain、新两流、旧33、TERM后新第四流通过。六VM均退出回收。[受限接受](../experiments/mihomo-events/source5-quality-acceptance.json)及真实只读入口（本地原件 `../experiments/mihomo-events/source5-quality-gate-validation.json`，不随Git上传）已完成，Python核验552文件，Go race1通过0失败0跳过，新runtime样本0。T129与T014勾选，140项完成31项。
- 进行中：T015生命周期/G07最小访问保护前置。scout已映射0016控制helper与新0017之间的pin/校验路径差异。root决定先新建私有Go overlay，仅重新固定core SHA到0017，保持旧identity-only配置及ValidateDeclaredIdentity，明确继续拒绝program-rules opt-in；原0016 helper/源码/VM不变。executor正在做受影响Control检查、offline新helper与实际安全host拒绝入口，尚未构建镜像或新登记VM。程序规则控制路径不由这一步获得支持，T016另按其任务实施。
- 失败待决策：本次正式21槽无失败；所有旧0/100、60/100及准备/合成失败保留。独立报告曾将HTTP/SOCKS attempt duration误称flow，root要求按原setup核对并在最终review冻结前纠正，原运行未改。gate首次cache环境错误原日志保留，真实最终入口通过使用固定离线环境；不重跑已有实际槽。
- 环境不足未验证：T015真实SSH/注销/系统挂起尚无证据，现有guest缺相应工具/执行流程；SIGSTOP/clock变动不能替代睡眠。生产权限、其他平台、SQL/GC、质量窗口/分位数与CLI/TUI仍后续验收。T017/T018及P0/P1未放行。主代理负责语义/全部实际VM/最终接受，executor配置gpt-6.1-sol/medium与scout配置gpt-6-luna/high按单写入职责实现和复核，无再派生。


### 2026-10-26 T015 当前核心控制准备与真实 guest S3 环境

- 已完成：新0017 legacy identity-only控制helper离线构建及独立复核，root另行接受为校准输入；仅core hash常量私有overlay，原源码与旧制品未改。成功检查57顶层/206含子测试，实际AF_UNIX首轮环境EPERM原件保留，授权同输入复验通过；program-rules opt-in继续拒绝。独立guest S3环境探针实际1VM，QMP SUSPEND/状态/WAKEUP、内核deep S3进入与恢复及相同boot ID一致；QEMU退出并回收，零代理样本。
- 进行中：新0017 takeover/spawn-window两场校准镜像/闭包及独立预审；旧0016原件只能作历史函数正例，换core hash的合成manifest被真实sourceinspect拒绝已保留，root决定由新实际VM提供完整正向结果。另准备guest内真实SSH会话，使用本地固定工具、合成账号/测试密钥和loopback，不修改宿主SSH或会话。
- 失败待决策：S3返回后出现S5及PID1退出panic，不能宣称干净guest关机；新副本中调查退出路径，原运行不改判。host QMP挂起/唤醒间隔505870989ns，而guest BOOTTIME/MONOTONIC仅增加约223ms，未证明补时。其不妨碍已观察到的guest S3前提，但不得扩展为代理连续性、宿主睡眠或调度补偿通过。
- 环境不足未验证：真实代理跨S3、SSH/注销、完整采集器/G07保护矩阵仍待执行；T015未勾，140项31完成，P0/P1未放行。root接受记录分别位于private/t015-control0017-helper/root-preparation-acceptance.json与private/t015-suspend-environment-vm/root-runtime-acceptance.json。executor配置gpt-6.1-sol/medium负责控制lab，scout配置gpt-6-luna/high独立复核；主代理负责语义与实际VM，无子代理再派生。


### 2026-10-26 T015 当前核心两场接管校准通过，SSH首场失败保留

- 已完成：0017有限生命周期校准（本地原件 `../experiments/mihomo-events/control-0017-lifecycle-calibration-validation.json`，不随Git上传）先统一登记2VM/4flow；takeover及spawn-window各2/2通过并经独立原件复核。两场各26导出、14/13来源帧，原长流41B和离线新流7B每方向值精确；原管理器分别-9/86退出，恢复后同core PID/start/generation/epoch，明确stop后原core被回收。只证明本次identity-only控制范围，源恢复recording_status仍unverified/writer_end null，未冒称接收或SQL提交。
- 进行中：SSH环境v2受限回收/诊断修正；原真实S3环境使用完全相同guest/image的新2.5秒挂起观测及QMP尾事件采集待预审，以查清短间隔时钟差异。后续需要把真实SSH及S3与核心转发、来源和采集消费关联，不能以环境成功替代。
- 失败待决策：SSH首场vm-ssh-84cab352中，真实交互login shell logout23及shell消失成功；杀client后同remote PID85/start392收到SIGHUP1，但等待/proc消失超时，整场不通过。超时点未记录stat，不能追认zombie；新副本补精确owned PID/start的状态/收养后waitpid，旧原件/失败不改。发行版UsePAM提示与lastlog/utmp缺失保留，范围仅SSH+shell，不是PAM/logind注销。S3之后关机panic仍单独未解，不拿无S3的SSH正常poweroff覆盖。
- 环境不足未验证：完整T015采集器、SSH/注销/代理跨休眠、F14及G07保护仍未完成；新2.5秒probe仅环境观察无代理。140项31完成，T015/T016/T017/T018及P0/P1保持未放行。主代理负责登记/实际VM/关键裁决；executor配置gpt-6.1-sol/medium负责新包实现，scout配置gpt-6-luna/high只读独立复核，没有子代理派生。


### 2026-10-26 T015 SSH环境v2通过，真实SSH/S3核心关联准备

- 已完成：SSH环境v2新VM vm-ssh-f965qirm七份导出独立复核；真实login shell logout23与shell消失、客户端-9之后同remote PID85/start384收到HUP1，R/ppid1→Z/ppid1，精确waitpid返回85/status33024/exit129。root受限接受为SSH/login shell及owned回收前提，零代理样本，不反推v1缺失的末态。更长S3环境观测原guest/image不变，host2.507390753s、guest boot-minus-mono2.142868945s；独立复核保留时钟精度未知和QMP guest-reset/内核PID1panic，不宣称干净关机。
- 进行中：executor的新SSH-origin manager/core夹具，以及root的manager离线S3/core夹具；均固定0017/helper53eef，不预设需要Setsid。S3-core包已冻结待独立预审，计划一VM两flow41/7；原takeover语义只插入S3，完整原checker/Scenario.run AST保留，新增QMP/kernel/同core/来源prefix谓词、三方法及六合成负例、实际801cpio/109ELF闭包。guest完成核心与进程回收并导出后由host QMP明确结束VM，避免把已知平台S5缺陷混作Hoopa shutdown证据；外部peer deadline/调度补跑仍不在此场。
- 失败待决策：旧SSH首场仍一场未通过，旧S3之后关机异常仍未解。S3-core准备首次marker缩进、AST Constant构造差异、直接host导入缺credentials导致的guard未达错误日志/源副本保留；最终真实image导入到达PID1 guard及无许可runner拒绝通过，未把import失败当安全guard通过。当前两新核心关联VM都尚未登记/运行。
- 环境不足未验证：真实SSH+core、S3+core、独立采集器降级/恢复、F14真实控制PID复用/缺claim/外来端口与G07各入口/journal访问矩阵仍待验证。140项31完成，T015/P0/P1仍未放行。主代理和executor(gpt-6.1-sol/medium)分写S3/SSH目录，scout(gpt-6-luna/high)独立取证与预审，无再派生。


### 2026-10-26 T015 真实 SSH 核心接管通过，S3 终端解析修订

- 已完成：真实SSH核心接管校准（本地原件 `../experiments/mihomo-events/control-0017-ssh-calibration-validation.json`，不随Git上传）一VM/两原应用槽，完整host checker2/2及独立29导出/14source帧复核通过，root另行受限接受。client84退出-9与远端manager88/start460的SIGHUP退出-1分别取证；core97/start509/同epoch在断连后持续转发并由新manager接管，41/7每方向字节及准确原connector保留。来源recording_status=unverified、writer_end null，无receiver/SQL声明，QEMU退出回收。
- 进行中：S3-core v3仅修host终端协议解析与独立PM证据检查，guest/cpio保持原字节。六方法、完整LF/CRLF/hash/尾部与S3负例、五help、两实际host拒绝、801cpio/109ELF闭包已通过，冻结待独立预审。独立receiver退出/恢复方案已裁决：不改控制helper白名单；固定0017直接启动，receiver1 hold-opened的durable receipt1先于kill，receiver2以同journal和新evidence重开，验证同事件/原receipt重投与离线流量补收，仍不冒称SQL。executor准备新夹具。
- 失败待决策：S3-core v1因动态QMP依赖仅记录hash而未运行前核验被预审拒绝，登记/VM0；v2补依赖核验后实际一VM，因host仅识别LF而真实virtconsole为CRLF，post-resume export等待超时，完整checker因缺runtime.suspend失败。30导出与实际S3前后记录保留，原run不改判；混合boot串口中BEFORE还被printk打断。v3将完整wire/base64/SHA及QMP/kernel/snapshot共同核验作为新尝试前提，不补造v2字段或改分母。旧SSH首场和S3后S5/panic亦保留。v3首次build参数不全被argparse拒绝，未执行build/VM，错误另记。
- 环境不足未验证：v3实际休眠核心结果、核心跨用户注销、独立collector、F14及G07完整矩阵仍未通过；S3外部peer超时/宿主睡眠/时钟精度/调度至多补一次、其他平台与生产supervisor仍另验。140项31完成，T015/T016/T017/T018及P0/P1未放行。root负责语义、串行实际VM与接受；executor配置gpt-6.1-sol/medium、scout配置gpt-6-luna/high分别实现和独立复核，无子代理再派生。


### 2026-10-26 T015 核心跨 guest S3 新尝试受限通过

- 已完成：S3 v3核心校准（本地原件 `../experiments/mihomo-events/control-0017-suspend-calibration-validation.json`，不随Git上传）新登记一VM两槽，完整checker2/2及独立30导出/14source帧复核通过。manager退出后实际deep S3/恢复，同core PID89/start410/同epoch保持，old41/new7每方向精确；新manager接管、显式停止并回收原core。挂起前后原来源各5帧/7235B前缀保留，后续追加闭合。QMP间隔2506756393ns；guest mono/boot增量228776514/2324312825ns，时钟精度仍unverified。host见完整CRLF/hash归档后QMP quit，13.47s退出回收；没有guest OS关机声明。
- 进行中：独立receiver SIGKILL/同journal重开与真实重投去重的夹具实现；F14真实缺claim/PID复用/外部端口输入映射；G07独立IPC peerUID/controller/HTTP和SOCKS入口、source和receiver权限矩阵方案。SSH公开记录55引用已独立核验通过，S3新公开记录61引用待最后文档复核，均不勾T015。
- 失败待决策：S3 v2一VM两槽仍失败，保留CRLF导出等待超时、缺runtime.suspend及interleaved BEFORE问题；v1预审拒绝且VM0。v3新输入/新登记/新运行不改变旧结果。旧SSH首场、S3后S5/PID1panic、G01历史0/100与60/100均保持。
- 环境不足未验证：核心跨登录shell logout/PAM/logind、真实宿主睡眠、外部peer超时、订阅调度补跑、独立采集消费、F14/G07其余项仍未完整验证。没有用source可见前缀冒充fsync/receiverACK/SQL。140项31完成，T015/T016/T017/T018及P0/P1未放行；实际VM串行，已启动者均已回收。executor配置gpt-6.1-sol/medium实施collector，scout配置gpt-6-luna/high独立取证，root负责语义/运行/接受。


### 2026-10-26 T015 F14发现controller凭据与连接归属缺口

- 已完成：S3公开记录61引用及root acceptance绑定独立核验，实际SSH/S3受限结论入档。F14只读映射明确缺claim的503 UNKNOWN/degraded及精确PID/start mismatch拒绝路径；既有PID复用样例属于connector，不能当core复用已证。映射见private/t015-f14-preparation/input-map.md/json。
- 进行中：collector独立夹具已通过首轮19方法与cpio闭包，继续稳定冻结；root制定helper-only连接归属修复设计并交独立事实复核，executor在collector交付后实施。采用私有Go overlay保持旧源码/核心/制品和历史证据不变，先无凭据建立TCP连接，核验原pidfd/启动实例及该同连接反向服务端socket的core FD inode，再允许HTTP Transport发送Bearer。
- 失败待决策：原helper只验证进程后向配置controller地址发凭据，缺socket归属绑定；受保护core预先reserve端口虽可拒绝占用，但不足以排除helper startup/exit窗口误连foreign。此为静态发现的安全缺口，尚未执行实际泄露测试；不从stock hub端口失败日志推出guarded core必持续存活。不允许用原helper进行预设安全成功的foreign场景，不以LISTEN端口预检查替代同连接证明。修复/新helper实际校准尚未通过，旧SSH/S3正常场景不获此能力。
- 环境不足未验证：完整F14/G07、核心跨登录注销、collector真实恢复和生产平台仍未完成；原failed记录及140项31完成/T015未勾/P0P1 false不变。新设计是从Linux proc/pidfd和Go调用路径推导的待验证方案，不是内核独占owner证书；特权ptrace或恶意core主动转交FD不在该受限可信核心模型内。


### 2026-10-26 T015 采集器副本校验修订与控制连接修复

- 已完成：collector v1独立预审发现复制后未在claim/QEMU前核对实际字节，原包冻结且登记/VM0。新v2仅补run-local cpio、image、全部harness及canonical LAB decoder的长度/SHA校验，并迁移登记目录；6项正负复制测试（失败时无claim/Popen）及原19项检查通过，0skip。重建cpio与v1逐字节相同，806项/109ELF完整闭包通过；33外部引用和795文件冻结已只读核验。guest-only client误用--help由guard拒绝，已单独记录，不算适用help。
- 进行中：v2独立预启动审查，之后才登记单VM两应用槽；helper私有overlay实施同一已建立连接的core FD归属检查及请求取消边界，真实TCP验收尚未完成。G07后续计划已明确消费新helper，不沿用旧helper的安全假设。
- 失败待决策：旧collector v1启动前绑定缺口已用新版本修订，旧件不改；controller端口凭据缺口尚待修复实测。旧SSH/S3/G01失败保持，不能从新源码或纯测试改判实际运行。
- 环境不足未验证：采集器真实重投/恢复、F14/G07完整矩阵、核心跨登录注销及生产平台未完成；T015/T016/T017/T018保持未勾，140项31完成，P0/P1 false。根代理负责新runner修订/冻结和实际VM，executor配置gpt-6.1-sol/medium负责helper，scout配置gpt-6-luna/high负责独立稳定输入复核。


### 2026-10-26 T015 独立采集器退出与同journal恢复受限通过

- 已完成：collector v2实际记录（本地原件 `../experiments/mihomo-events/control-0017-collector-calibration-validation.json`，不随Git上传）一VM两槽，完整checker及独立33导出/原帧复核2/2通过。固定0017 core76/start451不变，receiver86/start526 held receipt1后SIGKILL/reap；离线期间old累计24、新7关闭，来源增长而journal不变；receiver98/start638同目录/文件inode重开，首个实际HTTP重投沿用原事件/receipt1，14source/14唯一收据、13cutoff先于core停止；old最终41/new7每方向精确，complete marker实际收到。QEMU3496549 exit0、11.302s回收且proc absent。主代理已单独接受，公开记录63引用待最终文档复核。
- 进行中：controller owner helper纯/fake9主项24含子项与vet已通过，真实TCP及全部受影响race/离线构建继续；后续最小实际校准裁决为takeover、spawn-window、foreign-controller三模式。真实foreign接受连接数须如实报告，零连接不能冒充ownership分支执行。
- 失败待决策：collector v1静态拒绝且登记/VM0保留；v2新输入/登记/运行不改旧件。旧helper controller凭据归属缺口尚待修复验证，旧SSH/S3/G01失败均不重评。
- 环境不足未验证：source recording_status仍unverified、writer_end null，无source fsync/SQL/生产consumer cursor保证；F01/核心跨正常logout/F14/G07完整矩阵仍未通过。140项31完成、T015/T016/T017/T018未勾、P0/P1 false。root实际串行VM及接受，scout配置gpt-6-luna/high独立取证，executor配置gpt-6.1-sol/medium实现helper；未新增并行VM或宿主BPF/cgroup操作。


### 2026-10-26 T015 controller同连接归属helper准备通过

- 已完成：私有overlay新helper ad4a8069…c92e13f/11408390B、73项冻结通过scout独立复核和root正确性审查；原pidfd/boot/PID/start、反向ESTABLISHED tuple→inode→core FD交集先于同连接Bearer发送；原请求取消约束transport分离的dial ctx。最终race71主项/238含子项0失败0跳过，fake9/27、vet/离线构建通过，host launch实际拒绝。三个owned-child真实TCP例分别164B认证、foreign0B、listener替换后新listener0连接。公开文本补丁五文件在临时原源码fuzz=0应用与overlay逐字相同；Go原文件/core/旧helper/历史freeze未动。collector公开63引用和文档已独立核验通过。
- 进行中：executor准备新helper三模式固定核心校准，root已给具体边界与新helper准备接受；无新VM登记或core启动。源补丁/机器准备记录见experiments/mihomo-events/control-0017-controller-owner.patch及control-0017-controller-owner-validation.json。
- 失败待决策：旧helper缺口及历史失败保留，不把新准备通过当成旧件修复；foreign core退出status仅helper内部Wait可见，已裁决以准确claim/intent、真实guard拒绝及原PID消失/manager仍活证明无残存，显式wait status unobserved，无伪造guest wait。
- 环境不足未验证：新helper固定核心运行、核心logout/F01、真实core PID复用/缺claim、G07完整权限矩阵和SQL/生产托管仍未完成；140项31完成，T015/T016/T017/T018及P0/P1未放行。executor配置gpt-6.1-sol/medium实现与测试，scout配置gpt-6-luna/high稳定独审，root负责安全/恢复取舍及最终接受；新实际VM仍串行。


### 2026-10-26 T015 新helper固定核心三模式真实校准通过

- 已完成：三模式实际记录（本地原件 `../experiments/mihomo-events/control-0017-controller-owner-calibration-validation.json`，不随Git上传）绑定新helper ad4a、固定core5693与image-prepared-v5；一次登记三VM/四应用槽/一控制负向探针，串行实际运行后4/4与1/1通过，75份导出及28帧原source独立核验。takeover core90/start452、spawn core89/start488，旧/新流各41/7每方向；foreign实际PID74/start433/socket inode469接受1连接并recv0/EOF，无signal，core88/start492guard拒绝，manager存活，精确rolled_back/active0stopped。QEMU3673124/3677211/3682749均exit0/reap/procabsent；root-runtime-acceptance独立保存。公开摘要129引用已生成，待稳定文档复核。
- 进行中：G07两guest有限授权/未授权矩阵实现；新helper准备前置已满足。root补充F14缺claim后Recover原stop、真实core PID一次复用方案，并保留F01独立frontend退出/正常交互shell logout方案，后续分别准备和预审。G07固定源码HTTP missing Basic407、wrong Basic403已修正文档；认证前identity.Admit不冒充认证后opened/route/ready。
- 失败待决策：旧helper缺同连接归属证明的问题已在新helper有限校准闭合，但旧helper制品及已有结果不改写。三模式v1–v4准备镜像均无登记/VM，superseded历史保留；旧SSH/S3/G01失败不重评。foreign核心精确wait status仍unobserved，实际claim/start/proc/pidfd及原guard日志支持已观察的退出边界。
- 环境不足未验证：source reader仍recording_status unverified/writer_end null；无source fsync/SQL、生产托管或跨平台声明。正常logout/F01、真实core PID复用/缺claim、G07完整矩阵尚未实测；新helper不自动继承旧SSH/S3。140项31完成，T015/T016/T017/T018及P0/P1未放行。root负责关键裁决与实际串行运行，executor配置gpt-6.1-sol/medium负责实现，scout配置gpt-6-luna/high负责独立取证。


### 2026-10-26 T015 访问保护、PID复用与前端退出准备

- 已完成：新helper三模式公开记录129引用及当前G04/ADR/日志经独立最终核验一致；4/4应用槽、1/1控制负向、三VM、75导出/28source，仍无T015完成声明。F14设计复核明确缺claim时status/stop503 UNKNOWN、原stop degraded，恢复同ID/expected_revision1后方可提交stopped revision2；core仍为活manager子进程时不伪造PID1 wait。root准备独立foreign stdin/status子进程，4项host-safe检查通过并冻结、独审接受，实际PID复用0；最小readonly frontend子进程7项检查通过并冻结、独审接受，实际guest/IPC0。
- 进行中：G07与F14离线夹具由两个executor在独立目录实施，公共输入只读，注册和VM仍由root统一串行。F14必须一次分配旧core PID并核对不同start/exe，缺claim按原操作恢复；G07必须区分文件权限拒绝与SO_PEERCRED拒绝，绑定真实数据协议结果。root细化F01/正常logout集成：真实登录shell前台启动frontend、owned FIFO发quit、shell记录真实exit0；流量前后独立验证，再logout23及可选明确manager handoff，不混淆三次触发。
- 失败待决策：目前新增两个子进程准备无失败待裁决；旧实验失败和superseded输入均保留。foreign信号ledger仅声明已装HUP/INT/TERM/USR1/USR2，加同实例存活和实际响应，不能声明观测所有signal。前端socket timeout不是HTTP总时限，集成parent/VM须另设总deadline。
- 环境不足未验证：两个准备子进程尚未实际guest集成，不能算F01、F14或G07通过；source fsync/SQL、生产托管、跨平台和新helper SSH/S3未补验。任务仍140项31完成，T015/T016/T017/T018与P0/P1未放行。executor角色配置gpt-6.1-sol/medium，scout角色配置gpt-6-luna/high；root负责恢复/身份语义、运行和最终验收，无并行VM或宿主BPF/cgroup更改。


### 2026-10-26 T015 实际PID复用与缺claim恢复受限通过

- 已完成：F14新两场（本地原件 `../experiments/mihomo-events/control-0017-f14-calibration-validation.json`，不随Git上传）固定新helper/core，串行2VM、3/3应用槽及2/2控制负向探针通过；52份原始导出及21帧来源经独立复核。缺claim场景core90/start467在拒绝窗口继续41/7B，恢复同claim和原f14-stop后提交stopped revision2；PID复用场景原core91/start486实际回收，一次分配foreign91/start685，status/stop拒绝且active/原凭据不变，foreign持续响应且限定信号账本为空。两QEMU均exit0/procabsent，主代理分别接受行为证据，原result未改。
- 进行中：G07已实际串行两VM，完整checker通过7/7应用尝试（3允许流、4拒绝）、11/11管理探针及14/14文件探针；74份导出/20来源帧等待独立原件复核和单独接受。真实frontend quit/login logout新夹具继续构建；新helper SSH/S3另有明确最小回归裁决，最终只读T015入口准备已记录，尚未实现或放行。
- 失败待决策：F14 v1在登记/VM0时被拒绝，动态proc stat不能当固定身份；v2只修稳定身份与独立原active留档，旧输入保持。预审JSON freeze路径笔误以独立澄清补正确只读校验，原报告保留。运行独审发现两F14私有原归档含已结束guest的IPC token，root记录证据导出卫生缺陷；公开只保留摘要，不宣称导出保护通过。后续新包排除token，G07单独验证，不删改旧证据或重评分。
- 环境不足未验证：本批真实guest行为已观察，不记环境失败；source reader仍unverified/writer_end null，无source fsync/SQL/生产或跨平台承诺。缺claim核心数值wait仍unobserved，PID复用的PID1 wait0另记。140项31完成，T015/T016/T017/T018及P0/P1未放行。executor配置gpt-6.1-sol/medium实施，scout配置gpt-6-luna/high独审，root负责身份/恢复/凭据导出裁决及所有实际VM。


### 2026-10-26 T015 当前候选最小G07矩阵受限通过

- 已完成：G07实际记录（本地原件 `../experiments/mihomo-events/control-0017-g07-calibration-validation.json`，不随Git上传）两VM、7/7应用尝试（3实际转发、4拒绝）、11/11管理探针及14/14文件探针，经完整checker、74导出独立核验和root分别接受。管理core87/start447，合法HTTP/SOCKS各7B、四拒绝零source/accept；实际UID65534原权限EACCES和临时0711/0602下正确token仍401分别成立，原权限恢复。文件core74/start404与receiver停后，12次实际未授权操作均EACCES且成员/inode/mode/bytes/hash不变，两root只读重开通过。七source/七receipt正常投递不冒充receiver恢复；两归档均无IPC token。公开111引用待最终文档复核。
- 进行中：frontend quit/真实login logout包已冻结，独立预审进行中；executor继续固定新helper的SSH断开/S3最小回归准备。root记录最终只读入口明确11类guest场景及独立分母，未来场景不提前计入实际完成。
- 失败待决策：本批两VM完整检查无失败；F14私有token导出卫生限制与旧准备/运行失败保留。G07覆盖自己的token排除，不抹去F14旧原件缺陷或为生产通用认证背书。
- 环境不足未验证：source fsync/SQL/生产托管/跨平台仍按后续任务；frontend/正常logout及新helper SSH/S3尚未实测。140项31完成，T015/T016/T017/T018及P0/P1保持未放行。root完成核心正确性和原件接受；executor gpt-6.1-sol/medium、scout gpt-6-luna/high分别承担实现和独立复核。


### 2026-10-26 T015 最小前端普通退出与正常登录shell注销通过

- 已完成：logout实际记录（本地原件 `../experiments/mihomo-events/control-0017-logout-calibration-validation.json`，不随Git上传）一VM两槽2/2、46导出/13来源帧经独立复核和root接受。frontend111/start691普通quit0，shell87/start489正常logout、SSH exit23与shell absent分别记录；core98/start550及原世代保持，旧11→24→41、新7每方向完整。remote89/start506跨logout及流量实际live，之后单独原pidfd TERM/PID1 wait0，再本地接管和core显式stop/reap0。QEMU3943006 exit0/procabsent13.8468s。token明文无导出、前后metadata/hash相同；公开67引用待最终核验。G07公开111引用和当前文档已独立核验无冲突。
- 进行中：新helper SSH回归包已冻结（22方法0fail/skip，845冻/318external、833cpio/121ELF），独立预审中；S3包完成实际闭包，准备最终冻结。最终只读T015 gate尚未实现/实际运行，不能用本次有限退出测试勾整项。
- 失败待决策：本次VM与完整checker无失败；准备NameError、输入映射拒绝、superseded镜像及全部历史失败保留。后台manager未因logout退出如实记录；独立TERM不改写为注销信号，无管理器离线流量推论。
- 环境不足未验证：最小实验前端不代表产品CLI/TUI，真实shell注销不代表PAM/logind/宿主注销；source fsync/SQL/生产及其他平台仍未验证。140项31完成，T015/T016/T017/T018及P0/P1未放行。root负责串行VM及接受，executor gpt-6.1-sol/medium实现，scout gpt-6-luna/high独立复核。


### 2026-10-26 T015 新helper SSH/S3实际回归完成，汇总入口进行中

- 已完成：SSH新回归（本地原件 `../experiments/mihomo-events/control-0017-ssh-newhelper-calibration-validation.json`，不随Git上传）及S3新回归（本地原件 `../experiments/mihomo-events/control-0017-suspend-newhelper-calibration-validation.json`，不随Git上传）各一VM两槽2/2，经完整checker、原件独审及root接受。SSH remote86/start426实际HUP wait1/exit-1，core95/start468持续转发并接管，29导出/14帧；S3 core88/start433跨实际deep S3保持，前后五帧7233B同hash，30导出/14帧，hostQMP quit收尾。两场QEMU3981628/3985747均exit0/procabsent，旧41/新7每方向，token省略+metadata；公开51/52引用待文档最终复核。logout公开67引用已独审一致。
- 进行中：最终[source5生命周期证据束](../experiments/mihomo-events/source5-lifecycle-acceptance.json)绑定11已接受实际case、22应用尝试/18转发流/14控制探针/14文件探针、339导出/124帧。root有限运行矩阵接受与具体单场接受分开保存；executor实施只读Python/Go入口及关键反例，真实入口和稳定代码审查完成前不勾T015。该入口不运行VM或增加样本。
- 失败待决策：本批两真实VM无失败；准备help/closure标签错误及旧所有失败保留，SSH预审沿用v2字段标签笔误另存正确v1命令澄清。root范围核查确认最初三个helper校准也导出guest token，连同F14共五原归档的卫生限制在root-export-hygiene-scope.json记录；已结束guest，公开无明文，不改原件、不授予导出保护通过。其他新包的排除证据独立成立。
- 环境不足未验证：S3 host间隔2508252900ns与guest mono/boot210000163/2094699974ns分记，时钟精度/宿主睡眠/外部peer/PAM-logind/SQL/生产托管/跨平台仍未证明。140项31完成，T015/T016/T017/T018及P0/P1仍未放行。root负责所有实际运行/最终矩阵，executor gpt-6.1-sol/medium实现入口，scout gpt-6-luna/high独审原件与稳定文件。


### 2026-10-26 T015受限完成，T016多入口前提准备

- 已完成：T015最终只读验收（本地原件 `../experiments/mihomo-events/source5-lifecycle-gate-validation.json`，不随Git上传）绑定11个已独立接受VM、22应用尝试（18转发/4正确拒绝）、14控制/14文件探针、339导出/124source帧。最终Python核验628文件，Go race实际1通过0失败0跳过、10.741s，稳定代码和输入摘要未变；4方法/44合成负例通过。root-task-completion单独接受T015，任务140项完成32项；G04和当前候选G07最小保护受限通过。默认无env的skip及部分env预期失败不算运行通过。
- 进行中：T016独立证据盘点发现固定helper与core身份接管均限制单入口；已决定先准备同guest双份现有BPF对象/Owner的最小挂接探针，再决定多入口接管适配。private/t016-multi-owner-preflight已离线冻结，8纯/fake方法、Python3.8/host拒绝、779项镜像与107 ELF闭包通过；独立预审中，未登记/claim/VM。两端口、独立generation/FD/map/program/link，维持原NULL attach opts，不修改BPF ABI；0app/0source正式样本。完整路由、provider/pin、持久边界和多入口回滚仍须实际实现与验收。
- 失败待决策：首个实际Go gate失败于旧owner公共摘要的内嵌host格式，后续collector独审散文citation又触发原通用机器ref假设；全部失败/中间诊断保留。根层限定兼容原格式、保留完整机器引用核验，修后实际通过，未修改原VM结果。凭据扫描新增异名/嵌入原样token拒绝；五份旧private guest token归档的卫生限制仍在，无待裁决的T015实现失败，旧0/100等历史结果不重评。
- 环境不足未验证：双Owner真实同时挂接仍待隔离运行；FD关闭不代表所有内核对象销毁。source fsync/SQL/生产托管、宿主睡眠/PAM-logind、其他平台和产品CLI/TUI仍未由本次验收证明。T016/T017/T018与P0/P1未放行。root负责关键语义、实际执行和最终裁决；executor配置gpt-6.1-sol/medium承担实现与离线检查，scout配置gpt-6-luna/high承担独立稳定复核；本轮gate未新增VM、宿主BPF或cgroup变更。

### 2026-10-26 T016双Owner内核前提通过，多入口候选实施

- 已完成：双Owner实际前提（本地原件 `../experiments/mihomo-events/source5-multi-owner-prerequisite-validation.json`，不随Git上传）一隔离guest经canonical检查、原件独审及root单独接受。端口18993/18995、generation16001/16002对象同时独立，第二挂接后双重核验，关闭第一后第二不变；每份21个自有FD恰一次清理并EBADF，空source leaf已移除。QEMU16924 exit0、5.655s、process_absent；0应用/0source样本。T015最终公开记录37直接引用、6文档固定摘要亦独审一致。
- 进行中：[G05记录](validation/g05-routing.md)明确剩余矩阵。root裁决版本化入口集合、全解析后FD转移、typed对象别名先只读拒绝、失败清理、同source epoch不可变入口选择和逐入口诊断；executor实施新私有核心及另一个独立bootstrap，旧core/helper/BPF/已接受原件保持不变。两条实现完成后先做直接核心两应用校准，再接控制helper及完整路由/回滚/冲突。
- 失败待决策：首次QEMU在沙箱缺/dev/kvm而初始化失败，原result/claim保留；宿主只读检查确认KVM设备后，另登记且相同guest/code重试成功。该失败不算双挂接失败或正式样本，现无待裁决的双挂接问题。旧0/100、凭据导出限制及其他历史失败保留。
- 环境不足未验证：新核心、bootstrap及多应用流量尚未验收；FD清理不证明所有内核对象销毁，未扩展到生产/cross-platform/SQL。T016/T017/T018与P0/P1保持未放行，140项完成32项。实际使用executor角色配置gpt-6.1-sol/medium实施，scout角色配置gpt-6-luna/high独审，root完成关键语义裁决、串行VM与最终接受；未操作宿主BPF/cgroup。

### 2026-10-26 T016多入口离线实施接受，双应用夹具与候选编译并行

- 已完成：新core固定5e934b与bootstrap固定582f10经root关键语义审阅、稳定制品独审后离线接受；核心1068冻结成员一致。四Owner定向race9顶层/37含子项通过0失败0跳过，原组件540通过/4 opt-in跳过及根包60通过按各自范围保留。bootstrap22纯/fake检查通过，搬迁自包含与依赖先验hash已验证；v1准备失败和旧运行失败均保留。
- 进行中：双应用、双命名入口HTTP/SOCKS四槽夹具完成初步离线构建，正冻结最终镜像；独立预审和实际运行尚待执行。另一个executor实施无I/O候选编译器，显式provider/node/snapshot绑定、跨源pin保持、必需选择缺失拒绝、单成员手选组与原序规则；不会把纯编译或单成员组当成订阅刷新/自动选优验收。
- 失败待决策：无新增待决策的稳定实现失败；既有源码编译尝试失败、bootstrap v1加载问题及沙箱KVM初始化失败均保留且分别有后续修订，不改写原记录。
- 环境不足未验证：新core实际VM/应用/source样本仍0，完整F08–F13/F29–F33/C01及新helper尚未通过。任务140项完成32项，T016/T017/T018及P0/P1未放行。executor按角色配置gpt-6.1-sol/medium实施，scout按角色配置gpt-6-luna/high独立整理证据，root负责语义、差异验收与后续串行VM。

### 2026-10-26 T016双应用首场执行，纯候选编译与只读解释完成

- 已完成：新核心首场VM实际运行于冻结image-prepared-v4，QEMU260726正常exit0/reap/procabsent、12.594s；guest记录core76正常退出、客户端86/89/92/95四槽完成，41导出已安全提取。固定严格reader解码25source帧，四连接各33/33B、实际HTTP-ready13/11/32/31ms、完整epoch关闭；尚未完成整体验收。纯编译器及Explain稳定差异经root与scout分别审查，最终同包race15主/115子通过0失败0跳过、vet通过，原compiler三文件保持其v2摘要。
- 进行中：派生恢复checker与受保护同字节副本准备，用原reader重新完成剩余来源恢复断言；不重跑VM，不修改原证据或冻结输入。随后做原件独审、单独接受及多入口负向/控制helper回归。
- 失败待决策：原canonical首次exit1保留；独立诊断确认提取器落盘0644触发reader的0600保护，解析前拒绝。root已裁决只修主机验证材料的权限，保持原文件与失败，未放宽reader。纯编译器非法UTF8被Marshal替换的缺陷已由两个失败子测试复现并修正，合法Unicode及U+FFFD仍精确保留；v1制品/日志保留。
- 环境不足未验证：该场完整canonical及独立原件接受尚待完成，新helper和F08–F13/F29–F33/C01完整矩阵未通过；不宣称生产、SQL、自动组选优或跨平台支持。140项完成32项，T016/T017/T018/P0/P1保持未放行。executor按gpt-6.1-sol/medium配置实施，scout按gpt-6-luna/high配置独审，root完成关键语义与唯一实际VM运行。

### 2026-10-26 T016双应用直接校准接受，多入口管理适配推进

- 已完成：直接校准记录（本地原件 `../experiments/mihomo-events/source5-dual-entry-direct-validation.json`，不随Git上传）一隔离VM四应用槽4/4通过，41导出、25来源帧、42个Owner FD及四个原connector独立核对，完整epoch和四条33/33B终末值一致。派生checker实际exit0，独立原件复核后root受限接受，78引用逐一核验；原71文件字节/摘要/权限保持。来源ready13/11/32/31ms与源时间戳差一致，但本场没有独立等待oracle，不能替代新hash完整质量门槛。
- 进行中：已批准三项native后续准备：实际typed对象误复用拒绝、第二入口bind失败不提前开放第一入口、新核心legacy单入口。另一个executor按固定决策适配控制后端，显式assets v1/v2和核心hash配对、严格逐入口status/env，保留原8KiB bundle、事务/锁/日志/恢复语义；都只做准备，root独占实际运行。
- 失败待决策：原canonical的0644/0600失败保留；独立同字节保护副本和只改路径/输出的checker已通过，未放宽reader或原84断言。原件审查把夹具slot token标为random_per_run的表述由root独立旁注更正：它们是固定公开测试关联标记和payload，不是代理认证凭据；原审查不覆盖。无新增待裁决的直接校准失败，旧0/100及其他历史失败保持。
- 环境不足未验证：本次恢复仍recording_status unverified/writer_end null；新helper实际接管、完整F08–F13/F29–F33/C01、source fsync/SQL/生产和跨平台未验收。140项完成32项，T016/T017/T018及P0/P1保持未放行。root负责关键语义、实际执行和验收；观察到executor按gpt-6.1-sol/medium配置实施，scout按gpt-6-luna/high配置独立复核。

### 2026-10-26 T016多入口控制适配器离线接受

- 已完成：新adapter v2固定helper1c8760，最终121文件freeze、1301构建输入及原v1 106文件独立核对。race63主/230含子项通过0失败0跳过，vet/build0；ad4a owner-before-Bearer源码和测试逐字节保留，事务/store/claim/8KiB预算未改。root审原稳定差异及窄v2差异后离线接受，逐入口健康缺项明确unknown。
- 进行中：三native核心后续包准备；新helper首场4槽校准并行准备，明确两槽在manager被杀且仍缺席时实际转发，随后只读重开接管同core并显式stop。均尚未注册或运行。
- 失败待决策：root两项pure反例证明bare重复marker漏检、v2错误继承legacy nonce首8字节限制；v2修复并保留red、v1原件和全部失败。首包两次误选Native的socket创建被sandbox拒绝，不算通过；最初临时freeze字节未保存的准备来源限制单独披露，最终v1/v2冻结可核验。v2 Python3.8缺removesuffix的freeze准备错误发生在freeze生成前，已等价修复并保留日志，无新增未裁决语义问题。
- 环境不足未验证：本包pure/fake证明不代替真实新helper接管/回滚/并发或新核心完整门槛；完整T016/F08–F13/F29–F33/C01及后续门槛仍未完成。140项完成32项、P0/P1不放行。executor配置gpt-6.1-sol/medium，scout配置gpt-6-luna/high；root负责反例、最终语义和实际运行。


### 2026-10-26 T016单入口与新管理器校准接受，负例观测器修订

- 已完成：新核心原单入口（本地原件 `../experiments/mihomo-events/source5-new-core-legacy-validation.json`，不随Git上传）一VM两槽2/2、29导出/13帧/21FD，原bootstrap/schema1与PID84/87、HTTP/SOCKS、node:a及33/33B均独审接受。新helper多入口生命周期（本地原件 `../experiments/mihomo-events/source5-multi-entry-control-validation.json`，不随Git上传）一VM四槽4/4、45导出/25帧/42已列Owner FD；manager85 SIGKILL/reap后client114/117新连接成功，重开manager120只读接管core93/start620，显式stop revision2后PID1精确回收。两个QEMU均exit0/reap/procabsent，原reader/canonical全通过，原raw和副本字节独立一致。
- 进行中：新v3负例观测器采用同netns TCP/IPv6表逐轮采样，并以自有正向listener校准，保留原时序、typedID、source及release断言；仅准备两项负例，不重跑已接受legacy。纯编译到控制模板的连接按8KiB原预算实施；完整配置切换、provider/pin、回滚与冲突继续。
- 失败待决策：typed-alias v2首场（本地原件 `../experiments/mihomo-events/source5-native-negative-attempt-v2-validation.json`，不随Git上传）保留failed。核心75实际exit1固定拒绝；285个外scope TCP探测中273 ECONNREFUSED、12 EPERM，原guest与canonical正确失败。root原计划忽略root-attached BPF对外scope入口connect的拒绝；EPERM不证明端口关闭，不改成refused或通过。v3只修观测器；partial-bind v2未登记/执行。旧所有失败和0/100不改写。
- 环境不足未验证：两个新接受场均为有限校准，source recording_status unverified/writer_end null，未证明完整新核心门槛、来源fsync/SQL/生产或跨平台。完整F08–F13/F29–F33/C01仍未通过，140项完成32项，T016/T017/T018/P0/P1不放行。实际执行分工：root关键语义和串行VM，executor按gpt-6.1-sol/medium配置实施，scout按gpt-6-luna/high配置独立原件复核。


### 2026-10-26 T016控制模板接受，启动负例排除观测干扰

- 已完成：纯 `CompileControlTemplate` 将编译器拥有的 route-map 运行 generation 留空，供既有控制后端在提交时填入；拒绝调用者 generation，保持候选输入不变，继续执行原合计8192字节预算。两/四入口、程序能力、稳定pin、8192接受/8193拒绝等 race 18主/142含子项通过、vet通过；原Compile/Explain未修改，稳定差异经scout和root分别接受。v3 socket观测保留原校验结果及完整原件。
- 进行中：v4负例准备仅移除启动至精确回收区间内的主动TCP探测，以只读同netns TCP/IPv6表、已知自有listener及进程/FD身份进行有限采样；保留原来源/别名/释放要求。两个真正并发调用者的配置切换、旧版本零写冲突与同ID重试场景独立准备。
- 失败待决策：v3负例原记录（本地原件 `../experiments/mihomo-events/source5-native-negative-attempt-v3-validation.json`，不随Git上传）中partial-bind实际无来源文件，预定一个完整零流量epoch未出现，guest/canonical失败。root和scout确认启动期间外scope连接会触发BPF故障计数，而接管要求初始为零；8次entry-a EPERM先于首次核心exe观测，与提前拒绝相符，但原计数和精确拒绝点未导出，不能宣称已测因果。root裁决新v4先验证partial-bind的原期望；如仍失败，停止这类重试并设计内部阶段取证。typed-alias原canonical通过但也有观测干扰，精确原因接受暂缓，原96轮/15EPERM及所有记录保持。
- 环境不足未验证：完整F08–F13/F29–F33/C01、新核心完整门槛、source fsync/SQL/生产和跨平台仍未验收。140项完成32项、T016/T017/T018/P0/P1不放行。root负责失败裁决和实际运行；executor按gpt-6.1-sol/medium准备，scout按gpt-6-luna/high独立提取因果证据，无宿主BPF/cgroup改动。


### 2026-10-26 T016原生启动负例接受，并发证据保存修订

- 已完成：第二入口占用（本地原件 `../experiments/mihomo-events/source5-multi-entry-partial-bind-validation.json`，不随Git上传）与真实对象别名（本地原件 `../experiments/mihomo-events/source5-multi-entry-alias-validation.json`，不随Git上传）两场v4分别经过完整canonical、原始归档独审与root受限接受。前者106轮/80前后诊断、16导出/1完整零流量帧，后者84轮/120前后诊断、15导出/0帧；during均无主动connect，前后均ECONNREFUSED。原固定core/BPF/bootstrap未改，见证listener每轮可见，保护端口在采样点未监听，进程回收与资源释放一致。此处不主张连续检测或旧v3精确拒绝点。
- 进行中：C01v2仅修改完整race证据的紧凑序列化和夹具finally错误保留，维持256KiB上限、全快照、原4槽/2epoch/26帧要求；加入整场合成形状超限回归及清理异常注入测试。完整旧bundle回滚的原生前提已满足，按独立F10/F33计划准备。
- 失败待决策：C01v1原场（本地原件 `../experiments/mihomo-events/source5-concurrent-control-attempt-v1-validation.json`，不随Git上传）QEMU正常退出但整场失败。原记录只完成前2应用/1epoch13帧；两个真实IPC调用窗口重叠，race-a提交revision2、race-b CONFLICT，winner_retry及ID_CONFLICT已导出。cleanup的256KiB保存异常有原traceback；此前fresh-stale保存异常是代码顺序推断，主异常被遮蔽，未导出的探针/stop/后两槽不补造。新v2将保留全部字段与原上限重新登记验证，不重评v1。
- 环境不足未验证：完整回滚、持久故障边界、provider/pin、最终G05入口及新核心完整门槛尚未验收；source recording_status unverified/writer_end null，不推导fsync/SQL/生产或跨平台。140项完成32项，T016/T017/T018/P0/P1未放行。root裁决、实际串行VM和最终验收；executor按gpt-6.1-sol/medium准备，scout按gpt-6-luna/high独审稳定制品与原件。


### 2026-10-26 T016并发切换完整接受，回滚与发布失败继续

- 已完成：C01 v2实际记录（本地原件 `../experiments/mihomo-events/source5-concurrent-control-validation.json`，不随Git上传）一VM四槽4/4、60导出/26帧经完整canonical、原始归档独审及root受限接受。PID110/111请求区间实际重叠，race-a唯一提交revision2，race-b409 CONFLICT；原请求重试、同ID异内容及fresh stale三探针前后完整快照无额外写入。初始node:a/node:b→候选A的node:b/node:a，各33/33B、ready11/31/31/11ms；stop提交revision3。QEMU1234074正常exit0/reap/procabsent17.636s；公开100引用待最终文档复核。
- 进行中：F10/F33完整旧bundle回滚夹具独立实施，保持固定helper/core/bootstrap；目标3个epoch、4槽、27帧。F12下一方案已具体化为隔离guest内active文件发布失败、明确503 POISONED、原target继续转发及重开后的显式恢复；仅计划，尚未登记或运行。
- 失败待决策：本次新运行无失败；v1因256KiB证据保存及cleanup遮蔽主错误的失败完整保留。v2只改完整对象紧凑保存与异常清理，23pure/fake通过，实际race207437B低于原262144上限，原19测试/业务断言及固定helper未改。原所有native及G01失败不重评。
- 环境不足未验证：本场两个核心的/proc absent已观察，manager-owned wait状态未导出；55项原tar0600/5项0644、提取副本0600分开记录，无秘密扫描通过声明。恢复recording_status unverified/writer_end null；完整回滚、持久故障、provider/pin、最终G05入口、新核心完整门槛及SQL/生产/其他平台尚未验收。140项完成32项，T016/T017/T018/P0/P1不放行。root关键判断与实际VM；executor按gpt-6.1-sol/medium准备，scout按gpt-6-luna/high独审原件。


### 2026-10-26 T016完整旧配置回滚接受，发布失败与持久边界继续

- 已完成：F10/F33实际记录（本地原件 `../experiments/mihomo-events/source5-full-rollback-validation.json`，不随Git上传）一VM四槽4/4、64导出、三epoch13+1+13=27帧经完整canonical、原归档独审与root受限接受。见证先占用新第二入口18999；旧core92/start559退出，目标113/start888失败，恢复core123/start1052使用完整旧bundle/config/rules/groups/auth/entries，只有运行generation改变。恢复后两条新HTTP/SOCKS连接保持node:a/node:b，每流33/33B；同ID重试控制元数据/来源/身份/42FD不变，stop提交revision2。QEMU1509944正常exit0/reap/procabsent19.708s。前场C01公开100引用和5文档已独审一致。
- 进行中：F12固定helper下的guest内active发布失败夹具正在实施，明确区分旧active与已经运行的新target；要求503 POISONED、只读重开、显式恢复同一target及完整6槽/2epoch38帧。F13双失败及F11确定性持久边界暂停/杀进程的root方案已保存，尚未运行；最终G05收集入口和provider/pin矩阵继续。
- 失败待决策：F10v1预审发现两处把compact JSON语义digest与含LF原文件SHA混用的阻断，原失败已复现并保留，v1从未登记/运行。v2仅分离两类摘要并保留原inventory验证，23pure/fake通过后实际运行成功，固定helper/core/BPF未改，无该场新增待裁决失败。宿主/tmp两次空间不足导致命令未能启动，已将明确Hoopa自有缓存/工具/历史构建目录经逐文件SHA/权限/mtime核验迁入workspace，原路径保留符号链接；未删除其他项目内容或改写历史证据。迁移前未执行的命令不计通过。
- 环境不足未验证：三个核心manager-owned wait及失败目标post-exit fdinfo保持unavailable；retry只主张持久状态和身份相等，采样时间/proc-stat不要求逐字节一致。原归档59项0600/5项0644与提取0600分开记录；来源recording_status unverified/writer_end null。F11/F12/F13、完整provider/pin/规则矩阵、最终G05、新核心完整门槛及SQL/生产/其他平台仍未验收。140项完成32项，T016/T017/T018/P0/P1不放行。实际分工为root关键语义、唯一真实VM与接受，executor角色配置gpt-6.1-sol/medium实施，scout角色配置gpt-6-luna/high独立原件复核。


### 2026-10-26 T016补齐显示名与跨来源pin的纯编译回归

- 已完成：新增 `routingcandidate/provider_switch_test.go`，仅alias变化保持路由配置/稳定ID/选择，正确更新显示与原快照摘要；activeB与pinA保持不同，删除非active/pinned/被遮蔽引用均零候选拒绝、输入不变。原七份源码/测试与已接受freeze一致；同包race20主/147含子项0失败0跳过、vet0，scout独审稳定文件与原日志，root受限接受。
- 进行中：F12夹具20项pure/fake通过后正在镜像闭包及冻结；F13方案等待该包冻结后独立准备。F11固定源码适配性与provider候选两场未来方案均已静态核对，分别保留确定性SIGKILL、真实HTTP失败及实际切换验证要求。
- 失败待决策：新增编译回归无失败。旧F10v1/C01/native/G01失败及环境启动错误保持，不重评。
- 环境不足未验证：纯编译测试不证明HTTP获取、持久化、controller调用顺序或真实线路；F12/F13/F11尚无实际结果。140项完成32项，T016/T017/T018/P0/P1未放行。root实施新测试并运行检查、负责最终判断；scout按gpt-6-luna/high角色独审，executor按gpt-6.1-sol/medium继续独立F12准备。

### 2026-10-26 T016发布失败首场保留，修订周期检查点观测

- 已完成：F12 v1原运行（本地原件 `../experiments/mihomo-events/source5-active-publication-attempt-v1-validation.json`，不随Git上传）原始归档60文件经独审字节一致；QEMU1698407正常exit0/reap/procabsent15.390s。guest实际校准EBUSY、自bind active、publishing503 POISONED及后续零写拒绝，四应用各33/33B并正常回收。此处仅记录已发生部分，不接受整场F12。root新28项pure/fake检查与实际796cpio/109ELF/20隔离导入闭包通过，固定helper/core/bootstrap/BPF/readers保持。
- 进行中：独立F12v2保留每流六个必需事件，完整验证可选累计checkpoint的顺序/attempt/连接/segment/守恒，按完整流等待并报告实际帧数；失败前独立保存原始来源、kind/tail诊断且保留主异常。旧精确帧数拒绝合法合成checkpoint的RED已保留。executor继续F13双失败因果见证准备，F11确定性持久边界与provider矩阵仍待后续实际执行。
- 失败待决策：v1在recover前wait_frames(12)观察到更多帧而失败，新来源原件未导出，额外事件种类unknown；恢复副本准备exit1，canonical未执行。按源码ticker修订夹具不等于追认旧事件。新28测试首轮仅旧整函数AST保护未容许新增前后检查而失败，现验证原route/ready/final中部断言逐字节AST不变后通过。初步静态审查漏看begin从注册前至opened写入结束始终持有f.mu，误报pre-open ticker风险；root完整锁范围核对已排除该具体判断并单独更正，原报告保留，不修改核心或宣称所有路径无竞态。首个冻结命令把自身stdout纳入摘要后又打印，root只读复查发现唯一变动finalize.stdout；原清单/校验文件保留，全部日志关闭后在包外重新生成清单，镜像与程序字节均未变，尚未登记运行。
- 环境不足未验证：F12v2未运行，新target接管/recover与最后两槽尚无完整证据；v1新core精确退出未验证。原归档55项0600/5项0644、提取0600分列，不声明秘密扫描通过。T016/T017/T018/P0/P1仍未放行，140项完成32项。root实现本次夹具修订与关键语义/实际VM；executor配置gpt-6.1-sol/medium独立实施F13，scout配置gpt-6-luna/high核对原件和稳定差异。

### 2026-10-26 T016发布失败后的同核心显式恢复接受

- 已完成：F12 v2实际记录（本地原件 `../experiments/mihomo-events/source5-active-publication-validation.json`，不随Git上传）一VM六槽6/6、85导出、两epoch13+25=38帧，经完整canonical、原始归档独审与root受限接受。发布503 POISONED后旧active revision1不变，新的core112/start789继续运行；后续变更零写拒绝，管理器正常退出并重开后的status只读，显式recover提交revision2且仍是同一PID/启动实例/generation/epoch/42FD。恢复后两条新HTTP/SOCKS连接仍走node:b/node:a，全部六流各33/33B；stop提交revision3并由PID1精确wait0/reap目标核心。QEMU1794654正常exit0/reap/procabsent18.595s，六客户端与两管理器回收，夹具挂载清理。
- 进行中：F13目标启动和回滚均失败的独立因果见证夹具正在冻结；root在登记前发现管理器stdout实际20B而非0、canonical与host结果文件名不一致，两项RED及修复后32项pure/fake检查已保存。F11确定性持久边界和provider矩阵继续准备，最终G05收集入口未完成。
- 失败待决策：F12 v1的缺失新来源、额外事件unknown和未完成恢复继续保留；v2没有重新评分旧运行。此前漏看f.mu完整范围的静态误报已有单独更正，不修改核心；首轮freeze自身stdout变动在登记前发现并保留，最终固定1591引用清单经独审通过后才运行。
- 环境不足未验证：本场实际checkpoint0，正例仅pure/fake；初始核心manager-owned wait不可用，两来源recording_status unverified/writer_end null。原归档78项0600/7项0644与提取0600分列，无秘密扫描、source fsync/SQL/生产或跨平台声明。F11/F13/完整provider与规则矩阵/最终G05尚未通过。140项完成32项，T016/T017/T018/P0/P1未放行；root负责关键语义、唯一实际VM与接受，executor角色配置gpt-6.1-sol/medium实施，scout角色配置gpt-6-luna/high独审原件。

### 2026-10-26 T016双重绑定失败明确降级接受，保留校验器修订记录

- 已完成：F13实际记录（本地原件 `../experiments/mihomo-events/source5-failed-rollback-validation.json`，不随Git上传）一VM两槽2/2、63导出、三epoch13+1+1=15帧，经派生完整canonical和原始归档独审后root受限接受。见证111持有core90/start476的原pidfd，退出可读后bind/listen18998，再一次打开原操作观察applying/stop-old且Rollback缺失。新目标112/start811与完整旧配置回滚123/start916均失败；503 UNKNOWN/degraded、同ID200degraded、新请求409PENDING及管理器正常退出/重开status零写均有原件。QEMU1876990正常exit0/reap/procabsent16.935s，两个应用各33/33B及ready11/31ms，两个管理器/见证/客户端全部正常回收。
- 进行中：F11独立checkpoint helper overlay已实现严格selector/guest guard/固定私有marker/self SIGSTOP与异常恢复返回poison，原事务与store写序不变；exact12writes/48callbacks/14semantic及适用pure/fake race已通过，root完成关键seam阶段审查，最终vet/build/freeze及实际16场矩阵尚待验收。provider/有序规则矩阵和最终G05入口继续。
- 失败待决策：原F13 canonical exit1保留，原因是进程四类namespace与readiness六类namespace整字典比较。root新独立host checker逐项核对4process+2clock/PID1/offsets，5项原件回归、23负例通过；独立AST确认其他语句不变后，派生canonical在同一原运行/同字节恢复副本exit0。原冻结包、镜像、guest原件及原错误输出未改，没有重跑VM或增加槽分母；这是单独的派生验证，不将原校验失败覆盖成成功。
- 环境不足未验证：三个核心manager-owned wait/post-exit fdinfo保持unavailable，不虚构reap；actual checkpoint0，三来源recording_status unverified/writer_end null。原归档60项0600/3项0644与提取0600分列，无秘密扫描/source fsync/SQL/断电/生产/跨平台声明。F11、完整provider/规则矩阵与最终G05尚未完成；140项完成32项，T016/T017/T018/P0/P1不放行。root负责关键语义/实际VM/新校验修订/最终接受，executor角色配置gpt-6.1-sol/medium实施F11，scout角色配置gpt-6-luna/high独审稳定差异与原件。

### 2026-10-26 T016精确暂停helper离线接受，十六场恢复夹具实施

- 已完成：F11离线准备（本地原件 `../experiments/mihomo-events/source5-persistence-checkpoint-preparation.json`，不随Git上传）独立helper f509fa8a…978241/11,558,701B、freeze37dcc520…26c975/2248文件经稳定独审和root关键语义接受；2184构建/测试输入及10adapter副本匹配，原事务/store/锁/写序不变。73顶层/248含子节点race通过0失败0跳过，vet/build通过；12writes/48callbacks/14semantic fake trace独审吻合。固定marker仅selector/PID/start/boot，完整guest guard先于实例写入并在action再次校验，原exclusive writer同步后self SIGSTOP，意外resume返回poison。
- 进行中：executor已获正式包，实施private/t016-persistence-vm十六场共享夹具；root明确实际T态/精确SIGKILL、IPC真实错误、每row核心wait归属、孤立intent/no-claim降级、磁盘active2和所有temp保留。scout独立整理剩余有序规则/unknown/不可达证据缺口；provider和最终G05入口继续。
- 失败待决策：初始selector功能缺失RED保留；一次trace长JSON分块提取错误和finalizer错BPF路径的准备错误分别保留，后者freeze_created=false，修正材料路径后冻结，未改Go或重复构建。无新增实际VM失败，旧实际失败和F13原校验错误仍保持。
- 环境不足未验证：本包无built-helper服务/launcher/真实signal/socket/VM，host拒绝仅默认guard及pureCLI检查，不冒充实际运行。16VM/62app/30epoch/min402mandatory+actualCP仍为预定分母，不是实际结果；不证明任意partial-write/断电/source journal/SQL/生产/平台。140项完成32项，T016/T017/T018/P0/P1仍未放行。executor gpt-6.1-sol/medium实施与自检，scout gpt-6-luna/high独审稳定制品，root负责关键语义/最终接受/后续实际运行。

### 2026-10-26 T016恢复夹具关键审查，剩余路由场景具体化

- 已完成：root对F11十六行映射、实际暂停/杀死/重开、原始claim接管、orphan/no-claim和active2恢复分支作阶段审查（本地原件 `../private/t016-multi-entry-preparation/root-persistence-fixture-stage-review.md`，不随Git上传），未要求改变已接受事务或分母。后续有序规则方案（本地原件 `../private/t016-multi-entry-preparation/root-ordered-routing-validation-plan.md`，不随Git上传）已独立静态核对：五种unknown/AND场景及四种应用/进程/域名/IP规则顺序，共九个拟定VM、十八槽、105个必需事件；不是实际通过。已接受的管理器缺席后新连接证据继续复用，不新增重复场次。
- 进行中：executor继续F11完整guest/canonical、pure/fake与镜像准备。已按root审查补入全线程T态和自有SIGSTOP等待证据、write12终末重试的完整前后快照、marker实际字节与原始摘要绑定，最终稳定验收尚待完成。provider准备及不可达目标方案（本地原件 `../private/t016-multi-entry-preparation/root-unreachable-routing-validation-plan.md`，不随Git上传）排在此后。
- 失败待决策：静态审查发现setup_finished不含底层errno，root以因果边界补充（本地原件 `../private/t016-multi-entry-preparation/root-unreachable-causal-boundary.md`，不随Git上传）将未来结论限定为“唯一选中端点拨号失败且无备用线路转发”，不声称已观察ECONNREFUSED或TCP RST；未增加stderr导出或修改核心。一轮executor因模型容量不足中断，原修改保留并已在同配置恢复；无新增实际VM失败，历史失败不重评。
- 环境不足未验证：F11整场fake测试/最终freeze及十六场实际恢复仍待执行；后续新候选的实际编译输出、8KiB大小与新hash规则/不可达运行尚待验证。未知文本的已接纳连接与缺凭据的接纳前拒绝分列，缺失数据不填零。140项完成32项，T016/T017/T018/P0/P1不放行。观察到executor按gpt-6.1-sol/medium实施、scout按gpt-6-luna/high独立搜集和审查证据，root负责关键语义裁决。


### 2026-10-26 T016持久恢复矩阵完成离线准备并冻结

- 已完成：F11验证包（本地原件 `../private/t016-persistence-vm/README.md`，不随Git上传）固定十六个中断位置，37项离线测试通过；798归档条目、109 ELF依赖、22隔离导入检查通过。最终freeze包含2344项输入，镜像48731529B；这些是准备验收，不是实际恢复结果。完整控制分支合成测试与仅覆盖控制入口的canonical smoke边界已明确记录。
- 进行中：scout对稳定freeze及最终变更独审，root准备依序运行16场；预定62应用槽/30来源世代/402必需帧加实际checkpoint不改为已观察值。后续provider与规则矩阵保留既定顺序。
- 失败待决策：阶段审查发现上一场canonical仅五字段检查过弱，已补固定路径、完整分母、成功checker执行记录及root独立验收引用，并新增10个拒绝变体。原失败测试日志保留。executor第二次因模型容量中断后，root接管剩余实现与打包；没有自动切换模型或声称executor完成最终验收。
- 环境不足未验证：本包尚未登记或运行VM、helper、BPF或来源reader；实际全线程停止/杀进程/显式恢复/来源完整性仍待验证，不宣称磁盘掉电或任意故障顺序保证。140项完成32项，T016/T017/T018/P0/P1均未放行。root负责关键恢复语义与本次收尾；scout按gpt-6-luna/high独立审查，原executor配置gpt-6.1-sol/medium遭容量限制。


### 2026-10-26 T016首个持久边界实际运行完成，修正主机证据校验

- 已完成：w01原件审查（本地原件 `../private/t130-followup/t016-persistence-w01-raw-review.md`，不随Git上传）核对72个tar成员与提取原件一致。QEMU2354610正常退出回收17.964秒；四应用各33/33B，原core91/start521继续服务。checkpoint manager110/start801实际全线程T/SIGSTOP后被SIGKILL并回收，caller实际RemoteDisconnected；恢复缺失操作返回500 ACTION_FAILED且持久记录不变，显式stop提交revision2。原来源26帧=25必需+1实际checkpoint，属于原始观察，尚待完整canonical接受。
- 进行中：主机派生修订（本地原件 `../private/t016-persistence-host-v2/README.md`，不随Git上传）已冻结并交独审，44个离线测试方法通过。独立目录同字节副本成功后，继续处理同一run，不重跑虚拟机，不改变已登记16场/62槽/30epoch分母。
- 失败待决策：root新增目录约定错误地把派生输入/输出置于原run内部，触发原隔离规则导致copy exit1；此前预审漏查跨组件路径契约。改用独立sibling后原canonical实际exit1于Go路由字段序重建：导出sort_keys改变了字典顺序，已按固定Go结构顺序重建且保留SHA/长度比较。独审另发现真实source.lock零字节元数据未纳入旧exact-keyset，派生校验现完整验证锁的模式/owner/空摘要/原inode/mtime不变。原freeze、原运行、失败日志及审查保留。
- 环境不足未验证：新派生完整canonical尚未执行，其余15场尚未启动；helper/core/guest未修改，不能把主机校验修订当作恢复验收成功。provider executor仍因容量不可用，root未自动切换模型。140项完成32项，T016/T017/T018/P0/P1保持未完成。


### 2026-10-26 T016恢复矩阵前两场接受，继续顺序执行

- 已完成：矩阵进度（本地原件 `../experiments/mihomo-events/source5-persistence-validation.json`，不随Git上传）现记录w01/w02两场有限接受，共8应用槽、3来源世代、52帧（51必需+1实际checkpoint）。w01同原件派生canonical通过且111个原文件pins未变；w02在operation recorded边界实际杀管理器，恢复启动新目标并切换到node:b/node:a，4流各33/33B，最后stop revision3。两场均经原归档/来源结果独审和root接受，原失败记录保留。
- 进行中：w03及后续既定中断位置依次运行；每行需要前一行固定canonical、实际执行记录及独审/root接受绑定，不自动跳过失败。
- 失败待决策：w01主机目录和字段序错误已由独立host-only v2修订处理，原运行未重跑；w02无新失败。未以来源锁元数据替代原始来源pair，也未删除周期checkpoint。
- 环境不足未验证：余14行尚未接受，不能将2/16进度表述为完整恢复保证。来源recording_status unverified、writer_end null；w02新核心manager-owned wait不可用，/proc absence不替代wait。140项完成32项，T016/T017/T018/P0/P1未放行。


### 2026-10-26 T016第三场暴露检查点抢先返回，保留v1并准备确定性park

- 已完成：w03失败原件独审（本地原件 `../private/t130-followup/t016-persistence-w03-failure-review.md`，不随Git上传）核对50个归档成员一致。QEMU2565080退出回收，但guest失败：caller118在8,787,049,751ns收到真实503 POISONED，早于全线程T观察及预定SIGKILL检查；不是超时。两个已完成应用流各33/33B，失败前来源只保存12帧，无epoch_closed；旧core精确退出未验证。v1保留2个接受/1个失败/13个未执行，w03仍按4槽/2epoch登记，未降分母。
- 进行中：root按新决定（本地原件 `../private/t016-multi-entry-preparation/root-checkpoint-park-v2-decision.md`，不随Git上传）实施独立checkpoint helper v2：signal请求成功后永久park，不允许HTTP提前返回或事务前进；原guard/marker/事务/store顺序不改。新增真实ControlProbe fake事务回归验证signal先返回、request取消和释放fake park后的poison/零后续写；race及CLI race已通过，vet/build/pins继续。新helper需要独立freeze/审查及新16行矩阵，不能混入v1。
- 失败待决策：已确认f509把self SIGSTOP返回误当作已恢复的注释前提不足，实际w03证明请求可提前完成；不推断具体内核线程调度原因，不以503或marker-only当成功。旧所有原件和失败日志保留。独立设计审查（本地原件 `../private/t130-followup/t016-checkpoint-park-design-review.md`，不随Git上传）支持锁内park，另有graceful取消边界（本地原件 `../private/t016-multi-entry-preparation/root-checkpoint-park-v2-boundary.md`，不随Git上传）。
- 环境不足未验证：新helper无真实signal/VM结果；旧w03未执行recover/canonical。独立provider源码核对确认真正v2 overlay包含provider字段，旧public结构产生的兼容疑虑已排除，不据此改compiler；未来实际bundle仍待编译验证。140项完成32项，T016/T017/T018/P0/P1未放行。executor容量不可用，root实施关键修订，两个scout按gpt-6-luna/high分别独审原件与源路径，无自动模型切换。

### 2026-10-26 T016 parked helper与新F11矩阵离线准备完成

- 已完成：checkpoint helper v2通过74个顶层/249个含子项的测试记录、race/vet/build和独审，root接受仅用于新矩阵。新矩阵固定helper `dc706c…`、冻结 `e53b6b…`，43项纯/fake测试通过；镜像 `bad8a3…` 的798项cpio、109个ELF和22个隔离import均核对。Go字段顺序、source.lock完整元数据及外部恢复副本路径修正已纳入；旧w03失败原件保留。
- 进行中：冻结后的独立preboot审查，随后按原16场/62应用槽/30epoch/402 mandatory frames新登记、串行执行。provider真实Go编译准备由executor `gpt-6.1-sol/medium`独立执行；矩阵root单写，scout `gpt-6-luna/high`只写审查证据。
- 失败待决策：新版第一离线回归发现历史old-helper样本错误使用新hash期望，已限定该历史测试使用原hash并保留失败日志；最终43项通过。阶段路径审查发生于新wrapper写入前，最终版已固定v2 registry/new acceptance/run-local checker/sibling输出；泛用copy CLI的record参数不作为对任意手工调用的全run不可写承诺，实际固定命令必须写sibling。
- 环境不足未验证：新版尚无真实VM观察，旧2通过/1失败不能作为新hash样本。T016/T017/T018、P0/P1未完成，任务总计140、已完成32不变。

### 2026-10-26 新F11前两场接受，第三场完成恢复但主机比较需修订

- 已完成：新helper矩阵w01/w02均经原件独审/root接受，合计8应用槽、3epoch、52帧（51 mandatory+1CP）。w01无operation恢复500并保留原core；w02记录operation后恢复提交revision2再stop3。新版进度（本地原件 `../experiments/mihomo-events/source5-persistence-v2-validation.json`，不随Git上传）与旧v1独立。
- 进行中：w03 `vm-w03-qd72iwor` 的实际checkpoint请求未提前reply，manager113/start740精确kill/reap，Recover提交rev2/Stop3，4应用及2epoch原始26帧已取出；主机canonical在原件退出记录比较处exit1。root已冻结单独host-v3修订，按registered nonce/gen/PID/start比较完整记录，43项+focused2项通过（44distinct），正在稳定独审；接受前不重分析、不运行w04。
- 失败待决策：独审（本地原件 `../private/t130-followup/t016-matrix-v2-w03-failure-review.md`，不随Git上传）确认admission数组与sorted nonce对象values内容相同但顺序相反，不是core状态差异。原VM/source/失败checker日志不改；拟仅新目录分析同原件，不重试VM、不变分母。另provider首包只声明instance而被实际runtime要求拒绝，已由executor在独立v2改为instance/program_name/program_path并执行真实frozen validator：4旧拒绝、4新完整接受，相关race通过，正在独审。
- 环境不足未验证：w03原checker未到达strict/source native解码，后13场未执行；provider编译/validator通过不等于实际HTTP/控制更新/流量路由通过。T016/T017/T018及P0/P1不变，140任务完成32。矩阵root实现、scout gpt-6-luna/high审查；provider executor gpt-6.1-sol/medium实现，无自动模型切换。

### 2026-10-26 w03同原件主机修订通过，继续第四场

- 已完成：host-v3独审（本地原件 `../private/t130-followup/t016-matrix-v2-host-v3-review.md`，不随Git上传）与root接受后，仅用同一w03原VM/source做新sibling分析，copy/canonical均0；结果独审（本地原件 `../private/t130-followup/t016-matrix-v2-w03-canonical-review.md`，不随Git上传）确认116原run+11旧失败文件不变、4副本同字节、2epoch各13frames及4流33/33。root已接受有限w03，累计新矩阵3/16场、12应用、5epoch、78frames（77mandatory+1CP）。
- 进行中：w04已完成实际执行和canonical，正在单场原件独审；仍逐场接受。provider candidate v2经独审关闭required_fields问题，root仅接受纯编译/runtime-shape；executor继续新guest-probe准备，未来真实HTTP必须独立记录且编译拒绝前禁止Execute。
- 失败待决策：w03原主机canonical1保留，已由独立host-only修订解释并通过同原件后续完整校验，无新VM/改分母。旧v1提前503失败也保留。
- 环境不足未验证：新版余行和provider真实HTTP/更新/转发尚未验收；source recording_status unverified、writer_end null及manager-owned wait不可用边界不变。T016/T017/T018/P0/P1未完成。

### 2026-10-26 新F11矩阵前半段8场独审接受

- 已完成：新矩阵w01–w08全部逐场原件独审/root接受，固定累计30应用槽/14epoch/195frames（194mandatory+1CP）。w04 old-stopped、w05 new-spawn均恢复提交2再stop3；w06保留未引用孤儿intent，以不同nonce恢复（3intent仅2运行epoch）；w07按既定2槽/1epoch返回503UNKNOWN/degraded，desired running与observed verified-stopped明确区分，不猜测launch、不做ordinaryStop；w08接管已运行的同nonce/PID/start目标，42FD/source前缀保持，新旧核心均精确PID1wait0。
- 进行中：w09及后续8个中断位置按固定注册顺序继续；每场成功检查后仍需原件独审/root接受。进度原件引用（本地原件 `../experiments/mihomo-events/source5-persistence-v2-validation.json`，不随Git上传）持续更新。provider纯candidate v2和bounded guestprobe已独审/root接受，executor正在独立准备真实refresh-reject VM：7拒绝、4应用/1epoch/25mandatory，尚未运行。
- 失败待决策：旧v1提前503及新版w03主机JSON对象/数组顺序错误原件保留；新版w03通过host-only同原件修订，不重跑VM。当前无新增未裁决失败。
- 环境不足未验证：余8场、provider真实HTTP/全状态零写/旧线路保留未验收；source recording_status unverified/writer_end null不提升。孤儿FD字段null不是全局zero-FD证明。T016/T017/T018及P0/P1仍未完成；140任务完成32。root控制真实VM与恢复裁决，scout gpt-6-luna/high逐场审证，executor gpt-6.1-sol/medium准备独立provider包。

### 2026-10-26 新F11十二个完整记录写入边界接受

- 已完成：w09–w12均已逐场原件独审/root接受，新版进度（本地原件 `../experiments/mihomo-events/source5-persistence-v2-validation.json`，不随Git上传）累计12/16场、46应用槽、22epoch、300frames（298mandatory+2CP）。覆盖目标验证完成、publishing、active2已发布以及operation committed；恢复均核验同一目标身份，未重复启动。w12完整重试前后inventory相同，独立Status仍为相同实例、revision2、无pending，最后stop3。
- 进行中：第13场spawn-returned已完成运行、提取、同字节副本和canonical，正在独立原件审查；之后还有active write/file-sync/rename三场。provider-refresh包继续离线实现和镜像准备，无实际启动。
- 失败待决策：无新增实际失败。w12事件未单独保存重试request；root基于冻结源码中同一未修改self.req经同一序列化器发送，以及原始reply/完整快照接受有限重试行为，明确不声称独立捕获了逐字节相同的重试wire body。原v1提前回复失败、新版w03旧checker错误及各自原件保持。
- 环境不足未验证：尚余四场的最终接受、provider实际更新拒绝和其余规则矩阵；source recording_status unverified/writer_end null、任意partial-write/掉电/SQL和未实测平台均不提升。140任务完成32，T016/T017/T018/P0/P1未放行。root负责语义与实际VM，scout gpt-6-luna/high负责独审，executor gpt-6.1-sol/medium负责provider实现。

### 2026-10-26 F11第二版十六场有限矩阵完成接受

- 已完成：spawn-returned、active-write、active-file-sync、active-rename四场实际host/extract/copy/canonical均0，逐场原件独审后root接受。原operation尚无Instance与真实claim/运行身份分开；空/完整temp在恢复后保留原inode/mtime/hash；rename后从磁盘revision2恢复同target。完整矩阵16场、62槽、30epoch、404frames（402mandatory+2CP）、1252导出；最终汇总（本地原件 `../private/t016-multi-entry-preparation/root-persistence-matrix-v2-runtime-acceptance.json`，不随Git上传）核对2129个不同引用未变，[G05记录](validation/g05-routing.md)已更新。
- 进行中：executor继续provider-refresh离线包最后检查和冻结，随后独立预审/root实际运行；source-backed单场missing-proof/sticky-refusal方案交scout只读评估map寿命与恢复seam，尚无实施或运行授权。provider switch、规则次序/unknown和不可达节点仍按既定有限方案准备。
- 失败待决策：无新增VM失败；两处独审报告中的前驱路径/本行launch摘要转录错误已从原件核对修正，再由root接受，不改原run。旧v1提前响应失败及新版w03原canonical顺序错误均保留。w12重试wire body未单独导出、完整temp原始payload未导出的证据范围明确保留。
- 环境不足未验证：F11不证明掉电、任意partial-write、SQLite、source writer-end或跨平台；尚未执行的provider/规则负例保持未验证，最终routing_test.go收集入口未完成。140任务完成32，T016/T017/T018/P0/P1未放行。root负责关键语义、串行VM和汇总验收；scout gpt-6-luna/high独审，executor gpt-6.1-sol/medium继续实施，无自动模型切换。

### 2026-10-26 provider首场保留失败，补实际Go运行路由渲染契约

- 已完成：refresh包离线21方法/44子例、802cpio/110ELF/20隔离import通过，1596文件冻结独审后root执行一次。首场记录（本地原件 `../experiments/mihomo-events/source5-provider-refresh-attempt-v1-validation.json`，不随Git上传）保留QEMU3483035正常退出回收12.359秒、14个原归档成员逐项匹配、实际应用/GET均0；原注册4槽/1epoch/7探针不变。
- 进行中：root发现固定helper将原route模板解为带omitempty的Go结构再marshal，夹具却只向原模板补generation后比较对象。executor在独立小包用真实frozen Go validator/renderer生成四个fixture的完整运行route字节并验证唯一generation替换，随后新refresh-v2准备；switch包保持未冻结并暂停等待同一渲染契约。不是改代理内核或放宽字段比较。
- 失败待决策：首场guest在core-start的home/route-map.json比较处失败，原件未导出该实际文件，具体字段差异不可恢复；source为空的失败快照，copy因正长度要求exit1，canonical未执行；core精确退出/回收未验证。原包、原run和日志不改，不把host exit0当场景成功。静态源码说明与未来纯Go复现均不冒充该缺失文件原件。
- 环境不足未验证：provider真实HTTP拒绝/旧态零写/后续转发尚未观察；新route产物及refresh-v2尚未独审或运行。新的身份缺证据方案已解决只读FD与map全局冻结混淆，仍需实现refusal-observed/repair-complete客户端握手，尚无新场执行。140任务完成32，T016/T017/T018/P0/P1未放行。root负责恢复语义和本次诊断，executor gpt-6.1-sol/medium实施，scout gpt-6-luna/high独审原件。

### 2026-10-26 固定Go运行route产物接受，refresh-v2实施中

- 已完成：四份真实linuxControlConfig→typed json.Marshal产物在独立35文件小包冻结；Go普通/race各53PASS节点，Python5方法通过。独审后root核对2336构建输入、11overlay映射、4Go工具及原candidate95冻结文件，有限接受（本地原件 `../private/t016-multi-entry-preparation/root-provider-runtime-route-acceptance.json`，不随Git上传）绑定严格唯一generation等长替换和全字节比较。
- 进行中：executor在独立refresh-v2接入真实Lifecycle.start测试与实际runtime-route文件导出，再建镜像和独审。原template/whole-bundle语义摘要与运行文件分离；switch未冻结并等待复用该已接受产物。
- 失败待决策：首场实际失败保留。纯包说明把Python55个静态subTest上下文误计56，已由executor确认/root另记更正，原冻结不改；metadata续写命令无独立receipt，仅接受现存已核验元数据。root首次附加核验误按dict遍历旧candidate的list，写接受前停止，修正后全部pins通过。无新增VM。
- 环境不足未验证：真实provider GET/拒绝/旧线路保留、refresh-v2启动及source证据仍待新登记；不补填首场缺失文件，不放行T016/T017/T018/P0/P1。140任务完成32，模型分工保持root语义/执行、executor gpt-6.1-sol/medium实现、scout gpt-6-luna/high独审。

### 2026-10-26 refresh-v2实际全链通过，原件独审与规则准备继续

- 已完成：独立refresh-v2冻结1628文件、24方法/56实际subTest、820cpio/110ELF/21隔离import，经预审后root写单次运行接受（本地原件 `../private/t016-multi-entry-preparation/root-provider-refresh-v2-preboot-acceptance.json`，不随Git上传）。新run vm-provider-refresh-v2-gn89uigi 的register/host/extract/copy/canonical均0，4应用/1epoch/25frames；3真实GET和7拒绝报告已导出。实际runtime-route635B完整导出，严格匹配真实Go渲染结果；该场结果尚在独立原件审查。
- 进行中：scout审查本场原始归档、完整拒绝前后状态、同核心与Stop2回收；executor继续switch独立夹具的21方法/120实际subTest之后的镜像/闭包/冻结。root独立完成九行有序规则输入（本地原件 `../private/t016-ordered-candidate-preparation/README.md`，不随Git上传），纯Go43PASS节点/race、真实runtime渲染118PASS节点/race、Python2方法/56实际subTest，101文件冻结dbf28a52…55e8db，待独审后给未来guest消费。
- 失败待决策：本次refresh-v2无新的实际失败；旧首场及零字节失败source、缺失route、copy1保持原状。refresh-v2首轮fakeUID错误和switch首轮fake进程缺字段各留原日志并只修fake边界，不把离线测试当真实成功。
- 环境不足未验证：订阅切换、九行规则实际运行、不可达目标与接纳前缺证据拒绝尚待验证；九行候选是离线配置，不产生新VM分母。source recording_status unverified/writer_end null保持。140任务完成32，T016/T017/T018/P0/P1未放行；root负责关键语义与实际运行及ordered输入，executor gpt-6.1-sol/medium负责switch，scout gpt-6-luna/high负责稳定独审。

### 2026-10-26 refresh-v2原件独审及有限运行接受

- 已完成：独审原console归档75成员与提取字节/摘要逐项相同，70项0600/5项0644原模式与提取75项0600分列；4客户端身份、33/33终末、25原帧及同字节strict/source输出一致。root复核稳定引用后写接受（本地原件 `../private/t016-multi-entry-preparation/root-provider-refresh-v2-runtime-acceptance.json`，不随Git上传）56eb5df6…878451d和摘要（本地原件 `../experiments/mihomo-events/source5-provider-refresh-v2-validation.json`，不随Git上传）3c97c375…5ba74d7；G05记录新增7拒绝表与精确边界。
- 进行中：switch夹具最后闭包/冻结；root九行ordered输入已冻结，scout继续独立核对计划、101冻结文件、2351有效Go输入和实际测试日志。其后仍按新登记执行，不复用旧候选的运行结果。
- 失败待决策：无新增未裁决失败。原refresh首场、其缺失实际route/空source失败快照/copy1、旧恢复矩阵失败均保留；没有通过第二场重评第一场。
- 环境不足未验证：本场不证明成功provider switch、调度/自动选优、任意订阅格式、全部规则优先级/不可达/接纳前缺证明场景。source writer_end null和recording_status unverified保持；不增加正式100流样本。140项完成32，T016/T017/T018/P0/P1仍未放行。模型分工与授权保持，继续后续工作。

### 2026-10-26 switch首场取证超限，分世代完整记录修订继续

- 已完成：switch离线21方法/122实际subTest和825cpio/110ELF/20隔离import后冻结，经独立预审/root接受执行一次。原件独审确认59成员同字节、前4客户端各自退出回收、2完整epoch共26帧；失败摘要（本地原件 `../experiments/mihomo-events/source5-provider-switch-attempt-v1-validation.json`，不随Git上传）保留原8槽/3epoch/51必需帧分母。ordered九行候选和不可达单行候选已分别完成纯编译、固定runtime渲染、独审/root接受，均无实际VM结果。
- 进行中：executor按root布局裁决（本地原件 `../private/t016-multi-entry-preparation/root-provider-switch-v2-layout-decision.md`，不随Git上传）在独立switch-v2实现全epoch文件索引，完整记录与原上限保留，并用实际763项pins补生产写入路径、canonical及全形状预算测试。ordered整场夹具暂停等此修订；root已完成不可达失败谓词的5方法/256实际subTest纯测试并冻结，scout独审中。
- 失败待决策：switch第三次Apply已提交revision3，record_epoch保存第三代完整epoch后聚合registration超256KiB（实际307753B）。首版fake的元数据规模不足；后4槽、跨来源pin拒绝及Stop4未发生，第三代为空来源失败快照，copy1/canonical未运行。原guest、run、已消耗注册和失败日志不改；核心精确退出未验证，不从QEMU0推断。修订方案已裁决，尚待实现与新验收。
- 环境不足未验证：新switch-v2、九行规则、不可达和缺证明粘滞拒绝均无实际验收；新纯谓词不替代原始native/source/connector证据。140项完成32，T016/T017/T018/P0/P1不放行。root负责关键语义和真实VM；executor gpt-6.1-sol/medium单写修订，scout gpt-6-luna/high独立审证。

### 2026-10-26 switch-v2冻结，失败场景的独立准备模块接受

- 已完成：switch-v2独立包冻结1659文件，26方法/144实际subTest通过，镜像826cpio/110ELF/21隔离imports；三份原epoch原字节回归与旧307753B拒绝、全文件索引六次更新及canonical篡改检查通过。最终镜像764项pins进入预算，76JSON合成投影最大157613B；部分小记录仍为最小fake，不把投影当作未来运行尺寸保证。原失败run和上限保留。
- 已完成：不可达事件谓词独审/root接受，5方法/256子例，root另验证第11次尝试明确拒绝；全局source_seq连续性仍由固定完整strict/source读取链核验。缺证明客户端held-refusal握手5方法/93子例、不可达端点占位5方法/50子例分别通过独审及root有限接受。均为fake/native边界准备，没有宿主socket/BPF/VM操作。
- 进行中：scout稳定预审switch-v2及root单次wrapper，待接受后root新登记实际运行。executor恢复九行ordered整包；root拥有不可达整包，已实现端点模块，完整guest/client/canonical还待接入。缺证明握手要求真实控制通道、持久事件、BPF删除/NOEXIST恢复和五尝试整场证据，尚未实现完毕。
- 失败待决策：switch-v2一轮尺寸fake因原失败admission缺core_processes而报错，保留原日志后用明确标注的synthetic后续形状补齐，最终通过；无新增实际VM失败。独审发现端点helper本身不强制观测phase全集，root接受条件明确由整guest/canonical强制四应用前后及停止后的完整固定顺序。无未裁决语义问题。
- 环境不足未验证：所有新准备模块不证明实际拒绝、无fallback、BPF恢复或切换成功；T017差异证据只确认0017至新核心的执行源码变化集中在多入口owner/选择/status，不把旧hash的通过自动移给新hash。140项完成32，T016/T017/T018/P0/P1不变。root负责语义和最终验收，executor gpt-6.1-sol/medium实施，scout gpt-6-luna/high独审。

### 2026-10-26 switch-v2实际失败保留，修正跨语言探针基线契约

- 已完成：新v2登记/host/extract/copy均0，独审原76归档成员同字节。三次Apply和六客户端完成，原来源38帧=13+13+12，第三代未关闭；失败摘要（本地原件 `../experiments/mihomo-events/source5-provider-switch-attempt-v2-validation.json`，不随Git上传）固定8槽/3epoch分母，核心131精确退出未验证。原始Go probe实际返回expected_rejection/no candidate，accepted/input前后分别不变。
- 进行中：executor暂停保留ordered进度，在独立switch-v3按root裁决（本地原件 `../private/t016-multi-entry-preparation/root-provider-switch-v3-probe-decision.md`，不随Git上传）导出真实Go accepted Spec，绑定实际报告回归及原编译输出；完整guest/canonical、真实pins预算与镜像检查后再冻结。root继续不可达客户端模块：实际send返回计数、部分写入、12秒整体操作预算、EOF/reset与超时/EPIPE区别，5方法/51子例通过，尚待独审及完整运行集成。
- 失败待决策：v2 guest verifier错误地将switch-pin-absent的accepted基线设为refresh-baseline；冻结Go明确使用switch-active-b，fake复用错误期望而漏测。检查器在保存probe和before/after后抛错，故后两槽/Stop4缺失；canonical1的缺before-stop文件是后果。根因与修订已裁决，原run/旧包/一次注册不改，无自动重跑或host-only冒充补全。
- 环境不足未验证：成功八槽切换、完整有序规则、不可达及缺证明五尝试仍待实际验收；单独client/helper纯测试不证明原始source完整或真实网络行为。140项完成32，T016/T017/T018/P0/P1继续未放行。root承担关键决策与真实运行，executor gpt-6.1-sol/medium实施，scout gpt-6-luna/high独立审证。

### 2026-10-26 switch-v3八槽实测与原件独审接受

- 已完成：switch-v3冻结1726文件，30 Python方法/163实际subTest、真实Go完整Spec导出和编译等价、827cpio/110ELF/21隔离import通过。独审指出跨freeze旧checker依赖未显式pin，root在独立execution目录核验旧源码与完整真实输入前后摘要，再做2项纯回归，补充独审后接受一次VM；首个root补充脚本误选regression前缀的日志保留并排除最终闭包证据，原冻结不改。
- 已完成：新run vm-provider-switch-v3-oun9x28m 的register/host/extract/copy/canonical全部0，经91成员原件独审/root接受。公开摘要（本地原件 `../experiments/mihomo-events/source5-provider-switch-v3-validation.json`，不随Git上传）固定8流/3epoch/51mandatory/0CP；别名改动保持A，切active B后US跟随B而固定A保留，缺来源pin拒绝后两条线路继续。三次Apply及Stop4完整，前两场失败保持原状。root接受核对120个不同独审引用，最终197个引用固定。
- 进行中：executor继续九行ordered整包的实际生产入口纯测试、全wire字段和镜像闭包；root继续不可达节点整包。不可达failure_exchange已独审/root接受；新增local consumer 4方法/46subTest、实际program_client.main通过fake边界2方法/36subTest，保留部分写入并拒绝提前协议关闭、超时、意外响应，尚未运行该场VM。
- 失败待决策：无新增实际VM失败。原件独审报告把末代registration配置投影误标为槽身份摘要，已在接受前改为显式final_registration_slot_projection；实际PID/start/generation来自原客户端及各epoch登记，不跨代代填。此前两次switch失败及旧refresh/F11失败不重新评为通过。
- 环境不足未验证：manager82退出0/reap，三个核心均消失，但manager-owned core wait未导出；source recording_status unverified/writer_end null不提升。ordered、不可达及缺证明粘滞拒绝仍待完整实际验收；最终routing_test.go尚未接入，T016/T017/T018/P0/P1不放行，140任务完成32。root负责关键语义/实际VM/最终验收，executor gpt-6.1-sol/medium实施，scout gpt-6-luna/high独审。

### 2026-10-26 九行规则矩阵实测接受，缺证明事务继续

- 已完成：ordered冻结2548文件，18 Python方法/559实际subTest、真实Go完整wire test/race、836cpio/110ELF/23隔离import通过独审。root按九行各一次登记串行执行，全部register/host/extract/copy/canonical为0；原件独审核对315归档成员同字节，root复核272个独审引用并固定1133个最终引用后写接受（本地原件 `../private/t016-multi-entry-preparation/root-ordered-routing-runtime-acceptance.json`，不随Git上传）及摘要（本地原件 `../experiments/mihomo-events/source5-ordered-routing-validation.json`，不随Git上传）。18流包含12转发、6接纳后规则拒绝；9epoch/105mandatory+1CP=106帧，核心wait仍不可用，manager/client各自退出回收与之分列。
- 已完成：缺证明原语map4方法/34子例、pidfd5方法/46子例，有限两次全线程暂停/恢复/退出控制器6方法/64子例，均经过独审/root接受供后续集成。完整112/296证明谓词4方法/138子例及完整42FD选owner的3方法/78子例也已冻结独审；这些全部是纯测试，不计为真实BPF恢复或组停。owner选择使用known-first五份原件，测试中ready.boot_id到saved-core的连接明确标注synthetic，不声称实时进程身份。
- 进行中：不可达整包冻结1715文件，35方法/322子例、真实Go三组重试/CP向量test/race/vet、813cpio/110ELF/29隔离import，通过离线闭包后交独立预审，尚未登记实际VM。executor继续独立缺证明五尝试整包；root单写选定cookie删除/原字节NOEXIST恢复事务，接口与故障处理已记录，不改变核心或普通helper事务。
- 失败待决策：无新增真实VM失败。ordered离线的整process对象相等曾错误包含观测时间，冻结前改为稳定PID/start/cgroup/ns/executable身份并补动态观测正例，旧离线镜像/日志保留。不可达初期fake异常类型与元数据独占创建问题已修复并保留日志。executor启动一度报告model at capacity，以原角色重试成功，未切模型。
- 环境不足未验证：不可达端口全生命周期、真实缺证明删除/恢复/持有socket、第二暂停后两入口持续拒绝仍待实际验收；最终routing_test.go及T017/T018尚未完成。140项完成32，T016/P0/P1不放行。root负责事务语义、实际VM和最终验收；executor gpt-6.1-sol/medium实施，scout gpt-6-luna/high审证。

### 2026-10-26 不可达原件完整但检查器失败，宿主阶段契约修订

- 已完成：不可达1715文件冻结经独审/root接受后一次真实运行，register/host/extract/copy均0。独审62归档成员同字节；四客户端退出回收、Stop2和十份reservation原件齐备。纯解析45帧连续完整：19+10+10=39mandatory，另6CP，原来源与本地33/0分开。原失败摘要（本地原件 `../experiments/mihomo-events/source5-unreachable-attempt-v1-validation.json`，不随Git上传）保持canonical1、未运行strict/recovery，不将guest或QEMU0当完整成功。
- 进行中：executor暂停保留admission准备，独占新的unreachable-host-v2，只修connected/terminal字段阶段契约，接真实program_client.main完整JSONL到canonical fake，并用本场原8条JSONL/4final原件做回归。独立freeze/预审后root才执行新固定reader链；原VM、run、派生目录及原失败均不改，无新增VM分母。
- 已完成：root选定owner的删除/原字节NOEXIST恢复事务在真实ctypes适配器、fake map边界下通过6方法/37子例并冻结独审。初轮测试构造参数多传一项的失败保留；第二版通过后再补非法顺序也永久failed，最终第三轮通过。已核对历史source SHA与版本映射。独审指出接口文字“任何失败都返回dict”过宽，root用独立补充裁决（本地原件 `../private/t016-multi-entry-preparation/root-admission-transaction-precondition-addendum.md`，不随Git上传）明确pre-native异常须先独立封存，body失败须先封存结果，然后cleanup且不重试/不ACK；冻结原件不改。
- 失败待决策：不可达原canonical要求初始与终末key集合全等，而失败槽终末合法新增两字段。完整fake复制终末制造初始，未检出；修订原因和范围已裁决，后续严格谓词尚未实际执行。admission现有候选入口名与已冻entry-a/b接口不一致，scout确认无完整匹配旧产物；root授权在新包用原Go compiler/renderer生成精确entry-a/b A/B基线，不手改bundle，尚未生成验收。
- 环境不足未验证：新的host消费链、真实map恢复与全线程控制及五尝试整场保持未验证；T016/T017/T018/P0/P1未放行，140项完成32。模型分工保持root关键语义/真实执行，executor gpt-6.1-sol/medium实现，scout gpt-6-luna/high独审。


### 2026-10-26 T016同原件不可达验收完成，五尝试拒绝恢复继续

- 已完成：独立host-v2冻结146项，新阶段契约6方法/413子测试通过并独审；root创建同原件保护副本，copy/canonical均0。独审确认123原引用未变、45帧=39必需+6CP、四flow/一完整epoch与62导出一致。root接受57d9d05e…c38bc6、公开摘要51e875a1…23db5f3；原canonical退出1和失败摘要均保留，未重跑VM。失败客户端本地33/0与来源0/0分别保存，不补造errno/RST或核心wait。
- 进行中：连接缺证明/精确恢复/双入口粘滞拒绝完整场景；事务原语已独审接受，真实Go admission-baseline小包已冻结79文件，编译test/race各10PASS、runtime各14PASS，Python2方法16子测试，独审适合作为固定输入。root另明确T017按源码差异及当前真实集成证据评审，不将旧0017的100/100或恢复/质量矩阵改标为新核心。
- 失败待决策：无新增实际运行失败。executor连续模型容量错误，root接手配置封口和集成；新的窄任务executor可用并实现held客户端。保留首次gofmt准备失败与全部旧运行失败。
- 环境不足未验证：实际BPF原字节NOEXIST恢复、两次完整group stop及五尝试组合仍未执行；不得用离线模块通过替代。客户端最后ACK后的第三notice可能晚于实际close，parent将按原件持有时点与独立终态/回收分开核验，不能要求通知接收时仍持有。T016/T017/T018/P0/P1未放行，140项完成32。root关键语义/实际消费/接受；scout按gpt-6-luna/high独审，executor按gpt-6.1-sol/medium执行可用窄包，无自动模型切换。


### 2026-10-26 五尝试整包接线与最终路由门离线审查

- 已完成：五尝试 guest/parent/事务/全线程暂停接线；进程与 socket 持有回归 10 方法、62 实际子测试通过。宿主 checker 完整消费链及独立审查通过；使用第一版实际镜像的 754 项 pins 和完整 host 元数据，64 线程、256 checkpoint 的合成完整场景仍为 86 项导出、展开 7,669,985 字节，未增加原定上限。上述均为离线验证，实际 VM 样本为 0。
- 已完成：最终路由门 v3 经独审/root 有限接受，固定辅助代码从同一份已校验字节编译执行，回归证明即使存在有效但内容不同的 Python 字节码缓存也不会选用。正式 acceptance manifest 尚未生成，不能据离线 39 VM 合成输入勾选 T016。
- 进行中：冻结前修复 waiting/release 文件未写完即被 exists/read 观察的竞态，以及失败来源证据写入失败可能跳过 lifecycle cleanup 的缺口；同时修复 guest 的局部变量遮蔽 original 封存函数。随后重建镜像、闭包验证、独立预审、root 接受后执行一次真实场景。
- 失败待决策：第一版镜像与测试原件保留，尚未登记或执行真实 VM；独审发现已裁决的清理缺口待修复验收。原历史运行失败及 G01 原分母保持，不重评为通过。
- 环境不足未验证：真实 BPF 删除/NOEXIST 原字节恢复、两轮全线程组停、同一 held socket 与双入口粘滞拒绝仍未验证。T016/T017/T018/P0/P1 未放行，140 项任务完成 32。主代理负责语义与真实运行；executor 为 gpt-6.1-sol/medium，scout 为 gpt-6-luna/high，均可用且没有再次派生。


### 2026-10-26 五尝试真实运行完成，归档消费接口修订

- 已完成：完整准备包冻结 1,775 文件，经独审/root 接受后执行一次 VM。register/host/extract/copy 均 0；原 guest 记录五尝试完成、五客户端退出 0 并回收、原核心 90 由 guest PID1 wait 退出 0/回收及 pidfd 关闭，完整导出 86 项。原 source 纯解析为 13 帧、两条 33/33 终末及 complete epoch_closed，三个 held 尝试为 EOF 且没有来源流/终末/字节。以上待完整消费与原件独审，不作整体接受。
- 进行中：scout 独审首场完整原件；executor 在独立 admission-host-v2 修正归档读取衔接，实际 extractor 输出接完整 consumer 做回归，之后新冻结/预审/root 同原件消费。最终装配工具已实现并以合成输入通过 5 方法/14 子例，只能生成 task=false 草稿；T017 已有证据索引草稿验证 476 个引用，尚无接受裁决。
- 失败待决策：原 canonical 退出 1，在固定 reader 之前要求不存在的 evidence.tar.gz。实际 extractor 从 console envelope 直接提取；synthetic 额外写 tar 掩盖了该差异。root 已裁决仅修宿主消费链，不增加文件让旧检查器通过，不重跑 VM；原 run、失败退出及五尝试分母保留。
- 环境不足未验证：本次 strict/source reader 和完整 canonical 尚未通过，不能据 guest 状态放行。140 项任务完成 32，T016/T017/T018/P0/P1 不变。模型分工仍为 root 语义与实际执行、executor gpt-6.1-sol/medium、scout gpt-6-luna/high。


### 2026-10-26 T016 同原件修订与最终汇总通过

- 已完成：admission-host-v2 冻结 57 文件，12 方法/63 子测试通过；独审后 root 消费同一 VM 原件，新保护副本及 canonical 均退出 0。最终独审核对 248 前置引用，root 固定 264 引用接受五尝试；两条转发、三个接纳前拒绝、13 完整来源帧，未新增 VM。
- 已完成：最终装配核验 5,138 份原件引用，正式清单固定 16 组、39 VM、127 应用尝试、124 来源流、59 epoch、805 必需帧+9 checkpoint=814 帧。Go race 的正式汇总 1 项和输入契约 1 项通过，0 失败/0 跳过。T016 勾选，140 项完成 33。
- 进行中：T017 以原 0017 身份/恢复/质量结果、已审源码差异及当前多入口实际集成分层形成事件源裁决；T018 随后审查 DB-free P1 入口。没有重跑或改标旧 100/100、8 VM/16 槽、6 VM/21 槽。
- 失败待决策：无新增未裁决失败。旧 canonical 缺 tar 的失败保持；新的宿主读取使用原 console 内归档，没有给旧运行添加文件或重新评分。所有历史失败继续保留。
- 环境不足未验证：生产托管/默认部署、跨平台、来源 GC、SQL 和资源预算仍待各自任务；source recording_status unverified/writer_end null 保持。T017/T018/P0/P1 尚未放行。root 负责关键裁决与实际验收，executor gpt-6.1-sol/medium 整理新索引，scout gpt-6-luna/high 独立核对门槛，没有子代理再派生。


### 2026-10-26 T017 同源事件可行性裁决完成

- 已完成：新证据索引校验 5,593 唯一引用，T017 root 裁决接受受限同源事件路线；原 0017 的 G01/G02/G03 实际结果、未变算法来源、当前多入口增量测试/真实集成分别记录，没有搬移旧 hash 或重跑旧矩阵。ADR001 保留原生 recorder 否决全文；T017 勾选，140 项完成 34。
- 进行中：T018 核对控制 helper 的访问保护来源。G07 实际矩阵绑定旧核心 5693…/helper ad4a…；当前多入口 helper 为 1c876…。已有 review 证明 owner-before-credential 和 store/事务未变，正在补 IPC/认证/权限及改变调用点的精确来源记录，由 root 决定是否尚缺受影响检查。
- 失败待决策：无新实验失败；不将“新版未重新运行旧矩阵”本身判为失败，也不据源码相等虚构新的运行。原失败保持原结论。
- 环境不足未验证：二进制大小和 42 typed FD 只是已测事实，十分钟 RSS/CPU、两 owner 增量内存、生产规模/部署、SQL 与平台仍未验证，分别留给后续任务。T018/P0/P1 未放行。executor gpt-6.1-sol/medium 制作保护来源对照，scout gpt-6-luna/high 对稳定 T017 裁决独审，root 负责最终门槛。


### 2026-10-26 T018 核心可行性门槛关闭，进入 DB-free P1

- 已完成：保护来源对照与独审核验 84 引用；root 逐项裁决 5 个语义/范围问题，记录旧 G07 7/11/14 实测、未变实现及当前受影响路径证据之间的关系，没有生成新版矩阵结果。T017 稳定裁决另经独审，12 直接引用匹配且无阻断。
- 已完成：ADR002 与机器裁决明确 G01–G05/G07 最小保护、托管/身份/编译/入口模型和四个外部制品路径/hash；T018 勾选，140 项完成 35。允许 DB-free P1 实现，不表示 P0 整体或 P1 产品完成。
- 进行中：T019 驱动候选准备；T022/T023 定义产品生命周期/应用恢复验收，随后 T024 起实现产品状态存储。生产访问保护由 T025/T034/T060/T063 自身验收，不把原型当交付。
- 失败待决策：无未裁决关键阻断，全部原失败记录保持。当前 helper 未重跑旧整套 G07 是明确的证据范围，不伪造运行，也不自动复制不受影响矩阵。发布脚本首轮误用旧 helper 路径，在写入前失败；按实际 artifact 路径重新固定，无接受文件被覆盖。
- 环境不足未验证：G06/规模/资源、生产默认部署、来源回收/SQL、UDP/QUIC 和其他平台保持未验证；按计划不阻塞无数据库 P1。root 做关键语义与接受；executor gpt-6.1-sol/medium 整理驱动，scout gpt-6-luna/high 独审稳定证据，均未再派生。

### 2026-10-27 T019 双驱动共同事务探针与只读边界修订

- 已完成：26 个固定模块获取/校验及两个薄包装构建；根 go.mod 未增加 SQL。共同临时事务探针覆盖 sample/fact/checkpoint/gap/version、重放与冲突、精确整数、取消、真实 BUSY/页数上限 FULL、三个屏障中断和一个提交竞争。修订后 modernc CGO=0 与 mattn race 各 11 个有效顶层/13 个子测试通过，0 失败/0 跳过；helper 空入口不计覆盖。modernc 受影响只读路径另经定向 race 通过。
- 已完成：原模式复现定位 SHM 单字节改动；独立 raw reader 三条件各一次，正常读成功，WAL/SHM 权限 000 均在查询时返回 CANTOPEN，三个子进程正常回收。当前探针对直接 WAL 读保守返回 READ_UNAVAILABLE，DELETE quiescent 正向只读及 journal/sidecar/缺库策略拒绝保持文件树不变；策略拒绝不当作原生权限通过。modernc 的 macOS/Windows amd64 交叉构建通过，目标运行未执行。
- 进行中：[ADR003](adr/003-sqlite-driver.md)与机器摘要（本地原件 `../experiments/gost-mihomo/sqlite-probe-validation.json`，不随Git上传）记录部分证据，T019 尚未选定生产驱动；T022/T023 开始产品生命周期与恢复断言定义，后续 T024 文件状态存储独立推进。
- 失败待决策：首轮现代驱动 2 个顶层失败保留；WAL/SHM 存在加 mode=ro 不等于零改动，同进程已有映射使原权限测试前提无效。已修订策略和测试边界，无未裁决的新语义阻断；初轮日志/副本和独立复现不覆盖。
- 环境不足未验证：物理 ENOSPC、真正同步 I/O 故障及完整权限/目录边界、目标平台原生运行/mattn 跨平台工具链仍未验证；页数 FULL、隐藏 Commit 成功和交叉构建不替代。140 项完成 35，T019/T020/T021/P0/P1 不标完成。root 负责事务/只读策略与实际验收；executor gpt-6.1-sol/medium 完成获取与独立进程诊断，scout gpt-6-luna/high 审查证据，均未再派生。

### 2026-10-27 T019 真实受限文件系统耗尽与第二驱动权限诊断

- 已完成：两个驱动各在独立 user/mount namespace 的新 4 MiB tmpfs 中写到真实 ENOSPC/Bavail=0；共同事务 Commit 均返回原生 FULL(13)/uncertain。释放空间、关闭重开后完整旧 tuple 保持，同样本单次重试形成完整新 tuple。两驱动各 2 顶层/3 子测试及 race 通过，0 失败/0 跳过，执行约 1.27/1.12 秒且无超时。wrapper 退出卸载，宿主挂载目录为空；此证据不证明块设备断电或同步 I/O 故障。
- 已完成：mattn 原生权限诊断三个独立子进程各一次，正常读取 SHM 10 字节变化、DB/WAL 不变；WAL/SHM 000 在 Conn 阶段返回 CANTOPEN，文件不变，均正常回收。与 modernc 的 Query 阶段分别记录。Commit 错误包装改用 %w 保留原生错误身份，原共同测试继续绑定旧源码副本，新错误链由本次定向测试验证。
- 进行中：T022/T023 产品集成断言定义；root 写明 T024 文件布局/单写入者/持久发布决定，待定义完成后实现。T019 部分证据独审，驱动最终选择仍待剩余门槛。
- 失败待决策：mattn 诊断初次仅编译失败：复制的诊断 helper build tag 只含 modernc。原失败日志保留，未发生数据库运行；新副本只扩展 tag 后执行。预审指出外层 timeout 仅杀直接进程，执行前已加入专用进程组及超时 killpg；两次实际运行都未走超时路径。
- 环境不足未验证：真实同步 I/O 故障、完整目录/权限边界、其他平台原生运行与 mattn 目标工具链、容量资源仍分别待验。140 项完成 35，T019/P0/P1 不标完成。root 执行并裁决，executor gpt-6.1-sol/medium 准备 P1 测试，scout gpt-6-luna/high 复核新证据。

### 2026-10-27 T019 同步调用故障实际验证，P1 文件存储推进

- 已完成：modernc/mattn 各一次 Linux/amd64 子进程 seccomp TSYNC 注入，实际 Commit 返回 SQLITE_IOERR_FSYNC(1034)/primary 10，writer 不确定且拒绝复用；正常退出回收后，父进程重开核验旧聚合 tuple、按原样本重试并重开核验新 tuple。各 1 顶层及 race 通过，0 失败/0 跳过/无超时，独审核对 16 引用。记录 close 实际无错误，不宣称通用保证或物理断电验证。
- 进行中：P1 生命周期定义收紧持久登记、degraded 标记、Stop 恢复结果与唯一 session witness；T024 按新文件存储契约实现纯路径、持久写锁、不可变 bundle 和 active 原子发布。
- 失败待决策：P1 attempt6 编译遇到并行实现的 Paths 类型尚未落地，日志保留；无产品运行样本，不把编译依赖错误当作产品失败。既有运行失败保持。
- 环境不足未验证：T019 完整目录权限/目标平台运行与最终选型，T024 实际故障/权限验收尚待完成；P0/P1 产品仍未完成，140 项暂完成 35。root 负责存储关键语义与实现；executor gpt-6.1-sol/medium 完成测试定义和纯路径；scout gpt-6-luna/high 核对原件及 Linux 边界。

### 2026-10-27 P1 生命周期验收定义关闭，T024 实际验证开始

- 已完成：T022/T023 的 16 个顶层验收定义，经当前 bundle 原件绑定、持久登记、degraded、Stop 恢复、session 单次证据等窄修订及独审/root 接受；编译选择 0 测试并退出 0，选定叶节点在 adapter 未实现处明确 Fatal（退出 1，1 叶/2 失败 test nodes）。实际产品/VM 样本均 0，定义完成不代表 T034 产品运行通过。140 项完成 37。
- 已完成：T024 纯路径实现 4 顶层/71 实际子测试节点通过。Linux 存储实现已就绪，持久 state flock、完整校验 bundle、active 同目录发布、失败后 poison、显式 writer 重开同步、只读拒绝及缺失权威拒绝均待验收。
- 进行中：宿主普通 UID 下的 storage race 测试与稳定源码独审；原沙箱 attempt1 真实失败保留。
- 失败待决策：首轮被 sandbox 的目录 owner 映射 65534 拒绝；宿主只读检查确认 / 与 /tmp 实际 owner 0，代码不为该映射削弱 owner 规则，另开 attempt2-host 实测。该失败未改标通过。
- 环境不足未验证：T024 实际结果待收集，产品进程控制/恢复仍未实现；T019 完整权限及平台/选型继续待验。root 接受定义并实现关键存储；executor gpt-6.1-sol/medium 完成定义/Paths，scout gpt-6-luna/high 审查稳定输入，没有再次派生。

### 2026-10-27 T024 文件存储有限验收完成，进入 T025

- 已完成：T024 Linux 文件存储经稳定源码审查及实际 host race 通过，20 顶层/107 子测试节点（含既有 5 顶层标量），最后 runtime 保留 active.json/.writer.lock 的 4 个新增子例由定向 race 通过。旧hash与新定向证据分开保存。ADR012、文件存储契约及验收/机器摘要已写入，T024 勾选，140 项完成 38。
- 已完成：全部公开源码 internal/protocol/experiments/tests 的 gofmt、vet、普通测试、race 均通过；普通/race 各 303 顶层/1261 子测试节点，19 个需要真实环境的 opt-in 测试跳过另列，2 个无测试包不计用例。没有重跑已冻结 VM 或把跳过当作门槛通过。git diff --check 通过。
- 进行中：T025 原型到产品的进程归属/IPC 接线准备。明确配置 Generation 与每次进程 RunID 分离；exec 前 claim 不等于完成强身份/controller 归属核验后的持久登记。
- 失败待决策：原全仓 go vet ./... 扫描了 Git 忽略的 private 工具链/旧冻结源码，包发现失败；不删除原件或添加无关依赖，改为全部公开源码范围并在开发规范记录，原日志保留。原沙箱 owner 映射失败也保留。
- 环境不足未验证：T024 不包含断电/非Linux/打开同步点注入；产品生命周期未验证。发现当前受保护核心在 controller 可用前已启动 entry，不能据原型启动成功声称“登记+核验后才开放应用”；root 正裁决 T026 显式启动门控，独审整理受影响范围，不更改历史核心通过记录。root 实现/验收，executor gpt-6.1-sol/medium 准备接线，scout gpt-6-luna/high 收集证据，无子代理再派生。


### 2026-10-27 T025 平台归属与运行记录推进

- 已完成：Linux 原 pidfd 进程核验、同一已连接 controller 的归属证明、私有 Unix IPC、完整 argv 与 self 出生观察，以及 intent/birth/registration 的纯类型校验均通过限定 race，独审无阻断。具体分母和空 helper 分列于[平台原语验收](validation/p1-ownership-primitives.md)；controller 两次合法连接通过，同 netns 错进程对端收到 0 应用字节。IPC 并发 Accept 覆盖 deadline 的问题已通过可取消串行队列修正，并验证 Close 后已交付连接继续通信。
- 进行中：运行记录 Store 已接入首次 intent 才返回启动票据、无覆盖 birth、按操作保存登记及逐 run 目录重开同步。最新限定 storage race 9 顶层/28 子测试通过，等待稳定源码独审；出生记录为合成存储输入，没有 exec 核心。runtime 正接 actual 归属与登记，T025 保持未勾选，140 项完成 38。T026 的真实启动门控决定已落 ADR013 和任务描述。
- 失败待决策：无未裁决的关键阻断。IPC fixture mode、沙箱 socket EPERM、过长测试路径，argv 首次编译与 runner 前置失败均保留。run-store runner 最初把 TMPDIR 放在 /data 路径，两个环境都在祖先私有权限检查拒绝，尚未进入事务；改为宿主 /tmp 下自有 0700 临时目录后通过，没有修改权限谓词。
- 环境不足未验证：完整产品 launcher/接管、T026 新核心启动门控、T034 会话/恢复矩阵和跨平台仍待验；不把局部 Go 测试当作 P1 交付。root 负责控制/发布语义与实现，executor gpt-6.1-sol/medium 完成已定方案和测试，scout gpt-6-luna/high 独立核对稳定输入，无子代理再派生。

### 2026-10-27 T025 模块验收完成，进入 T026 控制契约

- 已完成：T025 稳定模块经 root 接受。持久 intent/出生/登记、随机启动身份、原 pidfd/同连接 owner、私有 IPC 及只读观察均有独审与限定 race 证据；10 个真实 Go fixture 子进程同 PID exec，全部正常回收。控制响应为合成协议，没有实际 Mihomo 样本。140 项完成 39。
- 已完成：168 个源码/module 输入前后哈希一致的一轮公开范围 gofmt/vet/test/race/diff 检查。普通/race 各 1824 父 run 节点，1805 pass/19 skip/0 fail；4 空 helper、17 opt-in 跳过与 2 旧 owner-set 环境不足跳过分列，未重跑旧核心/VM 矩阵。详情见运行归属验收。
- 进行中：T026 基础模式控制 v1 已写契约，controller 先可用、应用 socket 保持未 listen；完整核验/登记后认证 activate，部分 Listen 失败终末关闭，历史幂等结果与当前状态分开。新候选单独复制，旧冻结 source/hash 不变；完整身份模式不提升为默认 BPF 依赖。
- 失败待决策：无 T025 未裁决阻断；历史失败仍保留，不将 runner/环境失败改标通过。
- 环境不足未验证：T026 真实核心、T033 服务、T034 产品恢复/注销/休眠、非 Linux 及完整身份模式新 gate 均待验。root 负责协议/锁/事务裁决；executor gpt-6.1-sol/medium 实施与核验，scout gpt-6-luna/high 独立源码证据，无子代理再派生。

### 2026-10-27 T026 基础控制 v1 与第一轮真实转发

- 已完成：控制 v1 纯 wire 已实现；初轮7顶层/127子项通过后，独审指出状态/历史回执可达性过宽。root收紧至36组合中8合法/28拒绝，新定向race7顶层/159子项、vet通过；旧receipt/freeze保留，新revision-02另封。
- 已完成：新候选逐字复制固定旧source后追加独立basic分支，guard/early-main只改新变量分派；已离线构建，当前二进制2921c246…f27fabb。首次真实宿主运行1核心、HTTP CONNECT和SOCKS5各1条流，经两个本地HTTP上游/两逻辑组转发；controller可用时两个app入口仍拒绝connect，认证activate后流量正常，drain后旧流可继续而新连接拒绝，重投历史activate不重开。正常退出0；无BPF/VM，尚未用产品adapter或验证管理器崩溃接管。
- 进行中：核心状态机的native故障/并发/认证矩阵与旧guard定向回归，真实smoke独审；internal/mihomo固定能力/同连接HTTP适配及资产启动链仍待接入。T026保持未勾选，任务39/140，P1未完成。
- 失败待决策：当前无未裁决实现阻断；新变量曾被-v/子命令早退绕过的静态发现已以前置拒绝修复；协议初版允许不可达状态组合的缺口已修复，原证据保留。
- 环境不足未验证：完整身份模式的新gate、产品owner/持久登记→激活组合、T034注销/休眠和跨平台未验。root实现关键状态机与真实本地流量核验；executor gpt-6.1-sol/medium实现纯协议并准备core测试；scout gpt-6-luna/high独立核对源码与证据，无子代理再派生。

### 2026-10-27 T026 核心候选稳定封存与管理器 Inspector 接线

- 已完成：新core gate限定race40顶层/392子节点，430 pass/0 fail/2旧opt-in skip；其中新增7顶层/54子项全过，vet通过、1012输入前后相同。独审无阻断，partial握手窗口0次命中限制保留。
- 已完成：历史T016 delta逐字发布0018，新basic控制增量0019；fresh副本实际apply后1012文件字节与权限一致，离线build重新得到2921c246…f27fabb。有限组件结论和G01–G07受影响范围见[候选验收](validation/p1-core-control-candidate.md)，没有正式CapabilityRecord或P1 fixture替换，T026仍未勾选。
- 进行中：internal/mihomo实现受信固定能力摘要、完整bundle与独立snapshot绑定，经T025原连接的三次只读HTTP观察；首次compile成功（无测试），executor正在补有界响应/取消/完整性与实际固定核心Inspector验收。
- 失败待决策：无未裁决阻断。独审发现外层HTTP reader检查遗漏共享bufio预读尾部，root已补双层检查；回归测试待执行，不提前记通过。
- 环境不足未验证：产品同连接认证组合、持久登记后activate、launcher/管理器恢复与T034仍未验；同步ReadBundle不承诺被context抢占。此阶段不改变任务39/140与P0/P1状态。

### 2026-10-27 T026 Inspector 验证与登记后写操作接入

- 已完成：Inspector 限定 race 9 顶层/60 子节点，68 pass/1 默认真实核心 opt-in skip；另一次固定 2921c246…f27fabb 核心实际运行，1 顶层通过，原 pidfd/完整 argv/nonce/同连接核验后观察 closed，两 app connect 被拒，仅读 Store 树不变，TERM+Wait 退出0。vet/format 通过，38 Go/module 输入另存原字节，74 证据引用冻结。没有生成 birth/registration/activate 或恢复样本。
- 进行中：root 接入 ReadControlRegistration 和原 OwnedRun 的同连接作用域，将已有持久登记摘要绑定到 Activate/Drain；分离历史 Receipt 与 Current，至多一次 POST，失败不自动重试。三包编译选择0测试通过；executor 正验证受影响 Store/runtime/HTTP 及合成组合，未提前记通过。离线固定资产的安全 exec/独立 session 启动链在准备。
- 失败待决策：diagnostic-02 实测发现非分块 Trailer 声明及重复 Content-Encoding 可漏检，root 已改为拒绝全部 header presence，最终限定测试通过；原失败源码/日志保存。更早 unused import 编译失败保留。当前没有未裁决的新阻断。
- 环境不足未验证：持久登记→真实核心激活组合、产品 launcher、服务恢复/注销/休眠和其他平台仍待验。T026 不勾选，任务39/140，P0/P1未整体交付。executor gpt-6.1-sol/medium 完成测试，scout gpt-6-luna/high 独审冻结证据，root负责写操作语义和验收。

### 2026-10-27 T026 登记后控制组件验收，离线启动链验证开始

- 已完成：root 接受 Inspector 与已登记写操作组件有限证据，见[控制适配验收](validation/p1-core-controller.md)。mutation 最终受影响 race 15顶层/84子节点，98 pass/0 fail/1明确core skip，两个空helper另列；3个实际自有Go fixture child均正常Wait，控制语义合成，没有真实Mihomo写操作样本。独审核对180冻结/181引用/43输入。
- 进行中：固定离线 static ELF 的同FD核验→procfd exec、私有config/workdir、独立session/file stdio、自有child Wait和同PID出生链已实现，四包compile选择0测试通过，稳定源码独审无阻断；executor准备平台拒绝/真实FD exec及固定Mihomo全链组合。当前T026仍不勾选。
- 失败待决策：首轮runtime取消测试超时早于完整owner proof，已在callback实际进入后触发取消并通过，没有放宽生产守卫。原失败与其3个历史child保留、不加入最终分母。无新增未决生产问题。
- 环境不足未验证：T033固定内部CLI分派尚未接入，T029写入者串行化、T030以后恢复/发布和T034完整生命周期仍待验。任务39/140，P0/P1未整体交付；当前代码只接受受限Linux/amd64基础模式，不承诺来源身份/计量或任意代理配置。

### 2026-10-27 T026 完成，进入持久操作日志

- 已完成：root 接受[基础核心与离线启动组合](validation/p1-basic-control.md)，批准固定2921c246…fabb的有限basic能力。一个真实核心完成出生/同PID exec/闭态核验/登记/activate/真实echo/Store重开/drain/原pidfd TERM/Wait0；两个独立完整基础配置另以4条HTTP/SOCKS流验证规则顺序。旧identity fixture保持原hash和全部规则，P2身份gate由T138/T077承担。
- 已完成：统一检查192输入前后相同；test/race各410顶层/1722子节点、2111 pass/21 skip/0 fail，8空helper单列；gofmt/vet/空白通过。实际核心样本不加入该默认测试分母。T026勾选，140项完成40。
- 进行中：T027契约已固定不透明Unicode请求ID、完整JCS摘要、最新持久结果重放、终态tombstone和有界身份容量；root开始CAS文件存储和控制日志，executor实现独立规范化原语。T028 Plan/后续协调恢复尚未实现。
- 失败待决策：launcher首轮退出结果复制竞争已修复且保留原断言；规定检查首轮仅新文件格式未规整，修正后完整通过，旧失败原件保留。当前无未裁决阻断。
- 环境不足未验证：管理器死亡/恢复产品矩阵、生产来源身份/计量、非Linux、性能预算及完整P0/P1仍未通过。executor实际gpt-6.1-sol/medium，scout实际gpt-6-luna/high，root负责关键语义与最终验收；无子代理再派生。

### 2026-10-27 T027 日志实现与两项完整性修复

- 已完成：共享JCS原语限定race96节点通过、0失败/跳过；新增文件CAS、完整请求摘要、最新持久结果重放、终态tombstone与有界身份容量。具体边界见[日志验证记录](validation/p1-operation-journal.md)。
- 进行中：executor补充大小/步骤预算、只读/权限和poison边界，随后对config/control/runtime/mihomo稳定输入做定向检查。T028只读证据及固定输入/完整Intent编译的边界已整理，尚未实现Plan。
- 失败待决策：独审发现已有state可重建丢失持久目录，已改为拒绝并加四类零重建回归；首轮2个compaction失败定位RawMessage显式null，修复后66节点通过。进一步将精确序列化自检提前到CAS前，待最终复验。更早仅测试ID类型转换编译错误保留。当前无未裁决阻断，失败原件不删除。
- 环境不足未验证：产品管理器死亡/日志与active/核心共同恢复、原生磁盘故障和跨平台仍待对应阶段验收；T027不勾选，任务40/140。root关键语义，executor gpt-6.1-sol/medium、scout gpt-6-luna/high，各自限定范围，无再派生。

### 2026-10-27 T027 最终版本验收，继续 T028

- 已完成：持久日志模块通过 root 最终验收；独立引用/源码核对无阻断，T027 勾选，任务 41/140。最终分版本矩阵 544 pass/2 skip/0 fail、四个空 helper，明确不是单次四包全绿，详见[验收记录](validation/p1-operation-journal.md)。
- 进行中：T028 严格 Plan 信封、纯 Intent 验证器接口、完整输入/JCS 摘要绑定；完整来源/规则编译仍由 T037–T041 提供。T029 当前输入与中断确认的串行接纳边界由 root 设计。
- 失败待决策：RawMessage 二次转义的真实失败已修复为内部精确字节封装，并通过原断言及字面反斜杠用例；旧失败与源文件保留。当前无未裁决阻断。
- 环境不足未验证：真实核心 opt-in 两项未运行，管理器死亡恢复、原生磁盘故障、跨平台和产品 P0/P1 未通过；Journal poison 投影仅源码核对，故障注入发生在 Store。executor 实际 gpt-6.1-sol/medium，scout 实际 gpt-6-luna/high，root 决定持久语义并验收，无再派生。

### 2026-10-27 T028 组件验收，T029 接纳层实现

- 已完成：T028 严格信封/必需纯语义验证接口/完整摘要绑定通过限定验收，定向 102 pass、完整 control 124 pass，均无失败/跳过；82 个冻结引用独立核对通过。T028 勾选，任务 42/140，见[验收记录](validation/p1-plan-envelope.md)。
- 进行中：root 确定 T029 独占 Store 生命周期和跨 API 控制锁范围，并实现先重放后新请求校验；executor 以真实私有 Store 测试过期/许可/来源/历史阻断、并发和 Close 锁生命周期。T030 核心效果未实现。
- 失败待决策：T028 初版测试误读 private fixture，已改公开 Node 向量并重新验证；原历史保留。T029 tests-first 的缺 API 编译失败属预期。无未裁决阻断。
- 环境不足未验证：完整 Intent/provider/rule 编译、实际应用与恢复矩阵、非 Linux 和资源目标仍未通过。Plan 无进程/网络效果由源码和有限 XDG/env 检查支持，未宣称实际核心测试。executor gpt-6.1-sol/medium、scout gpt-6-luna/high，root 关键决策和验收，无再派生。

### 2026-10-27 T029 接纳模块验收，继续 T030

- 已完成：T029 独占 writer/跨 API 串行/先重放/全输入与独立影响复核通过限定验收；最终 control race 191 pass，定向 67 pass，均无失败/跳过，90 个冻结引用独立核对一致。任务 43/140，见[验收记录](validation/p1-coordinator-admission.md)。
- 进行中：T030 完整替换所需“现有登记+新鲜连接身份→原 pidfd TERM→原 pidfd 退出观察”模块由 executor 实现；root 设计 run 发现、操作关联和发布顺序。T030 未勾选。
- 失败待决策：接纳层审查发现自报影响可绕过中断许可、值复制可复制 mutex，两项已修复并由最终版本验证。早期过时正例、分类错误与原件保留；当前无未裁决阻断。
- 环境不足未验证：接纳使用真实 Store/flock 与合成独立输入，不证明真实核心效果、完整编译器、产品恢复或跨平台。协调层容量未独立重测，沿用 T027 原语及调用路径证据。executor gpt-6.1-sol/medium，scout gpt-6-luna/high，root 关键判断及验收，无再派生。

### 2026-10-27 T030 支撑能力验收与完整替换接线

- 已完成：运行枚举与原 pidfd 终止的有限验收；config race 401 pass，runtime race 57 pass，均无失败/跳过，2 个空 helper 单列。见[支撑验收](validation/p1-apply-support.md)。主代理完成关键顺序决策，executor gpt-6.1-sol / medium 实现终止，scout gpt-6-luna / high 核对证据。
- 进行中：T030 BasicApplier、真实 basic runtime 适配、Plan→固定原件材料桥以及操作容量/staging 预检；完整流程测试正在实施，T030 不勾选，仍为 43/140。新增[基础替换契约](../specs/001-hoopa-proxy-manager/contracts/basic-apply-v1.md)。
- 失败待决策：没有待用户裁决项。旧注册测试缺规范换行和目录符号链接见证已修正，历史记录保留其有限范围；早期材料测试遇并行新增接口尚未落盘，0 测试执行，不计通过。
- 环境不足未验证：本次支撑测试没有运行 Mihomo；完整实际 start/replace、回滚、进程中断恢复与 CLI 旅程仍需后续集成验收，不能借用组件通过。

### 2026-10-27 T030 实际首启与同端口替换通过，继续恢复实现

- 已完成：公开 BasicApplier 的实际固定核心首启→同端口 replace→原 pidfd 退出观察；2 次授权本地 HTTP echo、1 次 407；同请求零重复启动。最终 control/config race 680 pass、0 fail/skip，实际专项另计 1 场景+1 空 helper。见[部分验收](validation/p1-basic-apply.md)。
- 进行中：T030/T031 失败候选终止、完整旧态回滚与显式恢复。当前仅 pristine start/replace 成功及失败保留完成，T030/T031 未勾选，仍 43/140。
- 失败待决策：没有待用户决策项。审查发现首启缺操作历史限制，已修正并回归；prepared 证据空窗口明确为无运行效果例外。下一关键决策是 activation 失败/degraded 核心的受限终止证明，不放宽正常接管或新启动条件。
- 环境不足未验证：本次没有 VM/BPF/公网质量/多应用故障矩阵；失去原 pidfd 后的停止证明、真实回滚/发布失败恢复、生产 CLI 仍未验证。


### 2026-10-27 T030/T031 自动回滚通过，显式恢复进入验证

- 已完成：固定核心 failed/degraded 候选终止真实组合、旧/目标/恢复三实例自动回滚、独立退出观察和旧原件保留；control/config 回归 744 pass/1 明确材料 skip，独立实际专项 1 场景+1 空 helper。新工作目录 33 pass、显式操作临时残留维护 26 pass，见[分版本验收](validation/p1-basic-rollback.md)。
- 进行中：显式 Recover 原操作/调用操作分别持久、同请求只重放、新调用再次核验同实例、缺原件保持 degraded。旧退出事实增加 intent/birth/原注册的完整重核，真实重开测试正在准备。任务暂仍 43/140。
- 失败待决策：没有待用户裁决项。真实回滚首轮夹具凭据重复被安全拒绝，修独立凭据后通过；旧失败候选 PID/清理证据缺口保留。完整 Response schema 核对发现失败响应须 data:null，已修代码，定向回归待执行；不改写历史通过版本。
- 环境不足未验证：目前没有实际 SIGKILL 管理器矩阵、原生断电/磁盘同步故障、非 Linux 或生产 CLI。root 负责关键恢复顺序，executor 实际 gpt-6.1-sol / medium 做实现与测试，scout 实际 gpt-6-luna / high 独立核对；无子代理再派生。


### 2026-10-27 T030模块验收，T031实际OS死亡补验

此条是补验前的阶段快照；当前状态见下文最终补验条目，原过程记录保留。

- 已完成：T030完整替换模块；最终四包race1037pass/0fail/skip、vet、81文件格式/空白检查通过，冻结输入未变，任务44/140。[显式恢复](validation/p1-explicit-recovery.md)3场景/5固定core、42个定向合成节点及维护26节点均已有限验证。失败API响应data:null修正由最终版本回归覆盖。
- 进行中：T031真正独立管理器进程SIGKILL后原核心继续转发及fresh-owner恢复。现有实际证据是同测试父进程内Coordinator/Store Close/reopen，不把它冒充进程死亡；此处补一个实际样本，完整各阶段故障矩阵仍属T034。
- 失败待决策：无待用户裁决项。独审指出仅凭OldStopped不足，已加入old intent/birth/current-op open registration与rollback候选closed registration原件Match和唯一关联，缺失/错配反例通过。历史compile类型错误、早版29pass局部源码重建限制均保留。
- 环境不足未验证：产品CLI、全SIGKILL边界/真实断电、SSH注销/休眠与跨平台仍待对应阶段。本轮实际测试无VM/BPF，不宣称独立child.Wait退出码。root验收关键语义；executor gpt-6.1-sol/medium，scout gpt-6-luna/high，无再派生。


### 2026-10-27 T031模块完成，真实SIGKILL补验冻结

- 已完成：T031 自动回滚、degraded、显式操作残留维护、独立恢复调用与原目标分别持久、两记录中断及已开放实例恢复已按各自证据验收；最终 manager-crash-04 证明实际管理器 PID 1959989 被 SIGKILL 后，原核心 PID 1959999 继续三次本地转发中的后两次，新 Coordinator 取得锁并公开 Recover 完成原操作及调用，零新增运行。T031 勾选，45/140；见[完整边界](validation/p1-manager-crash-recovery.md)。实际 race 3 顶层 pass＝1 场景＋2 空 dispatch，0 fail/skip，vet 0。此前 1037-pass 回归的 97 项输入均未变，只新增该测试；分母不合并。
- 进行中：T032 运行意图、显式停止、停止后重新启动和有界授权重启。已核对 control/runtime 依赖方向、active 发布操作与 RunIntent 的绑定差异、stop-before-TERM 持久顺序和中断恢复需求，实施决策草案留于私有准备记录；尚无 T032 实现通过声明。T033 服务/CLI 和 T034 产品矩阵仍未完成。
- 失败待决策：无待用户决策项。manager-crash-01 的继承 pipe deadline 失败已修复并留证；02 的原件归档边界、03 的父 File.Fd 阻塞模式问题均保留。04 改 SyscallConn.Control，并在 spawn 前实测同 pipe 的短 deadline 返回超时/0 字节后重置。实际核心用原 pidfd TERM/WaitExited，再精确 Wait4 返回 exit 0、确认 proc 消失，测试 subreaper 0→1→0。没有用失败或旧版本替代最终断言。
- 环境不足未验证：本轮没有原生断电、其他平台或新的 SSH/logout/休眠环境验收；这些结果不能由单场进程死亡推导。存量长连接跨 SIGKILL 连续性和全部应用/回滚边界也不在本样本内，留给 T034。root 决定恢复语义并验收；executor 实际 gpt-6.1-sol/medium，scout 实际 gpt-6-luna/high，无再派生。scout 对 195 项冻结引用核对一致，最终报告 `private/p1-apply-preparation/manager-crash-recovery-final-evidence.md` 为 7730 B、SHA256 `53d26b77e401ed488acdafebaf052f0b6b0453f5362e2476d58930206fd414b4`。


### 2026-10-27 T032有限模块验收，进入T033服务接线

- 已完成：[运行生命周期模块](validation/p1-runtime-lifecycle.md)实现显式停止、停止后重新启动、既有closed runtime.start恢复和一次授权重启。三实际场景、六次固定core exec、九次本地echo；Stop/Start与Restart分版本封存。最终control/runtime race480pass（76top/404sub，0fail/skip）、vet退出0、46文件格式/空白通过。独立最终审查无阻断，root接受T032，46/140；不改写各子包“待root接受”的历史状态。
- 进行中：T033服务/CLI接线，先裁定遗留IPC端点、service.stop持久响应/退出顺序、已有running实例的start接管，以及固定Cobra依赖。T034完整产品矩阵保持未勾选。
- 失败待决策：本轮没有未裁决阻断。阶段审查发现生命周期正文可被压缩、重复授权可重置预算，分别以禁止生命周期正文压缩、共享Coordinator单次授权修复；编译、测试分类/错误原件名、相对核心路径等历史失败保留。RunIntent已持久但TargetRunID未持久、出生或退出未证窗口明确degraded，不冒称恢复成功。
- 环境不足未验证：本轮宿主Linux/amd64实际样本不代表VM/BPF、CLI服务、会话/休眠、断电或跨平台；完整产品故障矩阵仍待T034。root负责事务/锁/恢复及最终接受，executor实际gpt-6.1-sol/medium，scout实际gpt-6-luna/high，无再派生。最终审查SHA256 b8a0e112e8f47c5d6d1ee1c5674426bb76ce981acf8186f4c421170f2a9aec27；三份冻结分别80/101/51条pin独审匹配，计数含各自回执/源文件，不视作运行样本。


### 2026-10-27 T033固定依赖及有界IPC组件通过，继续运行接管分支

- 已完成：[服务准备与传输组件](validation/p1-service-preparation.md)经root有限接受。官方Cobra1.10.2/选定pflag1.0.9/mousetrap1.1.0已独立取得，TLS/sumdb开启，离线verify/build及1top3sub参数解析通过，根模块未改。Wire真实Unix socketpair首轮11top36sub通过，补强复验5top17sub通过，0fail/skip、vet/格式通过，生产源码未变；两轮不相加，未声明最终12top整组单次运行。
- 进行中：普通start已运行实例的持久接管控制分支与测试；新服务端点/就绪、service.stop原服务身份及退出协作、CLI和T034适配器仍待接入。当前46/140，T033未勾。
- 失败待决策：没有wire阻断。草案曾未明确服务stop自己的handler不能被后台worker取消一并中断，root已明确保留其有界上下文/回执连接；尚未声称服务实现验证。依赖准备VCS状态检查与Python工具兼容失败保留。
- 环境不足未验证：wire与依赖检查没有核心/VM/BPF样本，不证明IPC认证、服务死亡/重开或实际命令行为。executor实际gpt-6.1-sol/medium，scout实际gpt-6-luna/high独审，无再派生；root验收机器索引为p1-service-preparation-validation.json。


### 2026-10-27 T033已运行实例接管模块有限验证

- 已完成：[EnsureStarted接管分支](validation/p1-managed-start.md)按原实例完整证明持久记录普通start请求，保持原active/revision/generation/RunID和出生/注册不变；stopped分支回到原Start重新检查。真实固定核心一场（PID2569153/start41257745）接管、writer重开后公开Recover与精确重放保持同一实例，Source1次/echo4次，36份私有原件归档，原pidfd退出及proc消失清理通过。最终V2 control race436pass（61top375sub，0fail/skip）、vet、四文件格式检查通过；独审75引用匹配，无阻断。仍46/140，不把该子模块当T033全完成。
- 进行中：T033管理服务/端点发现/启动就绪、service.stop原服务身份与丢响应重放、Cobra命令接入；T034实际产品适配器和故障矩阵随后完成。执行决策保存在private/p1-service-preparation/decisions.md，传输与已运行接管契约已公开。
- 失败待决策：没有新未裁决阻断。root审查发现无效adoption证据时原目标可能保持pending而仅恢复调用degraded，新增reconcileAdoption保留原raw并明确两者状态；V2独立1top4sub通过，终态原目标保持不变。实际V1正常路径与V2解码失败检查分列，不重跑或改标实际样本；V1源文件事后精确重建且SHA匹配，明确不冒称执行前副本。
- 环境不足未验证：真实样本只是writer Close/reopen，不是本轮manager OS死亡、VM/BPF或产品CLI；service.stop/服务进程及T034尚待实际验证。root实施并验收关键事务，executor实际gpt-6.1-sol/medium执行限定测试，scout实际gpt-6-luna/high独审，无再派生。最终审查SHA256 9ebf646545b499af020db243689d54808208708f6b53d3b38f88ccf06046c2f7。T032索引的15个源码引用改指向原已冻结输入副本、原SHA保持，以免后续T033修订被错误当作T032实测源码。


### 2026-10-27 T033实际管理服务与CLI有限交付

- 已完成：[服务/CLI验收](validation/p1-service-lifecycle.md)。Cobra固定依赖实际接入；私有发现、实例握手、就绪、持久service.stop、跨实例/离线精确重放及只读查询已实现。两实际场景/四core/九echo分别验证普通接管与显式停止/再启、服务退出转发继续及单次重启预算；最终race633pass/0fail/1skip，114顶层/520子项，vet通过，22形状正例/3反例，134冻结源码/152证据引用独审一致。T033勾选，47/140。
- 进行中：T034完整产品harness与生命周期故障矩阵。当前 `tests/integration/p1_harness_test.go` 的构造器仍明确失败，不能把本轮模块场景当作该矩阵通过；需接真实产品调用、原件采集和独立会话/故障注入，保持完整bundle校验。
- 失败待决策：本任务无未解决项；actual01–04的夹具/离线状态预期/原因投影失败和并行测试漂移均保留。status有pending核心操作时保留unknown；离线缺管理writer只能显示activation_requires_management，不为查询取得写入权。跳过项是该轮未传可选核心变量的TestBasicMaterialActualApprovedBinary，不计通过。
- 环境不足未验证：本轮未覆盖实际SSH/logout/休眠、其他平台运行、断电与性能预算；已有旧VM会话样本仍只属当时制品。Windows只构建unsupported入口。临时盘ENOSPC以清理/迁移本轮可重建缓存恢复，未降低权限检查。root负责关键语义与验收，executor实际gpt-6.1-sol/medium、scout实际gpt-6-luna/high，无再派生；最终独审见private/p1-service-review/final-acceptance.md。P1与整体项目未完成。


### 2026-10-27 T034运输seam与终态恢复接纳前核验

- 已完成：[准备验收](validation/p1-integration-preparation.md)。真实CLI子进程运输保持原响应、20秒/65537字节stdout/4096字节stderr界限，只保存验证成功的0600原件；6顶层20子项普通/race与tagged vet通过，两个实际readonly错误原件均未创建状态。终态恢复对既有归属冲突在staging维护/接纳前拒绝，接纳后仍重复核验；合成顺序7顶层/race通过，实际缺注册原件两core四echo通过。最终control507pass/0fail/1skip，未变service102/CLI55由同生产版本前轮分别通过，vet通过；各范围不重复相加。
- 进行中：T034真实完整双入口fixture转换与productHarness、原调用/信号/故障/会话证据。构造器仍明确fatal，完整矩阵零行被本次helper冒充通过；F03将明确绑定既有终态start操作，首次启动与pristine控制场景需分开准备。
- 失败待决策：本轮已发现的F14前置归属冲突仍产生新记录问题已修正。保留旧四个拒绝场景的预期失败、实际测试编译错误/源码漂移、旧证明次数断言失败；仅修订“已终结目标”次数为接纳前后共四次，保留全部结果与原件断言。
- 环境不足未验证：实际缺注册补验只是F14相关一条路径，不等于完整PID复用/外部listener/缺nonce矩阵。SSH/logout/休眠、其他平台与性能继续未验证。原T033冻结结果保留；运输阶段旧recover.go按已记录SHA从T033归档精确重建，明确为事后封存。root负责事务/锁/裁决与实际场景，executor实际gpt-6.1-sol/medium，scout实际gpt-6-luna/high独审，无再派生。T034未勾，47/140。


### 2026-10-27 T034固定双入口实际连续性通过，继续故障事件接线

- 已完成：[有限转换及实际连续性](validation/p1-finite-dual-entry.md)。固定两个完整配置/独立snapshot保持原字节；loader27节点普通/race通过。最终actual-04与race-04各2顶层pass（1实际场景+1空dispatch）、0fail/skip，分别18.81/25.34秒；各3core/2Source/9echo。真实管理器SIGKILL后held/new均继续，服务重开保持完整持久文件，显式Recover不改原start收据/核心/revision；完整替换后双入口由实际node A转node B。三个core退出/proc消失，孤儿精确Wait4回收，无测试清理信号。
- 进行中：T034完整productHarness接线、真实launch/signal/workflow事件时刻记录，以及跨SIGKILL的故障命中原件；factory仍明确未实现，不用局部通过替代矩阵。仍47/140，T034未勾、P1及整体项目未完成。
- 失败待决策：无待用户授权或裁决项。旧系统python语法失败在启动前发生；首轮vet报告测试String/GoString复制锁，改指针接收器后tagged vet通过，旧失败保留。actual-01/02/03和race-03属于阶段记录，不累计成额外覆盖。
- 环境不足未验证：本次host Linux/amd64 loopback TCP，不代表SSH/logout/休眠、断电、跨平台、正式计量及预算。race覆盖integration监督进程/BasicApplier/网络辅助，产品CLI与固定核心未加race。root负责关键语义及最终接受，executor实际gpt-6.1-sol/medium实现loader并测试，scout实际gpt-6-luna/high独立核对；没有子代理再派生。

T034 后续接线发现：旧 `assertOrdering` 无条件要求 TargetRevision 非空且不同于 BaseRevision，并要求所有 committed 操作发布 active。接管/恢复/service.stop 不创建配置版本，授权 restart 保留版本但发布新运行权威；必须按原始操作种类分别检查。此处已列入下一工作包，未改冻结测试源、未声称完成修订，也不会靠漏报操作或伪造版本让矩阵通过。


### 2026-10-27 T034进程外故障见证继续推进

- 已完成：[V1实际五场景与见证](validation/p1-traced-faults.md)：普通/race各7测试节点、8core/4次manager SIGKILL，实际入口绑定冲突回滚及显式恢复均通过，无cleanup补信号。顺序策略保留nullable revision、完整历史操作及真实历史目标原文；默认六包普通/race各1320pass/12可选真实core跳过、vet通过。
- 进行中：完整productHarness、更多文件/运行边界与一次重启预算trace。restart源退出观察增量按新源码另验，不改标V1已冻结样本。47/140，T034未勾。
- 失败待决策：没有需用户授权或决策的阻塞；保留actual-01的CLI错误封装预期失败及两个无效TMPDIR runner结果，未放宽安全路径。临时盘ENOSPC在新重启race场景启动前发生，已保留失败并只清理经核实的旧Hoopa构建缓存后继续。
- 环境不足未验证：SSH/logout/休眠、断电、跨平台、资源预算；本轮loopback TCP的payload是线路见证，不是正式计量。root负责关键语义，executor实际gpt-6.1-sol/medium，scout实际gpt-6-luna/high，无再派生。

V2重启增量状态：普通实际场景通过；race-01在启动前遇到ENOSPC，已只清理旧Hoopa Go缓存解除。race-02新start在active发布后核验失败→degraded，尚无底层根因；新增仅测试编译启用的固定stage诊断后race-03通过，未据此宣称修复，正在定向复现。此失败由root诊断，不要求用户继续授权。

V2定向复现按预登记最多三轮执行：race-04/05通过，race-06在核心仍verified-running时watch提前inactive。新增独立原生pidfd自进程实验（普通/race各一次、3秒）确认timeout0 poll可因SIGURG返回EINTR；生产primitive此前直接传播该错误。root裁定仅对同一pidfd有界重试EINTR（最多16次并检查原ctx），其它错误/未知事件继续拒绝。修复与回归进行中；历史两次失败尚无errno证据，不将机制验证冒称为它们的已证根因。

V2阶段验收：[六实际场景](validation/p1-traced-faults.md)在修复后的同一冻结源码普通/race各8PASS节点、11核心、无cleanup信号；169输入一致。独立scout消息审核6405/6396原件引用和恢复/重启/清理关系，未另写文件，由root原样摘要登记v2-review-record.json。当前无新实现失败待裁决，历史无法完整归因的两个失败仍保留。完整adapter与剩余矩阵继续，T034仍未完成。

- 2026-10-27 T034只读接线增量：[七操作真实回执与owner观察](validation/p1-traced-faults.md)普通/race各1场景、2core、7操作/7原始回执、2新鲜owner收据、4组只读树快照一致，原pidfd退出且无cleanup补信号；172冻结输入、每轮1625证据引用独审一致。bundle/operation组件18节点、owner组件7节点分别normal/race通过，taggedvet通过；新鲜Observe不冒称完整managed activation。完整历史读取另完成473事件/47真实历史日志/7操作的离线关联；候选与rollback运行角色的顺序策略修订及完整factory仍进行中，长连接真实入口归属接线并行。已完成：该有限接线场景；进行中：上述适配器/矩阵；失败待决策：无须用户决定项，旧失败保留；环境不足未验证：实际SSH/logout/休眠及其他平台。T034未勾，47/140。

- 2026-10-27 T034统一适配器继续：[流量/回执/容量组件](validation/p1-traced-faults.md)各自冻结版本普通/race分别10/95/40 PASS、vet通过。流量保留原连接和实际节点见证，回执区分独立调用ID、wire结果与目标/恢复调用逻辑结果，容量拒绝允许旧ID精确回放。第一轮真实统一F02管理器SIGKILL后同核心held/new流量通过（19.38秒，181输入未变），发现并加固测试KillManager对已提前退出进程的误报路径；后续组合验证进行中，此单轮不替代完整验收。已完成：上述有限组件；进行中：统一factory和剩余故障矩阵；失败待决策：无用户决策项，历史失败保留；环境不足未验证：实际SSH/logout/休眠及其他平台。T034未勾，47/140。

- 2026-10-27 T034统一factory四场景：[普通/race各4PASS](validation/p1-traced-faults.md)，同184冻结输入，11core均原pidfd退出/proc消失/无cleanup补信号，各5528唯一原件独审一致，vet通过。覆盖真实入口冲突回滚、授权一次重启、Stop落盘崩溃恢复、无授权不重启。旧Apply退出状态仅执行时核验的归档限制已说明，新版增加独立wait/stdout回执。已完成：该四场景；进行中：六个F11发布切点及其余矩阵；失败待决策：publication01候选分支误要求RecoverCall已修订并复验中，其余五分支通过但整轮失败保留，无用户决策项；环境不足未验证：真实会话/休眠和其他平台。T034未勾，47/140。

- 2026-10-27 T034发布/启动窗口增量：[六发布切点普通/race各7PASS](validation/p1-traced-faults.md)，各18实际核心全退出，degraded夹具收尾各2次原pidfd信号如实记录；四active I/O切点普通5PASS、两启动窗口普通3PASS，race待补。prelaunch只声明未调用Start，postlaunch独立closed-owner/birth证明后显式恢复同一实例，均不造注册/退出事实。已完成：上述限定批次；进行中：runtime切点复验、回滚故障和其他矩阵；失败待决策：两个脚本收据错误已修正并保留，entry-opened证明角色已定向修正、实际复验中，无人工决策阻塞；环境不足未验证：真实会话/休眠及其他平台。T034未勾，47/140。

- 2026-10-27 T034运行时/回滚增量：十运行时切点普通11PASS、F13两回滚故障普通3PASS、degraded发布普通2PASS，各自冻结输入未变，race待补。已完成：上述限定真实批次与容量元数据来源/顺序组件（normal/race各56PASS、vet通过）；进行中：C01真实容量重放、其余race、归属/可选依赖/会话矩阵；失败待决策：无人工决策项，历史失败保留；环境不足未验证：真实会话/休眠及其他平台。回滚持久化失败保留rolling_back/pending，不能伪造degraded落盘。T034未勾，47/140。证据见 docs/validation/p1-traced-faults.md 与增量机器索引。

- 2026-10-28 T034继续：四I/O、两启动窗口、两回滚失败、外来监听器分别补齐冻结版本race；容量normal02真实6PASS，race02在300秒fixture预算结束前未启动末尾replay worker，失败保留、扩大测试预算后复验中。F14三个元数据场景normal4PASS，外来监听器normal/race各2PASS并证明子进程仍活；不冒称实际PID复用。F18固定build/launch依赖边界normal1PASS。实际专用VM S3休眠2PASS，197pins、1402非空原件独审一致，3payload交换同Run/原长连接、2core全退出无清理补信号；不含SSH/logout/OSshutdown。已完成：上述有限批次；进行中：剩余race、SSH/logout真实拓扑、实际PID复用与最终稳定审查；失败待决策：无人工决策阻塞，历史失败保留；环境不足未验证：其他平台，宿主KVM已确认且S3实测通过。T034未勾，47/140，见增量与会话机器索引。

- 2026-10-28 T034增量：已完成运行时十切点/degraded发布/F18/C01各自冻结版本race，11/2/1/6 PASS；C01 normal195输入与race197输入分列，原timeout失败保留。当前产品真实PID复用VM 2PASS，PID132/start1352→2066，Recover拒绝且新监听者仍活；197pins/1251原件独审通过，host PID namespace未改。进行中：F14元数据race、SSH/logout真实拓扑与最终稳定矩阵审查。失败待决策：无人工决策阻塞，历史失败不改标。环境不足未验证：其他平台与性能预算；Linux KVM可用，S3/PID复用均已实际验收。主代理负责锁/恢复语义；executor实际gpt-6.1-sol/medium、scout实际gpt-6-luna/high，无再派生。T034仍未勾，47/140。

- 2026-10-28 补记：F14 三个元数据场景 race 4PASS、六实际核心原 pidfd 退出/proc 消失；对应已启动批次的 race 已完成。SSH/logout 与完整覆盖/稳定审查仍进行中，T034未勾；无新增人工阻塞。

- 2026-10-28 生命周期补齐：F01前端两种退出、F03显式恢复及control-only空/运行状态，同197冻结输入normal/race各7PASS、九core全退出且无清理补信号。该批不含F02，下一步SSH/logout实际VM与稳定共享路径F02检查。T034未勾，47/140；无人工阻塞。

- 2026-10-28 SSH首轮保留失败：两个真实会话事件/原manager pidfd退出均完成；F04旧断言将公开只读unknown误解为独立Core必须不可观测，故两个子项及父项失败。root仅修订断言，仍保留activation_requires_management，不提升管理权威；须证明原完整身份/owner/ready并完成held/new转发，且全分支noMutation。201pin新快照仅lifecycle_test.go改变，实际复验中。该首轮只有断开前payload，不计连续性通过；无人工阻塞。

- 2026-10-28 T034完成：所需F01/F02/F03/F04/F06/F10/F11/F12/F13/F14/F18/C01及补充窗口场景均有接受的真实批次。SSH02普通VM 3PASS、201pins、2875导出/2734非空原件，七空引用仍不可用；4core全退出无补信号，6次4096B实际流量；checker16反例/旧失败拒绝、3834原文件未变、独审通过。最终201输入与live相同、taggedvet通过；S3新增无修改要求由既有原件离线五维比较验证，非重跑。已完成：[US5汇总](validation/us5-lifecycle.md)，T034勾选，48/140；进行中：T035/T036测试定义。失败待决策：无，旧失败均保留；环境不足未验证：其他平台、PAM/logind差异、断电与性能。分批版本不合并声称一次全树执行，P1/项目未完成。root负责语义与最终验收，executor实际gpt-6.1-sol/medium、scout实际gpt-6-luna/high，无再派生。

- 2026-10-28 T035/T036定义完成：26个顶层测试覆盖来源身份/pin/恶意输入、逻辑组与有界调度；固定Go语法/格式通过，因未来生产符号缺失的RED明确保留，不计语义PASS。独立scout复核无定义矛盾，T035当前check-04新增来源重标记反例，accepted-pin.json为本次权威，旧final-pin.json/check-03留作历史。已完成50/140；进行中T037；失败待决策无；环境不足未验证仍为其他平台与既有边界。executor实际gpt-6.1-sol/medium负责先前定义，scout实际gpt-6-luna/high复核，root作最终验收。

- 2026-10-28 T037完成：严格v1意图+有界JSON/YAML纯解析，root复核后补数字literal128字节上限与Probe主机/端口边界；最终check-02 document普通/race各20PASS、Intent各39PASS、vet通过、7输入hash相符。旧收据保留，未触碰运行/store事务。已完成51/140；进行中T038/T039；失败待决策无；环境不足未验证其他平台/完整来源运行矩阵。executor为gpt-6.1-sol/medium，scout为gpt-6-luna/high；root最终验收。

- 2026-10-28 T038/T039完成：Linux显式Fetch普通/race各16PASS，有限解析/身份各157PASS、vet通过；固定core34组原始/归一化68次-t通过且binary未变。root修正native ALPN/SNI与WS ed隐含参数后重验受影响范围，旧146PASS与草案保留。完成53/140；进行中T040；失败待决策无；环境不足未验证其他平台/远端协议握手/T044真实来源线路，构造校验不充当转发。root方案/Fetch/core验证，executor gpt-6.1-sol/medium、scout gpt-6-luna/high。见us3-provider-modules及验证索引。

- 2026-10-28 T040完成：policy普通/race各73PASS、0FAIL/0SKIP，15顶层含T036六组，vet通过；source元数据按需校验、20k链/深环和独立指针验证，root稳定审查/hash通过。完成54/140。进行中T041，新增发现basic-v1明确只接受loopback HTTP/SOCKS/Selector且URLTest存在默认首选，已补入同一任务的版本化核心/策略快照/成功探测前拒绝前提，未扩大旧capability或勾选T041。失败待决策无；其他平台/新核心实际矩阵仍未验证。executor实际gpt-6.1-sol/medium；scout实际gpt-6-luna/high提供源码证据。

- 2026-10-29 T054进行中：当前任务计数68/140（此前T052/T053仅完成测试定义，不代表run已交付）。已完成：本轮草稿/启动历史命名空间与路径边界普通、race各12顶层/130含子PASS，vet通过；初始化状态与身份连续性定向回归通过。进行中：真实init CLI与稳定源码独审，随后T055/T056。失败待决策：无人工决策项；control首轮runner cwd错误导致fixture缺失，修正入口后通过，原日志保留。环境不足未验证：其他平台、实际完整run旅程及P2账本仍未验证。root负责持久化/恢复语义；executor实际gpt-6.1-sol/medium负责纯输入和CLI，scout实际gpt-6-luna/high独立复核，无再派生。

- 2026-10-29 T054已完成69/140：[本地init验收](validation/us2-local-init.md)与公共索引保留最终源码/测试/真实CLI原件引用。普通/race存储各13顶层131含子、控制各15顶层46含子、纯输入各6、CLI模块各3PASS，真实CLI各8调用全部预期结果，338输入无变化、vet0、最终独审通过。进行中T055子环境与后续T056；失败待决策无，修前umask/历史预检/CLI退出码与cwd失败记录保留。环境不足未验证其他平台、资源预算；完整run/计量账本尚未完成。root验收关键持久化与实际CLI；executor gpt-6.1-sol/medium、scout gpt-6-luna/high按分工完成，无再派生。

- 2026-10-29 T055已完成70/140：managed/inherit/direct纯环境普通/race各10顶层44含子PASS，原测试定义未改，vet与独审通过；额外修复有效空env及fmt异常verb隐私，保留真实RED与修前记录。进行中T056持久启动、匿名字节载荷与TTY；失败待决策无，环境不足未验证其他平台/资源预算，真实应用仍待T058。root完成契约/最终验收，executor gpt-6.1-sol/medium实现及验证，scout gpt-6-luna/high独审。

- 2026-10-29 T056进行中，仍70/140：阶段类型已普通/race各4顶层38含子PASS；独立分配/append/读取连同类型首轮各14顶层80含子PASS，投影各3顶层8含子PASS、vet0。独审后澄清unpublished stage内部不作已接纳记录解释，并收紧锁deadline尝试前后检查，修订补验中。执行授权/匿名argv-env载荷/真实runner尚未接线；失败待决策无，process/TTY移植由executor gpt-6.1-sol/medium独立实现，scout gpt-6-luna/high复核，root保有日志/锁/恢复与最终验收；其他平台/资源预算未验证。

- 2026-10-29 T056/T057接线继续，仍70/140：分配/记录修订普通/race各15顶层81含子PASS；配置捕获4/18、匿名字节载荷4/34、新进程/TTY组件5/8、实际runner组件最终6/19、CLI范围16/60均普通/race通过，相互重叠集合不累加，定向vet及独审通过。root将产品TTY前台切换延后至原pidfd和birth确认，避免Start失败或缺pidfd后遗留前台组。T058实际CLI验证已运行：参数/IO与原始子进程信号两场通过；PTY前两次因独立Python观察器关闭自身控制终端收到SIGHUP而失败，原日志/全量归档保留，取证修订证实raw wait1；只在观察完成后的驱动收尾忽略自身SIGHUP，重验PTY通过。后台后代场亦通过，managed与最终稳定批次继续。失败待决策无（修订已确定），真实Agent首次旅程尚未执行；其他平台/资源预算未验证。executor实际gpt-6.1-sol/medium，scout实际gpt-6-luna/high，root负责核心时序和验收；这些中间结果不提前勾选T056–T058。

- 2026-10-29 T056/T057已完成，72/140：[受限应用启动](validation/us2-application-launch.md)真实普通/race各5场PASS，source/原件/稳定diff独审闭合，CLI/core/entry身份一致，旧失败归档保留。进行中T058纯本地首次init→apply→run补验。真实Agent尚未执行：自动审批拒绝继承现有认证向外部模型服务发送请求；已准备固定无工具提示和具体driver，并请求用户确认，未绕过拒绝。环境不足未验证其他平台/资源预算；项目不因这两项完成而整体完成。executor实际gpt-6.1-sol/medium，scout实际gpt-6-luna/high，root最终验收。

- 2026-10-29 T058本地首次旅程补验完成：[onboarding进度](validation/us2-onboarding.md)。normal02/race01从各自空状态实际init→draft direct/inherit→preapply managed拒绝→公共service/plan/apply→managed本地HTTP1次→原core/service publicstop及pidfd终末，父环境不变、query只读，无fallback。normal01私有driver变量遮蔽导致close失败保持FAIL，修驱动后完整重验，未改产品。真实Agent命令因自动审批拒绝尚未执行，具体用户确认仍待回应，T058继续未勾，计数72/140；没有通过其他入口绕过。其他平台/性能仍未验证。


## 2026-10-29 阶段源码上传准备

用户要求先整理当前部分，并明确包含 SPEC、后续继续开发。本轮只整理上传边界与说明，未提交、push 或执行真实 Agent 外部请求；任务勾选保持72/140，T058未完成。入口见[源码快照说明](upload-snapshot.md)。

| 工作 | 状态 | 证据或后续动作 |
| --- | --- | --- |
| 当前入口与继续开发资料 | 已完成 | README精简；更新development的当前事实并保留历史正文；完整spec/plan/tasks/contracts/research、.specify宪章模板脚本、共享skills进入候选 |
| 临时文件与上传排除 | 已完成 | 清除11个可再生Python缓存共147357B；新增本机账号/缓存/编辑器忽略项，15排除和10保留例通过；私有证据继续保留 |
| 第三方材料 | 已完成 | 新增THIRD_PARTY_NOTICES及7份逐字许可证/NOTICE；保留Hoopa MIT、上游Mihomo GPLv3、Spec Kit与各Go依赖的独立范围 |
| 干净候选构建与静态检查 | 已完成 | 只导出Git可见文件，Go1.27.3离线build cmd/hoopa、vet ./...均exit0；生产源码/测试/协议输入未修改 |
| 默认测试缺少外部材料 | 环境不足未验证 | 安全/tmp运行目录复验：979顶层PASS、18FAIL、37SKIP，25包PASS/3FAIL/4无测试；17失败要求实际批准核心，1失败要求本机私有审查原件，不能记作全库通过 |
| 首轮测试临时目录错误 | 已完成 | 原失败记录保留；Go1.27.3 testing.TempDir优先GOTMPDIR，误用本机/data路径被保护拒绝；只改运行目录重验，未放宽guard或改断言 |
| 默认检查与受控制品验收分层 | 进行中 | 明确记录当前材料前置；后续公共CI需单列制品/权限与私有证据验收，不能以静默skip消除失败 |
| 人工取舍或授权缺口 | 失败待决策：无新增 | 上传准备不依赖真实Agent授权；该项仍按原记录未执行，P2/P3和其他平台/预算继续待验收 |

本轮执行分工：root沿用当前会话模型与effort，负责上传范围、忽略规则、临时清理、许可材料、干净候选构建与最终验收；executor实际使用gpt-6.1-sol/medium整理文档和测试边界；scout实际使用gpt-6-luna/high只读检查候选、敏感模式、许可证来源与SPEC脚本。最多两个子任务，无再派生。原件保存于忽略的private/upload-preparation/，不复制认证文件，不改写已有验收JSON/hash或历史失败结论。


## 2026-10-29 上传边界收紧：运行报告留本地

用户明确临时/中间产物不随Git同步，工作继续。本轮只外置181个文件（178运行/过程JSON、3安装回执），原件仍在原位置且SHA不改；保留全部正式MD、完整SPEC/任务、六份最终acceptance、三个批准核心记录、构建manifest/锁、candidate/fixture与正式decision。旧记录中指向外置JSON的链接仅改为明确本地原件代码引用，label/路径/结果/数字/hash及140项任务勾选不变；任务仍72/140，T058真实Agent未执行。

收紧后最终候选为925文件/约9.9MB（未压缩文件总量），Go1.27.3离线build cmd/hoopa及vet ./...均exit0；默认测试979顶层PASS/18FAIL/37SKIP（25包PASS/3FAIL/4无测试），完整19失败事件与前轮逐名一致，无新增，不宣称全库通过。前轮1106/约15.8MB及首次临时目录失败仍按原记录保留，不回填为新快照结果。

受控gate复核继续依赖本地原运行报告/私有证据；正式结论不等于clone含完整原件。不改代码/核心准入/测试断言或验收JSON，不删除旧失败。共享integration/init-options/workflow-registry配置保留；只外置安装manifest/快照，feature/local overrides仍忽略。root负责忽略规则、候选导出及最终验收，文档执行者仅机械改链和本轮说明；没有提交/push或真实Agent外发。
