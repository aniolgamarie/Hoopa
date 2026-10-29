# 管理服务生命周期与查询 v1

2026-10-27，T033 接线契约。仅 Linux/amd64；基本核心不依赖 GOST、SQLite 或 BPF。
完整生命周期故障矩阵属于 T034，不能由本文件或单一往返代替。

## 启动与服务发现

Store 的实际排他 writer lock 是管理权来源。持锁后生成 24 字节随机实例，创建
`svc-<48hex>.sock`，将版本、实例、socket 文件名和 PID 发布到私有 `service.json`。
描述符权限 0600，使用独占临时文件、文件同步、rename、目录同步；目录/socket 与实际
SO_PEERCRED 仍由平台层校验。PID 仅参与连接验证，不能授权认领或终止核心。

认证连接先交换 `HPSVC001` 与实例的 24 字节原始值，双方核对后才进入
[有界帧协议](service-transport-v1.md)。客户端还需复核发现记录未被替换。
每次只交换一个请求；超时或丢失响应后不自动生成新请求身份。

新 writer 可发布自己的新描述符，不清理旧 socket。最多容纳 128 个服务端点名字，
扫描目录最多 4096 项，超限失败；关闭仅移除自己创建且 inode/描述符仍匹配的资源。
状态查询不清理残留。发现损坏返回完整性错误，不能因连接失败就删除旧文件。

`service run` 前台持有管理权，不启动或恢复核心。`start --control-only` 可显式启动后台
服务；后台子进程使用固定当前程序、新 session、私有日志、最小环境和 FD3 就绪管道，
4 KiB / 10 秒上限。就绪只证明 writer 和端点已就绪。前端退出不终止已就绪服务。
未就绪失败时只终止本次自己新建的直接子进程；不按发现 PID 发信号。

普通 `start` 要求已提交快照：已证明停止则从原 bundle 启动，已证明运行则执行
[持久接管](managed-start-v1.md)，未知或未终结操作需显式恢复。无快照不能启动核心。
机器 `runtime.start` 必须有已有 Store 的精确 revision，不能凭猜测创建新状态。
冷启动初始化仅由人类显式 control-only 路径执行。

`runtime.start(control_only=true)` 是服务就绪观察，不写核心操作日志，返回当前
instance/revision/control_only；不会发放重启预算。它不承诺跨实例持久重放同一观察。
所有已接纳的持久操作优先精确重放，跨服务也不能把旧请求变为新动作。

服务尚无可信通用编译器；所有编译/材料依赖明确拒绝。不得用 fixture 编译器交付
config.plan/apply 空成功。测试通过独立固定 fixture 预先生成已提交快照。

## 服务停止和中断恢复

`service.stop` 在同一控制锁下先保存包含原服务实例的 prepared 与停止意图，再关闭
接纳入口、取消后台任务，最后持久 committed。发起该请求的已接纳连接和它的有界
上下文保留用于发送原始收据；释放控制锁之后才关闭 watch、等待 worker、释放 writer。
正常停止不发信号给核心，不改变 desired_runtime、revision 或核心原件。

committed 表示**原服务已经关闭请求接纳入口**，不是声称当前 OS 进程已退出。
响应带 `SERVICE_ADMISSION_CLOSED` 警告，明确核心未停止、采集不可用、OS 退出未确认。
外部信号或客户端断连仍可能使响应无法交付；操作的持久结果保留供精确重放。

旧 stop 在新服务中只返回原收据，不关闭新服务。离线重放只安全读取现有记录。
中断 stop 的显式 `operation.recover` 必须在新的实际 writer 和不同服务实例下进行；
这证明原服务失去写入权，不推断核心或旧 OS 进程已死。恢复不关闭新服务，也不处理
其他未完成核心操作。缺少原实例的旧/损坏证据保持 degraded，不能补造事实。
为保留上述证明，service.stop 及指向它的恢复操作不压缩正文。

## 单次重启授权

`service run --restart-once` 只为本服务随后首次新接纳并 committed 的普通 runtime.start
授权一次原 pidfd 退出观察及最多一次新核心启动。control-only、查询、请求重放、恢复、
服务打开均不发放预算。原始 wait 不持控制锁；执行前再次核对原 active 与未完成记录。
后继退出仍可观测，但不再启动。服务重启不恢复预算；停止管理服务取消 worker，
关闭句柄而不终止核心。status 的 restart_policy 披露当前授权/等待/已用预算状态。

## 只读状态与历史操作

数据形状由 [service-lifecycle-v1.schema.json](service-lifecycle-v1.schema.json) 定义，
同时满足 [API 响应信封](api-v1.schema.json)。此处只交付 status.get、operation.get 和
control-only 的业务数据；其他操作不因有信封就被宣称已实现。

status 返回本次 observed_at、committed/desired runtime、service/core/collector 状态、
降级原因和实现能力。它不会获取 writer、更新观察时间文件、恢复或修复状态，也不
测速/探测外部目标；在线完整核心证明只读本机控制接口。任何未完成核心操作都阻止
把核心显示为 verified-running。读取前后 active 变化则拒绝，整个返回仍是有界观察，
不是跨进程的事务快照，也不保证之后进程存活。

离线可读取既有 revision、历史与停止证据；运行态完整激活证明依赖管理 writer，
缺少此能力显示 core=unknown / activation_requires_management，而不为查询打开 writer。
观察不到进程不等于它已停止。采集器当前始终 unavailable/not_implemented，不造零流量。
能力列表表示本二进制实现的操作，service=unavailable 时变更仍不可用。

pending 按存储稳定顺序最多返回 32 条，并提供 pending_total / pending_truncated；
不冒充完整列表，不悄悄截断字节。operation.get 可按已知 ID 查看具体记录。
operation.get 的 recovery 只含原记录 SHA256、证据摘要、步数、是否压缩、是否需要
显式恢复，以及恢复调用绑定的目标 ID；这些是历史索引，不是新鲜核心证明。
只投影有限错误代码，不输出原请求、路径、凭据或任意 evidence 正文。
