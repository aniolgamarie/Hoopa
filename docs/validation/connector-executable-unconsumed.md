# 未消费快照仍可被后一次 connect 使用

2026-10-24，consumed对象的进一步反例已实际观察到。最终新VM `vm-executable-unconsumed-s8p_otln` 满足[v2契约](../../specs/001-hoopa-proxy-manager/contracts/connector-executable-unconsumed-v2.md)，前一失败单独保留。见机器记录（本地原件 `../../experiments/sourceidentity/executable-unconsumed-validation.json`，不随Git上传）。

本批先在挂接BPF之前建立原client/listener/accepted TCP，保存双方ESTABLISHED、cookie/端点，无应用字节。首次采用SO_SNDTIMEO3秒的实际blocking connect，避免Python非阻塞连接包装影响后续EISCONN。随后挂接LSM与gate、确认空表和零计数。

| 步骤 | cookie | errno | consumed | captures / connects / allowed / refused / fault |
| --- | --- | --- | --- | --- |
| 对已建立原FD再次connect | 1 | 106 EISCONN | 0 | 1 / 0 / 0 / 0 / 0 |
| 解除LSM、改名、AF_UNSPEC并关闭旧listener后原FD重连 | 1 | 111 ECONNREFUSED | 1 | 1 / 1 / 1 / 0 / 0 |
| 新socket对照 | 3 | 1 EPERM | 无记录 | 1 / 2 / 1 / 1 / 0 |

第一步已采集 `/bin/python3`，但TCP返回EISCONN，没有经过gate。改名后独立readlink为 `/bin/hoopa-exe-unconsumed`，同dev/inode；后续重连却消费了旧快照。原288B记录除offset20从0变1，其余字节完全保持，包括旧路径/捕获时刻。目标无listener，111只说明通过gate后TCP拒绝，不代表代理转发。

AF_UNSPEC返回0、errno0、cookie1保持、TCP_CLOSE；首次SO_ERROR104，随后读取为0。旧accepted/listener均关闭，原TCP/TCP6表未出现目标listener，原文件名及所有自有FD/link/map清理核对。最终准备检查45项，实际新run三份导出摘要重验，无内核异常。

## 保留的失败与设计结论

首run `vm-executable-unconsumed-zngikp6d` 已观察到EISCONN留下consumed0，但夹具错误要求已建立连接AF_UNSPEC后的SO_ERROR0而停止；当时实际为104，**没有执行重连**。固定源码 `tcp_disconnect` 解释了该ECONNRESET行为。原run/image/契约/checker及三份导出保留；新v2明确104→读取清除0之后才重连，原失败不转换成通过。VM前另修正了首次connect的非阻塞前提及工具期望同步，准备失败不当作新VM。

这证明[一次消费修复](connector-executable-consumption.md)只能解决已消费记录重用，不能单独认证本次调用的新鲜性。仅再加“拒绝对已连接socket捕获”的特判也不足以证明所有未消费路径。后续研究将采集与检查绑定同一次系统调用标识，并独立验证该标识的覆盖、生命周期及不支持路径；它当前只是待验证方案。

两个实验均未接核心或提供正式名称能力。139/21、T132/T137、正式G01新增0/旧0/100和P0/P1门槛保持。
