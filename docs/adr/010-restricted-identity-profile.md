# ADR010：下一身份候选使用明确受限的连接来源 profile

状态：2026-10-25，受限工程方向已由[新v5实测](../validation/connector-field-profile.md)及[T132裁决](../validation/t132-restricted-acceptance.md)验收，T137已冻结独立0015候选。以下设计前提保持；旧v4的unverified不升级，正式G01/P0/P1仍未通过。

## 采用的边界

继续采用用户已选定的 `connect_initiator_v1`。实例属于发起原始 TCP connect 的 PID/TGID、leader 启动身份和坐标；写入者、FD 持有者及应用显示名独立表达。[生命周期实测](../validation/connector-executable-lifetimes.md)已补足 PID 复用、父子独立连接和原进程在准入前退出的证据。下一步集中于把这些证据接入可声明、可拒绝的受限 profile，不再无条件扩大故障矩阵。

名称采用[ADR009](009-process-label-sources.md)中明确区分来源的方案：程序名是同次 connect 的 executable 路径 basename，程序路径是该次有界原始路径快照；leader/caller comm 保留独立来源和可信状态。名称合并、筛选、规则和历史必须带来源版本，不能把同一文本的 executable 名、comm、应用显示名混合。当未来规则要求 kernel comm 时，必须有独立能力，不能用程序名代替。这是基于原需求的工程取舍，不伪记为用户新增的澄清答案。

该选择不修改旧 v3/v4 帧、不回填 legacy process 字段、不直接启用 PROCESS-NAME/PATH。新字段能力需要新的版本契约及候选。解释器名表示解释器，` (deleted)` 不剥离，原字节和 UTF-8 可用性分别保留；路径不被当作永久文件身份。

## 连续性采用有条件的论证

固定内核源码的普通、非 TFO、connect-to-listener 三次握手中，客户端先执行 active-established 回调，完成后才发送或安排最终 ACK；监听端收到该 ACK 后，accepted child 才执行 passive-established。已核对 `tcp_input.c`、`tcp_minisocks.c`、`tcp_ipv4.c` 的顺序和 `cgroup.c` 的直接 effective-program 调度。该路径未发现 tracing 式跳过或 established callback flag 开关。详情见[连续性说明](../validation/connector-continuity-review.md)。这是固定源码与成立前提的推论，不是“实测每一次回调”。

在此前提下，新 active 对 exact tuple 插入新 client cookie；若已有旧 tuple，INSERT_ONLY 失败并置 sticky fault。即使后续 passive 看到了旧值，Owner 在授予前的 fault 检查也拒绝。若没有旧值，passive 只能读取本次已发布的 client cookie，再用原 accepted cookie 固定绑定；Owner 不做近时间、第一候选或 PID 扫描猜配。tuple复用已有实际负例。

前提不能省略。整条 sockops 被卸载后，其错误计数仍可为0；[实际 cgroup 移除](../validation/connector-cgroup-hook-loss.md)依靠 link 的 cgroup_id 变化被准入拒绝。当前 Owner 只在 Admit 入口核验一次 link，之后核验 config/gate/fault、读取记录、再核验 fault/gate；没有前后两次 link 核验，更没有阻止特权方在中间替换程序的机制。不得把一次前置检查或零计数写成抗任意特权修改的保证。

## 必须声明的前提及实际满足程度

| 前提 | 当前隔离实验 | 后续运行入口的责任 |
| --- | --- | --- |
| 固定 Linux 6.18.54.1/amd64 内核、BTF、BPF 对象、核心及 ABI | QEMU 启动输入/摘要、guest release/BTF 与制品逐项核对 | 只允许已登记组合，版本或能力不符时拒绝该身份 profile |
| IPv4 127.0.0.1、固定原入口、普通完整握手 | 两协议原socket与端点核对；TFO负例另有实际拒绝 | 不把 IPv6、TFO、UDP/QUIC、simultaneous open、TCP_REPAIR 纳入已证范围 |
| 程序早于目标 socket 挂接，root覆盖原入口，客户端在受控leaf；PID、socket net及路径所需user/mount namespace满足登记范围 | PID1先bootstrap/adopt，再释放自有客户端；已有PID/net负例。[新0014守卫](../validation/connector-namespace-guard.md)已逐连接检查user/mount，两个实际namespace改变后均拒绝；核心自身预检和客户端启动检查不能替代此守卫 | 启动顺序、cgroup所有权及建连时namespace必须验证；任意外部既有socket不能补签原connector |
| 原link/program/map对象被核心持有，固定配置世代 | 旧20FD与新scoped 21FD分别接管、CLOEXEC/对象核验，实际移除与核心崩溃/新世代分别有证据 | 不复用旧世代map或按超时删tuple；恢复使用明确新世代 |
| 捕获期间无人改变hook/effective数组、socket cgroup、ftrace或内核状态 | 早期夹具为root；[新降权实验](../validation/connector-unprivileged-client.md)已实际核对65534/全部capability零/NNP及所测管理接口拒绝，未证明动态namespace不变 | 生产必须限制目标应用的管理权限，明确受信管理员边界；普通用户环境尚未完成该部署实现 |
| 精确记录而非无限保活 | 四个hash各1024、insert-only；错误和重复使当前世代拒绝，不回收覆盖 | 容量/恢复须公开。这里没有长期无界运行承诺，正常基础代理管理不应被伪称已交付 |

如果前提无法证实或原记录缺失/冲突，profile 必须拒绝关联及依赖规则，不能降为猜测的“已识别”。应用应能明确选择不依赖该 profile 的基础代理路径；这属于后续产品接入，并非当前原型已有的自动降级实现。

## 接下来只关闭明确剩余项

1. [原 Admit 调用边界计时](../validation/connector-admit-measurement.md)已于2026-10-25实际完成：161824ns、89686ns，包含mutex及同步读取；旧 unavailable 和纯锁等待不可拆分的边界保持。已有短CPU/RSS/map类型内存记录不冒充完整预算。
2. [非特权客户端](../validation/connector-unprivileged-client.md)已实际完成受控降权验证；[新守卫](../validation/connector-namespace-guard.md)三实际VM也已完成，证明真正改变user/mount后拒绝目标连接及正常followup。已完成[新source v5](../../specs/001-hoopa-proxy-manager/contracts/source-connector-v5.md)：用独立handoff、字段能力、manifest和启动前需求校验承载受限实例/程序名/路径；comm不升级，旧PROCESS规则保持拒绝。新0015已干净构建、实际preflight拒绝和同拓扑正例通过；独立记录不改旧制品结论。
3. 主代理已按T132明确判据完成受限裁决与T137候选冻结。继续T136最小控制harness及T127–T129正式矩阵；不重复旧0/100或用已完成的小流累计凑正式100。

不把“没有单独构造出一次 active 漏采”伪写为实际通过，也不因此无限增加无关压力测试。若后续发现信任前提内可触发的旧tuple错配反例，必须重新打开关联设计；若只有范围外或管理权限被主动改变的路径，则保留明确不支持边界。其他平台、真实SSH/休眠、完整10分钟性能和生产权限部署按各自后续任务验收。
