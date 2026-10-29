# Source5 质量与线路验证准备

2026-10-26后续状态：六准备完成后已另建新登记并通过[正式6VM/21槽验收](source5-formal-quality.md)。本页及其机器记录仍是原准备范围、formal0，不回写为正式结果。

2026-10-26。T128/T013已[受限通过](source5-formal-recovery.md)，T129继续，尚未登记正式质量样本。继续使用固定0017、source5和connect_initiator_v1；旧T134的profile2结果保留，不直接升级为本轮正式证据。项目140项完成29项，P0/P1未放行。

HTTP响应、SOCKS完整回复、DIRECT慢DNS、独立核心完整替换、七类失败/超时及多跳/成功重试的准备实验已分别通过，正式T129样本仍为0；当前准备六家庭独立formal副本和新镜像。HTTP准备中的两个独立原应用连接分别经过HTTP node:a/node:b。上游先发缺最后CRLF的200头，保存同一flow已opened但无setup_finished的来源原文快照；至少等待120ms，再保存一次快照后授权最后CRLF。最终核对源flow/attempt/ref及实际节点、ready与finish分别计算的耗时、连接段字节守恒。核心的进程相对单调时间与guest观察时钟分别使用，不能直接相减。

显式响应屏障helper已通过9项AF_UNIX合成检查，并经独立静态复核；初次sandbox发送EPERM原日志保留，同一测试在已授权执行环境复验通过。实际夹具另通过28项适用原检查、8项质量检查、7入口help和镜像完整文件/隔离导入闭包；冻结及独立预审后，新登记的真实VM已取得HTTP准备2/2（本地原件 `../../experiments/mihomo-events/source5-http-ready-preparation-validation.json`，不随Git上传），39项导出、14帧及四份快照经独立复核。node:a/node:b的实际扣留分别为124,253,294ns和125,372,428ns，源attempt ready耗时为137ms和126ms；两flow/attempt/ref与实际节点一致，终末与连接段均33B上传/33B下载。node:b两份快照之间新增零流量checkpoint，仍没有该flow的setup事件。正式T129样本仍为0。

本次核对了core自身起点/ready/finish的四个耗时等式；独立guest扣留区间只是建立耗时下界。send调用形状不代表TCP分包，source活跃文件的可见完整前缀也不代表fsync；严格恢复报告的`recording_status=unverified`保持。成功HTTP握手也不能代表应用业务响应耗时或持续链路质量。

慢DNS的DIRECT真实准备2/2（本地原件 `../../experiments/mihomo-events/source5-direct-dns-preparation-validation.json`，不随Git上传）已通过独立原件复核：40导出、13来源帧与13个journal payload逐字一致。guest固定回环resolver且目标不在hosts；规则解析和DIRECT建连每流实际各发一次A/IN查询，TTL0，共四次查询/45字节原文回复。首答扣留133,856,703ns/132,458,735ns，源flow→attempt分别136,075,072ns/133,911,192ns；flow ready总耗时137ms/134ms，独立attempt ready为1ms/0ms，后者是毫秒向下取整。每流两份opened但无setup快照、实际Direct node:d/ref/chain、原connector92/start529与98/start570，以及双向33B终末均核对。第二答快速返回，额外查询观察窗为空；自有socket/线程及core/collector均回收。没有修改宿主DNS或网络。

该包先通过16项准备检查、7入口help及实际cpio闭包，再登记执行。初版冻结镜像缺少guest `/etc/hosts`，在登记前被拦下，runtime为0；旧包保留，修正版v2明确加入精确回环hosts，缺失反例也拒绝。准备formal0由root范围判断给出，host-check没有`formal_t129_samples`字段，未改写原件。

线路切换存在一个已确认的前置限制：0017受保护controller拒绝旧T134脚本的`PUT /proxies/group:main`，不能直接重用该在线selector场景。[受保护契约](../../specs/001-hoopa-proxy-manager/contracts/protected-core-v1.md)使用独立新进程/入口，[ADR008](../adr/008-remaining-evidence-execution.md)明确独立端口；本轮按这一范围完成完整替换准备4/4（本地原件 `../../experiments/mihomo-events/source5-replacement-preparation-validation.json`，不随Git上传）。旧入口18993的A流停在12B，原source checkpoint及receipt落到独立journal后才drain；新入口18983的B流及改名映射下的新A流先完成，旧A流随后续传至33B。旧core退出后，新core再完成第四条B流。全部flow/segment/client/node/target均为双向33B。

这次VM实际验证了两个独立capture generation和leaf同时工作：每core有10个map、6个program、5个link，两个owner的对象ID按类不交叉。旧core退出后新owner仍接纳原connector，旧frames/manifest及12B检查点原文未被改写；旧/新source分别9/19帧，与9/19份原journal payload、正常collector收据一致，50项导出经独立复核。四原connector为PID93/start467、115/start637、120/start686、124/start743。改名仅指新route-map的display_alias，来源保持稳定node/group ID；没有UI实现结论。本次不证明同端口无缝替换、同core在线selector修改、现有应用代理环境自动更新或跨平台兼容。

SOCKS5最后响应字节夹具完成7项AF_UNIX检查和独立静态复核后，新lab通过28项适用检查、9项质量检查、7入口help和实际镜像闭包。新登记SOCKS5准备2/2（本地原件 `../../experiments/mihomo-events/source5-socks-ready-preparation-validation.json`，不随Git上传）已通过独立原件复核：39导出、14来源帧与14个journal payload逐字一致；node:a/node:b的源建立耗时140ms/128ms，观察扣留124,771,441ns/126,302,999ns。两次无提前setup快照后才放行BND.PORT最后字节，实际成功写入分别为2字节method、9字节响应前缀、1字节末尾；两原connector PID93/start472及PID99/start521、实际链/ref和33B双向终末均核对。BND字段是脚本值，不是实际目标socket地址；source可见前缀不等于fsync，正式T129样本仍为0。

失败/超时准备7/7（本地原件 `../../experiments/mihomo-events/source5-upstream-failure-preparation-validation.json`，不随Git上传）已原件复核：HTTP403、SOCKS坏VER/REP/RSV及截断BND各产生10次失败尝试，HTTP/SOCKS停滞各产生一次超时，共7条原flow/52次attempt。两个超时的flow observed wait为5001ms/5007ms；52次实际helper accept均与同flow原cookie和source attempt关联，helper没有拨目标端点。七客户端均在入口协议成功后写入33B、接收0B并观察EOF；来源有明确的零checkpoint和closed终末，segment为空、unallocated为0。这是观察到的零转发量，不是把缺口填零。全部setup保留failure/timeout、ready_unknown原因和独立等待；ready、成功duration及actual ref为null，所选逻辑节点没有冒充实际线路。

该run共有99来源帧：98帧cutoff加完整退出marker，99份journal原文和99个collector accepted均对应，38项导出核对通过。末次deadline未被helper accept的允许分支本次没有触发；不宣称验证了该路径。初版验收器的“两流33B cutoff”及BOOT字符串比较在登记前被拦，旧冻结包和失败复现保留；新包增加完整check_delivery正反例及跨十进制位数时序检查后独立预审、重新登记运行，未修改旧结果。

固定候选的两项路径限制需要另行裁决，不能归因于测试环境不足：tunnel的上游dial context从`Background`派生，客户端关闭并不会取消它；普通dial/握手使用共享5秒deadline、最多10次重试。DIRECT的普通adapter支持TFO，但当前受保护profile精确字段白名单拒绝`tfo`，故不能用TFO配置充当本轮实际lazy/unknown样本。新增source5注入检查（本地原件 `../../experiments/mihomo-events/source5-quality-injected-validation.json`，不随Git上传）已独立复核，2顶层/含子测试11项race通过，0失败/跳过，固定1199项源码树不变。实际writer/严格decoder验证了错误分类、已证ready不被覆盖、once-only完成和重试段守恒；身份、Conn、error及部分写入n均为注入，不能升格为完整核心网络取消或lazy证据。原错误可达性分析已保留并由完整配置校验调用链修正。

固定HTTP adapter边界（本地原件 `../../experiments/mihomo-events/source5-http-adapter-boundary-validation.json`，不随Git上传）也已独立复核：现有实际adapter经注入net.Pipe dialer执行success/reject/truncated/cancelled，1顶层/含子测试5项race通过，0失败/跳过，固定源码未变。显式取消其context可返回context.Canceled且无Conn；这仍不是受保护完整核心中“客户端断开取消上游”的样本。

多跳/成功重试准备4/4（本地原件 `../../experiments/mihomo-events/source5-nested-retry-preparation-validation.json`，不随Git上传）已独立复核并受限接受：35导出、28来源帧及28个原journal payload一致，实际sourceinspect为complete/validated、四个closed33/33且无gap；recording_status仍为unverified。两个原应用分别HTTP/SOCKS入站，出站均为SOCKS B→HTTP A，B实际CONNECT目标19110正是A监听端口，A再连应用目标18995。两槽的inner/outer `partial_sent→release_authorized` hold分别124,208,736/124,952,368ns及125,713,087/126,276,986ns，源outer ready为254ms/255ms。两层各两份完整来源快照均无提前setup。内层实际relay分别154B上传/92B下载，包含外层121B HTTP请求头/59B回复头与33B应用数据；外层、来源及应用均双向33B。

另两槽分别保留HTTP403、SOCKS坏REP的第一次失败，第二次才取得实际Conn/ref/成功证明及唯一33/33段；四flow共6 attempts，8个upstream accept、4个target accept。独立原connector为PID95/start508、103/start566、110/start620、116/start660。actual top-level checker和实际来源恢复均成功；此前离线合成manifest/sourceinspect探针失败原样保留，未将其改评为通过。所有自有core/collector/client、fixture socket/线程已清理。

正式场景按[ADR011](../adr/011-source5-quality-acceptance.md)和[正式质量契约](../../specs/001-hoopa-proxy-manager/contracts/source5-formal-quality-v1.md)分层：固定核心可达网络、source5受控输入和适配器边界分别计证。六VM/21原应用槽的逻辑场景已明确，具体新镜像及全部slot输入仍须在首个正式VM前冻结登记；当前未启动正式执行。某项能力不足不会被全unknown或合成结果替代。SQL事务、生产账本和用户界面不在本阶段提前搭建。
