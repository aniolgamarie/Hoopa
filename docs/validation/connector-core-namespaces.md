# PID 与网络命名空间的范围拒绝实测

2026-10-24，按[预登记契约](../../specs/001-hoopa-proxy-manager/contracts/connector-core-namespaces-v1.md)，同一固定核心及 BPF 完成两次隔离 VM 检查。范围外的原始 TCP `connect` 均收到 `EPERM`，应用写入、核心 accepted/admitted、flow 和实际 node/target 连接全部为零。两份来源各只有一帧 epoch 记录。见机器记录（本地原件 `../../experiments/sourceidentity/core-namespaces-validation.json`，不随Git上传）。

| 场景 | 实际进程与 namespace 证据 | 结果 |
| --- | --- | --- |
| PID namespace | 初始 PID 85，内部 PID 1，NSpid `[85,1]`；原网络 namespace | connect errno 1，0 应用字节 |
| network namespace | 初始/可见 PID 86；独立 net inode `4026532154`、socket netns cookie `4099` | connect errno 1，0 应用字节 |

首轮通过普通串口导出大份 BTF 时，内核日志插入 base64，且出现 watchdog soft lockup。尽管 QEMU 退出码为 0，严格提取器仍拒绝材料；该轮保存为失败，没有删除日志后改判。之后只修改证据运输：普通 UART 保存启动/内核日志，独立 virtio console 导出材料。V2 的 117 项离线检查后，两次新 VM 完整导出、摘要和判据检查均通过，启动日志没有相同内核异常。V1 的 86 项检查与 V2 存在重叠，不累计为唯一测试数。

两轮成功运行共 34 份导出文件均再次核对大小与 SHA256；从实际 guest 只读导出的 BTF 均为 6,018,902 字节，SHA256 `1c1326be5d6ca0e60ad8790971b6765bba91cc8a3ab897a94794177484c8c9b9`。BTF 仅作为后续函数挂接研究的环境材料，不是名称字段证明。内核 fault map 计数没有另行观察，不以本轮拒绝结果声称已核验其数值。

这关闭了当前受限拓扑的两项范围拒绝子步骤，不代表支持跨 namespace。三个实际 VM 中保留一轮失败、两轮有限通过；没有新增正向身份流或正式 G01 样本。T132/T137、139 项中完成 21 项、旧 G01 0/100 与 P0/P1 门槛不变；继续名称一致性、tuple 复用、覆盖连续性及缺失/冲突矩阵。
