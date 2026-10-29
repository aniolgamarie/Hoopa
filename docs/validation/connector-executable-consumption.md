# 已消费快照重用的有限修复

2026-10-24，新增 `consumed` 原子标记的对象在两个独立 VM 中通过[预登记契约](../../specs/001-hoopa-proxy-manager/contracts/connector-executable-consumption-v1.md)。53 项离线检查覆盖原始记录布局、旧 profile 拒绝、错误消费标记及正常路径；随后主代理实际构建、运行、提取并独立核验两轮。见机器记录（本地原件 `../../experiments/sourceidentity/executable-consumption-validation.json`，不随Git上传）。

原型保留 288B 布局，将 offset20 原保留字段定义为新 profile 的 `consumed`。gate 在 cookie、PID/TID、路径结果与故障状态核验后，只有原子 CAS 0→1 成功才允许连接。新对象 SHA256 为 `feda13ebad10bd057b5e58bf0eb2e33ebeb3afdddfd71bf2525c3acd36c837dd`；不能拿旧 reserved=0 对象或旧记录通过新检查器。

| 实际 VM | 阶段 | connect errno | 累计 captures / connects / allowed / refused / fault |
| --- | --- | --- | --- |
| `vm-consumed-retry-yycbzupo` | 初次 cookie1 | 111 ECONNREFUSED | 1 / 1 / 1 / 0 / 0 |
| 同上 | 解除 LSM、改名、原 FD AF_UNSPEC 后重试 cookie1 | 1 EPERM | 1 / 2 / 1 / 1 / 0 |
| 同上 | 新 socket cookie4097 | 1 EPERM | 1 / 3 / 1 / 2 / 0 |
| `vm-consumed-fresh-9589smd2` | 仅 gate、cookie1 | 1 EPERM | 0 / 1 / 0 / 1 / 0 |
| 同上 | LSM 已挂接、cookie2 | 111 ECONNREFUSED | 1 / 2 / 1 / 1 / 0 |
| 同上 | comm 改名、新 cookie3 | 111 ECONNREFUSED | 2 / 3 / 2 / 1 / 0 |
| 同上 | 解除 LSM、新 cookie4097 | 1 EPERM | 2 / 4 / 2 / 2 / 0 |

第一轮复用与[旧对象反例](connector-executable-socket-reuse.md)相同的原 FD/同 cookie 时序。旧记录消费标记保持 1、原路径不改写，新的真实路径观察不同，重试被拒绝。第二轮证明正常新 cookie 的两条路径仍可消费，comm 改名不改变 exe 路径。目标无 listener，ECONNREFUSED 仅表示经过 gate 后 TCP 拒绝，并不是代理转发成功。

三份导出文件各自重新计算摘要，共六份；程序/link/BTF、map 属性与 FD 权限、记录原文、状态、文件/comm 恢复和 FD 清理均核对。两轮没有观察到内核异常。旧失败与旧对象原始记录未修改。

这只修复**成功消费过的快照再次使用**。已采集但未经过 gate 的记录仍为 0；后续连接如果缺少采集，单靠 CAS 不能证明该记录来自本次尝试。[后续独立反例](connector-executable-unconsumed.md)已经实际复现此分支。LSM 及 cgroup 连续性、本次调用的新鲜性、并发与其他连接路径仍需继续处理。没有接入 Mihomo，没有升级 name/path 或正式身份能力，139/21、T132/T137、旧 G01 0/100、正式新增 0 和 P0/P1 门槛保持。
