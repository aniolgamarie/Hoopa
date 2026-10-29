# ADR 002：允许进入不依赖数据库的 P1 实现

2026-10-26：**T018 已完成受限可行性裁决，允许开始 DB-free P1 实现。**
[机器裁决](../../experiments/mihomo-events/p0-core-gates-decision.json)绑定实际制品路径/hash、
逐门槛证据和 root 对保护来源的决定。当前 140 项任务完成 35 项。
这关闭的是基础管理开发前的可行性门槛；P0 仍需 T019–T021，P1 产品尚未实现或验收。

| 门槛 | 已接受证据 | 本次裁决边界 |
| --- | --- | --- |
| G01 | 固定 0017 原 connector 正向 100/100 及独立边界 | PID/启动身份与 connect 快照；旧完整二进制结果不改标为新 hash |
| G02 | 固定 0017 的 8 VM/16 槽来源、接收与重开恢复 | 明确 pending/gap；ACK 不证明 SQL，来源 GC 后续实现 |
| G03 | 固定 0017 的 6 VM/21 槽 ready 与线路 | 注入/adapter 与真实网络分列；setup 不是业务 RTT |
| G04 | T015 的 11 VM/22 尝试、接管/登记窗口、SSH/logout/guest S3 等 | helper ad4a 与旧核心限定环境结果；当前多入口接管增量另有 T016 实际证据 |
| G05 | T016 当前多入口核心/helper 的 16 组、39 VM、127 尝试/124 来源流、59 epoch | 最终汇总 race 通过；旧失败保留，四 owner 只有合成测试 |
| G07 最小保护 | 0017/ad4a 的 7 应用/11 管理/14 文件实际探针，加上未变保护实现、当前受影响路径测试/集成及本次语义审查 | 接受限定工程前提；没有在 1c876 上重新运行整套 7/11/14 矩阵，也不产生新版同名结果 |
| G06/G07 规模 | T019/T020 未执行 | 驱动、事务、规模与性能仍待原任务验收，按计划不阻塞无数据库的 P1 开发 |

## 候选与访问保护的来源裁决

原核心 `5693d4ac2bda5fcae7e5ac037d23db1b2a48049f681b8c825b8b4db12ac1b967`
及 helper `ad4a8069691e6668c658eb98c18c845bc3b2f5f903a08fe1db28560c2c92e13f`
保留各自完整实验。多入口核心为
`5e934bdc28dd07e7b7f8cf372d1d4f7a553a3aa3bd835de24f982dfed011a388`，
helper 为 `1c87609972b0ac5570101c50945bfe4aa09a4687a433adb9c538722871107f95`。
机器裁决固定四个实际可执行文件的路径/大小/hash；它们是外部实验制品，不加入 Git。

保护来源对照（本地原件 `../../private/t018-protection-lineage/report.md`，不随Git上传）和独审核对 84 项引用：
helper 的 owner、IPC、store、probe 四份有效编译源码相同，runtime/process 的
12 个变化函数与新增 multi 模块单列；核心选中 11 文件中 9 份相同，另两份只对
明确相同的函数/鉴权前缀认定继承。旧结果没有从一份文件相等直接推成新版运行。

root 复核了改变的调用关系：controller 的唯一 Bearer/loopback 检查仍在所有
状态分支之前；每个入口保留独立且已验证的认证 store，指定 owner.Admit 在原有
认证 HandleHoopaConn 之前执行；缺 owner/proof 会进入共享 degraded。helper
仍在同一条已验证 socket 上发送凭据；新增 schema union、全部入口/世代及健康
检查拒绝缺字段或不可用值。新 helper 已记录 race 63 顶层/230 含子项通过；其中
不含 IPC/native owner 重跑，这点明确保留。当前两入口转发、接管与缺证明持续
拒绝的实际证据覆盖改变的调用路径；结合旧真实保护矩阵和未变算法，足以支持
此次有限可行性裁决，无需重复所有不受影响的旧矩阵。

G04 历史文字中的“新 helper”特指 ad4a；本次明确与 1c876 分开。默认特权安装、
抵抗可信核心恶意行为、任意生产部署和跨平台仍未获能力声明。P1 新实现必须
完成 T025/T034/T060/T063 的实际保护与恢复验收，不能只引用本 ADR 交付。

## 进入 P1 的实现模型

| 模型 | 已确定的不变量 |
| --- | --- |
| 托管 | 外部核心独立于前端；control-only 只观察，停止/恢复显式执行，不因查询重启核心 |
| 身份 | boot/PID/start、二进制/路径、私有 nonce/启动意图/工作目录、同连接 controller owner 证明；PID 或端口单独不认领 |
| 编译 | 完整不可变 bundle 固定全部资产、稳定来源/节点/组、有序规则和能力摘要；缺目标/不支持能力在修改前拒绝 |
| 入口 | managed 使用已提交的 profile 入口；每次 run 不隐式重载，入口上下文不能冒充 PID，不自动直连或换来源 |
| 发布与恢复 | 运行改变前持久操作；实际核验后 publishing→active→committed；完整回滚无法证明则 degraded，查询仍只读 |
| 统计能力 | 实验同源事件能力与 stock/GOST 兼容能力分开；P1 不装配 SQL，计量与高级进程条件须按能力显式拒绝未支持场景 |

按既定依赖启动 T022/T023 的验收定义、T024 起的产品实现；T019 驱动探针可并行。
基础管理不依赖 GOST、SQLite 或 TUI，不将 trusted-root guest 的启动方式变成
默认安装要求。后续产品的新实现与新运行环境仍必须重新验证受影响路径。
source/receipt/SQL 各自有持久边界，原 reader 的 unverified/null 不被改成通过；
来源回收、规模、性能、完整计量与平台支持分别由 P2/P3 和尚未完成的 P0 任务处理。
下面是原历史阶段判断，原失败和当时不放行结论均保留。

## 2026-10-22/23历史阶段判断


日期：2026-10-22。状态：不放行 DB-free P1；T018 保持未完成。本文记录当前阶段判断，不表示其前置任务已全部完成。

依据：[任务清单](../../specs/001-hoopa-proxy-manager/tasks.md)规定关键 failed/unverified 不得放行；[ADR 001](001-event-source.md)否决当前完整事件源候选，[ADR 005](005-telemetry-replan.md)已确定同一数据面事件路线及接收journal整改。运行子断言通过不能替代整体经验前提。

| 门槛 | 当前结果 | 证据与剩余工作 |
| --- | --- | --- |
| 真实应用 | 子断言 passed | [工作流](../validation/p0-workflow.md)：Codex 正常/持续完成，实际代理路径有计数和连接证据；wrapper 信号等另验 |
| G01 | 当前隔离候选严格断言failed，门槛未关闭 | [原组合源身份](../validation/g01-identity.md)保留反例；[隔离候选](../validation/native-candidate.md)已执行完整100流分母，身份0/100、全部owner_visibility，终末/目标100/100不能代替身份 |
| G02 | 部分子断言passed，整体unverified | [原组合计数](../validation/g02-counters.md)及[隔离候选](../validation/native-candidate.md)有100流整数终末、来源503重投等证据；完整源故障/未投递/重启矩阵仍未通过 |
| G03 | 部分事件已观察，整体unverified | [原组合质量](../validation/g03-quality.md)保留；隔离候选已同flow/attempt记录单DIRECT路线，但协议ready、两节点和重试归属未通过 |
| G04 | unverified | harness 退出后同一父会话内核心仍在并完成请求；跨工具会话未存活，原因未隔离。无生产接管/注册间隙/SSH注销/休眠证据 |
| G05 | unverified | 受控共享入口无需 GOST 已验证；完整命名应用入站/三值规则/固定目标/分阶段开放/回滚/旧候选冲突矩阵未实现或运行 |
| G07 最小保护 | 部分 passed，整体 unverified | 9 项核心 controller/metrics/数据认证通过；本地管理 IPC 不存在，未验证其授权矩阵，不视为不适用而放行 |
| G06 / G07 规模 | 未执行 | T019/T020 依赖 T018；不提前安装 SQL 驱动或伪造容量、安全、跨平台结果 |

托管、生产身份/epoch、配置编译和应用入口模型仍是待验证设计，不从本次 cmd.Process 清理或私有文件格式推出生产保证。所有实测原始证据仅保留于私有目录；公共摘要绑定固定版本和 SHA-256。

恢复实施的条件：先按2026-10-23[全计划复核](../validation/plan-task-review.md)执行T130同拓扑诊断、T131具体身份裁决与T132最小正负身份验证，补全T133–T135、由T137冻结新候选后才重跑T127–T129正式矩阵；T136为T015/T016提供最小控制harness。输入无变化时不重跑已知失败。接收journal通过只关闭已送达事件持久化子断言；来源身份/未投递/计时线路仍分别验收。只有真实结果满足 T018 才能进入 T022 及后续 P1。隔离实验核心已获此前范围授权；扩大特权、发行或降低完整性要求不能由实现自行豁免。

2026-10-23最新设计以[ADR007](007-p0-evidence-closure.md)及用户确认的connect_initiator_v1为准：原始建连实例/名称路径快照、FD共享转交不重写；旧0/100不重评。T132仍待原入口唯一关联原型；T133采用原帧重放与派生pending/gap报告，撤回强制v2恢复引用图；T134先冻结profile再按协议ready/真实两线路验证。此项需求和计划修订不改变上表failed/unverified或T018不放行结论。
