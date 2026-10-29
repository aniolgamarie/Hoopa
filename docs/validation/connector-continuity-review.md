# 组合关联的连续性判据

2026-10-24，固定源码与既有证据复核；不是新的 VM 通过记录，也不放行 T132。范围是 Linux 6.18.54.1/amd64、001–013 核心、executable-online 组合 BPF、初始 namespace 和受控专用 cgroup。固定内核来源 commit 为 `d504d40ab83839cb61942a812a99d183c42227b9`。本次没有审查更新版内核或上游产品。

## 关联能证明什么

[组合程序](../../experiments/sourceidentity/bpf/executable_online/online_snapshot.bpf.c)用原客户端 socket cookie 保存 connect 事实，active-established 向精确 `(generation, netns, client_ip, server_ip, client_port, server_port)` 键插入 client cookie；passive-established 再按原 accepted cookie 保存绑定。[Owner](../../experiments/sourceidentity/coreadmission/owner.go)从真实 accepted socket 的 cookie、netns、端点出发查绑定，沿唯一 client cookie 查 connect 和 exe 副本，前后检查 gate/fault。没有按时间选近似流或枚举候选取第一项。

tuple、binding、connect 和 exe 哈希均 insert-only、容量 1024，不覆盖、不删除、不按超时回收。重复发布会使本捕获世代拒绝；[真实 tuple 复用负例](connector-executable-connection-negative.md)已验证这一有限行为。用户态对这些 map 只有只读权限；不能假装通过该 FD 删除记录来制造内核漏采测试。task-storage 的调用编号另存，任务退出会释放 task-storage，但不会自动删去 exe/connect 哈希；这与[接纳前早退实测](connector-executable-lifetimes.md)一致：两个协议的原 child 都在核心停止期间建连、退出并回收，恢复后仍保留原身份。

“键只保存一个值”和“这个值属于当前连接”是两项条件。如果新 active 回调未执行，旧 tuple 还在，passive 可能看到唯一旧值；insert-only 本身发现不了没有执行的写入。关联连续性必须另有依据，不能由零 fault 或单值 map 推导。

## 固定内核路径与成立前提

固定源码的普通 SYN_SENT/SYNACK 路径经 `tcp_finish_connect → tcp_init_transfer → bpf_skops_established` 调用 active-established。后者构造 fullsock 上下文；cgroup dispatcher 直接执行 socket 所属 cgroup 的 effective 程序数组，没有 established callback flag 开关或 tracing 式递归跳过。返回值沿 void 调用链丢弃，因此 connect 成功也不能代替回调成功证据。实际路径由 scout 对 `net/ipv4/tcp_input.c`、`include/linux/bpf-cgroup.h`、`kernel/bpf/cgroup.c`、`net/core/filter.c` 的固定快照核对。

由此得到的是**有前提的源码推论**：普通完整握手、程序持续存在于该 socket 的 effective cgroup 数组、对象/范围未变时，active 会进入直接执行路径。前提包括 attach 早于目标 socket、覆盖根及受控叶、准确的初始 namespace、核心持续持有并核验对象、无未授权 cgroup/BPF/kernel 管理变更。当前隔离实验不能替未来生产部署自动证明这些前提。

TFO 不能套用上述先后顺序：服务端 `tcp_fastopen_create_child` 可在初始 SYN 时先执行 passive，早于客户端收到 SYNACK 的 active。组合程序在原 connect gate 拒绝已识别 TFO，核心还有[原 accepted TCP_INFO 边界](connector-core-tfo-negative.md)；[新组合两个 TFO 负例](connector-executable-connection-negative.md)实际被拒。各记录只证明其固定输入和观察到的端点结果，不声称插桩识别了所有内部拒绝分支。普通半关闭的正例保留，不能通过拒绝所有连接规避该问题。

## 不同挂钩必须分开看

| 挂钩 | 固定对象/源码边界 | 已有实际证据和限制 |
| --- | --- | --- |
| raw syscall enter/exit | 持有 raw tracepoint link；每次 observed entry 清旧 token，仅 native connect 分配新编号；raw recursion miss 在准入前后读取 | [link detach/update 请求被拒](connector-capture-link-operations.md)，[诊断读取](connector-capture-diagnostics.md)；没有注入真实 recursion skip，零值不证明所有回调 |
| LSM socket_connect | 带引用 exe_file/path 快照与当前调用编号绑定；没有等同 raw 的 miss counter | 缺/旧快照在 gate 拒绝有独立探针；组合普通正例、FD/exec 变化成立，不能用 raw 零值代替 LSM 证据 |
| cgroup connect4/sockops | cgroup link 支持显式 detach；detach 后原对象仍在但 cgroup_id=0，Owner 应在关联读取前拒绝 | 原在线独立实验是关闭 link FD；[新组合两次实际 BPF_LINK_DETACH](connector-cgroup-hook-loss.md)均成功，cgroup_id变0后新连接准入拒绝。sockops移除后四计数仍0，不能借 raw/LSM 的“不支持 detach”结论或零计数证明健康 |

raw/LSM link 的最终引用释放会解除挂接；本次成功取得副本的 privileged harness 临时增加了引用，不是生产所有权方案。cgroup link 的 detach 则可以在 FD 仍持有时发生，必须核验其实际 cgroup 关联，不能只检查 FD 存在。该区别已用于两次实际移除实验的判据。

## 剩余可执行工作

同名父子、PID 复用和接纳前早退三 VM，以及 cgroup 两种 hook 实际移除均完成有限验收；继续固定受限部署的成立前提，原 Admit 调用耗时已另有[实际两流测量](connector-admit-measurement.md)，[非特权客户端](connector-unprivileged-client.md)已实际验证所登记管理接口拒绝，新增0014的[建连时user/mount守卫](connector-namespace-guard.md)已完成三实际VM，下一步实现[严格v5字段能力和启动前需求校验](../../specs/001-hoopa-proxy-manager/contracts/source-connector-v5.md)；启动时namespace采样不能代替已独立实测的逐连接范围检查。移除整个 sockops 不等于只漏一个 active 回调，报告须保留区别。内核没有 sockops missed-counter；“计数为零”不能关闭剩余部署连续性问题，也不能把无法触发的中间状态伪造为已验。

本轮不增加容量饱和、任意特权对抗或所有内核支持要求。任务所需资源记录按实际短窗口、map 类型用量和可观察等待分别报告；不支持的 task-storage 遍历、旧资源记录没有量测接口的 Admit 延迟保持 unavailable；[新实验计时](connector-admit-measurement.md)已实测调用边界，不能填回旧运行或拆成纯锁等待。正式名称/路径/实例能力、T132/T137 和新正式样本数均不变。

## 主代理对有限原型证明方式的裁决（2026-10-25）

T132受限原型的关联连续性采用固定源码顺序论证与实际正负证据相结合。不是要求实验枚举所有内核调度，也不把未找到反例当成证明。必须同时成立的前提是：固定内核和对象、原accepted socket调用链、目标socket创建前挂接、普通非TFO完整握手、受控cgroup/namespace和可信管理方。源码中active完成后才发送最终ACK、passive随后发布绑定的因果顺序，是排除“passive先看到旧候选”的依据；实际tuple重复、TFO和整hook卸载测试分别检查其可触发边界。

模型中主动省略一个active回调、仍执行passive的确可产生零fault错配；此反例保留。没有实际注入仅一次active丢失，也不宣称有这项通过记录。在当前固定源码直接调度和上述可信前提内，尚未找到使该模型状态可达的具体路径。该条件性源码论证足以进入受限字段候选的实现与验收；若发现前提内可达路径，须撤销这一裁决并重新设计关联，不能降低未知率指标。任意特权方运行中改程序/hook等前提外行为不通过增加文案变成已支持场景。

缺失原binding/connect/exe记录的拒绝由Owner定向测试覆盖；实际connect4卸载提供操作级缺失捕获反例，两者分别记录。关联不枚举候选，重复精确tuple由insert-only和sticky fault拒绝，已有实际复用记录。事件丢失/重启由旧来源12/12检查点保留、final/tail未知、新世代33/33以及私有副本的稳定重开gap检查支撑；私有overlay没有变成VM API输出。这些证据已足够关闭各自的有限子断言，无需为不可写map强造删除接口。

这一裁决不勾选T132：新v5字段能力、严格版本绑定、真实启动前拒绝和新候选正例仍须实现并通过；T137和正式100流另验。四个1024容量哈希及当前短RSS/CPU/memlock、两次整段Admit耗时按原口径保留，纯锁等待和完整10分钟预算仍未测，不作为已达成性能指标。
