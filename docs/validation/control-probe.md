# T136 最小控制原型验证

2026-10-25。T136最小控制原型已按[0016五场实际矩阵](control-rebind.md)完成有限验收：回滚、完整替换、身份拒绝及两种同核心接管均通过。下文保留0015阶段的实现与运行记录，旧失败不改判。本范围不放行P0/P1或正式G01，真实SSH/注销/休眠仍归T015。

## 实现与边界

[控制状态机](../../experiments/mihomo-events/control_probe.go)保存不可变 bundle、操作和启动意图；先持久化 nonce/意图，再启动 helper。helper 在同 PID exec 核心前持久化 claim。重新打开控制实例只读取状态，显式 Recover 才继续未完成操作；关闭管理器不停止核心。身份核验结合 boot ID、PID/start ticks、nonce、generation、实际 executable 摘要、参数/环境、工作目录和核心认证状态；停止通过核验后的 pidfd 执行。

[持久目录](../../experiments/mihomo-events/control_store_linux.go)采用单 writer 锁和逐文件 Sync/rename/目录 Sync；结果不确定时拒绝继续写入，不能把它描述为跨文件数据库事务。重开核验 pending 与 active 的关系；只扫描控制材料，不按控制 JSON 的大小预算递归读取核心来源账本。无 claim 或身份不明时不自动补启动，不猜测目标进程。

[IPC](../../experiments/mihomo-events/control_ipc_linux.go)只监听私有 0700 目录中的 0600 Unix socket，使用同 UID peer 检查及独立随机凭据。请求严格限制字段、大小和版本，同 ID 查询先于 revision 冲突；响应不返回 bundle、凭据或底层错误详情。Status 的 observation 只描述 active 记录，pending 可能另有尚未发布的存活核心，不能据此断言所有核心已停止。

实验入口是 [controlprobe](../../experiments/mihomo-events/cmd/controlprobe/main.go)，不是产品 `hoopa` CLI。真实核心启动/停止仅允许固定隔离 guest；宿主拒绝入口已实际验证。二进制与镜像留在忽略的 `private/`，仓库提供源码与摘要。控制 helper 为 11,380,031 B，SHA256 `6574418f4d08ebabd5812ed7b578d14a1a3004eee3dff0a8aeb7c109de86a680`；使用 [T137 固定 0015 核心](../../experiments/mihomo-events/candidates/1ef1b53ae41435596844c264ae5e1e5201ce95b5d2762be872371023f4d6af25.json)。本次不改变该候选的身份能力范围。

## 已执行检查

冻结 helper 的受影响 Control 测试以 race 运行：56 个顶层、含子项 200 个通过，个别测试 0 fail/skip；两个包 vet 通过。cmd 包无测试，仅编译，未计为测试通过。单元层使用假进程后端，实际本地 Unix socket 与真实 VM 证据分别登记；不把假后端的故障矩阵算作真实核心行为。

两次成功 VM 均为固定 Linux amd64、2 CPU/1 GiB、无网卡/宿主共享，最长 150 秒。应用在 connect 前已降到 UID/GID 65534、清空 capabilities 并开启 NNP；保留原进程及来源事件对照。镜像 SHA256 为 `16e37453ecd80d22278d38da1ecb0104d2cd1408263950f55225a8a7ea361cd8`。

| 场景 | 真实观察 | 来源核对 |
| --- | --- | --- |
| `vm-takeover-ovtssyqd` | 原管理器 SIGKILL/-9；旧连接继续传输，离线期间新连接成功；新管理器接管原核心 PID 88/start 441，generation/epoch 不变 | 两流每方向 41 B、7 B；14 帧、26 导出；完整终末，无 gap |
| `vm-spawn-window-h9dkeb3b` | 原管理器在成功启动、父记录保存前退出 86；active 仍为 stopped/rev0，操作尚无 instance，helper claim 已持久化；显式 Recover 发布原核心 PID 88/start 485 为 rev1 | 两流每方向 41 B、7 B；13 帧、26 导出；完整终末，无 gap |

两场 VM、规范提取器和严格 checker 均退出 0；总计 4 流、27 帧、52 导出。最后显式停止到 rev2，孤儿核心由 guest PID1 实际 wait/reap、退出 0。原管理器、新管理器是不同启动实例，应用原始 PID/start/cookie 与 source5 opened 一一核对；不是把启动器身份填入原应用。Node 回显、应用读写和来源整数终末一致。

实际错误 token 查询为 401，认证只读查询为 200，同 ID 重试为 200，旧 revision 的新请求为 409；相应控制文件摘要/mtime 前后相同。同 UID peer 路径为真实执行，跨 UID peer 拒绝目前仅有注入式单元证据，不能扩大为真实跨 UID IPC 验收。

## 保留失败与当前缺项

首个 `vm-takeover-tw4pa3c1` 的 VM/提取器退出 0，但严格 checker 为 1：核心已启动，首条应用流在请求头后遇到 EOF，节点夹具的字面请求行断言拒绝了 10 次尝试。原失败请求行未导出；“hosts 解析到 127.0.0.1 与要求字面域名不相容”是源码支持的推断，不写成原始观测。唯一流终末为 0/0、来源 epoch 尚未结束，不能算接管通过。

新 v2 夹具只接受登记域名或其声明的 hosts 地址，保存原请求行，并增加正反校验；实际成功场景记录到 `CONNECT 127.0.0.1:443 HTTP/1.1`。核心、helper 和 BPF 未修改；旧镜像、运行与失败分析完整保留，成功记录另建。

新增三场结果见[替换与身份拒绝验收](control-replace.md)及机器记录（本地原件 `../../experiments/mihomo-events/control-replace-validation.json`，不随Git上传）：完整替换、身份冲突拒绝两场各自 VM/提取/checker 均为 0，共 3 流、21 帧、66 导出；低 UID 在 0700 实例目录得到 EACCES，仅证明文件权限拒绝。rollback 场景 VM/提取为 0、checker 为 1，目标与恢复实例均输出通用受保护配置拒绝，HTTP 503、操作 degraded；保留的 9 帧单独记录，不计作回滚成功。预置端口冲突是夹具条件，原运行未采 bind errno，实际原因仍待独立诊断。缺 claim 的恢复拒绝目前只有定向测试。真实 SSH/注销/休眠归 T015；生产托管/权限部署、其他内核和平台、完整性能预算未验证。控制原型不迁移跨世代的应用长连接，也未实现完整 CLI、历史 SQL 或热重载。

主代理负责持久化/恢复/身份语义、实际 VM 与验收；executor（gpt-6.1-sol/medium）实现已确定模块和独立夹具，scout（gpt-6-luna/high）只读复核稳定源码及原始证据，未再派生。此页0015阶段记录为 139 项、完成 23 项；后续0016验收完成后为139项、24项；正式 G01 新样本为 0，原 0/100 保留。
