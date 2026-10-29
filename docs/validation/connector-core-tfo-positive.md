# 新 TFO 检查核心的普通转发回归

2026-10-24，[0010候选](../../experiments/mihomo-events/core-tfo-candidate-manifest.json) SHA256 `e9dd96f3816bcc050bfcc472f8cd578bd4bf9a26f09923790df5481ebdadd9be` 完成一轮新的实际正向VM `vm-ordinary-halfclose-r0c0j7rj`。46项准备检查之后，主代理审查稳定工具、构建/启动VM并在完整提取后运行独立checker。见[契约](../../specs/001-hoopa-proxy-manager/contracts/connector-core-tfo-guard-v1.md)与机器记录（本地原件 `../../experiments/sourceidentity/core-tfo-positive-validation.json`，不随Git上传）。

| 客户端原PID | 协议 | 目标 | 实际线路 | 转发ready耗时 | 双向终末 |
| --- | --- | --- | --- | --- | --- |
| 84 | HTTP CONNECT | alpha.test | node:a | 11ms | 33 / 33B |
| 87 | SOCKS5 | beta.test | node:b | 31ms | 33 / 33B |

两条连接均先完成代理认证握手，再send33→SHUT_WR→读取完整33B与EOF。原PID/start/cookie与opened逐一核对，ready、线路binding及终末引用同一flow/attempt/实际连接。独立node/target保存各token的真实收发字节，不能以规则配置替代实际线路。名称观察仍未认证，旧process字段保持unknown。

本轮共13原始帧，严格decoder与只读source恢复核对两条closed33/33、无gap且未知尾部未伪造；两个客户端均exit0/reap，核心SIGTERM0，累计admitted2/rejected0/revokedfalse，结束live gauge0。21份原始导出重新计算摘要，kernel/BTF、11FD移交、独立证据通道和线程清理通过，无内核异常。

这验证了新检查没有破坏这两条普通early-half-close路径，没有在原accepted时刻直接观测其TCP state，也没有覆盖TFO拒绝。原始TCP_INFO对普通/TFO及CLOSE_WAIT的区分见[独立标志实验](connector-tfo-boundary.md)。旧001–009结果保留，不能移作此新hash的通过证据；TFO拒绝及其余T132要求仍须继续。正式能力、T132/T137、139/21、G01新增0与P0/P1门槛保持。

补充记录：同一0010候选的[独立TFO同四元组拒绝实际回归](connector-core-tfo-negative.md)已完成有限判据核对，见负向机器记录（本地原件 `../../experiments/sourceidentity/core-tfo-negative-validation.json`，不随Git上传）。该后续记录不扩大本页原实验的证明范围，也不识别具体内部拒绝分支。
