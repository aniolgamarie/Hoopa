# T132 有限 accepted 关联模型 v1

状态：**条件化的离线研究模型，不能作为在线身份解析器**。接续[生命周期观测](connector-lifecycle-spike-v1.md)与[停止写入候选](connector-stop-spike-v1.md)。实现为[association_model.py](../../../experiments/sourceidentity/association_model.py)，没有新增 BPF profile、map ABI、核心补丁或 CapabilityRecord。既有 observer 不消费此模型输出。

## 1. 待证明的关系

目标链为：原始 connect 的进程实例 → 客户端 socket cookie → active 事件的端点 → passive socket cookie → 核心原 accepted FD。两侧是不同 socket，cookie 不要求相等。归属沿用 connect_initiator_v1；父进程 connect 后子进程写入、FD 转交不改写已证 connector。PID 必须与启动实例一起使用，不能按同名或当前 FD 持有人补配。

本模型只描述 IPv4 TCP、两端 127.0.0.1、普通三次握手、预登记入口及有限新建观察对象。键包含 generation、**socket 实测** netns cookie、规范化 client/server 端点；固定地址族、协议和地址在模型中省略，不代表实际实现可忽略它们。TCP Fast Open、simultaneous open、TCP_REPAIR、NAT/透明改写、未知 BPF 改写、跨 netns 转发和残留旧 socket 均不在该条件命题内。

netns 不能从异步 callback 的当前 task 或配置声明推断。固定 v6.6 的 sockops helper 读取 socket namespace；原 accepted FD 的 SO_COOKIE/SO_NETNS_COOKIE 用来核对服务端 socket 及其 namespace，不能直接读出客户端 peer cookie。[helper 源码](https://github.com/torvalds/linux/blob/v6.6/net/core/filter.c)、[getsockopt 源码](https://github.com/torvalds/linux/blob/v6.6/net/core/sock.c)。现有 lifecycle/gated 48B established ABI 没有实测 netns，这些旧记录不能直接转换成模型的有效输入。

## 2. 因果依据与 accept 的精确边界

普通 active 路径先在 tcp_finish_connect/tcp_init_transfer 调用 active-established callback，然后发送完成握手的 ACK。据此推导：完整同步执行的 active 发布可以先于该流 passive 查询；这不是时间相近匹配。[固定 v6.6 TCP 路径](https://github.com/torvalds/linux/blob/v6.6/net/ipv4/tcp_input.c)。TFO/交叉 SYN 等其他分支不能沿用此推导，仍须在目标内核验证观察器覆盖与写入成功。

服务端顺序必须区分三个点：tcp_check_req 经 hashdance 使 child 进入 accept queue；随后 tcp_v4_rcv 调用 tcp_child_process 处理状态转换；inet_accept 取到 child 后还要取得 child socket 锁，才 graft 到交付的 socket。普通新 child 的状态处理在释放 BH socket 锁前执行，支持这条受限路径下 callback 与 FD 交付的先后关系；**不能把 queue 已出现/已 dequeue 当作 callback 已完成**。[tcp_check_req/tcp_child_process](https://github.com/torvalds/linux/blob/v6.6/net/ipv4/tcp_minisocks.c)、[调用方](https://github.com/torvalds/linux/blob/v6.6/net/ipv4/tcp_ipv4.c)、[inet_accept 的锁与交付](https://github.com/torvalds/linux/blob/v6.6/net/ipv4/af_inet.c)。模型不证明这些内核锁路径，不依赖模拟 accept 事件发布在线结果。

## 3. 冻结的模型协议

每个成功合成流按 C→A→P→Q 推进，不同流枚举全部交错；指定 reuse 的流须等前一流 Q 后开始，表示有序复用反例，不模拟 TCP 的真实复用许可。Q 是离线诊断查询，P→Q 是模型调度约束。最终发布还要求所有阶段完成，并假设停止协议已得到稳定边界；模型没有实现该边界。

| 阶段 | 可用输入与行为 |
| --- | --- |
| C：connect 捕获 | 按 client cookie 插入 generation/netns/入口配置与 PID/start 快照。此时不能预知 active 才观察到的客户端临时端口 |
| A：active 发布 | 按自身 cookie 核对 C，再用 active 自身端点构造 key，NOEXIST 插入 key→client cookie |
| P：passive 绑定 | 用自身 socket namespace/反向端点构造相同 key；查到 client cookie 后，按自身 generation/netns/accepted cookie 插入绑定 |
| Q：原 FD 证据查询 | 核验预登记 generation/netns，只按实际 witness 的 accepted cookie 查绑定，再读取 C 的实例。不把 oracle 中的正确 cookie 作为解析器输入 |
| 最终发布 | 所有阶段结束且无 sticky fault，才产生研究用 candidate_pairs；任一冲突、容量、缺失或上下文错误拒绝整轮，不发布部分成功结果 |

三个表仅插入；不覆盖、不 delete、不 LRU、不按 TCP_CLOSE 回收。满表和重复 key 是失败，不能丢弃旧记录继续称完整。该方式避免旧删除误伤新插入的 ABA，却不适合作为长期运行的无界生产索引。

每条模型边把一个完整 callback 抽象为一步，NOEXIST 插入按原子操作处理；不证明真实多次 helper 操作的交错、内存序、故障 map 可用性或 verifier。连接失败只留 C、不形成 pair；该小模型含失败流的整批不发布，未定义生产环境正常失败连接与其他流并存时的可用性策略。错误 witness、漏采及容量为合成注入，所有 uint64 展示为字符串。独立 oracle 仅在结果产生后比较 PID/start，不能用它修补候选关联。

## 4. 三条可重放错配反例

1. **忽略 fault**：旧流完成后，新流复用 key。新 active 插入冲突；passive 仍可能读到旧 cookie。若忽略 sticky fault，会把新 accepted socket 归给旧进程；正确模型拒绝整轮。
2. **漏采新 active**：同样复用 key，但新 active 的观察完全缺失，不产生 fault。新 passive 命中旧 key，最终可以零 fault 错绑。完整回调覆盖必须独立证明，不能从零 fault 或稳定快照推导。
3. **取错原 FD**：witness 错取另一条真实已接受连接的 cookie，同 generation/netns 下查表完全合法，仍然错绑。解析器不能凭 cookie 数值判断它是否来自当前原 FD；获取、保存、包装与关闭顺序必须另验。

这三条反例是模型成功捕获的不安全行为，不是内核故障实测，也不产生正式 G01 分母。后两条直接否决“NOEXIST + 零 fault 即可上线”。模型的 correct 名称只指按约定拒绝 fault，**不表示省略前提后仍安全**。

## 5. 下一实现与实际验收条件

| 顺序 | 必需证据与失败处理 |
| --- | --- |
| A：范围与连续性 | 在隔离 Linux 固定内核/BTF/boot、初始 PID namespace、socket netns、客户端与 listener 的实际 cgroup/创建时序；新对象/空 map、完整 attach 和 gate 开放后才放行。预登记原客户端及服务端真值，断开/绕过任一 hook、迁移或范围缺口必须显式拒绝，不能只检查结束时 link 仍在 |
| B：带 netns 的关联观察 | 单独版本化 ABI/profile，实际核对 active socket cookie 与 C、passive cookie 与原 FD；旧 48B ABI 不复用为新能力。必须验证 helper、共享 map、端序、NOEXIST 冲突、容量和稳定停止，不能仅凭离线可编译进入核心 |
| C：原 FD 见证 | 在包装前取得原 accepted socket 的两个 cookie，生命周期持有方式须防 FD 编号复用/并发关闭；验证原 FD、包装对象、另一真实 FD、核心出口 FD 的正反例。模型中的合法 witness 是前提，代码不能接受调用方自行声称来源正确 |
| D：独立核对与反例 | 预登记客户端 PID/start/socket 与服务端原 FD；测试用关联标记只供独立 oracle，不参与候选查表。按实例逐条核对，不能只比数量。覆盖普通并发、同 PID 新启动、失败 connect、早退、共享/转交、tuple 复用、漏 hook、错 witness、命名空间错配；未知不补零 |
| E：在线接线门槛 | A–D 和停止/连续性真实成立后，再审查路由前读取的一致边界、等待/拒绝和能力撤销。离线最终拒绝无法撤销已经执行的路由，故不能直接把本模型改名为在线 resolver；随后才进入 T137、正式 T127–T129 与 G01–G03 |

仅输入中写 coverage=true、两次快照相同、回调时间接近、map 数量相等、状态为 ESTABLISHED 均不代替以上证明。没有隔离环境时不发布内核关联能力；可以独立准备和验证不加载 BPF 的前置组件，不把 v6.6 接口存在外推为当前厂商 5.10 可用，也不改产品最低支持版本。

C 项后续已有[原 accepted 见证准备](accepted-socket-witness-v1.md)：包自身 AcceptTCP，在单个 Control 窗口读取真实 socket 字段，快照与私有连接绑定。本机普通 Go Close 的窗口得到局部运行证据，但 SO_NETNS_COOKIE 返回 ENOPROTOOPT，完整双 cookie 正例仍未验证；不补全旧模型的 witness 来源前提，也不放行 A–E。

## 6. 状态与兼容

模型输出固定 runtime/verifier/identity_capability=unverified、result_claim=bounded_model_only。源码/测试及三条反例见[验收记录](../../../docs/validation/connector-association-model.md)。既有 capture/lifecycle/gated 源码、loader、历史 JSON、001–007 核心补丁与原 G01 0/100 保留。T132 未完成，139 项/21 完成不变；本次没有正式身份新样本、没有回填通过的平台或能力。
