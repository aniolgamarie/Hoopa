# 普通 start 对已运行实例的接管 v1

2026-10-27，T033 控制层增量。此分支由显式普通 start 调用；打开管理服务、
control-only、status/doctor 均不得隐式调用它。服务和 CLI 的实际接线另验。

`BasicApplier.EnsureStarted` 接收现有 `runtime.start` 请求，参数仍为
expected_revision 与 control_only=false。相同已接纳身份先返回原持久结果，
不同内容复用 ID 拒绝；然后才检查当前 context、参数、revision、历史与容量。

| 当前状态 | 行为 |
| --- | --- |
| 已证明的 committed stop | 释放本次只读检查的控制锁，再调用已有 Start；Start 在同一锁上重新完成全部重放、revision 和证明检查，不跨解锁传递启动许可 |
| 已提交 running，原实例完整可证 | 在控制锁内写入 adoption 操作，保持原 core RunID、出生/注册、active、revision、generation、目录与配置 |
| 空快照、pending/degraded、孤立运行来源或身份不明 | 明确拒绝，不能凭进程未找到而重新启动 |

adoption 在接纳之前要求唯一原运行及新鲜完整 ownership/registration/activation
证明。接纳后保存独立 evidence：version=1、kind=adopt-running、原 base、RunID、
完整 ProcessIdentity 和阶段。它的 Intent.TargetRevision 为 null；base 的原发布
操作才是核心创建/发布身份，不能把 adoption 操作当成新的运行来源。

该操作经过 prepared → validated → applying → verifying → publishing → committed，
但没有核心写操作或 active 发布。执行入口、publishing 前、committed 前分别核对
active 全指针、唯一来源原件和同一完整进程身份。持久 Result 使用既有变更收据；
committed 只证明本次已核验并接管当前原实例，不保证它之后不会退出。

显式 operation.recover 先持久本次独立恢复调用，再识别 adoption 的严格 evidence，
重新核验同一原运行后推进原目标。它不 spawn、重登记、activate、写 active 或授予
重启预算。若原实例已经退出或变为不可证，保持 recovery required/degraded。

在 journal 接纳 prepared 后、adoption evidence 尚未持久的窗口，磁盘只有 `{}`：
不能猜测该 runtime.start 本来打算接管还是新启动。现有保守恢复分支保留原 evidence，
将原目标及恢复调用分别标为 degraded；不会重建启动票据或制造 adoption 身份。

服务层必须直接重放已持久的精确 Result；旧请求在后续 revision 已前进后也只返回
原结果，不能变成新的启动动作。接管成功本身不授予自动重启预算；需要额外显式
授权的服务范围才可调用 T032 的 AuthorizeRestart。
