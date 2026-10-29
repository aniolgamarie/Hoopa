# 原建连进程退出后的连接归属实测

2026-10-24，按[早退契约](../../specs/001-hoopa-proxy-manager/contracts/connector-core-earlyexit-v1.md)，85 项离线工具检查后执行真实隔离 VM `private/t132-core-earlyexit/vm-early-exit-lequ0fo8`。固定核心/BPF 不变，**1 VM / 2 流 / 14 帧** 全部满足本批有限判据。输入、源码和原始文件摘要见机器记录（本地原件 `../../experiments/sourceidentity/core-earlyexit-validation.json`，不随Git上传），22 份导出文件已再次核验。

| 原建连子进程 | 继续使用 socket 的父进程 | 原 client/accepted cookie | 实际线路和终末 |
| --- | --- | --- | --- |
| PID 83 | PID 82 | 2 / 3 | HTTP → node:a，ready 11 ms，双向各 33 B |
| PID 87 | PID 86 | 4097 / 4098 | SOCKS5 → node:b，ready 31 ms，双向各 33 B |

子进程自行 connect，只经 SCM_RIGHTS 转交一个原 TCP FD，关闭自己的 FD 并正常退出。父进程核对收到的 cookie/netns/端点、wait/reap 自有子进程退出码 0，并确认原 `/proc` 目录消失后，才进行代理握手和负载收发。原始 v3 connector 仍为子进程 PID、leader 启动实例和原 cookie；writer 单列父进程，没有查找当前 socket 持有者来重写归属。

每流独立 token 的完整负载与 node/target 记录一致，同一 flow 的实际 connection/attempt、ready、连接段和整数 closed 全部核对。两入口最终都经受控 HTTP 上游，延迟是已证 CONNECT ready 耗时，包含夹具延迟，不是业务 TTFB。重开投影保留精确 final；退出后的名称没有伪造观察值。

这证明原 connector 在协议握手前退出仍可保留归属；**不证明核心 Admit 查询发生在其退出之后**，也不升级名称一致性、任意 FD 转交监测或产品界面能力。后续按[namespace 契约](../../specs/001-hoopa-proxy-manager/contracts/connector-core-namespaces-v1.md)验证范围拒绝，并继续名称/tuple/连续性矩阵。T132/T137、139 项/21 完成、正式 G01 新样本 0、旧 0/100 与 P0/P1 门槛保持。
