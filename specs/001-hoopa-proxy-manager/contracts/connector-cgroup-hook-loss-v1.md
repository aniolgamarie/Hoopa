# 核心仍持有 FD 时的 cgroup 挂钩移除 v1

2026-10-24，运行前登记。固定 001–013 核心 `476d57884b810e96d8b44367afbf91001bc63b6b516930a66b89d6e8dc05467b`、组合 BPF `1bf999df3affd167b06c923b7dde3ef27ec521e7e0afaf2accb3f447a6059500`，独立无网卡/宿主共享的 6.18.54.1/amd64 guest。区别于[raw/LSM 操作被拒](connector-capture-link-operations-v1.md)，本次真正解除一个 cgroup link，检验[连续性复核](../../../docs/validation/connector-continuity-review.md)识别的对象状态边界。不修改候选或原运行，不操作宿主资源。

两个模式分别独立 VM：`detach-sockops` 针对 `hoopa_lifecycle`，`detach-connect4` 针对 `hoopa_connect`。每场先完成 HTTP/alpha.test/node:a 的原始 connector/exe、ready、33/33、提前 SHUT_WR、完整 closed，保存原始六帧 prefix。status 为 admitted1/rejected0/revoked=false、gauge0，四个诊断计数 observed 零。

guest PID1 只针对自己仍持有的核心 Popen 调用 pidfd_open/getfd，复制该模式准确 handoff link FD。必须核对原 FD 的 ID、CLOEXEC 和实际 info：type=cgroup、link ID、匹配 program ID、root cgroup ID、对应 attach type。成功后以固定 bpf syscall 321、command34、4B linkfd attr 执行一次 BPF_LINK_DETACH，要求返回0/errno0。随后原 FD 与副本仍指向相同 link/program/type/attach type，而 info cgroup_id=0。仅关闭副本和 pidfd 各一次，原 FD 保留；不重新 attach、不改安全策略/map、不重试来改变分母。缺权限或不符合结果均保留失败。

再由受控 source 叶中的新原生客户端以不同本地端口、全新 cookie 发起普通目标 TCP connect。必须实际 connect 成功并记录原 PID/start/socket/exe oracle，但在发送任何 HTTP/SOCKS 或应用字节前观察 EOF/reset；普通超时不算拒绝通过。核心 status 最终应为 admitted1/rejected1/revoked=true，不能生成新 opened/route/实际节点或第二流。原第一流 prefix 原字节保持，核心正常 SIGTERM0 后来源恰为7帧完整 epoch、第一流 final33/33/tail0；目标/节点仅有第一流 token 的字节。客户端退出并 reap，核心及临时 FD 清理均有界。

`detach-sockops` 之后 connect4/同调用 exe gate 仍在；缺的是 active/passive 绑定路径，capture_faults 可以保持0，本次登记期望四个诊断仍为 observed零，拒绝由 link 身份条件触发。`detach-connect4` 之后原 connect 记录缺失；若实际建立回调运行，capture_faults 必须 observed非零，其他三个计数仍零。记录实际值而不规定恰好两次；不能将 sockops 返回值说成直接拒绝 TCP。Owner 在关联 map 读取前核验 cgroup link，外部结果不单独证明内部 map lookup 的具体失败分支。

该实验验证显式移除整个挂钩后的拒绝，不声称只漏一个 active 回调、kernel所有分支连续、任意特权对抗、恢复可用性或正式身份能力。当前诊断接口仅报告特定计数，零值不能替代 link 核验；不因故障实验顺手自动修复。正式新 G01 样本0、旧0/100、T132/T137和P0/P1门槛保持。
