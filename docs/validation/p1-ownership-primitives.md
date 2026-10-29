# P1 进程归属平台原语有限验收

2026-10-27。状态：平台原语及纯记录类型已通过限定检查，**T025 未完成**。
机器摘要：p1-ownership-primitives-validation.json（本地原件 `../../experiments/gost-mihomo/p1-ownership-primitives-validation.json`，不随Git上传）。
当前没有产品核心启动或 VM 样本，不把本记录当作 T034 生命周期通过。

## 已实现和验证

| 范围 | 实测与边界 |
|------|------------|
| Linux/amd64 原 pidfd 身份 | 真实测试子进程的 boot/PID/start、exe path/hash、cwd、nonce，原 pidfd 的 fdinfo 绑定；句柄关闭不杀进程，信号只发原句柄，退出与 child Wait 分开 |
| 同连接 controller 归属 | 只允许规范 IPv4 loopback；先连接，再将反向 TCP tuple 的唯一 ESTABLISHED 非零 inode 与目标 FD 相交，完整身份前后复核，成功返回原连接；核验本身不写应用字节 |
| Unix IPC | 私有目录 FD、0600 socket、双方实际 SO_PEERCRED；并发 Accept 串行管理 deadline，排队可取消，Close 唤醒等待且不关闭已交出的连接 |
| 完整 argv 与出生观察 | 原句柄身份前后复核之间比较完整 cmdline 含终结 NUL；self boot/PID/start 是 exec 前提示，不代替实际核心身份 |
| 三种运行记录形状 | intent/birth/registration 的范围、精确 uint64 字符串和完整绑定；恢复 operation 可不同于出生 operation |

对应 race 记录分别为：进程 6 个有效顶层＋1 个空父 helper／5 子测试；
controller 5 个有效顶层＋1 个空父 helper／11 子测试；IPC 12／28；
argv/出生 5／10；记录类型 6／83。均为 0 失败、0 跳过。
IPC、argv/出生及记录类型相应包 vet 通过。空 helper 仅供显式子进程入口，
不单独计作实际能力覆盖，子测试数也不当作独立核心运行数。

controller 正向测试实际连接同一服务器子进程两次；错误进程测试使用同一 netns
中的两个不同子进程，对端观察 READ 0，证明不能仅凭 namespace TCP 表认领。
这不是 HTTP 认证/status/完整路由配置的测试。IPC 的其他 UID 拒绝只有合成策略
用例，没有在本轮实际切换 UID；相同 UID 恶意替换/FD 转交按 ADR013 排除。

## 保留的失败与剩余工作

进程最初的 /proc/self 数字检查后来补上原 pidfd fdinfo 对目标路径 PID 的绑定；
旧测试 pins 保留，新检查有定向实测，不声称跑过真实 namespace PID 碰撞。
IPC 最初 fixture 的 0755 不符合私有目录要求，修正 fixture 后沙箱仍遇到原生 socket
EPERM；宿主普通 UID 的限定测试通过。另一个过长替换 socket 路径仅修测试绑定，
没有削弱生产权限检查。并发 Accept 的审查问题已修复并重新实测。

后续两个子包的 runner 前置兼容错误、argv 测试的初次编译失败也保留；不修改旧
退出码，也不把没有执行到 Go/核心的尝试计作运行通过。

T025 正在接入持久 intent、launcher 出生发布、实际进程与同连接核心核验之后的
registration。T026 还须实现认证、完整配置检查及新候选的 closed-start/activate；
现有固定核心仍不具备这项启动能力。实际管理器退出/接管、会话退出、恢复矩阵与
跨平台验证均保持待验。

root 负责身份、锁、发布顺序及最终范围裁决；executor 实际使用
gpt-6.1-sol/medium 实现 IPC、记录类型及 argv/出生补件，scout 使用
gpt-6-luna/high 核对稳定源码与记录，均未再次派生子代理。
