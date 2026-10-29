# ADR007：P0身份、恢复与质量证据的收敛方案

日期：2026-10-23。状态：可行性复核后的设计输入，**不是运行验收通过**。T132/T133/T134保持未完成，原G01的0/100、001/002/003补丁与旧记录不变。依据[规格](../../specs/001-hoopa-proxy-manager/spec.md)、[ADR006](006-source-identity-proof.md)和[T133部分结果](../validation/source-lifecycle.md)。

后续实施记录：本文保留设计形成时的状态。T133明文本地最小闭环随后已完成，见[来源传输验收](../validation/source-transport.md)；T132/T134的线程/进程字段、受控部署及实际Conn证明方法由[ADR008](008-remaining-evidence-execution.md)进一步具体化，仍待相应真实原型。原G01、正式G02/G03及P1没有因此放行。

本次修订记录用户的新明确选择：默认按原始客户端TCP连接发起进程归属，FD转交不改写历史；正式规格/用例随之更新。此前以request writer为首期必要身份的设计取消。此前把v2 recovery_gap、全前缀跨端摘要及跨epoch引用图作为T133必经步骤的方案撤回；它们没有实现或取得运行证据，不再作为P0任务要求。本次没有加载BPF、改系统权限或替换核心。

## 一、可行性判断

| 工作 | 判断 | 进入实现前的条件 |
|---|---|---|
| 已批准的connect发起者归属 | 有条件可行 | 原入口关联、启动身份/建连快照及后端连续性仍须真实原型证明 |
| 以connect hook证明请求写入者或唯一FD owner | 不采用为首期目标 | 用户选择connector；不能暗示观察了后续每次写入者 |
| T133日志重放、pending/gap恢复及严格校验 | 工程路径明确，可独立推进 | 补核心接线/恢复报告、故障及计数边界；既有局部测试不等于完整通过 |
| 强制v2跨epoch恢复引用协议 | P0不采用 | 收益不足以承担版本迁移、历史原文依赖、配额和永久pending成本 |
| T134三类协议ready及真实两线路 | 可分步实现 | 先冻结事件/模型字段，再按协议确认、预读/partial边界和路线做真实校准 |
| 全协议/全平台/无权限的完整进程与时延支持 | 无此保证 | 每字段、后端、协议、平台按证据单独声明能力 |

## 二、源进程身份：原始连接发起实例

### 用户已确认的口径

默认`attribution_basis=connect_initiator_v1`。源进程指向Hoopa已验证原始入口建立客户端TCP连接的进程实例；不指随后每个请求/字节的作者，不指当前FD owner，也不从应用profile或进程树推断。

- 主进程和子进程各自connect，分别归各自实例。
- 父进程connect后把已连接FD交给子进程，仍归父connector；这是实际connect证据，不是把父身份默认套给子。
- fork共享FD、后续FD转交、改名或exec，不改变已记录连接的connector及其建连快照；后续新连接重新捕获。
- 不把GOST/Mihomo新建的上游socket证据当成原入口客户端的connector。完整计量仍走已验证的原入口；兼容转发链缺原始证据时单列限制。
- 只有connector事实缺失、来源/namespace错配、入口关联多解或捕获连续性不明时，相关字段为unknown。FD共享/转交本身不再自动令已证connector未知。

进程名和路径是**建连时快照**。路由前验证的是该快照与原入口连接的关联，不是稍后查询一个复用PID或新exec路径。字段能力分开：实例、名称、路径分别有来源和证据；路径未验证时按路径规则加载失败，不以名称证明代替。名称是内核comm等观察名时必须注明来源，不冒称完整可执行文件名。

界面/JSON必须显示归属口径及限制。没有监测FD转交时，`fd_transfer_observation=unverified`，不能写成“没有转交”；也不要求为了connector归属而追踪每次FD传递。晚到connector补证可修订统计归属，不改写过去选路。

旧v1/旧proc候选数据不自动获得该basis。它们保留原字段/证据类别；原0/100不重新评分。新测试标明`connect_initiator_v1`语义，转交FD案例按新的明确预期重新登记；正式100流仍须待新候选固定后执行。

### 后端仍是有停止条件的技术原型

Linux内核connect观测是候选，尚未成为已裁决可交付后端。固定v5.10的IPv4 TCP连接路径具有cgroup connect hook，只说明有捕获调用任务的入口，不说明当前宿主可以加载或完整覆盖。[Linux固定源码](https://github.com/torvalds/linux/blob/v5.10/net/ipv4/tcp_ipv4.c)。ring buffer可能因容量不足而reserve失败，不能单靠异步用户态事件流证明无遗漏。[上游文档](https://docs.kernel.org/5.10/bpf/ringbuf.html)。

T132分成两个最小原型：

1. **捕获原型**：两个同名进程及各自子进程connect真实入站，捕获TGID/PID坐标系、boot/start/PIDns/netns、建连名称快照、客户端socket身份和后端generation；与预登记真值核验。路径能力另验，缺字段不猜填。
2. **关联原型**：给出可审查的客户端socket→原accepted socket关联算法、明确匹配键和字段来源，再验证真实入口。客户端和accepted socket的cookie不同，不能相等比较；纯时间接近或tuple近似匹配不够。必须覆盖同tuple复用、并发短流、早退、跨namespace及后端重启/丢失。

### 可审查的最小关联候选（未选定、未运行）

为避免只写“需要证明关联”，首次spike具体限定同一netns、loopback IPv4、客户端与Mihomo均在明确监测范围、普通TCP三次握手；Fast Open、跨netns/NAT/IPv6等不据此获得支持。拟验证：

1. connect4时以客户端socket cookie暂存connector实例及comm快照。不要在此把未最终确定的源端口当匹配键；固定v5.10连接路径在后续才分配源端口。[tcp_ipv4.c](https://github.com/torvalds/linux/blob/v5.10/net/ipv4/tcp_ipv4.c)。
2. ACTIVE_ESTABLISHED回调取得完整本地/远端地址端口，以netns/地址族/规范化完整tuple及后端generation建立客户端cookie索引。原始快照经cookie传递，不能在异步回调里把当前执行任务当connector。
3. PASSIVE_ESTABLISHED回调按反向完整tuple查询该生命周期内唯一客户端记录，复制证据到服务端accepted cookie索引。active/passive回调及tuple字段存在于固定UAPI；普通握手代码提供先active再passive的候选窗口，实际attach/可见性必须原型核验。[bpf.h](https://github.com/torvalds/linux/blob/v5.10/include/uapi/linux/bpf.h)、[tcp_input.c](https://github.com/torvalds/linux/blob/v5.10/net/ipv4/tcp_input.c)。
4. Mihomo在路由前从原始accepted TCPConn读取SO_COOKIE，查询服务端索引，再复核tuple/generation与字段证据。两端cookie分别使用，绝不相等比较。cookie helper可用性、跨hook稳定性、connect时start/comm坐标及原TCPConn能否保留到路由点均为spike断言，不因存在API就视为通过。
5. 状态回调按各端cookie管理生命周期：客户端索引不得仅靠超时猜删；已绑定的accepted证据不得因客户端退出提前抹掉，服务端生命周期结束后才能安全回收。tuple是当前生命周期索引，不是永久flow ID；旧记录未证结束时不覆盖为新连接，冲突/更新失败/回调脱离或覆盖连续性不明时使相关映射unknown，必要时停用整后端generation。匹配状态走同步内核map，ringbuf仅诊断，不能成为唯一关联状态。

这只是待验算法假设，尤其缺少原子冲突/复用与失败清理的实现证据。2026-10-23后续复核发现固定v5.10的sockops没有netns-cookie helper，“核对cookie后删除tuple”也不能原子防止误删新记录；上述第2–5步不能作为已可实施的完整算法。按[ADR008第6节](008-remaining-evidence-execution.md#6-生命周期准备与关联前提修正)先实现有限不可变生命周期观察，关联实验优先核验固定v6.6的接口，复用/清理仍需独立证明。Spike A验证connect→active的cookie/实例/名称快照传递，Spike B验证active→passive→真实Mihomo SO_COOKIE关联；分别包含失败建连、快速关闭、并发短流、陈旧/重复map和tuple复用。观察到一次误配就停止；预定不可见样本须在路由前显示unknown。路径快照另验，不增加为第一步spike的前置；正式路径规则仍须通过该字段能力。

**停止门槛**：不能在路由前证明唯一关联，或无法检测丢失/状态过期，就停在实验性connect事实捕获，不编写完整采集服务、不将T132勾选。父connect/子写、fork共享按connector正例检验；原connector缺失/关联多解是unknown反例。全unknown及永久拒绝不能代替正例。

首次候选只承诺预登记且实际证明的内核/平台/namespace/地址族范围，不能由Linux5.10元数据或BTF存在推导“Linux均支持”。观测位置、PID坐标系和启动身份单位必须在原型前登记，不能混用host TGID、容器PID、start ticks和纳秒值。

后端默认关闭；其加载、attach、cgroup访问及最小权限须按实际环境验证，用户当前仅选择统计口径，没有授权自动赋权/加载。status/doctor保持只读、不试加载BPF。基础代理不依赖该后端；进程字段能力缺失在配置加载时拒绝，运行中后端退化则新连接的相应规则按unknown处理，已有转发不改路。数据面身份查询不得依赖Hoopa管理器或SQL在线。

原型成本须单列helper RSS/CPU、内核map内存/容量、每连接事件数与入口关联等待；这些计入整套资源评估，不能因不在管理器RSS里就忽略。首次结果只对应固定profile，不自动扩大正式平台支持。

## 三、T133采用最小恢复闭环

### 共同校验代码

提炼小型、无IO/无Mihomo/无SQL依赖的wire与epoch状态校验；Hoopa直接使用，核心补丁携带同源文本副本和摘要。WAL、接收journal与SQL仍各自拥有写入/确认边界，不链接两个进程。

接入source重开、source发送快照、receiver及reader。覆盖版本/字段/重复键、精确整数、epoch/instance/generation、事件ID与source_seq、opened/attempt/终态顺序、累计单调及marker一致性。真实旧v1 fixtures保留原文，包括DIRECT空provider；不维护两套漂移的手写解析器。

### 权威事实与派生恢复报告

P0的权威事实仍是不可变的旧manifest、durable opened/checkpoint/closed/gap及原始source日志。恢复报告是这些事实的确定投影，不是一个伪造的来源事件：

| 情形 | 报告结果 |
|---|---|
| 有opened，无final，也没有旧writer终止/稳定快照证据 | pending，最后可信checkpoint独立保存，尾部null |
| 有opened，无final，明确取得旧source writer终止及全扫描证据 | gap/epoch_ended_without_final，尾部null；不声称网络连接在该时刻结束 |
| 有可信closed，包括此前未投递的closed | 保留精确累计，原ID/原文重放，不额外加一次final，不制造missing-final gap |
| 已有非字节维度gap及精确closed | 保留已知字节，继续显示维度缺口 |
| 文件损坏/缺manifest/快照不能稳定校验 | integrity_error/unavailable，保留证据，不丢尾或换空日志 |

只有显式启动/恢复写操作在取得受管source目录独占writer锁并完成核验后，才能发布旧writer已结束的证据。锁只证明该目录的writer互斥，不证明核心进程退出或网络连接结束。只读reader/查询没有这项证据时保持pending，不顺手修复、封存或写报告。

恢复报告最少包含原source/epoch/flow/opened ID、最后可信checkpoint、扫描截止点、终止证据或其缺失原因、尾部null、scan_status与历史质量。时间只记录证据时点/可证范围，不用新启动时间冒充旧连接的精确结束时刻。

若需持久保存一次恢复结论，使用独立的`reconciliation_id`，由固定命名空间+旧source/epoch/flow/opened+missing-final事实类型生成；不包含新启动nonce/时间/截止点。同ID同事实幂等，内容冲突拒绝；不能推进source_seq、receipt_seq或新增ByteFact。P0可由保留的source日志重复生成报告，无须每次重启另写来源恢复事件。生产持久Gap/Reconciliation由T073同事务去重，传输/回收若需要额外记录由T138另行设计。 生产引入GC前，T138须确保接收端已有可恢复的manifest、原帧，以及（恢复判断必需的来源结束证据或等价已持久稳定恢复事实）；不能仅凭字节帧ACK删除唯一推导材料。转运格式另在T138冻结，P0不提前实现。

pending 不发布不可恢复缺口事实。持久事实仅在旧 epoch 已结束且稳定全扫描后生成；同 ID 冲突比较只覆盖不可变事实字段（旧来源引用、最后可信 checkpoint、边界及未知尾部），不比较每次报告的扫描/验证时刻等观测元数据。重复报告可携带新的观测元数据，不能因此新建事实或报事实冲突。

`scan_status`、历史gap质量和当前`recording_status`分开。已确认的旧缺口继续显示，不要求先把永久未知尾部“恢复完整”才接新流；新记录能力本身无法确认、文件完整性有问题或容量不足时才拒绝新完整计量。

### 重开与确认顺序

1. 取得writer锁，配对核验旧manifest/帧文件、版本及身份，全量严格扫描。坏尾/孤立/不明文件保留并拒绝新完整计量，不自动截断。
2. 对完整但先前结果不确定的帧，重开校验及文件Sync成功后才进入已确认重放集合；新建目录项还须持久化目录。[fsync(2)](https://man7.org/linux/man-pages/man2/fsync.2.html)。
3. 生成上述恢复报告，保留旧epoch/事件原文。报告失败显示unavailable，不能输出一份空的“完整历史”。
4. 确认当前新epoch的manifest/源文件、终末与marker预留及每流opened持久化能力，再发布当前记录ready。不要为全部历史流预留新的recovery_gap事件空间，不等待collector在线或历史引用核验。
5. sender只发送已确认截止点，按原source_seq/event_id/原文重放旧事件；receiver Write+Sync才返回receipt ACK。接收副本与SQL提交仍分开。

启动扫描发生在新flow开放前；运行期保留flow→source锁顺序，不持source锁等待flow锁或网络ACK。P0仍不回收source，到8MiB/32epoch等实际限制时明确停新计量。生产持续运行能力仍由T138完成，不能宣称有界P0日志永远可用。

### T133故障与成本验收

- opened后退出、checkpoint后退出、closed未投递、503/ACK丢失及多epoch重放：旧身份/原文保真，无重计，尾部未知不为0。
- 完整Write后Sync不确定：本实例不推进；重开全校验+Sync后按原ID消歧。
- 短写/坏checksum/重复键/错epoch或generation/缺manifest：保留坏文件，显式拒绝，不静默修尾。
- 反复崩溃/重开：同一旧流的派生恢复结论不产生第二份字节事实，也不生成一串新的来源恢复事件。
- 配额满/ENOSPC/collector离线：已有转发不改路，当前记录能力/历史缺口状态分别展示，新的完整计量遵守实际容量。
- CONNECT/SOCKS5/预读/partial n/lazy握手失败：按已冻结边界实际n计数，不计请求长度或外层握手wire开销。
- 在配置上限内测量全量重开扫描时间、峰值内存及最多未终结flow数；预先登记测试规模与容差。上限必须来自代码及manifest，不能将假设的无限历史当作P0输入。

故障注入与真实进程退出分开记录；注入不是实际断电/宿主磁盘填满证明。T133补齐实现及证据后仍由T128执行正式G02；本次范围收敛不关闭任何已失败或未执行断言。

## 四、T134先冻结最小协议profile再实现

T134工程上可分步推进，但当前v1 schema不能完整表达新connector快照、已验证ready_kind、cancelled及稳定线路字段。**应先同步data-model/telemetry契约和合成fixtures，再修改来源/receiver**，避免再次到集成时发现字段无法解析。

若这些必需字段打破当前严格v1形状，使用明确的新实验事件profile v2；保留v1解码/旧原文，每epoch版本固定，新hash另验。这里的v2是身份/计时/线路字段演进，**不包含已撤回的强制recovery_gap/跨epoch引用图**。T133先接好现有v1共同校验，T132/T134增加字段时扩展同一包。

| ready_kind | 成功边界 | 限制 |
|---|---|---|
| tcp_connected | DIRECT目标TCP建立 | 不代表TLS或业务成功 |
| http_connect_accepted | 上游HTTP CONNECT成功响应完整解析 | 固定候选当前只接受200；通用CONNECT成功语义为2xx，不把实现子集当协议全貌 |
| socks5_connect_accepted | VER/RSV/REP及完整地址均校验成功 | 不是仅收到一个可解析地址 |
| ready_unproven | 无法观察或尚未校准的协议 | duration为null，不混入成功分布 |

HTTP CONNECT的通用成功响应规则见[RFC9110 §9.3.6](https://www.rfc-editor.org/rfc/rfc9110.html#section-9.3.6)。本期只按固定核心实际接受且完成校准的分支声明能力，不能自动改写其它状态处理。

flow从target_known计到经协议证明的ready，包含身份关联等待、DNS、选路与重试；attempt只计本次尝试。用同进程单调时钟算差，UTC仅展示关联；按definition/ready_kind/protocol/目标范围分组统计。失败、超时、取消及ready未知保存结果/等待/阶段，不能当成功耗时或业务TTFB。

固定候选的SOCKS5回复头缺少成功码校验，已提供[003补丁](../../experiments/mihomo-events/patches/0003-socks5-reply-validation.patch)及27项合成流证据（本地原件 `../../experiments/mihomo-events/socks5-reply-validation.json`，不随Git上传）：原实现11个拒绝反例失败，修复后全部通过。没有新binary或真实ready标定，旧001/002候选仍不含此修复。

在每次具体Conn产生处保存source/epoch/flow/attempt/generation、当时规则/逻辑目标、有序节点链及稳定节点/provider身份；tracker绑定同attempt。没有具体Conn的失败只记selection_only/unavailable，不从意图推导实际链。节点改名/切组不改已有连接历史。

若要给每节点分配字节，记录预读、partial Write及relay所属连接段的实际n。flow总量=可证连接段总量+同口径未分配量，不把这些视图再加一次；无可信拆分点的累计恢复只恢复flow总量，节点维度保留缺口。

真实校准至少覆盖：两个独立受控代理端点A/B、慢DNS、延迟成功回复/拒绝/截断、超时/取消、A失败后B重试、保持A长连接切到B并改名后历史不变。另测**HTTP CONNECT成功回复头和首个隧道payload同包**，确认解析预读保留/不丢/不重复计数；固定适配器的临时bufio读法提示此边界必须实测，目前不是已证运行缺陷。lazy无明确确认仍未知，业务加密/复用内TTFB为N/A。

## 五、更新范围与执行顺序

1. 用户口径落入spec的FR-034/049/050、相关用例/SC及产品统计/路由文档；原失败不重新评分，测试语义另登记。
2. data-model和telemetry契约补connector快照、恢复报告、ready/route/字节段及能力字段；research/plan/tasks同步，宪章原则不降低。
3. T132先做捕获及入口关联的可行性原型；关键关联无算法/真实正反例就停止，不先建完整helper服务。
4. T133按最小恢复闭环收尾；T134按明确协议/数据profile实现及真实校准。二者不等待完整身份/UI/SQL工作完成。
5. T135接字段级/协议级能力拒绝及入站ready；T132–T135各证据齐备才T137冻结、再正式T127–T129。T138/T073保留生产传输、持久对账与回收责任。

本次只完成需求语义澄清及可行性/计划修订，没有新增运行能力或任务完成标记。标准托管CI可做适用编译/测试，不自动证明特权内核观测、真实SSH/休眠或平台正式支持。
