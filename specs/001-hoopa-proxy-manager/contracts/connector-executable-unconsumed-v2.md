# 未消费记录反例 v2：已建立连接断开错误的口径

2026-10-24，使用新VM登记；[v1](connector-executable-unconsumed-v1.md)及原run `vm-executable-unconsumed-zngikp6d` 保留为失败，不重新解释为完整反例通过。

首轮已经实际观察blocking首次建立、重复connect返回EISCONN106、LSM留下consumed0、累计1/0/0/0/0。它在AF_UNSPEC后因夹具要求SO_ERROR0而停止，**没有执行后续重连**。实际AF_UNSPEC返回0、errno0、原cookie不变、TCP_CLOSE，但首次SO_ERROR为104。

固定源码 `net/ipv4/tcp.c` 的 `tcp_disconnect` 对需要reset的原ESTABLISHED状态设置ECONNRESET；先前来自已CLOSED状态的实验不能作为本情形的SO_ERROR预期。本v2只更正这项已证明的夹具前提：AF_UNSPEC返回0/errno0、cookie不变/TCP_CLOSE，首次SO_ERROR必须104，紧接着再次读取必须0，明确记录错误已被读取清除。其余v1的EISCONN106→重连111→新socket1、record仅consumed0→1、1/0/0/0/0→1/1/1/0/0→1/2/1/1/0，以及原文件/FD/记录/导出完整性判据不变。

新工具、probe与image分别固定摘要；host检查器拒绝首轮旧profile材料。另修正工具输出中的歧义字段，以 `unconsumed_bypass_observed=true` 与 `unconsumed_safety_proved=false` 表达预期设计缺口。准备检查首次缺少runner同步的104/0期望已在VM前修正；准备失败与实际运行失败分开计数。

本实验即使观察到反例也不代表快照方案安全。无核心接线、正式身份能力或G01新样本，T132/T137门槛不变。
