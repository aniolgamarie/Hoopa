# Hoopa 技术研究与决策

日期：2026-10-22。输入：[规格](spec.md)、[宪章](../../.specify/memory/constitution.md)及既有设计文档。
本文件完成规划阶段的资料研究与选型路径，不是 P0 的运行报告。可变上游文档不作为固定二进制的验收证据。

## 1. 决策如何生效

- **已决定**：可以据此分解任务的产品行为和工程基线。
- **实验候选**：已经决定实验、比较条件和失败出口；只有固定版本的实测与 ADR 才能将它升级为实现承诺。
- **运行验收**：本轮均未执行。来源阅读、模块元数据、构建、真实访问与故障注入分别记录。

不存在等待用户回答的产品问题。驱动、进程存活、源身份与连接事实等经验问题通过下列 P0 实验裁决，不能用推测填成通过。

## 2. 工具链与依赖边界（R01）

**Decision**：构建候选固定为 Go 1.27.3、Cobra v1.10.2。TUI 到 P2 才加入，候选使用
`charm.land/bubbletea/v2 v2.0.8` 与 `charm.land/bubbles/v2 v2.2.1`。
P0 开始前记录实际工具链、模块校验和；不在本次文档工作中创建 go.mod、安装依赖或宣称已构建。

**Rationale**：Go 发布记录提供此工具链版本；Cobra 固定 tag 的模块要求可满足。
Bubbles v2.2.1 的 go.mod 直接要求 Tea v2.0.8，两者要求 Go 1.25.0；这组元数据相容，仍需实际构建。
不以 main 分支或更高 Tea 版本替代已核对的组合。

**Alternatives considered**：提前引入 TUI、使用 Tea v1 或自行覆盖 Tea v2.1.0，均增加 P0/P1 无需承担的依赖或兼容验证。
源码使用单 module；本地开发可先使用 `module hoopa`，正式发布前随最终仓库地址改为真实 module path，不虚构 GitHub 组织名。

来源：[Go release history](https://go.dev/doc/devel/release)、[Cobra v1.10.2](https://github.com/spf13/cobra/blob/v1.10.2/go.mod)、
[Tea v2.0.8](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/go.mod)、[Bubbles v2.2.1](https://github.com/charmbracelet/bubbles/blob/v2.2.1/go.mod)。

## 3. 进程托管与平台（R02）

**Decision**：先验证一个 hoopa 程序的 CLI、前台服务及内部启动入口，Mihomo/GOST 为独立 OS 进程。
Linux 用户态会话脱离、独立文件日志、私有工作目录和启动身份为候选，不默认安装 systemd/launchd 服务、登录钩子或计划任务。
核心生命周期不绑定 CLI/TUI context；关闭客户端不发 stop，管理服务退出不主动杀死存活核心。

**Rationale**：Go 的 CommandContext/Process.Kill 不提供完整后代管理保证；Linux 新 session 也不能越过宿主的注销清理策略。
因此分别测试 CLI 结束、管理器崩溃、SSH 断开、注销、休眠与主机重启，不将它们合为一个“后台运行”。

**Alternatives considered**：默认系统服务、Windows 默认 kill-on-close Job Object，以及用 nohup/setsid 一项推断跨平台支持，均不符合当前授权或证据要求。
若用户态方案不能满足某宿主生命周期，记录该环境限制并由 ADR 调整托管选择；不暗装服务或开启 linger。

来源：[Go os/exec](https://pkg.go.dev/os/exec)、[systemd logind.conf](https://www.freedesktop.org/software/systemd/man/latest/logind.conf.html)、
[Windows Job Objects](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects)、[Apple launchd](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html)。

## 4. 配置、订阅和稳定身份（R03）

**Decision**：本地版本化 YAML 表达意图；严格解析并拒绝未知字段。订阅首期仅接受经过 fixtures 验收的 Clash/Mihomo 节点 YAML，
只提取允许的节点字段，不运行扩展脚本或在线转换。Hoopa 将 provider 固定为不可变本地快照，核心不再独立更新同一来源。

US/JP/BEST 默认从活动订阅解析；逻辑别名引用目标组，而不是复制其当前选中节点。跨来源固定节点必须显式引用并纳入完整候选校验。
节点使用来源命名空间内的稳定 ID，显示名不参与唯一性。首期以私有身份键对规范化连接参数计算指纹并维护本地 ID 映射；
认证或其他身份参数变化、重复/歧义及身份键丢失不自动重绑定固定选择。原始认证值不作为公开 ID 或默认输出。

**Rationale**：Mihomo 的 file provider 是可用构件，本地不可变快照使下载与运行态发布可分离。
节点没有统一的供应商永久身份，严格映射比按显示名猜测更适合固定 pin。

**Alternatives considered**：远端整份配置作为权威、按显示名匹配、自动混合供应商候选、内核和管理器各自更新，均破坏已确认规则或恢复边界。

首期可调工程限制：单次来源读取 30 秒、解压后正文 10 MiB、最多 10,000 节点、解析深度 64、全局最多 2 个来源更新且每来源 1 个。
越界拒绝候选，保留旧快照；这些是待 P0 校验的防资源耗尽默认值，不是支持规模实测。
订阅下载、显式 probe 与已有业务转发的启动授权分别处理。调度不重放错过周期，每个到期来源恢复时最多安排一次工作。

来源：[Mihomo proxy providers](https://wiki.metacubex.one/en/config/proxy-providers/)。
Clash Verge Rev 的分层覆盖组织提供参考，但其脚本与通用合并链不进入 Hoopa；Hoopa 编译受限意图并保护本地字段。
[扩展配置说明](https://www.clashverge.dev/guide/extend.html)。

## 5. 变更、冲突与恢复（R04）

**Decision**：采用不可变完整快照、持久操作日志、单写入者和明确中断的完整替换基线。
预览为纯本地计算，返回包含 base revision、输入快照摘要、候选摘要和影响摘要的计划对象；不创建内部候选状态。
调用方可显式将计划重定向到文件。apply 只执行该计划，不重新下载或偷偷重新规划。

对新操作，拿到写入权后先检查 base revision 和全部输入身份，过期返回 CONFLICT，任何本次持久或运行变更均未开始。
先按 request ID 查询同一已开始操作的结果，避免把合法重试误当成新候选。相同 request ID、不同内容返回 REQUEST_ID_REUSED。
无歧义的人工确认可由 CLI 完成；机器调用提交明确确认的 plan/impact 摘要，不出现提示等待。

**Rationale**：用户已选择拒绝旧候选而非自动合并。文件系统、核心 API 和 SQLite 无共同事务；
需要在实际运行核验后才发布 active pointer，并保留每个可能崩溃点的恢复判断。

**Alternatives considered**：多组逐个修改后立即报成功、自动合并旧计划、用一个 SQL 事务包住核心重启，均不满足已确认语义。
热重载和单组选择仅在 P0 证明一致性与恢复后优化，不作为首期正确性的前提。

细化顺序见 [data-model.md](data-model.md)；对外确认与错误见 [contracts/cli.md](contracts/cli.md)。

## 6. 应用与真实源进程路由（R05）

**Decision**：P1 先验证命名 Mihomo 入站保留应用上下文，不计量不需要 GOST。
使用统一有序规则的中间表示；以入站条件编译应用规则，不能用 listener 的直接 proxy 字段绕过更早的域名/IP 条件。
只启用已通过固定版本探针的编译能力。真实源进程的实例/精确名称/路径匹配属于 P2，能力未成立则拒绝该配置。

**Rationale**：官方文档提供 IN-NAME、进程规则与 listener 构件，但字段存在不证明本项目需要的跨入站顺序和未知身份拒绝。
标准 PROCESS-NAME/PATH 的未匹配结果不能自动等同“身份已知且不相等”。

**Alternatives considered**：以 GOST PID、父进程或 profile 代替业务源身份；事后补 PID 再倒写路由；每连接回调管理器决定线路，均不采用。
若需要身份/事件适配，必须证明它在原始接入点、路由前完成，并在独立数据组件中执行已发布策略；管理器崩溃不能使已应用路由失效。
当前尚无经验证的适配实现，不把“新增 adapter”视为能力已解决。不能满足时停止相应 P0 门槛并重做设计，不能发布一个永远拒绝的“进程规则支持”。

**2026-10-23用户口径澄清**：按 `connect_initiator_v1` 识别原始客户端TCP连接发起实例，名称/路径为建连快照，路由前验证该快照与实际入口连接的唯一关联。父connect后子写、fork共享/FD转交不改变connector；后续每请求/字节作者不在该证明内。没有观察FD转交时显示unverified，不称“没有转交”。实例、名称、路径分别验收；路径能力缺失拒绝相应配置。

Linux内核connect观测仍为有条件研究候选，尚无通过真实原入口的关联算法；ADR007给出connect→active→passive→accepted SO_COOKIE的受限待验候选，两端cookie不同，不能直接比较或靠近时间/tuple猜配。T132先做connect捕获，再证明客户端socket到原accepted socket关联、连续性/复用/早退/namespace边界；任何关键正例失败即停在spike，不先建完整helper。用户选择语义不等于加载/赋权；helper RSS/CPU和kernel map内存分别计入整套预算。旧proc可见性失败和旧0/100不重评，详见[ADR007](../../docs/adr/007-p0-evidence-closure.md)。

来源：[Mihomo rules](https://wiki.metacubex.one/en/config/rules/)、[listeners](https://wiki.metacubex.one/en/config/inbound/listeners/)。

## 7. 入口计数与连接事实（R06，实测后修订）

**Decision**：按 [ADR 005](../../docs/adr/005-telemetry-replan.md)重规划完整计量：应用直接进入Mihomo，同一数据面产生原源进程、flow/attempt、计数、计时与实际线路。stock当前版本不具备完整事件输出，不以更快轮询或GOST SID时间近似拼接补齐。GOST保留可选兼容入口及累计对照，不再是完整计量必经链。

Hoopa接收journal负责已送达事件的持久化、幂等和接收重放；receipt_seq与来源source_seq分离，不能靠接收连续性证明源完整。源端需可恢复spool或durable opened census；无法恢复的尾部明确gap，不承诺故障零损失。完整源尚未实现，下一原型观测点及核验顺序见[候选契约](contracts/telemetry-events.md)。

**原组合实测进展**：原组合21项收发、9项认证和真实Codex路径已通过子断言；终末recorder有整数bytes但缺同步源PID/启动身份与setup起点。早期Linux原入口探针对owner集合不可见拒绝，未完成100连接身份验收；后续隔离候选的实际失败结果见下。接收journal已通过Write/Sync不确定、重开去重、损坏拒绝与锁测试，真实recorder接线另记运行证据；这些不等于G01–G03完成。

**隔离候选后续**：固定commit的16文件补丁已构建为独立Hoopa P0实验核心，23项定向race、vet/构建及单流smoke通过子断言。
100预登记短流实际终末字节/域名均100%正确，原始PID/名称/start为0/100、均owner_visibility，严格身份测试exit1。
T126取得可运行候选，T127完整矩阵未完成，T128/T129未启动；不得将源码实现或单路线事件当作G01–G03通过。
实际边界、未支持项及原文摘要见[验证记录](../../docs/validation/native-candidate.md)。

2026-10-23全计划复核将已知owner_visibility设为前置诊断，新增T130–T137的具体身份裁决、最小验证、缺失能力补全和新候选冻结；生产传输/回收另由T138实现。下一步不能在输入不变时直接重跑T127，详见[计划](plan.md#2026-10-23-全计划复核与当前执行入口)和[复核记录](../../docs/validation/plan-task-review.md)。未改变旧失败或规格要求。

**2026-10-23恢复范围收敛**：T133已有checkpoint/正常marker、严格接收、只读census/pending及真实小规模生命周期子证据，完整门槛仍未过。下一步提炼现有v1纯wire/epoch共同校验，接source重开/发送/receiver/reader；权威事实仍是不可变manifest/原帧/原seq。恢复报告为派生数据：opened缺final且无source writer结束证据=pending；显式恢复持writer锁并核验稳定来源后才可标epoch_ended_without_final gap，CP保留、尾部null，不声称网络结束；可信closed按原ID重放、不加第二次final。损坏/缺manifest/快照不稳定保留证据并拒绝新完整计量。

撤回未实现的强制v2 recovery_gap/跨epoch引用图和全历史恢复事件容量屏障。可选稳定reconciliation_id只绑定旧source/epoch/flow/opened和事实kind，不包含新启动/time/cutoff，不占来源/接收序或新增字节。P0可重复生成报告；生产持久gap/reconciliation、传输与回收归T073/T138。历史缺口与当前记录能力分开，旧gap不会永久阻塞新流；实际完整性/容量/current recording能力不足仍拒绝新完整计量。8MiB/32epoch等P0上限及扫描成本须按实际代码验证，不承诺无限持续运行。

**候选边界**：完整统计使用待校准的mihomo_proxy_stream_v1；旧GOST入口原始计数分源/边界保存，不合并或改历史。Mihomo tracker已有同记录的bytes/rule/chain，但stock活动API没有持久终末流。隔离实验已按具体提案构建增强候选；该候选不是stock能力或正式发行，不默认特权。

**Rationale**：同一来源的flow ID可消除跨核心猜配。身份仍须在原始socket、选路前得到；直接删GOST不能自动补出stock元数据缺少的PID/启动身份。setup需正确处理DNS、失败和延迟握手，tracker.Start或全连接duration不够。

**Alternatives considered**：GOST admission+procfs+recorder需要权限/共享FD和跨跳映射的额外证明；保留为探针，不叠加为生产数据组件。只交付累计量、无限轮询、自研代理和默认抓包均不满足本次方案。

固定来源：[Mihomo metadata](https://github.com/MetaCubeX/mihomo/blob/v1.19.26/constant/metadata.go)、[tracker](https://github.com/MetaCubeX/mihomo/blob/v1.19.26/tunnel/statistic/tracker.go)、[TCP流程](https://github.com/MetaCubeX/mihomo/blob/v1.19.26/tunnel/tunnel.go)；本机GOST build info匹配x v0.17.2，不能由报告版本3.3.1反推官方资产来源。事实和许可核验边界见[ADR](../../docs/adr/005-telemetry-replan.md)。

## 8. SQLite 与只读查询（R07）

**Decision**：账本使用 SQLite，单一写入者；入口/连接检查点与各自增量在各自本地事务内提交，跨采集源不声称原子提交。
P0 用同一最小 schema 与故障集比较 modernc.org/sqlite 和 mattn/go-sqlite3，随后以 ADR 固定一个驱动及精确版本。
两候选是限时实验，不在生产保留双驱动抽象；驱动决定未完成不阻止不依赖账本的 P1 工作，但阻止 P2 账本定稿。

**Rationale**：modernc 无 CGO；mattn 默认需要 CGO 和 C 工具链。元数据不能证明实际 RSS、产物大小或本项目恢复行为。
两者必须比较 commit 结果不确定、进程终止、磁盘满、busy、WAL/SHM 权限和三平台构建。

默认实验使用本地文件系统、WAL、foreign_keys=ON、synchronous=FULL 与有界 busy timeout；不支持将活跃账本放在网络文件系统。
不将网络、进程启动或 fsync 多资源操作放在 SQL 长事务内。最终参数按实验结果固定。

只读优先通过现有服务执行无持久副作用的查询；服务未运行时仅使用已存在、能安全只读打开的账本。
不能为查询创建 sidecar、恢复 WAL、迁移、修复或启用写连接；条件不足返回 READ_UNAVAILABLE 与原因。
不得对活跃 WAL 使用 immutable=1 冒充安全只读，也不得因 status/doctor 更新持久 last_seen。

**Alternatives considered**：预先按“纯 Go 更小”定驱动、无条件普通连接打开、自动 migrate、坏库时自动换空库，均不采用。

来源：[modernc SQLite](https://pkg.go.dev/modernc.org/sqlite)、[mattn/go-sqlite3](https://github.com/mattn/go-sqlite3)、[SQLite WAL](https://www.sqlite.org/wal.html)。

## 9. 统计、质量与自动保留（R08）

**Decision**：保存唯一去重字节事实与有版本的归属修订，进程、同名、域名、交叉视图均为投影。
分页绑定数据高水位、归属版本、保留世代、过滤及稳定排序键；清理使旧快照不可用时返回 SNAPSHOT_EXPIRED，不偷偷刷新后续页。
聚合重算同口径样本，不平均 p95；速率先在相同窗口汇总字节，恢复累计不能补逐秒历史。

探测、forward_setup 和可见请求首字节是不同测量定义。forward_setup 起于完整代理请求已解析且目标已知，止于本次可转发通道就绪；期间 DNS 等待计入。
HTTPS 不解密，不可见请求阶段标 N/A；GOST 全连接 duration 不能替代建立耗时。
自动组探测与用户只读查询分开，显式单节点 probe 也须验证不改选择；排除会清除固定选择的组级 delay 接口。

**后续质量profile**：先冻结data-model/telemetry契约与正反fixtures再改来源/解码；当前严格v1不能表达全部connector/ready/cancelled/稳定线路能力。必要时明确新实验profile并保留旧v1，不引入恢复引用图。T134先验证DIRECT TCP、固定候选HTTP CONNECT 200（通用成功语义为2xx）与SOCKS5完整成功回复；outcome保留取消与unknown，ready未证为ready_unproven/reason ready_unknown/null。flow包含target_known后的身份等待/DNS/重试，attempt独立计时；同进程单调时钟计算，业务TTFB不可见为N/A。

具体Conn产生处保存稳定node/provider/generation及flow/attempt，tracker绑定同attempt；无Conn的失败只记selection_only/unavailable，不从配置推导实际链。字节按实际n归连接段，flow总量=同边界已证段+unallocated，不重复相加。真实两线路、旧连接切组/改名及HTTP成功回复头与首段payload同包的缓冲保留仍待校准，现有003仅为SOCKS5合成协议修复证据。

保留按 UTC 观测时间的闭区间 [T−30×24h,T]，可配置。启用/缩短先预览确认，之后管理服务按已确认策略自动清理；
30 天是初始参数，自动清理初始 disabled，首次明确确认后才启用；禁用不承诺无限容量。
服务退出不安装替代计划任务，恢复后有界补清理并显示期间的待清理状态。读取只报告，不触发清理。
清理采用短事务批次，保留必要计数检查点、活跃运行引用和已清理区间；执行前核对当前策略版本。
策略延长不恢复已删区间，迟到导入/归属修订不重置年龄，跨边界无法分段不比例分摊。

**Rationale**：这是用户已确认的数据可信度和清理行为，SQLite 聚合或 UI 刷新方式不得改变它。
**Alternatives considered**：独立维度账、只存 Top N、长事务维持永久分页快照、查询顺手清理、清理后补零，均不采用。

依据：[规格澄清](spec.md#clarifications)、[流量统计设计](../../docs/traffic-statistics.md)。

## 10. 本地安全与应用接入（R09）

**Decision**：Linux 管理 IPC 首选私有目录中的 Unix socket，校验当前用户身份；核心 controller 另设随机凭据和最小允许端点。
GOST metrics 单独配置认证或私有 Unix socket，不能假设 controller 的认证覆盖它。
数据入口仅绑定本地，按固定版本支持设置 HTTP/SOCKS 认证；凭据经私有配置/目标子进程环境传递，默认展示脱敏。

**Rationale**：GOST 官方文档明确 metrics 的独立 Basic Auth 与 Unix socket 候选。本地监听不等于同用户 OS 隔离。
目录/凭据使用限制权限，Windows 使用实际 ACL；跨用户未授权矩阵作为 P0/P3 验收，不依赖端口难猜。

managed 清理冲突大小写代理变量，只从 profile 生成代理与 NO_PROXY；初始 bypass 列表为空，不隐式继承父 NO_PROXY。
inspect 必须显示该选择，用户可在本地 profile 显式配置绕过。inherit 原样继承，direct 清理声明变量；均不启动不需要的核心。
参数数组启动，保留信号、TTY、退出状态；管理 JSON 与实际应用 stdout/stderr 分离。

**Alternatives considered**：监听 0.0.0.0、只保护 WebAPI、不显示父环境冲突、把 direct 称为绕过 TUN，均不采用。
秘密及业务正文不进日志/默认 JSON/fixtures；不默认持久保存完整 argv、环境或请求 URL 查询参数。

来源：[GOST metrics](https://gost.run/en/tutorials/metrics/)；更详细的威胁边界由宪章 VIII 约束。

## 11. 基线规模、平台与许可（R10）

**Decision**：性能预算保持规格原值；P0 使用非空规则/节点与 0/1/5 计量入口测量。
规模实验依次使用 10,000 / 100,000 / 1,000,000 条历史连接、不同短连接速率与进程/域名基数，记录每连接磁盘成本、写入积压、查询 p50/p95、冷/热缓存和 RSS。
这些是实验梯度，不是已经承诺支持百万连接。由结果确定 P2 支持规模与查询时延预算并写 ADR；不能把缺少基线解释为无限规模支持。

过载时使用有界队列与明确 stale/gap，保留旧转发路径；无法确认计量记录能力时拒绝新的计量 launch。
不为达到体积/内存目标丢弃承诺明细。macOS/Windows 先标准托管 CI 的适用检查，真实退出/休眠另验，不默认接受额外收费。

**Rationale**：当前尚无完整Hoopa产品的性能基线，局部可执行原型不证明吞吐/查询预算，预设实测数字会构成虚假承诺。
**Alternatives considered**：空配置跑性能、将交叉编译作为正式支持、默认大规格付费 runner，均不采用。

源码保持当前 MIT 许可；不直接复制 Mihomo/Clash Verge Rev 的 GPL 实现。依赖和外部核心记录固定版本、来源、摘要及许可，发行时保留必要声明。
Clash Verge Rev 的配置校验、核心独立运行及界面组织仅作为产品参考，不推导出 Hoopa 已有恢复或累计计量保证。

## 12. P0 裁决表与研究完成状态

| 裁决 | 实验输出 | 通过后决定 | 失败出口 |
|------|----------|------------|----------|
| G01 原始身份与短连接 | 两同名进程、子进程、PID 复用、100 条短连接，执行前登记真值 | 固定支持的采集入口与唯一关联方法 | 停止相应门槛，具体适配 ADR；不靠轮询/全 unknown 过关 |
| G02 计数与 recorder | CONNECT/SOCKS5、长连接、最终字节、重放、世代、精度与同口径对账 | 固定 counter/事件定义与缺口恢复范围 | 替换事件来源或记录阻断，不按比例拼账 |
| G03 计时与实际链 | 建立起止、失败/超时、跨跳共同身份、多节点并发 | 固定可发布测量与线路证据 | N/A/未知如实保留；关键能力未成立不得关闭 P0 |
| G04 独立运行/接管 | 管理器崩溃、SSH 退出、spawn 中断、PID/端口冲突 | 固定平台启动/身份算法 | 报环境限制或重做托管 ADR，不误杀和重复启动 |
| G05 规则与变更 | 无计量应用规则、顺序、身份未知、分阶段开放、部分失败、过期候选 | 固定编译能力和 apply/recovery 基线 | 保持旧快照或 degraded，不发布有缺陷能力 |
| G06 驱动与只读 | 两驱动同一故障集、WAL/SHM、三平台构建、RSS/体积 | 固定唯一驱动/版本/参数 | 继续独立 P1，P2 账本不定稿 |
| G07 规模与安全 | 规模梯度、清理批次、认证/权限、0/1/5 入口 | 固定资源预算解释与入口模型 | 缩小已验证支持规模或调整实现，不丢数据假达标 |

资料研究已完成，七项运行门槛均**未关闭**。G01已执行隔离候选的100流身份断言并失败；G02/G03已有部分计数、重投与单路线事件证据，尚未执行完整矩阵。G04–G07仍未完成运行验收，不把已有子探针等同完整门槛通过；各项实际范围以[原组合记录](../../docs/validation/p0-workflow.md)、[重规划记录](../../docs/validation/telemetry-replan.md)及[隔离候选记录](../../docs/validation/native-candidate.md)为准。
整体 33 类故障矩阵仍以 [prototype.md](../../docs/prototype.md) 为验收来源，不要求 P0 提前实现全部 P2 查询/TUI。
后续设计见 [plan.md](plan.md)、[data-model.md](data-model.md) 与 [quickstart.md](quickstart.md)。
