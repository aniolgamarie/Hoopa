# US4 有序应用选路

2026-10-29，T045／T046 测试定义与 T047 纯规则模块已完成；T048 入口与 Plan 输入已按有限模块边界完成，T049 纯静态解释也已完成，T050 完整影响预览已有限验收，T051 实际产品矩阵尚未执行。当前完成 64/140。T044 的[双来源验收](us3-sources.md)证明来源、逻辑组与固定 pin，不代表应用入口选路已经实现。

## 已完成的测试定义

T045 索引（本地原件 `../../experiments/gost-mihomo/us4-rule-definition-validation.json`，不随Git上传）记录 `internal/policy/rules_test.go` 的 13 个顶层定义，覆盖数组顺序与默认目标、false 后继续命中后续 true、unknown 阻断后续匹配、all 的完整三值表及两种顺序、域名／IP／应用重叠、精确来源节点、所有目标提前检查和输入／输出所有权。

规则计算按[纯规则契约](../../specs/001-hoopa-proxy-manager/contracts/ordered-rules-v1.md)区分正面观察到的无应用／仅 IP 请求与未知事实；known=false 中的旧值不能被用作证据。已知但格式非法的事实属于输入错误，不能被 all 中的 false 兄弟隐藏。纯计算使用测试提供的进程事实，不赋予当前核心进程识别能力；P1 编译仍须拒绝未支持的过程条件。

gofmt 与独立 Go AST 语法检查通过，稳定 10 项输入和独立增量复核通过。定义阶段包测试因当时 `NewRuleSet` 等未来 API 尚未实现而预期编译 RED，**当时不计语义验收通过**；现已完成 T047 后文所列实际模块验证。原始检查和审查在 `private/us4-preparation/t045/check-02/`，首次语法检查工具参数错误及原始 RED 保留。

## 下一阶段边界

[命名入口契约](../../specs/001-hoopa-proxy-manager/contracts/named-app-entry-v1.md)保留 Intent App 和 SourceSettings v1，另行显式准备不可变私有 app binding。Plan 不分配端口、不生成凭据；编译独立验证完整绑定材料和父设置身份，并纳入 Plan／generation。每个 managed、未计量应用都有自己的入口；应用目的地仅通过统一有序规则表达。入口标识不冒充 PID。

已有固定 sources-v2 核心语法接受命名入口和 IN-NAME，是待验证的实施基础。双应用的实际路径、域名／IP 重叠、直接 node 目标、部分入口绑定失败后的完整回滚，以及真实管理器死亡后既有策略继续执行，仍需 T046 定义、T048 实现和 T051 实测。当前不能把语法支持或旧共享入口测试当作这些场景通过。

- 已完成：T045／T046 测试定义、T047 纯规则模块与各自最终复核。
- 进行中：T051 实际产品故障矩阵；T048 模块已通过，实际产品矩阵仍待 T051。
- 失败待决策：无人工阻塞；缺失未来 API 的 RED 属定义阶段预期，不作为通过结果。
- 环境不足未验证：其他平台、性能预算；本机 Linux 的固定核心、私有文件、TLS、Unix socket 与 pidfd 验证条件可用。

执行子代理使用 `gpt-6.1-sol / medium`，证据及独立复核子代理使用 `gpt-6-luna / high`；主代理负责接口、事务／生命周期边界和最终验收。

T046 的定义索引（本地原件 `../../experiments/gost-mihomo/us4-app-definition-validation.json`，不随Git上传）绑定4个实际场景定义和2个测试进程入口：公开 service Plan/Apply 的两应用／域名／IP／固定node路线，完整中断预览，缺失／失效／变化binding的零写拒绝，后续入口占用导致的完整回滚与foreign listener存活，真实manager SIGKILL下旧/新连接及重开不重复core。独立tag只选择两份新测试文件，gofmt/AST通过；定义阶段因未来API缺失而RED；2026-10-29接口已补齐且tagged编译通过，尚未执行这些实际场景。负例预登记空hop集合，任何到达origin的请求都会成为最终trace violation；独审对此的初次误读及纠正均保留。

源码取证同时证实：原sources-v2的IP-CIDR在DNS失败后会把目标IP不可用化为false，可能继续后续app/default。T048需单独解决并验证未知值传播和all中false支配，冻结独立app候选后再批准应用选路。T046已只依赖未来 `ApprovedAppRoutingCapability`／`HOOPA_P1_APP_CORE`，没有将旧c90作为应用正例。已知localhost／literal-IP场景与未知IP资格证据分列；原T044有限验收保留。

## T047纯规则求值模块

机器索引（本地原件 `../../experiments/gost-mihomo/us4-rules-validation.json`，不随Git上传）固定实现、测试与依赖15项scoped pins及独立最终审查。全policy normal/race各42顶层、236含子测试通过，其中本模块13顶层/96案例；均0失败/跳过，vet通过。构造前验证所有引用来源、组、规则/default目标和精确pin，保留owned规则树；all按false/unknown支配且不掩盖malformed-known，首个决定性unknown明确reject，所有false才用显式default。输出克隆、错误不含输入。process name/path纯事实按当前parser的非空UTF8逐字匹配，不附加控制字符过滤；这不证明真实OS进程身份。

此项只验收纯计算。原Compile的process拒绝仍通过，应用入口、完整Plan、实际核心和故障回滚仍在T048–T051。T048初版实际VM暴露无FlowID分派接线缺口，已保留失败并继续修复，不把helper测试当成二进制资格。

T048的[应用选路核心前置资格](us4-app-core.md)现已有限验收；源码helper不足的初版VM失败已修正并保留，新ELF的真实DNS/规则矩阵通过。private binding、完整Plan和产品故障矩阵仍继续，T048不提前勾选。

T048 的[私有入口与 Plan 输入](us4-app-inputs.md)现已有限验收，核心、注册表和当前control各自独审闭合。T048勾选只表示实现与上述模块检查完成，T051真实产品路径、部分绑定回滚及manager死亡矩阵仍未执行；继续T049／T050。

## T049 纯静态解释

解释验证索引（本地原件 `../../experiments/gost-mihomo/us4-rule-explanation-validation.json`，不随Git上传）记录完整policy普通/race各55顶层311项通过、无失败或跳过，vet与16项固定输入及独审通过。ExplainRules保留原顺序与精确意图目标，给出缺目标/来源诊断；无事实时不计算，有显式事实时仅输出supplied_facts推算，实际证据始终not_observed。非法声明/使用到的非法已知事实返回零报告；缺引用的合法草稿可返回明确invalid诊断，但不产生可执行RuleSet。

遮蔽只证明规范化重复谓词或早期精确AND原子集包含关系，不声称一般谓词可达性。全局4096对比较、65536原子访问/比较预算耗尽时明确budget_exhausted，保留已完成证明而不跳过验证或改写求值。旧规则与编译golden未变；未读取控制器、探测网络或改变选择。

T050的[完整影响预览](us4-impact.md)已有限验收，新Plan v2绑定旧/新代身份并集、存量/新连接处理及准确回滚目标，保留旧v1读取和精确重放。实际ELF Plan普通/race各12顶层37项通过，但未启动核心；T051继续验证真实应用运行。

### 2026-10-29 T051 normal-01 失败与修订边界

实际四场为2 PASS/2 FAIL，另2个dispatch helper PASS；guest exit1，QEMU exit0只证明传输完成。332项清单、502561字节普通文件、4个精确6ec6核心排除项与全部管理器/journal/run现场保存在 `private/us4-preparation/t051-runtime/normal-01/`，2581编译输入有独立源码归档。不得把顺序选路17个origin token、SIGKILL场10个token的部分通过算作T051完成。

一处失败是subreaper夹具遗漏正常service.stop之后被收养子进程的回收；修复只在原pidfd确认退出后按准确PID Wait4，仍要求/proc消失。另一处是原自动回滚要求closed登记，无法覆盖候选在controller就绪前终止。采用同一次直接子进程真实Wait终态和不可变intent/birth绑定的可选target_exit证据；保持既有登记/pidfd停止路径和固定核心hash。该失败批次时修订尚待复验，完成结果见下节。

原partial-bind夹具要求active全字节等于旧值，与既有basic-apply-v1契约的OperationID=本次回滚操作不符。修订只纠正该操作关联预期，旧revision/generation/manifest必须不变，并额外检查持久退出证明与公共operation.recover不产生新run。原失败断言和日志保留在normal-01编译源码归档中。

### T051 实际应用矩阵完成（2026-10-29）

真实运行索引（本地原件 `../../experiments/gost-mihomo/us4-app-runtime-validation.json`，不随Git上传）保留旧失败，并记录修复后隔离 Linux/amd64 VM 普通/race各4个真实场景和2个dispatch helper通过，0失败、0跳过。两轮各35个实际origin GET token，按场景17/3/5/10分布，非应用token为0；guest和QEMU均exit0，完整导出分别342项/565980字节及342项/566028字节，各4个固定6ec6核心副本按精确SHA排除，没有race告警或应急清理错误。2584编译/测试/fixture输入及源码原件在两轮后保持一致；race的guest加载器、依赖与GLIBC符号已核验。

| 场景 | 实际验证 |
|---|---|
| 有序选路与替换 | app/domain/IP先后次序、共享入口、准确source+node固定选择、切订阅不重绑、不通节点终末拒绝；完整替换按预览中断旧连接 |
| 失效输入 | 缺/变更私有binding、缺目标和不支持谓词拒绝，无持久/运行副作用；原应用入口继续转发 |
| 后续入口占用 | 目标启动失败有direct-child Wait持久证明，完整旧配置三入口恢复，旧流中断且独立foreign listener存活；管理器重开后公共recover保持相同回滚实例与run数量 |
| 管理器SIGKILL | 两应用已有及新连接继续转发；重开和终态恢复保留原核心身份，不重复启动 |

修复的runtime启动器普通/race各15顶层49项通过，控制层各26顶层172项通过，vet与最终独审通过。控制层历史证明及故障边界测试明确使用合成运行结果；真实核心结论仅取上述VM。规则模块16项源码摘要与T049验收相同，沿用其完整policy普通/race各55顶层311项通过的结果，不重复计数或声称重跑。

新target_exit只允许同一次live Apply使用实际直接子进程终态和原件绑定确认候选退出。持久checkpoint前崩溃不能恢复内存事实；checkpoint后但完整回滚RunID/open之前崩溃仍degraded，不重构启动票据。已登记候选继续使用原身份/回执/pidfd停止路径；没有新增按数值PID发信号的产品路径。回滚OperationID关联本次操作，旧revision/generation/manifest保持不变。

本项验收限于无计量命名应用入口及显式完整替换，不证明原应用PID归属、UDP/QUIC计量、无缝迁移、其他平台或性能预算。当前完成65/140项，继续T139最小配置/状态/诊断命令，再推进初始化与子进程接入；P1和整个产品尚未完成。执行者实际gpt-6.1-sol/medium，独审实际gpt-6-luna/high，root负责退出/持久化/恢复语义及最终验收。
