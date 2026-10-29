# T136 最小控制原型 v1

状态：T136最小实验原型已完成[0016有限验收](../../../docs/validation/control-rebind.md)。五场实际控制矩阵与两项guest socket回归通过；不代表产品管理器、正式P0门槛或跨平台支持。0015旧失败/候选保留，新socket预留不承诺未listen阶段独占；协议处理仍在所有listener完成后启动。

## 固定决策

控制进程与核心分离；控制进程退出、关闭IPC或释放控制锁不发送核心终止信号，子进程不设置PDEATHSIG，也不能继承控制锁/IPC FD。普通打开控制器和status只观察已有状态，不启动、停止、恢复或补日志。只有明确的apply、stop、recover产生持久变更。

IPC为私有0700实例目录中的0600 Unix socket，同时核对SO_PEERCRED当前管理UID和独立随机控制token。它不是核心loopback HTTP入口；核心仍使用自身独立认证status/drain。相同UID的恶意进程及可信管理员不在OS隔离承诺内。服务单写者使用实例flock，进程内用一个操作mutex；持久化不持任何账本锁，本原型没有数据库。新实例初始化是独立显式动作。

Linux原型要求`instanceDir/control.sock`绝对规范路径不超过107字节，在创建目录/绑定socket前拒绝超长路径。实例外层保存IPC socket与token，`store/`是传给`InitControlProbe`/`OpenControlProbe`的控制记录目录。该目录必须处于可信、不可由其他用户替换的父目录下；当前路径检查不承诺抵御恶意同UID进程替换祖先目录的竞态。

0015没有undrain。完整替换要求明确accept_disruption：持久记录操作及目标意图，核验并停止所属旧核心，确认旧进程和其采集FD已退出后，才启动新候选。不并行挂接两个身份世代；旧长连接可能中断。新启动失败后，恢复是重新启动完整旧bundle并核验，使用新的OS进程、source epoch和运行generation；不把旧draining对象标回running。配置revision和bundle回到旧值，活动记录中的实际运行身份更新为已核验的恢复实例。无法证明完整旧态时degraded，保留所有证据。

## 持久材料和有界接口

实例目录下保留锁、独立active.json、operations目录、launches目录及不可变bundle快照。文件0600，目录0700；拒绝符号链接、错误owner/mode、超限、损坏和未知版本。初始化不覆盖已有实例。单文件写入采用同目录临时文件、完整Write、文件Sync、rename、父目录Sync；操作文件与active.json不是跨文件事务。任何不确定Write/Sync/rename结果使当前控制器poisoned，停止进一步变更，显式重开/恢复再判定，不能假定回滚成功。

首期最多32份未清理操作，每份最多256KiB、active最多64KiB；原型bundle的Config+RouteMap原bytes合计最多8KiB，路径最多4096B，OS实例字段采用有界PID/UUID/u64启动ticks；在接纳变更前核验容量，不能停掉旧核心后才发现已知输入无法发布。不做GC/压缩，不忘记旧ID。达到上限拒绝新操作，同ID查询/重试仍可读。bundle快照包含固定核心path/hash、完整配置原字节/hash和路由映射模板；生成新运行generation时只生成独立launch材料，不改bundle。请求摘要只针对本实验严格类型结构的规范序列化；不声称实现RFC8785或产品Plan摘要。

控制记录扫描最多1024个目录项，逐文件限制只适用于控制元数据。`launches/<nonce>/`在核验0700/owner及相应原始launch意图后作为运行目录边界，不递归读取核心活动journal、bootstrap输出和stdout/stderr；来源journal保持自身8MiB预算，后端在Observe中另行有界读取并核验claim/config等身份材料。管理器重开不能因正常运行日志超过256KiB而拒绝接管；控制器的只读打开也不代表已验证该运行目录全部内容。当前原型不承诺stdout/stderr轮转。重开如有未完成操作，active必须精确匹配其base、已登记目标发布结果或恢复发布结果；不相关的合法形状记录也拒绝，不能用recover覆盖。

导出的最小Go接口位于`experiments/mihomo-events/control_probe.go`，具体结构以同包类型为唯一实现定义：

- `InitControlProbe(directory)`显式初始化stopped、revision0。
- `OpenControlProbe(directory, backend)`取得控制写入权并只读加载；`Close`释放控制资源，绝不停止核心。
- `Execute(ctx, request)`处理apply/stop；`Recover(ctx, operation_id, expected_revision)`显式恢复原操作；`Status(ctx)`只读返回活动意图、当前观察及未完成操作，绝不修复。
- 后端最小职责为纯`Validate(bundle)`、只读`Observe(intent/instance)`、显式`Start(intent)`、显式`Stop(launch with bound instance)`。后端返回verified-running、verified-stopped或unknown，错误/未知不能折叠成不存在。启动失败只有已证明自有进程结束时才可返回known-stopped；缺少spawn登记的intent一律unknown。

新变更请求包含版本、request_id、apply/stop操作、expected_revision，以及apply所需bundle和accept_disruption；stop同样必须接受中断。request_id限制为有界安全token。Recover是按原operation_id推进既有操作的独立原型方法，不新建操作或覆盖原请求摘要；其重复调用按原阶段幂等，expected_revision仍核对当前active。IPC recover只接收原operation_id和expected_revision，不冒充产品API的独立请求去重实现。未知字段、重复键、多余正文和不适用参数拒绝。读取请求不写日志。严格类型JSON键名按声明精确匹配，大小写别名也视为未知字段；数组中的类型化对象同样验证，防止多个不同拼写覆盖同一字段。

## 操作顺序

1. 取得服务操作mutex，在已持flock下先按request_id查已有操作；相同完整请求摘要返回原阶段/结果，不再次执行，也不先按过期revision拒绝。异内容返回ID冲突。
2. 新请求复核expected_revision；不符立即CONFLICT，持久文件、bundle、launch和运行状态必须零变化。随后核验完整输入、固定候选及影响确认；验证失败也不产生操作。
3. 持久记录operation_id/request_digest、base/target revision、旧active及旧/新bundle快照、目标running/stopped、applying阶段。任何运行态动作之前，相关launch nonce/generation/意图都必须已持久。
4. apply先记录stop-old步骤，核验并停止旧实例，再记录old-stopped；随后记录new-spawn意图，再调用Start。start原型不接受将旧generation沿用到新进程。若初始stopped则无需停止旧实例。
5. 对新实例独立Observe确认binary/配置/generation/控制身份及running，再持久verifying、publishing。发布active.json后，最后持久committed。发布与终态日志之间的故障保留，不能重启一次核心来填补日志。
6. stop在发信号前持久目标stopped，证明所属核心结束后发布stopped及新revision。恢复该操作不得因旧active仍写running而重启核心。
7. 可确定运行失败先持久rolling_back。目标候选如仍存在，只能经完整身份复核后停止；unknown则degraded。旧态原为stopped，确认目标结束后恢复stopped；旧态原为running，持久一个独立rollback launch意图，启动旧bundle、独立Observe后才恢复旧revision/bundle与实际新运行身份、记录rolled_back。同一个rollback intent不能重复spawn。

原型status中的observation只描述active记录所指实例，pending列表另示尚未终结的操作；它不是对所有pending目标是否存活的汇总。例如spawn登记窗口可同时存在active=stopped和未发布的存活target，不能据active观察断言没有核心运行。

任何已有未终结操作阻止新的变更；同ID读取、Status及显式Recover仍可执行。错误或取消在运行态变化后不能跳过证据判定直接返回“未应用”。读status时不得用pending/gap或unknown填零。

## 接管和spawn窗口

launch意图在spawn前持久，带32hex随机nonce、`gen:control-`加nonce的唯一运行generation、bundle摘要、候选path/hash、私有cwd及配置摘要。自有launcher在exec核心前用O_EXCL持久claim：PID、boot ID、OS start ticks、原nonce/意图摘要，然后保持同PID连续exec至bootstrap及核心。claim只证明已登记实例，不独自授予控制权。

实际Linux后端在取得pidfd后复核boot/PID/start、实际exe path/hash、cwd、精确受控argv、nonce环境，以及认证核心status中的generation/config hash；前后复核实例未变、pidfd仍存活。停止只使用该已核验pidfd发信号，不能按裸PID kill。PID重用、缺claim、claim与实际不符、端口被另占或观察权限不足均unknown/conflict，不认领、不杀、不重复spawn。缺claim不能因一次扫描没有找到进程就当成从未spawn。

控制器重启只加载材料和观察；显式recover若目标已准确running且旧实例已准确结束，可完成原publishing/active/committed而不重复启动。若已有active已指向该实例，只补原操作终态。rollback运行若已准确存在则复用并核验；有可能存在但无法证明时degraded。原运行完整、没有未完成操作时，只读核验即可接管控制权；控制器实例不替换核心/source世代。

## 故障钩子与验收

先实现持久状态机和fake backend的确定性故障断言，再接真实Linux backend/IPC/VM。故障钩子只能由测试显式配置，覆盖操作持久前、spawn意图后、spawn返回前登记窗口、旧核心停止后、新核心验证后、active发布前/后、committed前、rollback启动/验证/发布及存储Sync不确定。单元故障不能计作真实VM通过。

必须验证C01过期新请求零写、同ID同内容旧revision重试不重复、副作用前意图持久、未知不杀/不重启、Close不Stop、stopped意图恢复不自动开机、发布窗口恢复保持同PID、rollback使用新实例且恢复旧配置、恢复失败degraded保留材料、两个写者互斥以及只读查询零写。真实最小矩阵另登记：控制进程退出转发继续、重新启动控制器认领同一核心、spawn窗口中断后的明确接管或unknown、替换期间入口关闭/开放顺序、候选失败后旧bundle恢复、IPC授权/未授权及核心身份冲突拒绝。真实SSH/注销/休眠仍归T015。

最初实际后端使用T137固定0015制品，原记录保留。回滚故障经独立诊断后，主代理裁决追加0016候选：只修正受保护socket复用及回归测试，helper改为精确绑定新core哈希；五场新矩阵另行验收。任何后续核心变化仍须先裁决影响与新制品验收，不能修改已冻结候选或把旧hash通过记录赋给新hash。控制器代码存在或fake测试通过本身不计作交付。

实际启动适配：控制器留在cgroup root，每次launch采用独立空的`hoopa-online-control-<nonce>` leaf及新的私有home/trace/bootstrap目录；不覆盖bootstrap的O_EXCL材料。现有应用不会自动迁移到新leaf，跨运行世代的存活应用重新接入不由本原型承诺。管理器重启接管同一存活核心不重建leaf、重载对象或改source epoch。
