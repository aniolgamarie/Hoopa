# T034 进程外故障见证与实际恢复增量

日期：2026-10-27。机器索引（本地原件 `../../experiments/gost-mihomo/p1-traced-fault-validation.json`，不随Git上传）保存冻结源码与证据摘要。此记录覆盖限定 Linux/amd64、固定基础核心和两套原始双入口配置。T034 仍未完成，总任务保持 47/140；完整 `productHarness` 构造器仍明确失败，以下场景不冒充整张矩阵通过。

## 见证与默认行为

普通构建中的 `internal/p1testhook` 在编译期禁用。仅显式编译 `p1integration` 的 `cmd/p1probe` 能安装测试观察器；没有环境变量、用户命令或产品 JSON 开关开启故障注入。前端仍使用普通 `hoopa`，测试管理器执行真实 service/BasicApplier。有限 Apply 来源保持完整配置原文，没有添加通用产品编译器。

独立监督进程通过继承的 Unix `SOCK_SEQPACKET` 接收事件，核对内核 `SCM_CREDENTIALS` 的发送 PID/UID、流 ID、序号与原始字节摘要。事件、来源收据和决定先经文件及父目录同步，才发送 ACK；journal 成功发布后，监督者另行只读捕获当时真实 operation 文件。杀管理器时先保留切点，再对自己创建的直接子进程 SIGKILL，等待实际退出后保存独立杀进程收据。

文件操作前的 checkpoint 与操作成功后的观察分开。临时文件可能已创建，不能把 checkpoint 当完整持久化证明。Store checkpoint 没有直接携带操作 ID，后续适配器必须结合串行调用作用域和原件绑定；journal 的 `publishing` 也不等于 active 已发布。观察器失联会立即终止测试管理器，不能转成一次虚构的业务回滚。

传输的三秒上限覆盖 socket/可取消等待，不承诺打断内核文件同步；Close 仍等待正在进行的同步。目录与可执行文件属于可信、同 UID 的受控夹具，不声明对恶意同 UID 或 mount 替换的安全隔离。

## 按操作语义检查顺序

测试 DTO 从 Store 的真实编码原文投影，保留 nullable `TargetRevision`，完整列出持久操作，不靠省略接管、恢复或 service.stop 来满足旧断言。

| 操作 | 版本与实际副作用 |
| --- | --- |
| Apply、从停止态启动、显式停止 | 检查各自新目标版本及真实发布顺序 |
| 自动一次重启 | 不产生目标配置版本，但需要新运行身份和权威发布 |
| 已运行实例接管 | 保持原版本/运行，重新证明归属，不新增核心或 active 发布 |
| 显式恢复调用 | 调用自身与被恢复目标分别保留，结果不混淆两者 ID |
| service.stop | 保持核心意图；服务入口关闭事实与 OS 退出分别检查 |

恢复调用中的目标 SHA/phase 是当时看到的版本。非终态调用允许引用独立保存的真实历史原文，但必须完整匹配不可变 Identity/Request；提交的恢复调用仍绑定当前终态目标。历史版本不能从最终 steps 重新伪造。本次绑定校验不额外声称已证明所有历史版本之间的完整因果顺序。

## 实际场景 V1

普通轮 `private/p1-trace-preparation/actual-03` 和竞态轮 `race-02` 各运行两顶层、五子场景，共七个 PASS 节点，零失败/跳过；分别 63.12 秒和 89.91 秒。每轮八个真实核心、四次管理器 SIGKILL；八个核心均由原 pidfd 确认退出且 `/proc` 消失，无测试清理补发信号。已由原父进程回收的候选与监督者回收的孤儿分别记录。

| 故障窗口 | 实际结果 |
| --- | --- |
| Stop 意图落盘后杀管理器 | 尚未给核心发信号；原长连接和新连接仍转发；显式恢复完成原 Stop |
| Stop 核心退出确认后杀管理器 | 核心已退出；恢复不重复发送信号 |
| Stop active 发布后杀管理器 | 停止意图已发布，日志未提交；恢复完成原记录，不重启核心 |
| 真正监听器占用目标入口 | 正常 Activate 遇到绑定冲突；停止候选，以新 RunID/nonce 恢复旧完整配置/版本，两入口实际回到旧节点 |
| Apply active 发布后杀管理器 | 新目标已运行、日志处于 publishing；重开不修改状态，显式恢复提交原目标，无额外启动 |

每次服务重开前后对完整持久目录作只读快照比较。恢复调用和原目标分别保留原文；故障命中以事件、实际 kill/wait 收据或实际监听器证据为准。上游随机 payload/echo 仅作为转发与节点见证，不是累计流量计量。

竞态轮对测试监督进程与 `p1probe` 均启用 `-race`；普通前端 CLI 与固定核心未加 race。输入在执行前复制并封存，两轮 before/after 摘要一致；后续重启观察增量另行登记，不把本轮证据改标为新源码实测。

## 定向检查与保留失败

- 顺序/投影策略最终普通与 race 各 48 PASS 节点，tagged vet 通过。它们检查策略，不证明产品实际执行顺序。
- 进程外传输及监督 helper 的普通、race、vet 均通过；独立 driver 覆盖空闲、错误 ACK、来源不符、最后无效帧、未命中 selector、实际 SIGKILL 等情况。
- V1 普通生产构建六包回归：普通与 race 各 1,320 PASS 节点、12 个可选真实核心场景跳过、零失败，vet 通过。12 个跳过未计为实际核心通过；实际五场景单独执行。122 个受影响源码摘要在该回归前后不变。
- 首轮实际 Stop 测试误把 CLI 的合法 `READ_UNAVAILABLE` 封装当作必须抛出的传输错误。修正测试后通过，`actual-01` 失败保留。
- 最初两次默认回归 runner 的临时目录选择触发既有安全路径拒绝，记录为无效执行环境；最终改为在仓库编译、从 `/tmp` 运行测试，未放宽生产目录规则。

## V2：有界重启与原 pidfd 的 EINTR 处理

V2机器索引（本地原件 `../../experiments/gost-mihomo/p1-traced-restart-validation.json`，不随Git上传）保存独立冻结输入。最终 `actual-04` 与 `race-03` 各三顶层、六场景、八个 PASS 节点，零失败/跳过，分别 92.55/129.37 秒。每轮十一核心、四次管理器 SIGKILL、两次监督者通过原 pidfd 的核心 SIGKILL；十一核心均已退出且 `/proc` 消失，没有 cleanup 补发信号。五个旧场景与新重启预算在同一冻结源码下完整复验，169 个输入及源文件集合前后均一致。

新场景显式从停止态普通 start 授予一次预算；第一次核心退出后仅产生一个新 RunID/nonce，配置 revision/generation 不变，两入口仍真实转发；第二次核心退出后预算耗尽，状态确认停止，查询不触发第三次启动。源退出事件位于原 pidfd 等待成功之后，不冒充重启请求已获接纳；记录时仍持有 watch 的 attempt 串行锁。

独立原生实验对自己的线程发送 SIGURG，普通/race 各运行三秒，实际确认 timeout=0 的 pidfd poll 也可能返回 EINTR。原 primitive 直接传播该中断，会使调用者的观察/等待失败。修复仅重试同一原 pidfd 的 poll，最多十六次，每次前检查原 context；其余 errno、未知 revents 仍失败，只有 POLLIN/HUP 可确认退出。不重试业务操作，不放宽身份证明或原有超时。取消与最后一次失败 syscall 并发时，实际 EINTR 可以成为返回结果；没有额外 poll 或授权。

修复后平台包普通/race 各 115 PASS 节点、vet 通过；runtime/control/service 普通/race各 695 PASS 节点、12 个可选实际核心场景跳过，vet 通过。48 个平台验证输入和 3,684 个上层依赖/工具输入分别在各自执行前后核对一致。这些是分包执行结果，不与 V1 CLI/config 的旧版本检查拼成一次全套通过。

历史 `restart/race-02` 的 active 发布后核验失败、`restart/race-06` 的 live core/watch inactive 没有捕获原始 errno，故不能事后宣称已证明它们都由 EINTR 导致。其他诊断轮通过没有被用来覆盖失败；当前接受的是已明确的 primitive 修复及固定新版本六场景结果。

## 只读数据与真实操作回执接线

独立机器索引（本地原件 `../../experiments/gost-mihomo/p1-product-projection-validation.json`，不随Git上传）记录 `product-projection-core/actual-01` 与 `race-01`。两轮各一个真实场景、两个核心，分别 23.75/36.05 秒，零失败/跳过；172 个源码与输入在各轮前后保持一致。该场景覆盖 Apply、普通 Start 接管、独立 Recover 调用、两次 Stop、从停止态重新 Start 和 service.stop，共七个操作和七份实际 CLI 回执。恢复调用和原目标分别绑定，未借用目标 ID 冒充恢复回执。

完整操作清单由实际 Store 枚举并逐份读取原文，包含终态操作；不从 status 的有限 pending 列表补造。Bundle 投影保留完整文件摘要、原始规则顺序、组选择及入口认证摘要。预期节点仅是静态配置预期，仍需独立上游证据才能声称实际走了该节点。

两次新鲜 owner 观察都与独立保存的原进程句柄匹配；核心停止后的观察返回未知/错误。它们使用真实 Controller/Ownership.Observe，生成注明来源的观察收据，不称为 HTTP 原始报文、完整 managed activation 或时延测量。四组只读前后快照均不变；两个核心最终由原 pidfd 确认退出且 `/proc` 消失，没有清理补发信号。

读取组件定向检查分别为 bundle/operation 普通与 race 各 18 PASS、owner 观察各 7 PASS，均通过 tagged vet；各自冻结源码单独保存。独立 scout 核验真实场景每轮 1,625 份证据引用与全部 172 份冻结输入，未发现该范围的必修问题。后续历史事件投影和顺序策略修订另行验证，不把本场景改标成完整工厂或故障矩阵通过。

历史组件随后在其冻结版本完成普通/race 各 23 PASS、vet 通过。它读取每次真正发布时保存的日志原件，校验 wire、peer、序号、操作身份和摘要；不会从最终 steps 重建历史。再经角色策略修正后的最终组合检查，普通/race 各 66 PASS、零失败/跳过、vet 通过，192 个源码与输入及文件集合前后不变。两份已有真实场景归档各含 473 条事件、47 份独立历史日志，投影为 64 个边界、2 次启动、2 次产品停止信号和 7 个操作；这是离线原件检查，没有新增核心执行。

顺序策略分别保存候选 RunID、回滚 RunID 和当前恢复目标。候选先验证成功、随后回滚时，早先候选证据仍绑定原候选；`rolling_back` 或其后 `degraded` 的恢复证明选择回滚实例，缺少该实例不退回已停止候选。新增三种 phase 的正反例为明确标注的合成策略测试，尚不算该故障的真实执行。普通 Start 的接管前证明也须绑定原运行，不能提前赋予操作接纳身份。

投影拒绝未闭合请求、缺少实际接纳事件的当前操作及未知 workflow 切点。连续序号本身不能证明没有遗漏整个流或尾部；当前归档通过完整文件索引绑定原数组，未来现场适配器仍需保存采集时的完整流与完成状态。

## 流量接线、回执语义与容量拒绝

组件机器索引（本地原件 `../../experiments/gost-mihomo/p1-adapter-components-validation.json`，不随Git上传）分别绑定三个冻结版本，未拼成一次全树验收。流量组件普通/race 各 10 PASS、vet 通过；每轮一个实际核心、两个经原进程句柄证明服务端归属的入口连接、三次实际 payload/echo。继续长连接使用原连接，错误的运行/入口/目标选择不能重拨；两个入口均有实际上游节点见证。payload 字节不含 framing/CONNECT，也不是累计计量账本。原核心在关闭测试流量组件后仍活着，随后通过产品 Stop 退出，无清理补信号。

回执组合检查普通/race 各 95 PASS、vet 通过，194 个输入及文件集合未变；读取已有真实七操作回执与事件，未新增核心执行。独立保存调用请求 ID、原始 wire outcome/error code 和从真实记录推导的逻辑结果。未提交的恢复调用以自身 pending/degraded 为准，不能被旧目标的终态覆盖；拒绝接纳的请求没有伪造的逻辑终态。不可出现的恢复调用 rolling_back/rolled_back 被拒绝。

容量错误现以 `OPERATION_CAPACITY_EXHAUSTED`、退出码 4、不可自动重试返回，schema 和 CLI 契约同步。普通/race 各 40 PASS、vet 通过。实际服务先产生一个真实已提交的 service.stop 回执，再明确用 1,023 条 Prepared 元数据种子填满容量：新 ID 不接纳，原 ID 原请求仍逐字节回放，改内容重用 ID 返回 `REQUEST_ID_REUSED`，服务继续运行且持久树不变。种子不算成功产品操作或核心执行。

## 统一适配器的四个实际故障场景

统一适配器索引（本地原件 `../../experiments/gost-mihomo/p1-product-harness-validation.json`，不随Git上传）记录 `harness-fault-01` 与 `harness-fault-race-01`。两轮使用相同 184 份冻结输入，各四个 PASS、零失败/跳过，分别 96.27/150.00 秒；tagged vet 通过。场景包括真实入口占用后的完整旧配置回滚、一次授权核心重启、Stop 意图落盘后管理器 SIGKILL 并显式恢复、没有重启授权时原核心退出且没有新启动。每轮十一核心均由原 pidfd 确认退出且 `/proc` 消失，无 cleanup 补发信号；独立 scout 核验各 5,528 份唯一原件引用。这里只验收四个场景，T034 仍未完成。

无授权分支保留公开 Runtime 的 unknown 状态和原 desired running 意图；进程确已退出来自完整原 pidfd 清单的独立观察，不把“查不到”推成已管理停止。测试原 5 秒窗口曾在重启提交后、状态证明返回前超时，现将集成观察上限设为 20 秒；这不是产品延迟或性能预算。随后暴露的 teardown 多余 Stop 失败也保留：只有完整原句柄清单证明全退出时才跳过产品 Stop，并留退出清单收据。

上述旧冻结版本在执行中核验 Apply worker 的 Wait、退出码和真实回执，但没有单独保存退出码原件；后续源码已补 worker-exit 收据，关联独立请求、原 stdout、响应、实际 WaitStatus，不能据此补称旧版本已具有该原件。后续六切点首次运行中五个 operation 阶段通过，candidate/prepared 因将重新 Apply 错当 Recover 而失败；整轮不算通过，正在修正后复验。

## 持久化、I/O 与启动窗口增量

增量索引（本地原件 `../../experiments/gost-mihomo/p1-product-fault-increment-validation.json`，不随Git上传）分别保存各批次输入，T034 尚未完成。六个发布切点普通/race 各 7 PASS 节点（六子场景加父项）：候选发布但尚未接纳，以及日志 applying、publishing、committed、rolling_back、rolled_back。两个版本均绑定相同的 184 份输入；race 从普通轮保存的完整源码/夹具快照构建，主工作区后续改动不算这轮输入。每轮十八实际核心全部退出且 proc 消失；保留 degraded 诊断的两例最终由测试夹具使用原 pidfd 收尾，共两次补信号，不能称为产品 Stop 成功。候选未接纳后的再次 Apply 只验证新提交，不要求不存在的恢复调用。

四个 active I/O 切点普通轮 5 PASS 节点、十二实际核心，无清理补信号，race 待补。故障通过真实测试编译 checkpoint 的 InjectError 注入，分别为临时写、文件同步、rename 前、目录同步前；不是声称观察到内核真实 EIO。选择器核对独立请求 scope、当时真实日志和 publishing phase。pending 的目标由新鲜进程/controller 观察证明已打开，同时公开管理状态保持 unknown/degraded；显式恢复后才验收提交状态。

启动窗口普通轮 3 PASS 节点（两子场景加父项），187 份冻结输入未变，race 待补。prelaunch 在完整、已终止的管理器流中证明同步 intent-published cut 发生于 Start 之前，再核对确切 intent 原件；缺 birth 只是辅助检查。该清理行明确为未调用启动，不写 PID/退出事实。postlaunch 在管理器和采集完成后，用独立十秒上限读取 birth、实际进程参数及连接归属，并保留另一原 pidfd；不会延长三秒 trace ACK 窗口。原件严格关联 launcher 的 peer/sequence/PID、intent/birth 摘要和 closed owner 观察，不造注册事件。显式恢复随后登记并打开同一实例，没有重复启动。独立 scout 未发现该范围的必修问题。

冻结脚本前两次在末尾写收据时遇到相对路径错误，原 stdout 和失败记录保留；后续改为启动即保存脚本、每条命令结束即保存退出结果。publication race 已用同一快照补齐完整收据；runtime 首轮还暴露 entry-opened 后的恢复证明角色缺口，已用实际归档与四个反例修正顺序策略（普通/race 各五节点），真实运行复验进行中。单条真实 recovery activation proof 可同时验证原目标和恢复调用，仍严格绑定已登记实例、候选/回滚角色及 generation，不补造丢失的 open-verified 事件。

## 当前状态

- 已完成：V1 五场景、V2 六场景及有界 EINTR 修复、各自分包回归、事件运输/监督 helper、按类型顺序策略及历史目标原文绑定。
- 已完成的接线组件：完整 bundle/operation 读取、真实 owner 观察和上述七操作回执关联场景。
- 进行中：统一适配器已完成下述分批持久化、恢复、归属和容量场景；真实 SSH/logout 仍在接线，随后检查完整 F01–F18/C01 覆盖及最终稳定版本。各批次属于明确冻结的输入边界，不拼成一次全树验收。
- 失败待决策：无新的实现失败待用户决策。上述两次历史失败未取得完整归因，继续保留并在后续矩阵中观察，不以单次通过作永久无故障承诺。
- 环境不足未验证：其他平台、断电与资源预算。Linux 专用 KVM 可用；当前产品 S3 和实际 PID 复用已分别完成下述普通构建验收，SSH/logout 是实现与验收中的场景。

主代理负责事务/恢复语义和实际场景；executor 实际使用 `gpt-6.1-sol/medium` 完成独立实现与验证，scout 实际使用 `gpt-6-luna/high` 核对原件与边界，无子代理再派生。

运行时十切点普通轮为 11 PASS 节点，187 份冻结输入未变；覆盖原核心停止发出/观察、启动后未注册、登记验证、目标验证、入口打开前后及 active 发布/回滚。登记切点使用实际 verifying phase。入口打开后崩溃不能补造丢失的 open-verified 事件；显式恢复的新鲜实际证明只有绑定同一原 RunID、注册身份与候选/回滚角色后，才允许支持原操作的后续发布。二十八实际核心全部退出，一条未启动 intent 单独记录；该阶段 race 尚待补齐，后续结果见下文。

F13 回滚失败普通轮为 3 PASS 节点（两子场景加父项），F11 degraded 发布切点另为 2 PASS 节点，各自冻结输入未变，race 待补。归属故障通过实际杀死原回滚进程使验证失败，产品写入 degraded；回滚 active 写入失败则遵循持久化错误契约停止后续写入，保留 rolling_back/pending。两者公开状态均为 unknown/degraded，普通写入受阻，原件保留；测试不会把后者伪装成已落盘的 degraded 日志。清理补信号数量见增量索引，不算产品 Stop 成功。

四 active I/O 切点及两启动窗口现已分别从各自原冻结输入补齐 race：5/3 PASS 节点，输入未变、原 pidfd 退出与 proc 消失完整，均无清理补信号。F18 普通真实场景已通过启动、转发及 Stop；依赖证据限定于固定 Hoopa build 的实际 Go 模块图和子进程启动环境（无 GOST 搜索 PATH、只有 Cobra/pflag 外部模块），不是宿主机或 native-library 清单。F14 三个元数据故障普通真实场景通过：修改 birth 启动身份、缺 registration/birth、registration 不完整；后者仍能独立证明实际 owner，但无法取得产品管理权威。真实 PID 复用尚未验证，外来监听进程场景接线中。

C01 首轮失败原件保留：身份冲突探针错误地尝试为已提交的目标重新计算 impact，修订为保留原 Plan 的其他字段；新增容量种子的真实 fixture begin/end 尚未列入投影分类，现已显式处理。失败归档离线普通/race 各5PASS，完整验证18个流、6653事件、1024操作（4个真实旧操作加1020个 Prepared 种子）；不补称原实际轮通过。离线authority-copy输入映射实际原件stat/size/SHA，不冒称完整protected树快照。新实际 C01 复验进行中。

当前产品的真实 S3 验收见 会话索引（本地原件 `../../experiments/gost-mihomo/p1-product-session-validation.json`，不随Git上传）。专用 Linux KVM guest 无网卡、无宿主目录共享，固定197份输入；普通构建实际2PASS节点。QMP SUSPEND/WAKEUP、内核deep S3日志、guest BOOTTIME/MONOTONIC差值约2.389秒互相印证。同Run的原长连接继续与新入口连接共三次4096B payload/echo；两个核心均原pidfd退出、proc消失，无夹具补信号。独立核验1365导出文件、1402非空原件引用，三个空占位字段明确不算原件通过。QMP quit是导出后的VM收尾，不算guest OSshutdown；SSH断开/登录shell退出仍需真实session内manager及session外见证接线。

容量normal02通过后，race02在每测试300秒上下文耗尽时未能启动最后的重放worker；已返回的capacity-rejected业务响应确为OPERATION_CAPACITY_EXHAUSTED，manager后续request_id=null的CANCELLED是SIGTERM结束回执，两者不能混同。该race轮仍失败；fixture预算改为480秒重跑，不更改产品重放语义，也不把测试预算当性能结果。

运行时十切点、degraded 发布、F18 固定可选依赖场景与容量 C01 现已补齐各自冻结版本 race，分别 11/2/1/6 PASS 节点。运行时普通/race 各 28 个实际核心加一条未启动 intent，不能把 intent 计为核心；race 有三次夹具清理信号。容量 race03 使用 197 份输入及 480 秒 fixture 上限，实际 398.76 秒通过；普通 normal02 为 195 份输入，两版分列。原 race02 超时失败保留。各批次全数原 pidfd 退出、proc 消失；具体清理信号与原件在增量索引。

真实 PID 复用索引（本地原件 `../../experiments/gost-mihomo/p1-product-pid-reuse-validation.json`，不随Git上传）记录当前产品的专用 VM F14 子场景：原核心退出且精确回收后，新监听进程实际取得 PID 132，同一 boot 的启动 tick 从 1352 变为 2066。Recover 返回 CONFLICT；四次原 pidfd 与实际 TCP 归属观察确认新进程仍活，持久树未被更改。197 份输入、1210 个导出文件与 1251 份非空原件独审通过，2 PASS 节点；两个核心退出，无夹具清理补信号。设置 PID 游标只发生于无网卡/无共享目录的 guest，未改宿主 PID namespace。这与修改 birth 的元数据故障分别记账，不替代其他 F14 场景或 SSH 验收。

F14 三个归属元数据场景随后补齐同冻结输入 race：4 PASS 节点，六实际核心退出且 proc 消失。修改 birth/删除 registration 的场景与真实 PID 复用 VM 继续分别列出，不互相替代。上述已启动批次没有剩余 race 阻塞；全矩阵覆盖与最终稳定版本仍须单独审查。

覆盖审查补跑 F01 正常／异常前端退出、F03 显式恢复及 control-only 空／运行状态：`harness-lifecycle-02` 与 `harness-lifecycle-race-02` 使用同一 197 份冻结输入，普通/race 各 7 PASS 节点、九实际核心全退出且 proc 消失、无夹具清理补信号。前端是实际固定 CLI 的最小进程，不冒称完整 TUI；显式接管保留原核心和配置版本，单独重开控制服务不改变运行权威。该批明确不含 F02；SSH 接线稳定后另补 F02 受影响共享路径检查。
