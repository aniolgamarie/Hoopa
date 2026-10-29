# 来源恢复与流边界的局部补全记录

日期：2026-10-23。状态：**局部实现及定向验证；T132/T133/T134/T137均未勾选，G02/P0未通过**。本记录接续[002生命周期实验](source-lifecycle.md)，按[ADR007](../adr/007-p0-evidence-closure.md)收敛来源恢复，不覆盖旧结果。原严格身份验收0/100保持失败，未生成passed CapabilityRecord。

后续状态：本文保留本次局部补全时的历史范围与首次失败；随后[来源传输验收](source-transport.md)补齐T133的collector离线/ACK停滞/重开与配对预算，T133本地最小闭环已完成。T132/T134、正式G02及P0仍未完成；不能将本文原run或原机器JSON的历史状态改判为通过。

制品、文件摘要与命令见[候选manifest](../../experiments/mihomo-events/closure-candidate-manifest.json)；测试日志路径、摘要、逐项结果和限制见机器验证记录（本地原件 `../../experiments/mihomo-events/closure-validation.json`，不随Git上传）。本文记录当前已观察到的通过、失败与未执行项，不代表完整G02、协议ready或全项目完成。

## 制品与来源

固定上游为Mihomo `v1.19.26`、commit `fc8c5a24b16991f98cd736950c17d1aa306a5041`。从只读干净checkout复制全新私有树，依次对001、002、003、004、005执行`git apply --check`和实际apply，十步均成功。新树与已审查目标树的文件内容、路径、符号链接及文件/目录权限一致；整树SHA-256为`8e94f93b165057caa03f66579a226b0638c04c429fb52de74dfcbb0022842358`，具体摘要算法见manifest。

| 制品 | SHA-256 |
| --- | --- |
| [004来源恢复与校验补丁](../../experiments/mihomo-events/patches/0004-source-recovery-validation.patch)，12个核心文件 | `53b740fb3131f0428ca5d0dcce6331bf2a60d7921ef0fdff69df4bac06458204` |
| [005流缓冲边界补丁](../../experiments/mihomo-events/patches/0005-stream-buffer-boundaries.patch)，5个HTTP/tunnel文件 | `c5f031ce085e201618a4c3cb987a15e555af826452fe8d668faaaa7de9205dd5` |
| 新实验二进制`mihomo-closure`，50,550,333字节 | `bfc1df3a99871f2b1257e4b28b58b64a97b6289840b6cd63a50c6b86ec75c6d2` |

构建目录为`/tmp/hoopa-t133-closure-artifact-l35s9_fn/source`，二进制在同级`mihomo-closure`。固定Go1.27.3，`GOTOOLCHAIN=local`、`GOPROXY=off`及既有模块缓存，使用`-p=2 -trimpath -buildvcs=false`和固定Version/BuildTime；版本输出为`Mihomo Meta hoopa-t133-closure linux amd64 with go1.27.3 2026-10-23`。`go.mod/go.sum`与上游、003后输入及目标树一致。001–003、旧manifest、旧二进制及用户核心未替换。`-buildvcs=false`的来源证明由独立固定输入和摘要承担，不冒称二进制具有原生干净VCS证明。

## 已接入的边界

[纯wire/epoch校验](../../protocol/telemetrywire/)由Hoopa使用，六个生产文件逐字节复制到核心。严格v1的七种事件形状、重复/转义重名字段、精确整数、来源身份/generation、连续seq、opened先于其他流事件、累计单调、终态/marker与attempt归属共同校验；允许真实v1的多attempt及connection_binding。并发未终结flow最多32，终结历史可超过32。DIRECT空provider原文仍兼容；此实现没有新增v2字段或把旧proc证据重新标成connector能力。

核心持既有source writer锁后，严格配对manifest与frames、核对文件名/身份/配额并稳定全扫描；完整旧帧须通过校验、文件Sync及目录Sync后进入重放集合。损坏、缺manifest、错误绑定或陌生epoch文件保留原字节并拒绝新的完整计量，不截尾或换空日志。Hoopa接收器与reader沿用同源校验；接收器在journal完整Write+Sync后才提交校验状态和返回receipt，同ID同原文重投去重，异文冲突。

append遵循Validate → 完整Write → Sync → Commit，再推进seq和confirmedSize。Write/Sync结果不确定即poison，不推进确认位置；当前sender只读取confirmedSize前缀。完整但未确认的帧保留，重开后按原ID/seq/原文消歧，不能在本次运行提前发送。旧epoch仅从启动验证集合重放，并复核manifest、大小与稳定摘要；后续发现旧内容变化会停止新的完整计量。发送前收集已确认快照，HTTP等待不持source锁。

恢复报告是原始事实的派生投影：opened缺final且没有旧writer结束证明时保持pending、保存最后可信checkpoint、tail=null；只有显式启动/恢复持独占writer锁并核验稳定完整来源，才派生`epoch_ended_without_final`缺口。锁只证明该目录的source writer epoch结束，不证明核心或网络连接在此时结束。精确closed原样幂等保留，不额外制造missing-final gap；维度gap不抹去已知字节。只读恢复不提供writer结束证明，也不写报告或封存旧epoch。

可选reconciliation身份基于原source/epoch/flow/opened及缺final事实类型；重复恢复不增加source_seq、receipt_seq或ByteFact。同ID冲突比较稳定事实，扫描时点等运行元数据不参与。历史gap、扫描结果与当前recording状态分别显示；旧永久未知尾部不阻止已验证的新记录，容量满则仅保留重放。生产持久Gap/Reconciliation、SQL与回收仍由T073/T138承担，来源Sync、接收ACK和SQL不是共同事务。

005保留HTTP CONNECT成功响应解析时预读的隧道payload；lazy首次Write只在trace分支抽取一次真实Write的actual n计量及短写检查，stock分支仍走原Write。该抽取使故障注入覆盖真实调用点，没有把Dial返回、空Write或未知握手提升为协议ready。

## 定向验证

下表来自不同阶段的定向执行，不能将行间数量相加冒充一次完整套件。

| 观察 | 结果与限制 |
| --- | --- |
| manifest配对/内容 | 旧实现6个拒绝反例失败；修复后6个通过，错误证据保持原文 |
| 并发flow上限 | 纯状态旧反例先失败；修复后允许65条终结历史与32条活动流，核心真实生成32条opened再追加合法第33帧时拒绝重开且原文不变 |
| 核心本地race | 新状态规则后的整包28项顶层通过、2项网络测试默认skip；最后定向8项通过，覆盖真实32+第33帧及恢复反例 |
| Hoopa最终race | 纯包18项、接收/恢复25项、sourceinspect 2项顶层通过；原身份集成1项skip，不能算身份通过 |
| 确认/恢复故障 | CP或closed完整Write后Sync不确定、短写/ENOSPC注入、重复字段/错误身份/损坏、反复恢复、历史gap与新ready分离通过；注入不等于真实断电或宿主磁盘填满 |
| 网络重投历史 | 默认2项skip；显式开启后在沙箱因socket权限失败；随后根代理按明确授权执行本地HTTP race，503重投与ACK丢失/重开2项通过、0skip。三个结果分别保留 |
| HTTP预读 | 修复前4个子例中2失败：小payload读到0/7字节，大payload读到4135/8192字节；空payload/逐字节分片通过。修复后4/4 race通过 |
| lazy首次Write | 8个子例通过：partial/zero/full与error/nil、空payload和握手未证。只计actual n、保留原error、短写返回ErrShortWrite，不将失败/空写判为ready；这是既有内联行为的可测试抽取 |
| 静态/构建检查 | 相关vet、gofmt、新fresh树离线构建及版本校验通过；不是上游全量或跨平台测试 |

原始定向日志集中在`/tmp/hoopa-t133-closure-86xtehn1`，Hoopa最终race日志为`/tmp/hoopa-t133-closure-hoopa-final-race.jsonl`；机器记录保存对应路径和SHA。网络显式通过日志为`network-explicit-after.jsonl`，沙箱失败日志为`network-race.log`，两者不能互相替换。

容量扫描样本为**32个epoch文件对、总证据精确8,388,608字节、2,163帧、0个未终结flow**：race扫描2.809717627秒，采样额外Go heap 48,685,216字节，进程生命周期MaxRSS 178,664 KiB。**32个pending flow是另一份独立样本**，不能宣称同一次扫描同时覆盖这两个最大值。60秒/额外512MiB是预先登记的测试回归护栏；Go heap抽样与进程生命周期MaxRSS也不是同一指标，均不代表管理器/整套产品的闲置RSS、CPU或性能预算达标。

## 新二进制首次真实探针

首次运行目录为`/tmp/hoopa-lifecycle-edy6mctq`，摘要`summary.private.json`固定了本次二进制SHA。该组合运行总体`passed=false`，不能用定向单测或后续孤立诊断覆盖其失败。

| 场景 | 已观察结果 |
| --- | --- |
| normal：CONNECT、CONNECT同包预读、SOCKS5域名 | 三条受控流双向各257字节；各1个opened、checkpoint、closed，累计精确；共19事件、complete marker、0gap，manifest绑定通过 |
| active-shutdown | `RuntimeError`，该场景trace目录为空，`passed=false`；cleanup通过不代表流量/恢复通过 |
| crash/reopen | 首次组合运行未执行，不从旧候选结果转移为新二进制通过 |
| 独立active诊断 | `/tmp/hoopa-t133-closure-86xtehn1/isolated-active-diagnostic.json`记录换用新echo、没有前置normal场景后诊断通过；是输入变化后的孤立诊断，不能擦除原组合失败或充当正式门槛 |

在新增固定阶段码、未改变原组合顺序与TCP前提的诊断run `/tmp/hoopa-lifecycle-0b4o42bw` 中，三场景本次通过：crash/reopen旧帧/事件ID/seq不变，新请求在新epoch正常终结。由于未复现异常，该run没有确定首次失败原因，旧失败仍保留。

检查固定源码发现另一个确定的探针前提缺口：`hub/executor/executor.go` 的`ApplyConfig`在`OnRunning`之前开放listeners，`tunnel.handleTCPConn`在非Running时可直接关闭；HTTP CONNECT的200回复也先于交给tunnel。TCP可连接及200头本身不能证明已经能够转发。这个调用顺序是源码事实，不据此认定首次异常就是该竞态。

当前探针增加独立命名的非计量mixed入站，最多4秒内要求CONNECT到本地echo完成11字节`hoopa-ready`回显，记录每次预检结果后才开始主入站计量。预检不进入`HOOPA_TRACE_INBOUND`、不消费被测flow分母；每个新core都须再次通过。新增纯测试覆盖TCP已接受但协议失败时继续等待、成功转发才就绪、截止与子进程退出失败关闭，以及固定错误码脱敏；本次共16项Python纯测试通过。该前提只证明此受控路径可转发，不是产品协议ready测量或所有路由均已就绪。

修后run `/tmp/hoopa-lifecycle-coop5rr8` 的normal、active-shutdown、crash-restart三项通过，所有owned children/echo线程清理通过：normal三流各257/257字节、active有1gap/1marker、crash旧原帧/事件位置不变且新epoch精确终结。另以Go `sourceinspect --manifest`检查四对真实文件：4条closed、1条gap、1条pending；pending保留可信checkpoint和null尾部、不生成reconciliation_id，四对文件hash/size/mtime不变。只读报告没有伪造writer结束证据；核心持锁重开后的gap投影另由定向核心测试验证。这些结果与首次失败分别保存在机器JSON中。

最终仓库全量`go test -race -count=1 -json ./...`在默认沙箱为96项顶层通过、2项skip、3项失败：两个既有sourceidentity回环测试与`TestSandboxPrivateAndIndependent`均因`socket: operation not permitted`未完成。此结果不是全部green，也不改变正式G01的旧失败；没有为了得到green重跑身份100流。全量`go vet ./...`通过，局部45项race及明确授权的两项核心网络race通过保持各自范围。

## 未放行的能力

T132仍缺正式正向connector关联能力；本轮没有加载BPF或改变系统权限。T133已有真实组合恢复小样本，但collector离线/背压完整矩阵、实际协议lazy边界及首次异常归因仍未闭合，任务保持未完成。T134协议ready、lazy握手成功边界、两条独立实际线路及稳定节点历史没有因此通过。G01原0/100失败保留，G02完整矩阵、G03及T137未放行；新normal三流也不能替代正式100流。

生产SQL/有界GC、真实宿主ENOSPC/断电、SSH/注销/休眠、其他平台和产品性能预算不由本轮通过。记录范围是`T133_source_recovery_plus_T134_buffer_precondition_not_T137`，不是P0或整项目完成声明。
