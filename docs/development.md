# 开发与协作规范

状态：当前 DB-free P1 阶段工程规范，更新日期：2026-10-29。已有 `cmd/hoopa`、Cobra CLI、严格 JSON/YAML 解析和 `go.mod/go.sum`；固定 Go 1.27.3。当前累计72/140，有限交付与未验证范围见[README](../README.md)和[任务记录](task-log.md)。本文同时保留后续阶段的工程要求；不以规范条目代表能力已实现。2026-10-24 的原型观察在下方单列为历史记录。

## 1. 设计依据与变更记录

- 实现以[架构设计](design.md)、[计量设计](metering.md)和[原型门槛](prototype.md)为基线。
- 区分用户约束、首期方案、待实验假设和已取得的运行证据；不能将资料阅读写成测试通过。
- 行为、接口、恢复策略或支持平台发生变化时，同一 PR 更新对应设计和验收条目。
- 小范围澄清直接修改原文；影响依赖方向、事务、锁、世代、切换或兼容性的选择另写 ADR。
- ADR 随决策创建于 `docs/adr/`，记录背景、备选项、取舍、不变量、证据与后果；不预建空目录。
- 新 ADR 标明替代的旧决策，保留旧记录与迁移说明，不静默改写已发布行为。
- 设计前提被推翻时暂停相关实现，由主代理判断；不通过增加重试、放宽断言或隐藏缺口绕过门槛。
- [任务记录](task-log.md)使用已完成、进行中、失败待决策、环境不足未验证四种状态。

### 继续现有 SPEC

当前产品规格位于 [`specs/001-hoopa-proxy-manager/`](../specs/001-hoopa-proxy-manager/spec.md)。完整 spec、plan、tasks、contracts、research 与配套材料随源码保留；`.specify` 的宪章、模板、脚本及 `.agents/skills` 是共享工作流的一部分。

`.specify/feature.json` 只记录本机当前特性指针，由其目录内忽略规则排除。在 `main` 等非特性分支继续已有规格时，从仓库根显式选择目录，不依赖另一台机器的指针：

```sh
SPECIFY_FEATURE_DIRECTORY="$PWD/specs/001-hoopa-proxy-manager" \
  ./.specify/scripts/bash/check-prerequisites.sh --paths-only
```

该命令只核对定位信息，不生成新规格或更新任务状态。后续执行相应 Spec Kit 工作流时继续使用同一目录选择；当前脚本的目录覆盖变量是 `SPECIFY_FEATURE_DIRECTORY`，不能用旧版的 `SPECIFY_FEATURE` 代替。

## 2. 包边界与依赖

- 当前公开 Go 源码范围包含 `cmd/`、`internal/`、`protocol/`、`experiments/` 与 `tests/`；构建入口为 `cmd/hoopa`。这不将 `internal` 包变成外部稳定 API。
- 使用单仓库、单 Go module 和一个 `hoopa` 可执行文件；当前本地 module 为 `hoopa`，仓库正式地址确定后再改为真实 module 路径，不虚构发布地址。
- Go + Cobra 为基础；TUI 按需加入，CLI 和 TUI 只适配输入输出并调用相同用例。
- 遵守 `cli/tui → control 用例 → 领域与适配能力` 的依赖方向，入口负责组装依赖。
- `policy` 保持确定性，不依赖 Cobra、Bubble Tea、GOST 或 SQL；外部进程通过适配器访问。
- Mihomo 独立运行，不嵌入为 Go 库；GOST 和 SQLite 仅为可选计量能力。
- 不计量的管理与接入必须能在没有 GOST、数据库和 TUI 的环境工作。
- 接口放在消费方，只为实际需要替换的时钟、进程、核心 API、订阅和账本定义小接口。
- 有真实边界再拆包；不预建通用插件框架、事件总线、`utils` 或未使用的多内核抽象。

## 3. Go 错误、context 与进程

- 错误逐层补充操作语境，保留 `%w` 错误链；使用 `errors.Is/As` 判断，不匹配错误字符串。
- 用例返回可分类错误，CLI 统一映射展示与退出码；业务层不调用 `os.Exit` 或打印到 stdout。
- 预期输入错误、依赖故障与内部缺陷分别处理；不吞错，不将失败的查询转换成空成功结果。
- `context.Context` 作为首参数显式传递；网络、IPC、等待和外部检查设定有界超时。
- goroutine 有明确所有者、退出条件与回收路径；取消与 deadline 不代替已定义的提交和恢复协议。
- 已开始应用的操作遇到客户端断开时，返回或保留 operation ID，依持久记录继续恢复，不能推断回滚成功。
- 管理器 context 取消不得自动杀死已脱离的 Mihomo/GOST；显式 stop 才按已验证身份停止所属进程。
- 应用通过参数数组启动，不拼接 shell；信号、终端中断、退出状态和后台后代由平台适配层处理。
- 不假定 SIGHUP 可重载；仅调用固定版本已经实验验证的能力，并正确回收应回收的子进程。

## 4. 配置、JSON 与退出码

- YAML 用户意图带 `schema_version`，严格拒绝未知字段；迁移显式、可预览，保留恢复材料。
- 不以配置写入成功代表运行态已生效；revision、generation 与 operation ID 各自保持原有含义。
- 配置应用严格遵循设计中的单写入者、持久操作记录、运行验证、active 发布与回滚顺序。
- 文件替换、核心运行与 SQLite 是不同边界；不宣称跨它们原子提交，不在数据库长事务内做网络或进程操作。
- checkpoint、delta 与 gap 在同一短事务提交；提交不确定时先按持久样本身份消歧，再决定重试。
- 当前 JSON v1 的 schema、错误码和兼容性契约位于 [`specs/001-hoopa-proxy-manager/contracts/`](../specs/001-hoopa-proxy-manager/contracts/api-v1.schema.json)；[服务生命周期](../specs/001-hoopa-proxy-manager/contracts/service-lifecycle-v1.md)、[最小 CLI](../specs/001-hoopa-proxy-manager/contracts/minimal-cli-v1.md)及[本地 init](../specs/001-hoopa-proxy-manager/contracts/local-init-v1.md)分别约束已实现路径。整体 CLI 规划不等于命令均已交付。
- 管理命令响应 stdout 仅一个 JSON 对象，带版本、请求标识、结果、错误和 warnings；本地 init/app inspect 成功报告允许 `request_id:null`，不放宽管理 RPC 的关联校验。`run` 是独立的原始应用 IO 路径，不套管理 JSON。
- 写请求带预期 revision；未知主版本明确失败。新增兼容字段不能改变旧字段类型、含义或缺失语义。
- 稳定错误码供自动化判断，错误文本可改进；JSON 输出不混入进度、颜色、交互提示或子进程输出。
- 当前管理响应退出码：0 成功，2 参数/版本/能力拒绝，3 冲突或请求身份复用，4 读取/依赖/操作不可用，5 完整性/恢复或无法验证的响应，6 未确认授权，7 取消。精确错误码到退出码的映射以 [`service.ExitCode`](../internal/service/api_linux.go) 与对应契约/测试为准，不能将所有非零结果归为同一类。
- stopped、stale、degraded 等是可表达的业务状态，查询成功读取它们不必成为命令执行失败。
- `run --app ID [--mode …] [--meter] -- ARGV...` 按[应用启动契约](../specs/001-hoopa-proxy-manager/contracts/application-launch-v1.md)透传目标退出码，信号为128+signal；记录区分普通退出与信号，不能套用管理命令错误含义。
- wrapper 启动失败、有限 exec 失败与目标已退出分别报告。当前 exec 格式/权限/不可用为126、未找到为127，并有固定诊断与私有失败通道；普通应用126/127不据此重分类。终末记录或收尾失败返回5并保留已观察结果，精确 CLI 映射见 [`run_linux.go`](../internal/cli/run_linux.go) 和[派发契约](../specs/001-hoopa-proxy-manager/contracts/application-launch-dispatch-v1.md)。
- 应用 stdout/stderr 保持原通道；结构化 launch 记录另行查询，不与目标输出混流。

## 5. 本地边界与敏感信息

- 默认仅监听 loopback；controller、metrics 和本地 IPC 使用私有访问保护，不能只依赖端口隐蔽。
- loopback 不能隔离同一用户下的所有进程；应用 profile 与 service 身份不等于 OS 安全身份。
- 目录、配置、快照和凭据采用限制权限或平台 ACL；诊断不能顺手修改权限或修复运行状态。
- 订阅 URL、节点认证、controller secret、应用内容不进入日志、默认 JSON、诊断包或 Git fixtures。
- 使用合成数据测试，保留脱敏前后可追溯摘要；原始敏感证据留在受控私有目录。
- 远端订阅只提取允许的节点字段，不覆盖本地规则、路径、监听、脚本、控制面或 TUN 设置。
- `status`、`doctor` 只读：不创建目录、启动服务、更新订阅、执行选优、探测健康、迁移或自动恢复。
- 核心查询采用无副作用允许列表，不能因为 HTTP 方法是 GET 就认为只读。
- 账本优先读取管理服务快照；离线读取不能创建、回放或写 journal，不对活跃 WAL 强用 `immutable=1`。
- 无法证明安全读取时明确报告不可用；修复、迁移和强一致历史查询使用单独显式操作。

## 6. 测试与故障证据

逐轮运行报告、准备/诊断收据和安装回执是本地材料，不随 Git 同步。下文“本地原件”代码路径仅定位原工作树中的忽略文件；正式验收 Markdown、六份最终 acceptance 和批准/构建身份记录保留。source5 等受控 gate 复核还要求匹配的本地报告及私有原件，公开源码或保留的最终结论本身不足以复验全部证据。不得生成替代 PASS、改原 hash 或将缺材料断言静默跳过。

- 单元测试覆盖策略图、确定性编译、严格解析、环境构造、世代判断和差量幂等；时间通过小接口控制。
- 黄金文件只保存合成配置、订阅和指标；更新预期必须解释行为变化，不能机械接受新输出。
- 适配器测试冻结外部核心版本，验证指标标签、错误响应、能力边界和配置检查结果。
- 集成测试使用私有实验目录、loopback 端口和已核验进程身份，不全局按名称 kill。
- 配置应用注入每个持久化边界崩溃、端口冲突、active 写失败与回滚失败，核对恢复后的完整运行态。
- 计量注入样本重放、counter 重置、series 消失、磁盘满、COMMIT 不确定和管理器重启，核对无重计与显式 gap。
- 进程/域名统计覆盖同名并发、PID 复用、子进程归属、极短连接、域名/IP 多对多、未知归属、晚到修订与口径对账；合并、交叉查询、排序分页和 Top N 使用同一数据版本核对不丢行、不重复、不丢未知项。
- 质量统计核对时延边界、超时/失败/N/A、单调计时、p95 合并、窗口流量覆盖及实际连接链；probe 与业务数据分离，查询不清除固定选择。应用/进程规则覆盖无 GOST 入站、统一顺序、缺身份三态、固定目标失效和管理器崩溃后的策略执行。
- 只读命令在时钟与后台任务受控的对照环境比较文件树、hash、mtime 和进程集合；读请求不新增持久审计或更新时间，atime 与无关后台写入单列。
- 基础功能单独验收无 GOST / 无数据库路径；不能靠完整开发环境掩盖可选依赖被误设为必需。
- Linux 真实应用验收覆盖 SSH、信号、后台后代、旧入口排空、长连接与管理器接管，不能用 mock 代替。
- 按[原型故障矩阵](prototype.md)保存版本摘要、事件顺序、断言、脱敏日志与未验证项，性能目标附测量口径。

### 历史记录：2026-10-24 原型阶段

以下按当时的制品、任务数与边界保留，含原失败和当时的未完成结论；不代表当前72/140状态。后续有限验收另见[任务记录](task-log.md)，不回填或改判这些原运行。

T132 在 2026-10-24 取得[隔离 Linux VM 两流校准证据](validation/isolated-linux-lab.md)，对应机器记录（本地原件 `../experiments/sourceidentity/isolated-lab-validation.json`，不随Git上传）。固定 QEMU 9.1.2/KVM 与官方 WSL Linux 6.18.54.1，无网卡或宿主共享；真实 BTF、cgroup2、双 cookie、load/attach 与两个客户端 PID 83/84 到原 accepted cookie 的核对通过，observer/driver 均 exit 0，停止计数为 admitted/completed 35/35、active=0。原样导出的完整材料经明确操作者范围声明后得到 `consistent_unverified`，该声明与检查结果不认证连续隔离或正式身份能力。

本次为实验能力验收：修复私有 libbpf 0.6 读取新 BTF 的失败、TEST_RUN opts ABI 及 map 对象标志与 FD 只读权限的分离核验；本次受影响 loader 58 项与 checker 20 项测试通过，区别于历史驱动准备阶段的 58 项。5 个离线材料变异和 2 个真实 CLI 参数反例被拒绝，不能作为真实停止交错、漏挂、范围或复用故障矩阵。原始失败、旧 manifest 与正式 G01 **0/100** 保留，不增加正式分母；T132 未完成，139 项完成 21 项，T137/P0/P1 不放行。首选 6.6 基线尚未验收，本次不声明正式 Linux/Windows 支持；当时的下一步优先级按下述真实故障观察收紧；后续在线实验进度另列，完整启动身份/名称能力仍未验收。二进制和原始日志仅留 `private/`，不进入 Git。

随后按事先登记执行了三个真实 VM 负例，完整材料仍见[同一验收记录](validation/isolated-linux-lab.md)及机器摘要；它们与前述 5 个离线变异、2 个 CLI 参数反例分开：

| 真实场景 | 实际结果与验收边界 |
| --- | --- |
| wrong-driver-scope | 故意不放置 driver，driver 1 / observer 0，scope 预检拒绝，零 fixture；命中原定负例 |
| observer-terminated / SIGTERM | 实际 driver 1 / observer 1，observer 按 handler 清理并写出 interrupted result，driver 拒绝已停止 observer；不符合原登记的 driver 0 / observer -15 / 缺 result，保留预期失配，不改判通过 |
| 独立登记的 observer-killed / SIGKILL | driver 0 / observer -9，三个 fixture 均 exit 0，无 observer result，最终 calibration failed；只命中“后验拒绝缺失材料”的有限判据 |

SIGKILL 后 fixture 仍实际通信，直接反证“最终拒绝等于路由前保护”。当前限定 driver 仅用于诊断校准，**不得用于生产路由**。该历史观察推动了后续有限在线准入原型；在该阶段，核心原 socket 接线与 FD/link/map 所有权仍待后续实测；长期回收、重启及完整身份矩阵继续另验，已取得的两个有限负例不能替代这些要求。SIGTERM 失配、独立 SIGKILL 登记和所有原始失败均保留；T132、T137/P0/P1、139/21 与正式 G01 0/100 不变。

有限在线准入随后取得[两轮真实矩阵证据](validation/connector-online.md)：同一实验服务拥有 listener、两条 link、maps 与原 accepted socket，Q 按原 socket 见证查同步绑定，grant 前无应用 I/O。v1 八场景取得 10 条允许配对和六种拒绝条件；v2 的线程 connect、父 connect/子写、SCM_RIGHTS 转交及真实 tuple 复用四场景另取得 4 条允许配对和一种复用拒绝条件。合计 **13 次 VM、12 个有效场景、14 条有限允许配对、7 种拒绝条件**；tuple 场景含一允许及一拒绝。首次 thread 虽已回显，仍因私有 image 缺 `libgcc_s.so.1` 以 -6 退出，保持失败；仅补 image 依赖后新登记重跑通过。host checker 保留原 v2 失败，单列替换 thread，不将失败轮的交换算入允许配对。

线程实际 TID 与 TGID 分列、fork/SCM 后仍保留原 connector，均与独立客户端 oracle 核对；仅在 100 Hz 且 time namespace 两项偏移为零时换算启动 ticks，采样 comm 一致不认证原子改名或路径。tuple 第二个进程实际 connect 成功、四元组相同而 PID/cookie 不同，服务端无 identity/grant、应用读写零且 fault 非零。服务 SIGKILL 后，随后启动的客户端 connect refused 只验证同 owner 实验入口终止，**不是管理器离线转发验收**。该在线实验阶段仍未接Mihomo；后续有限核心接线见下段，长期回收、重启、跨 namespace、exec/名称矩阵仍须另验。旧 SIGTERM 预期失配和 observer SIGKILL 后验拒绝均不改判；T132/T137 未完成、139/21、G01 新增 0 及旧 0/100 保持，身份能力仍 unverified。


2026-10-24 后续[有限核心接线](validation/connector-core.md)已将原 Accept→Admit→metadata 纯值复制→同 flow profile3 opened 接入真实 HTTP CONNECT/SOCKS5。`exec_core` 在同一 PID exec 移交11个FD，Go核验并恢复CLOEXEC；旧process unknown、正式身份能力false保持。严格字段、BOOT与进程质量单调时钟、原帧恢复及不展示身份的RecoveryFlow见[v3契约](../specs/001-hoopa-proxy-manager/contracts/source-connector-v3.md)，运行分层见机器验证（本地原件 `../experiments/sourceidentity/core-validation.json`，不随Git上传）。

0009独立修复early-half-close真实回归：common.Cast可以穿过generic tracker，但无Upstream的Hoopa计量wrapper需显式转发CloseWrite控制，不能为修复而暴露绕过计数的读写fastpath。保留首次失败、修复输入及原请求复验，不能将只改fixture的成功当成恢复半关闭。新[manifest](../experiments/mihomo-events/core-connector-candidate-manifest.json)记录001–009干净应用/完整树与权限、Go1.27.3离线构建和最终SHA256；无版本ldflags的默认打印 `1.10.0` 不等于固定源码v1.19.26/commit。

已核对最终候选正常/原半关闭两协议双向33B、重开旧文件hash不变/两世代、三类移交错误零source/零listener。其余drain、范围外与核心退出场景以新验收记录的实际阶段为准，不回填旧binary；核心SIGKILL不能替代T136管理器退出/接管。名称非原子unverified、路径unknown；完整scope/namespace、exec/改名、长期map回收、生产权限部署和完整身份仍待验，T132/T137、139/21、正式G01新增0/旧0/100与P0/P1门槛不变。

## 7. 工程验收与 CI

构建候选固定为 Go 1.27.3。执行前核对本地版本，并为每条 Go 命令显式设置 `GOTOOLCHAIN=local`，防止自动选择或下载工具链；本地版本不满足时记录实际版本并交主代理决策，不擅改候选、不自动安装。

```sh
GOTOOLCHAIN=local go version
```

以下是公开源码检查命令，不是本次文档整理新取得的通过结果。带 `us4integration`、`onboardingintegration`、`us2integration` 标记的实际矩阵不在普通测试选择中；部分旧集成也会因缺少固定制品而跳过。真实核心/进程测试须按各专项验收提供批准制品、权限与隔离环境，未执行或跳过均不算通过。

```sh
gofmt -l ./cmd ./internal ./protocol ./experiments ./tests
GOTOOLCHAIN=local go vet ./cmd/... ./internal/... ./protocol/... ./experiments/... ./tests/...
GOTOOLCHAIN=local go test ./cmd/... ./internal/... ./protocol/... ./experiments/... ./tests/...
GOTOOLCHAIN=local go test -race ./cmd/... ./internal/... ./protocol/... ./experiments/... ./tests/...
```

Go1.27.3 的 `testing.TempDir` 优先使用 `GOTMPDIR`。Store 的真实文件测试同时要求私有目录有安全祖先；仅把 `TMPDIR` 指向 `/tmp`，却保留位于不安全祖先下的 `GOTMPDIR`，仍会被拒绝。不要为测试放宽路径 guard。需要显式临时目录时，例如：

```sh
test_tmp=$(mktemp -d /tmp/hoopa-tests-XXXXXX)
GOTOOLCHAIN=local GOTMPDIR="$test_tmp" go test ./internal/config
```

若编译临时文件因容量原因放在仓库盘，应单独给测试进程设置安全的运行目录，例如通过 `go test -exec "env GOTMPDIR=$test_tmp" …`，而不是把编译盘路径当成 Store 的安全运行路径。不要覆盖 `HOME` 来迁就测试；失败原件保全、进程收尾后再清理自己创建的目录。权限或路径环境失败与业务测试失败分别记录。

- gofmt 检查覆盖当时全部 Go 源码目录，输出必须为空；新增目录时扩展检查范围，不假定仅有 cmd/internal。
- 在含忽略的 `private/` 证据/工具链副本的工作树中，Go 的 `./...` 不遵循 `.gitignore`。本地全项目检查显式使用当前全部公开源码范围 `./cmd/... ./internal/... ./protocol/... ./experiments/... ./tests/...`，格式检查覆盖同一组目录；新增公开 Go 目录时同时扩展范围。干净 checkout 可继续使用 `./...`。不能为使检查通过删除冻结原件或为私有工具链补根模块依赖。
- 先运行受影响测试和必要集成案例，再完成规定检查；race 需受支持工具链，环境不足单列记录。
- 文档变更检查内部链接和 `git diff --check`；无代码变化无需虚构 Go 测试结果。
- CI 先建立 Linux 标准托管 runner；macOS/Windows 使用标准 runner 做构建、单元测试和适用回环集成。
- 公开仓库标准 runner 免费与私有仓库额度规则不同，费用依据见 [GitHub Actions 官方说明](https://docs.github.com/en/billing/concepts/product-billing/github-actions)。
- 结合仓库类型核对额度、预算和产物保留；不启用额外付费的大规格 runner，不默认接受超额计费。
- CI 通过只证明已执行项目；平台支持表分列构建、回环、真实应用、退出/休眠和性能证据。
- 未取得真实运行证据的平台标为未验证或实验性，交叉编译成功不能升级为正式支持。


## 8. 依赖、许可与发行

- Go 依赖固定版本并提交 [`go.mod`](../go.mod) / [`go.sum`](../go.sum)；当前直接依赖为 Cobra v1.10.2 和 go.yaml.in/yaml/v3 v3.0.4，间接依赖亦由清单固定。升级单独评审变更记录、兼容性、许可和相关回归结果；当前基础路径未引入 TUI 或 SQL 驱动。
- 后续获准引入或更新依赖时使用已经批准的精确版本，不使用 `@latest`。以下为维护流程示例，可能访问模块网络/缓存，不是本次整理执行过的命令：

  ```sh
  GOTOOLCHAIN=local go get github.com/spf13/cobra@v1.10.2
  GOTOOLCHAIN=local go mod tidy
  GOTOOLCHAIN=local go mod download -json
  GOTOOLCHAIN=local go mod verify
  ```

- 审查 `go.mod/go.sum` 的实际 diff：确认直接版本、传递依赖与许可；将下载元数据中的版本、模块/清单校验和及命令结果记录到[任务记录](task-log.md)，一起提交两个文件。`go mod verify` 核对本地缓存完整性，不能代替许可证审查或真实功能/平台验收；不通过关闭校验、改代理或忽略 checksum 差异获取通过。
- `.gitignore` 覆盖根目录产物、外部核心及私有配置/状态/证据目录；真实实验材料留私有位置，合成 `testdata/`、源码和 `go.sum` 保持可见，不用全局 YAML/JSON 忽略规则隐藏测试材料。 BPF/object 编译结果、静态/动态库及临时备份/补丁拒绝文件也属于本地产物；C/BPF 源码与文本补丁仍保留。
- 外部 Mihomo/GOST 记录路径、版本、摘要与源码 tag/commit；不引用可变 latest，不在运行时自动更新核心。
- Git 保存源码及构建定义；构建产物、发行压缩包、外部核心可执行文件和运行数据加入忽略规则，通过发行附件或 CI 制品分发产物。
- Hoopa 消费 Rotom 管理的明确二进制，不接管其内部状态；核心版本不满足已验证能力时明确拒绝。
- 仓库保留现有 [MIT LICENSE](../LICENSE)；阅读上游行为不等于获准直接复制 GPL 源码进 MIT 文件。
- 当前 Spec Kit 工作流、Mihomo 文本补丁与 Go 依赖的来源及许可证原文见[第三方清单](../THIRD_PARTY_NOTICES.md)；根 MIT 许可不覆盖这些上游材料。
- 不直接搬入上游 GPL 实现；若计划复用代码或随包分发核心，先记录来源、版本、许可和拟发行方式并评估义务。
- 每次发行保留直接/传递依赖、外部核心、版权声明及必要许可证清单；独立进程也不能省略发行审查。
- 这些是工程记录规则，不构成对具体组合或发行方式的确定法律结论。

## 9. PR 与多模型协作

- PR 保持单一可评审目标，说明触发场景、行为变化、不变量、执行命令、结果与未验证限制。
- 先完成实现、相关验证和常规修复再交接；最终审查针对写入完成后的稳定 diff 或 commit。
- 主代理沿用会话模型与 effort，负责需求、设计取舍、事务/锁/恢复语义、异常决策与最终验收。
- executor 按角色配置实现明确方案、测试并修复；scout 按角色配置只读搜集来源、日志和调用链证据。
- 最多同时两个子任务，通常一个写代码、一个独立收证；共享文件指定单一写入者，子代理不再派生。
- 任务包明确目标、输入路径、可写范围、接口与不变量、验收命令和升级条件，并提醒保留他人改动。
- 前提失败、事务/并发语义不清、需扩大范围，或同一失败经过两种有效尝试仍未解决时，升级主代理重新决策。
- 不因普通修复向用户反复索要继续；仅缺必要人类决策或授权时询问，环境不足不阻塞无关工作。
- 任务记录保留四种状态及证据路径；汇报实际执行模型与检查结果，子代理不可用时明确说明。
