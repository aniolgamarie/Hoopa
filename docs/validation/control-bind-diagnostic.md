# T136 回滚端口绑定诊断

2026-10-25。独立隔离 VM `vm-rollback-hk8xc710` 完成诊断，保存55个导出、3个来源共9帧。它保留 HTTP503/degraded，不是回滚成功，也不计作 T136 通过。机器记录（本地原件 `../../experiments/mihomo-events/control-bind-diagnostic.json`，不随Git上传）绑定原始输入摘要；此前[完整替换、身份拒绝及回滚失败](control-replace.md)不改判。

## 原始实例与 TCP 状态

原核心、失败目标、失败恢复实例的 PID/start 分别为89/447、106/614、116/722。每份 helper claim 在探测前先绑定原始 intent 摘要、规范 assets 摘要、当前 boot 及启动身份，再观察原 PID 消失；已有 parent instance 要求精确相等。guest 没有猜测 PID 发信号，也没有把进程消失当成观察到核心退出码。

旧应用仍保持 socket 打开时，原入口127.0.0.1:18993为 FIN_WAIT2，反向客户端127.0.0.1:51710为 CLOSE_WAIT；探测前后完整 TCP 表一致。控制端口18994另有 TIME_WAIT，不能与入口四元组混为一条连接。

| 自有 bind 实验 | 监听尚存时 | server accepted 与 listener 先关闭、client 读到 EOF 但仍打开后 |
| --- | --- | --- |
| 原入口18993，新 socket reuseaddr 分别0和1 | 未重新建立监听 | 两次 errno98（EADDRINUSE），均未调用 listen |
| 独立21093，旧 listener reuseaddr0，新 socket reuseaddr1 | errno98 | errno98 |
| 独立21094，旧 listener reuseaddr1，新 socket reuseaddr1 | errno98 | errno0，成功 bind 后关闭，未调用 listen |

所有对照均记录 SO_REUSEPORT=0，未修改 sysctl；自有 socket 最终全部关闭。原应用在探测后明确关闭，管理器正常结束。原来源7帧、一条11/11 B应用流；失败目标和失败恢复各1帧、零应用流。三来源均保留原始材料并由既有严格解码/恢复工具核对；不以来源完整推导回滚成功。

## 结论与验证边界

固定内核源码与这组实际对照支持“旧非复用 accepted socket 状态阻止立即重绑”的原因假设；原失败只有通用受保护配置拒绝，不能据诊断唯一确定此前503的内部原因。SO_REUSEADDR也不提供未listen阶段的独占预留：两个可复用预留可能共存，真正监听仍须处理失败；本批没有验证新的核心候选或修改后的替换事务。

独立 scout 已复核55份导出、归档、原始TCP状态及复用结果。本文整理时再次只读重算全部导出/归档及9帧边界与摘要，没有重跑 VM、checker 或网络测试。0016候选的构建与实际验收另行记录，编译成功不能写成测试通过。

[控制契约](../../specs/001-hoopa-proxy-manager/contracts/control-probe-v1.md)与[接管原型](control-probe.md)的其余边界保持。旧失败、0015制品及T137记录保留；T136未勾选，139项完成23项，正式G01新增0，旧0/100、P0/P1未放行保持。本批不证明其他内核、生产部署或一般并发预留保证。
