# 建连时 user/mount 范围守卫 v1

2026-10-25，运行前登记。延续[ADR010](../../../docs/adr/010-restricted-identity-profile.md)和[非特权客户端](connector-unprivileged-client-v1.md)实验，关闭已发现的具体缺口：零 capabilities 与 NoNewPrivs 不保证应用以后不能进入新 user namespace；核心自身的 namespace 检查也不等于客户端建连时检查。选择内核捕获路径中的逐次检查，不通过启动时采样宣称动态不变，也不引入应用 seccomp 策略。

## 版本与固定边界

新增独立 `scoped-executable-online` 实验 profile、BPF 源目录/对象和核心补丁0014；旧对象、0013核心、旧运行和旧handoff v1/v2语义保持。固定6.18.54.1/amd64、普通IPv4 loopback TCP、受控root/leaf、原native connect token及非TFO判据不扩大。该批仍输出严格source v4的unverified证据，不开放PROCESS规则或正式身份能力。

不改32B capture_config、112B connect记录或296B exe记录。在原9map/6program/5link基础上增加一个24B ARRAY map `identity_scope`，总计21个移交FD。map key为u32零，max_entries=1，BPF只读；初始全零。值为little-endian：`generation:u64, userns_inode:u32, mountns_inode:u32, schema_version:u32=1, reserved:u32=0`。generation须与原capture_config相同，两个inode非零；不把reserved复用为隐式版本。

bootstrap在受控guest初始user/mount namespace中从自身与PID1的真实proc namespace记录一致值，先写入、读回、冻结两份配置，再挂接任何采集link。任一步失败只清理自有资源，不启动核心。旧profile不接受新map或额外字段。BPF新对象只能经新profile加载；map/program引用集合须逐项验证。

新增严格 `hoopa-core-identity-handoff-v3`，在原字段之外仅增加非零u32 `userns_inode`、`mountns_inode`，maps必须多且仅多 `identity_scope`；全部21FD唯一、有界、完成复制才允许exec继承。未知/缺失/重复字段、旧schema带新字段、配置或世代错配一律拒绝。核心恢复CLOEXEC、核对对象及精确配置、通过只写同值仍EPERM确认冻结，并核对核心自身与PID1、handoff的user/mount坐标；成功后才监听。每次Admit保留原link/config/gate/fault顺序，额外核对冻结scope内容；不改变锁、终末、恢复或旧source格式。

## 调用线程上的检查与失败

读取必须从当前connect调用线程出发，而非借用group_leader的cred/nsproxy。检查 `current->cred->user_ns` 的level为0、inode等于登记值，以及 `current->nsproxy->mnt_ns` 的inode等于登记值。空指针、读失败、配置无效或不匹配均拒绝。现有PID实例、socket netns、cgroup、同call token和原accepted关联判据继续适用。

在目标LSM executable捕获前检查，取得path后再检查；两次满足才允许发布本次exe记录。cgroup/connect4在消费exe记录及发布connect记录前再次检查。失败置原世代sticky capture_fault；目标connect实际拒绝，后续正常目标connect也不能借旧记录继续。LSM返回及拒绝计数必须明确，不把“没有新exe记录”当作已证的单一内部失败分支。原raw/LSM诊断不重置。

该检查约束user/mount范围，不声称一次普通CO-RE读取就是跨全部进程状态的原子快照。当前线程同步系统调用内不能自己并发setns；其他线程的namespace不自动替换当前线程nsproxy。路径由内核d_path按调用者fs root解析，不宣称mount inode单独固定全局根目录；本profile的受控启动、零初始capability和受信管理员前提继续成立。时间namespace不用于解释内核boot-time字段，本次不增加其守卫或宣称全面namespace支持。受信特权方修改共享fs root、挂载或hook仍在已声明的信任边界之外。

## 验证顺序与独立判据

先验Python3.8纯fake、Go相关race及新BPF编译/离线map引用解析，覆盖精确版本字段、冻结前挂接禁止、所有失败cleanup、scope字节/世代不符拒绝、旧profile严格性；这些都不能代替实际verifier和系统调用。

实际验证只在自有无网卡/无宿主共享、有界隔离VM内执行，每种模式新VM：

1. 正例：按降权契约的两顺序HTTP/SOCKS客户端完成全部UID/GID/capability与权限拒绝检查；新增user/mount原始坐标和21FD核对。两原PID/TID/start/cookie与exe快照、node:a/b、真实ready、SHUT_WR后33/33完整EOF、13帧/2终末/无gap、admitted2/rejected0/revoked=false及四零诊断全部成立。
2. user负例：保留正常基线流后，由自有降权客户端明确执行 `unshare(CLONE_NEWUSER)`；保存原始调用/errno及变更前后user/mount/PID/net/cgroup。必须实际成功进入新user namespace且PID/net/cgroup仍属原范围，再向原目标执行native connect，预期EPERM/零协议应用写/无新增flow；同原initial namespace的后续正常客户端也必须EPERM。若guest不允许创建，记该模式未验证，不改称守卫通过。
3. mount负例：保留正常基线流后，仅限自有root夹具执行 `unshare(CLONE_NEWNS)`，保持初始user/PID/net及同leaf，证明只有mount坐标改变。目标connect及正常followup同样拒绝；这是隔离守卫测试，不能作为非特权客户端具有mount管理能力的证据。不执行mount、更改sysctl或宿主namespace。

负例保持已完成基线的原帧前缀及完整来源终末；读取实际capture_fault变化、状态与其他诊断，区分BPF未进入accepted与Owner拒绝计数。逐次保留VM/extract/check结果、errno、原始scope值和输入摘要。任一失败保留原制品与运行，修改后使用新镜像/新run，不回填旧结果。

通过本批只证明新增守卫在登记范围内有效；T132最终身份能力、新版本字段契约、T137干净构建及正式G01仍须独立裁决。生产launcher、其他内核/平台、任意privileged干扰与长期容量不由这三种模式证明。
