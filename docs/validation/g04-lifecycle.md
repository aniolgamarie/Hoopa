# G04 生命周期与最小访问保护

2026-10-26：**T015 已完成受限验收，G04 与当前候选 G07 最小矩阵通过，P0/P1 未放行。** 最终只读入口（本地原件 `../../experiments/mihomo-events/source5-lifecycle-gate-validation.json`，不随Git上传）实际 Go race 为1通过、0失败、0跳过，Python核验628份原文件；本记录消费固定0017候选的独立实验，不把旧0016控制测试、环境探针或离线检查直接记作当前候选的实际托管结果。

当前核心 SHA256 为 `5693d4ac2bda5fcae7e5ac037d23db1b2a48049f681b8c825b8b4db12ac1b967`。首轮生命周期校准使用独立 helper `53eef443f3a8911d7d2212605e4d18dba0b32b7cb1f61685ecb58c7e9e985341`；新增 controller 连接归属修复使用 `ad4a8069691e6668c658eb98c18c845bc3b2f5f903a08fe1db28560c2c92e13f`，其三模式固定核心校准已单独通过。两者均采用 legacy identity-only 配置，继续拒绝 `hoopa-program-rules` opt-in。程序规则控制路径由 T016 另验，旧 helper 的结果保留各自适用范围。

## 已完成的前置检查

| 前置 | 实际结果 | 适用边界 |
| --- | --- | --- |
| 当前核心控制 helper | 离线构建、vet、57顶层/206含子测试通过；独立审查与主代理接受完成 | 包含受控 backend 与真实 AF_UNIX 检查；没有启动核心。首轮 socket bind EPERM 和随后授权同输入复验分别保留 |
| guest S3 环境 | 一次隔离 VM 实际进入 deep S3 并恢复；QMP、内核日志与同一 boot ID 相互印证 | 零代理样本，无核心、BPF或采集器；不能替代代理跨休眠验收 |
| SSH 环境首场 | 一次 VM：交互式登录 shell 的 `logout` 返回23且 shell 消失；客户端被杀后同一远端 PID/start 收到 SIGHUP | 等待远端 PID 消失超时，整场不通过；新副本补精确进程观察/回收，不改原结果。零代理样本 |
| SSH 环境 v2 | 新 VM 两场景均通过并经独立复核：logout23；断连后同一远端进程被收养并回收，exit129 | 仅增加精确进程观察/回收；仍为 SSH/login shell 前提，零代理样本，不证明 PAM/logind 用户会话清理 |

helper 冻结、独立审查和主代理接受位于 `private/t015-control0017-helper/` 与 `private/t130-followup/control0017-helper-review.*`。S3 原运行位于 `private/t015-suspend-environment-vm/vm-s3-r2en6yaf/`，主代理受限接受为 `root-runtime-acceptance.json`；SSH 冻结位于 `private/t015-session-environment-vm/freeze.json`。私有实验制品不作为仓库内分发的二进制。

SSH 首场原运行 `vm-ssh-84cab352` 保留七份导出：正常登录 shell PID78/start363，client退出23；断连 remote PID85/start392收到SIGHUP1后，最终 `/proc/85` 仍存在而超时。由于没有记录超时点的进程状态，不能把它追认为已证明的 zombie。新副本仅补同一 owned PID/start 的状态观察与被 PID1 收养时的 `waitpid(WNOHANG)`，不通过 PID 数字猜测或杀进程。该场没有 S3，关机日志没有出现前一 S3 探针的 PID1 panic；这不足以解释 S3 后关机异常。

v2 原运行 `private/t015-session-environment-v2-vm/vm-ssh-f965qirm` 的七份导出完整匹配：remote PID85/start384 收到SIGHUP1，先记录 R/ppid1、再记录 Z/ppid1；WNOHANG先返回0，再返回准确PID85、status33024/exit129。其 `owned_instance_reaped=true` 有真实 wait 结果。该新场景验证了受限回收路径，不反推旧场末态。独立审查与接受记录分别为 `private/t130-followup/ssh-v2-runtime-review.*` 和该新目录的 `root-runtime-acceptance.json`。

## 必须保留的异常与语义边界

S3 的宿主 QMP 事件接收间隔为505870989ns，guest BOOTTIME/MONOTONIC分别只增加223076020/223069945ns。因此本次没有证明 guest 补时、宿主睡眠、调度恢复或订阅更新补跑。S3 返回后还发生 S5 power-down 后的 PID1 退出 panic；QEMU 虽退出并已回收，这不能称为干净 guest 关机。原运行保持不变，退出路径在后续新实验中单独处理。

保持 guest/image 字节不变的新2.5秒观测 `private/t015-suspend-clock-vm/vm-s3-clock-u0vvjlr7` 也实际进入并恢复 S3。宿主事件间隔2507390753ns，guest MONOTONIC增加171728618ns、BOOTTIME增加2314597563ns，两者差2142868945ns；计入了部分挂起时间，但不能承诺相对宿主的补时精度。持续收集的 QMP 尾事件为 `SHUTDOWN reason=guest-reset`，与内核 S5 后 PID1 panic 一致，原关机异常仍未解决。后续代理 S3 测试应先明确停止并验证核心/采集器、导出证据，再由宿主 QMP 有界结束专用 VM；这种实验收尾必须明确标为主动结束 VM，不算 guest 正常关机通过。

当前 `linuxControlSpawnProcess` 重定向标准流且不使用 parent-death signal，但没有显式创建独立 session。fork/exec 会继承 session；终端挂断及 session leader 退出可能产生 SIGHUP（[Linux setsid 文档](https://man7.org/linux/man-pages/man2/setsid.2.html)）。固定0017的受保护运行路径在进入 `running` 前注册并消费 SIGHUP，明确不执行热重载。因此，既不能仅凭缺少 `Setsid` 就断言核心必然退出，也不能凭管理器 `SIGKILL` 测试就宣称 SSH 断开安全。实际测试必须保留 SID、PGID、控制终端、信号、进程启动身份与原连接证据；启动窗口另行区分。

## 当前验收矩阵

| 范围 | 当前状态 | 尚需证明 |
| --- | --- | --- |
| 当前核心的管理器死亡与同核心接管 | 原helper两场四流通过；新helper另登记两场四流也通过，各场原长流41B及离线新流7B，均为每方向值 | 各自独立受限接受；无 SSH/采集器或完整缺失 claim 窗口推论 |
| F01 界面/调用进程退出 | 最小前台入口普通quit0后原流继续，独立一次VM验证通过 | 仅P0最小控制入口；产品CLI/TUI仍未实现 |
| 采集器退出和恢复 | 同一固定核心下的独立 receiver SIGKILL/同 journal 重开通过，一VM两流 | 来源原帧、接收原 receipt 去重分别核验；来源fsync、产品stale/gap展示、SQL及生产恢复仍另验 |
| F04 SSH 断开与登录 shell logout | 新helper真实SSH断开与正常交互shell logout分别通过，均保留原核心及41/7流量 | 两种触发及manager命运分记；PAM/logind用户会话清理另列 |
| F04 休眠恢复 | 当前核心及新helper跨guest deep S3另行回归通过，旧/新流及同核心接管保持 | 仅同guest端点；独立采集消费、宿主睡眠、外部peer超时、时钟精度与订阅更新补跑仍另验 |
| F14 PID复用、外部端口、记录缺失 | 三类有限场景均已有真实证据：外部controller实际accept1/recv0；另两VM完成真实PID复用拒绝及缺claim后原操作恢复 | 仅固定核心/新helper与已登记guest；F14私有原始归档含一次性guest token，导出保护不记通过 |
| G07 最小保护 | 当前固定候选两VM矩阵受限通过：7/7应用尝试、11/11管理探针、14/14文件探针 | 四个未授权数据尝试单列；原件无IPC token；不推导生产部署、source fsync或SQL |

基础测试不启用 GOST；没有以 controller 认证代替 GOST metrics 的独立认证。若后续启用兼容入口，必须增加对应独立场景。

两场当前核心的校准结果（本地原件 `../../experiments/mihomo-events/control-0017-lifecycle-calibration-validation.json`，不随Git上传）已单独接受：takeover 的原管理器被 SIGKILL，核心 PID89/start435 保持；spawn-window 原管理器在已获得启动 claim、尚未发布实例时退出86，核心 PID88/start426 保持。各场新管理器接管同一 generation/epoch，原长连接在前后继续，管理器离线期间另一个非特权原应用连接成功；没有重复启动核心。两场各26份导出、分别14与13个来源事件经原件校验和独立复核。来源检查保持 `recording_status=unverified`、`writer_end_evidence=null`，不把完整来源字节流当作 fsync 收据或 SQL 提交。QEMU均退出回收；这四条连接不追加到已经关闭的 G01 正式分母。

新增真实 SSH 接管校准（本地原件 `../../experiments/mihomo-events/control-0017-ssh-calibration-validation.json`，不随Git上传）登记一VM两流，完整检查及独立原件复核2/2通过。`vm-ssh-disconnect-takeover-mv6jz013` 中，SSH client PID84被SIGKILL；远端wrapper以PID88/start460、SID/PGID88及 `/dev/pts/0` exec为固定helper。该管理器实际被SIGHUP终止，由PID1按准确实例回收（wait status1，exit -1），与client的exit -9分别记录。核心PID97/start509及来源epoch保持；原流11→13→17累计41B、离线新流7B均为每方向值，新本地管理器接管后显式停止并回收核心。29份导出、14帧来源及完整终末保留，来源持久性口径仍为unverified；未启用receiver或SQL。此处只证明SSH传输断开，不扩展为PAM/logind注销。

新增核心跨 S3 校准（本地原件 `../../experiments/mihomo-events/control-0017-suspend-calibration-validation.json`，不随Git上传）在管理器退出后实际挂起并恢复guest。v3新登记一VM两流，完整检查和独立30份导出/14source帧复核2/2通过：核心PID89/start410、原boot/generation/epoch保持，原流41B、恢复后新流7B每方向值精确；接管和显式停止后的原核心回收完成。挂起前后原来源可见前缀各5帧/7235B，最终来源只追加并闭合。QMP SUSPEND→WAKEUP观测间隔2506756393ns；guest MONOTONIC增加228776514ns、BOOTTIME增加2324312825ns，仍不宣称时钟精度。证据完整导出后由host QMP quit结束VM，没有尝试guest OS关机，不覆盖旧S5/panic异常。

该S3系列的失败另存：v1因外部动态依赖缺运行前核验被预审拒绝，登记/VM0；v2已实际尝试一VM两流，但host仅接受LF而串口实际输出CRLF，等待导出超时，完整checker因缺失runner的S3结果而失败。另有userspace BEFORE标记被printk打断。v3仅修host协议解析和证据检查，保持guest/cpio字节相同，验证完整LF/CRLF封装与归档hash，并由QMP、kernel PM序列和原始前后快照共同证明S3。旧v2不重评，仍为一次失败尝试；没有用新2/2覆盖旧分母。

新增独立采集器恢复校准（本地原件 `../../experiments/mihomo-events/control-0017-collector-calibration-validation.json`，不随Git上传）已受限接受：v2一VM两应用槽，完整checker与33份导出独立复核2/2通过。固定0017由既有受保护入口直接启动，本场没有控制helper。core PID76/start451及同一boot/generation/epoch保持；receiver1 PID86/start526实际持久接收opened为receipt1后hold、不发成功ACK，再被精确SIGKILL并回收。它离线时旧流累计到24B，新流7B关闭，来源前缀增长而三份journal快照逐字节不变。receiver2 PID98/start638在同一journal目录/文件inode上重开，首个真实HTTP重投是同一原始opened事件，仍返回receipt1；最终14个原source事件对应14个唯一收据，13事件终末cutoff先于core停止。旧流最终41B、新流7B，均为每方向值；core与receiver2均正常退出回收，原epoch_closed complete也实际被接收。

该结果证明本次独立采集器故障未中断转发、原事件可补收且重复投递不重计。source只读扫描仍为recording_status=unverified、writer_end_evidence=null；可见来源前缀不等于fsync，接收journal收据不等于SQL事务，也未实现产品stale/gap界面。v1因启动前未核对run-local复制字节被独立预审拒绝，登记/VM0；v2只补host副本校验，重建guest/cpio字节与v1完全相同。旧件和拒绝记录保留，没有重评历史样本。

F14静态映射发现新的安全缺口：旧helper在核验claim、pidfd、PID/start、exe/config后，仍直接向配置的TCP controller地址发送Bearer，没有绑定这条连接的服务端socket与已核验核心。受保护核心会预留并检查端口，预先占用可使启动被拒绝；这不足以证明helper在启动、退出及端口释放窗口不会连到外部监听者。此项尚无实际泄露实验，不预判原核心会持续存活，也不拿先前正常端口场景证明安全。主代理已决定先修helper：连接后在发送任何应用字节前，核验原pidfd/启动实例、反向已建立TCP连接及该核心FD的inode归属，再在同一连接发送认证请求；只检查端口后重新连接不能消除竞态。核心制品保持固定，旧helper与既有结果保留，新helper须独立冻结并验证受影响路径，未完成前F14/G07不放行。

controller连接归属修复准备（本地原件 `../../experiments/mihomo-events/control-0017-controller-owner-validation.json`，不随Git上传）现已受限接受，普通[文本补丁](../../experiments/mihomo-events/control-0017-controller-owner.patch)可审查；补丁在原源码临时副本以fuzz=0实际应用，五份结果与编译overlay逐字相同。新helper为`ad4a8069691e6668c658eb98c18c845bc3b2f5f903a08fe1db28560c2c92e13f`（11408390B），保持legacy identity-only与旧控制语义，核心及旧helper未变。最终race71顶层/238含子项、0失败/0跳过，vet和离线构建通过；三个真实owned-child TCP场景观察到合法认证请求164B、错误owner零应用字节，以及监听端口替换后新listener零连接而原连接收到164B。pidfd/启动实例、反向已建立tuple和core FD inode在发送Bearer前核对，原1秒请求取消同时约束Go分离的dial上下文；缺证据关闭连接。

新增三模式固定核心校准（本地原件 `../../experiments/mihomo-events/control-0017-controller-owner-calibration-validation.json`，不随Git上传）已完成主代理完整检查、scout原件独立复核和单独接受：三VM、四应用槽4/4及一个独立控制负向探针1/1。takeover原核心PID90/start452、spawn-window原核心PID89/start488分别在原管理器exit -9/86后保持；两个新管理器接管原generation/epoch，各场旧流41B、新流7B精确闭合。两场分别14/13帧，均26份原始导出。这里的四流不新增G01正式样本。

foreign-controller实际占用者为PID74/start433，固定监听socket inode469；它接受了一条真实连接、收到0应用字节并观察到EOF，原进程/监听者保持，无信号或响应伪造。核心PID88/start492记录真实guard拒绝并消失，原pidfd可读，manager PID79仍存活；apply返回200但操作明确为rolled_back/rolled-back，active保持revision0/stopped，未发布运行实例。来源仅一帧complete epoch_closed、零flow，23份导出。核心由manager内部Wait回收，未导出精确退出码，因此仍记wait status unobserved，未伪造PID1 waitpid。本场实际出现了连接；若其他场景accept为0，只能记录启动冲突，不能冒充同连接检查分支执行。

三场QEMU均exit0并回收、宿主/proc消失；75份导出逐项长度/SHA核验通过。来源只读结果继续保持recording_status=unverified和writer_end_evidence=null，没有source fsync或SQL声明。该新helper结果不自动移植旧helper的SSH/S3验收，也不完成F01、核心正常logout、core PID复用/缺claim或G07。当前身份验证不宣称抵抗特权ptrace或可信core恶意转交FD。G07随后使用新helper，HTTP missing Basic应为407、wrong Basic为403，按固定源码分别验证；真实UID65534的文件权限拒绝与临时隔离出的SO_PEERCRED拒绝分记，不相互替代。

实际运行由主代理登记并串行启动，原件与稳定实现由 scout（配置 gpt-6-luna/high）独立复核；executor（配置 gpt-6.1-sol/medium）承担指定文件的实现和检查。接受决策与原运行分开保存，失败不通过改写元数据重新评分。完成本记录不等于完成 T015。

新增F14 两场实际验证（本地原件 `../../experiments/mihomo-events/control-0017-f14-calibration-validation.json`，不随Git上传）已完成完整检查、52份原始导出的独立复核与主代理受限接受。缺claim场景的原核心PID90/start467保持转发，status/stop返回503 UNKNOWN，原stop进入degraded；恢复同一claim后，以同一操作ID及expected_revision1恢复，提交stopped revision2。两条原应用流分别41/7B每方向，14帧完整来源；核心退出有原pidfd可读和proc消失证据，数值wait仍记unobserved。

PID复用场景先精确回收原核心PID91/start486，再一次分配出PID91/start685的自有Python子进程。新管理器status/stop均503 UNKNOWN，原active revision1及原claim/intent保持不变；子进程在请求前后均可响应，已安装HUP/INT/TERM/USR1/USR2的信号账本为空，最后通过自身stdin正常退出。该场原7B流及七帧来源闭合；不能把有限信号账本称为完整信号审计。两场共3/3应用槽、2/2控制负向探针，QEMU均exit0并回收，不增加G01正式分母。

F14 v1在启动前因把动态proc stat整对象当固定身份比较而被拒绝，登记/VM均0。v2改为稳定boot/PID/start/exe身份比较，并独立保留原active字节；旧件不改。独立运行复核还发现两份F14原归档包含已结束隔离guest的IPC token：原件保留在被Git忽略的private目录，公开记录只引用路径/长度/hash，不公开明文，也不宣称凭据导出保护通过。G07及后续新包单独排除token并检查；此项限制与已观察的拒绝、恢复行为分别记录。来源持久性仍为unverified，无SQL或生产托管结论。

新增G07 最小访问矩阵（本地原件 `../../experiments/mihomo-events/control-0017-g07-calibration-validation.json`，不随Git上传）已通过完整checker、74份原始导出的独立复核与主代理接受。管理场景使用新helper与core87/start447：IPC/controller各自验证正确、缺失、错误、重复凭据；真实UID65534/空groups/零capability/NNP1进程在原私有权限下读取token和连接IPC均EACCES。另临时开放目录0711/socket0602，验证即使提供正确token，该UID仍因peer credential被401拒绝；随后恢复原0700/0600及完整库存。两种拒绝机制分别计数。

六个数据尝试各有独立原PID/start和实际协议结果：HTTP合法、SOCKS合法分别7B每方向并产生13帧来源；HTTP缺Basic407、错误Basic403，以及SOCKS无可接受方法/错误密码四次均无对应source flow或上游accept。认证前identity.Admit可能发生，不把它当作认证后已转发。

文件场景直接使用同一固定受保护core74/start404与正常receiver，先完成一条7B流和原closed收据，再停止两个writer。来源与journal各有一次root授权只读重开，以及UID65534的read/append/truncate/rename/unlink/create六次拒绝；12次拒绝均EACCES，逐次目录成员、inode、权限、长度和hash保持。最终七帧来源对应七个唯一收据；root read重开不是receiver恢复，也不证明source fsync或SQL事务。两份归档均排除IPC token，实际两QEMU均exit0并回收。该结果关闭本候选有限最小保护矩阵，完整T015及生产保护仍按各自范围验收。

新增前端普通退出与登录shell注销（本地原件 `../../experiments/mihomo-events/control-0017-logout-calibration-validation.json`，不随Git上传）一VM两槽已完成完整checker、46份导出独立复核及root受限接受。真实无remote-command的`ssh -tt`进入交互bash，实际monitor on、huponexit off，夹具不改变这些设置。后台管理器PID89/start506保留SID87/PGID89，前台最小入口PID111/start691先观察原核心verified-running，再通过普通quit返回0；原连接在入口退出后继续。随后shell PID87/start489执行logout23，SSH client退出23且原shell消失；shell数值wait status仍未直接观察。

核心PID98/start550及原boot/generation/epoch跨两个触发保持，原连接11→24→41B、新连接7B每方向闭合，共13帧完整来源。后台管理器在logout及上述流量后实际仍live；随后夹具才以原pidfd独立发送TERM，PID1准确wait0，再由本地管理器接管并显式停止/回收原核心。该TERM不归因于logout，也不将此前流量称为管理器离线转发。QEMU3943006退出0并回收；token明文不导出、前后元数据/hash不变。来源可见前缀仍不等于fsync，未测试SQL、产品CLI/TUI、PAM/logind或宿主注销。

新增新helper SSH断开回归（本地原件 `../../experiments/mihomo-events/control-0017-ssh-newhelper-calibration-validation.json`，不随Git上传）一VM两槽2/2独立通过。SSH client82被SIGKILL并回收，remote helper86/start426在sshd退出后被PID1收养，实际wait status1/exit -1证明SIGHUP终止；夹具未向remote补发替代终止信号。core95/start468与原世代保持，管理器缺席期间旧连接继续13B、新连接完成7B，接管后旧流终末41B，14帧来源完整；最后核心显式stop/reap0。29份导出核验，QEMU3981628退出0并回收。这是新helper自己的实际结果，不移植旧helper通过记录，也不等同正常logout。

新增新helper S3回归（本地原件 `../../experiments/mihomo-events/control-0017-suspend-newhelper-calibration-validation.json`，不随Git上传）一VM两槽2/2独立通过。原管理器exit -9后guest实际进入deep S3并被QMP唤醒，core88/start433与原boot/generation/epoch保持；旧41B、新7B每方向闭合，14帧来源与30份导出核验。挂起前后原五帧/7233B/hash相同，完整来源只追加至终末。host SUSPEND→WAKEUP间隔2508252900ns，guest MONOTONIC/BOOTTIME分别增加210000163/2094699974ns，仍不宣称时钟精度。显式核心/管理器清理及导出完成后，host QMP quit结束VM，guest OS shutdown未测试；QEMU3985747退出0并回收。两项回归均排除token明文并保留元数据/hash，旧S3异常与失败不重评。

统一验收输入汇总11个独立VM、22个应用尝试（18个转发流及4个正确拒绝）、14个控制探针、14个文件探针、339份导出和124帧来源；实际case均已独审接受，最终只读入口也已完成。补充范围核查发现，最初新helper的takeover/spawn/foreign三个私有归档也沿用了包含token的旧导出逻辑；与F14两场共五份旧原件保留同一卫生限制。它们均是已结束隔离guest的测试凭据，公开只含摘要；不宣称这五场通过凭据导出保护。G07、logout和新SSH/S3的排除证据分别成立，不删除旧原件或抹去限制。

## 最终只读入口与任务完成

稳定实现及限定修订已独立复核；最终Python3.8合成图4方法、44负例通过。实际入口显式提供验收manifest及SHA，Go race为1通过、0失败、0跳过，Python同一最终代码核验628份文件。默认未提供输入只报告unverified skip，单独提供路径或SHA会失败，均不算实际验收通过。本入口只读原件，不运行VM、代理、collector或原场景checker，不增加运行分母。

首个实际Go入口因旧摘要格式假设失败，记录及后续诊断保留。修订按原格式读取：最早三个owner摘要内嵌完整canonical host结果，独立host/root接受仍由完整机器引用核验；collector独审中的散文citation没有历史hash声明，其完整JSON被固定，实际场景材料另有机器引用。旧client缺独立slot字段时，使用冻结顺序、不同终末字节数及原身份/cookie关联；交换顺序、错host、重复run、漏root引用均有拒绝负例。未给旧原件补字段或改评分。

新保护场景对每份导出扫描原样64位小写hex token，以候选加LF重建存储摘要，覆盖异名文件及嵌入记录；不要求导出内token后有LF。扫描不输出候选内容，限原每案16MiB清单，不宣称编码形式的完整秘密审计。旧五份私有归档的卫生限制保持。

主代理据上述结果完成T015，任务总数140、完成32。T016路由/完整回滚、T017来源整体评审与T018放行仍待各自完成；受限Linux guest证据不等于生产托管、跨平台、source fsync或SQL账本验收。
