# ADR006：原socket证据算法与当前可运行环境的阻断

日期：2026-10-23。状态：T131诊断/设计裁决完成；当前宿主不批准完整source_process_evidence/process_rule_tristate能力，T132未启动。依赖[T130真实前提诊断](../validation/p0-prerequisites.md)，保留[ADR005](005-telemetry-replan.md)的同源路线和旧100流FAIL。

2026-10-23后续澄清：用户已明确选择原始TCP连接发起实例及其建连名称/路径快照（connect_initiator_v1）。下面的全owner/proc算法保留历史设计与负向事实，不再作为新T132必须证明当前唯一FD holder的要求；共享/转交不自动否定已证connector。后续语义与原型门槛以[ADR007](007-p0-evidence-closure.md)及已同步的规格为准。

## 已证事实与根因范围

默认工具沙箱的可见owner范围可以读取，但不能bind回环；可运行候选的实际环境存在同UID FD权限拒绝，原socket由实际核心观测时仍报告owner_visibility。两条前提样本已验证相同UID/netns/PIDns和父子关系，观察结果不能在两种执行环境间互换。尚不能仅凭EACCES、某个sysctl缺失或进程名锁定具体LSM/dumpability策略，也不通过停止其他用户进程解决。

旧探针以`/proc/PID`文件所有者筛选同UID候选，这不构成进程实际EUID证明。Linux手册明确proc文件所有权会受dumpable状态影响；FD链接访问还受ptrace访问检查约束。[proc_pid(5)](https://man7.org/linux/man-pages/man5/proc_pid.5.html)、[proc_pid_fd(5)](https://man7.org/linux/man-pages/man5/proc_pid_fd.5.html)。新诊断使用status.Uid；旧Resolve及旧核心未在本任务中改写，后续不能提升其能力记录。

## 原proc实验算法输入（历史方案）

限定Linux、同EUID/同netns/PIDns、IPv4 loopback、原始ESTABLISHED TCP，命名入站在解析目标后、选路前持有真实接入socket：

1. 使用真实peer/local tuple及网络namespace查询客户端socket inode，校验协议/状态/UID，拒绝PROXY头或可变声明地址替代内核tuple。解析目标即分配flow；持有服务端socket不等同拥有客户端FD。
2. 在数据面中确认完整候选范围可见，以status实际UID分类，不能只凭proc目录所有者、launch或首个命中进程缩小集合。进程不存在与权限不足分开，无法分类/截断/上限均无完整证明。
3. 枚举原socket的全部可见持有关系；先固定boot/PIDns、TGID/PID/start及UID，再检查FD并复核这些身份。线程独立FD表必须覆盖或给出可验证共享表前提，不能默认主线程FD视图代表全部线程；cross-UID共享不在当前可证明范围。
4. 复核tuple/inode与持有者集合，保存当时comm及名称来源、可验证exe路径和选路时点。名称、路径、实例分别匹配，exec/改名不能倒写历史；裸PID不是长期规则身份。
5. 多持有者、读取不足、UID/start/namespace变化、已退出或转交竞态无法排除时返回带原因的unknown。当前规则确需该身份时拒绝连接；环境整体不支持时在配置加载阶段拒绝对应能力，基础非进程规则仍可执行。

双重proc扫描只是非原子的候选证据，不能证明不存在中途FD转交，也不能自动产生生产“唯一owner”保证。T132必须用受控正向同名源、子孙、共享/转交、PID复用与权限反例实证上述可识别范围；不能把当前全部unknown或仅外部observer通过当作正向证明。T127完整100流另验。

## 选择与失败出口

保留该受限算法作为可重复实验输入，当前不选择任何已证明可用的生产身份后端。现有宿主不满足可见性前提，线程FD视图/跨UID共享/转交与路由时点仍缺证据；本任务不声称只换环境就解决全部身份问题。

不采用：忽略不可见owner、仅取首个PID、把应用凭据/profile/launcher当作源PID、延后轮询回填路由、默认授予CAP_SYS_PTRACE/eBPF/更改dumpability或系统策略。显式授权的内核事件后端或隔离执行环境是可能的后续具体设计输入，各自须说明来源证据、完整性、权限、FD转交及管理器离线行为并另行原型；本ADR不将它们批准为已实现后备。

允许再试的条件：与这次失败直接相关的实际执行环境、具体算法或实现输入有可观察变化，记录为何预计消除阻断；在同拓扑最小正负验证成立后，再重新冻结候选和登记正式矩阵。修改文档、重编相同源码或只在不能运行核心的沙箱扫描成功均不成立。旧run/正式分母保留，不借改变支持类别通过。

T131关闭的是具体负向裁决和算法边界，不是G01能力。T132/T137/T127/T018继续阻断；T133–T135可独立完善来源生命周期、计时/实际链、配置级能力拒绝，不能因此开放P1或宣称完整进程统计。

后续按[ADR007](007-p0-evidence-closure.md)执行用户已确认的新connector口径；proc枚举保留为诊断/受限证据，优先验证显式内核connect事实和原入口关联两个小原型。父connect后子写仍归父connector，名称/路径能力分别验证，不暗示逐字节writer。尚无通过实测的入口唯一关联算法，没有另行加载授权或必要证据时不加载内核后端；本ADR的proc负向结论及原G01失败不变。
