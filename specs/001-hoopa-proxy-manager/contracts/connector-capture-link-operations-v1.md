# 固定采集 link 的 detach/update 操作边界 v1

2026-10-24，运行前登记。使用固定 001–013 核心 `476d57884b810e96d8b44367afbf91001bc63b6b516930a66b89d6e8dc05467b`、组合对象 `1bf999df3affd167b06c923b7dde3ef27ec521e7e0afaf2accb3f447a6059500` 和隔离 6.18.54.1/amd64 guest。固定内核源码的 raw tracepoint 和 tracing/LSM link ops 没有 detach/update 回调；本轮检查实际 API 结果与前后转发，不能据此宣称抵抗所有特权干预或完整 hook 连续性。

一个实际核心先完成 HTTP→node:a 的33/33提前SHUT_WR基线。之后仅由启动并持有该 Popen 的 guest PID1，对仍存活的核心执行以下有限测试：

1. `pidfd_open(core_pid, 0)`，记录真实 pidfd fdinfo、目标 PID 与原 exec intent 相符；`pidfd_getfd(pidfd, target_fd, 0)`只复制原 handoff 中 `call_enter`、`call_exit`、`capture_exe` 的三个 link 及三个对应 program FD。记录真实 credentials/CapEff、Yama观察，不更改权限或安全策略。
2. 对每个副本先 `BPF_OBJ_GET_INFO_BY_FD`，核对其类型、非零 ID、确切 program name、link所指program ID与原核心 fdinfo/移交一致。副本必须CLOEXEC。不能把无效FD、错误类型或错误参数导致的拒绝当通过。
3. 每个 link 执行 `BPF_LINK_DETACH`，attr仅4字节link_fd，预期`-1/EOPNOTSUPP(95)`；然后执行 `BPF_LINK_UPDATE`，attr为16字节link_fd/new_prog_fd/flags=0/old_prog_fd=0，使用对应真实program副本，预期`-1/EINVAL(22)`。固定x86_64 syscall bpf=321、pidfd_open=434、pidfd_getfd=438；BPF命令detach=34、update=29、get_info=15。保存准确参数、原生返回/errno及原info字节。
4. 逐项再次读取info确认身份没有变化；只关闭实验副本和pidfd，各关闭一次，保留cleanup结果。原核心FD不关闭、不替换；禁止 map update、额外attach、修改sysctl/security设置或宿主状态。若复制/查询失败或detach/update意外成功，保留失败并停止该场，不放宽为通过。
5. 关闭副本后再完成 SOCKS5→node:b 的33/33提前SHUT_WR流。前后两流均用v4严格检查原PID/TID/start/cookie、296B exe记录、同调用token/BOOT区间、实际node/ready/终末。最终一个完整13帧epoch、两个closed flow/tail=0、核心SIGTERM0；admission 2/0/false。两流之间的操作阶段前后旧status逐项相等，诊断四计数均observed/"0"且前后相等，诊断不能触发修复。

本轮特意由具有相应权限的测试父进程暂时增加FD引用，这不是生产FD移交方案，也不能宣称测试期间核心是唯一用户态引用者。它验证这些API不能按此调用形态移除/替换已持有的hook；没有实际移除hook、注入raw recursion skip，或检验ptrace关闭原FD等其他干预。errno与已核对源码路径一致是有限证据，不冒充内核分支跟踪。

权限拒绝、缺失功能、timeout、其他errno、错误对象、源帧不全均为失败/未验证。离线准备与真实VM分别记录，旧run保持；正式身份能力false/unverified、G01新增0、T132/T137及P0/P1门槛不变。
