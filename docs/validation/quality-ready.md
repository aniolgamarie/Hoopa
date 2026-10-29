# T134 连接质量与实际线路：局部实现及验收记录

日期：2026-10-23。T134 的严格事件 profile、三类协议完成证明、实际连接线路、连接段累计以及本地真实重试已有实现和通过记录。本报告只记录已执行的范围；主代理已完成最终局部候选归档与稳定内容验收，T134勾选完成，139项任务完成20项。它不关闭原 [G03](g03-quality.md)，也不改变 [G01 身份验收](g01-identity.md) 的原始 **0/100** 结果。

依据为 [冻结的 telemetry-quality-v2 契约](../../specs/001-hoopa-proxy-manager/contracts/telemetry-quality-v2.md)及 [ADR008](../adr/008-remaining-evidence-execution.md)。候选归档目标为 [quality-candidate-manifest.json](../../experiments/mihomo-events/quality-candidate-manifest.json) 与 quality-validation.json（本地原件 `../../experiments/mihomo-events/quality-validation.json`，不随Git上传）；两份归档已由主代理完成，绑定全新upstream依次应用001–006后的源码与实际构建。以下首版、r2和最终版本的路径、hash、失败及分母保持独立。

## 已实现的契约与边界

| 维度 | 实现及校验规则 |
| --- | --- |
| wire 与 epoch | `schema_version` 只接受数值字面量 1 或 2，同 epoch 固定版本；v1 原文和旧校验继续保留，不回填 v2 证明。v2 未知、缺失、重复字段及嵌套重复键均拒绝；ID、规范十进制字符串、uint64 溢出和字段数量严格检查。manifest 仍为形状版本 1。 |
| 时间坐标 | `opened.flow_start_ns` 来自源进程在解析 target 前捕获的单调时钟；只在同一 source/epoch 内比较。`flow_start <= attempt_start <= ready <= finish` 在有证明时成立。成功耗时到 `ready_ns`，等待到 `finish_ns`，分别对纳秒差除以 1,000,000 向下取整；UTC 不参与耗时。finish 在加锁/写事件前捕获。 |
| 成功与失败 | `forward_setup_v2` 区分 success/failure/timeout/cancelled/unknown。无证明时 ready 和成功 duration 为 null，`reason=ready_unknown`；已经观察到的错误优先保留。只有返回实际 Conn、无错误且无证明时才可为 unknown。保留 attempt zero 表示 metadata/route 最终中止：从 flow start 开始，stage 为 `pre_route`，无实际 Conn；可恢复 DNS 失败后仍成功路由不会凭空产生失败 setup。 |
| 协议证明 | DIRECT 在真实目标 TCP connect 后产生 `tcp_connected`；HTTP 在完整、实际接受的 200 CONNECT 回复后产生 `http_connect_accepted`；SOCKS5 在校验 VER/REP/RSV 并完整读取 BND 后产生 `socks5_connect_accepted`。HTTP 成功头后的预读 payload 保留。 |
| wrapper 与 lazy | group 只传递已有证明；只有明确透明的 callback/tracker wrapper 转发证明。未知外层不能借用内层 TCP 证明。lazy 首次 Write 返回正字节且 `NeedHandshake` 变 false，也不构成 ready；TFO 和未校准协议不因此获得成功耗时。业务 TTFB 仍不可用。 |
| 实际连接身份 | `actual_connection_ref` 属于具体外层返回 Conn，并在计量 I/O 前绑定唯一 flow/attempt；同 attempt 只注册一个实际 Conn。无返回 Conn 的失败不发 actual 事件。tracker 仅绑定该 flow/attempt 已登记的引用，引用及 tracker 绑定唯一；规则选择意图不冒充实际线路。 |
| 线路与稳定引用 | 实际链按 core 的 inner-to-outer 顺序冻结，1–8 跳；每跳保留内部名、类型、实际 provider 和 nullable node/provider/group ID。生成 bundle 使用持久 `node:`/`group:` 引用作 core 名称，展示名另存；node 与 group 互斥，provider ID 如存在必须等于实际 provider 名。配置/映射不符拒绝，不按当前 group 选择或展示名倒推历史。 |
| 生成 bundle | [quality_bundle.go](../../experiments/mihomo-events/quality_bundle.go) 生成 core 可读取的 JSON 配置与独立 route map，校验重复 ID、引用、循环、类型、IPv4 回环端点及所有可能实际链长度；最长 8 跳通过、9 跳拒绝。展示改名不改变 core 配置中的内部名。 |
| 累计字节 | checkpoint/closed 的共有总量仍为累计值，另带每连接段及 unallocated；每流最多十段，零字节段也必须出现。各方向满足 `flow=sum(segments)+unallocated`，每项单调且求和不溢出。首次 Write/relay 的实际 n 在同一 flow 锁内更新总量与原段，重试和后续 tracker 绑定不搬移历史字节。 |
| reader、receiver、恢复 | [telemetrywire](../../protocol/telemetrywire/) 保留 typed v2 及原始字节，v2 Counter 保留真实 schema 2 和精确总量；[reader](../../experiments/mihomo-events/source_lifecycle.go) 与实际 [SourceReceiver](../../experiments/mihomo-events/receiver_linux.go) 执行严格 epoch 校验。恢复可保留 v1/v2 已知总量，不从旧总量捏造 quality、线路、连接段或跨 epoch 恢复事件。 |

## 验收矩阵：不同测试层分别计证

| 层次与测试 | 实际运行内容 | 观察到的结果与限制 |
| --- | --- | --- |
| 严格 wire/epoch fixtures | [正反样例](../../testdata/telemetry-quality-v2/)及 [quality_v2_test.go](../../protocol/telemetrywire/quality_v2_test.go)：字段形状、嵌套重复键、uint64 边界、时间等式、引用错配、版本混用、段守恒/单调、8 跳/10 段、原文 replay/recovery。另由主代理补充 attempt/ref 与 pre_route 规则。 | 包级 test、`-race`、vet 通过；追加检查通过。日志 wire-race（本地原件 `/tmp/hoopa-t134-wire-race.log`，不随Git上传）、wire-followup（本地原件 `/tmp/hoopa-t134-wire-followup.log`，不随Git上传）。这是 parser/state 证据，不是网络就绪证明。 |
| core flow/计量单测 | `TestQualitySegmentsRetryAndCheckpoint`、`TestQualityReadySeparateFromFinishAndErrorOutcome`、`TestQualityReopenRetainsV1AndV2Epochs`、`TestQualityMappingMismatchFailsBeforeRegistration`、`TestQualityFlowPreDialWaitAndAbortedRoute`。 | core-quality-final-race.jsonl（本地原件 `/tmp/hoopa-t134-m3_248tu/core-quality-final-race.jsonl`，不随Git上传） 的五组均通过；覆盖部分写入/错误后的原段保留、并发 checkpoint 守恒、成功 ready 与较晚 finish 分离、failure/timeout/cancelled/lazy、v1/v2 重开、映射拒绝和 pre-dial 等待。受控 Conn/时钟夹具不作为完整核心网络端到端证明。 |
| HTTP adapter 协议模拟 | `net.Pipe` 接到实际 HTTP adapter：成功、403 拒绝、头部截断、cancelled；控制回复释放前 Dial 不完成，成功时预读 payload 可读。 | protocol-race.jsonl（本地原件 `/tmp/hoopa-t134-m3_248tu/protocol-race.jsonl`，不随Git上传） 中 HTTP 四个子例通过。它使用 Pipe，不是真实 HTTP TCP 代理服务。 |
| SOCKS5 adapter 协议模拟 | `net.Pipe` 接到实际 SOCKS adapter：成功、坏 VER、坏 REP、坏 RSV、截断 BND、150ms deadline timeout；完整回复前不能 ready。 | 同一 protocol 日志中六个子例通过。这个包同次执行的 DIRECT 测试失败，故该整包运行仍为 **FAIL**，不能称全包通过。 |
| 未知外层与透明 wrapper | `TestHoopaQualityUnknownOuterAndTransparentCallbacks`：透明 callback 保留原 ref；构造的未知/lazy 外层即使正数 Write 后无需 handshake，也不能借用内层 DIRECT 证明。 | 同一 protocol 日志中通过；这是模拟 wrapper 行为，不是 VMess 等其他协议的真实成功验收。 |
| DIRECT 的 DNS 与 TCP 边界 | 实际 DIRECT adapter，80ms 受控 resolver，真实 IPv4 回环 TCP accept；证明 ready 包含本次 DIRECT lookup，断言 elapsed 在 75–750ms。 | direct-dns-runtime-fixed-race.jsonl（本地原件 `/tmp/hoopa-t134-m3_248tu/direct-dns-runtime-fixed-race.jsonl`，不随Git上传） 独立 `-race` 通过，测试 0.08s、包 1.110s。真实的是 DIRECT TCP/adapter 路径；resolver 为注入的受控实现，未验证外部 DNS 服务。此前失败另记下表。 |
| 规则解析 DNS 与 flow/attempt 分界 | 实际 `resolveMetadata` 与规则解析，90ms 受控 resolver；test-only TCP direct shim 拨已解析回环 IP，产生受控 Conn 证明，再进入实际 source/严格 decoder。断言 flow duration 至少比 attempt duration 多 85ms，attempt start 不早于 DNS 返回。 | rule-dns-resolved-race.jsonl（本地原件 `/tmp/hoopa-t134-m3_248tu/rule-dns-resolved-race.jsonl`，不随Git上传） 独立 `-race` 通过，测试 0.14s、包 1.162s。这是实际 rule resolver + 测试专用 TCP shim；**不是完整固定核心 DNS E2E**，shim 的受控证明也不代替生产 DIRECT 证明。 |
| 固定核心真实五流 | 真实 HTTP A、SOCKS5 B、DIRECT、SOCKS5→HTTP nested、新 generation 展示改名后的 A；每流双向各 257B。源文件和实际 receiver 都严格解码。 | 首版和 r2 独立通过，见下文原始运行表；真实验证延迟边界、预读、切组保留 A、实际 nested endpoint/chain、稳定 ID、原文历史、receiver reopen/replay。 |
| 固定核心真实失败后重试 | HTTP 首次 503、SOCKS 坏 REP、SOCKS 截断 BND，分别在同一客户端 flow 中第二次成功。 | r2 三场景分别通过；每场景固定 1 flow / 2 attempts，失败无 actual/ref/ready/成功 duration，成功使用新 attempt，唯一成功段双向各 257B。未把 6 attempts 当作 6 flows。 |
| 既有 core 回归与 receiver | core-race.jsonl（本地原件 `/tmp/hoopa-t134-m3_248tu/core-race.jsonl`，不随Git上传）、receiver-race.jsonl（本地原件 `/tmp/hoopa-t134-receiver-race.jsonl`，不随Git上传）。 | core 五个包结果均 pass，其中 hoopatrace 两项显式网络测试 skip；receiver 日志三包 pass，其中原身份测试与两项 closure 网络测试 skip。skip 不记为执行通过，也不取代已经单独运行的 T133 材料。 |

## 固定核心真实运行：输入、真值与历史

实现入口为 [setup_probe_test.go](../../experiments/mihomo-events/setup_probe_test.go)。默认不创建监听端点、不启动子进程，显式 `HOOPA_QUALITY_NETWORK_TEST=1` 后才执行；开启后缺少候选绝对路径或规范 SHA256 会失败，不会 skip。候选参数为 `HOOPA_QUALITY_CANDIDATE`、`HOOPA_QUALITY_CANDIDATE_SHA256`，核心使用 `HOOPA_TRACE_PROFILE=2` 和绝对路径 `HOOPA_TRACE_ROUTE_MAP`。

每 run 在被测核心启动前写入输入 bundle、mapping、候选 digest、场景分母、字节真值与容差；新 generation 另有预登记文件。候选经 no-follow 打开、复制并核对 hash 后，只执行私有 0500 副本；目录 0700、数据/配置/日志 0600，最终遍历检查。所有监听和被拨目标限定已登记 IPv4 回环地址，HTTP/SOCKS 夹具拒绝任意 CONNECT 目标。清理只终止并回收本测试拥有的子进程；独立、不计量的 health 入口先完成 DIRECT CONNECT+echo，TCP accept 本身不作为转发就绪。

每个代理完整响应固定延迟 120ms，并有独立 gate。延迟到期、响应仍被扣住时，同时读取实际 source 和 receiver，确认尚无该 attempt 的 setup。ready 耗时预登记区间为期望延迟减 20ms 至加 250ms；nested 期望 240ms。DIRECT 用同一上限容差，不声称 0ms 是零时延，仅为毫秒向下取整。耗时还逐项核对 `ready-start`，不会拿 `finish-start` 代替。

HTTP 服务把 17B payload 与成功头一起写出，实际向 echo 转发全部 257B，再仅跳过已预发的 17B echo 回复，最终客户端收到完整 257B。A 流先完成 128B，切组到 B 并完成 B 的 257B 后，A 原连接继续完成 129B。nested 既核对 `[node:b,node:nested,group:main]` 实际顺序，也核对 SOCKS 服务实际接到的目标就是 HTTP 监听端点。非 DIRECT 路径还核对 `group:main`/`Selector`，所有 node 名称、类型、nullable provider 及 ref/attempt 绑定均核验。

| 候选/运行 | 结果及 duration_ms 顺序 | 原始日志与私有完整证据 |
| --- | --- | --- |
| 首版第一次 | **FAIL**；第一代四流已观察 124/127/0/249，但第二代前置计数断言失败；不能写为五流通过。 | network-1.log（本地原件 `/tmp/hoopa-t134-setup-network-1.log`，不随Git上传）；run 3869614048（本地原件 `/tmp/hoopa-t134-setup-3869614048/`，不随Git上传）。 |
| 首版第二次 | **PASS**；五流 135/134/0/286/156；`-race` 包 2.523s。 | network-2.log（本地原件 `/tmp/hoopa-t134-setup-network-2.log`，不随Git上传）；run 3431972264（本地原件 `/tmp/hoopa-t134-setup-3431972264/`，不随Git上传）。 |
| 首版稳定断言复验 | **PASS**；五流 127/131/0/272/146；含实际 group ID/adapter type 校验，`-race` 包 2.556s。 | network-final.log（本地原件 `/tmp/hoopa-t134-setup-network-final.log`，不随Git上传）；run 1295250428（本地原件 `/tmp/hoopa-t134-setup-1295250428/`，不随Git上传）。 |
| r2 五流 | **PASS**；五流 130/131/0/286/139；五流测试 1.53s。 | r2-network.log（本地原件 `/tmp/hoopa-t134-setup-r2-network.log`，不随Git上传）；run 1189490465（本地原件 `/tmp/hoopa-t134-setup-1189490465/`，不随Git上传）。 |
| r2 HTTP 503→成功 | **PASS**；固定 1 flow / 2 attempts，成功 duration 126ms；测试 0.68s。 | 同一 r2 日志；run 2011699230（本地原件 `/tmp/hoopa-t134-setup-2011699230/`，不随Git上传）。 |
| r2 SOCKS 坏 REP→成功 | **PASS**；固定 1 flow / 2 attempts，成功 duration 126ms；测试 0.69s。 | 同一 r2 日志；run 1976901938（本地原件 `/tmp/hoopa-t134-setup-1976901938/`，不随Git上传）。 |
| r2 SOCKS 截断 BND→成功 | **PASS**；固定 1 flow / 2 attempts，成功 duration 128ms；测试 0.64s。 | 同一 r2 日志；run 3196772329（本地原件 `/tmp/hoopa-t134-setup-3196772329/`，不随Git上传）。 |

五流顺序固定为 HTTP A / SOCKS B / DIRECT / nested / 改名重开 A。r2 的五流与三个重试场景合计 `-race` 包 4.569s；它们分别使用预登记的分母，旧 run 不并入新 run。

首版路径 `/tmp/hoopa-t134-m3_248tu/mihomo-quality`，SHA256 为 `96a4139f178d962abfaabbc05a53fd61ff43e268fd4204c8d2da782af96eaf3d`。r2 路径 `/tmp/hoopa-t134-m3_248tu/mihomo-quality-r2`，SHA256 为 `98d1adc416c261c9c21d24173a742ef35ce3672dd9d2bdd8453607b9ab2267cd`。r2 补入 pre_route 中止规则和同 attempt 唯一 Conn 校验；首版二进制和证据未被覆盖。后续最终构建若有不同 digest，必须记录为新的候选与运行，不能替换这里的 hash。

重试 oracle 在冻结 source 和 receiver 中再次数出恰好两个 setup、一个 actual、一个 closed；失败 attempt 无 actual，成功 final 的唯一 segment 属于成功 attempt/ref。flow 的成功耗时包含前次失败等待，而第二次 attempt 耗时独立。五流测试还在第二代真实流量后逐字比较旧 manifest/frames，重开实际 receiver 后相同原文返回原 receipt；这证明本实验的接收去重，未运行 SQL/ByteFact 结算验收。

## 最终固定候选复验

最终版本`hoopa-t134-quality-final`位于`/tmp/hoopa-t134-artifact-ul72y63w/mihomo-quality`，SHA256 `9d52edee280d7e4893911e3c4a6ae43150cf4914421431f7ee19567505459b29`。006补丁SHA256 `62d743872d1cad47b98a09e7eff41c5fbd517016df8660c66e4c8acb6437cb98`，从固定未修改upstream依次check/apply001–006、整树内容/路径/权限比较通过后构建，go.mod/go.sum不变。相对r2收紧pre_route中止后不能再实际建连或出现后续setup，新增测试及注释；旧候选均保留。

最终真实日志（本地原件 `/tmp/hoopa-t134-artifact-ul72y63w/quality-network-final.jsonl`，不随Git上传）共2个顶层测试、3个重试子例通过，0fail/skip，包耗时4.836s。分母为8流/11attempts，健康入口不计入分母。每流上下行各257B，flow与原连接段一致。Go验收程序开启race，启动的core为普通构建；核心包另有race检查，不能称整个core子进程有race插桩。

| 最终运行 | 结果 | 证据目录 |
| --- | --- | --- |
| 五条成功流 | PASS；HTTP A / SOCKS B / DIRECT / nested / 改名A；成功attempt耗时129/130/0/278/151ms | 2935430640（本地原件 `/tmp/hoopa-t134-setup-2935430640/`，不随Git上传） |
| HTTP503后成功 | PASS；1流/2attempts；成功attempt为127ms | 286555945（本地原件 `/tmp/hoopa-t134-setup-286555945/`，不随Git上传） |
| SOCKS坏REP后成功 | PASS；1流/2attempts；成功attempt为127ms | 4236254482（本地原件 `/tmp/hoopa-t134-setup-4236254482/`，不随Git上传） |
| SOCKS截断BND后成功 | PASS；1流/2attempts；成功attempt为124ms | 1898480223（本地原件 `/tmp/hoopa-t134-setup-1898480223/`，不随Git上传） |

最终仓库定向race（本地原件 `/tmp/hoopa-t134-final-repo-race.jsonl`，不随Git上传）为61个顶层通过、5个skip（含子测试371pass/5skip），涉及wire、实验包和只读命令三个包；skip为原身份1项、旧T133显式网络2项、本轮显式质量2项，后两项已由上述最终真实run独立执行。没有重复执行未受影响的旧身份失败矩阵。全量仓库vet无诊断；最终核心source race（本地原件 `/tmp/hoopa-t134-artifact-ul72y63w/quality-source-race.jsonl`，不随Git上传）为5个顶层、12个含子测试通过，0fail/skip；核心定向vet无诊断。这些数字不代表上游全量测试或正式G03。

八份共同wire生产文本与最终core副本逐字节一致；scout对稳定源码树/二进制/补丁/依赖和日志独立只读核验，主代理补核仓库到core文本一致。最终命令见[quickstart](../../specs/001-hoopa-proxy-manager/quickstart.md#t134局部候选与显式复验)，私有证据路径可能随临时目录清理失效，仓库保留摘要、源码补丁和可重跑测试。

## 保留的失败与修正证据

| 旧运行 | 原结果与定位 | 后续独立证据 |
| --- | --- | --- |
| 首版 network-1 | FAIL。collector 的 deliveries 包含已持久去重事件的重放投递；观测端直接累计 setup 次数，把第二次启动时旧 epoch 的重放也算入新数量，触发“完整协议回复前出现 ready”断言。此日志没有证成核心提前 ready。 | 观测按 epoch/event 原文去重，重复原文不一致则失败；并增加回复仍被扣住时 source 侧严格读取，避免 receiver 传送延迟掩盖早发事件。network-2 和稳定复验分别通过；旧 FAIL 保留。 |
| protocol-race 的 DIRECT | FAIL，`socket: operation not permitted`；Pipe HTTP/SOCKS/unknown 子组成功仍保留，但整包失败。 | 获授权回环上下文另跑 DIRECT；不是将环境失败改成 skip/pass。 |
| direct-dns-runtime-race | FAIL，`dns resolve failed: couldn't find ip`。受控 resolver 最初 `Invalid()` 返回 false，走到绕过 fake 的分支，夹具未按预期供给 IP。 | 修正受控 resolver 的分支条件后，direct-dns-runtime-fixed-race 独立通过；不改写旧 run。 |
| rule-dns-race | **构建失败**，测试直接 import outbound 引出 outbound→dns→tunnel 的 test import cycle；目标测试未执行。 | 改为 test-only TCP direct shim，随后另跑；不能把编译失败算作执行通过。 |
| rule-dns-fixed-race | FAIL。shim 用 hostname 再走系统解析，日志为 `lookup rule-slow.invalid ... no such host`，没有使用 rule 已解析的回环 IP。 | shim 改为只拨已解析的 `metadata.DstIP` 后，rule-dns-resolved-race 独立通过；其范围仍限 rule resolver+shim，不升级为完整核心 DNS E2E。 |

## 包级检查、默认 gate 与当前状态

首版默认检查（本地原件 `/tmp/hoopa-t134-setup-default.log`，不随Git上传） 的 bundle/candidate gate 单测通过，真实质量测试显式 skip；r2 默认检查（本地原件 `/tmp/hoopa-t134-setup-r2-default.log`，不随Git上传） 另含 8/9 跳边界单测通过，两项真实质量顶层测试均因默认 gate skip。显式开启的 r2 网络矩阵没有 skip。

首版默认全包 `-race` 通过（1.830s），日志（本地原件 `/tmp/hoopa-t134-setup-package-race.log`，不随Git上传）；r2 默认全包 `-race` 通过（1.834s），日志（本地原件 `/tmp/hoopa-t134-setup-r2-package-race.log`，不随Git上传）；首版 vet（本地原件 `/tmp/hoopa-t134-setup-vet.log`，不随Git上传） 和 r2 vet（本地原件 `/tmp/hoopa-t134-setup-r2-vet.log`，不随Git上传） 均成功退出、无诊断。默认包结果允许网络 gate skip，因此这些包级 pass 本身不能代替上文显式网络运行。

离线工具链为 `/tmp/hoopa-go-1.27.3-cb6ry1lq/go/bin/go`，`GOTOOLCHAIN=local GOPROXY=off GOCACHE=/tmp/hoopa-mihomo-events-buildcache`。r2 真实运行命令结构为：

```sh
HOOPA_QUALITY_NETWORK_TEST=1 \
HOOPA_QUALITY_CANDIDATE=/tmp/hoopa-t134-m3_248tu/mihomo-quality-r2 \
HOOPA_QUALITY_CANDIDATE_SHA256=98d1adc416c261c9c21d24173a742ef35ce3672dd9d2bdd8453607b9ab2267cd \
GOTOOLCHAIN=local GOPROXY=off GOCACHE=/tmp/hoopa-mihomo-events-buildcache \
/tmp/hoopa-go-1.27.3-cb6ry1lq/go/bin/go test -race ./experiments/mihomo-events \
-run '^TestHoopaQualityActual(TwoLines|RejectedThenRetry)$' -count=1 -v
```

| 任务记录状态 | 本次范围 |
| --- | --- |
| 已完成 | 上述wire、bundle、协议/flow单测、真实回环五流及三种失败重试；最终001–006候选fresh apply/构建/复验和T134局部任务验收。 |
| 进行中 | T135加载期能力拒绝/入口保护及T137完整候选仍待实现和验收；T134局部候选已完成归档，不等于T137。 |
| 失败待决策 | 已列失败均保留历史；本报告涉及的夹具缺陷已有独立通过结果，无新增待决策阻塞。 |
| 环境不足未验证 | T132 真实内核身份关联所需受控权限/部署前提仍未满足；不能以协议成功替代身份 load/attach。完整核心 DNS E2E、未测协议和生产命名 provider 能力也没有本报告的通过证据。 |

本次真实 mapping 全为 inline 节点，`provider_name=""`、`provider_id=null`，这不证明命名订阅 provider 的生产映射能力。只校准了 DIRECT 普通 TCP、明文 HTTP CONNECT 200、SOCKS5 及所列 nested；认证、TLS/HTTPS 上游、其他 SOCKS 地址族、其他协议、TFO、未知/lazy 网络协议均不能由这些 run 获得 ready 能力。取消/timeout/unknown 的当前证据来自协议或 flow 夹具，不能写成额外真实固定核心端到端场景。

T135 加载期能力拒绝、T137 最终候选冻结、T129 正式 G03/G05 参考矩阵仍需各自证据；T132 的环境缺口和原 G01 0/100 保留。G01、G02、G03 的正式门槛未因本地 T134 通过而放行，已有 T133 局部结果仍按 [source-transport](source-transport.md) 单独解释；P1 仍未获放行。

wire、bundle 和真实回环验收由 executor 配置 GPT-6.1-sol / medium 执行，核心实现和关键语义由主代理负责；scout配置gpt-6-luna / high只读审查固定源码调用链与稳定归档；子代理未继续派生。本报告只引用已看到的日志，不将未执行检查记为通过。
