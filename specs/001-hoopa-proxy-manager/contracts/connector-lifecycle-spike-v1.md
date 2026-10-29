# T132 socket 生命周期观测准备契约 v1

状态：离线源码和编译目标；没有 loader、运行捕获、关联查询或通过的身份能力。接续 [connect 捕获准备](connector-capture-spike-v1.md)，不替代 connect_initiator_v1，也不构成 telemetry wire 新版本。

## 1. 本步骤解决什么

[lifecycle_snapshot.bpf.c](../../../experiments/sourceidentity/bpf/lifecycle_snapshot.bpf.c) 包含原 connect 源码，在**同一个对象**中增加 `sockops` 程序及两个 map。未来 loader 须为此对象的两个程序使用同一组 map，先冻结配置，再完成全部 attach 和覆盖核验，最后放行预登记客户端；任一 attach 失败不得开始样本。当前没有实现这套 loader。

程序记录 active/passive established 及 TCP_CLOSE 观察，为将来核对 cookie、端点、回调和进程早退提供原始材料。它没有 tuple 索引，没有从 passive cookie 到 connector 的映射，不输出 verified 身份。passive 回调中的服务端 socket 尚须与原始 accepted fd 的 SO_COOKIE 实测核对，不能仅凭回调名称宣布已关联到入口。

## 2. 编译目标与可复现输入

`prepare.py --profile capture` 保持原默认捕获目标；`--profile lifecycle` 仅增加固定白名单内的 lifecycle 源文件，包含的 connect 源文件保持不变。两个 C 输入及 BTF 均在任何工具运行前复制到同一私有目录；每个 C 最多 1 MiB，其他原有限额/权限/超时不变。没有任意 `--source` 或递归 include 搜索。

manifest 增加 `profile` 与 `sources`，逐文件绑定原路径、快照路径和摘要；旧 `inputs.source` 仍指向主编译输入。lifecycle 除原必需 sections 外要求非空 `sockops` PROGBITS、长度为 8 的倍数。只构建 Linux/x86_64 的 `bpfel` 对象；不声明其他字节序或平台支持。两种 profile 的 runtime/verifier/association 均保持 `unverified`。

## 3. 新增 map ABI

原三个 map 和 connect 的 112B ABI 不变。新 map 均为 HASH、2048 项，键为 uint64 socket cookie，用户态只读，BPF 仅 `BPF_NOEXIST` 插入。重复 cookie、容量不足或 helper 错误累加原 `capture_faults`；任何故障使本轮全部观察不能作为通过证据。没有覆盖、LRU、delete、定时回收或故障复位。

| Map | 值布局（BPFEL，字段顺序固定） |
| --- | --- |
| established_records | 48B：三个 uint64 generation / observed_boot_ns / socket_cookie；六个 uint32 schema_version=1 / callback_op / local_ip4 / remote_ip4 / local_port / remote_port |
| close_records | 40B：三个 uint64 generation / observed_boot_ns / socket_cookie；四个 uint32 schema_version=1 / old_state / new_state / reserved=0 |

IPv4 地址保留 context 的网络字节序；两个端口在记录中均为主机序。sockops 的 remote_port 是**32 位网络序 context 值**，用整 32 位转换；不能照抄 connect4 中 user_port 的 16 位转换。local/remote 保持各自 socket 的方向，不在此步骤规范化为关联键。[固定 v5.10 UAPI 与 context 转换](https://github.com/torvalds/linux/blob/v5.10/include/uapi/linux/bpf.h)、[filter.c](https://github.com/torvalds/linux/blob/v5.10/net/core/filter.c)。

## 4. 观察与故障语义

- established 仅观察两端地址均为 127.0.0.1 的 IPv4 TCP：active 远端端口或 passive 本地端口须等于冻结的入口端口，端口合法且为 full socket。
- active 按自身 socket cookie 查 connect_records，并核对 generation、捕获时 netns/cgroup。缺失或错配使整轮失效；不在异步回调中读取当前 task 并称其为 connector。
- passive 只保存 socket cookie、端点和回调时间。其 netns、进程身份以及与某条 active 观察的关系均未证明；不把配置中的 netns 复制为观察事实。相同 tuple 或接近的时间不能升级该记录。
- established 插入后，以原 flags OR STATE_CB_FLAG 设置状态通知，保留其他已有 flags；helper 非零返回使整轮失效。注册与后续回调的运行连续性尚未验证。
- STATE_CB 使用 args[0]=old_state、args[1]=new_state；仅对已有 established cookie 记录 TCP_CLOSE，忽略其他观察器可能启用的无关 socket 通知。缺少 close 记录不能推导“仍活跃”，也不能当作零丢失。
- TCP_CLOSE 是状态转换通知，不等于应用 close(2)、进程退出、socket 对象销毁或对端已关闭。它不授权清理 tuple 或 accepted 证据。记录不含累计字节和协议 ready；两回调时间差不能冒充访问延迟或 T134 的真实 ready 延迟。

三个回调不修改目的地址、端口或转发策略；sockops 只注册状态观察 flags。源码返回值与 callback-flags helper 的成功码不同。故障 map 自身可用性、停止后的稳定读取及 attach 连续性仍采用原捕获契约的 loader 停止条件，零故障计数不能单独证明完备性。

## 5. 已否决的关联前提

固定上游 v5.10 的 sockops helper 分派没有 `get_netns_cookie`；sock_addr 支持它不表示 sockops 也支持。该版本也没有 `SO_NETNS_COOKIE` getsockopt 选项；原 fd 的 SO_COOKIE 只能核验 socket 身份，不能代替 namespace 证据。[v5.10 helper 分派](https://github.com/torvalds/linux/blob/v5.10/net/core/filter.c)、[基础 helper](https://github.com/torvalds/linux/blob/v5.10/kernel/bpf/helpers.c)、[socket UAPI](https://github.com/torvalds/linux/blob/v5.10/include/uapi/asm-generic/socket.h)、[getsockopt 实现](https://github.com/torvalds/linux/blob/v5.10/net/core/sock.c)。这些是上游源码结论，未取得本机厂商完整实现，不能代替本机 verifier 测试。

未来关联原型首选固定 v6.6 内核作为验证基线：该版本源码已有 sockops netns-cookie helper 及 SO_NETNS_COOKIE 路径。[v6.6 filter.c](https://github.com/torvalds/linux/blob/v6.6/net/core/filter.c)、[v6.6 sock.c](https://github.com/torvalds/linux/blob/v6.6/net/core/sock.c)。这只消除一个接口缺口；仍须预登记实际内核/BTF、权限、cgroup 与每种 helper 的 verifier/运行证据，不升级当前宿主，也不宣布 Hoopa 的最低支持版本。

“lookup → 核对旧 cookie → delete(tuple)”不是原子比较删除，存在如下反例：

1. 旧关闭处理读取 tuple K，观察到旧 cookie A。
2. 新连接将同一 K 更新为 cookie B。
3. 旧处理按 K 删除，误删 B。

这是按 API 语义推导的并发反例，未在内核实测。map delete 只接收 key；把 lookup/delete 放进 BPF spinlock 临界区也不是现成修复，因为持锁时禁止 helper 调用。[固定 v5.10 map 与 spinlock 契约](https://github.com/torvalds/linux/blob/v5.10/include/uapi/linux/bpf.h)。本观测对象完全不建立或删除 tuple 索引；未来关联原型另行冻结复用、陈旧记录、失败 connect、早退及跨 namespace 的规则，未证明安全之前不回收/复用索引、不进入核心路由。

## 6. 获得隔离环境后的验收顺序

1. 固定目标内核/BTF/工具/对象、boot ID、PID/net namespace、精确 cgroup 和 generation；验证两程序的 map 共享、freeze、故障 map 与完整 attach，再放行测试流量。任何前提失败先停止，不开始正式 100 流。
2. 预登记客户端 socket、connector leader/TID/start、目标端口、原服务端 fd；检查 connect→active cookie 一致以及 passive→SO_COOKIE 一致。两侧 cookie 各自使用，不要求客户端 cookie 等于服务端 cookie。
3. 逐项检查失败 connect、快速退出/半关闭/复位、客户端先于服务端关闭、同名进程/工作线程、父 connect 子写、并发短流、tuple 复用、跨 namespace 和故障/容量注入。缺失与重复分别记录；不能仅用“记录数量相等”判定唯一关联。
4. 停止新流量、按审查后的顺序结束本轮并取得稳定 map/fault 快照。原始时间和状态仍是观测数据；loader/读取端、名称一致性和唯一关联分别验收，不能用本步骤的离线编译通过代替 T132。

## 7. 离线对象检查与单条记录解码

后续准备增加 [inspect_object.py](../../../experiments/sourceidentity/inspect_object.py) 与 [observations.py](../../../experiments/sourceidentity/observations.py)。它们是未来loader所需的对象准入和ABI读取部分，不包含加载、attach、配置写入/freeze或内核map读取。

检查器要求明确profile、对象绝对路径/SHA256及libbpf绝对路径/SHA256。输入均为普通文件、各最多64MiB；对象先校验摘要和BPFEL头，再将固定内存缓冲交给真实libbpf的open_mem，缓冲保持到对象关闭。库调用前后校验摘要；这能发现偶然变化，不构成对同UID恶意替换者或所有动态依赖的强隔离。文件名/发行包版本不证明其与某个上游tag完全一致，具体库以路径、摘要和实际符号为准。

仅绑定open/read/close接口；按实际导出选择新或旧迭代器，旧接口参数顺序为previous/object。遍历每种对象最多16项，重复指针/名称、程序名称/section或map类型/大小/容量/flags不符均拒绝。open返回NULL/ERR_PTR不关闭无效指针；成功对象无论后续检查是否失败均关闭。接口签名依据[固定libbpf头文件](https://raw.githubusercontent.com/libbpf/libbpf/v0.6.0/src/libbpf.h)及[旧接口声明](https://raw.githubusercontent.com/libbpf/libbpf/v0.6.0/src/libbpf_legacy.h)，本机兼容性另有实际离线检查。

输出status=inspected只表示本profile的对象声明匹配；program type/expected attach type仅保存getter观察，`program_type_verification`仍unverified。没有执行CO-RE加载重定位、verifier或attach，因此runtime/verifier/association也仍unverified。工具stdout只输出JSON，错误exit1；libbpf自身诊断可出现在stderr。检查器不创建状态目录，执行时禁止生成Python字节码缓存。

解码器仅消费调用方提供的原始bytes，严格核对配置32B、记录112/48/40B、故障值8B、key、schema/reserved和generation，以及对应记录自身可核对的范围。它不枚举内核map，不读取运行目录，不把单条校验当成集合完备或跨socket关联：

- uint64的cookie、启动时间、generation与fault count在JSON结果中均为十进制字符串，保留大于2^53及uint64最大值。
- comm保留完整16B的raw_hex，状态仅unavailable/unverified。text只在首NUL前为有效UTF-8时产生；非法UTF-8为null，读取失败后的部分原始内容不升级为有效名称。
- IPv4字段按原4字节网络序解释，端口按ABI主机序读取。active/passive仅核对自身端点与入口端口，不按tuple或时间猜配。
- close只校验本profile的TCP状态范围及TCP_CLOSE，不推断对象销毁、进程退出或可回收。fault=0也不生成passed；全部输出带evidence_status=unverified。

上述离线工具只有纯bytes/假库测试与真实对象解析结果，没有运行捕获样本。后续有限loader准备见第8节；实际map共享、freeze、attach覆盖、故障注入、停止与稳定快照仍须另行验收。

## 8. 有限 loader 准备与停止边界

新增[native_loader.py](../../../experiments/sourceidentity/native_loader.py)、[loader_session.py](../../../experiments/sourceidentity/loader_session.py)及[run_observer.py](../../../experiments/sourceidentity/run_observer.py)。它们只承担lifecycle观察原型，当前所有加载/挂接路径仅经fake CDLL/backend测试；不改变第6节的真实验收顺序，也不构成完整helper或accepted关联实现。

加载封装按固定[libbpf v0.6.0 libbpf.h](https://raw.githubusercontent.com/libbpf/libbpf/v0.6.0/src/libbpf.h)和[bpf.h](https://raw.githubusercontent.com/libbpf/libbpf/v0.6.0/src/bpf.h)绑定。type/expected_attach_type setter返回void，设置后用getter核对；明确connect为CGROUP_SOCK_ADDR/connect4、lifecycle为SOCK_OPS。load后通过内核info核对两个程序及五个map的类型、名称、尺寸/flags、唯一非零ID和程序实际引用的map ID集合；两次program info查询有容量/长度边界。当前本机导出符号不代表该运行路径通过，更不证明厂商库与上游tag完全一致。

配置顺序固定：新对象及空记录map/零fault/零config → 写入32B配置并readback → MAP_FREEZE → 对同值写入须被EPERM拒绝 → 再readback → 挂接connect → 核验link info → 挂接sockops → 核验link info。link info核对CGROUP类型、program ID、cgroup ID和attach类型。只用直接`bpf_link_create(..., NULL)`，失败不回退到BPF_PROG_ATTACH；FD 0合法，每个成功返回的FD先登记所有权再验证。FD关闭不重试，避免关闭被复用的编号；主错误与全部清理错误分别保留。

runner暂限Linux/x86_64。用固定打开的cgroup FD核验cgroup2文件系统、非mount根、domain、无进程和子cgroup；配置cgroup ID暂取该FD的inode，并由link info返回的ID交叉核对。该候选坐标的源码依据为固定[v6.6 cgroup_id](https://github.com/torvalds/linux/blob/v6.6/include/linux/cgroup.h)、[kernfs ID/inode](https://github.com/torvalds/linux/blob/v6.6/include/linux/kernfs.h)与[inode实现](https://github.com/torvalds/linux/blob/v6.6/fs/kernfs/inode.c)，不能外推到32位或未经验证的厂商内核。netns-cookie来自操作者预登记，observer的namespace inode另存；不据此宣布初始PID namespace、客户端实际netns或整个范围已证。

准备前后均检查目标为空；外部harness须等`attached_observer`后才启动已登记流量。该检查不构成cgroup变更锁或attach连续性证明，外部并发修改不在此原型的排他保证内。信号处理只设置停止意图，控制器在资源登记后检查；观察期0–60秒（不含0），不能当作阻塞内核调用的硬超时。

停止后按逆序关闭link、再有限枚举map、最后关闭object；配置、挂接、观察、清理任一步失败均不发布正常快照。逐map扫描仅保存诊断值和原始bytes，非原子、无跨map一致cutoff：

- `BPF_MAP_FREEZE`阻止用户态syscall写，**不阻止BPF程序写**。config另有BPF_RDONLY_PROG；不能用freeze观察map取得稳定快照。[v6.6 syscall实现](https://github.com/torvalds/linux/blob/v6.6/kernel/bpf/syscall.c)
- cgroup link detach更新effective程序数组，旧数组通过RCU延迟释放，正在遍历它的回调可能继续写map。关闭link FD没有向用户态提供“回调已排空”的证明。[v6.6 cgroup实现](https://github.com/torvalds/linux/blob/v6.6/kernel/bpf/cgroup.c)
- STATE_CB_FLAG留在socket上，但不是socket独立持有该BPF程序的引用。后续状态事件查询cgroup当前程序数组；新attachment可能收到旧socket启用的状态事件。因此本轮关闭FD也不声称恢复了所有socket flags或消除了后续复用影响。[flags设置](https://github.com/torvalds/linux/blob/v6.6/net/core/filter.c)、[TCP状态触发](https://github.com/torvalds/linux/blob/v6.6/net/ipv4/tcp.c)、[TCP dispatch](https://github.com/torvalds/linux/blob/v6.6/include/net/tcp.h)

据以上固定源码推导，当前扫描只能是diagnostic_snapshot，quiescence/completeness/association始终unverified。两次相等扫描、fault=0或记录数相同都不能补上停止屏障。map枚举有容量/重复key上限，只把ENOENT作为正常迭代结束；其他读取错误保留失败，快照null不冒充空集合。解析错误保留原key/value hex，非零fault使观察流程失败，uint64保持十进制字符串。

后续仍须设计并在隔离内核证明：停止新样本与在途回调的同步、稳定fault/map边界、detach/退出时资源释放、attachment连续性和范围证明，再完成唯一accepted关联。没有这组证据，不得用该观察器放行T132/T137或正式100流。当前[验收记录](../../../docs/validation/connector-loader-session.md)只描述准备与fake测试。

## 9. 后续 gated 候选

在保留上述lifecycle默认行为的基础上，新增[停止协议候选](connector-stop-spike-v1.md)。两个观察body由统一gate wrapper包围，额外TC程序在共享锁内关闭准入，用户态LOCK读取active归零后才扫描；source-level writer停止与callback/RCU退出、采集完备性分别陈述。控制程序禁止attach，未来需要显式TEST_RUN执行，当前仅fake验证。

三个profile的实际离线编译/解析、101项定向测试及默认程序指令section兼容结果见[新准备记录](../../../docs/validation/connector-stop-preparation.md)。第8节对detach/freeze局限仍成立；新候选不以它们当屏障，也不因源码/模型通过而宣称T132运行成功。停止协议的真实交错、范围与连续性仍须先验，才能推进原accepted关联。
