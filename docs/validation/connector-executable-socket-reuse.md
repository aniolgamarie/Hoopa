# 同一 socket 复用旧快照的真实反例

2026-10-24，45 项聚焦离线检查后，在一次新 VM `vm-executable-socket-reuse-z17lyowj` 中复现了原可执行文件对象的设计缺口。实验满足[反例契约](../../specs/001-hoopa-proxy-manager/contracts/connector-executable-socket-reuse-v1.md)，并不表示快照方案可以发布。见机器记录（本地原件 `../../experiments/sourceidentity/executable-socket-reuse-validation.json`，不随Git上传）。

首次普通 connect 的原 socket cookie=1，LSM 记录 `/bin/python3`，gate 允许后在无 listener 端点得到 ECONNREFUSED。随后实际解除 LSM link，将同一个运行文件改名为 `/bin/hoopa-exe-retry`，在原 FD 上以 AF_UNSPEC 重置 TCP。重置返回 0、errno/SO_ERROR=0、TCP_INFO=TCP_CLOSE，cookie 仍为 1，状态与记录均未变化。

| 目标 connect | 原 cookie | 实际结果 | captures / connects / allowed / refused / fault |
| --- | --- | --- | --- |
| 初次连接 | 1 | ECONNREFUSED | 1 / 1 / 1 / 0 / 0 |
| 原 FD 重试 | 1 | ECONNREFUSED | 1 / 2 / 2 / 0 / 0 |
| 新 socket 对照 | 4097 | EPERM | 1 / 3 / 2 / 1 / 0 |

第二次仍被允许，但原始记录逐字节保持旧路径，独立 readlink 已为新路径，记录时刻也早于这次 connect。新 socket 缺快照时能拒绝，无法据此推断同 cookie 重试也安全。原两个独立实验都明确使用新 socket，其有限结果仍保留；本反例补出了尚未覆盖的范围。

所有操作均在隔离 guest；程序、map/FD、link/BTF、同 inode/dev 和文件名恢复、两个 socket 关闭及其他清理核对通过，三份导出文件摘要重验。没有代理数据转发、正式 G01 新样本或核心接线。

后续按[消费修复契约](../../specs/001-hoopa-proxy-manager/contracts/connector-executable-consumption-v1.md)完成[两次独立 VM 验证](connector-executable-consumption.md)：新对象拒绝已消费 cookie 重试，并保留新 cookie 正常路径。这项修复只关闭已消费记录重用；“已经捕获但未到达 gate”的旧记录、其他旁路和完整挂接时序仍需独立处理，不能从一条 CAS 宣称全部新鲜性问题解决。
