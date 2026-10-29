# Source5恢复验收准备

2026-10-26准备阶段记录。T127身份的受限验收后，本页记录当前0017来源spool和接收journal的准备证据；随后另登记的[T128正式矩阵现已受限通过](source5-formal-recovery.md)，不改变本页准备样本的阶段。旧T133的001–005结果只作参考，不算0017正式结果。实施顺序和三个传输case见[冻结契约](../../specs/001-hoopa-proxy-manager/contracts/source5-recovery-transport-v1.md)。

现有collector helper采用已有严格receiver/journal，固定source5 manifest后才接流；实验日志区分source_seq、receipt_seq与每次delivery_index。503发生在Accept之前，丢ACK发生在持久Accept之后，hold等待不持receiver锁。文件/httptest准备检查race9顶层、19含子测试通过，vet和CGO0构建通过；随后独立完成了以下三个真实VM传输准备场景。

当前传输准备摘要（本地原件 `../../experiments/mihomo-events/source5-transport-preparation-validation.json`，不随Git上传）记录新登记6/6、3VM、39来源帧和99导出文件，VM/规范提取/checker全部退出0且经独立逐字节复核。每个VM有HTTP和SOCKS两条普通原始连接，分别走node:a和node:b，双向各33字节并正常EOF。

| 实际故障 | 观察结果 |
| --- | --- |
| 接收端离线，后续首次closed持久接收后丢ACK | 离线时实际ECONNREFUSED但两流仍完成；同一closed原文重投沿用原receipt |
| 接收端离线，后续首次closed在Accept前返回503 | 首投没有receipt，后续原文重投完整接收 |
| 首opened持久接收后挂起ACK | 两流在释放前完成；2秒超时后同opened原文重投，receipt保持不变；释放后补收完整截止前缀 |

三个来源各12条截止事件完整送达，接收journal逐帧保留原文；42次delivery对应39条唯一receipt，其中41次delivery日志记录accepted，重复投递没有新增receipt。本次三个epoch_closed也都实际收到，但退出marker不属于强制交付截止，不能因此承诺核心退出时所有marker必然送达。旧首轮准备4/6仍保留：held运行的客户端release字典覆盖ACK文件Path，导致释放时AttributeError；独立fixed lab仅改局部变量名和两处使用，新增实际closure作用域回归先复现旧失败，再完成30/30准备检查和新6/6实跑。旧失败未重评分。

为准备实际文件系统故障，独立隔离VM已完成一个2MiB tmpfs校准，VM、规范提取和checker均退出0，5项导出经独立复核。原始观察摘要（本地原件 `../../experiments/mihomo-events/source5-enospc-feasibility-validation.json`，不随Git上传）保存实际挂载、syscall trace、退出与完整性引用。PID73在FD3先保存4096字节，再将同文件系统填满；该FD的8192字节写入实际返回ENOSPC。仅删除自有filler后，同FD写入8192字节和fsync成功；最终12288字节与预登记内容一致，原4096字节保持。挂载卸载成功，自有strace和probe均退出。

该VM没有启动代理核心或加载BPF，也没有宿主共享目录或网卡；不能把校准当成核心恢复、已有核心的ptrace attach、断电持久性或未插桩性能验证。固定init在guest启动时挂载proc/sysfs/devtmpfs；实验Python先验证PID1/guest标记，再挂载自己的2MiB tmpfs。直接在宿主调用guest/probe均在写入前被guard拒绝。

核心和collector的新登记SIGKILL重开准备（本地原件 `../../experiments/mihomo-events/source5-restart-preparation-validation.json`，不随Git上传）现已2/2通过，13帧/44导出经独立复核。旧HTTP流实际12/12检查点在collector1确认后，依次SIGKILL/wait collector1和core1；原客户端随后观察EOF。新核心PID、启动时刻、capture generation和BPF对象均不同，原配置、旧来源文件和journal前缀保持。collector2带固定两份manifest重开同journal，六个旧事件重投保留receipt1–6；新SOCKS流33/33的六条截止事件对应receipt7–12。新epoch marker实际收到并单列为receipt13，不提升退出交付保证。

旧来源的实际只读报告仍是pending、检查点12/12、final/tail=null；新来源closed33/33、tail0。绑定本次VM注册/launch/全部导出哈希后，固定源码overlay在私有副本上实际race1通过、0失败、0跳过；三次独占全扫描重开得到相同`epoch_ended_without_final`缺口标识和fact hash，仍保留12/12且final/tail=null，新closed保持。原五个输入文件和1199源码条目未改。这个内部恢复报告不是VM公开接口返回，不能改写原只读pending结果。

原首次重开登记0/2继续保留：guest流程完成后，host checker先因str/Path接口错误失败；随后无写开发诊断发现每实例argv与实际recovery字段名未接入。新lab只修正这三个checker位置并补六项完整输入/删除字段/错slot等回归，guest与initramfs逐字不变；原诊断不算重评分，新2/2来自重新登记的真实VM。

已有核心ptrace的新登记校准（本地原件 `../../experiments/mihomo-events/source5-core-attach-preparation-validation.json`，不随Git上传）也已2/2通过：core PID78/start418的唯一writer FD7实际完成12次完整write和12次fsync，两流各33/33；tracer PID86以TERM退出并回收、无KILL，脱离后的8个当前task均TracerPid0，原核心仍running，之后正常停核。13帧/36导出及三份source原件摘要核对一致，自有tmpfs卸载返回0。原首次0/2保留，其客户端旧guest标记在建连前拒绝；新lab只修正精确标记并增加固定kernel/machine检查，原11加新9项guard回归通过。该校准不含filler或ENOSPC，不能算满盘恢复通过。

定向故障边界检查（本地原件 `../../experiments/mihomo-events/source5-fault-boundaries-preparation-validation.json`，不随Git上传）已通过独立复核：source5合成身份/计数与注入I/O的race共5项，配额耗尽仍保留精确整数终末`9007199254740998/17`，只标记检查点窗口缺口；不确定写不推进确认位置，sender只发原确认前缀，partial尾原样保留并拒绝重开。固定源码的HTTP预读、initial write实际n及未知lazy外层证明另有race15项通过。固定源码1199个路径/类型/权限/内容条目未改。这些是合成、注入和内存pipe检查，不充当真实满盘、周期worker或全局准入配额的VM证明。

全局逻辑预留的三槽准备（本地原件 `../../experiments/mihomo-events/source5-quota-preparation-validation.json`，不随Git上传）已实际3/3通过，31导出、14来源帧及14唯一receipt经独立复核。来源manifest明确限额200000字节：原HTTP连接12/12保持时，第二SOCKS连接被拒绝继续转发，没有第二个source flow或node连接；原连接随后仍保存精确12/12终末，释放预留后第三SOCKS连接正常完成33/33。13条截止事件逐字接收，随后完整epoch marker也实际收到；核心和collector均正常退出并回收。

第二连接实际完成了SOCKS成功回复，之后才遇到EOF，不能描述为客户端收到了SOCKS失败响应。其原TCP/身份准入与来源计量准入分开：client记录应用调用零字节，来源final/flow/node均为null，没有伪造closed0/0。这是固定200000配置下的暂时预留不足，与真实文件系统ENOSPC、单流检查点窗口耗尽分别取证。

真正source满盘的新登记准备（本地原件 `../../experiments/mihomo-events/source5-core-enospc-preparation-validation.json`，不随Git上传）已2/2通过并完成独立复核。实际core PID77/start416唯一writer FD7先write917返回57，再write860返回ENOSPC；2MiB tmpfs free0。原流client/node/target实际17/17，可信来源仍只到六帧、12/12检查点，final/tail为null；新SOCKS在协议成功并尝试发送33字节后reset，无node:b或新source flow，不能把其客户端write当转发成功。完整来源8192字节含57字节partial尾，确认前缀8135字节与六条journal原文一致。原件保存后才移除filler，重开退出1且不改变原件；固定源码另对该实际输入私有副本race1通过0跳过，三次独占重开均ErrSource、无新epoch、不修复，1199源码条目及输入哈希不变。该证据支持安全拒绝实际partial尾，未观察真实ENOSPC后的complete尾成功恢复，也未实现自动修复。

旧失败继续保留：首次未登记包漏Path，后续fixed包27项准备通过但实际镜像漏evidence_checks/oracle_checks导致guest导入退出，原登记0/2；新bundle-fixed只补两份未改模块，实际cpio隔离导入回归通过后另登记上述新VM，未重评分旧结果。私有副本overlay的首次后检查因Path排序与相对字符串排序不同失败，按相对路径核对1199条无差异后完成；未重跑实际测试。

HTTP节点前缀与collector同ID冲突的新准备VM（本地原件 `../../experiments/mihomo-events/source5-buffer-preparation-validation.json`，不随Git上传）已2/2通过，30导出及控制请求已完成独立运行证据复核。两流均source/segment/client上传33、下载41，真实target双向33，额外8字节来自节点；应用发送调用形状不当作TCP包边界，本次两个client都未观察到inbound握手读过头。collector的两次独立parent控制请求分别409/no receipt和204/原receipt1，三份23982字节journal快照逐字相同；13原来源帧对应13唯一receipt、15delivery（含一次冲突与一次原文重投），30导出。它们不增加原应用身份分母，也不把合成预读分支算成真实内核分包证明。

活动连接正常TERM准备（本地原件 `../../experiments/mihomo-events/source5-term-preparation-validation.json`，不随Git上传）已1/1通过并独立复核，28导出。原client PID94/start506持有12/12检查点，先确认六条原文与receipt，再TERM/wait核心、授权客户端仅recv；实际EOF且后续应用写0。来源八帧包含closed12/12与complete/unfinished0 marker，journal八条原文一致，本次closed与marker均实际送达。另一合法shutdown gap分支仍只属合成输入，退出时通用交付保证不由本例推出。

各准备场景为后续正式矩阵提供输入。六family正式夹具、新镜像及独立预审完成后，按[正式契约](../../specs/001-hoopa-proxy-manager/contracts/source5-recovery-formal-v1.md)先冻结全部注册，再另运行8VM/16槽，已完成受限验收；结果见[正式恢复报告](source5-formal-recovery.md)。本页准备样本的正式计数始终为0，未重新分类。T128/T013已完成，T129继续，140项完成29项；P0/P1仍未放行。
