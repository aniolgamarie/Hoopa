# 组合身份的并发、PID 复用与接纳前早退

2026-10-24，按[预登记契约](../../specs/001-hoopa-proxy-manager/contracts/connector-executable-lifetimes-v1.md)完成三个独立隔离 VM。每场 VM、证据提取、严格校验均退出 0；共六条流、39 帧、77 份原始导出。固定 001–013 核心、executable-online BPF 和 6.18.54.1/amd64 内核，不增加正式 G01 样本，不提升身份能力。

机器记录（本地原件 `../../experiments/sourceidentity/executable-lifetimes-validation.json`，不随Git上传）包含各流原始 PID/TID、启动身份、cookie、exe 原记录摘要、两线路、资源样本与私有证据路径/摘要。镜像 v2 为 41,988,368 字节，SHA256 `7cb4271383cb225127e3920a522ed328b17333d77f4f41a9418b3afb8ef39b34`；核心 SHA256 `476d57884b810e96d8b44367afbf91001bc63b6b516930a66b89d6e8dc05467b`，BPF SHA256 `1bf999df3affd167b06c923b7dde3ef27ec521e7e0afaf2accb3f447a6059500`。162 项离线准备检查与真实运行分别记录。

## 原始进程与后续使用者

| 场景 | 原 connector / writer | 实际成立的顺序 | HTTP / SOCKS5 实际线路及 ready |
| --- | --- | --- | --- |
| 同名父子并发 | 父 85/85、子 86/86；各自建连和写入 | 两条连接的 ready 均不晚于 source_seq 9，第一 closed 为 11；两条流重叠 | node:a / 16 ms；node:b / 31 ms |
| PID 复用 | 两次均为 PID/TID 30000，各自写入 | start ticks 452→481；第一进程回收后 26,353,967 ns 才启动第二次；旧 closed seq6 早于新 opened seq7 | node:a / 11 ms；node:b / 31 ms |
| 接纳前早退 | connector 87、91；writer 86、90 | 核心停止→子建连/转交/退出/reap→确认子 `/proc` 消失→恢复核心→writer 握手 | node:a / 12 ms；node:b / 31 ms |

父子并发证明两条已建立流同时存活，不证明内核回调同时运行。PID 复用仅在专用 guest 中写 ns_last_pid，实际 PID 必须为登记的 30000；没有修改宿主分配器。两个启动实例、socket cookie、调用编号均不同，旧事件没有用当前 PID 回填。

早退场景对自有核心 PID78 实际发送 SIGSTOP，经 WUNTRACED 得到停止信号，并两次核对全部八个 task 为停止状态。原子进程只建 TCP、保存独立 oracle、经 SCM_RIGHTS 发送 socket，然后退出；父 writer 在子进程回收、消失前后均不发送协议或应用数据。核心仍停止时，来源前缀没有这个 cookie 的 opened；SIGCONT 后才释放 writer。两协议最终 opened 都记录已经退出的原 child，包含原 start identity、exe 和 cookie，没有改写成 writer。该顺序补足了[旧早退记录](connector-core-earlyexit.md)未证明 accept/Admit 晚于原进程退出的限制。

六条流的 296 字节 exe 原记录均与各自独立 `/proc/self/exe` oracle 核对：路径 `/bin/python3`，device2/inode8，原调用编号与原 connector 一致。每条流发出 33 字节后 SHUT_WR，仍收回完整 33 字节和 EOF；两线路各自核对实际目标 token。每场 source_seq 连续 1–13，两个 closed、tail=0、最终 epoch complete、无 gap；核心 SIGTERM 退出0，自有客户端均退出0并回收。最终准入为 admitted2/rejected0/revoked=false，诊断四项在 ready/end 均 observed0。零诊断不证明完整连续性。

## 资源观察的口径

| 场景 | ready→end RSS / HWM（KiB） | CPU ticks 增量（CLK_TCK=100） |
| --- | --- | ---: |
| 父子并发 | 34160/34160 → 35540/35540 | 2 |
| PID 复用 | 33776/33776 → 35176/35176 | 1 |
| 接纳前早退 | 33704/34072 → 35060/35060 | 2 |

每次保留同一 core PID/start 的 `/proc/stat`、`status` 原文及 BOOTTIME 采样区间，另保存固定 20 个 BPF FD：9 map、6 program、5 link。四个身份关联 hash 的容量各为1024；map fdinfo 的 memlock 合计 809776 字节，是固定内核各 map 类型报告的用量，不含动态 task-storage 元素的全部成本，也不代表当前占用条数或全部内核物理内存。

上述是顺序读取的短窗口核心样本；累计 CPU 可能包括同 PID exec 前 loader，只有 ready→end 差值用于本窗口，早退窗口还包含 SIGSTOP。没有常驻独立 helper。Admit 内部等待无测量接口，记录为 unavailable；协议 ready 耗时不可代替它，也不能据此宣布 10 分钟空闲或整套性能预算通过。

## 保留的失败与剩余范围

首次 v1 在 `vm-family-concurrent-cqu0mox7` 启动失败：VM/extract=0，checker=1，guest 在调用未定义 `tcp_settings` 时停止，仅导出 readiness；核心未启动，没有流。静态核查另发现 `export_btf` 定义遗漏，但失败执行尚未到达它，不能称本次已观察到 BTF 导出。原镜像、源码、运行和失败矩阵均保留。

修复只补回两个只读 helper，并新增直接执行实际 guest.main 的假环境启动回归：旧副本复现失败，新三个模式都到达核心启动入口。v2 使用新镜像、新运行；原 157 项准备结果没有改写成 162 项。三个 v2 原始运行分别为 `vm-family-concurrent-r772ifpn`、`vm-pid-reuse-rb794q_n`、`vm-early-exit-before-admit-an9pyhww`。独立 scout 复核全部 77 份导出与三个 virtio 归档摘要，核对原始 STOP/退出顺序、启动身份、exe、线路及字节。

这些有限结果仍依赖固定内核、初始 namespace、受控 cgroup 和持续挂钩前提；[连续性判据](connector-continuity-review.md)及 cgroup 实际移除负例继续推进。正式 instance/name/path 均 false/unverified，T132/T137 未完成，139项/21完成、正式新 G01=0、旧0/100及 P0/P1 未放行不变。其他内核、平台、真实 SSH/休眠与性能预算仍未验证。
