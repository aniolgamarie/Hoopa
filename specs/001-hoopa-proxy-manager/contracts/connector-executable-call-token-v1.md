# 原生 connect 调用编号与快照消费原型

状态：2026-10-24 运行前登记，仅供 T132 私有隔离原型。此前未消费快照反例仍保留，不能把此候选写成正式身份能力。

固定 Linux 6.18.54.1 guest、内核/BTF 摘要、x86_64、原生 `connect(42)`、初始命名空间、专用 cgroup 和 IPv4 loopback 127.0.0.1:18993。仅目标 TGID 的连接参与本次原型。VM 无网络设备和宿主共享；宿主只编译、启动和读取证据，不加载 BPF。可信 loader 独占实验期间的程序、map、link 和配置；不覆盖恶意特权进程改写内核/BPF、ptrace 或其他 syscall 重写机制。这是明确的实验前提，不是已经实现的生产防护。

`raw_tp/sys_enter` 每次观察到目标线程的调用先清旧 token；只有 id42、TS_COMPAT 未置位、标准原生用户 CS0x33 才分配非零编号。共享单调 u64 计数器以最多8次 CAS 尝试生成编号，达到上限不回绕；用尽重试、分配或读取失败设置 sticky fault。编号保存在当前 task 的 storage 中；`sys_exit` 清除。LSM 将同一次调用编号与 executable 引用路径一起写入 insert-only cookie 记录；cgroup/connect4 要求当前编号与记录相同，再 CAS 消费。记录升级为296字节、state为64字节、task storage为8字节，独立 profile `executable-native-connect-token-v1`；旧288字节记录不兼容。

同一普通同步 syscall 的这些阶段属于同一 task；迁移不改变 task storage。每个 task 只有一个活动用户 syscall，跨 task 的编号由原子计数器去重；不存在对同一 task token 的普通并发用户写者。首轮仅单线程，不验证跨线程 fault 与所有并发准入之间的全序；生产集成不能把普通 fault 读取当成撤销已在进行的其他准入。本原型不将有限运行解释成全内核内存序证明。必须保存程序类型/ID、map结构、link附着与清理、原始状态和完整记录，原始 recursion_misses 非零不接受。link存在或misses为零本身不能证明所有路径的持续覆盖。

原生以外的调用不获得 token。cgroup gate 另外检查实际 TCP socket 的 `fastopen_req` 与 CO-RE `fastopen_connect` 位，拒绝直接及延迟 TFO；accepted-side 的0010 TFO_CHILD检查仍独立保留。此项不宣称 io_uring、compat/x32 或 kernel-originated connect 已获得支持，也不假设所有内核 API 都经过此 gate。

首个有限运行先建立普通 TCP 连接，再关闭 listener（保留已接受/客户端 FD），随后加载候选。按序执行：

1. 只挂 entry/exit/gate，新 cookie 原生 connect 必须 EPERM，不能凭 token 缺少路径而放行。
2. 加上 LSM，新 cookie 普通 connect 得到 ECONNREFUSED，证明 gate 放行到无人监听端口；记录 consumed1。
3. 对加载前已建立的原 socket 再次 native connect，必须 EISCONN；LSM 留 consumed0、gate 不执行。
4. 解除 LSM、改名 executable，原 socket AF_UNSPEC 重置（SO_ERROR104 后读取清0），再原生 connect 必须 EPERM，原记录 consumed0 且全字节不变；新 cookie 同样 EPERM。
5. 恢复 LSM，新 cookie 普通 connect 再到 ECONNREFUSED，记录新路径并 consumed1。
6. 新 socket 设置 TCP_FASTOPEN_CONNECT=1 后原生 connect 必须 EPERM，采集记录保持 consumed0；独立新 socket 用 MSG_FASTOPEN 必须 EPERM，不得回落普通 connect。
7. 解除 entry link、保留 exit/LSM/gate，新 cookie 原生 connect 必须 EPERM、无新快照；恢复 entry 后普通新 cookie 恢复 ECONNREFUSED。

全部普通尝试用 blocking FD 和3秒 SO_SNDTIMEO，结果同时保存返回值/errno、原 FD/cookie、时间、计数与记录。用户数据不进入转发，没有 Mihomo 集成。正常预期 token 依次1–9，末态 captures5/connects9/allowed3/refused6/fault0/next_token9/entries9/exits9；若真实路径或 verifier 推翻预期，保留失败并由主代理决定修订，不能把失败转成通过。

配置冻结、BTF与所有输入 pin、全部导出摘要、EOF/cleanup 和无 kernel incident 是验收条件。通过只说明上述有限用例；T132/T137、正式身份开关、G01 新样本0及原0/100记录保持原状态。
