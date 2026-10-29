# 同源 Mihomo 事件候选准备（T126）

> 运行报告与准备/诊断过程材料保留在本地原位置，不随 Git 同步。“本地原件”代码路径不是源码 clone 文件；正式 acceptance、构建身份与源码保留。只读验收 gate 仍需完整且 hash 匹配的本地报告/私有原件，不能仅凭本页结论宣称重新验收通过。

当前状态（2026-10-26）：140项/27完成；固定0017/source5的[T127受限正式身份验收及T012评审](../../docs/validation/source5-formal-identity.md)已完成，正向100/100及各独立边界分母分别记录。[T128恢复准备](../../docs/validation/source5-recovery-preparation.md)已完成三个真实传输case的新6/6，完整恢复矩阵与T129未完成，P0/P1仍未放行。当前制品见[0017 manifest](core-program-rules-candidate-manifest.json)；旧0/100、首轮60/100及准备失败不重评分。

以下保留早期T126及后续局部候选的历史材料，不代表当前所有任务状态。

T126阶段：已取得隔离可运行候选；当时T127的100短流严格身份断言失败，完整矩阵未完成，T128/T129未启动。
2026-10-23阶段状态：T130/T131完成负向诊断/裁决，T133完成本地来源/传输闭环，T134完成[质量/线路局部验收](../../docs/validation/quality-ready.md)，T135完成[受保护实验入口验收](../../docs/validation/protected-entry.md)，当前139项/21完成；T132/T127/T128/T129/T136/T137/T018与正式门槛仍未通过，P0/P1未放行。下方按时间保留各旧候选状态，不将后续能力回填到旧hash。
基础代理的既有校准结果、候选源码审查、计数帧解析测试是三种不同证据，均不能替代完整来源验收。
总体路线见 [ADR 005](../../docs/adr/005-telemetry-replan.md)，逐项改动见[上游补丁提案](upstream-proposal.md)。
本轮的实际补丁/构建输入见[候选manifest](candidate-manifest.json)与[补丁说明](patches/README.md)，
实际结果及未通过项见[验证记录](../../docs/validation/native-candidate.md)。

## 2026-10-24：有限 connector 核心接线

[008](patches/0008-core-connector-admission.patch)将原 Accept→Admit→metadata 值复制→同 flow profile3 opened 接通，并严格核对 connector JSON、原始 ABI 和 v2 质量/计数状态。旧 process 保持 unknown，正式身份能力 false；Flow/RecoveryFlow 不展示身份，只有 durable opened 原帧与解码值保留 connector。profile3 受保护 tracing 绑定身份 handoff，质量开关也显式识别 3；缺失/冲突不能 fallback 到 proc。

[009](patches/0009-metered-tcp-half-close.patch)独立修复实测发现的早期半关闭回归：common.Cast 可穿 generic tracker，但 Hoopa 计量 wrapper 故意不暴露 Upstream，曾漏传 CloseWrite；修复只显式转发半关闭控制操作，不开放读写 fastpath。失败的原请求和修后同请求分别保留。真实最终候选已核对两协议正常与原 early-half-close 的双向各 33 B、重开旧文件 hash 不变与两世代，以及三类启动错误的零 source/零 listener 拒绝；完整场景以[核心验收](../../docs/validation/connector-core.md)和机器摘要（本地原件 `../sourceidentity/core-validation.json`，不随Git上传）为准。

最终 001–009 候选 SHA256 `452effa120f0f0beea122169c9e442b6338389bc4768111c4ed7ffb8d5f44f9b`，固定源码 v1.19.26 / `fc8c5a24b16991f98cd736950c17d1aa306a5041`；[manifest](core-connector-candidate-manifest.json)记录 fresh apply/build、完整树和权限。构建无版本 ldflags，打印默认 `1.10.0` 不代表源码版本。最终默认协议/receiver race 有 406 pass、7 个显式网络 skip；授权 capability 回环矩阵 49 pass、0 skip，均为各自测试口径，不能合成无重复总数或正式 G01 分母。

严格字段、BOOT 与质量时钟、epoch 版本及重放边界见[v3 契约](../../specs/001-hoopa-proxy-manager/contracts/source-connector-v3.md)。有限接线已实测，生产支持和完整身份仍未认证；名称非原子 unverified、路径 unknown、PROCESS/UID 仍拒绝。完整 scope/namespace、exec/改名、长期回收及生产权限部署待验，T136 管理器退出/接管另验。T132/T137 未勾选，139/21、正式 G01 新增 0 和旧 0/100 不变，旧补丁/JSON/失败不覆盖。

## 固定输入与已确认边界

- 官方源码：Mihomo `v1.19.26`，commit `fc8c5a24b16991f98cd736950c17d1aa306a5041`。
  本次只读缓存位于 `/tmp/hoopa-mihomo-v1.19.26-audit/source`；[source.lock.json](source.lock.json)记录已审查文件的 SHA-256。
  固定文件与调用位置的结论见[源码审查](source-review.md)，不把临时缓存作为长期证据的唯一副本。
- 现有外部二进制 SHA-256 为 `78f3d225a53f8345db5b1f3f147d155ef5a1d56843931f6d61f908045cefb789`。
  它报告 v1.19.26，VCS revision 与标签相同，但 module 带 `+dirty`、`vcs.modified=true`。
  因此无法证明该二进制等同干净标签源码；原运行观察继续绑定该二进制摘要，不移植为新候选结果。
- 已审查 stock 接口没有完整 PID/启动身份、源端持久终末事件、建立计时或三值进程规则保证。
  所有完整来源能力仍为未验证，不能因 `/connections`、进程名或解析器存在而置为已支持。
- 用户在具体提案和隔离实验授权问题后再次调用implement，本次据此在私有/tmp工作副本实施/构建了实验增强；
  未替换或重启用户核心，未发行核心分支，也没有启用特权采集、TUN或系统服务。

## 已实现：计数帧解码准备

`counter_event.go` 的 `DecodeCounterEvent` 只接受候选原生 `bytes_checkpoint` / `closed` 计数帧，
返回类型化标识、UTC 时间、来源序号和精确累计字节。它不产生事件、不证明来源身份、不推导增量，
也不证明跨帧序号连续、终末恰好一次、路由归属或磁盘持久化。

这一窄 wire profile 的 JSON 对象必须恰好包含以下 13 个字段：

| 字段 | 要求 |
|---|---|
| schema_version | 数字 token `1`；`1.0`、`1e0`、字符串及未知版本均拒绝 |
| source_instance_id / source_epoch / event_id | 本地稳定 token；分别标识来源、计数世代及事件 |
| source_seq | 非零 uint64 十进制字符串；由产生方持久序列分配 |
| flow_id / attempt_id / config_generation | 本地稳定 token；不得用当前配置或接收顺序补填 |
| observed_at | 合法 RFC3339，至多纳秒精度，使用 UTC `Z` |
| boundary_ref | 此候选固定为 `mihomo_proxy_stream_v1`；解析接受不代表边界已验收 |
| kind | `bytes_checkpoint` 或 `closed` |
| upload_bytes / download_bytes | canonical uint64 十进制字符串，表示累计值，允许 `"0"` |

合成示例：

```json
{
  "schema_version": 1,
  "source_instance_id": "core-example",
  "source_epoch": "epoch-example",
  "event_id": "event-2",
  "source_seq": "2",
  "flow_id": "flow-example",
  "attempt_id": "attempt-1",
  "observed_at": "2026-10-22T10:11:12Z",
  "config_generation": "generation-example",
  "boundary_ref": "mihomo_proxy_stream_v1",
  "kind": "closed",
  "upload_bytes": "9007199254740993",
  "download_bytes": "0"
}
```

上限为 64 KiB，拒绝缺字段、未知字段、重复字段（包括转义后的重名键）、null、额外 JSON、
数字字节值、前导零和溢出。`receipt_seq` 不属于源帧，必须由独立接收日志保存；legacy 无 source_seq
的 GOST 回调不通过此解码器，继续使用其原边界及接收记录。错误只返回固定类别，不回显敏感输入。
在该计数解析准备阶段，完整事件族的 opened、身份、选路、计时和 gap 尚须与增强来源共同实现；后续 004/006/008 的实现分节记录，不能将此计数子集当作完整协议。

从仓库根目录用固定 Go 1.27.3 执行（不启动代理或联网）：

```sh
GOTOOLCHAIN=local GOCACHE=/tmp/hoopa-go-cache GOMODCACHE=/tmp/hoopa-go-modcache GOPROXY=off \
  /tmp/hoopa-go-1.27.3-cb6ry1lq/go/bin/go test -race -v ./experiments/mihomo-events
GOTOOLCHAIN=local GOCACHE=/tmp/hoopa-go-cache GOMODCACHE=/tmp/hoopa-go-modcache GOPROXY=off \
  /tmp/hoopa-go-1.27.3-cb6ry1lq/go/bin/go vet ./experiments/mihomo-events
```

2026-10-22：5 项顶层测试、111 项子测试通过，0 skip/FAIL/竞态；vet 和 gofmt 通过。
这是上述解析器测试结果，不是核心或 G01–G05 的运行结果。

## 隔离来源的实际实现与边界（001历史）

固定16文件补丁增加显式入站观测、原始socket持有查询、精确名称/路径与AND三值匹配、逐attempt计时/链、
tracker绑定和不会被buffer/Unwrap绕过的计数包装。trace关闭先终止并等待I/O计数，再尝试同步保存终末后移出活动表；
落盘失败不报告终末成功，poison并保留opened census供判定缺口。完整故障恢复矩阵尚未通过。

源端使用带SHA-256校验的私有帧文件和Write+Sync后推进的source_seq；源epoch有binary/hash/boot/PID/start/nonce清单。
预留独立closed及维度gap空间，中间事件不能耗尽；opened写前失败释放预留，不确定写入则poison。
发送器只连接显式带凭据的loopback HTTP接收端，重投原事件ID/原文；采集端仍须独立持久确认。
本原型不回收源段，固定8MiB、32epoch、32活跃flow、8KiB帧上限；容量/epoch到上限只回放旧记录、拒绝新计量，
不启动空白新来源来掩盖历史。此处描述001初始候选；周期checkpoint与epoch_closed随后由002补充，严格重开/派生恢复由004补充，完整SQL消费者和生产回收协议仍未实现。

001候选当时实际只验收了单次成功建链的直接回环流量；重试字节的逐attempt拆分、两节点历史及协议ready仍未验收，
不能把最后attempt或当前group当成全部流量的线路。配置加载期拒绝未接线，运行期只明确支持精确process name/path、
AND及白名单原子条件；其他组合明确unsupported，不能宣称完整进程规则已交付。配置重载/世代变更未验收，
本轮使用固定generation、无外部controller的不可变合成配置。当前补丁仅供Linux amd64/固定Go1.27.3实验，非其他平台支持声明。

`native_probe.py`使用两个同名子进程和两个受控域名；每条truth先落盘Sync，子进程收到确认才发CONNECT。
`identity_test.go`默认未提供固定candidate/hash时明确skip；显式G01运行不能把身份不可见改成skip：

```sh
HOOPA_NATIVE_CANDIDATE=/tmp/hoopa-mihomo-events-ildaobd2/mihomo-hoopa-candidate \
HOOPA_NATIVE_CANDIDATE_SHA256=76e0e114ac9a669ec0eb131f35ead972d3e24b2dceeb270e72a3e879f5b9c32e \
GOTOOLCHAIN=local GOCACHE=/tmp/hoopa-go-cache GOMODCACHE=/tmp/hoopa-go-modcache GOPROXY=off \
  /tmp/hoopa-go-1.27.3-cb6ry1lq/go/bin/go test -count=1 -v ./experiments/mihomo-events -run '^TestNativeCandidateIdentity$'
```

当前结果为100/100域名和终末字节正确、0/100原始身份正确（100条owner_visibility），真实测试exit1。
全部样本保留；不能据此将T127勾选或放行T128/T129/P1。下一步需要可证明owner可见性的合适环境或具体身份方案调整，
不能默认提权、忽略不可见进程或降低正确率要求。

## T130/T133 新增实验入口

`experiments/sourceidentity/cmd/preflight/`输出有界只读环境诊断；`native_probe.py`可显式指定该observer及其SHA，只允许两条非身份验收样本。两个源socket持有后才扫描；扫描可读不证明唯一owner，普通沙箱与可执行回环核心的环境不能混用。详情及原负向结果见[前提诊断](../../docs/validation/p0-prerequisites.md)。

`source_event.go`对全部七种当前候选事件进行形状校验；`receiver_linux.go`检查来源序列后持久接收；`source_lifecycle.go`在停止写入的帧快照上恢复pending/gap。共同校验现在位于`protocol/telemetrywire/`，并以六份同源文本接入004核心的重开、发送、receiver及reader。`cmd/sourceinspect/`不写日志、不发送ACK；`--epoch`旧模式输出计数汇总，新增`--manifest`模式输出含flow明细的派生报告，只应保存于私有目录。只读者不提供writer结束证明，缺final仍为pending，不能以接收恢复替代源端恢复。

`lifecycle_probe.py`必须显式给候选绝对路径/SHA和`--allow-loopback`，实际执行0700运行目录内已验0500副本；每次创建新私有证据目录，运行三个小规模场景。例（只适用本地隔离实验，不是正式G01或G02）：

```sh
python3 -B experiments/mihomo-events/lifecycle_probe.py \
  --candidate /tmp/hoopa-t133-4m34o312/mihomo-t133 \
  --sha256 aad983b1e6597704fa5de84793da5869d9d6680b43b57c689a8be716292b5655 \
  --allow-loopback
```

上述002候选的三场景通过且已清理owned children；原帧另经Go reader检查。Python纯测试可用`python3 -B -m unittest discover -s experiments/mihomo-events -p 'test_*probe.py' -v`执行，不启动核心。002的hash/补丁、精确Go验证数量及当时范围见[生命周期manifest](lifecycle-candidate-manifest.json)和[当时实际验证](../../docs/validation/source-lifecycle.md)。新候选须独立验证，不能直接沿用002结果。

## 后续收敛与SOCKS5前提修复

[ADR007](../../docs/adr/007-p0-evidence-closure.md)采用用户确认的connect_initiator_v1建连实例/名称路径快照；FD共享/转交不改变已证connector；当时入口唯一关联仍待原型，后续有限关联与核心接线见页首。T133按原帧重放+派生pending/gap报告收敛，共用严格校验，不要求v2恢复事件/跨epoch引用图。新身份/质量字段需先冻结profile，再按协议ready、真实两线路/实际n字节段校准；这些是该阶段待实现/实证的要求，后续结果分节记录，不回填为旧v1候选通过。

固定SOCKS5 ClientHandshake未检查回复成功码的问题，已另在私有副本最小修复并形成[003补丁](patches/0003-socks5-reply-validation.patch)及27项合成流证据（本地原件 `socks5-reply-validation.json`，不随Git上传）。该次003实验只有源码补丁和纯协议测试；以下新组合二进制包含该修复，旧二进制不具备此修复。截至003阶段真实ready标定尚未完成，后续006验收另记。

## 004/005：来源恢复及预读边界

新增[004](patches/0004-source-recovery-validation.patch)接好严格v1/manifest配对、source重开和confirmed prefix发送、派生pending/gap恢复；新增[005](patches/0005-stream-buffer-boundaries.patch)修复HTTP CONNECT同包回复/payload预读丢失，并抽取lazy首次Write实际n测试边界。[独立manifest](closure-candidate-manifest.json)记录从干净上游实际应用001–005并构建的局部候选，完整结果见[来源恢复记录](../../docs/validation/source-recovery-closure.md)及机器摘要（本地原件 `closure-validation.json`，不随Git上传）。

```sh
python3 -B experiments/mihomo-events/lifecycle_probe.py \
  --candidate /tmp/hoopa-t133-closure-artifact-l35s9_fn/mihomo-closure \
  --sha256 bfc1df3a99871f2b1257e4b28b58b64a97b6289840b6cd63a50c6b86ec75c6d2 \
  --allow-loopback
```

该命令需要显式本地回环权限和仍存在的固定制品，不下载或安装核心。首次组合normal三流通过、active失败、crash未执行，结果保留；随后有诊断run通过，但没有定位首次异常的确切原因。源码已证TCP监听可先于Running开放，因此当前探针先通过独立、非计量入站完成CONNECT/11字节回显，再执行主入站257字节测量；修改前后run独立保存。修后normal/active/crash三小场景及四对只读快照通过，16项Python纯测试通过；截至该次组合验证，它们尚不构成完整T133/G02或协议ready/两实际线路验收；该run和原0/100保持原状态，后续T133状态见下节。

## 后续T133传输验收

`transport_integration_test.go`补齐collector真实离线、持久接收后扣ACK、SIGKILL、receiver与core重开。离线阶段独占bind但不listen；主流量与CP/closed完成后才启动接收器。重放使用实际`SourceReceiver.Accept`，原帧、ID/seq和receipt不变；原closed累计每epoch只出现一次。新默认测试明确skip，显式回环race两项通过。另修复`ReadSourceRecovery`的manifest+frames总字节预算，精确上限接受、超限拒绝，不无限读取合法小帧。

[本地验收](../../docs/validation/source-transport.md)及新机器记录（本地原件 `transport-validation.json`，不随Git上传）给出固定命令和范围。截至T133阶段，本地最小闭环已完成；当时T128正式G02、T132身份、T134协议ready/实际线路及T137仍未完成。上文及`closure-validation.json`保留当时状态，原0/100和首次active失败不改。核心仍为同一001–005二进制，本次没有覆盖旧patch/manifest或重新构建核心。

## T134质量/实际线路局部验收

新增[严格v2契约](../../specs/001-hoopa-proxy-manager/contracts/telemetry-quality-v2.md)、纯wire/epoch校验、[bundle生成](quality_bundle.go)与[真实受控探针](setup_probe_test.go)。006在实际Conn保存协议ready和固定链，首次Write/relay按actual n累计到原段；v1原文、总量边界和恢复语义保留。固定最终候选8流/11attempts通过，涵盖HTTP/SOCKS两线路、DIRECT、nested、切组保留旧链、改名重开及3类失败后重试。

[新manifest](quality-candidate-manifest.json)、机器结果（本地原件 `quality-validation.json`，不随Git上传）与[分层验收报告](../../docs/validation/quality-ready.md)绑定实际范围；显式复验命令见[quickstart](../../specs/001-hoopa-proxy-manager/quickstart.md#t134局部候选与显式复验)。二进制版本`hoopa-t134-quality-final`、SHA256 `9d52edee280d7e4893911e3c4a6ae43150cf4914421431f7ee19567505459b29`，仅私有tmp保存，仓库不含二进制。命名订阅provider、身份后端、配置能力拒绝及正式G03不在这次通过声明中；原失败与旧hash记录保留。

## T135受保护实验入口局部验收

新增[有界JSON校验器](capabilityprofile/profile.go)、[真实入口测试](capability_test.go)、[trace准入测试](capability_trace_test.go)及[受保护契约](../../specs/001-hoopa-proxy-manager/contracts/protected-core-v1.md)。007在新私有上游副本依次应用001–007，整树内容/权限一致后离线构建；最终版本`hoopa-t135-guard-final`，SHA256 `7c022d3263cf5c16d72fd68834041ae2bfb15e748283b6aa75d529c1fdf4440f`，二进制位于`/tmp/hoopa-t135-artifact-tg95ee9o/mihomo-guard`，不覆盖旧制品。

[manifest](capability-candidate-manifest.json)、机器验证（本地原件 `capability-validation.json`，不随Git上传）与[验收报告](../../docs/validation/protected-entry.md)记录完整范围，显式离线复验命令见[quickstart](../../specs/001-hoopa-proxy-manager/quickstart.md#t135受保护实验入口与显式复验)。最终真实回环2顶层、含子测试49项pass，0fail/skip，2.839s；harness开启race，核心子进程为普通构建。默认网络gate skip，显式缺候选必须失败。

加载期完整能力拒绝、来源可写/容量准入、全部端点先bind后listen、独立入口认证、HTTP逐请求重验、A/B原序首匹配、只读status、不可变generation/SIGHUP、drain旧TCP续传与独立新进程replace均按范围验收。进程实例/名称/路径及不支持组合仍加载期拒绝，identity均false，AND unknown只有既有单测；不宣称正向身份、热reload、同UID OS隔离或其他平台/产品支持。T135本地完成不关闭T132/T127/T128/T129/T136/T137/T018，P0/P1仍未放行，旧失败不回填。

## T041 来源编译与受保护自动组

[0020](patches/0020-sources-control-v2.patch)在独立固定副本中增加 sources-v2 控制协议和有限自动组保护，保留 basic-v1。新静态核心和产品编译、准入、接管/停止/重开已完成[有限验收](../../docs/validation/us3-compiler.md)，批准绑定见[独立 artifact](approved-sources-core-v2.json)。未成功探测及全失败时拒绝，实时选择不进入不可变策略快照；不是进程计量或业务 ready 延迟能力。动态构建的首次准入失败与撤回记录保留，新 SHA 不继承旧完整故障矩阵。二进制仅在忽略目录，仓库不提交制品。
