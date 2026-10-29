# T132 有限观察器停止协议候选 v1

日期：2026-10-23。对应T132与[生命周期观察契约](connector-lifecycle-spike-v1.md)。这是独立`gated` profile，当前仅有模型、假接口、实际离线编译和对象解析证据，未执行verifier、load、attach或TEST_RUN。它处理观察数据的停止写入边界，不提供原accepted socket唯一关联，不生成passed身份能力。

## 1. 问题与决定

detach更新RCU数组不等于等待在途回调结束；MAP_FREEZE不阻止BPF程序写。因此原lifecycle profile仍只能提供不带稳定性保证的诊断扫描。新候选采用一个共享锁，把“检查准入并登记active”“关闭准入”“退出active”“读取状态”排成同一顺序，数据写留在锁外。

不得采用两个替代做法：先读open再单独增加active，会在停止者看到零以后出现迟到写；用户态读取状态后用BPF_F_LOCK整块写回closed，会覆盖并发变化的active。两者的可重放反例由[有界模型](../../../experiments/sourceidentity/stop_model.py)生成。只读两次相等的map也不是停止证明。

## 2. 对象与ABI

`gated_snapshot.bpf.c`包括原connect/lifecycle观察body，外层wrapper统一执行admit→body→finish。两个原profile仍直接调用同一body，默认不启用gate。原body的正常/失败提前返回均回到wrapper，不能跳过finish。新对象共有3程序、6个map：

| 程序 | 类型 / section | 引用map |
| --- | --- | --- |
| hoopa_connect | CGROUP_SOCK_ADDR / cgroup/connect4 | capture_config、connect_records、capture_faults、capture_gate |
| hoopa_lifecycle | SOCK_OPS / sockops | 原5个map加capture_gate |
| hoopa_stop | SCHED_CLS / tc | 仅capture_gate；禁止attach |

capture_gate为ARRAY，key=u32零，max_entries=1，BPF_F_RDONLY限制用户态直接写入。value固定40B，布局`<IIQQQII>`：

| 偏移 | 字段 | 语义 |
| ---: | --- | --- |
| 0 | bpf_spin_lock，4B | 内核锁；用户态copy跳过该字段，输出缓冲预先置零 |
| 4 | closed，u32 | 初始0，仅可从0到1 |
| 8 | active，u64 | 已准入、未完成观察body的回调数 |
| 16 | admitted，u64 | 累计准入数，不重置 |
| 24 | completed，u64 | 累计退出数，不重置 |
| 32 | poison，u32 | 饱和或不变量破坏后置1，本轮拒绝 |
| 36 | reserved，u32 | 必须为0 |

初始化必须全零；正常状态要求`active = admitted - completed`，且completed不大于admitted。admitted到达u64最大值后拒绝继续准入并closed+poison；finish检查active非零及completed<admitted后才更新，避免下溢/溢出。导出u64为十进制字符串。

gate包围整个body，计数包含随后被过滤的无关回调，不能当作连接数或流量事件数。每次准入观察有两次共享锁操作，锁争用和额外map成本必须在真实环境测量；本原型不作性能预算通过声明。

## 3. 并发顺序与证明边界

1. 回调先lookup gate，再获取其锁。closed为1则释放锁并返回，不写观察/fault/callback flags，也不增加拒绝计数；否则在锁内同时增加admitted/active。
2. 释放锁后执行完整原观察body。所有记录、fault及callback flags写在此区间；任何helper失败也先回到wrapper。
3. wrapper再次获取同一锁，减少active并增加completed，然后释放锁。所有观察写先于此release。
4. 停止控制程序获取同一锁，只置closed=1，不改active或其他计数。停止不可重开；新generation必须新对象/map。
5. 用户态用BPF_F_LOCK读取同一map value。仅当closed=1、active=0、poison=0且计数一致时，记录`closed_and_drained_observed`候选状态。随后扫描观察map，前后再用LOCK读取核对gate不变；读错、计数漂移或超时均拒绝扫描结果。

源码级推理是：stop之后无法新增已准入writer；最后一个已准入writer在完成观察写后才令active归零。后来取得同一锁的读者看到零时，所有这类写先于读者；随后只可能有被拒绝的晚到回调。即使回调已运行到lookup但停在锁前，随后也只能读到closed并退出。gate的锁字本身仍可能变化，协议只约束观察数据和gate业务计数。

可见性依据来自固定[v6.6 BPF锁及LOCK复制实现](https://github.com/torvalds/linux/blob/v6.6/kernel/bpf/helpers.c)、[syscall LOCK读取](https://github.com/torvalds/linux/blob/v6.6/kernel/bpf/syscall.c)、[内核锁acquire/release契约](https://github.com/torvalds/linux/blob/v6.6/Documentation/memory-barriers.txt)。锁helper包含irq保护；架构分支用arch_spin_lock/unlock，fallback用atomic_xchg/atomic_set_release。此为同锁可见性依据，不是通用full barrier或当前厂商内核实测。[verifier约束](https://github.com/torvalds/linux/blob/v6.6/kernel/bpf/verifier.c)要求有效BTF锁字段、一次一锁、持锁期间不调用普通helper。所有lookup和观察helper均在锁外。

这是一组按准入时刻确定的回调，不是时间戳截断的全网络快照。停止时尚未准入的TCP_CLOSE可能被排除；active归零也不代表所有TCP结束、回调指令/RCU退出或入口范围完备。既有socket flags不恢复。初始空cgroup检查不证明没有残留socket、外部变更或整个namespace均已监测；这些继续单独验收。

## 4. 控制调用与失败处理

固定[libbpf v0.6.0 bpf.h](https://github.com/libbpf/libbpf/blob/v0.6.0/src/bpf.h)和[bpf.c](https://github.com/libbpf/libbpf/blob/v0.6.0/src/bpf.c)提供`bpf_map_lookup_elem_flags(..., BPF_F_LOCK=4)`及旧参数形式`bpf_prog_test_run`。后者按repeat=1执行hoopa_stop，输入64B dummy Ethernet frame，data_out=NULL；旧wrapper不传输出容量，不能用预置size_out假装有界输出。仅检查返回码、retval和返回大小，不消费测试包内容。

固定[v6.6 TC test-run实现](https://github.com/torvalds/linux/blob/v6.6/net/bpf/test_run.c)支持此SCHED_CLS调用且不要求attach。该控制程序仅操作gate，无skb修改或redirect helper；测试调用不把dummy frame发往实际网络。TEST_RUN本身会执行内核程序，**当前仅fake调用，未来同样需要加载授权和隔离环境**。

会话在正常停止、观察失败、中断或部分挂接失败后，先尝试stop，再逆序关闭已持有的link，最后关闭object。停止失败保留错误且继续尝试关闭，禁止退化成无gate成功快照。若TEST_RUN发生错误，即使closed可能已写入，也不重开、不把未知改成成功。没有pin、遗留attach兼容回退或自动修复。

active轮询默认预算2秒、最多1001次；预算从同步TEST_RUN返回后开始。TEST_RUN、锁获取及单次lookup可能阻塞，该预算不是整个停止动作的硬超时。资源释放、强制终止和卡住内核调用另需隔离环境验证；不默认创建强杀/提权机制。

## 5. 输出与验收

runner新增显式`--profile gated`，默认仍lifecycle；两者均须`--allow-bpf-load`才可能执行加载。manifest固定profile，结果新增writer_stop。整个结果仍为observed_unverified，quiescence/completeness/association/identity_capability继续unverified；writer_stop只保存已观察到的候选gate状态，不签发CapabilityRecord。非零capture_faults仍使运行失败，停止稳定不能掩盖丢失。

离线验收包括1/2/3回调的去重状态图、两个错误方案反例、全部返回路径包裹审查、共享map及LOCK/TEST_RUN假接口、排空/超时/错误/计数漂移、部分挂接和中断清理、旧profile兼容、实际编译与只读对象解析。模型将每个body抽象为一次观察或fault写；真实body有多个helper，模型不模拟内核内存模型、调度、公平性、BPF verifier或任意规模。

真实验收先固定内核/BTF/库/对象与拓扑，在非正式少量样本中检验：并发writer跨停止边界、admission前后暂停、body失败的退出计数、停止后的晚到回调无观察写、异常poison、停止调用失败和退出资源回收。取得与实际运行匹配的停止、范围和连续性证据后，才继续原accepted socket唯一关联及T132/T137。详见[本轮准备验收](../../../docs/validation/connector-stop-preparation.md)。
