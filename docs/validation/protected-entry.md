# T135 受保护实验入口验收记录

日期：2026-10-23。本记录覆盖 [Protected core v1 契约](../../specs/001-hoopa-proxy-manager/contracts/protected-core-v1.md)规定的 Linux/amd64、IPv4 loopback 显式实验入口。最终 007 补丁已从干净上游依次应用 001–007、核对整树后构建，固定候选完成真实复验。主代理据此完成 T135 本地范围，139 项任务完成 21 项；不构成正式 G05/G07、产品或平台支持放行。

## 固定输入与归档状态

| 阶段 | 固定输入与实际状态 |
| --- | --- |
| r2 真实验收 | `/tmp/hoopa-t135-dl7j479q/mihomo-guard-r2`；SHA256 `9d43afb86faa40001396a85a0ee20152384d5dfeb8291ec7bf6e4761403374d3`。完整入口矩阵实际通过，旧制品和日志保留 |
| r4 增量复验 | `/tmp/hoopa-t135-dl7j479q/mihomo-guard-r4`；51,013,256 字节，SHA256 `abe106aedaf8a4e2309c4fc96dada0bf82b3e4b95efe8155f8a8d87e40020f3b`。加入全部资源先 bind 后 listen、来源可写/容量准入后，真实入口与 trace 矩阵完整通过 |
| 最终归档 | `/tmp/hoopa-t135-artifact-tg95ee9o/mihomo-guard`；SHA256 `7c022d3263cf5c16d72fd68834041ae2bfb15e748283b6aa75d529c1fdf4440f`；版本 `hoopa-t135-guard-final`。[candidate manifest](../../experiments/mihomo-events/capability-candidate-manifest.json) 绑定 7 份补丁、源码树、工具链和制品；validation（本地原件 `../../experiments/mihomo-events/capability-validation.json`，不随Git上传） 绑定测试、私有证据摘要、失败及未验证范围。仓库仅保存文本，不保存二进制或带凭据的原始配置 |

[纯 JSON 校验器](../../experiments/mihomo-events/capabilityprofile/profile.go)不执行 I/O 或核心调用。它递归拒绝未知/重复键、null、类型及数值错误，限制 64 KiB 输入、1–4 个命名 mixed TCP 入口、64 个节点/组、8 跳引用链和 64 条有序规则。完整候选必须先通过校验；不可达的 PROCESS/UID、OR/NOT、规则集等不支持条件也拒绝。规则顺序、AND 原子顺序与引用成员顺序不重写。

显式启动使用 `HOOPA_GUARD_CONFIG_SHA256`、`HOOPA_GUARD_GENERATION`、`-d` 私有绝对目录和 `-f` 私有绝对 JSON 文件。同一份原字节用于 hash、校验及核心 Parse。目录要求当前用户所有、0700、无符号链接；配置要求普通文件、当前用户所有、0600、NOFOLLOW。测试将候选按 SHA256 复制到私有目录的 0500 快照，不执行可变原路径；日志和配置保存在 0700 目录中的 0600 文件。只管理、终止并 Wait 本测试启动的 child。

## 已执行的真实回环矩阵

[入口测试](../../experiments/mihomo-events/capability_test.go)默认 skip；只有 `HOOPA_CAPABILITY_NETWORK_TEST=1` 才执行。显式启用但缺少候选绝对路径或规范 SHA256 时失败，不能回落成 skip。默认日志（本地原件 `/tmp/hoopa-t135-capability-default-race.log`，不随Git上传）与缺候选日志（本地原件 `/tmp/hoopa-t135-capability-missing-candidate.log`，不随Git上传）分别保留。

r2 真实 race 日志（本地原件 `/tmp/hoopa-t135-capability-r2-network-race-escalated.log`，不随Git上传）通过，包耗时 2.333s；私有证据为 `/tmp/hoopa-t135-capability-2795922894`。r4 完整真实 race 日志（本地原件 `/tmp/hoopa-t135-dl7j479q/capability-r4-network.jsonl`，不随Git上传）通过，2 个顶层测试、含子测试 49 项 pass、0 fail/skip，包耗时 2.739s。入口证据为 `/tmp/hoopa-t135-capability-1625434564`，trace 证据为 `/tmp/hoopa-t135-capability-2605404127`。这些计数是测试事件数量，不是连接身份覆盖率或正式参考矩阵分母。

最终 fresh 候选的真实复验（本地原件 `/tmp/hoopa-t135-artifact-tg95ee9o/capability-network-final.jsonl`，不随Git上传）同样为 2 个顶层、49 项含子测试 pass、0 fail/skip，包耗时 2.839s。入口与 trace 私有证据分别位于 `/tmp/hoopa-t135-capability-2828836096`、`/tmp/hoopa-t135-capability-1220296870`；103 个文件的路径、长度和 SHA256 已归档，不将中间候选结果直接赋给最终 hash。

| 场景 | 实际观察与证据边界 |
| --- | --- |
| 加载期拒绝 | 实际启动子进程测试实例/名称/路径进程规则、不可达进程规则、OR、UDP、非 loopback 入口/controller/proxy、命名入口 proxy 绕过、未知/重复字段、缺用户/secret、控制凭据复用、坏/缺 hash、坏/缺 generation、权限错误和已注册 CLI controller override。退出前持续轮询应关闭的端口，退出后重新 bind 确认资源释放；失败非零退出，stderr 精确为固定拒绝行，stdout 空，无测试凭据或敏感规则值 |
| 合法 `-t` | 完整解析后零退出；期间探针和退出后 bind 证明没有入口监听。该结果与上行真实启动拒绝分开，不以 `-t` 代替启动验收。未知 CLI 参数仍由原生 flag parser 处理；既有 `-v` 版本查询没有被报告为受保护配置错误 |
| 首匹配线路 | 两个独立受控 HTTP CONNECT 代理及真实回环 echo/HTTP 目标；代理计数证明 DOMAIN、IN-NAME、单层 AND 按原序首匹配到 A 或 B。entry-b 的 AND 先于 DOMAIN，IN-NAME 先于 DOMAIN-SUFFIX。这里只校准所列规则和明文回环代理 |
| 入口认证 | 两个命名 mixed 入口分别使用独立用户名/密码。HTTP CONNECT 与 SOCKS5 授权转发成功；缺凭据、错凭据、另一入口凭据均拒绝且受控上游无请求。SOCKS4 和 SOCKS5 UDP ASSOCIATE 不放行。HTTP keepalive 第一请求成功后，下一请求无凭据、错误凭据或另一入口凭据仍拒绝，目标计数不增加 |
| 只读控制 | 缺凭据、错凭据及入口密码访问 status/drain 返回 401，不暴露 generation/hash。授权 status 返回 schema=1、固定 generation/hash/state 及三个 false 的 identity 字段。配置 PUT/PATCH、组修改、规则修改、provider 更新、restart、主动 delay GET 和原生 configs 读取均先认证，授权后 409；受控目标未收到主动探测 |
| SIGHUP 与失败新候选 | 改写磁盘配置后发送 SIGHUP，旧进程仍报告原 generation/hash，继续原线路。独立坏候选非零退出，既有核心继续转发。没有执行热 reload 或对用户核心操作 |
| 晚出现的端口冲突 | 预先占用后一代理入口端口，候选启动失败；前面入口及 controller 期间不接受 TCP，退出后可重新 bind；旧核心转发继续。该真实场景覆盖晚 bind 失败，不等于模拟所有 listen 转换错误 |
| drain 与独立完整替换 | 旧 TCP 已交换数据后 drain，重复 drain 成功；新入口 TCP 连接失败，controller 保留并报告 draining。旧连接继续 echo；独立新 generation/端口按新规则通过 B，旧流仍可续传并正常半关闭到 EOF。没有迁移连接，也没有原地改写旧进程规则 |

Go 验收程序开启 race；固定核心子进程为普通构建，不能称整个子进程有 race 插桩。核心相关包另有独立 race 检查。离线工具链为 `/tmp/hoopa-go-1.27.3-cb6ry1lq/go/bin/go`，环境使用 `GOTOOLCHAIN=local GOPROXY=off GOCACHE=/tmp/hoopa-mihomo-events-buildcache`；最终命令使用 `-race -count=1 -timeout=90s -json ./experiments/mihomo-events -run '^TestCapability'`，启用 gate 和固定候选参数见 validation 的 `network_command` 及 [验证指南](../../specs/001-hoopa-proxy-manager/quickstart.md)。

## trace、来源准入与 socket 预留的分层证据

初版入口矩阵的 `trace-generation-mismatch` 同时缺少其他 trace 必需项，只证明组合输入整体拒绝，不能单独归因 generation。后续 [trace admission 测试](../../experiments/mihomo-events/capability_trace_test.go)建立合法 trace 配置、完整固定 route map 和私有来源目录，再分别只改 generation、map adapter type、source 目录权限。r4 三个独立否例均拒绝且不生成 epoch；合法配置实际建立一条双向各 257B 的连接，正常退出后仅一个 epoch，manifest generation 一致，严格 recovery/lifecycle reader 通过且 final 上下行均为 257B。这证明本次受保护入口连接到既有 v2 来源，不证明 connector 身份。

生产 `PrepareGuard` 在来源初始化后检查 generation、新 epoch、readOnly/poisoned/stopping/closed 状态及首流与终末预留空间。真实创建并关闭 32 个 epoch 后再开 source 的反例曾证明只读重放源被误准入；修后 `TestSourceGuardRejectsReadOnlyCapacity` 在核心 race 回归通过。该证据是实际来源文件/epoch 容量场景；剩余空间表达式的代码检查不能升级成宿主真实满盘、断电或所有资源故障的端到端证明。

r4 的生产实现先对全部入口/controller 只 bind、不 listen，全部成功后转换 listener，最后启动协议处理和发布 running。失败会清理本实例已预留/创建的资源；多个 listener 没有内核原子事务。真实晚端口冲突已通过。首次 reservation-race 日志（本地原件 `/tmp/hoopa-t135-dl7j479q/reservation-race.jsonl`，不随Git上传）因 main init 的原生 flag parser 不识别 `-test.paniconexit0` 而失败，测试未执行。随后将同一生产 helper 和测试移入 `component/hoopaguard`（`Reserve`/`Listen`），避免 main 初始化干扰；显式 `HOOPA_GUARD_NETWORK_TEST=1` 的独立 race 运行（本地原件 `/tmp/hoopa-t135-dl7j479q/reservation-fixed-race.jsonl`，不随Git上传）实际通过 `SO_ACCEPTCONN=0`、`SO_REUSEADDR=0`、connect 拒绝、重复 bind 拒绝、激活后可连接及释放后可重占。最终 007 含此位置调整，fresh 候选再跑完整入口矩阵通过。

核心定向 race 回归（本地原件 `/tmp/hoopa-t135-dl7j479q/core-regression-race.jsonl`，不随Git上传）涉及 rules/common、rules/logic、transport/socks5、component/hoopatrace 四包：41 个顶层、含子测试 91 项 pass，0 fail；2 项旧网络 gate skip，分别为 `TestSourceLostACKAndReopenReplayOriginalBytes` 与 `TestSourceSenderRetriesSamePersistedEvent`。该回归不能被描述为全部测试无 skip 或上游全量通过。`TestHoopaANDFalseOverridesUnknown` 等既有三值规则单测保留通过，但真实实验 profile 不加载任何进程条件，不能由本次网络矩阵推导 AND 的连接发起者 unknown 端到端语义。

## 保留的失败、环境拒绝与默认检查

| 记录 | 原始结果及后续独立证据 |
| --- | --- |
| 初次 profile red（本地原件 `/tmp/hoopa-t135-profile-red.log`，不随Git上传） | 默认 Go cache 目录只读，测试未执行；不作为反例行为通过。后续改用指定 `/tmp` cache |
| 畸形 AND 反例（本地原件 `/tmp/hoopa-t135-profile-malformed-red.log`，不随Git上传） | `TestMalformedANDCannotPanic` 实际触发 slice bounds panic，属于解析器缺陷。检查前缀/边界顺序修正后，profile-final-race（本地原件 `/tmp/hoopa-t135-profile-final-race.log`，不随Git上传）通过，包耗时 1.127s；旧 panic 日志保留 |
| 来源只读初次 red（本地原件 `/tmp/hoopa-t135-dl7j479q/guard-readonly-red.log`，不随Git上传） | 缺少 guardReady 符号导致构建失败，不能算执行了只读反例 |
| 来源只读 runtime red（本地原件 `/tmp/hoopa-t135-dl7j479q/guard-readonly-red-runtime.log`，不随Git上传） | 32 个真实 epoch 后，只读 replay source 被误准入，测试实际失败；修后独立核心 race 回归中的对应测试通过 |
| r2 默认沙箱网络运行（本地原件 `/tmp/hoopa-t135-capability-r2-network-race.log`，不随Git上传） | `socket: operation not permitted`，测试失败，未完成网络矩阵；获既有授权的回环执行在另一日志实际通过，不改写该环境失败 |
| 默认包检查（本地原件 `/tmp/hoopa-t135-capability-package.log`，不随Git上传）、默认 race gate（本地原件 `/tmp/hoopa-t135-capability-default-race.log`，不随Git上传）、vet（本地原件 `/tmp/hoopa-t135-capability-vet.log`，不随Git上传） | 当时仓库包测试及 vet 成功，显式网络测试默认 skip；默认 pass 不替代 r2/r4 真实运行，也不追溯覆盖此后主代理的源码改动 |
| socket reservation 单测入口（本地原件 `/tmp/hoopa-t135-dl7j479q/reservation-race.jsonl`，不随Git上传） | main init flag parser 阻断首次测试；生产 helper 与测试迁入独立 component 后，上述 reservation-fixed-race 实际执行并通过，不覆盖旧失败 |

r4 真实日志 SHA256 为 `d013e09e3f7b1366e55f611a5b68c18b4566b6336d1b6d83c7ee06da502fe598`；上述核心回归日志 SHA256 为 `091a73c1302c67b8d66d9fc99f9b2bff80ca2e93aff679fab4f52e6c9fd70409`。最终仓库定向 race 为 462 项含子测试 pass、7 skip、0 fail；默认网络 gate 的 skip 不计入真实通过。仓库及核心相关包 vet 通过。核心回归运行树与最终 fresh 树逐文件/模式/路径一致，8 份 telemetrywire 及纯 profile 均与仓库逐字节一致，001–006 与依赖文件不变；scout 独立核对一致性。临时证据可能随 `/tmp` 清理失效，仓库 manifest 保存了固定输入、文本补丁和可重跑条件，不复制原始私密配置。

## 能力与后续边界

| 状态 | 本记录的判断 |
| --- | --- |
| 已执行通过 | 纯校验器修后 race；r2/r4 与最终 fresh 候选真实矩阵；来源准入修后核心 race；socket reservation 显式 race；最终定向仓库 race/vet 与核心 vet；归档一致性核对 |
| 已跳过 | 默认显式网络 gate；核心定向回归的两项旧网络 gate，均保持 skip，不计为通过 |
| 历史失败 | 畸形 AND panic、32 epoch 只读源误放行、构建/测试入口失败、沙箱 socket EPERM 均按上表保留；对应修后或授权通过仅覆盖各自范围 |
| 进行中 | 项目仍在 P0：T132 正向 connector 证明、T137 完整候选及正式矩阵尚未完成，T136 依赖 T137；T135 的本地结果不能跨越这些依赖 |
| 环境不足或未验证 | 原始 connector instance/name/path 后端及其受控部署；其他系统/架构/地址族、远端网络与 TLS 上游、宿主真实满盘/断电、产品性能和正式 G05/G07 矩阵均无本记录的通过证据 |

进程实例、名称、路径规则在加载时全部拒绝，三个 identity 字段均 false。这是能力不支持的明确声明，不能以永久拒绝所有连接冒充正向身份能力；原 G01 失败与 T132 环境缺口保持。AND 的三值 unknown 只有既有单测证据，本次真实首匹配不补造 connector 证据。

loopback、私有文件和独立凭据不构成同 UID 的 OS 隔离。drain 保留的是已接受 TCP；已有 HTTP keepalive 仍可提交逐次认证的新请求，不能称 drain 逐请求切断。完整替换仅通过独立新进程/端口完成，没有热 reload；跨进程 generation 唯一分配、持久操作身份、崩溃接管和完整回滚矩阵留给 T136 等后续管理器工作。

校验器、入口验收及初稿由 executor 配置 GPT-6.1-sol / medium 执行；scout 配置 GPT-6-luna / high 收集调用路径、只读复核与归档一致性证据；核心接线、来源/socket 关键语义、发现反例后的修正和最终验收由主代理负责。子代理未继续派生。这里的通过仅适用于 Linux/amd64 回环实验与上述固定候选，不改变正式门槛或产品支持状态。
