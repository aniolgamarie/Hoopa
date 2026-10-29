# ADR 013：产品运行身份、登记与启动门控

日期：2026-10-27。状态：T025 模块已有限验收，T026 实现决定尚未实际验收。

## 原型到产品的差异

原 P0 helper 的配置 generation 与随机启动 nonce 绑定；产品需要在自动重启时
保留已提交配置 generation，并产生独立的进程 RunID。原 launcher 的 exec 前 claim
也不等于核验实际可执行文件和 controller 归属后的运行登记。

另一个实际缺口是：当前固定核心 `5e934bdc…11a388` 在 controller 开始服务之前
就启动应用入口。它有全端口绑定检查、认证和逐连接身份准入，但没有“管理器完成
当前操作登记及运行核验后才开放转发”的启动屏障。不能通过更名为“凭据尚未交付”
满足原要求，也不能把测试中的预期事件当成实际核心已经产生的事件。

## 运行身份决定

- 配置 Generation 绑定不可变 bundle；RunID 与随机 launch nonce 每次新 spawn
  生成，恢复接管同一进程则保持。来源 CounterEpoch 由来源自身运行身份和随机量
  产生；每入口 capture_generation 的既有 wire 派生值不是授权凭据，也不替代 RunID。
- launch intent 在 spawn 前持久化，包含原操作、完整 bundle、固定资产与工作目录。
  exec 前 claim 只证明出生链。actual PID/start/boot、exe path/hash、cwd、nonce 及
  同一已连接 controller 的 owner/status 全部核验后，才持久化 registration。
- Unix `SO_PEERCRED` 验证 CLI 与 manager 的本地账户关系；它不是 Mihomo 的身份
  证明。manager→core 仍在同一 TCP 连接先核验 reverse tuple 的唯一 inode 与目标
  进程 FD 相交，并在原 pidfd 前后核验进程实例，成功后才发送 controller 凭据。
- Linux 进程平台层返回持有原 pidfd 的句柄和只读观察。序列化 PID/身份不是信号
  许可；协调器先持久 stopped 意图、重新证明核心归属，再在原 pidfd 上发信号。
  不回退数字 PID kill，信息不足返回 unverified/conflict，不推导“没有进程”。
- 新 child 由启动它的 manager 独立 Wait；接管的非 child 只能证明 pidfd 退出，不能
  声称已经由该 manager reap。句柄关闭及 manager context 取消都不杀核心。
  显式启动采用独立 session、文件标准流，无 CommandContext/Pdeathsig；实际注销和
  休眠行为仍须 T034 验证，不能只据 Setsid 推导结果。
- state 的持久写锁是唯一写入者权威；持锁后才能绑定自己的管理 socket。关闭服务
  先停止接纳并清理本次 socket，结束写任务，最后释放写锁；不连带停止核心。
  同 UID 恶意替换、exec 或挂载不在本层隔离范围。Unix cleanup 在删除前核对原 inode，
  但不宣称 Linux 提供 compare-and-unlink 原子操作。

## T026 启动门控决定

产品必须选择声明并实际验证 closed-start/activate 能力的固定核心。当前核心能力
不足，不能作为满足此流程的产品候选。T026 在单独候选中增加最小受保护启动门控：
控制入口可观察，全部应用转发保持关闭；登记及完整运行校验后，通过认证的控制
操作开放全部入口。未授权、错误 generation/config、缺能力、部分开放失败及管理器
在开放前后崩溃必须有明确状态，不静默降级为立即开放或 stock controller。

这项变化继续使用 Mihomo 的转发内核，不增加第二层代理或 GOST 依赖。实现前固定
[控制 v1 契约](../../specs/001-hoopa-proxy-manager/contracts/core-control-v1.md)与幂等/失败语义；新候选重新冻结源码、补丁、二进制及能力记录。
G04/G05/G07 的新路径必须实际验证，身份/来源/质量的受影响范围按真实差异裁决。
旧核心的 100/100、恢复/质量矩阵及当前多入口结果保持原 hash 和范围；T018 的有限
工程入口不充当新门控通过记录。T026 验收后更新 P1 固定 fixture，T034 执行产品矩阵。

## 基础模式与完整身份模式

当前 core 源码存在不启用 tracing/identity owner 的受保护应用路由分支；当前 P0
control adapter v2 则固定走 BPF/bootstrap。两者不能混为默认产品启动方式。
T026 的基础管理先使用无 BPF、无计量的受保护模式，仍须具备 controller 认证、
命名应用入口和真实 closed-start/activate；不回退未受管理的 stock 启动路径。
依赖原始 PID、进程名或路径证明的规则在此模式明确缺能力而拒绝，应用上下文
路由继续可用，不把入口所属应用当作真实源进程。

完整身份模式另行准备固定 Python/bootstrap、BPF/libbpf、scope 和 FD handoff
闭包，保留已有实验路径的特权/环境限制并单独验收。产品 launcher 在最外层、
任何 exec 之前持久出生记录，关闭继承的出生目录 FD，然后保持同 PID 进入
对应模式的已核验启动链。不能把直接 exec 实验核心当作已经完成 BPF 建立，
也不把 trusted-root guest 变成基础 CLI 默认部署前提。两种模式的能力摘要和
实测范围分列；一个模式的通过不授予另一个模式的身份/计量能力。

## 当前证据

本决定来自运行身份接线准备（本地原件 `../../private/p1-ownership-preparation/report.md`，不随Git上传）与
核心启动门槛证据图（本地原件 `../../private/p1-core-gate-preparation/findings.md`，不随Git上传），均是只读材料。
基础模式选择另据受保护模式与实验启动链的源码核对（本地原件 `../../private/p1-core-gate-preparation/basic-mode.md`，不随Git上传）。
[T025 运行归属模块](../validation/p1-runtime-ownership.md)已有限验收，包括持久记录与
测试进程的真实出生/exec/同连接核验接线；真实 Mihomo 产品接管、退出
和新门控仍未通过，没有修改或重评已冻结的原型制品。

## 实施责任补充：基础输入与完整身份产品接线

2026-10-27核对发现，早期P1 reference固定的是含进程规则的旧完整身份bundle，
与本ADR后来确定的无BPF基础启动路径不一致。P1生命周期改用独立实测的完整basic
双bundle reference；原identity fixture保持原hash和全部规则，不能通过删规则迁移。
这是基础生命周期输入的改版，不是重评T022/T023历史定义或削减P2身份需求。

T138负责将完整身份/来源当前核心补上同等门控/控制契约，冻结其bootstrap、BPF、
libbpf与scope/FD handoff及必要复验；T077负责产品最外层birth→同PID启动→实际
owner/登记→activate及失败/退出/接管测试。T091再使用该能力接进程规则。
不新增循环前置，不让基础P1依赖BPF，不把T136/T137/T140已完成的实验任务冒充
这些产品接线已经完成。此责任仍未实施、未验证。
