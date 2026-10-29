# US3 来源更新集成记录

2026-10-28，T043 已按下述有限 profile 验收，任务总数完成57/140。该计数保留 T043 验收时点；后续 T044 已完成[双来源限定矩阵](us3-sources.md)，完整订阅管理、P1 和项目尚未完成。总验收索引（本地原件 `../../experiments/gost-mihomo/us3-source-control-validation.json`，不随Git上传）保留各批范围和当前输入绑定，不叠加有重合的测试计数。

## 已完成的输入与回执模块

| 模块 | 普通 / race | 范围 |
|---|---:|---|
| 冻结下载输入 | 各 31 PASS，14 个顶层测试 | HTTPS 原地址在 secret reference 改写或删除后仍固定；重新准备的 digest 用于提交检查；文件正文在下载阶段读取 |
| 更新上下文 codec | 各 112 PASS，6 个顶层测试 | 完整私有定义、区域声明、设置和请求绑定、序号与小时预约时间，精确 JSON |
| 更新请求解析 | 各 6 PASS | 严格 envelope/params、可选 Intent、区域策略省略与清空的区别、精确来源选择与复制 |
| 阶段回执与记录绑定 | 各 78 PASS，7 个顶层测试 | JCS、成功/失败 envelope、阶段一致性、上下文绑定、压缩终态边界 |

上述范围各有 vet 通过记录，无跳过。输入和回执 codec 不证明下载、事务或恢复执行。每批 scoped pins 对应当时的限定输入；合同后续增加集成设计不等于旧测试覆盖了新增设计。

私有证据分别位于 `private/us3-preparation/t043-fetch/`、`t043-context/`、`t043-request/`、`t043-result/tests/`。源码与可审阅合同保存在仓库，含私有地址/输入的测试运行目录保持忽略。

## 已验证的有限服务集成

目前实现了单 Coordinator 的后台更新、原 Journal 持久预约与结果、同请求只重放、每来源一个且全局两个 worker、关闭前取消并 join、输入变化拒绝提交，以及复用原 `operation.recover` 工作流的来源恢复。来源更新不发布 active pointer，不启动核心。`subscription.update`、`subscription.list` 已接入服务，`operation.get` 和 pending 状态提供单次尝试的 downloaded/validated 与恢复状态。来源列表分别核验最新可用和已应用资产，每页最多32来源，后续 cursor 绑定完整视图，变化显式过期。

冻结46项输入，定向 control 普通/race各140 PASS（36顶层），完整 service race106 PASS（34顶层），均0失败/0跳过，相关vet通过。机器记录（本地原件 `../../experiments/gost-mihomo/us3-source-update-validation.json`，不随Git上传）保留命令、输入pin和原日志摘要。该范围包括实际文件和 HTTPS 更新、旧快照在验证失败后保留、同来源/全局并发限制、下载期间只读 status、secret 变化拒绝提交、并发 Close 等待；真实子管理器在 HTTPS 请求中被 SIGKILL 后，重开只重放原 Applying 记录，显式恢复为 interrupted、不重投下载，随后新请求使用下一序号。真实 Unix socket 服务也通过更新、进度轮询、失败回执严格帧校验、列表、退出与离线重放。恢复子矩阵独立normal/race各31PASS，覆盖Publishing原资产恢复提交/输入变化回滚、依赖损坏零写拒绝、live冲突、早期中断、原RollingBack事实保留和压缩终态/旧恢复调用收束；合并验证包含其当前版本。

真实 SIGKILL 测试只涉及管理器与来源下载，不启动 Mihomo，因此不证明核心转发连续性。Publishing/故障注入矩阵另行验证。显式恢复的 RestoreAsset 是有限本地重新解析，可在既有 recovery 锁内执行；实时下载和新 provider 解析在锁外。锁内历史读取、私有凭据准备有容量上界，但尚无延迟基线。

## 失败与剩余范围

首轮 worker 测试 1 PASS / 5 FAIL：空 `snapshot_id` 使用了不允许空值的 `config.ID` JSON 类型，Prepared 回执无法编码。已修为阶段约束下的字符串，失败原件保留于 `t043-updater/attempt-01.log`；修复后同六个场景普通测试通过，随后相应 race 通过。独立审查发现的 downloaded/失败原因阶段矛盾也已修正，并有纯记录反例测试。

T043验收时状态：已完成有限输入/回执、后台更新/恢复、只读分页、订阅CLI、本地Plan、持久调度与实际SourceApply；当时进行中：T044双来源US/JP/BEST、F08/F09/F32/SC-006及精确阻断原因。失败待决策：无人工决策阻塞，历史失败保留。环境不足未验证：其他平台、性能预算与实际机器休眠；本机Linux文件、TLS、进程终止条件可用。

## CLI、本地计划与准备写入增量

订阅 CLI 已接入实际服务：`subscription list [--cursor]`、`subscription update ID [--input PATH] [--regions PATH]` 使用同一版本化请求和幂等回执。有限普通/race 各42 PASS（11顶层），vet及独立审查通过；验证了真实 Execute→service.Run、human/machine 一致、成功/失败重放、普通文件边界和不隐式启动服务。其证据位于 `private/us3-preparation/t043-cli/`，不是整个 T043 完成声明。

本地 Plan reader 已完成普通/race 各22 PASS（9顶层），无失败或跳过，vet、19项输入绑定及独立审查通过。Plan 捕获已验证的原来源历史、设置和引用资产，在锁外恢复及编译；它只打开校验批准的真实静态 ELF，不启动核心、不下载、不创建工作目录。未引用 provider 正文不是输入依赖；已引用来源的新候选、地区策略、设置或完整 Intent 变化均重新绑定目标 generation。测试还证明捕获后的编译不会混入后续 base/settings/资产变化。证据位于 `private/us3-preparation/t043-plan/reader/`。

`config.plan` 已接入服务及离线只读入口；`config.apply` 已通过来源材料桥接进入原 `BasicApplier`。实际 Unix socket 新增6项普通测试通过，覆盖线上/离线计划一致、确认失败零写、无服务时不自动启动、核心缺失不修复。Apply 首先复核原计划、确认、版本和完整输入，随后才记录准备目录的名字、创建目录并执行原材料检查，最后沿原 Journal/RunIntent/一次性启动票据路径继续。独立静态审查未发现准备孤儿可以授予启动权限；真实核心首次应用、来源切换和活跃下载重叠由后续实际测试验收，不能由这里的拒绝路径推断。

工作目录准备早期测试保留三轮失败原件：前两轮夹具误将已签名 Plan 传给只接受未签名输入的 BuildPlan；第三轮核心文件的 `/data`、`/data/1` 祖先为非 sticky 的0777目录，被实际 ELF 路径检查拒绝。正例改为将已核对 SHA 的同一核心复制到私有目录，未放宽生产检查。修复后准备写入、原/新核心材料、Coordinator 和来源历史定向 race 共174 PASS（23顶层），0失败/0跳过。更早的95 PASS普通/162 PASS race各有1个未设置旧核心环境变量的 opt-in 跳过，不能充当那个旧核心正例通过；后来174项已实际提供两个批准核心。

当前有限 source Plan profile 对相同 generation 的 no-op 和已有历史 generation 处于 stopped 时的更换配置显式返回 CAPABILITY_UNSUPPORTED。支持首次 start 和运行中完整 replace；停止后的既有 bundle 仍走原 runtime.start。锁内 Apply 的两次本地复编译有容量上界，尚无锁等待与内存预算实测。

post-F08 的实际 SourceApply 普通/race各1个多阶段场景已通过，无失败/跳过，24项输入稳定，vet及独立最终审查通过。每场真实启动两个批准核心、完成3次原生 HTTP 代理转发；A→B 后 US/BEST alias 不变、follow保留父组引用。旧Plan与未确认Apply零写拒绝；真实HTTPS下载跨首次Apply后因原revision变化回滚为input_changed且保留旧Latest；缺失settings时相同请求仍直接重放；list的latest/applied/pending分别核验。替换及公开Stop后原pidfd退出、proc消失，无cleanup补信号。证据位于 `t043-apply/`；其pre-F08原件单独保留，不充当最终输入验收。该场景只有US/BEST，完整US/JP/BEST矩阵仍属T044。

## F08 空来源修正

T044准备时发现旧更新器会把合法的零节点Snapshot提交为Latest，与F08保留旧快照的要求不一致。现已在持久更新边界将其判为downloaded=true、validated=false、validation_failed，保持旧Latest和active，不发布空provider资产。parser仍可以表达结构合法的空Snapshot；Plan/list/recovery拒绝历史空候选，不能借恢复或查询把它晋升。非空候选若删掉某个必需地区或固定pin，则由完整Plan拒绝不完整配置，旧active保持运行。

定向控制普通/race各71 PASS（32顶层），服务/CLI race分别113/59 PASS，均0失败/0跳过，vet与10项输入稳定复核通过。合法历史空资产/回执/Journal夹具覆盖Publishing和Committed恢复拒绝及Plan/list拒绝，查询不修复；实际非空→空更新证明旧Latest保留。原先以空provider充当成功来源的service/list fixture已改为非空；旧验收日志仍保留当时事实，其空来源成功预期被本次修正替代。详细原件为 `t043-empty/`。

## 持久调度有限模块验收

服务已接入进程内30秒 pulse，只有 committed Intent 的周期配置可以启用自动采集；构造 updater、status、list 和 Plan 不启动 timer。自动和手动采集共用现有 Journal、全局两个及每来源一个 worker。固定 cutoff 只选一次到期来源，准入锁内再核对来源、原尝试身份与到期条件，手动更新介入后旧 pulse 不得再预约。成功采集不推进 active revision，也不自动 Apply。

额外真实 service.Run/Unix socket 场景普通/race各1 PASS：首次启动自动取得来源；删去原 provider 后重启服务仍保持 sequence1，不重复已预约周期；两次公开 service.stop 均等待 timer 退出，active pointer保持原值。其 stopped committed bundle 仍是 synthetic fixture，没有启动核心。独立补充原件为 `t043-schedule/service-pulse-receipt.json`，不追溯计入前述冻结组合计数。

保留已预约的历史 `next_due`；若历史手动采集未声明周期，则以其原 started_at 加当前 committed 周期推导 `effective_next_due`，避免立即重复下载。列表分开展示这两个时间。普通来源依赖错误不阻断其他健康来源；dead nonterminal operation 仍是全局恢复屏障，调度不重放、不修复该历史。服务状态给出调度 waiting/degraded/stopped 及固定错误码，查询返回独立值。

9个调度定向场景已通过 race；与来源列表、更新、真实 SIGKILL 子管理器父测试合并为20 PASS，另有一个仅供子进程启动的 helper opt-in 跳过。该有限场景使用真实 Journal、文件与 HTTPS，但调度用的 committed bundle 是明确标注的 synthetic stopped fixture，不冒称实际核心 Apply。服务/CLI/纯调度后续 race 分别112/59/42 PASS，均无失败或跳过；本轮19组离线 schema 检查通过。最终组合普通/race各249 PASS（63顶层），0失败/0跳过，相关 vet 已通过，25项输入前后无漂移；见本轮机器记录（本地原件 `../../experiments/gost-mihomo/us3-local-plan-schedule-validation.json`，不随Git上传）。独立最终复核已通过；JSON Schema只检查声明的形状，跨字段一致性由对应服务测试覆盖。原46项输入的更新收据保持历史范围，不被上述新增接线追溯扩大。

主代理负责锁、日志、恢复语义与最终验收；executor 实际配置为 gpt-6.1-sol / medium，负责限定实现或测试；scout 实际配置为 gpt-6-luna / high，负责独立证据与审查。子代理未再派生。
