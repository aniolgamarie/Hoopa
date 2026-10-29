# ADR008：剩余身份证据、来源故障与协议质量的实施边界

日期：2026-10-23。状态：剩余问题的实施决定；T133明文本地最小闭环已通过[独立验收](../validation/source-transport.md)，T134按[质量/线路局部验收](../validation/quality-ready.md)完成，T135亦已通过[受保护入口局部验收](../validation/protected-entry.md)；2026-10-24补充独立VM两流校准及有限在线准入正反例，T132仍未完成，不自动放行G01–G03、T137或P0/P1。接续[ADR007](007-p0-evidence-closure.md)与[来源局部补全](../validation/source-recovery-closure.md)。

## 1. 分开能力开发与正式门槛

T133负责现有v1来源恢复和计数边界的最小能力，不要求先完成T127身份100流；T128在其自身依赖满足后另执行正式G02。两者的矩阵不能混成一个无结束条件的任务。T133仍须完成自己的全部明文断言，不能以此划分删除故障要求。

新增真实传输测试使用同一固定001–005二进制和实际`SourceReceiver`持久journal：

| 场景 | 必须观察到的事实 |
| --- | --- |
| collector完全离线 | 主入站完成受控字节收发，source保存checkpoint和closed；上线后按原ID/seq/原文接收 |
| collector持久接收后扣住ACK | 已有主连接继续收发，checkpoint/closed继续产生；HTTP等待不能持source/flow锁 |
| closed持久但ACK未达时SIGKILL | 明确验证自有子进程因SIGKILL结束；receiver重开同原文返回同receipt；core重开保持旧manifest/frames并重放，新的epoch正常接流 |
| 同事件反复重放 | 同ID同原文保持原receipt；累计closed只结算一次，不把CP与final相加 |

健康入口、collector与echo均只监听本地回环，候选路径/hash、真值和deadline预登记；健康请求不进入计量入站，不消耗被测flow分母。默认不执行网络测试；显式开启后失败不能改成skip。旧首次active异常及原G01失败保留，不能靠换分母覆盖。

首次异常没有足够诊断，不能补造确定根因。已证监听先于Running的探针前提缺陷，通过独立转发预检修正；新前提下的结果作为新run记录。历史异常不会被改判为通过，也不要求凭空恢复其已丢失的现场信息。

## 2. T132需要受控部署前提，不能用普通proc扫描替代

2026-10-23宿主前提诊断必须区分两个执行上下文：默认沙箱的能力位为0、NoNewPrivs启用；与获授权回环核心相同的执行上下文中，effective/permitted/ambient能力仍为0，但bounding set非0、NoNewPrivs=0、Seccomp=0。后者的非特权BPF策略为2、cgroup挂载只读且没有已委派可写cgroup2；bpffs挂载为rw，但root所有的01700目录对当前EUID不可写。不能把沙箱策略套到真实核心，或把可写挂载当成当前用户已有加载能力。当时两者都没有进行load/attach；此历史证据不代表整个宿主的管理员没有能力，亦不覆盖后来独立VM中的真实实验。

首个验证环境必须提供隔离Linux、固定内核/BTF、覆盖client和Mihomo的受控cgroup及独立loader权限。首期仅验证同netns、IPv4 loopback、普通三次握手；不启用TFO，不扩展到NAT/跨namespace。loader只服务该scope，默认不pin、不安装系统服务；attach/map FD、generation失效、detach及关闭顺序必须可审查。普通核心不持加载权限，数据面查询不得依赖管理器或SQL在线。

候选要求connect4保存客户端cookie上的connector快照，再由active/passive建立客户端与原入口关联。普通握手的active callback先于发送最终ACK，passive callback在处理该ACK时执行，这为候选提供因果顺序；这段源码依据本身不证明实际attach、map更新必定成功或清理没有竞态。[固定Linux v5.10 TCP路径](https://github.com/torvalds/linux/blob/v5.10/net/ipv4/tcp_input.c#L5538-L5552)。原始accepted socket的SO_COOKIE必须在包装前捕获或经已验证的unwrap读取；客户端与accepted cookie不同。本轮核查发现v5.10的sockops不能直接读netns cookie，原tuple发布/绑定仍是未成立的候选，不能据此实现权威关联；见下文第6节。

必须先冻结进程坐标，避免下一次实现再把线程混成进程：

- 归属PID采用明确坐标下的TGID；调用TID可作单独诊断，不能代替进程PID。
- 进程实例启动身份来自对应进程leader；不能将TGID与当前工作线程的start字段组合。原始内核纳秒身份与proc的USER_HZ ticks使用不同字段和单位，不能直接比较或静默转换。
- 若进程名定义为leader comm，记录该字段来源及建连观察时点；当前调用线程comm不能冒充leader名。名称读取一致性、并发改名和exec需另有反例，缺证据保持该字段unknown。路径能力另验，不由comm推导。
- PID namespace坐标与观测namespace分别记录；`pid_ns_for_children`不能直接冒充调用进程当前active PID namespace。

这些要求有固定源码依据：helper读取当前task的TGID/PID和comm；CLONE_THREAD共享leader/TGID，每个新task却各自设置start_boottime。[BPF helper实现](https://github.com/torvalds/linux/blob/v5.10/kernel/bpf/helpers.c#L159-L217)、[线程与启动时间](https://github.com/torvalds/linux/blob/v5.10/kernel/fork.c#L2035-L2069)。它们只支持字段语义，不能证明当前connect4程序可通过verifier或读取一致性已验收。

map容量、失败connect未到active的处理、tuple复用/陈旧记录、后端重启和attach失联都是停止断言。“核验cookie后按tuple删除”不是原子比较删除，不能作为已解决的清理算法；准备阶段只保留有限不可变观察，满额/冲突使整轮失效。将来权威关联走同步map，ring buffer只做诊断；冲突/缺失不能用时间近似补配。A捕获与B入口关联均有真实正反例后才可推进T132。没有这些部署前提时，基础代理可工作，依赖缺失身份字段的配置拒绝；这不是G01通过。

## 3. T134必须把证明附着到实际Conn

不再扩展`Dial nil`或`NeedHandshake=false`推断。最小实现采用实际Conn携带的协议完成证明，T134先冻结新实验profile和正反fixtures，再接生产观测点。

| 产生位置 | 可附着的证明 |
| --- | --- |
| DIRECT成功建立目标TCP后 | `tcp_connected`；限定已验普通TCP，不声称TLS/业务成功 |
| HTTP适配器完整解析其实际接受的成功响应后 | `http_connect_accepted`；本固定实现只接受200，保留成功头后的预读payload |
| SOCKS5校验VER/REP/RSV并读完整BND响应后 | `socks5_connect_accepted`；截断/非成功回复没有证明 |
| 其他协议或lazy无确认 | `ready_unproven`，成功duration=null，保留已知failure/timeout/cancelled及独立等待 |

证明绑定新建的`actual_connection_ref`、本次attempt与同进程单调完成点。后续tracker UUID仅是该引用的绑定，不承担更早的连接身份。group可以传递证明，不得重造；未知外层协议必须显式遮断内层TCP证明，不能沿任意unwrap找到内层DIRECT就说整条代理通道ready。无Conn的失败只保留selection_only/unavailable。

flow从target_known计时，attempt从本次调用开始，均到同一个已证协议完成点；finish时刻与ready时刻分别保存。这样来源落盘/调度时间不会误计入ready耗时。失败/超时/取消的分类优先于ready未知，UTC只用于展示关联。旧v1仍保持原文及unverified，不回填为新能力。

稳定线路标识优先由Hoopa生成配置承担：core使用持久node/provider/group引用作为内部名称，用户展示名另存映射。内部引用不随展示改名变化；Mihomo随机adapter UUID不能当跨重开稳定ID。配置bundle必须验证这些内部引用与节点类型/提供者的映射。实际Conn产生/追加chain时冻结当时引用；不能在查询时从当前group或当前别名倒推旧路线。原生或手工核心配置没有该映射时保留原链证据和稳定ID缺失，拒绝需要完整稳定线路的能力声明。

首次Write与relay的actual n更新同一flow锁下的flow总量和所属连接段；同一checkpoint cutoff下验证`flow=已证段+unallocated`。重试前一个Conn已写出的部分字节保留在原segment，不能全部挂到最后attempt。只有flow累计可恢复时不猜分段。新字段属于质量/线路profile，不引入已撤回的v2恢复事件或跨epoch恢复图。

T134按以下顺序实施和验收，均属于原T134，不新增一套重复任务：

1. 冻结严格profile、单调单位、引用及成功/拒绝/截断/取消/未知fixtures；保留旧v1解码。
2. 接三类协议完成证明、实际Conn引用和receiver/reader校验；未知外层不能误用内层证明。
3. 用受控A/B实际代理验证延迟回复、失败/重试、预读和保持A连接切到B；展示改名后稳定引用及旧历史不变。
4. 验证连接段actual n守恒、慢DNS、超时/取消和lazy未知；记录每个定义的分母和未验证协议。

profile文档与协议单测均不能代替这四步的真实结果。T137仍须等待身份、来源、质量/线路及加载期能力拒绝齐备；没有绕过T018启动P1的决定。

2026-10-23执行结果：006候选完成严格v2、三类协议证明、实际Conn链与连接段守恒；最终固定候选的五流/三种失败重试及定向DNS/取消/超时/未知wrapper有分层证据。T134完成仅覆盖报告明列范围；inline provider为null，命名订阅provider和完整核心DNS E2E未验证。下一项可独立推进T135配置能力拒绝/入口保护；T132仍须受控部署及真实关联正反例，T137不能提前勾选。

## 4. T135 用受限实验 profile 验证入口原语

2026-10-23 后续执行：固定 007 候选实现 [protected-core-v1](../../specs/001-hoopa-proxy-manager/contracts/protected-core-v1.md)，不把通用上游配置面直接暴露为 Hoopa 管理接口。严格 JSON 校验和核心 Parse 使用同一份私有输入；全部能力与可选 trace generation/节点映射/来源可写准入完成后，才预留和开放入口。所有进程条件仍明确不支持；合法非进程配置有真实正向用例，首匹配规则和命名入口不走绕过规则的快路径。

控制面仅保留认证 status 与显式 drain，其他修改和主动探测拒绝；HTTP keepalive 每请求重新认证，SOCKS5 强制用户名/密码 CONNECT。SIGHUP 保持原配置，完整替换为独立进程与端口；drain 保留已接受 TCP，并不截断既有 keepalive 的后续认证请求。来源只读重放模式不能准入新计量，所有 socket 先 bind 再 listen，避免晚端口冲突提前开放其他入口。没有承诺跨 listener 的内核原子事务。

最终 fixed hash 的真实矩阵通过，解析器畸形 AND 与 32 epoch 只读来源准入的真实失败均已修复并保留修前日志。详见[验收记录](../validation/protected-entry.md)和机器证据（本地原件 `../../experiments/mihomo-events/capability-validation.json`，不随Git上传）。T135 关闭本地配置/认证/排空原语，跨进程持久操作、崩溃接管和完整回滚仍在 T136；T132 与 T137 的依赖不变，不能因已有 replace 原语提前标记管理器恢复完成。

## 5. T132 暂无隔离环境时的可执行准备（历史阶段）

用户在该准备阶段明确确认“暂时没有，先完成原型准备”。据此只完成[connect 捕获准备契约](../../specs/001-hoopa-proxy-manager/contracts/connector-capture-spike-v1.md)、有限观察源码与离线编译工具，不提前构建完整 helper。已针对本机 BTF 实际编译；该结果只证明生成了含 CO-RE 元数据的对象，不证明 verifier、attach 或归属正确。

首个 capture 子步骤暂限初始 PID namespace 和一个精确 cgroup/netns；leader TGID/start 与调用线程字段分开，comm 只记录未验证观察，路径未支持。故障计数不自行撤销旧记录，未来 loader 必须核验 map、配置 freeze、故障注入与停止后的稳定读取，attach 连续性丢失时不能报告通过。connect 调用尚未与 active/passive 或原 accepted cookie 接线，不能称成功连接或完整身份能力。

本机只读材料为 headers/devel 与 BTF，没有取得完整厂商内核实现；上游 v5.10 helper 分派的 BPF/perfmon 能力检查仅作设计输入。详细[准备结果](../validation/connector-preparation.md)保留编译、假工具测试、环境缺失和实际磁盘故障；T132、T137及正式门槛不因准备完成而放行。

## 6. 生命周期准备与关联前提修正

继续准备时新增[生命周期观测契约](../../specs/001-hoopa-proxy-manager/contracts/connector-lifecycle-spike-v1.md)和独立 `lifecycle` 编译目标。该对象共享原 connect maps，在 active 按 cookie 查捕获记录，保存 active/passive established 及 TCP_CLOSE 的有限不可变观察；没有 tuple 索引、accepted→connector 查询或运行中回收。原 capture 源码及历史制品保持原样。[新准备记录](../validation/connector-lifecycle-preparation.md)区分离线编译、准备器测试与未执行的内核验收。

两项前提已明确修正：固定v5.10的sockops helper分派不支持get_netns_cookie，getsockopt也没有SO_NETNS_COOKIE；lookup/核对cookie/delete的步骤不能原子防止新tuple记录被旧关闭处理删掉。完整反例及官方固定源码见上述契约。仅声明同netns或给key加配置netns不能填补观测缺口，BPF spinlock内也不能直接调用map helper。

未来唯一关联原型首选固定v6.6作验证基线，因为该版本已有两条netns-cookie接口；实际构建版本/BTF、verifier与运行能力仍须预登记，不升级宿主，不改变产品正式支持范围。先用观测原型核对各自cookie/端序/回调/早退，再冻结安全关联算法及复用规则；只编译过的观察、无错误计数或时间接近均不能生成passed身份能力。T132与正式门槛保持未完成。

后续准备已实现真实libbpf对象open/read/close检查和严格单条ABI解码，见[验收记录](../validation/connector-object-inspection.md)。本机缺开发头与所查Go加载缓存，采用已有运行库的公开FFI，不自写ELF/CO-RE加载器。发行包0.6.0不等同上游tag，固定库摘要与实际导出；两个对象的声明匹配及畸形对象拒绝有真实离线证据，但不含load/attach。单条解码保留uint64精度与unknown，不建立集合关联或完整性结论。

## 7. 有限 loader 准备不等于稳定停止证明

新增有限lifecycle加载封装和显式实验命令，freeze、共享map/直接link info核验、全部挂接后放行观测、部分失败逆序清理等顺序已通过假接口测试，见[新验收](../validation/connector-loader-session.md)。该loader准备阶段未执行load/attach；开关不代替环境授权，不创建cgroup或安装服务，也不接入身份路由。

固定v6.6源码显示，cgroup detach更新RCU管理的effective程序数组；关闭FD不证明旧回调结束。MAP_FREEZE只阻止用户态写，也不能冻结BPF回调写入。因而“detach后立即读”不能直接实现此前要求的稳定map/fault快照，当前仅输出diagnostic_snapshot，quiescence/completeness始终unverified。socket的STATE_CB_FLAG还会留存；后续新attachment对旧socket的影响也须列入实验。依据与调用链见[生命周期契约第8节](../../specs/001-hoopa-proxy-manager/contracts/connector-lifecycle-spike-v1.md#8-有限-loader-准备与停止边界)。

后续先设计并验证停止新样本/在途回调的同步与稳定cutoff，核验目标范围和attachment连续性，再推进原accepted cookie唯一关联。零fault、相同计数或两次相等扫描不能替代证明；仍不提前建立完整helper、放行T132/T137或重跑正式100流。

## 8. gated writer 停止候选

进一步准备采用[停止协议v1](../../specs/001-hoopa-proxy-manager/contracts/connector-stop-spike-v1.md)：同锁内检查准入并增加active、关闭准入、退出active，用户态只用BPF_F_LOCK读取；额外不attach的TC程序通过显式TEST_RUN置closed。禁止先读open再计数、禁止用户态读后整块写回计数，两者已有模型反例。

两个原观察body的所有正常/错误返回均在wrapper中汇合退出，数据与fault写先于finish的锁release；同锁LOCK读取为可见性提供固定源码依据。停止候选接入loader清理，错误/超时/计数漂移拒绝扫描。超时仅限制TEST_RUN返回后的轮询，不是内核调用硬超时；closed/active零不声称回调全部退出、TCP结束或采集完整。

本轮101项Python测试、三个profile真实离线构建和对象只读解析通过，默认profile的三个指令section与旧对象逐字相同；具体[验收](../validation/connector-stop-preparation.md)绑定新源码/对象hash。该停止准备阶段没有load/attach/TEST_RUN，当时T132仍缺真实gate/范围/连续性及accepted唯一关联，不改变T137或P0/P1依赖。

## 9. 有限 accepted 关联模型与不能隐含的前提

新增[关联模型契约](../../specs/001-hoopa-proxy-manager/contracts/connector-association-model-v1.md)。普通握手下，connect 捕获、active 按自身 cookie 发布端点、passive 以自身 cookie 绑定构成候选因果链；只插入、不回收，重复 tuple 或容量冲突令整轮失效。模型 Q 是离线 witness 查询，最终发布仅在假设稳定停止后解释；现有 observer/BPF ABI 未增加关联能力。

14 项纯模型测试通过，同时保留三类错配：忽略 sticky fault、复用时漏 active、取错另一条真实 accepted FD。后两类零 fault 仍可能错配，故覆盖与原 FD 来源必须独立证明，不能让 resolver 用测试 oracle 的正确 cookie 自证。C 只记录可得上下文，不预知 active 才有的客户端临时端口。队列发布、child 状态处理、inet_accept 的锁及 FD 交付分别说明，不把 queue 可见当作回调完成。

下一步按契约 A–E：真实范围/attach 连续性 → 带实测 netns 的版本化观察 → 防 FD 复用的原始见证 → 独立正反例 → 路由前一致性与核心接线。最终离线拒绝不能撤回已经执行的路由；因此不得把本模型直接升级为在线 resolver。详见[验收](../validation/connector-association-model.md)。该模型验收时 T132 仍缺内核正例与完整能力；后续两流 VM 正例见第12节，完整能力仍未验收。T137、正式 G01–G03 和 P0/P1 均未放行，旧 0/100 不重评。

## 10. 原 accepted FD 见证独立准备

[见证契约](../../specs/001-hoopa-proxy-manager/contracts/accepted-socket-witness-v1.md)新增独立Go实验包，自己AcceptTCP并在一次RawConn.Control中读取真实socket属性、端点和两个cookie；快照私有绑定原连接，不提供任意FD或自造witness的准入接口，失败关闭新连接并保留捕获/清理错误。这里仅准备关联链的服务端见证，不改变内核关联ABI、observer或001–007核心补丁。

最终fake race为4个顶层/含子测试38个通过，vet/gofmt通过；两个平台stub只离线编译。本机loopback证明普通Go Close标关闭后Control窗口内SO_COOKIE保持稳定，但SO_NETNS_COOKIE返回ENOPROTOOPT，完整正例明确skip/unverified；拒绝、accepted清理及listener仍可用有实际局部断言。最终清理helper调整仅重验受影响拒绝路径，不把skip或package exit0称双cookie通过。详见[验收](../validation/accepted-socket-witness.md)。

受控core未来接线应在serveEntry接受后、处理goroutine及mixed.HandleHoopaConn之前，但需先补接口支持、原FD对passive及独立oracle证据，再验net.Conn可选接口/半关闭/包装链。当前wrapper不是可直接替换所有TCPConn能力的生产适配，T132/T137与正式门槛不变。

## 11. 小规模校准准备链与下一道运行门槛

按[校准契约](../../specs/001-hoopa-proxy-manager/contracts/connector-calibration-v1.md)新增namespaced profile和独立Go fixture/离线checker。established 56B/schema2在原48B布局后追加实际socket namespace，旧profile保持；原accepted见证与client原socket用实际传输token独立后验核对，候选不消费oracle。缺事件、重复tuple、错witness和缺listening均拒绝；startup ticks与内核ns未换算，所有能力仍unverified。

本轮129项共享接口回归、12项核对测试、fixture fake race/vet/build及三个受影响对象真实离线构建/解析通过，旧profile指令段相同；见[验收](../validation/connector-calibration.md)。该校准准备阶段用户尚无隔离环境，宿主NETNS已知不支持；当时不反复重试，也未运行BPF/cgroup或正式100流。

该准备阶段预定的下一道运行门槛为外部lab预登记→namespaced attach marker→launcher先放置server/client再建socket→两个独立client token校准→成功关闭/停止后核对。该链的外部范围、连续挂接、原始输入来源及退出状态必须独立留证；checker只提供诊断一致。真实小样本结果将决定在线关联和核心适配的后续实现，不因工具已齐而勾选T132或放行T137/T136/P0/P1。

## 12. 独立 VM 两流真实校准与仍未关闭的门槛

2026-10-24 已完成上述小规模链的真实实验正例，见[隔离 Linux 实验验收](../validation/isolated-linux-lab.md)与机器记录（本地原件 `../../experiments/sourceidentity/isolated-lab-validation.json`，不随Git上传）。固定 QEMU 9.1.2/KVM、官方 WSL Linux 6.18.54.1，无网卡或宿主共享；真实 BTF/cgroup2、load/attach 与双 cookie 核对成功。成功 run `vm-calibration-64kp6nzk` 中，两个独立客户端 PID 83/84 分别与原 accepted cookie 的见证一致，observer/driver 均 exit 0，停止 admitted/completed=35/35、active=0。首选 6.6 基线仍未验收；该实验不是正式 Linux 或 Windows 支持声明。

实际执行依次暴露并修复 libbpf 0.6 无法读取新 BTF、现代库移除旧 TEST_RUN 接口，以及 map-info 不保留用户访问标志而 FD 模式仍只读的差异。旧库、旧对象与原失败保留；新库仅在私有目录构建 1.6.2，TEST_RUN 使用已核对的 opts ABI，map 永久标志与实际 FD 权限分别严格比较，不通过放宽只读限制取得通过。本次受影响 loader 58 项与 checker 20 项测试通过，和历史驱动准备阶段的 58 项分开记录。

跨 VM 导出保持原材料和 receipt 字节，完整 checker 在显式原目录声明与独立 operator 范围声明下返回 `consistent_unverified`；声明不认证来源，所有身份/关联/完整性/范围能力仍为 `unverified`。5 个离线材料变异与 2 个真实 CLI 参数反例被拒绝，它们不构成真实停止交错、漏挂、范围或复用故障矩阵。后续真实故障结果与在线保护优先级见第13节；启动身份/名称和在线 resolver/核心接线仍未验收，离线后验一致不能撤回已执行路由。

T132 与 T137/P0/P1 不因此放行，139 项任务完成 21 项不变。旧正式 G01 **0/100**、首次异常、旧 manifest 和每轮原始失败全部保留，不增加或替换正式分母。原始二进制、内核及日志只留 `private/t132-kvm-lab/`，Git 仅保存源码、脱敏验收与摘要。

## 13. 真实停止/范围负例与前置保护缺口

后续真实场景均先登记再执行，见[隔离实验验收](../validation/isolated-linux-lab.md)与机器记录（本地原件 `../../experiments/sourceidentity/isolated-lab-validation.json`，不随Git上传）。wrong-driver-scope 故意略过 preexec cgroup 放置，实测 driver 1 / observer 0、scope 预检拒绝且零 fixture，命中原预期负例。它只验证该 driver 的初始放置拒绝，不证明整个执行期间的连续范围。

observer-terminated 的原登记预期为 driver 0 / observer -15 / 无完整 result，实际 SIGTERM 被 observer handler 处理，清理后 observer 1 并写出 interrupted result，driver 1 在发现已停止 observer 时拒绝，未启动 fixture。必须保留该预期失配；真实准入拒绝是另有价值的观察，不能据此改判原登记通过。

随后另行登记 observer-killed，用自有 Popen 的 SIGKILL 在 attached marker 后、driver 前终止 observer。新 run `vm-negative-observer-killed-cliyp3q8` 实际 driver 0 / observer -9、三个 fixture 均 exit 0、无 observer result，最终 calibration failed，命中独立登记的有限缺失材料拒绝判据。原始 SIGTERM 登记与 run、该独立 SIGKILL 登记、成功两流镜像及所有原始材料分开保留。

**observer 死亡后 fixture 仍实际通信，最终拒绝不是路由前保护。** 当前限定 driver 是诊断校准工具，不得用于生产路由。该历史缺口已推动第14节的有限在线原型；后续优先生产核心原 socket 接线及 FD/link/map 所有权，再以独立真值验证长期回收、重启、跨 namespace、exec/名称及完整停止/范围/复用矩阵；已有两个命中有限判据的负例不构成完整故障矩阵或身份能力通过。T132、T137/P0/P1、139 项完成 21 项及正式 G01 0/100 与原分母保持不变，不升级平台支持。


## 14. 有限在线准入已观察，生产所有权与生命周期仍待验

2026-10-24 的[在线实验验收](../validation/connector-online.md)落实了同 owner listener、两条 link、maps 与原 accepted socket 的有限原型。Q 只按该原 socket 见证读取同步绑定，grant 前无应用 I/O；旧诊断 observer/driver 不获得在线授权能力。v1 八场景取得 10 条允许配对与六种拒绝条件；v2 的 native thread、父 connect/子写、SCM_RIGHTS 转交及 tuple 复用四个有效场景新增 4 条允许配对与一种复用拒绝条件。合计 **13 次 VM、12 个有效场景、14 条有限允许配对、7 种拒绝条件**，tuple 场景一允许一拒绝。首次 thread 的回显后 -6/libgcc 缺失失败完整保留；只补私有 image 运行时依赖，新登记重跑成功，host checker 不改判原 v2 失败或混入其配对。

线程 TID/TGID 和 fork/SCM 的原 connector 字段经独立 client oracle 比对；100 Hz 且 time namespace 零偏移时核对 leader 启动 ticks，采样 comm 相等保持 unverified，不证明原子 rename 或路径。tuple 第二次实际同四元组 connect 成功且 PID/cookie 不同，随后服务端拒绝、无 identity/grant、零应用读写并有 fault；bind/connect 失败不能充当此项通过。旧 SIGTERM 预期失配和 observer SIGKILL 后 fixture 仍通信的事后拒绝结论均不变。新的同 owner 服务 SIGKILL 后客户端 connect refused 仅证明实验入口随所有者终止，不能记作管理器离线转发验收。

下一步推进生产核心原 socket 接线及 FD/link/map 所有权，再验长期 map 回收、重启/接管、跨 namespace、exec/名称和完整故障交错矩阵；不能以已有局部准入替代这些边界。正式身份能力保持 unverified，T132/T137 未完成、139 项完成 21 项、G01 新增 0、旧 0/100 与原分母不变，P0/P1 不放行。私有二进制和原始日志继续不入 Git，固定实验内核不定义正式平台支持。
