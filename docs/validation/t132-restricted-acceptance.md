# T132：受限连接来源原型验收裁决

2026-10-25，主代理裁决：T132 在 [ADR010](../adr/010-restricted-identity-profile.md) 所定义的固定 Linux 原型范围内完成。新0015实际正例和实际启动拒绝均已通过；此结论允许冻结 T137 候选并继续 P0，正式 G01、生产权限部署及产品交付仍未通过。

## 新制品与字段能力

[0015构建记录](../../experiments/mihomo-events/core-field-profile-candidate-manifest.json)固定 Mihomo v1.19.26 的 commit、001–015补丁顺序、Go1.27.3、依赖和新二进制 SHA256 `1ef1b53ae41435596844c264ae5e1e5201ce95b5d2762be872371023f4d6af25`。从干净上游实际应用补丁后的源码树与审查树逐项一致；定向 race 38顶层/180含子项通过、0跳过，vet及构建成功。构建记录创建时的“运行未验”状态保持原样，后续运行由独立记录追加。

[新字段实测](connector-field-profile.md)的唯一新正例为 `vm-positive-xuab9n1d`，使用原始 source5帧、manifest2和handoff4。两个非特权原连接发起进程分别是 PID86/start_ticks479 与 PID91/start_ticks525；实例的完整坐标还包含boot ID、初始PID namespace和leader BOOT启动时间。程序名 `python3` 与路径 `/bin/python3` 来自同次connect的296B exe快照，并与独立原socket/proc证据及实际24B scope读回值一致。名称不是kernel comm、脚本名或产品显示名。

实例、程序名、程序路径三字段在 `linux_amd64_loopback_connect_v1` 中可用；comm仍为unverified。旧process字段不使用，旧status中的三个PROCESS规则能力仍为false，所有旧PROCESS/UID规则仍拒绝。没有监测逐字节writer或全部FD转交；连接历史按已证发起者保留，不能把该能力描述为当前唯一FD持有者。

两个客户端入口分别为HTTP、SOCKS5，两条上游均为HTTP CONNECT。实际Conn上的node:a/node:b及同flow/attempt的ready点分别得到11ms/31ms；定义是`forward_setup_v2`、`http_connect_accepted`，不是业务TTFB。每流真实SHUT_WR后均收到33字节及EOF，源终末上传/下载33/33、已证连接段守恒、未分配字节0。完整epoch共有13帧；独立只读恢复得到closed、tail0，无gap。

实际候选的三种非法字段需求（comm、重复、未知字段）均以真实exit1和受保护错误标志拒绝，1顶层/4含子项通过、0跳过。该运行标志不区分唯一内部拒绝分支；配置判据、实际guard函数测试及已审查调用顺序共同证明拒绝发生在PrepareGuard和listener之前。不能用最终端口不通代替这个顺序证明。

## 判据与证据边界

| T132判据 | 证据及裁决 |
| --- | --- |
| 原connect到原accepted的关联、原始坐标与唯一性 | [连续性裁决](connector-continuity-review.md)、固定内核源码顺序及新实际正例。精确cookie链与insert-only冲突拒绝；不使用时间邻近或首个PID猜配。 |
| 同名父子、子独立建连、PID复用、接纳前早退 | [生命周期实测](connector-executable-lifetimes.md)保持原0013摘要及结果；新v5字段只在原准入成功后形成值副本。 |
| 工作线程、父connect子写、FD转交、exec/改名 | [原组合边界记录](connector-executable-core-edges.md)按原hash引用；connector归属不被后续writer替换，comm不升级。 |
| tuple复用、TFO、缺记录/冲突与namespace越界 | [连接负例](connector-executable-connection-negative.md)、[范围负例](connector-executable-scope-negative.md)、[逐连接user/mount守卫](connector-namespace-guard.md)及Owner定向断言；失败不发布已识别opened。 |
| 捕获中断、重启和来源缺口 | [实际cgroup hook移除](connector-cgroup-hook-loss.md)、[核心崩溃记录](connector-executable-core-crash.md)及v5完整/pending/反复独占重开定向测试。旧实际崩溃是v4，不能写成新v5崩溃运行；readonly pending不会伪造零或不可恢复gap。 |
| 启动需求、旧版本严格性与字段来源 | source5 exact形状、manifest绑定、旧v1–v4拒绝多余字段、v5混版/坏raw/缺失字段反例，以及本候选实际preflight与新正例。 |
| 资源与等待 | [原短窗口资源记录](connector-executable-lifetimes.md)和[两次Admit计时](connector-admit-measurement.md)保持原hash/窗口；四个hash各1024、insert-only无回收。161824ns/89686ns包含mutex及同步读取，不能拆成纯锁等待。未测新候选长稳态或完整10分钟预算。 |

受限连续性成立依赖固定内核/BTF/对象、挂接早于目标socket、普通非TFO完整握手、初始namespace、受控cgroup及可信loader/管理员。模型中选择性漏掉一个active而仍执行passive可导致错配的反例仍保留；没有实际注入该选择性漏采。在固定源码直接调度与上述前提内采用有条件顺序证明，不把零fault或“尚未看到错配”当成一般正确性证明。发现前提内可达反例须重新打开T132设计。

## 对T137及后续的影响

本次变更增加纯字段profile、handoff/source/manifest版本、受保护配置需求和已存在路径上的显式绑定；没有改变BPF对象、112/296B记录、Owner锁和capture校验顺序，也没有修改ACK/GC/恢复事实顺序。旧实际矩阵只作为未变更机制的原hash证据引用；本候选自己的字段、质量分派、终末和配置入口由新运行与定向测试核验，旧passed不转贴到新hash。

T137据此追加独立候选和限定能力记录。原G01失败0/100保留，新正式分母仍为0；两个校准流不能累计充作正式100。随后执行T136控制harness和T127–T129正式矩阵，P0/P1继续受原门槛约束。其他平台、IPv6、UDP/QUIC、任意特权对抗、生产部署、持久账本和进程路由规则均不在本次完成范围。
