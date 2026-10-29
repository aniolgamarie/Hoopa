# G05 路由与完整配置切换验收

2026-10-26：T016 已在限定 Linux 原型范围通过。最终只读入口核验 16 组、
39 次已接受 VM 运行、127 次应用尝试、124 条来源流、59 个 epoch，合计
805 必需帧和 9 个 checkpoint；正式 race 验收（本地原件 `../../experiments/mihomo-events/source5-routing-gate-validation.json`，不随Git上传）
为汇总测试 1 通过、输入契约测试 1 通过、0 失败、0 跳过，无新增运行样本。
固定多入口核心为 `5e934bdc…11a388`，控制 helper 为 `1c876099…107f95`；
完整摘要与逐场原件见[接受清单](../../experiments/mihomo-events/source5-routing-acceptance.json)。
T017/T018 另行裁决；本次不表示 P0、P1 或生产多入口产品完成。

以下保留各阶段证据与失败记录。原 0017 核心和 ad4a 控制 helper 的身份接管
限单入口；旧完整二进制的验收仍归原 hash，不能改标为当前候选的运行结果。

## 已完成：多入口身份对象的内核前提

独立双 Owner 探针记录（本地原件 `../../experiments/mihomo-events/source5-multi-owner-prerequisite-validation.json`，不随Git上传）
绑定固定 Linux 6.18.54.1、原 scoped executable BPF 对象和 libbpf。
一次隔离 guest 实测证明：同一空 source cgroup、同一 root cgroup 上，端口
18993/18995、capture generation 16001/16002 的两份对象可以同时挂接；各自的
FD、map/program/link ID 独立。第二份挂接后两份均重新核验；关闭第一份后，
第二份的配置、scope 和资源绑定保持不变。每份清理一次，各 21 个自有 FD
检查为 EBADF，本地 owned links 为空，空 source leaf 已移除。

原始 console、guest readiness、事件顺序、对象绑定及清理结果经独立复核，
root 另存受限接受记录。QEMU exit 0 且进程已回收。这只是一次挂接前提实验，
应用槽和 source 事件样本均为 0，不证明核心多入口转发，不证明所有内核对象
已销毁。原 `vm_exited_unverified` 运行结果保持原样。

首次在执行沙箱中启动时 `/dev/kvm` 不可见，QEMU 初始化报 ENOENT，未进入
guest。该失败及原登记保留。确认宿主执行环境有 KVM 设备后，以独立登记重试；
重试只重绑镜像元数据路径，guest 镜像和执行代码的字节均未改变。

## 已完成有限启动负例，保留原观测失败

v3 原运行记录（本地原件 `../../experiments/mihomo-events/source5-native-negative-attempt-v3-validation.json`，不随Git上传）
保留两场结果：对象别名场的原 canonical 通过，96 轮 TCP/IPv6 表采样均见
已知监听端口、未见受保护端口监听；部分绑定场 103 轮采样也完成，但来源文件
为零，未满足预定的一个完整零流量 epoch，guest 与 canonical 均失败。

代码审查发现，启动期间从 root cgroup 主动连接受保护入口会增加 BPF 的
`capture_faults`，而核心接管要求初始计数为零。部分绑定场有 8 次 entry-a
EPERM，发生在首次观察到核心可执行文件之前。这个机制与提前拒绝相符；
原运行没有导出实际计数或核心内部拒绝点，不能将推断写成已测事实。
对象别名场也有主动探测干扰，root 暂不接受其精确拒绝原因，原通过记录保持。

v4 已将监测就绪到核心回收的整个区间改为只读 socket 表与进程/FD 观测，
主动诊断连接仅留在启动前和回收后，两场均通过原 checker、原件独审及 root 接受。
第二入口占用（本地原件 `../../experiments/mihomo-events/source5-multi-entry-partial-bind-validation.json`，不随Git上传）
产生预定的一个完整零流量 epoch；106 轮表采样、80 次前后连接拒绝，核心退出后
18993/18994 可重新绑定，自有占用 listener 保持原身份与响应。16 份导出与原归档一致。
实际对象别名（本地原件 `../../experiments/mihomo-events/source5-multi-entry-alias-validation.json`，不随Git上传）
将 entry-b 的 connect_records 从实际 map ID 12 替换为 entry-a 的 ID 2，核心拒绝，
来源事件为零；84 轮表采样、120 次前后连接拒绝，15 份导出与原归档一致。

两场均没有 during 主动连接，保留已知 listener 每轮可见及原来源/释放断言，
核心与见证进程均回收。监听缺席仍是有限采样结论；这不证明任意短时监听都能
被发现，也不追溯确定 v3 的内部计数或拒绝点。完整旧配置回滚继续单独验证。

## 已完成有限校准：入口绑定与事件记录

新候选采用版本化外层 handoff，把每个命名入口和端口绑定到独立的既有 v4
Owner。所有子记录先严格解析，再检查全局 FD 与实际内核对象 ID，拒绝别名、
缺项和错配；任何失败不得开放部分入口。原单入口接口、BPF 格式及事件 wire
格式保持原契约，新增集合接口单独验证。

原单入口路径的 `HOOPA_TRACE_INBOUND` 只选择一个入口。新候选由受保护配置一次
绑定不可变入口集合，所有入口共享同一 source epoch，各连接仍保留自己的入口、
进程及线路证据。状态和诊断逐入口列出，读取失败明确 unavailable；集合诊断是
顺序读取，不能声称是内核原子快照。

新核心与独立 bootstrap 的离线实施已完成并经稳定差异复核。核心固定摘要为
`5e934bdc28dd07e7b7f8cf372d1d4f7a553a3aa3bd835de24f982dfed011a388`；
四 Owner 定向 race 检查 9 个顶层用例、含子用例 37 项通过，无失败或跳过。
前一轮组件检查含子用例 540 项通过、4 项需运行环境的 opt-in 检查跳过，
根包 60 项通过；这些计数各有独立范围，不相加充当运行样本。
自包含 bootstrap 的 22 项纯逻辑/替身检查通过，已核验搬迁后加载与五个依赖
分别被篡改时的拒绝。

双应用直接校准（本地原件 `../../experiments/mihomo-events/source5-dual-entry-direct-validation.json`，不随Git上传）
已通过完整校验、独立原件复核和 root 受限接受：一场隔离 VM、两个应用、两个
命名入口的 HTTP/SOCKS 共四槽通过；41 份导出、25 条来源事件、42 个独立
Owner FD 和 4 条原始 connector 证据一致。app-a 经 group:US 到 node:a，app-b
到 node:b；两入口都正常转发，每条终末值上、下行各 33 字节，来源 epoch 完整
关闭，核心及四个客户端正常退出并回收。JP 组本次未执行，node:b 是夹具节点，
尚不代表订阅固定节点验收。

第一次主机校验在恢复读取阶段失败：提取器生成 `0644` 文件，固定 reader
要求 `0600`，拒绝发生在解析前。原归档、71 个原运行文件及首次失败均保留。
另建 `0700` 目录内的同字节 `0600` 副本，经独立审查的派生 checker 保持原
84 条断言和 reader 不变，在同一场 VM 的证据上通过。没有重跑 VM；副本权限
不作为 guest 文件权限证据。恢复结果为四条 closed、零尾部残片、无维度缺口；
`recording_status=unverified`、`writer_end_evidence=null`，不证明来源 fsync 或 SQL。

四条来源事件记录的 ready 耗时为 13/11/32/31 ms，与其单调时钟差向下取整一致；
此校准没有另加独立等待观测，不代替新核心完整质量门槛。导出排除了代理认证
凭据和完整环境；预定的夹具关联标记及 33 字节测试正文明确保留。

新核心的原单入口兼容校准（本地原件 `../../experiments/mihomo-events/source5-new-core-legacy-validation.json`，不随Git上传）
已受限接受：原 bootstrap/v4、21 个 Owner FD、schema1 状态，以及两个独立
进程的 HTTP/SOCKS 转发均通过。29 份导出、13 条来源事件逐项核对，每条流
上、下行各 33 字节，原进程、来源和退出回收证据一致。这不替代多入口负例。

对象别名首场负例（本地原件 `../../experiments/mihomo-events/source5-native-negative-attempt-v2-validation.json`，不随Git上传）
保留为失败：核心确实以固定保护错误退出，但原方案要求 cgroup 外探测全部
返回 ECONNREFUSED，与入口 BPF 保护冲突。285 次探测中 273 次 ECONNREFUSED、
12 次 EPERM；后者只能表示探测不可用，不能证明端口关闭。原 guest 错误、
checker exit1、归档和登记均不改写。后续 v3/v4 的独立运行和观测修订见上文；
v4 已完成两项有限负例验收，不能据此重判首场失败。采样仍不能排除间隙内
极短的监听，完整旧配置回滚另行验证。

旧 helper 的事务、锁、意图记录和恢复语义不在此步骤改动。新核心
哈希变化后，须重验受影响的身份、来源恢复、质量、认证、生命周期及回滚路径，
不能继承旧二进制的门槛通过。

新控制适配器已受限离线接受，固定 helper 摘要为
`1c87609972b0ac5570101c50945bfe4aa09a4687a433adb9c538722871107f95`。
assets v1 继续绑定旧单入口核心；v2 明确绑定新核心和 2–4 个入口。状态采用
严格 schema 分支，逐入口核对世代和诊断；缺失或 unavailable 不填成零。
沿用原控制凭据发送前的同连接 owner 核验，事务、锁、claim 和 8 KiB bundle
上限保持。最终定向 race 63 主测试、含子用例 230 项通过，无失败或跳过；
vet/build 通过。独审发现的环境标记漏检和新模式误用旧 nonce 限制均先复现再
修正；早期编译失败及误选 native 测试的 socket 权限拒绝保留，未计通过。
新 helper 的有限生命周期校准（本地原件 `../../experiments/mihomo-events/source5-multi-entry-control-validation.json`，不随Git上传）
已通过完整 checker、原件独审和 root 接受：manager85 被 SIGKILL 并回收后，
新的 client114/117 在重开 manager 之前经两入口继续转发。manager120 只读重开，
核验同一 core93/start620、来源 epoch、42 个已列出的 Owner FD 和未改写的控制
元数据；显式 stop 将 revision1 提交为 stopped revision2，核心由 PID1 精确回收。
一场 VM 共四流、45 份导出、25 帧，每流 33/33 字节。此处列出的 FD 不是整个
进程的全量 FD 清单；这也不证明完整配置回滚、并发冲突或各持久边界故障恢复。

## 并发切换：新登记的完整场景已受限接受

C01 v2 完整记录（本地原件 `../../experiments/mihomo-events/source5-concurrent-control-validation.json`，不随Git上传）
经原 checker、原始归档独审及 root 接受：两个真实 IPC 调用者 PID110/111
使用同一 revision1，请求在途区间重叠约 1.706 秒；race-a 唯一提交 revision2，
race-b 返回 409 CONFLICT，未留下其操作、bundle 或 launch。随后三个有效请求
分别验证原请求重试返回原操作、同 ID 异内容返回 ID_CONFLICT、全新旧版本请求
返回 CONFLICT；完整路径/元数据、来源、状态和已列 42 FD 快照均保持。

本场四个真实应用槽全部完成：初始 HTTP/SOCKS 经 node:a/node:b，替换后经
node:b/node:a。两个独立运行世代各 13 帧，共 26 帧、60 份导出；每流终末
33/33 字节，建立耗时 11/31/31/11 ms。显式 stop 提交 stopped revision3。
QEMU 正常退出并回收；两个核心只有原始 /proc absent 证据，管理器拥有的
wait 状态未导出，不补写为 exit0。来源恢复仍为 recording_status unverified、
writer_end_evidence null；此处不证明持久故障恢复或完整 G05。

C01 首场失败记录（本地原件 `../../experiments/mihomo-events/source5-concurrent-control-attempt-v1-validation.json`，不随Git上传）
保留真实重叠请求的一个 revision2 提交、一个 CONFLICT，以及原提交重试和
ID_CONFLICT 的结果。原登记四个应用槽仅完成前两个，只有初始 epoch 的 13 帧；
后续旧版本探针、stop、后两应用和第二来源对未完整导出，不能记为 C01 通过。

夹具把多个完整状态快照重复保存为格式化 JSON，后续保存超过 256 KiB 上限；
cleanup 再次保存时触发同一上限，遮蔽了主异常并中断最后证据导出。原 traceback
只直接证明 cleanup 的保存失败；此前 fresh-stale 保存失败由代码顺序推断。
v2 在原上限内使用完整对象的紧凑 JSON，并确保保存异常仍记录原错误及完成其他
清理。原 19 项测试及新增 4 项回归通过；实际完整 race 记录为 207437 字节，
低于原 262144 字节上限。新运行完成原四槽、两 epoch、26 帧要求；合成大小
回归单列，首场失败不重评。完整旧 bundle 回滚的独立场景见下文；运行态
已应用但 active 发布失败的首场不完整运行和夹具修订见下文。

## 完整旧配置回滚：第二入口占用场景已受限接受

F10/F33 实际记录（本地原件 `../../experiments/mihomo-events/source5-full-rollback-validation.json`，不随Git上传）
经过原 checker、原始归档独审及 root 接受。一场 VM、四个应用槽、三个运行
世代共 27 帧和 64 份导出均与预登记一致。独立见证进程先占用候选第二入口
18999；控制器停下旧 core92/start559，候选 core113/start888 启动失败，随后
用新的 core123/start1052 恢复完整旧 bundle。配置、规则、组选择、认证和
两个入口均与旧态一致，route-map 只更换运行 generation；active 保持 revision1。

初始与恢复后各运行两个真实的新应用连接，HTTP/SOCKS 均仍走原 node:a/node:b。
每流终末 33/33 字节，建立耗时依次为 11/32/11/31 ms；初始、失败目标、恢复
世代分别有 13、1、13 帧，失败目标为完整零流量 epoch。同一失败请求重试
返回原 rolled_back 操作，未新增 launch 或控制文件写入；核心身份、完整控制
元数据、来源、状态和已列 42 FD 保持，采样时间和 proc-stat 字节的自然变化
不被误称为整个观测对象逐字节相同。显式 stop 提交 stopped revision2。

失败目标的身份来自原 durable claim 和 bootstrap intent，原记录没有
Target.Instance，不补造运行实例。三个核心仅证实 /proc absent，管理器拥有的
wait 状态未导出；失败目标退出后的 FD 清单也明确 unavailable。原始归档
59 项为 0600、5 项为 0644，提取副本为 0600；不声明秘密扫描通过。来源恢复
仍为 recording_status unverified、writer_end_evidence null，不由此推出来源
fsync、SQL、断电持久性、无中断切换或完整 G05 通过。

v1 在实际运行前发现把语义摘要与含末尾 LF 的文件 SHA 混用，两个失败断言
均已复现并保留；v1 未登记或运行。v2 将 launch/assets/bundle 的语义摘要与
原文件字节、权限、SHA 分开核验，23 项 pure/fake 检查通过后才登记本场；
固定 helper/core/bootstrap/BPF/readers 未改，原冻结材料保持。

## active 发布失败：显式恢复同一核心已受限通过

F12 v2 实际记录（本地原件 `../../experiments/mihomo-events/source5-active-publication-validation.json`，不随Git上传）
已经通过完整校验、原始归档独立复核与主代理验收：一个隔离 VM、六个应用槽、
85 份导出、两个 epoch 共 38 帧。发布失败返回 503 POISONED，旧 active
仍是 revision1；新 core112/start789 已经运行。后续变更被零写拒绝，管理器
正常退出后，新核心由 guest PID1 接管，重新打开管理器的只读 status 不修改状态。

解除夹具故障后，显式 recover 提交 revision2，仍是同一 PID、启动实例、
generation、source epoch 和 42 个身份 FD，没有第三次启动或回滚。恢复后的
两条新连接继续走替换后的 node:b/node:a；六条连接均有原始 connector 身份、
实际线路、ready 与 33/33 字节终末记录。最后显式 stop 提交 revision3，
PID1 对原目标核心的精确 wait 为 0，已回收且 /proc 不存在；六个客户端、
两个管理器均正常退出并回收，夹具挂载已清理。

本场实际 checkpoint 为 0，可选 checkpoint 的正例仅由 pure/fake 检查证明。
初始核心的 manager-owned wait 未导出；两个来源仍标记 recording_status
unverified、writer_end_evidence null。原归档 78 项 0600、7 项 0644 与提取
副本 0600 分开记录，不声明秘密扫描、来源 fsync、SQL、断电恢复或完整 T016 通过。

### 首场失败与夹具修订保留

F12 v1 原运行（本地原件 `../../experiments/mihomo-events/source5-active-publication-attempt-v1-validation.json`，不随Git上传）
在独立文件自 bind 校准中实际观察到 EBUSY，随后 active 发布返回
503 POISONED，操作保持 publishing。旧 active revision1 与已经运行的新
core112/start740 分开记录；前四个应用各完成 33/33 字节，新核心上的两条
连接发生在发布失败后。原归档 60 份导出与提取字节一致。

进入恢复准备时，夹具要求新来源恰好有 12 帧，但实际可见数更大，断言失败。
原夹具只在成功 stop 后复制新来源，因此只导出旧 epoch 的 13 帧；新增事件
的数量和种类无法恢复，不能追认成 checkpoint。恢复副本准备因缺第二对来源
失败，canonical 未执行；重开、显式恢复、后两应用和最终 stop 均未到达。
应急管理器退出与卸载已记录，新核心的精确退出仍 unverified。

固定核心每秒可为活跃流写入累计字节 checkpoint，这独立证明“每流固定六帧”
不是可靠前提。v2 保留六个必需事件，允许并完整校验其间的累计检查点，按
完成流数等待，分别记录必需帧和实际检查点；所有原始事件都参与 reader 和
恢复校验。失败清理前独立保存已有原始来源和缺口诊断，不覆盖已导出原件。
28 项 pure/fake 检查和镜像闭包验证通过后，独立登记并执行上述 v2；不重新评分 v1。

初次静态审查曾怀疑 ticker 能在 Flow 注册后、opened 写入前插入 checkpoint；
root 核对完整函数后排除了这一判断：begin 在注册前就持有 f.mu，直至 opened
写入及函数返回，Checkpoint 使用同一把锁。原审查留存并另附更正，不为这个
误判修改核心。合法周期检查点仍需完整校验，新校验继续拒绝 gap；不将局部
锁顺序判断扩大为所有路径无竞态。原实际失败与已接受历史结果均保留。

## 新配置和回滚均失败：明确降级已受限通过

F13 实际记录（本地原件 `../../experiments/mihomo-events/source5-failed-rollback-validation.json`，不随Git上传）
包含一个隔离 VM、两个基线应用、63 份原始导出及三个 epoch 共 15 帧。
独立见证先占用新入口 18999；另一见证通过原核心的 pidfd 观察其退出后，
占用旧入口 18998，并立即只读打开原操作文件。实际读到 applying/stop-old，
尚无 rollback 引用，因果前提成立；没有重试读取或人为暂停核心。

新目标和完整旧配置的回滚均启动失败，各保留一个完整零流量 epoch、原始
claim 与 bootstrap 证据。控制请求返回 503 UNKNOWN，操作保持 degraded，
旧 active revision1 保持原字节，但其核心已验证停止。同 ID 重试返回原降级
结果，新请求返回 409 PENDING；完整存储、来源和身份观察在这些探针及管理器
正常退出、重开、status 查询之间保持不变。没有用 Stop/Recover 清除失败状态。
两个应用、两个管理器和两个见证均退出并回收；三个核心的 /proc 均不存在，
它们的 manager-owned wait 状态仍不可用，不据此声称已取得核心退出码。

原主机 canonical 在命名空间整字典比较处 exit1：进程记录有四类字段，
readiness 另含两类时钟字段。原错误输出和冻结包不变。独立派生校验器逐项
核对四类进程与两类时钟命名空间、PID1 和时钟偏移；五项原件回归及 23 个
负例通过，独立 AST 复核确认其余校验语句不变。派生校验器对同一原运行
完整执行 strict/source readers 后 exit0，原归档独审一致；没有新增 VM 或应用槽。

本场 checkpoint 为 0，两个成功流各 33/33 字节，ready 为 11/31ms。
三个来源仍为 recording_status unverified、writer_end_evidence null。
本次只接受这一个双入口占用故障场景，F11 持久化中断矩阵及完整 G05 仍待验收。

## 持久化中断：历史首版暂停工具的离线准备

F11 准备记录（本地原件 `../../experiments/mihomo-events/source5-persistence-checkpoint-preparation.json`，不随Git上传）
固定了独立实验 helper `f509fa8a…978241`，与此前 `1c876099…07f95` 分开记账。
它只在专用 guest、明确启用私有 checkpoint 选项时，在指定原始回调处写入
有 PID/启动实例/boot 绑定的私有标记并暂停自身，供 PID1 观察后实际 SIGKILL。
原事务、锁和存储写入顺序未改；未启用时保留原行为。

73 个顶层检查、248 个含子检查节点在 race 下通过，0 失败、0 跳过；vet 和
离线构建通过。独立复核覆盖 2,248 项冻结文件、2,184 个构建/测试输入和
原 adapter 副本。fake backend 轨迹验证正常替换的 12 次逻辑写入、48 个
阶段回调及 14 个语义边界，区分孤立意图、缺 claim、已返回 Instance 和
磁盘 active 已发布而内存尚未更新等状态。

这些是离线准备结果，尚无实际暂停、SIGKILL 或恢复样本。16 场计划矩阵的
62 应用槽、30 epoch、最少 402 必需帧仍是预定分母，需另行冻结、登记和逐场
验证，并加上实际合法 checkpoint；不能把 fake 轨迹或标记文件当作实际崩溃证明。
部分写入、断电、来源 journal、SQLite 及回滚路径的所有写入点不由此保证。

## F11 第二版：十六个实际进程崩溃位置受限接受

首版实际第三场出现了提前回复：self SIGSTOP 调用返回后，IPC caller 已收到
503 POISONED，尚未完成预定的停止观察和 SIGKILL。该失败及首版两场接受、
十三场未执行的记录保留在首版结果（本地原件 `../../experiments/mihomo-events/source5-persistence-validation.json`，不随Git上传），
没有重评为通过。新的实验 helper `dc706c03…856a8` 在 signal 请求成功后保持
park，禁止事务或响应抢先前进；原事务和存储顺序不变。独立准备、冻结与
预审后，新矩阵重新登记，旧样本未计入其分母。

第二版结果（本地原件 `../../experiments/mihomo-events/source5-persistence-v2-validation.json`，不随Git上传）
现包含完整16场逐场原件独审和root接受：62应用槽、30来源世代、404帧
（402必需帧加2个实际checkpoint）、1252份原始导出。最终汇总核对2129个
不同文件引用的长度和摘要，全部保持一致；未再运行虚拟机或重新采样。
汇总接受记录（本地原件 `../../private/t016-multi-entry-preparation/root-persistence-matrix-v2-runtime-acceptance.json`，不随Git上传）
保存各场接受、原件和范围限制。

| 中断位置 | 已观察的恢复行为 |
| --- | --- |
| 正常替换的12次完整记录写入后 | 按原持久记录区分缺操作、旧核心停止、目标已运行、publishing及committed；有运行证据时接管同一目标，无额外猜测启动 |
| 核心已启动、操作尚未保存Instance | 原operation仍无Instance；独立原claim和PID/启动实例绑定实际目标，恢复后才保存其身份 |
| active临时文件尚为空 | 旧active仍有效，空临时文件在只读重开和显式恢复后保留原元数据 |
| active临时文件已完整同步 | 旧active与完整临时文件并存；恢复使用原目标，保留临时文件，其记录的完整摘要与发布后active一致 |
| active已重命名、目录同步前 | 重开读取磁盘revision2，以expected revision2恢复同一目标；不使用死亡管理器内存中的旧revision |

第7场有引用的启动意图但无claim，按预登记期望返回UNKNOWN/degraded，保留
旧active与实际已停止的观察，不猜测启动、直连或用普通Stop清除状态。
这是预期的安全降级分支，不是成功转发。第12场终态重试返回原结果，前后
完整inventory相同，并另取Status证明实例未变；冻结源码使用同一原请求对象，
但事件没有单独保存重试wire body，不能宣称独立捕获了逐字节相同的请求。

第二版第3场曾因主机校验器把数组顺序与按nonce排序的对象顺序混用而失败。
原失败保留；独立修订只按注册身份匹配完整退出记录，对同一原件完成后续
校验，没有重跑该VM。含凭据的临时文件原始payload不导出，其长度、完整摘要、
元数据及脱敏语义投影分别保存。来源仍为`recording_status=unverified`、
`writer_end_evidence=null`；没有直接观察到的manager-owned wait保持不可用。
这些结果只证明固定Linux隔离环境中的16个进程崩溃位置，不证明掉电、任意
部分写入、SQLite或所有来源持久化行为，也不代表完整G05/T016完成。

## 已完成离线实现：来源、固定节点与必需选择的编译

现有配置校验能拒绝空组和无效引用，但不足以证明订阅刷新失败时保留旧态，
也没有验证每个组的实际选择。T016 先实现无 I/O 的受限候选编译器：显式来源
ID、节点 ID 与原快照摘要绑定；活动订阅切换不能重绑定跨来源 pin；所有引用，
包括被前序规则遮蔽的引用，均须有效。显示名不参与身份判断。

受限纯编译器已实施，稳定代码复核发现的非法 UTF-8 条件被 JSON 编码替换问题
已先复现再修正。编译器定向 race 检查 10 主测试、72 子测试通过；后续新增
只读解释后同包检查为 15 主测试、115 子测试通过，无失败或跳过，vet 通过。
这些结果属于离线验证。

`CompileControlTemplate` 已完成纯编译适配，拒绝调用者 generation，仅将编译器
拥有的 route-map generation 留空供原控制后端填入，继续执行合计 8192 字节预算。
最终同包 race 18 主测试、含子用例 142 项通过，vet 通过；不表示运行时切换已验收。

随后补充[独立的 alias-only 与跨来源 pin 回归](../../experiments/mihomo-events/routingcandidate/provider_switch_test.go)：
仅改显示名时配置和稳定身份不变，route-map 显示信息与原快照溯源摘要更新；
active 切到 B 后固定在 A 的节点仍保持绑定。删除非活动 pinned provider、
用同名节点替换原 ID、删除被前序规则遮蔽的引用均返回零候选且不修改输入。
原七份编译器/测试源码保持原冻结摘要，仅新增这份测试；同包 race 现为
20 主测试、147 含子项，0 失败/0 跳过，vet 通过，稳定差异经独立复核。
这仍不证明下载、持久更新或实际转发。

此手选原型把已验证的明确选择编译成单成员组，缺选择、缺节点或地区不符就
拒绝整个候选，不把核心自动选择首成员当作手选恢复。自动组和多成员运行时
改选不由此原型证明。来源信息保留为编译溯源；inline 核心事件中的 provider
字段不能用这些意图补填。真实下载失败、旧态保留、实际线路和回滚另做运行
验证；纯编译检查不计为 F08/F09/F32 通过。

`Explain` 针对明确提供的事实，按同一顺序解释三值 AND：确定 false 覆盖
unknown，当前规则 unknown 会阻止继续选路，较早的确定匹配使后续规则标为
未求值。它不读取核心、进程、DNS 或计数，不表示真实进程身份或实际线路；
当前只解释该次查询的遮蔽，不声称完成全部静态遮蔽分析。

## 订阅拒绝首场：保留夹具失败，补齐运行文件渲染契约

refresh夹具离线21个测试方法、44个子例及镜像检查通过，冻结预审后执行了
一次隔离VM。首场原件记录（本地原件 `../../experiments/mihomo-events/source5-provider-refresh-attempt-v1-validation.json`，不随Git上传）
显示guest在core-start核对运行路由文件时失败，应用和provider GET尚未开始。
4个应用槽、1个epoch、7个拒绝探针的注册分母不变；14份原归档与提取内容
一致，零字节source是失败时快照，不是一个完成的零流量epoch。核心精确退出
未验证，QEMU退出回收成功不能代替它；copy因source正长度要求失败，未运行
canonical reader。

固定helper会将模板解为带`omitempty`的Go结构，再补generation并marshal为
运行文件。夹具原来只给模板补generation，错误地期望显式null和空字段仍然
存在。实际guest运行文件未导出，因此具体字段差异不能从该场恢复。后续用
真实固定Go渲染路径生成完整预期字节，独立补测试和新夹具，再重新登记验证；
不改旧原件、不放宽字段比较，也不把离线复现当作首场的实际文件。

该纯渲染小包（本地原件 `../../private/t016-multi-entry-preparation/root-provider-runtime-route-acceptance.json`，不随Git上传）
已独审并受限接受：四份实际固定Go渲染产物，普通及race各53个PASS节点，
Python五个测试方法通过。root另核验2336个有效构建输入及原候选95个冻结文件。
Python子测试上下文静态展开为55，原冻结说明误写56已独立更正；不将没有保存的
逐子测试日志当作执行记录。新夹具只对Go产物中的唯一generation字段作严格
等长替换，再比对实际运行文件全部字节，仍待新的完整启动入口与实际场次验收。

## 订阅拒绝第二版：七种拒绝与旧态保留已受限接受

独立新夹具经24方法/56实际子测试、镜像闭包和冻结预审后，执行一次新登记的
隔离Linux场次。运行、提取、保护副本和canonical均成功；独立原件审查后，
root接受记录（本地原件 `../../private/t016-multi-entry-preparation/root-provider-refresh-v2-runtime-acceptance.json`，不随Git上传）
与公开摘要（本地原件 `../../experiments/mihomo-events/source5-provider-refresh-v2-validation.json`，不随Git上传）
绑定原始75份导出、4应用槽、1epoch和25帧完整事件。第一版失败原件继续保留。

实际导出的运行route为635字节，与真实Go渲染结果在替换唯一generation后
逐字节相同；该文件摘要绑定25处快照、停止状态及只读API库存。编译器原模板、
bundle语义摘要与运行文件分别保存，不通过归一化删除意外字段。

| 拒绝输入 | 本场真实结果 | 原候选状态 |
| --- | --- | --- |
| 下载端返回503 | 真实GET完整20字节响应，获取阶段拒绝 | 保留 |
| 空节点列表 | 真实GET200、完整103字节，编译拒绝 | 保留 |
| 所选节点移除 | 真实GET200、完整213字节，编译拒绝 | 保留 |
| 缺少US明确选择 | 静态编译拒绝，不调用Execute | 保留 |
| 非活动但被引用的来源缺失 | 静态编译拒绝，不调用Execute | 保留 |
| 非活动来源内容无效 | 静态编译拒绝，不调用Execute | 保留 |
| 前序规则遮蔽的来源引用缺失 | 静态编译拒绝，不调用Execute | 保留 |

七组前后完整库存、accepted snapshots、claim、active/operation、42个列出的
FD和source前缀均不变。原核心PID91/start518在首管理器正常退出、PID1接管和
第二管理器只读重开后保持同一身份；两条后续连接继续原group/node路线。
四条连接各有33/33字节终末，来源报告的ready延迟为HTTP11ms、SOCKS5 31ms；
该场未新增独立计时校准。显式Stop提交revision2，PID1另有精确核心wait0/
reap/absence证据，不从QEMU退出推断核心退出。

原tar有70项0600、5项0644；提取后75项均为0600，两种权限记录分列。完整source
通过严格读取和同字节恢复分析，但recording_status仍为unverified、writer_end
为空，不证明新fsync/SQLite保证。本场只覆盖固定规范化来源及有限失败输入；
成功订阅切换、自动选择、更新调度和生产应用行为仍待各自验证。

## 订阅切换首场：第三代配置已提交，取证聚合超限

原始失败摘要（本地原件 `../../experiments/mihomo-events/source5-provider-switch-attempt-v1-validation.json`，不随Git上传）
保留固定8应用槽、3来源世代、51必需帧的分母。实际只完成前4客户端和前2个
完整世代，共26帧；第三代配置提交revision3后，夹具保存三份完整registration
的聚合JSON需要307753字节，超过262144字节上限。第三代epoch文件已经保存，
当前registration和route投影尚未更新，后4槽、跨来源pin拒绝和Stop4均未执行。

原始59个归档成员经独审逐项匹配。QEMU3921519正常退出并回收，耗时18.486秒，
该状态不能替代guest成功或核心精确退出。第三代来源为零字节失败快照，保护
副本步骤按原正长度约束拒绝，canonical未运行；前两代成功不改变整场失败。

离线fake仅有3个pin或缺少pins，实际每份registration有763个pin，原尺寸测试
遗漏了这一规模。后续独立v2保留完整分世代记录，以版本化索引绑定整文件的
大小和摘要；epoch后续更新必须同步刷新索引。上限、字段、槽数及原失败原件
均保留。新夹具须覆盖真实metadata规模并重新冻结、预审和登记，尚无实际结果。

## 订阅切换第二场：真实探针基线与Python检查器不一致

第二场失败摘要（本地原件 `../../experiments/mihomo-events/source5-provider-switch-attempt-v2-validation.json`，不随Git上传）
保留新v2的8槽/3epoch/51必需帧分母。文件索引通过前期读取，实际完成三次Apply
及六条连接，但在跨来源pin缺失探针的元数据比较处停止。六客户端各33/33并
退出回收；来源为13+13+12帧，第三代没有epoch_closed，后两客户端及Stop4缺失。
原76归档成员全部逐字节匹配；保护副本成功，canonical因未产出的before-stop
文件失败，未进入完整native reader链。QEMU3961255正常退出回收19.693秒，
核心131精确退出仍未验证，不能把这些观察重新解释为整场通过。

冻结Go探针对该输入明确选用switch-active-b作为accepted基线，实际报告为
expected_rejection、无candidate，accepted/input前后各自不变。Python检查器
却复用了refresh-baseline；fake也按同一错误基线生成报告，离线测试未发现
跨实现契约差异。后续v3将直接导出冻结Go的完整accepted Spec，并用本次原始
探针stdout和完整记录回归，不修改探针/编译器/核心，不重评原失败或补造缺失
动作。只有独立新登记的完整八槽场次才能接受这一切换场景。

## 订阅切换第三场：完整八槽有限验收通过

第三场结果（本地原件 `../../experiments/mihomo-events/source5-provider-switch-v3-validation.json`，不随Git上传）
已经过冻结预审、一次隔离VM执行、原件独审及root接受。8个客户端均完成33/33
字节交换并退出回收；3个独立来源世代分别为13、13、25帧，共51个必需帧，
额外checkpoint为0，每代都有完整epoch_closed。91个原归档成员与导出清单、
提取字节逐项匹配；归档头为82项0600/9项0644，提取均为0600。

仅改显示别名时，原节点身份及A线路保持；切换active provider至B后，group:US
跟随B，固定A节点仍走A。真实Go探针对缺少固定节点来源的配置返回编译拒绝，
没有candidate；accepted/input前后各自不变，控制存储、claim、42FD及来源前缀
也不变。拒绝之后的两个客户端继续分别使用B与固定A。三次Apply提交revision
1/2/3，最后Stop提交revision4；分世代完整文件索引保留全部原始registration。

第三版以真实Go导出的完整accepted Spec和第二场原始1972字节stdout修正
跨实现契约。独审发现旧checker测试依赖未显式校验后，root另做校验输入前后
哈希的两项纯回归并获独审；该补证不追溯宣称旧测试闭包完整。前两场失败及
各自原件不改，第三场成功不补造它们缺失的步骤。

QEMU4086261退出0、回收且消失，耗时21.481秒；manager82也退出0并回收。
三个核心91/112/133均观察到消失，但由manager持有的wait状态未导出，仍明确
不可用。原run状态保留vm_exited_unverified；接受结论在独立root记录中。
各来源recording_status仍为unverified，writer_end_evidence为空；本场不新增
持久writer结束、SQLite、长连接无损迁移、调度/自动选优或跨平台保证。

## 九行有序规则：有限验收通过

九行结果（本地原件 `../../experiments/mihomo-events/source5-ordered-routing-validation.json`，不随Git上传）
已完成冻结预审、九次独立登记的隔离VM执行、原件独审和root接受。18条应用
连接分别通过HTTP和SOCKS入口：12条按预期转发，各方向终末33字节；6条在
接纳后按规则拒绝，保留实际opened、路由失败和终末0字节事件。后者有原始
进程与连接身份，不是无法采集时补出的零流量记录。

| 原规则顺序或条件 | 两条连接的实际结果 |
| --- | --- |
| 可用进程名优先，后面的路径字段未知 | group:US → A |
| 未知进程路径优先，后面存在可用名称 | 拒绝，不跳过未知规则 |
| AND的已知条件为false，另一个字段未知 | 继续匹配MATCH，group:JP → B |
| AND的已知条件为true，另一个字段未知 | 拒绝 |
| 进程名称字节无法解为UTF-8 | 拒绝，原始路径字节与PID/start仍保留 |
| 应用规则排在进程、域名、IP规则前 | group:US → A |
| 进程规则排在应用、域名、IP规则前 | 固定B节点 |
| 域名规则排在应用、进程、IP规则前 | group:JP → B |
| IP规则排在应用、进程、域名规则前 | 固定A节点 |

九个来源世代均完整结束，共106帧：105个必需事件和domain-first的一条实际
checkpoint。315个原归档成员均与导出清单、提取副本逐字节匹配；每场35成员
与每核心42个typed FD分别核验。实际733字节运行route文件均匹配固定Go产物
唯一generation替换后的完整字节。原客户端PID/start/socket cookie、raw296
可执行文件证明和独立节点/目标接收记录分别关联到本场、本入口和本来源世代。

九场manager与18个客户端均观察到退出0、回收和消失。核心均消失，但由manager
持有的核心精确wait未导出；不从manager或QEMU的退出码推断核心退出码。原run
状态保留vm_exited_unverified，接受结论另存。recording_status仍为unverified，
writer_end_evidence为空，不增加持久写入或掉电保证，也不增加正式100流分母。
不可达节点、缺证明持续拒绝和最终只读验收入口仍需完成。

## 不可达首场：完整原件保留，阶段字段检查失败

首场记录（本地原件 `../../experiments/mihomo-events/source5-unreachable-attempt-v1-validation.json`，不随Git上传）
保留一次VM、四应用槽、一个来源世代的原分母。guest完成两条B线路成功连接、
两条A线路失败连接和Stop2；62个原归档成员均与清单、提取字节匹配，十份端点
保留记录也完整导出。四客户端退出0并回收，核心消失而精确wait仍不可用。
QEMU494215退出0并回收，耗时24.781秒；这些不是canonical通过的替代证据。

原canonical在固定reader调用前失败：初始connected记录没有终末阶段才新增的
failure_exchange和exchange_interval，检查器却要求两个阶段的字段集合全等。
完整fake从终末行复制初始行，错误保留了这两个晚出现字段；单独真实main输出
测试没有把完整JSONL接到canonical，因而漏测。原检查器、失败receipt和数据不改。
后续仅在独立host-v2修订阶段字段契约，用真实main的完整两行输出做集成回归，
再通过新保护副本读取同一份原来源；不为这一宿主检查器修订重跑VM。

原件纯解析观察到45帧：39必需帧和6条实际checkpoint。两个失败流各10次建立
失败，尝试8/9/10的checkpoint均为0/0，终末0/0；两个成功流终末33/33，epoch
完整关闭。失败客户端本地实际发送33、收到0并观察EOF，与来源0/0分开保留。
这不提供ECONNREFUSED或TCP RST的证据；未执行的strict/recovery及后续谓词
仍未验证，不能据此宣称不可达场景已完整通过。

## 不可达场景：同一原件的独立host-v2校验已受限接受

新检查器只修正初始与终末记录的阶段字段契约：connected必须恰有53个字段，
成功终末仍为53个，失败终末只新增两个指定字段；全部身份字段保持类型和值
相同，初始计数、空哈希、握手和socket状态逐项核验。真实客户端main的完整
两行输出已接入归档、提取、保护副本和完整canonical模拟，6个测试方法、413个
实际子测试通过。旧检查器在相同模拟输入上仍按原断言失败。

独立预审后，root对同一场原始来源创建新保护副本并运行新consumer。copy与
canonical均退出0；strict解码45帧，恢复器核对完整epoch及sequence45。独立复核
确认123个原引用不变、两份副本与来源逐字节相同，root已保存接受记录（本地原件 `../../private/t016-multi-entry-preparation/root-unreachable-host-v2-runtime-acceptance.json`，不随Git上传）
和公开摘要（本地原件 `../../experiments/mihomo-events/source5-unreachable-host-v2-validation.json`，不随Git上传）。
本次修订新增VM数为0；原canonical退出1和旧失败摘要保留。

实际结果为4条流、1个epoch、39必需事件加6条checkpoint。两条B线路各有33/33
来源终末；两条A线路各10次建立失败、来源终末0/0，客户端本地33/0与EOF单独
记录。十次被动观察确认指定端点被占用但未监听；errno和TCP RST原因仍为null。
来源recording_status为unverified、writer_end为空，核心精确wait不可用；不据此
增加持久保证、生产可用性或平台支持声明。

## 最终有限验收对照

| 范围 | 状态和所需证据 |
| --- | --- |
| F29/F30 | 双入口实际转发、原 connector 关联、unknown 拒绝及管理器离线策略已接受；五尝试补齐缺证明、精确恢复和双入口持续拒绝 |
| F31/F32 | 九行有序三值规则、稳定 group/provider/pin 及不可达目标无后备已接受；生产只读解释由 T049 实现 |
| F08/F09 | refresh 第二版 7 种拒绝及旧态零写、switch 第三版 8 槽来源切换与跨来源 pin 拒绝已接受；生产调度/自动选优另验 |
| F10/F33 | 原生分阶段开放负例及四槽/三 epoch 完整旧配置回滚已接受 |
| F11/F12/F13 | F11 第二版 16 场/62 槽/30 epoch、F12 发布失败及同核心恢复、F13 双重绑定失败后降级已接受；旧失败与派生修订分别保留 |
| C01 | 新登记四槽/两 epoch 并发旧候选零写拒绝已接受 |
| 最终入口 | `routing_test.go` 与固定 Python gate v3 实际消费正式清单，完整原件核验及 race 通过；不是合成数据验收 |

T016 的有限完成不表示生产实现或整个项目完成；当前任务进度以任务清单为准。


## 五尝试缺证明场景：冻结前修订完成，等待真实运行

当前准备包已冻结 1,775 个文件，第二版镜像的实际闭包验证为 816 个 cpio 项、
110 个 ELF、32 个隔离只读 imports。固定五次尝试包含两条正常转发和三次接纳前
拒绝；后者没有来源 flow/terminal/bytes，保持 null。正常导出固定为 86 项，
64 线程与 256 checkpoint 的完整合成边界仍低于原 JSON、成员数和归档上限。

冻结前修复了两端 waiting/release 原件的部分写入可见竞态，并补齐失败来源
证据保存再次失败时的后续清理。实际 guest 入口注入错误的回归确认首个异常
会封存；局部变量对封存函数的遮蔽也已消除。原子发布的 link 是可见点，
目录 fsync 随后失败仍可能已被对端观察，因此保持完整原件并使场景失败、
清理所持有的资源，不推断对端尚未继续。

这些结果仍为离线准备：整包须经稳定独立预审与 root 接受，再执行一次固定
VM 场景、完整原件消费与独审。旧镜像、旧源码和旧失败保留；实际删除/精确
恢复证明、两轮完整组停与双入口粘滞拒绝尚未由本包验证。T016/P0/P1 不变。


## 五尝试首场：原件齐全，宿主归档接口失败保留

首场记录（本地原件 `../../experiments/mihomo-events/source5-admission-attempt-v1-validation.json`，不随Git上传）
固定一次 VM、五应用尝试、两条来源流与一个 epoch。register、host、extract、
copy 均退出 0；canonical 在调用固定 reader 前退出 1，因为它要求实际 extractor
未生成的独立 evidence.tar.gz。压缩归档原本完整嵌在 console envelope 内；
synthetic 自行多写了 tar，漏掉这项生产衔接差异。

独立原件审查已确认 86 个归档成员与清单、提取字节逐项相同。原归档模式为
83 项 0600、3 项 0644；提取后 86 项均为 0600，两种模式分别记录。QEMU 正常
退出并回收；管理器 82 正常退出、原核心 90 由 guest PID1 在两次完整组停后
直接终止，实际 wait 为退出 0 并回收。五客户端均正常退出回收，三个 held
尝试各有三份事件与两次 ACK，来源身份/终末/字节为空。

原 source 纯解析为 17,668 字节、13 帧，两条正常流均为 33/33，最后为 complete
关闭；没有实际 checkpoint。这些原始观察不替代尚未通过的严格 reader 和
完整 canonical。root 只授权在独立 host-v2 读取同一 console 归档、补实际
extractor→consumer 回归，随后新冻结/预审、保护副本与同原件消费；不补 tar、
不重跑 VM，也不改变旧失败和原分母。T016/P0/P1 尚未放行。


## 同原件宿主修订与最终接受

独立 admission-host-v2 直接核验有界 console envelope 中的压缩归档，保持
原成员、长度、摘要与提取字节检查；没有补造 tar。12 方法/63 子测试通过，
冻结后经独审/root 接受才消费原 VM 的新保护副本。copy、strict/source 读取及
canonical 均通过；随后完成独立原件复核，248 项前置引用未变。原首次 canonical
退出 1 及失败摘要仍保留，没有新增 VM 或更改五次尝试的分母。

五尝试接受记录（本地原件 `../../experiments/mihomo-events/source5-admission-validation.json`，不随Git上传）
确认两次全线程组停、选定证明实际删除及原字节 NOEXIST 恢复。两条基线连接
分别走 US/node A、JP/node B，上下行均为 33/33；原始 ready 耗时为 12/32 ms，
属于 upstream HTTP CONNECT 建立耗时，不是业务 RTT。缺证明尝试被拒绝后，
同一 socket 持有期间恢复原证明，后续两入口的新有效证明仍被共享降级状态拒绝。
这三个接纳前拒绝没有来源 flow/终末/字节，明确为 null，不能补成零。

核心由本场 guest PID1 精确终止、wait 退出 0 并回收；这项夹具清理不等于生产
Stop/Recover，原 durable active 仍为旧登记。来源末帧 complete、未完成流为 0；
`recording_status=unverified`、`writer_end_evidence=null` 原样保留，不提升为
来源 fsync 或 SQL 保证。

最终装配对 5,138 份原件引用核验后生成草稿；root 接受并固定正式清单，Go race
再从该清单消费全部 16 组原件。39 VM 是被接受场景的合计，历史失败另行保留，
并非所有尝试都成功。T016 当前边界为 Linux amd64、初始 namespace、IPv4 回环、
专用 cgroup 和受控 guest；四 owner 仅有合成测试。生产部署、任意平台、同端口
无缝替换、UDP/QUIC、来源 GC、SQL 及性能预算不由此项验收代替。
