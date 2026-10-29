# T128 source5 来源与接收恢复：分阶段验证

2026-10-26。T127/T012已完成受限身份验收；T128必须重新验证固定0017（SHA256 `5693d4ac2bda5fcae7e5ac037d23db1b2a48049f681b8c825b8b4db12ac1b967`）。T133旧001–005实跑只提供夹具参考，不继承为本候选的通过。此处首先冻结三个真实传输准备场景；它们完成也不能勾选整个T128。

## 三个传输准备场景

每个场景使用独立、无NIC和宿主共享的固定Linux/amd64 VM，核心、source5身份/BPF对象与T127一致。每场两条普通原connector连接：HTTP CONNECT/alpha.test/node:a及SOCKS5/beta.test/node:b，每条双向33字节。保持connect前原socket/活进程独立观察、truth写入/fsync/release、原始来源身份/目标/节点与整数终末核验。场景名称与客户端ordinary模式分开。

先完成静态/纯检查并冻结镜像，随后一次登记六个准备槽位，按三个VM各执行一次。每次VM、规范提取、host checker与失败都保留；不重试同一登记、不借T127样本或准备样本补正式分母。正式T128的完整组成须在剩余故障准备完成后另行冻结。

| 场景 | 操作顺序 | 必须观察 |
| --- | --- | --- |
| offline-drop-closed | parent独占bind但不listen的collector端口；两流完成来源closed后才启动collector；首次closed持久接收后丢HTTP响应 | offline实际ECONNREFUSED；两条33/33仍转发；同原event/raw重投获得同receipt，journal只保留一次 |
| offline-reject-closed | 同样先离线；首次closed在调用Accept前返回503，后续才接受 | 首次没有receipt，拒绝后原始event/raw重投；最终冻结来源前缀逐字齐全、无重复 |
| held-opened | collector在流前启动；首opened持久接收后阻塞ACK；两个流终末完成后parent才写release | held先于首流完成；无ACK期间转发/来源终末继续，释放后完整补收；opened重投不新增receipt |

held-opened的屏障放在**node:a的首个上游HTTP CONNECT成功响应之前**：node handler读取完整CONNECT，再观察collector同flow的accepted/held记录，保存观察与原始来源尚无closed的证据，然后发送200。不得在客户端仅完成TCP connect、尚未发送代理目标请求时等待opened；核心解析目标后才生成opened，该等待会形成死锁。此屏障不持来源/接收器锁，且有固定截止；失败不改成正常计时通过。第二流仍须在未释放ACK时完成。最后parent向自有release文件写精确`release\n`并flush/fsync。

## Collector与三种位置

实验helper使用当前`OpenSourceReceiverWithManifests`、`Accept`、`Close`，不另造持久接收实现。启动时固定1–32份schema2/source5 manifest；新epoch必须通过显式停止/重开helper加入完整manifest集合，不暗中按事件填充来源身份。

HTTP仅监听显式IPv4 loopback，POST `/events`校验独立Bearer，原事件最多8192字节并严格解析。来源`source_seq`、接收journal的`receipt_seq`、实验每次投递的`delivery_index`分别记录；重投的delivery可增加，原事件及receipt不变。

正常顺序为严格解析→Accept完整Write/Sync→实验evidence Write/Sync→ACK。503场景在Accept之前；丢ACK在持久Accept之后。hold等待在collector/receiver锁外；取消、停止或证据写失败不得返回204。helper日志只存原事件摘要/长度/ID等，不保存Bearer或payload；journal中的原payload保留。日志的durability不替代接收journal或来源spool的durability。

## 对账截止与退出

两个流closed后、核心退出前冻结完整来源前缀和明确cutoff；collector须接收该前缀的所有原始事件。接收journal逐帧验证外层长度/摘要、严格envelope、连续receipt序列、epoch/event身份及base64原payload，与来源前缀逐字比较。来源和接收序号不能互相代填，也不从相同计数推导逐字一致。

核心随后正常退出可写epoch_closed；来源sender同时退出，不能假设该marker必然已经被接收。保存实际是否送达，并明确此组验证的是cutoff内全部事件与两条终末，完整旧epoch重放/重启仍属于后续场景。helper停止需唤醒hold、等handler结束后关闭自己的receiver/log；所有自有进程都要wait/reap。原始证据仅只读，需Sync重开时使用明确私有副本，不能让status/reader修复原journal。

## T128仍须补齐的范围

后续准备和正式矩阵必须覆盖：采集器进程退出与journal重开、活动核心SIGKILL后12/12等非零checkpoint及未知尾部、新核心/epoch重放与新流；正常退出的gap/marker与崩溃区分；同ID异内容冲突；来源配额/背压、真实guest小型文件系统满盘；buffer预读、partial-write与lazy-handshake的actual n边界。真实文件系统故障与测试注入分别标注，不能将逻辑配额当内核ENOSPC。

无writer结束证明的只读来源报告保持pending、final/tail=null。只有明确source writer独占锁及稳定全扫描才能派生gap；当前核心内部报告若只能通过固定源码的测试overlay在私有副本上观察，必须与实际VM接口证据分开。来源spool、接收journal和SQL不构成共同事务，SQL驱动/事务/生产GC仍由后续任务承担。未完成的范围不能用本组三个准备case替代。

## 后续崩溃重开准备的固定顺序

下一独立准备case采用同VM两个核心实例、两个collector实例和两个原始连接槽位。旧HTTP流实际往返12字节后保持socket，等待完整非零checkpoint和collector1的持久receipt；再依次SIGKILL并wait collector1、核心1。旧客户端必须观察EOF或连接中断，超时不能充当通过。保持原connect前parent观察和truth fsync/release，不用旧v4自报身份替换source5证明。

旧cgroup清空后，以新capture generation/BPF对象/PID启动相同0017核心2，同source目录与配置generation。核心2产生新epoch，原epoch文件逐字不变；collector2在固定两份manifest下重开同一journal，用独立evidence日志记录旧原文重投沿用原receipt。新SOCKS流33/33正常闭合后冻结截止，两个epoch的所有原帧与截止内新帧必须完整对账。新epoch_closed实际交付另列，不改变截止定义。

原epoch只读报告仍为pending、final/tail=null；只有固定源码overlay在证据私有副本上执行真实独占openSource/full scan后才检查派生gap，三次重开fact ID/hash稳定，完整原文重放及新closed保持。实际VM重开与这个内部报告检查属于不同证据。若杀死进程恰好留下partial source或journal，保留失败并裁决，不能截断后沿用同一登记记通过；这个case不能代表任意写入中断点均可自动恢复。

## 后续真实满盘准备的条件分支

真实满盘只在guest自有小型tmpfs上作用于source spool，journal和strace输出在挂载外。先核对已有core的PID/start、source `.frames` FD和附加到所有当前线程的tracer，再在12/12检查点后用自有filler填满。逻辑source quota、注入writeFrame错误与真实内核ENOSPC分别记录；ptrace失败不改宿主策略，也不继续把注入当实际故障。

满盘后源文件末页可能尚有余量，实际形状可为零写或partial后续ENOSPC。记录确切FD的syscall及全部原尾部：无partial的完整前缀可在独占全扫描后派生gap；存在partial/坏帧时严格重开须拒绝且不修复。完整write后Sync失败另列，未实际触发时不能宣称已覆盖。追踪会影响时序，本实验不用于性能或协议延迟预算验收。

## 正常退出与配额的预期边界

受保护核心正常停止会先关闭入口和已知连接，再等待处理函数结束，因此“发送TERM时仍活跃”不等于“来源一定产生gap”。准备与正式登记均须预先区分合法终末：真实Close先发生时保留精确final；source shutdown先发生时使用`source_shutdown_before_final`、final/tail=null及不完整epoch。不能要求每次正常退出都造gap，也不能把活动快照当作final；实际未观察分支只保留其源码/注入证据。

逻辑配额要分别记录新流预留不足、单流检查点窗口耗尽和物理文件系统ENOSPC。固定配置下新准入因预留不足而拒绝时，原流仍须能写入其终末；关闭原流后释放预留可能允许新流，不能称作永久历史容量耗尽。直接调用Checkpoint的注入测试不等于真实一秒周期耗尽。TCP对端的分片写入也不保证某个精确partial Write返回值，actual n的注入断言与真实应用预读字节守恒分层记录。
